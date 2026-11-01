package httpdebug

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The fixture artifacts come from the same SHA's real Go TLS clients on
// amd64/arm64. Exercise the production journal/exporter with their original
// packets and observed secrets, then have an independent reader decrypt them.
func TestWireIndependentDecryption(t *testing.T) {
	directory := os.Getenv("BBOX_WIRE_FIXTURE_DIR")
	if directory == "" {
		t.Skip("same-run Go TLS fixtures are supplied by CI")
	}
	var fixtures []string
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".capture.json") {
			fixtures = append(fixtures, path)
		}
		return nil
	})
	if err != nil || len(fixtures) != 24 {
		t.Fatalf("expected 2 architectures x 3 Go versions x 2 link modes x 2 TLS versions: %d fixtures, %v", len(fixtures), err)
	}
	for _, path := range fixtures {
		relative, _ := filepath.Rel(directory, path)
		t.Run(relative, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Port   int              `json:"port"`
				URIs   []string         `json:"uris"`
				Events []plaintextEvent `json:"events"`
			}
			if err := json.Unmarshal(data, &fixture); err != nil || fixture.Port < 1 || fixture.Port > 65535 || len(fixture.URIs) == 0 {
				t.Fatal("invalid real TLS fixture", err)
			}
			engine, store := testEngine(t, Config{HTTPS: true, CaptureNetwork: true, CaptureTLSKeys: true})
			for _, event := range fixture.Events {
				if err := engine.acceptWire(event); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			if err := store.WriteWire(&output, "pcapng", ""); err != nil {
				t.Fatal(err)
			}
			pcap := filepath.Join(t.TempDir(), "export.pcapng")
			if err := os.WriteFile(pcap, output.Bytes(), 0o600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("tshark", "-r", pcap, "-d", fmt.Sprintf("tcp.port==%d,tls", fixture.Port),
				"-Y", "http.request", "-T", "fields", "-e", "http.request.uri")
			decoded, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("independent production-pcapng decryption: %v: %s", err, decoded)
			}
			for _, uri := range fixture.URIs {
				if !strings.Contains(string(decoded), uri+"\n") {
					t.Fatalf("production export did not decrypt %s: %s", uri, decoded)
				}
			}
		})
	}
}

func TestWireExportPreservesRawPacketsKeysAndRecoversPartialWrite(t *testing.T) {
	engine, store := testEngine(t, Config{HTTPS: true, CaptureNetwork: true, CaptureTLSKeys: true})
	packet := make([]byte, 43)
	packet[0], packet[9] = 0x45, 6
	copy(packet[40:], []byte{0x17, 3, 3})
	at := int64(1700000000123456789)
	for _, event := range []plaintextEvent{
		{Type: "packet", ID: "wire", Data: packet, TimestampNS: at, PacketLength: 80},
		{Type: "secret", ID: "wire", Label: "CLIENT_RANDOM", ClientRandom: bytes.Repeat([]byte{1}, 32), Secret: bytes.Repeat([]byte{2}, 48)},
		{Type: "secret", ID: "wire", Label: "CLIENT_RANDOM", ClientRandom: bytes.Repeat([]byte{1}, 32), Secret: bytes.Repeat([]byte{2}, 48)},
	} {
		if err := engine.acceptWire(event); err != nil {
			t.Fatal(err)
		}
	}
	if state := store.wire.state(); state.Packets != 1 || state.Secrets != 1 {
		t.Fatalf("wrong retained counts: %+v", state)
	}
	var output bytes.Buffer
	if err := store.WriteWire(&output, "pcapng", ""); err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	var packets, keys int
	for len(data) > 0 {
		if len(data) < 12 {
			t.Fatal("short pcapng block")
		}
		kind, length := binary.LittleEndian.Uint32(data), int(binary.LittleEndian.Uint32(data[4:]))
		if length < 12 || length%4 != 0 || length > len(data) || int(binary.LittleEndian.Uint32(data[length-4:])) != length {
			t.Fatal("invalid pcapng block framing")
		}
		body := data[8 : length-4]
		switch kind {
		case 1:
			if binary.LittleEndian.Uint16(body) != 101 || !bytes.Contains(body[8:], []byte{9, 0, 1, 0, 9, 0, 0, 0}) {
				t.Fatal("wrong link type or time resolution")
			}
		case 6:
			if keys != 1 {
				t.Fatal("late key was not moved before packets for single-pass decryption")
			}
			stamp := uint64(binary.LittleEndian.Uint32(body[4:]))<<32 | uint64(binary.LittleEndian.Uint32(body[8:]))
			if stamp != uint64(at) || binary.LittleEndian.Uint32(body[12:]) != 43 || binary.LittleEndian.Uint32(body[16:]) != 80 || !bytes.Equal(body[20:63], packet) {
				t.Fatal("raw bytes, timestamp or truncation lengths changed")
			}
			packets++
		case 10:
			if binary.LittleEndian.Uint32(body) != 0x544c534b {
				t.Fatal("wrong secrets type")
			}
			line := string(body[8 : 8+binary.LittleEndian.Uint32(body[4:])])
			if line != "CLIENT_RANDOM "+strings.Repeat("01", 32)+" "+strings.Repeat("02", 48)+"\n" {
				t.Fatal("NSS key log changed")
			}
			keys++
		}
		data = data[length:]
	}
	if packets != 1 || keys != 1 {
		t.Fatalf("missing packet/secrets blocks: %d/%d", packets, keys)
	}
	path := filepath.Join(store.wire.directory, wireID("wire")+".wire")
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.WriteString(`{"type":"packet"`)
	file.Close()
	reopened, err := OpenStore(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	if state := reopened.wire.state(); state.Packets != 1 || state.Secrets != 1 || state.Error == "" {
		t.Fatalf("interrupted tail was not recovered: %+v", state)
	}
	output.Reset()
	if err := reopened.WriteWire(&output, "keys", ""); err != nil || !strings.HasPrefix(output.String(), "CLIENT_RANDOM ") {
		t.Fatal("keys did not survive reopening", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatal("raw capture is not private")
	}
	keep := filepath.Join(store.wire.directory, "unrelated.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened.Clear()
	if state := reopened.wire.state(); state.Bytes != 0 || state.Packets != 0 || state.Secrets != 0 {
		t.Fatalf("clear retained sensitive data: %+v", state)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("clear removed an unrelated file")
	}
}

func TestWireFiltersKeepWholeConnectionsAndRejectUnavailableExports(t *testing.T) {
	engine, store := testEngine(t, Config{HTTPS: true, CaptureTLSKeys: true})
	for _, id := range []string{"selected", "other"} {
		request, _ := http.NewRequest("GET", "https://"+id+".test/", nil)
		store.begin(Metadata{CaptureID: id}, request, 64)
		random := bytes.Repeat([]byte(id[:1]), 32)
		if err := engine.acceptWire(plaintextEvent{Type: "secret", ID: id, Label: "CLIENT_TRAFFIC_SECRET_0", ClientRandom: random, Secret: bytes.Repeat([]byte{3}, 32)}); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := store.WriteWire(&output, "keys", "selected.test"); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "\n") != 1 || !strings.Contains(output.String(), strings.Repeat("73", 32)) {
		t.Fatal("query exported another connection's keys")
	}
	if err := store.WriteWire(&output, "pcapng", ""); err == nil {
		t.Fatal("export claimed packets when only keys exist")
	}
	if err := store.WriteWire(&output, "keys", "absent.test"); err == nil {
		t.Fatal("empty query match exported all keys")
	}
	if err := engine.acceptWire(plaintextEvent{Type: "packet", ID: "disabled", Data: []byte{0x45}, TimestampNS: 1, PacketLength: 1}); err == nil {
		t.Fatal("packet capture ignored its setting")
	}
	if err := engine.acceptWire(plaintextEvent{Type: "secret", ID: "selected", Label: "CLIENT_TRAFFIC_SECRET_0", ClientRandom: bytes.Repeat([]byte{'s'}, 32), Secret: bytes.Repeat([]byte{4}, 32)}); err == nil {
		t.Fatal("conflicting keys silently replaced the first key")
	}
}

func TestWireRetentionAndLossAreReportedInExport(t *testing.T) {
	engine, store := testEngine(t, Config{HTTPS: true, CaptureNetwork: true})
	store.wire.limit = 300
	for i := 0; i < 5; i++ {
		if err := engine.acceptWire(plaintextEvent{Type: "packet", ID: string(rune('a' + i)), Data: bytes.Repeat([]byte{0x45}, 60), TimestampNS: 1, PacketLength: 60}); err != nil {
			t.Fatal(err)
		}
	}
	if state := store.wire.state(); state.Bytes > 300 || state.Packets >= 5 || state.Error == "" {
		t.Fatalf("raw retention not bounded/reported: %+v", state)
	}
	if err := engine.acceptWire(plaintextEvent{Type: "packet_loss", PacketLength: 2, Error: "two packets lost"}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := store.WriteWire(&output, "pcapng", ""); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(output.Bytes(), []byte("two packets lost")) {
		t.Fatal("pcapng hid reported loss")
	}
}

func TestPlaintextSocketGenerationsAndLateMetadataNeverGuessRoutes(t *testing.T) {
	engine, store := testEngine(t, Config{HTTPS: true})
	for _, id := range []string{"first", "second"} {
		if err := engine.passive.accept(plaintextEvent{Type: "open", ID: id, PID: 42, UID: 10001, Adapter: "Go TLS"}); err != nil {
			t.Fatal(err)
		}
		feedPlaintext(t, engine, id, "write", []byte("GET /"+id+" HTTP/1.1\r\nHost: api.test:8443\r\n\r\n"), 3)
		feedPlaintext(t, engine, id, "read", []byte("HTTP/1.1 204 No Content\r\n\r\n"), 4)
		closePlaintext(t, engine, id)
	}
	items := waitComplete(t, store, 2)
	for index, id := range []string{"first", "second"} {
		socket := &SocketMetadata{FD: 0, Inode: string(rune('a' + index)), NetNS: 1, Source: "127.0.0.1:1234", Destination: "127.0.0.1:443"}
		if index == 1 {
			socket.Source = "[::1]:5678"
			socket.Destination = "[::1]:9443"
		}
		if err := engine.passive.accept(plaintextEvent{Type: "socket", ID: id, Socket: socket}); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range items {
		detail, _ := store.Detail(item.ID)
		metadata := detail.Metadata
		if metadata.Outbound != "" || metadata.Rule != "" || metadata.Port != 8443 || metadata.Socket == nil || metadata.Socket.FD != 0 || metadata.TLSAdapter != "Go TLS" {
			t.Fatalf("socket replaced HTTP authority or invented route: %+v", metadata)
		}
		if metadata.CaptureID == "first" && metadata.Source != "127.0.0.1:1234" || metadata.CaptureID == "second" && metadata.Source != "[::1]:5678" {
			t.Fatalf("FD reuse crossed TLS generations: %+v", metadata)
		}
	}
	if err := engine.passive.accept(plaintextEvent{Type: "socket", ID: "first", SocketConflict: true}); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		detail, _ := store.Detail(item.ID)
		if detail.Metadata.CaptureID == "first" && (detail.Metadata.Socket != nil || detail.Metadata.Source != "" || !strings.Contains(detail.Note, "socket endpoints and sing-box route are unavailable")) {
			t.Fatal("ambiguous association remained visible")
		}
	}
	if engine.parsedRequests.Load() != 2 || engine.recordedRequests.Load() != 2 || engine.goOpened.Load() != 2 || engine.plaintextConnections.Load() != 2 {
		t.Fatal("diagnostics confused observed, plaintext and parsed stages")
	}
}
