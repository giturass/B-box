package httpdebug

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResourceCachePrunesOnlyUnreferencedOwnedFiles(t *testing.T) {
	spec, err := compileProcessingResource("http://resource.test/active.js", 0, true, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	r := testProcessingResources(t, t.TempDir(), t.TempDir(), scriptEnvironment{}, nil)
	r.specs = []*processingResource{spec}
	writeTestScriptCache(t, r, spec, `"last good"`, time.Now())
	active := r.key(spec) + ".json"
	unused := strings.Repeat("1", 64) + ".json"
	pending := active + ".pending"
	for _, name := range []string{unused, pending, "notes.json"} {
		if err := os.WriteFile(filepath.Join(r.directory, name), []byte("keep or reclaim"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	directory := strings.Repeat("2", 64) + ".json"
	if err := os.Mkdir(filepath.Join(r.directory, directory), 0700); err != nil {
		t.Fatal(err)
	}
	link := strings.Repeat("3", 64) + ".json"
	if err := os.Symlink(filepath.Join(r.directory, active), filepath.Join(r.directory, link)); err != nil {
		t.Fatal(err)
	}
	if err := r.pruneCache(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{unused, pending} {
		if _, err := os.Lstat(filepath.Join(r.directory, name)); !os.IsNotExist(err) {
			t.Fatal("unused cache was not reclaimed", name, err)
		}
	}
	for _, name := range []string{active, "notes.json", directory, link} {
		if _, err := os.Lstat(filepath.Join(r.directory, name)); err != nil {
			t.Fatal("cleanup removed an active or unrelated entry", name, err)
		}
	}
	value := loadTestScriptResource(t, r, spec)
	if string(value.data) != `"last good"` {
		t.Fatal("cleanup lost offline last-good source")
	}
}

func TestResourceCacheWritesBeyondFormerDiskQuota(t *testing.T) {
	r := testProcessingResources(t, t.TempDir(), t.TempDir(), scriptEnvironment{}, nil)
	spec, err := compileProcessingResource("http://resource.test/large", 0, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	// Base64 in the cache document takes this single resource past 64 MiB.
	if err = r.writeCache(context.Background(), spec, bytes.Repeat([]byte("x"), 49*1024*1024)); err != nil {
		t.Fatal("cache still has the former disk quota", err)
	}
	info, err := os.Stat(filepath.Join(r.directory, r.key(spec)+".json"))
	if err != nil || info.Size() <= 64*1024*1024 {
		t.Fatal("large cache was not committed", err)
	}
}

func TestResourceRefreshContinuesWhileAnotherOriginStalls(t *testing.T) {
	stalled, fast := make(chan struct{}, 1), make(chan struct{}, 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stalled" {
			stalled <- struct{}{}
			<-r.Context().Done()
			return
		}
		fast <- struct{}{}
		_, _ = io.WriteString(w, "usable")
	}))
	defer origin.Close()
	var specs []*processingResource
	for _, path := range []string{"/stalled", "/fast"} {
		spec, err := compileProcessingResource("http://resource.test"+path, 0, false, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		specs = append(specs, spec)
	}
	r := newProcessingResources(context.Background(), t.TempDir(), t.TempDir(), routedTestEnvironment(origin).dial, nil, specs)
	defer r.Close()
	for _, started := range []chan struct{}{stalled, fast} {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("one resource blocked another refresh")
		}
	}
	closed := make(chan struct{})
	go func() { r.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("resource shutdown did not cancel its stalled read")
	}
}
