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
	"syscall"
	"testing"
	"time"
)

func TestSurgeEmbeddedResourceReferenceKeepsContentType(t *testing.T) {
	for _, test := range []struct{ kind, data, contentType string }{
		{"tiny-gif", "", "image/gif"},
		{"base64", "eA==", "application/octet-stream"},
		{"text", "plain", "text/plain; charset=utf-8"},
	} {
		t.Run(test.kind, func(t *testing.T) {
			content, _ := json.Marshal(RewriteConfig{
				Resources: map[string]RewriteResource{"body": {DataType: test.kind, Data: test.data}},
				MapLocal:  []MapLocal{{RewriteMatch: RewriteMatch{Pattern: "."}, Resource: "body"}},
			})
			program := rewriteProgram(t, string(content))
			request, _ := http.NewRequest("GET", "http://example.test/resource", nil)
			response, err := program.request(request, "resource", request.URL.String(), func(string) {})
			if err != nil || response == nil {
				t.Fatal("missing local resource response", err)
			}
			response.Body.Close()
			if response.Header.Get("Content-Type") != test.contentType {
				t.Fatalf("lost referenced type: %s", response.Header.Get("Content-Type"))
			}
		})
	}
}

func testProcessingResources(t *testing.T, directory, profile string, environment scriptEnvironment, specs []*processingResource) *processingResources {
	t.Helper()
	resources := newProcessingResources(context.Background(), directory, profile, environment.dial, environment.roots, specs)
	t.Cleanup(resources.Close)
	return resources
}

func TestSurgeFileResourcesAreValidatedWithoutLoading(t *testing.T) {
	var requests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer origin.Close()
	content, _ := json.Marshal(RewriteConfig{MapLocal: []MapLocal{{RewriteMatch: RewriteMatch{Pattern: "."}, DataType: "file", Data: origin.URL + "/resource.json"}}})
	var rewrite RewriteConfig
	if err := json.Unmarshal(content, &rewrite); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileRewrite(&rewrite); err != nil {
		t.Fatal(err)
	}
	scriptContent, _ := json.Marshal(ScriptsConfig{HTTP: []HTTPScript{{RewriteMatch: RewriteMatch{Pattern: "."}, Phase: "request", ScriptPath: origin.URL + "/script.js"}}})
	var scripts ScriptsConfig
	if err := json.Unmarshal(scriptContent, &scripts); err != nil {
		t.Fatal(err)
	}
	if _, err := CompileScripts(&scripts, time.UTC); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("configuration validation downloaded a resource")
	}
	for _, path := range []string{"../outside", "/absolute/file", "file:///etc/passwd", "https://user:secret@example.test/x"} {
		if _, err := compileProcessingResource(path, 0, true, 0, false); err == nil {
			t.Fatalf("accepted invalid resource path: %s", path)
		}
	}
	if _, err := compileProcessingResource("not-created.js", 0, true, 0, false); err != nil {
		t.Fatal("validation tried to read a local file", err)
	}
}

func TestSurgeMapLocalReadsProfileFilesAndRejectsEscapes(t *testing.T) {
	profile := t.TempDir()
	local := filepath.Join(profile, "mock.json")
	if err := os.WriteFile(local, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	program := rewriteProgram(t, `{"map_local":[{"pattern":".","data_type":"file","data":"mock.json","status":999}]}`)
	program.environment.resources = testProcessingResources(t, t.TempDir(), profile, scriptEnvironment{}, nil)
	for _, expected := range []string{`{"version":1}`, `{"version":2}`} {
		if err := os.WriteFile(local, []byte(expected), 0o600); err != nil {
			t.Fatal(err)
		}
		request, _ := http.NewRequest("GET", "http://example.test/local", nil)
		response, err := program.request(request, "test", request.URL.String(), func(string) {})
		if err != nil || response == nil {
			t.Fatal("local file mock failed", err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(body) != expected || response.StatusCode != 999 || !strings.HasPrefix(response.Header.Get("Content-Type"), "application/json") {
			t.Fatalf("local file result: %q %+v %v", body, response.Header, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, local); err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest("GET", "http://example.test/local", nil)
	var notes []string
	response, err := program.request(request, "test", request.URL.String(), func(s string) { notes = append(notes, s) })
	if err != nil || response != nil || !strings.Contains(strings.Join(notes, " "), "Map Local skipped") {
		t.Fatal("file escape was served or not reported", err)
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(local, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if response, err := program.request(request, "test", request.URL.String(), func(string) {}); err != nil || response != nil {
		t.Fatal("FIFO became a resource", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("local resource open blocked on a FIFO")
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(local)
	if err != nil {
		t.Fatal(err)
	}
	err = file.Truncate(MaxLocalResourceBytes + 1)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response, err := program.request(request, "test", request.URL.String(), func(string) {}); err != nil || response != nil {
		t.Fatal("oversized file became a resource", err)
	}
	if processingBodies.used.Load() != 0 {
		t.Fatal("local resource retained capacity")
	}
}

func TestSurgeURLResourcesCacheDeduplicateAndSurviveOfflineReload(t *testing.T) {
	var requests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `{"cached":true}`)
	}))
	defer origin.Close()
	directory := t.TempDir()
	spec, err := compileProcessingResource("http://resource.test/mock.json", MaxLocalResourceBytes, false, 86400, false)
	if err != nil {
		t.Fatal(err)
	}
	resources := testProcessingResources(t, directory, t.TempDir(), routedTestEnvironment(origin), nil)
	var workers sync.WaitGroup
	for range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			value, lease, err := resources.get(context.Background(), spec)
			if err != nil {
				t.Error(err)
				return
			}
			defer lease.release()
			if string(value.data) != `{"cached":true}` {
				t.Error("wrong cached bytes")
			}
		}()
	}
	workers.Wait()
	if requests.Load() != 1 {
		t.Fatalf("concurrent resource requests downloaded %d times", requests.Load())
	}
	resources.Close()
	offline := testProcessingResources(t, directory, t.TempDir(), scriptEnvironment{}, nil)
	value, lease, err := offline.get(context.Background(), spec)
	if err != nil {
		t.Fatal("offline cache was lost", err)
	}
	lease.release()
	if string(value.data) != `{"cached":true}` {
		t.Fatal("offline cache changed")
	}
	if processingBodies.used.Load() != 0 {
		t.Fatal("resource readers retained capacity")
	}
}

func TestSurgeRemoteScriptsRefreshAndKeepLastGoodCode(t *testing.T) {
	var source atomic.Value
	source.Store(`$done({response:{body:"first"}});`)
	requested := make(chan struct{}, 16)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, source.Load().(string))
		requested <- struct{}{}
	}))
	defer origin.Close()
	configuration := `{"http":[{"pattern":".","phase":"request","script_path":"http://resource.test/script.js","script_update_interval":1}]}`
	program := scriptProgram(t, configuration)
	spec := program.http[0].source
	directory := t.TempDir()
	resources := testProcessingResources(t, directory, t.TempDir(), routedTestEnvironment(origin), []*processingResource{spec})
	run := func(want string) {
		t.Helper()
		request, _ := http.NewRequest("GET", "http://example.test/", nil)
		result, err := runHTTPScript(request, nil, &program.http[0], "test", nil, scriptEnvironment{resources: resources}, func(string) {})
		if err != nil || result.response == nil {
			t.Fatal("loaded script did not run", err)
		}
		body, err := io.ReadAll(result.response.Body)
		result.response.Body.Close()
		if err != nil || string(body) != want {
			t.Fatalf("script result %q != %q: %v", body, want, err)
		}
	}
	run("first")
	select {
	case <-requested:
	case <-time.After(2 * time.Second):
		t.Fatal("script was not downloaded")
	}
	source.Store("function { invalid")
	select {
	case <-requested:
	case <-time.After(3 * time.Second):
		t.Fatal("script was not updated automatically")
	}
	run("first")
	source.Store(`$done({response:{body:"second"}});`)
	select {
	case <-requested:
	case <-time.After(3 * time.Second):
		t.Fatal("script update did not recover")
	}
	run("second")
	resources.Close()
	offline := testProcessingResources(t, directory, t.TempDir(), scriptEnvironment{}, nil)
	value, lease, err := offline.get(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.release()
	if !strings.Contains(string(value.data), "second") {
		t.Fatal("last good source was not committed")
	}
}

func TestSurgeLocalScriptCacheDebugReloadAndLoadFailurePassthrough(t *testing.T) {
	profile := t.TempDir()
	path := filepath.Join(profile, "script.js")
	write := func(source string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`$done({response:{body:"first"}});`)
	resources := testProcessingResources(t, t.TempDir(), profile, scriptEnvironment{}, nil)
	normal := httpAPIScript(t, HTTPScript{ScriptPath: "script.js"})
	debug := httpAPIScript(t, HTTPScript{ScriptPath: "script.js", Debug: true})
	run := func(rule *compiledScript, want string) {
		t.Helper()
		request, _ := http.NewRequest("GET", "http://example.test/", nil)
		result, err := runHTTPScript(request, nil, rule, "test", nil, scriptEnvironment{resources: resources}, func(string) {})
		if err != nil || result.response == nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(result.response.Body)
		result.response.Body.Close()
		if string(body) != want {
			t.Fatalf("local script %q != %q", body, want)
		}
	}
	run(normal, "first")
	write(`$done({response:{body:"second"}});`)
	run(normal, "first")
	run(debug, "second")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	content, _ := json.Marshal(ScriptsConfig{HTTP: []HTTPScript{{RewriteMatch: RewriteMatch{Pattern: "."}, Phase: "request", ScriptPath: "missing.js"}}})
	scripts := scriptProgram(t, string(content))
	program := &httpProgram{RewriteProgram: &RewriteProgram{limit: MaxBodyLimit}, scripts: scripts, environment: scriptEnvironment{resources: resources}}
	request, _ := http.NewRequest("POST", "http://example.test/", strings.NewReader("original"))
	response, err := program.request(request, "test", request.URL.String(), func(string) {})
	if err != nil || response != nil {
		t.Fatal("missing script blocked upstream passthrough", err)
	}
	body, _ := io.ReadAll(request.Body)
	request.Body.Close()
	if string(body) != "original" {
		t.Fatal("loading failure consumed request body")
	}
	if processingBodies.used.Load() != 0 {
		t.Fatal("script source leaked capacity")
	}
}

func TestSurgeResourceRefreshStopsWithService(t *testing.T) {
	started := make(chan struct{}, 1)
	stopped := make(chan struct{}, 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		<-r.Context().Done()
		stopped <- struct{}{}
	}))
	defer origin.Close()
	spec, err := compileProcessingResource("http://resource.test/slow.js", 0, true, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	resources := testProcessingResources(t, t.TempDir(), t.TempDir(), routedTestEnvironment(origin), []*processingResource{spec})
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not start")
	}
	resources.Close()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("service stop retained resource connection")
	}
	if processingBodies.used.Load() != 0 {
		t.Fatal("cancelled download retained capacity")
	}
}
