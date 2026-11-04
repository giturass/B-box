package httpdebug

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func bodyTestStore(t *testing.T) (*Store, *http.Request) {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Clear)
	r, err := http.NewRequest(http.MethodPost, "http://example.test/body", strings.NewReader("stream"))
	if err != nil {
		t.Fatal(err)
	}
	return s, r
}

func TestStoreBodyHandlesCloseAtEveryRetentionBoundary(t *testing.T) {
	for _, boundary := range []string{"eof", "finish", "limit", "clear", "eviction"} {
		t.Run(boundary, func(t *testing.T) {
			s, request := bodyTestStore(t)
			id := s.begin(Metadata{}, request, 16)
			s.appendBody(id, false, []byte("abc"))
			file := s.records[id].bodies[0].file
			s.appendBody(id, false, []byte("def"))
			if s.records[id].bodies[0].file != file {
				t.Fatal("body chunks reopened their descriptor")
			}
			switch boundary {
			case "eof":
				s.bodyComplete(id, false)
			case "finish":
				s.finish(id)
			case "limit":
				s.appendBody(id, false, bytes.Repeat([]byte("g"), 30))
				s.appendBody(id, false, []byte("still forwarded"))
			case "clear":
				s.Clear()
			case "eviction":
				for range MaxEntries {
					s.begin(Metadata{}, request, 16)
				}
			}
			if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Fatalf("descriptor remained open after %s: %v", boundary, err)
			}
			if boundary == "clear" || boundary == "eviction" {
				if _, err := s.Detail(id); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("removed record reappeared: %v", err)
				}
				return
			}
			detail, err := s.Detail(id)
			if err != nil {
				t.Fatal(err)
			}
			if boundary == "limit" {
				if string(detail.RequestBody) != "abcdefgggggggggg" || !detail.Request.BodyTruncated || detail.Request.BodyBytes != 51 {
					t.Fatalf("prefix limit changed forwarded byte accounting: %+v", detail.Request)
				}
			} else if string(detail.RequestBody) != "abcdef" {
				t.Fatalf("body chunks changed: %q", detail.RequestBody)
			}
		})
	}
}

func TestStoreSlowBodyWriteDoesNotBlockOtherRecordsOrClear(t *testing.T) {
	s, request := bodyTestStore(t)
	id := s.begin(Metadata{}, request, MaxBodyLimit)
	s.appendBody(id, false, []byte("x"))
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	// A pipe with no draining reader models a disk write that has stalled. Leave
	// the regular on-disk record in place for real index and deletion operations.
	s.mu.Lock()
	body := &s.records[id].bodies[0]
	s.closeBodyLocked(body)
	body.file = writer
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.appendBody(id, false, bytes.Repeat([]byte("b"), int(MaxBodyLimit)))
	}()
	started := make(chan error, 1)
	go func() { _, err := io.ReadFull(reader, make([]byte, 1)); started <- err }()
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("body write did not start")
	}
	progress := make(chan error, 1)
	go func() {
		other := s.begin(Metadata{}, request, 64)
		s.appendBody(other, false, []byte("independent"))
		s.bodyComplete(other, false)
		s.finish(other)
		detail, err := s.Detail(other)
		if err == nil && string(detail.RequestBody) != "independent" {
			err = errors.New("unrelated body was corrupted")
		}
		s.Clear()
		progress <- err
	}()
	select {
	case err := <-progress:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("one stalled body blocked unrelated capture work")
	}
	reader.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("removed writer was not released")
	}
	if page := s.Page(MaxEntries, ""); page.Total != 0 || page.StoredBytes != 0 {
		t.Fatalf("late write resurrected cleared accounting: %+v", page)
	}
	for _, suffix := range []string{".request", ".response", ".json"} {
		if _, err := os.Stat(filepath.Join(s.directory, id+suffix)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("late write recreated %s: %v", suffix, err)
		}
	}
}

func TestStoreDetailsRemainConsistentDuringConcurrentBodyWrites(t *testing.T) {
	s, request := bodyTestStore(t)
	id := s.begin(Metadata{}, request, 64*1024)
	var writers sync.WaitGroup
	for _, response := range []bool{false, true} {
		writers.Add(1)
		go func() {
			defer writers.Done()
			chunk := bytes.Repeat([]byte("x"), 1024)
			for range 128 {
				s.appendBody(id, response, chunk)
			}
			s.bodyComplete(id, response)
		}()
	}
	for range 50 {
		detail, err := s.Detail(id)
		if err != nil {
			t.Fatal(err)
		}
		for i, body := range [][]byte{detail.RequestBody, detail.ResponseBody} {
			message := []Message{detail.Request, detail.Response}[i]
			if int64(len(body)) != message.CapturedBytes || message.BodyBytes < message.CapturedBytes || len(body) > 64*1024 {
				t.Fatalf("inconsistent body snapshot: %+v, read=%d", message, len(body))
			}
		}
	}
	writers.Wait()
	s.finish(id)
	detail, err := s.Detail(id)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range []Message{detail.Request, detail.Response} {
		if !message.BodyComplete || !message.BodyTruncated || message.CapturedBytes != 64*1024 || message.BodyBytes != 128*1024 {
			t.Fatalf("incorrect final body accounting: %+v", message)
		}
	}
}

func TestStoreCompletionWaitsForPendingBodyAccounting(t *testing.T) {
	s, request := bodyTestStore(t)
	id := s.begin(Metadata{}, request, MaxBodyLimit)
	s.appendBody(id, false, []byte("x"))
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	s.mu.Lock()
	body := &s.records[id].bodies[0]
	s.closeBodyLocked(body)
	body.file = writer
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.appendBody(id, false, bytes.Repeat([]byte("b"), int(MaxBodyLimit)))
	}()
	started := make(chan error, 1)
	go func() { _, err := io.ReadFull(reader, make([]byte, 1)); started <- err }()
	select {
	case err := <-started:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("body write did not start")
	}
	s.finish(id)
	detail, err := s.Detail(id)
	if err != nil || detail.Complete || s.Page(1, "").Items[0].Complete {
		t.Fatalf("completion published before the pending write: %+v, %v", detail, err)
	}
	reader.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("finishing writer did not return")
	}
	detail, err = s.Detail(id)
	if err != nil || !detail.Complete || s.pendingBytes != 0 || s.records[id].pendingBytes != 0 {
		t.Fatalf("final completion or reservation accounting was lost: %+v, %v", detail, err)
	}
	if _, err := writer.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("finished writer remained open: %v", err)
	}
}
