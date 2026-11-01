package httpdebug

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const maxWireBytes = 32 * 1024 * 1024
const maxWireFlows = 128

// A private bounded journal retains original IP packets and observed secrets.
// Export converts it to standard pcapng (RAW IP + TLS Decryption Secrets
// Blocks) or NSS key-log text. No synthetic TCP/TLS packets are generated.
type wireRecord struct {
	Type   string `json:"type"`
	Time   int64  `json:"time,omitempty"`
	Length int    `json:"length,omitempty"`
	Data   []byte `json:"data,omitempty"`
	Label  string `json:"label,omitempty"`
	Random []byte `json:"random,omitempty"`
	Secret []byte `json:"secret,omitempty"`
}
type wireFlow struct {
	bytes, packets, secrets int64
	at                      time.Time
	keys                    map[string]string
}
type wireState struct {
	Bytes   int64  `json:"bytes"`
	Packets int64  `json:"packets"`
	Secrets int64  `json:"secrets"`
	Error   string `json:"error,omitempty"`
}
type wireStore struct {
	mu           sync.Mutex
	directory    string
	flows        map[string]*wireFlow
	bytes, limit int64
	warning      string
}

func wireID(id string) string    { sum := sha256.Sum256([]byte(id)); return hex.EncodeToString(sum[:]) }
func validWireID(id string) bool { _, err := hex.DecodeString(id); return len(id) == 64 && err == nil }

func openWireStore(directory string) (*wireStore, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	w := &wireStore{directory: directory, flows: make(map[string]*wireFlow), limit: maxWireBytes}
	if file, err := os.Open(filepath.Join(directory, "warning.txt")); err == nil {
		data, _ := io.ReadAll(io.LimitReader(file, 4096))
		file.Close()
		w.warning = string(data)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		id := strings.TrimSuffix(entry.Name(), ".wire")
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".wire") || !validWireID(id) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		flow := &wireFlow{at: info.ModTime(), keys: make(map[string]string)}
		path := filepath.Join(directory, entry.Name())
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		reader := bufio.NewReaderSize(io.LimitReader(file, maxWireBytes+1), 64*1024)
		for {
			line, readErr := reader.ReadSlice('\n')
			if readErr != nil {
				break
			}
			var record wireRecord
			if json.Unmarshal(line, &record) != nil || record.validate() != nil {
				break
			}
			if record.Type == "packet" {
				flow.packets++
			} else {
				key, value := record.key()
				if prior, exists := flow.keys[key]; exists && prior != value {
					break
				}
				flow.keys[key] = value
				flow.secrets++
			}
			flow.bytes += int64(len(line))
		}
		file.Close()
		if flow.bytes != info.Size() {
			if err := os.Truncate(path, flow.bytes); err != nil {
				return nil, err
			}
			w.warnLocked("Raw capture journal was interrupted; only the valid prefix was retained")
		}
		w.flows[id] = flow
		w.bytes += flow.bytes
		if err := w.trimLocked(); err != nil {
			return nil, err
		}
	}
	return w, nil
}

func (r wireRecord) validate() error {
	switch r.Type {
	case "packet":
		if r.Time <= 0 || len(r.Data) == 0 || len(r.Data) > 32768 || r.Length < len(r.Data) || r.Length > 65575 ||
			(r.Data[0]>>4 != 4 && r.Data[0]>>4 != 6) {
			return errors.New("invalid raw IP packet")
		}
	case "secret":
		if len(r.Random) != 32 {
			return errors.New("invalid TLS client random length")
		}
		switch r.Label {
		case "CLIENT_RANDOM":
			if len(r.Secret) != 48 {
				return errors.New("invalid TLS 1.2 master secret length")
			}
		case "CLIENT_HANDSHAKE_TRAFFIC_SECRET", "SERVER_HANDSHAKE_TRAFFIC_SECRET", "CLIENT_TRAFFIC_SECRET_0", "SERVER_TRAFFIC_SECRET_0", "EXPORTER_SECRET", "EARLY_EXPORTER_SECRET", "CLIENT_EARLY_TRAFFIC_SECRET":
			if len(r.Secret) != 32 && len(r.Secret) != 48 {
				return errors.New("invalid TLS 1.3 secret length")
			}
		default:
			return errors.New("unsupported TLS key-log label")
		}
	default:
		return errors.New("invalid raw capture record")
	}
	return nil
}
func (r wireRecord) key() (string, string) {
	return r.Label + " " + hex.EncodeToString(r.Random), hex.EncodeToString(r.Secret)
}
func (r wireRecord) keyLine() string { key, value := r.key(); return key + " " + value + "\n" }

func (w *wireStore) append(id string, record wireRecord) error {
	if id == "" || len(id) > 96 {
		return errors.New("invalid raw capture connection")
	}
	if err := record.validate(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	id = wireID(id)
	flow := w.flows[id]
	if flow == nil {
		flow = &wireFlow{at: time.Now(), keys: make(map[string]string)}
		w.flows[id] = flow
	}
	if record.Type == "secret" {
		key, value := record.key()
		if previous, exists := flow.keys[key]; exists {
			if previous != value {
				return errors.New("conflicting TLS secrets for one connection")
			}
			return nil
		}
		if len(flow.keys) >= 32 {
			return errors.New("TLS key limit reached for one connection")
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(w.directory, id+".wire")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	n, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil || n != len(data) {
		_ = os.Truncate(path, flow.bytes)
		return errors.Join(writeErr, closeErr, io.ErrShortWrite)
	}
	flow.bytes += int64(n)
	w.bytes += int64(n)
	if record.Type == "packet" {
		flow.packets++
	} else {
		key, value := record.key()
		flow.keys[key] = value
		flow.secrets++
	}
	return w.trimLocked()
}

func (w *wireStore) trimLocked() error {
	for len(w.flows) > maxWireFlows || w.bytes > w.limit {
		oldest := ""
		for id, flow := range w.flows {
			if oldest == "" || flow.at.Before(w.flows[oldest].at) {
				oldest = id
			}
		}
		if oldest == "" {
			break
		}
		if err := os.Remove(filepath.Join(w.directory, oldest+".wire")); err != nil && !errors.Is(err, os.ErrNotExist) {
			w.warnLocked("Unable to remove expired raw capture: " + err.Error())
			return fmt.Errorf("enforce raw capture retention: %w", err)
		}
		w.bytes -= w.flows[oldest].bytes
		delete(w.flows, oldest)
		w.warnLocked("Raw capture retention limit reached; earlier packets or TLS keys were removed")
	}
	return nil
}
func (w *wireStore) warnLocked(message string) {
	message = strings.ToValidUTF8(message, "�")
	if len(message) > 4096 {
		message = strings.ToValidUTF8(message[:4096], "")
	}
	if w.warning == message {
		return
	}
	w.warning = message
	path := filepath.Join(w.directory, "warning.txt")
	if err := os.WriteFile(path+".tmp", []byte(message), 0o600); err == nil {
		_ = os.Rename(path+".tmp", path)
	}
}
func (w *wireStore) warn(message string) { w.mu.Lock(); defer w.mu.Unlock(); w.warnLocked(message) }
func (w *wireStore) state() wireState {
	w.mu.Lock()
	defer w.mu.Unlock()
	state := wireState{Bytes: w.bytes, Error: w.warning}
	for _, flow := range w.flows {
		state.Packets += flow.packets
		state.Secrets += flow.secrets
	}
	return state
}
func (w *wireStore) clear() {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id, flow := range w.flows {
		if err := os.Remove(filepath.Join(w.directory, id+".wire")); err == nil || errors.Is(err, os.ErrNotExist) {
			w.bytes -= flow.bytes
			delete(w.flows, id)
		}
	}
	w.warning = ""
	_ = os.Remove(filepath.Join(w.directory, "warning.txt"))
	_ = os.Remove(filepath.Join(w.directory, "warning.txt.tmp"))
	if len(w.flows) > 0 {
		w.warnLocked("Some raw capture files could not be deleted")
	}
}

func pcapBlock(kind uint32, body []byte) []byte {
	length := (len(body) + 3) &^ 3
	data := make([]byte, length+12)
	binary.LittleEndian.PutUint32(data, kind)
	binary.LittleEndian.PutUint32(data[4:], uint32(len(data)))
	copy(data[8:], body)
	binary.LittleEndian.PutUint32(data[len(data)-4:], uint32(len(data)))
	return data
}
func pcapOption(code uint16, data []byte) []byte {
	option := make([]byte, 4+(len(data)+3)&^3)
	binary.LittleEndian.PutUint16(option, code)
	binary.LittleEndian.PutUint16(option[2:], uint16(len(data)))
	copy(option[4:], data)
	return option
}
func pcapHeader(comment string) []byte {
	body := make([]byte, 16)
	binary.LittleEndian.PutUint32(body, 0x1a2b3c4d)
	binary.LittleEndian.PutUint16(body[4:], 1)
	binary.LittleEndian.PutUint64(body[8:], ^uint64(0))
	body = append(body, pcapOption(1, []byte(comment))...)
	body = append(body, 0, 0, 0, 0)
	result := pcapBlock(0x0a0d0d0a, body)
	body = make([]byte, 8)
	binary.LittleEndian.PutUint16(body, 101) // LINKTYPE_RAW
	binary.LittleEndian.PutUint32(body[4:], 32768)
	body = append(body, pcapOption(9, []byte{9})...) // if_tsresol: nanoseconds
	body = append(body, 0, 0, 0, 0)
	return append(result, pcapBlock(1, body)...)
}
func pcapSecrets(line []byte) []byte {
	body := make([]byte, 8)
	binary.LittleEndian.PutUint32(body, 0x544c534b) // TLSK
	binary.LittleEndian.PutUint32(body[4:], uint32(len(line)))
	return pcapBlock(10, append(body, line...))
}
func (r wireRecord) pcap() []byte {
	body := make([]byte, 20)
	binary.LittleEndian.PutUint32(body[4:], uint32(uint64(r.Time)>>32))
	binary.LittleEndian.PutUint32(body[8:], uint32(r.Time))
	binary.LittleEndian.PutUint32(body[12:], uint32(len(r.Data)))
	binary.LittleEndian.PutUint32(body[16:], uint32(r.Length))
	return pcapBlock(6, append(body, r.Data...))
}

// A nonempty query selects complete TLS connections of matching HTTP records.
// TLS multiplexing cannot isolate an encrypted packet to one HTTP request.
func (s *Store) WriteWire(output io.Writer, format, query string) error {
	if format != "pcapng" && format != "keys" {
		return errors.New("unknown raw capture export format")
	}
	var selected map[string]bool
	if strings.TrimSpace(query) != "" {
		selected = make(map[string]bool)
		for _, item := range s.Page(MaxEntries, query).Items {
			if detail, err := s.Detail(item.ID); err == nil && detail.Metadata.CaptureID != "" {
				selected[wireID(detail.Metadata.CaptureID)] = true
			}
		}
	}
	w := s.wire
	w.mu.Lock()
	defer w.mu.Unlock()
	var ids []string
	var count int64
	for id, flow := range w.flows {
		if selected != nil && !selected[id] {
			continue
		}
		ids = append(ids, id)
		if format == "keys" {
			count += flow.secrets
		} else {
			count += flow.packets
		}
	}
	if count == 0 {
		return errors.New("No matching raw packets or TLS keys have been saved")
	}
	sort.Strings(ids)
	for _, id := range ids {
		if format == "pcapng" {
			comment := "B-box: original IP packets for one TLS connection; may include other HTTP/2 requests on that connection. Capture can start after TCP establishment."
			if w.warning != "" {
				comment += " " + w.warning
			}
			if _, err := output.Write(pcapHeader(comment)); err != nil {
				return err
			}
			// Perf events from different CPUs can deliver a secret after a
			// packet that needs it. Place every retained key before this
			// connection's packets so a single-pass reader can decrypt them.
			var keys []string
			for key := range w.flows[id].keys {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if _, err := output.Write(pcapSecrets([]byte(key + " " + w.flows[id].keys[key] + "\n"))); err != nil {
					return err
				}
			}
		}
		file, err := os.Open(filepath.Join(w.directory, id+".wire"))
		if err != nil {
			return err
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 8192), 64*1024)
		for scanner.Scan() {
			var record wireRecord
			if err = json.Unmarshal(scanner.Bytes(), &record); err == nil {
				err = record.validate()
			}
			if err != nil {
				break
			}
			if format == "pcapng" && record.Type == "packet" {
				_, err = output.Write(record.pcap())
			} else if format == "keys" && record.Type == "secret" {
				_, err = io.WriteString(output, record.keyLine())
			}
			if err != nil {
				break
			}
		}
		if err == nil {
			err = scanner.Err()
		}
		file.Close()
		if err != nil {
			return fmt.Errorf("export raw capture: %w", err)
		}
	}
	return nil
}
