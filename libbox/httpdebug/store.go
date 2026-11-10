package httpdebug

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Message struct {
	Method            string   `json:"method,omitempty"`
	URL               string   `json:"url,omitempty"`
	Protocol          string   `json:"protocol"`
	Status            int      `json:"status,omitempty"`
	Headers           []Header `json:"headers"`
	HeadersTruncated  bool     `json:"headersTruncated"`
	Trailers          []Header `json:"trailers,omitempty"`
	TrailersTruncated bool     `json:"trailersTruncated,omitempty"`
	BodyBytes         int64    `json:"bodyBytes"`
	CapturedBytes     int64    `json:"capturedBytes"`
	BodyTruncated     bool     `json:"bodyTruncated"`
	BodyComplete      bool     `json:"bodyComplete"`
}

type Exchange struct {
	ID           string   `json:"id"`
	StartedAt    int64    `json:"startedAt"`
	DurationMS   int64    `json:"durationMs"`
	Complete     bool     `json:"complete"`
	Error        string   `json:"error,omitempty"`
	Note         string   `json:"note,omitempty"`
	OriginalURL  string   `json:"originalUrl,omitempty"`
	DomainOnly   bool     `json:"domainOnly,omitempty"`
	Metadata     Metadata `json:"metadata"`
	Request      Message  `json:"request"`
	Response     Message  `json:"response"`
	BodyLimit    int64    `json:"bodyLimit"`
	storedBytes  int64
	jsonBytes    int64
	pendingBytes int64
	finishing    bool
	bodies       *[2]bodyFile
}

// mu serializes writes in one direction. The descriptor and lifecycle flags are
// protected by Store.mu; a busy descriptor is closed by its writer after unlink.
type bodyFile struct {
	mu      sync.Mutex
	file    *os.File
	writing bool
	done    bool
}

type Detail struct {
	Exchange
	RequestBody     []byte  `json:"requestBody"`
	ResponseBody    []byte  `json:"responseBody"`
	RequestPreview  Preview `json:"requestPreview"`
	ResponsePreview Preview `json:"responsePreview"`
}

type Summary struct {
	ID            string   `json:"id"`
	ConnectionID  string   `json:"connectionId,omitempty"`
	StartedAt     int64    `json:"startedAt"`
	DurationMS    int64    `json:"durationMs"`
	Complete      bool     `json:"complete"`
	Method        string   `json:"method"`
	URL           string   `json:"url"`
	OriginalURL   string   `json:"originalUrl,omitempty"`
	Protocol      string   `json:"protocol"`
	Status        int      `json:"status"`
	Packages      []string `json:"packages"`
	Source        string   `json:"source"`
	Destination   string   `json:"destination"`
	Inbound       string   `json:"inbound"`
	Outbound      string   `json:"outbound"`
	Rule          string   `json:"rule"`
	Bytes         int64    `json:"bytes"`
	RequestBytes  int64    `json:"requestBytes"`
	ResponseBytes int64    `json:"responseBytes"`
	Error         string   `json:"error,omitempty"`
	DomainOnly    bool     `json:"domainOnly,omitempty"`
}

type Page struct {
	Items       []Summary `json:"items"`
	Total       int       `json:"total"`
	StoredBytes int64     `json:"storedBytes"`
	Error       string    `json:"error,omitempty"`
}

// Store retains a bounded prefix of each body on disk, never a full streaming
// response in memory. The index lock is released during body writes, detail reads
// and preview decoding; no lock is held across upstream network operations.
type Store struct {
	// Protected by managerMu; independent of recording retention and Clear.
	scriptStorage *scriptStore
	mu            sync.Mutex
	directory     string
	records       map[string]*Exchange
	order         []string
	totalBytes    int64
	pendingBytes  int64
	lastError     string
}

func OpenStore(directory string) (*Store, error) {
	if directory == "" {
		return nil, errors.New("missing capture storage directory")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	if err := removeRetiredWireFiles(directory); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	s := &Store{directory: directory, records: make(map[string]*Exchange)}
	for _, entry := range entries {
		name := entry.Name()
		id := strings.TrimSuffix(name, ".json")
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || !validID(id) {
			continue
		}
		file, err := os.Open(filepath.Join(directory, name))
		if err != nil {
			continue
		}
		var record Exchange
		err = json.NewDecoder(io.LimitReader(file, 2*1024*1024)).Decode(&record)
		file.Close()
		if err != nil || record.ID != id || record.BodyLimit < 1 || record.BodyLimit > MaxBodyLimit {
			_ = os.Remove(filepath.Join(directory, name))
			continue
		}
		if !record.Complete {
			record.Complete = true
			record.Error = "Capture interrupted before the transaction finished"
		}
		record.Request.CapturedBytes, record.Response.CapturedBytes = 0, 0
		for _, suffix := range []string{".json", ".request", ".response"} {
			if info, err := os.Stat(filepath.Join(directory, id+suffix)); err == nil {
				record.storedBytes += info.Size()
				if suffix == ".json" {
					record.jsonBytes = info.Size()
				} else {
					message := &record.Request
					if suffix == ".response" {
						message = &record.Response
					}
					message.CapturedBytes = min(info.Size(), record.BodyLimit)
					message.BodyBytes = max(message.BodyBytes, info.Size())
				}
			}
		}
		record.Request.BodyTruncated = record.Request.BodyTruncated || record.Request.BodyBytes > record.Request.CapturedBytes
		record.Response.BodyTruncated = record.Response.BodyTruncated || record.Response.BodyBytes > record.Response.CapturedBytes
		s.records[id] = &record
		s.order = append(s.order, id)
		s.totalBytes += record.storedBytes
	}
	slices.SortFunc(s.order, func(a, b string) int {
		if s.records[a].StartedAt < s.records[b].StartedAt {
			return -1
		}
		if s.records[a].StartedAt > s.records[b].StartedAt {
			return 1
		}
		return strings.Compare(a, b)
	})
	// Only remove files owned by this store, including orphaned body/temp files.
	for _, entry := range entries {
		name := entry.Name()
		for _, suffix := range []string{".request", ".response", ".json.tmp"} {
			id := strings.TrimSuffix(name, suffix)
			if strings.HasSuffix(name, suffix) && validID(id) && (suffix == ".json.tmp" || s.records[id] == nil) {
				_ = os.Remove(filepath.Join(directory, name))
			}
		}
	}
	s.trimLocked()
	return s, nil
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func (s *Store) begin(metadata Metadata, request *http.Request, bodyLimit int64) string {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return ""
	}
	id := hex.EncodeToString(random)
	headers, truncated := captureHeaders(request.Header, request.Host)
	record := &Exchange{
		ID: id, StartedAt: time.Now().UnixMilli(), Metadata: metadata, BodyLimit: bodyLimit,
		Request: Message{Method: request.Method, URL: request.URL.String(), Protocol: request.Proto,
			Headers: headers, HeadersTruncated: truncated, BodyComplete: request.Body == nil || request.Body == http.NoBody},
		Response: Message{Headers: []Header{}},
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[id] = record
	s.order = append(s.order, id)
	s.persistLocked(record)
	s.trimLocked()
	return id
}

func (s *Store) response(id string, response *http.Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[id]; record != nil && !record.finishing && !record.Complete {
		if response.TLS != nil && record.Metadata.TLS != nil {
			metadata := *record.Metadata.TLS
			metadata.UpstreamVersion = tlsVersion(response.TLS.Version)
			protocol := response.TLS.NegotiatedProtocol
			metadata.UpstreamALPN = &protocol
			metadata.UpstreamServerName = response.TLS.ServerName
			if len(response.TLS.PeerCertificates) > 0 {
				fingerprint := sha256.Sum256(response.TLS.PeerCertificates[0].Raw)
				metadata.UpstreamCertificate = hex.EncodeToString(fingerprint[:])
			}
			record.Metadata.TLS = &metadata
		}
		headers, truncated := captureHeaders(response.Header, "")
		record.Response = Message{
			Status: response.StatusCode, Protocol: response.Proto, Headers: headers, HeadersTruncated: truncated,
			BodyComplete: response.Body == nil || response.Body == http.NoBody || response.ContentLength == 0,
		}
		if response.StatusCode == http.StatusSwitchingProtocols {
			record.Note += "\nProtocol upgraded; only the HTTP handshake is recorded"
			record.Response.BodyComplete = true
		}
		s.persistLocked(record)
		s.trimLocked()
	}
}

func (s *Store) rewrittenRequest(id string, request *http.Request, originalURL string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[id]; record != nil && !record.finishing && !record.Complete {
		if originalURL != request.URL.String() {
			record.OriginalURL = originalURL
		}
		record.Request.URL = request.URL.String()
		record.Request.Method = request.Method
		record.Request.Headers, record.Request.HeadersTruncated = captureHeaders(request.Header, request.Host)
		record.Request.BodyComplete = request.Body == nil || request.Body == http.NoBody
	}
}

func (s *Store) addNote(id, note string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[id]; record != nil && !record.finishing && !record.Complete {
		appendRecordNote(record, note)
	}
}

func appendRecordNote(record *Exchange, note string) {
	if record.Note != "" {
		note = "\n" + note
	}
	remaining := max(0, 4096-len(record.Note))
	record.Note += strings.ToValidUTF8(note[:min(len(note), remaining)], "")
}

func (s *Store) domainOnly(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[id]; record != nil && !record.finishing && !record.Complete {
		record.DomainOnly = true
		record.Note = "Domain only: observed TLS/QUIC hostname; HTTPS plaintext was not observed"
	}
}

// Domain extraction uses all retained matching records, not only the currently
// visible page. Historical URL hosts remain readable and are deduped.
func (s *Store) Domains(query string) []string {
	page := s.Page(MaxEntries, query)
	s.mu.Lock()
	defer s.mu.Unlock()
	hosts := make(map[string]bool)
	for _, item := range page.Items {
		r := s.records[item.ID]
		if r == nil {
			continue
		}
		values := []string{r.Metadata.Host}
		for _, raw := range []string{r.Request.URL, r.OriginalURL} {
			if value, err := url.Parse(raw); err == nil {
				values = append(values, value.Hostname())
			}
		}
		for _, value := range values {
			value = normalizeHost(value)
			if validHost(value) {
				hosts[value] = true
			}
		}
	}
	result := make([]string, 0, len(hosts))
	for host := range hosts {
		result = append(result, host)
	}
	slices.Sort(result)
	return result
}

func (s *Store) appendBody(id string, response bool, p []byte) {
	index, suffix := 0, ".request"
	if response {
		index, suffix = 1, ".response"
	}
	s.mu.Lock()
	record := s.records[id]
	if record == nil || record.Complete || record.finishing {
		s.mu.Unlock()
		return
	}
	if record.bodies == nil {
		record.bodies = new([2]bodyFile)
	}
	body := &record.bodies[index]
	s.mu.Unlock()

	body.mu.Lock()
	defer body.mu.Unlock()
	s.mu.Lock()
	if s.records[id] != record || record.Complete || record.finishing {
		s.mu.Unlock()
		return
	}
	message := &record.Request
	if response {
		message = &record.Response
	}
	message.BodyBytes += int64(len(p))
	message.BodyTruncated = message.BodyBytes > message.CapturedBytes
	remaining := record.BodyLimit - message.CapturedBytes
	// Reaching the saved prefix limit must not stop counting forwarded bytes.
	if remaining <= 0 || len(p) == 0 || body.done {
		s.mu.Unlock()
		return
	}
	p = p[:min(int64(len(p)), remaining)]
	if body.file == nil {
		var err error
		body.file, err = os.OpenFile(filepath.Join(s.directory, id+suffix), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			s.lastError = "Cannot save capture body: " + err.Error()
			s.mu.Unlock()
			return
		}
	}
	file := body.file
	reserved := int64(len(p))
	record.pendingBytes += reserved
	s.pendingBytes += reserved
	// Reserve the prefix before releasing the index lock, so concurrent writes
	// cannot all claim the same remaining space in the retained storage budget.
	s.trimLocked()
	if s.records[id] != record {
		s.mu.Unlock()
		return
	}
	body.writing = true
	s.mu.Unlock()

	n, err := file.Write(p)
	s.mu.Lock()
	defer s.mu.Unlock()
	body.writing = false
	if s.records[id] == record {
		record.pendingBytes -= reserved
		s.pendingBytes -= reserved
		message.CapturedBytes += int64(n)
		record.storedBytes += int64(n)
		s.totalBytes += int64(n)
		message.BodyTruncated = message.BodyBytes > message.CapturedBytes
		if err != nil {
			s.lastError = "Cannot save capture body: " + err.Error()
		}
		if message.CapturedBytes >= record.BodyLimit {
			body.done = true
		}
		if record.finishing {
			record.Complete = record.pendingBytes == 0
			s.persistLocked(record)
		}
		s.trimLocked()
	}
	if err != nil || body.done || record.Complete || s.records[id] != record {
		s.closeBodyLocked(body)
	}
}

func (s *Store) closeBodyLocked(body *bodyFile) {
	if body.file != nil && !body.writing {
		if err := body.file.Close(); err != nil {
			s.lastError = "Cannot close capture body: " + err.Error()
		}
		body.file = nil
	}
}

func (s *Store) closeBodiesLocked(record *Exchange) {
	if record.bodies != nil {
		for index := range record.bodies {
			body := &record.bodies[index]
			body.done = true
			s.closeBodyLocked(body)
		}
	}
}

func (s *Store) bodyComplete(id string, response bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[id]; record != nil && !record.finishing && !record.Complete {
		if response {
			record.Response.BodyComplete = true
		} else {
			record.Request.BodyComplete = true
		}
		if record.bodies != nil {
			index := 0
			if response {
				index = 1
			}
			body := &record.bodies[index]
			body.done = true
			s.closeBodyLocked(body)
		}
	}
}

func (s *Store) trailers(id string, response bool, trailers http.Header) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[id]; record != nil && !record.finishing && !record.Complete {
		message := &record.Request
		if response {
			message = &record.Response
		}
		message.Trailers, message.TrailersTruncated = captureHeaders(trailers, "")
	}
}

func (s *Store) setError(id string, err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[id]; record != nil && !record.finishing && !record.Complete && record.Error == "" {
		record.Error = err.Error()
	}
}

func (s *Store) finish(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[id]; record != nil && !record.finishing && !record.Complete {
		s.finishLocked(record)
		s.trimLocked()
	}
}

func (s *Store) finishLocked(record *Exchange) {
	record.finishing = true
	// Readers may stop polling a completed record. Publish completion only
	// after every in-flight body write has committed its final byte counts.
	record.Complete = record.pendingBytes == 0
	record.DurationMS = max(0, time.Now().UnixMilli()-record.StartedAt)
	s.closeBodiesLocked(record)
	s.persistLocked(record)
}

func (s *Store) persistLocked(record *Exchange) {
	data, err := json.Marshal(record)
	if err == nil {
		path := filepath.Join(s.directory, record.ID+".json")
		err = os.WriteFile(path+".tmp", data, 0o600)
		if err == nil {
			err = os.Rename(path+".tmp", path)
		}
		if err != nil {
			_ = os.Remove(path + ".tmp")
		}
	}
	if err != nil {
		s.lastError = "Cannot save capture metadata: " + err.Error()
		return
	}
	delta := int64(len(data)) - record.jsonBytes
	record.jsonBytes = int64(len(data))
	record.storedBytes += delta
	s.totalBytes += delta
}

func (s *Store) removeLocked(id string) {
	if record := s.records[id]; record != nil {
		s.closeBodiesLocked(record)
		for _, suffix := range []string{".json", ".request", ".response", ".json.tmp"} {
			if err := os.Remove(filepath.Join(s.directory, id+suffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.lastError = "Cannot remove capture: " + err.Error()
			}
		}
		s.totalBytes -= record.storedBytes
		s.pendingBytes -= record.pendingBytes
		delete(s.records, id)
	}
}

func (s *Store) trimLocked() {
	for len(s.order) > 0 && (len(s.order) > MaxEntries || s.totalBytes+s.pendingBytes > MaxStorageBytes) {
		s.removeLocked(s.order[0])
		s.order = s.order[1:]
	}
}

func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = ""
	for _, id := range s.order {
		s.removeLocked(id)
	}
	s.order = nil
}

func (s *Store) Page(limit int, query string) Page {
	limit = min(max(limit, 1), MaxEntries)
	query = strings.ToLower(strings.TrimSpace(query))
	s.mu.Lock()
	defer s.mu.Unlock()
	page := Page{Items: make([]Summary, 0), StoredBytes: s.totalBytes, Error: s.lastError}
	for _, id := range slices.Backward(s.order) {
		r := s.records[id]
		if query != "" && !strings.Contains(strings.ToLower(r.Request.URL+" "+r.Request.Method+" "+
			strings.Join(r.Metadata.Packages, " ")+" "+strconv.Itoa(r.Response.Status)), query) {
			continue
		}
		page.Total++
		if len(page.Items) >= limit {
			continue
		}
		duration := r.DurationMS
		if !r.Complete {
			duration = max(0, time.Now().UnixMilli()-r.StartedAt)
		}
		page.Items = append(page.Items, Summary{ID: id, ConnectionID: r.Metadata.ConnectionID,
			StartedAt: r.StartedAt, DurationMS: duration, Complete: r.Complete,
			Method: r.Request.Method, URL: r.Request.URL, OriginalURL: r.OriginalURL, Protocol: r.Request.Protocol, Status: r.Response.Status,
			Packages: r.Metadata.Packages, Source: r.Metadata.Source, Destination: r.Metadata.Destination,
			Inbound: r.Metadata.Inbound, Outbound: r.Metadata.Outbound, Rule: r.Metadata.Rule,
			Bytes: r.Request.BodyBytes + r.Response.BodyBytes, RequestBytes: r.Request.BodyBytes, ResponseBytes: r.Response.BodyBytes,
			Error: r.Error, DomainOnly: r.DomainOnly})
	}
	return page
}

func (s *Store) Detail(id string) (*Detail, error) {
	var files [2]*os.File
	defer func() {
		for _, file := range files {
			if file != nil {
				file.Close()
			}
		}
	}()
	s.mu.Lock()
	record := s.records[id]
	if record == nil {
		s.mu.Unlock()
		return nil, os.ErrNotExist
	}
	detail := &Detail{Exchange: *record}
	detail.bodies = nil
	if !detail.Complete {
		detail.DurationMS = max(0, time.Now().UnixMilli()-detail.StartedAt)
	}
	for i, suffix := range []string{".request", ".response"} {
		message := &detail.Request
		if i == 1 {
			message = &detail.Response
		}
		file, err := os.Open(filepath.Join(s.directory, id+suffix))
		if errors.Is(err, os.ErrNotExist) {
			message.BodyTruncated = message.BodyTruncated || message.CapturedBytes > 0 || message.BodyBytes > 0
			message.CapturedBytes = 0
			continue
		}
		if err != nil {
			s.mu.Unlock()
			return nil, err
		}
		files[i] = file
	}
	// Opening under the index lock pins the files across eviction/Clear. Read only
	// the captured prefix in this snapshot, even if a writer appends concurrently.
	s.mu.Unlock()
	for i, file := range files {
		if file == nil {
			continue
		}
		message := &detail.Request
		if i == 1 {
			message = &detail.Response
		}
		data, err := io.ReadAll(io.LimitReader(file, message.CapturedBytes))
		if err != nil {
			return nil, err
		}
		message.BodyTruncated = message.BodyTruncated || int64(len(data)) < message.CapturedBytes || int64(len(data)) < message.BodyBytes
		message.CapturedBytes = int64(len(data))
		if i == 0 {
			detail.RequestBody = data
		} else {
			detail.ResponseBody = data
		}
	}
	detail.RequestPreview = preview(detail.Request, detail.RequestBody)
	detail.ResponsePreview = preview(detail.Response, detail.ResponseBody)
	return detail, nil
}
