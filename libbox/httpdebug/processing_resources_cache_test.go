package httpdebug

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dop251/goja"
)

func loadTestScriptResource(t *testing.T, resources *processingResources, spec *processingResource) processingResourceValue {
	t.Helper()
	value, lease, err := resources.get(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	if value.program == nil {
		t.Fatal("script resource has no compiled program")
	}
	return value
}

func writeTestScriptCache(t *testing.T, resources *processingResources, spec *processingResource, source string, fetched time.Time) {
	t.Helper()
	if err := os.MkdirAll(resources.directory, 0o700); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(resourceCacheDocument{Fetched: fetched, Data: []byte(source)})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(resources.directory, resources.key(spec)+".json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, fetched, fetched); err != nil {
		t.Fatal(err)
	}
}

func TestURLScriptProgramCacheConcurrentLoadsAndOfflineReload(t *testing.T) {
	const source = `var count = (typeof count === "undefined" ? 0 : count) + 1; "cached:" + count;`
	var requests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, source)
	}))
	defer origin.Close()
	spec, err := compileProcessingResource("http://resource.test/script.js", MaxScriptBytes, true, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	resources := testProcessingResources(t, directory, t.TempDir(), routedTestEnvironment(origin), nil)
	const count = 12
	programs := make(chan *goja.Program, count)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range count {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			value, lease, err := resources.get(context.Background(), spec)
			if err != nil {
				t.Error(err)
				return
			}
			defer lease.release()
			if value.program == nil {
				t.Error("script resource has no compiled program")
				return
			}
			programs <- value.program
			result, err := goja.New().RunProgram(value.program)
			if err != nil || result.String() != "cached:1" {
				t.Errorf("cached program did not use an independent VM: %v, %v", result, err)
			}
		}()
	}
	close(start)
	workers.Wait()
	close(programs)
	if len(programs) != count || requests.Load() != 1 {
		t.Fatalf("concurrent loads returned %d programs and downloaded %d times", len(programs), requests.Load())
	}
	first := <-programs
	for program := range programs {
		if program != first {
			t.Fatal("concurrent disk cache hits recompiled the same source")
		}
	}
	sameResource := *spec
	if loadTestScriptResource(t, resources, &sameResource).program != first {
		t.Fatal("another rule using the same URL did not reuse its program")
	}
	resources.Close()
	origin.Close()
	offline := testProcessingResources(t, directory, t.TempDir(), scriptEnvironment{}, nil)
	reloaded := loadTestScriptResource(t, offline, spec)
	if reloaded.program == first || string(reloaded.data) != source {
		t.Fatal("service reload did not compile its own program from the persisted source")
	}
	for range 3 {
		if loadTestScriptResource(t, offline, spec).program != reloaded.program {
			t.Fatal("offline disk cache hit recompiled the source")
		}
	}
	if processingBodies.used.Load() != 0 {
		t.Fatal("script resource readers retained body reservations")
	}
}

func TestURLScriptProgramCacheUsesContentHashAndHonorsDebug(t *testing.T) {
	spec, err := compileProcessingResource("http://resource.test/script.js", MaxScriptBytes, true, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	resources := testProcessingResources(t, t.TempDir(), t.TempDir(), scriptEnvironment{}, nil)
	fetched := time.Now().Truncate(time.Second)
	writeTestScriptCache(t, resources, spec, `"first"`, fetched)
	first := loadTestScriptResource(t, resources, spec)
	writeTestScriptCache(t, resources, spec, `"first"`, fetched.Add(-time.Second))
	if loadTestScriptResource(t, resources, spec).program != first.program {
		t.Fatal("cache metadata change recompiled unchanged source")
	}
	// The file size, modification time and fetched timestamp stay unchanged.
	writeTestScriptCache(t, resources, spec, `"other"`, fetched.Add(-time.Second))
	second := loadTestScriptResource(t, resources, spec)
	if second.program == first.program {
		t.Fatal("same-size source change reused stale bytecode")
	}
	result, err := goja.New().RunProgram(second.program)
	if err != nil || result.String() != "other" {
		t.Fatalf("updated program returned %v: %v", result, err)
	}
	debug := *spec
	debug.debug = true
	one := loadTestScriptResource(t, resources, &debug)
	two := loadTestScriptResource(t, resources, &debug)
	if one.program == two.program || one.program == second.program || two.program == second.program {
		t.Fatal("debug loads reused a compiled program")
	}
	if loadTestScriptResource(t, resources, spec).program != second.program {
		t.Fatal("debug loads replaced the normal compiled cache")
	}
	if processingBodies.used.Load() != 0 {
		t.Fatal("script cache updates retained body reservations")
	}
}

func TestURLScriptProgramCacheRefreshKeepsLastUsableProgram(t *testing.T) {
	const firstSource = `"first"`
	const nextSource = `"second"`
	var source atomic.Value
	source.Store(firstSource)
	var requests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, source.Load().(string))
	}))
	defer origin.Close()
	spec, err := compileProcessingResource("http://resource.test/script.js", MaxScriptBytes, true, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	resources := testProcessingResources(t, t.TempDir(), t.TempDir(), routedTestEnvironment(origin), nil)
	previous := loadTestScriptResource(t, resources, spec)
	for _, test := range []struct {
		name, source, want string
		failWrite          bool
	}{
		{"unchanged download", firstSource, firstSource, false},
		{"invalid script", "function { invalid", firstSource, false},
		{"oversized script", strings.Repeat("x", MaxScriptBytes+1), firstSource, false},
		{"disk commit failure", nextSource, firstSource, true},
		{"changed download", nextSource, nextSource, false},
		{"unchanged updated download", nextSource, nextSource, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			source.Store(test.source)
			// Expire the persisted source and retry deadline without sleeping.
			writeTestScriptCache(t, resources, spec, string(previous.data), time.Now().Add(-2*spec.interval))
			state := resources.state(spec)
			state.gate <- struct{}{}
			state.next = time.Time{}
			<-state.gate
			if test.failWrite {
				pending := filepath.Join(resources.directory, resources.key(spec)+".json.pending")
				if err := os.Mkdir(pending, 0o700); err != nil {
					t.Fatal(err)
				}
				blocker := filepath.Join(pending, "keep")
				if err := os.WriteFile(blocker, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := os.Remove(blocker); err != nil {
						t.Error(err)
					}
					if err := os.Remove(pending); err != nil {
						t.Error(err)
					}
				})
			}
			lease := reserveBodyWork(0)
			if lease == nil {
				t.Fatal("no body budget for resource refresh")
			}
			before := requests.Load()
			value, err := resources.load(context.Background(), spec, lease, true)
			lease.release()
			if err != nil || string(value.data) != test.want || value.program == nil {
				t.Fatalf("refresh did not return the usable source: %q, %v", value.data, err)
			}
			if requests.Load() != before+1 {
				t.Fatal("refresh did not attempt a download")
			}
			if (value.program == previous.program) != (test.want == string(previous.data)) {
				t.Fatal("compiled program changed without a committed source change or retained stale code")
			}
			cached := loadTestScriptResource(t, resources, spec)
			if cached.program != value.program || string(cached.data) != test.want {
				t.Fatal("disk cache and compiled program diverged after refresh")
			}
			previous = value
		})
	}
	if processingBodies.used.Load() != 0 {
		t.Fatal("script refresh retained body reservations")
	}
}
