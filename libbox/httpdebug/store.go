package httpdebug

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Message struct {
	Method           string   `json:"method,omitempty"`
	URL              string   `json:"url,omitempty"`
	Protocol         string   `json:"protocol"`
	Status           int      `json:"status,omitempty"`
	Headers          []Header `json:"headers"`
	HeadersTruncated bool     `json:"headersTruncated"`
	BodyBytes        int64    `json:"bodyBytes"`
	CapturedBytes    int64    `json:"capturedBytes"`
	BodyTruncated    bool     `json:"bodyTruncated"`
	BodyComplete     bool     `json:"bodyComplete"`
}

type Exchange struct {
	ID          string   `json:"id"`
	StartedAt   int64    `json:"startedAt"`
	DurationMS  int64    `json:"durationMs"`
	Complete    bool     `json:"complete"`
	Error       string   `json:"error,omitempty"`
	Note        string   `json:"note,omitempty"`
	Metadata    Metadata `json:"metadata"`
	Request     Message  `json:"request"`
	Response    Message  `json:"response"`
	BodyLimit   int64    `json:"bodyLimit"`
	storedBytes int64
	jsonBytes   int64
}

type Detail struct {
	Exchange
	RequestBody     []byte  `json:"requestBody"`
	ResponseBody    []byte  `json:"responseBody"`
	RequestPreview  Preview `json:"requestPreview"`
	ResponsePreview Preview `json:"responsePreview"`
}

type Summary struct {
	ID         string   `json:"id"`
	StartedAt  int64    `json:"startedAt"`
	DurationMS int64    `json:"durationMs"`
	Complete   bool     `json:"complete"`
	Method     string   `json:"method"`
	URL        string   `json:"url"`
	Protocol   string   `json:"protocol"`
	Status     int      `json:"status"`
	Packages   []string `json:"packages"`
	Bytes      int64    `json:"bytes"`
	Error      string   `json:"error,omitempty"`
}

type Page struct {
	Items       []Summary `json:"items"`
	Total       int       `json:"total"`
	StoredBytes int64     `json:"storedBytes"`
	Error       string    `json:"error,omitempty"`
}

// Store retains a bounded prefix of each body on disk, never a full streaming
// response in memory. All operations share one lock; no lock is held across
// upstream network operations or the complete duration of an HTTP transaction.
type Store struct {
	mu         sync.Mutex
	directory  string
	records    map[string]*Exchange
	order      []string
	totalBytes int64
	lastError  string
}

func OpenStore(directory string) (*Store, error) {
	if directory == "" {
		return nil, errors.New("missing capture storage directory")
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
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
	if record := s.records[id]; record != nil {
		headers, truncated := captureHeaders(response.Header, "")
		record.Response = Message{
			Status: response.StatusCode, Protocol: response.Proto, Headers: headers, HeadersTruncated: truncated,
			BodyComplete: response.Body == nil || response.Body == http.NoBody || response.ContentLength == 0,
		}
		if response.StatusCode == http.StatusSwitchingProtocols {
			record.Note = "Protocol upgraded; only the HTTP handshake is recorded"
			record.Response.BodyComplete = true
		}
		s.persistLocked(record)
		s.trimLocked()
	}
}

func (s *Store) appendBody(id string, response bool, p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.records[id]
	if record == nil {
		return
	}
	message, suffix := &record.Request, ".request"
	if response {
		message, suffix = &record.Response, ".response"
	}
	message.BodyBytes += int64(len(p))
	remaining := record.BodyLimit - message.CapturedBytes
	if remaining > 0 && len(p) > 0 {
		if int64(len(p)) > remaining {
			p = p[:remaining]
		}
		file, err := os.OpenFile(filepath.Join(s.directory, id+suffix), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err == nil {
			var n int
			n, err = file.Write(p)
			file.Close()
			message.CapturedBytes += int64(n)
			record.storedBytes += int64(n)
			s.totalBytes += int64(n)
		}
		if err != nil {
			s.lastError = "Cannot save capture body: " + err.Error()
		}
	}
	message.BodyTruncated = message.BodyBytes > message.CapturedBytes
	s.trimLocked()
}

func (s *Store) bodyComplete(id string, response bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[id]; record != nil {
		if response {
			record.Response.BodyComplete = true
		} else {
			record.Request.BodyComplete = true
		}
	}
}

func (s *Store) setError(id string, err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[id]; record != nil {
		record.Error = err.Error()
	}
}

func (s *Store) finish(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[id]; record != nil {
		record.Complete = true
		record.DurationMS = max(0, time.Now().UnixMilli()-record.StartedAt)
		s.persistLocked(record)
		s.trimLocked()
	}
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
		for _, suffix := range []string{".json", ".request", ".response", ".json.tmp"} {
			if err := os.Remove(filepath.Join(s.directory, id+suffix)); err != nil && !errors.Is(err, os.ErrNotExist) {
				s.lastError = "Cannot remove capture: " + err.Error()
			}
		}
		s.totalBytes -= record.storedBytes
		delete(s.records, id)
	}
}

func (s *Store) trimLocked() {
	for len(s.order) > 0 && (len(s.order) > MaxEntries || s.totalBytes > MaxStorageBytes) {
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
		page.Items = append(page.Items, Summary{ID: id, StartedAt: r.StartedAt, DurationMS: duration, Complete: r.Complete,
			Method: r.Request.Method, URL: r.Request.URL, Protocol: r.Request.Protocol, Status: r.Response.Status,
			Packages: r.Metadata.Packages, Bytes: r.Request.BodyBytes + r.Response.BodyBytes, Error: r.Error})
	}
	return page
}

func (s *Store) Detail(id string) (*Detail, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record := s.records[id]
	if record == nil {
		return nil, os.ErrNotExist
	}
	detail := &Detail{Exchange: *record}
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
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(file, record.BodyLimit))
		file.Close()
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
