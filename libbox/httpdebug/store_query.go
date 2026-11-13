package httpdebug

import (
	"container/heap"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CaptureQuery selects saved summaries before they cross the platform bridge.
// Parents contains only the current connection IDs and their searchable fields;
// it lets an HTTP request match the same observed parent metadata as a socket.
type CaptureQuery struct {
	Query        string            `json:"query"`
	Filter       string            `json:"filter"`
	Sort         string            `json:"sort"`
	ConnectionID string            `json:"connectionId"`
	Offset       int               `json:"offset"`
	Limit        int               `json:"limit"`
	Parents      map[string]string `json:"parents"`
	Known        map[string]string `json:"known"`
}

type QueryPage struct {
	Page
	IDs           []string          `json:"ids"`
	Versions      map[string]string `json:"versions"`
	Represented   []string          `json:"represented"`
	CapturedTotal int               `json:"capturedTotal"`
	HARCount      int               `json:"harCount"`
	HasMore       bool              `json:"hasMore"`
}

func (q CaptureQuery) validate(paged bool) error {
	if paged && (q.Limit <= 0 || q.Offset < 0 || q.Offset > math.MaxInt-q.Limit) {
		return errors.New("invalid capture page range")
	}
	if q.Filter != "" && q.Filter != "All" && q.Filter != "Captured" && q.Filter != "Active" && q.Filter != "Finished" {
		return errors.New("invalid capture activity filter")
	}
	if q.Sort != "" && q.Sort != "Created" && q.Sort != "Speed" && q.Sort != "Upload" && q.Sort != "Download" {
		return errors.New("invalid capture activity sort")
	}
	return nil
}

func (q CaptureQuery) matches(r *Exchange, words []string) bool {
	if q.ConnectionID != "" && r.Metadata.ConnectionID != q.ConnectionID {
		return false
	}
	active := !r.Complete && !r.DomainOnly
	if q.Filter == "Active" && !active || q.Filter == "Finished" && active {
		return false
	}
	if len(words) == 0 {
		return true
	}
	text := strings.ToLower(strings.Join([]string{
		r.ID, r.Metadata.ConnectionID, r.Request.URL, r.OriginalURL, r.Request.Method,
		r.Request.Protocol, strconv.Itoa(r.Response.Status), strings.Join(r.Metadata.Packages, ", "),
		r.Metadata.Source, r.Metadata.Destination, r.Metadata.Inbound, r.Metadata.Outbound,
		r.Metadata.Rule, r.Error, q.Parents[r.Metadata.ConnectionID],
	}, " "))
	for _, word := range words {
		if !strings.Contains(text, word) {
			return false
		}
	}
	return true
}

func captureSupportsHAR(r *Exchange) bool {
	return !r.DomainOnly && !r.Metadata.RawTCP && strings.HasPrefix(r.Request.Protocol, "HTTP/")
}

func captureRank(r *Exchange, sort string) int64 {
	switch sort {
	case "Speed":
		return 0 // Request speed is unknown, never the parent connection's rate.
	case "Upload":
		return r.Request.BodyBytes
	case "Download":
		return r.Response.BodyBytes
	default:
		return r.StartedAt
	}
}

func compareCaptures(a, b *Exchange, sort string) int {
	ar, br := captureRank(a, sort), captureRank(b, sort)
	if ar > br {
		return -1
	}
	if ar < br {
		return 1
	}
	return strings.Compare(a.ID, b.ID)
}

// The worst retained entry is at the root, bounding temporary sorting storage
// by the requested window instead of the size of the saved history.
type captureHeap struct {
	items []*Exchange
	sort  string
}

func (h captureHeap) Len() int           { return len(h.items) }
func (h captureHeap) Less(i, j int) bool { return compareCaptures(h.items[i], h.items[j], h.sort) > 0 }
func (h captureHeap) Swap(i, j int)      { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *captureHeap) Push(value any)    { h.items = append(h.items, value.(*Exchange)) }
func (h *captureHeap) Pop() any {
	last := len(h.items) - 1
	value := h.items[last]
	h.items[last] = nil
	h.items = h.items[:last]
	return value
}

func (s *Store) Query(q CaptureQuery) (QueryPage, error) {
	if err := q.validate(true); err != nil {
		return QueryPage{}, err
	}
	words := strings.Fields(strings.ToLower(q.Query))
	s.mu.Lock()
	defer s.mu.Unlock()
	page := QueryPage{
		Page:          Page{Items: make([]Summary, 0), StoredBytes: s.totalBytes, Error: s.lastError},
		IDs:           make([]string, 0),
		Versions:      make(map[string]string),
		Represented:   make([]string, 0),
		CapturedTotal: len(s.records),
	}
	end := q.Offset + q.Limit
	selected := &captureHeap{items: make([]*Exchange, 0, min(end, len(s.records))), sort: q.Sort}
	represented := make(map[string]bool)
	for _, id := range s.order {
		r := s.records[id]
		if !q.matches(r, words) {
			continue
		}
		page.Total++
		if captureSupportsHAR(r) {
			page.HARCount++
		}
		if _, present := q.Parents[r.Metadata.ConnectionID]; present && r.Metadata.ConnectionID != "" {
			represented[r.Metadata.ConnectionID] = true
		}
		if selected.Len() < end {
			heap.Push(selected, r)
		} else if compareCaptures(r, selected.items[0], q.Sort) < 0 {
			selected.items[0] = r
			heap.Fix(selected, 0)
		}
	}
	slices.SortFunc(selected.items, func(a, b *Exchange) int { return compareCaptures(a, b, q.Sort) })
	page.HasMore = page.Total > end
	for id := range represented {
		page.Represented = append(page.Represented, id)
	}
	slices.Sort(page.Represented)
	now := time.Now().UnixMilli()
	for _, r := range selected.items[min(q.Offset, len(selected.items)):] {
		summary := captureSummary(r, now)
		encoded, err := json.Marshal(summary)
		if err != nil {
			return QueryPage{}, err
		}
		digest := sha256.Sum256(encoded)
		version := hex.EncodeToString(digest[:])
		page.IDs = append(page.IDs, r.ID)
		page.Versions[r.ID] = version
		if q.Known[r.ID] != version {
			page.Items = append(page.Items, summary)
		}
	}
	return page, nil
}

// Selection is explicit and frozen before Android opens a document picker.
// It contains only IDs, so exporting a full query does not allocate summaries
// or transfer the history to Dart. Clear never substitutes newer records.
func (s *Store) SelectHAR(q CaptureQuery) ([]string, error) {
	if err := q.validate(false); err != nil {
		return nil, err
	}
	words := strings.Fields(strings.ToLower(q.Query))
	s.mu.Lock()
	defer s.mu.Unlock()
	selected := make([]*Exchange, 0)
	for _, id := range s.order {
		r := s.records[id]
		if captureSupportsHAR(r) && q.matches(r, words) {
			selected = append(selected, r)
		}
	}
	slices.SortFunc(selected, func(a, b *Exchange) int { return compareCaptures(a, b, q.Sort) })
	ids := make([]string, 0, len(selected))
	for _, r := range selected {
		ids = append(ids, r.ID)
	}
	return ids, nil
}

func DecodeCaptureQuery(value string) (CaptureQuery, error) {
	var query CaptureQuery
	if strings.TrimSpace(value) == "null" {
		return query, errors.New("missing capture query")
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&query); err != nil {
		return query, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return query, errors.New("invalid capture query")
	}
	return query, nil
}

func captureSummary(r *Exchange, now int64) Summary {
	duration := r.DurationMS
	if !r.Complete {
		duration = max(0, now-r.StartedAt)
	}
	return Summary{ID: r.ID, ConnectionID: r.Metadata.ConnectionID,
		StartedAt: r.StartedAt, DurationMS: duration, Complete: r.Complete,
		Method: r.Request.Method, URL: r.Request.URL, OriginalURL: r.OriginalURL, Protocol: r.Request.Protocol, Status: r.Response.Status,
		Packages: slices.Clone(r.Metadata.Packages), Source: r.Metadata.Source, Destination: r.Metadata.Destination,
		Inbound: r.Metadata.Inbound, Outbound: r.Metadata.Outbound, Rule: r.Metadata.Rule,
		Bytes: r.Request.BodyBytes + r.Response.BodyBytes, RequestBytes: r.Request.BodyBytes, ResponseBytes: r.Response.BodyBytes,
		Error: r.Error, DomainOnly: r.DomainOnly}
}
