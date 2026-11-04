package httpdebug

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func rewriteProgram(t *testing.T, content string) *httpProgram {
	t.Helper()
	var config RewriteConfig
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatal(err)
	}
	program, err := CompileRewrite(&config)
	if err != nil {
		t.Fatal(err)
	}
	return &httpProgram{RewriteProgram: program}
}

func scriptProgram(t *testing.T, content string) *ScriptProgram {
	t.Helper()
	var config ScriptsConfig
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatal(err)
	}
	program, err := CompileScripts(&config, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	return program
}

func TestRewriteConfigValidation(t *testing.T) {
	for _, invalid := range []string{
		`null`, `{"enabled":true,"unknown":1}`, `{"mitm":{"enabled":true}}`,
		`{"mitm":{"hosts":["https://example.com"]}}`, `{"mitm":{"block_quic":true}}`,
		`{"header_rewrite":[{"pattern":"[","phase":"request","operation":"add","header":"X-Test"}]}`,
		`{"header_rewrite":[{"pattern":".","phase":"request","operation":"add","header":"Content-Length","value":"0"}]}`,
		`{"header_rewrite":[{"pattern":".","phase":"response","operation":"add","header":"X-Test","value":"x\r\nInjected: yes"}]}`,
		`{"header_rewrite":[{"pattern":".","phase":"request","operation":"delete","header":"Host"}]}`,
		`{"url_rewrite":[{"pattern":".","action":"header","replacement":"file:///etc/passwd"}]}`,
		`{"url_rewrite":[{"pattern":".","action":"302","replacement":"http://"}]}`,
		`{"map_remote":[{"pattern":".","replacement":"https://example.test:99999/"}]}`,
		`{"map_remote":[{"pattern":".","replacement":"https://user:password@example.test/"}]}`,
		`{"body_rewrite":[{"pattern":".","phase":"request","regex":"(?<=x)y"}]}`,
		`{"scripts":[{"pattern":".","phase":"request","source":"function {"}]}`,
		`{"scripts":[{"pattern":".","phase":"request","source":"$done()","timeout_ms":2001}]}`,
		`{"scripts":[{"pattern":".","phase":"request","source":"$done()","script_path":"https://example.com/x.js"}]}`,
		`{"map_local":[{"pattern":".","resource":"missing"}]}`,
		`{"map_local":[{"pattern":".","data_type":"base64","data":"%%%"}]}`,
		`{"max_body_bytes":1048577}`, `{"ports":[0]}`,
	} {
		var config RewriteConfig
		if err := json.Unmarshal([]byte(invalid), &config); err == nil {
			t.Errorf("accepted invalid extension: %s", invalid)
		}
	}

}

func TestRewritePipelineAndRecordedMessages(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != "static-script" || r.URL.Path != "/changed" || r.Host != "example.test" {
			t.Errorf("upstream request: %s %s %q: %v", r.Host, r.URL, body, err)
		}
		if values := r.Header.Values("X-Add"); len(values) != 2 || values[0] != "one" || values[1] != "two" {
			t.Errorf("duplicate request headers lost: %v", values)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("User-Agent") != "new-agent" {
			t.Errorf("header rewrite failed: %v", r.Header)
		}
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("ETag", "old-etag")
		writer := gzip.NewWriter(w)
		_, _ = writer.Write([]byte("old"))
		_ = writer.Close()
	}))
	defer origin.Close()
	e, store := testEngine(t, Config{})
	e.rewrite = rewriteProgram(t, `{"enabled":true,"header_rewrite":[{"pattern":"original$","phase":"request","operation":"add","header":"X-Add","value":"two"},{"pattern":"original$","phase":"request","operation":"delete","header":"Cookie"},{"pattern":"original$","phase":"request","operation":"replace_regex","header":"User-Agent","regex":"old","replacement":"new"},{"pattern":"original$","phase":"response","operation":"add","header":"X-Response","value":"rewritten"}],"url_rewrite":[{"pattern":"^http://example.test/original$","action":"header","replacement":"http://example.test/changed"}],"body_rewrite":[{"pattern":"original$","phase":"request","regex":"old","replacement":"static"},{"pattern":"original$","phase":"response","regex":"old","replacement":"new"}]}`).RewriteProgram
	e.scripts = scriptProgram(t, `{"enabled":true,"http":[{"pattern":"original$","phase":"request","requires_body":true,"source":"$done({body: $request.body + '-script'});"},{"pattern":"original$","phase":"response","requires_body":true,"full_headers":true,"source":"$response.headers.push({field:'X-Script',value:$request.id}); $done({status:202, headers:$response.headers, body:$response.body + '-script'}); throw new Error('after done');"}]}`)
	client := debugClient(t, e, origin.Listener.Addr().String())
	request, _ := http.NewRequest("POST", "http://example.test/original", strings.NewReader("old"))
	request.Header.Set("Cookie", "secret=1")
	request.Header.Set("User-Agent", "old-agent")
	request.Header.Set("X-Add", "one")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "new-script" || response.StatusCode != 202 {
		t.Fatalf("rewritten response %d %q: %v", response.StatusCode, body, err)
	}
	if response.Header.Get("Content-Encoding") != "" || response.Header.Get("ETag") != "" || len(response.Header.Values("Set-Cookie")) != 2 || response.Header.Get("X-Response") != "rewritten" {
		t.Fatalf("response headers: %v", response.Header)
	}
	items := waitComplete(t, store, 1)
	detail, err := store.Detail(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(detail.RequestBody) != "static-script" || string(detail.ResponseBody) != "new-script" || detail.OriginalURL != "http://example.test/original" || detail.Request.URL != "http://example.test/changed" || response.Header.Get("X-Script") != detail.ID {
		t.Fatalf("incorrect capture after rewrite: %+v", detail)
	}
}

func TestRewriteHTTPSOfflineMockAndRecordingOff(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "http1", true: "http2"}[h2], func(t *testing.T) {
			e, store, ca := mitmEngine(t, Config{})
			e.captureEnabled = false
			e.rewrite = rewriteProgram(t, `{"enabled":true,"map_local":[{"pattern":"/static$","status":201,"data":"local","headers":[{"name":"Set-Cookie","value":"a=1"},{"name":"Set-Cookie","value":"b=2"}]}]}`).RewriteProgram
			e.scripts = scriptProgram(t, `{"enabled":true,"http":[{"pattern":"/dynamic$","phase":"request","source":"$done({response:{status:202,headers:{'Content-Type':'application/json'},body:JSON.stringify({method:$request.method})}});"}]}`)
			e.mitm = mitmConfig(t, `{"enabled":true,"hosts":["origin.test"]}`)
			client, dials := mitmClient(t, e, "127.0.0.1:1", ca, nil, h2)
			for _, path := range []string{"static", "dynamic"} {
				response, err := client.Get("https://origin.test/" + path)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if path == "static" && (string(body) != "local" || response.StatusCode != 201 || len(response.Header.Values("Set-Cookie")) != 2) {
					t.Fatalf("static mock: %d %q", response.StatusCode, body)
				}
				if path == "dynamic" && (string(body) != `{"method":"GET"}` || response.StatusCode != 202) {
					t.Fatalf("dynamic mock: %d %q", response.StatusCode, body)
				}
			}
			if dials.Load() != 0 || store.Page(10, "").Total != 0 {
				t.Fatal("offline mock dialed an origin or silently enabled recording")
			}
		})
	}
}

func TestRewriteRemoteUsesMappedDialAndVerifiesTLS(t *testing.T) {
	var received atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		if r.URL.RequestURI() != "/mapped?raw=1;2&escaped=%2F" {
			t.Errorf("mapped request target changed: %s", r.URL)
		}
		_, _ = io.WriteString(w, r.Host)
	}))
	defer origin.Close()
	for _, trusted := range []bool{true, false} {
		t.Run(map[bool]string{true: "trusted", false: "untrusted"}[trusted], func(t *testing.T) {
			e, store := testEngine(t, Config{})
			content, _ := json.Marshal(map[string]any{"enabled": true, "map_remote": []any{map[string]any{"pattern": "^http://example.test/(.*)", "replacement": origin.URL + "/$1"}}})
			e.rewrite = rewriteProgram(t, string(content)).RewriteProgram
			var originalDials, mappedDials atomic.Int32
			roots := x509.NewCertPool()
			if trusted {
				roots.AddCert(origin.Certificate())
			}
			transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				client, server := net.Pipe()
				if !e.HandleRouted(context.Background(), server, Metadata{ConnectionID: "original-client", Host: "example.test", Port: 80, Protocol: "http"},
					func(context.Context) (net.Conn, error) {
						originalDials.Add(1)
						return nil, errors.New("original origin must not be dialed")
					},
					func(ctx context.Context, address string) (net.Conn, error) {
						mappedDials.Add(1)
						if address != origin.Listener.Addr().String() {
							t.Errorf("wrong routed address: %s", address)
						}
						return (&net.Dialer{}).DialContext(ctx, "tcp", address)
					}, roots, nil) {
					t.Error("not handled")
				}
				return client, nil
			}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
			response, err := client.Get("http://example.test/mapped?raw=1;2&escaped=%2F")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if trusted && (response.StatusCode != 200 || string(body) != origin.Listener.Addr().String()) {
				t.Fatalf("mapped response: %d %q", response.StatusCode, body)
			}
			if !trusted && response.StatusCode != 502 {
				t.Fatalf("untrusted mapped TLS accepted: %d", response.StatusCode)
			}
			if originalDials.Load() != 0 || mappedDials.Load() != 1 {
				t.Fatalf("wrong dialers: %d/%d", originalDials.Load(), mappedDials.Load())
			}
			detail, err := store.Detail(waitComplete(t, store, 1)[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if detail.Metadata.ConnectionID != "original-client" || !strings.HasPrefix(detail.OriginalURL, "http://example.test/") ||
				!strings.HasPrefix(detail.Request.URL, origin.URL+"/") {
				t.Fatalf("rewriting lost the original connection association: %+v", detail)
			}
		})
	}
	if received.Load() != 1 {
		t.Fatal("untrusted TLS received an HTTP request")
	}
}

func TestRewriteOversizedAndStreamingResponsesAreUnchanged(t *testing.T) {
	for _, encoding := range []string{"identity", "gzip", "br", "event-stream"} {
		t.Run(encoding, func(t *testing.T) {
			body := []byte(strings.Repeat("old", 1024))
			if encoding == "gzip" {
				var buffer bytes.Buffer
				writer := gzip.NewWriter(&buffer)
				_, _ = writer.Write(body)
				writer.Close()
				body = buffer.Bytes()
			}
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if encoding == "event-stream" {
					w.Header().Set("Content-Type", "text/event-stream")
				} else {
					w.Header().Set("Content-Encoding", encoding)
				}
				_, _ = w.Write(body)
			}))
			defer origin.Close()
			e, store := testEngine(t, Config{})
			e.rewrite = rewriteProgram(t, `{"enabled":true,"max_body_bytes":128,"body_rewrite":[{"pattern":".","phase":"response","regex":"old","replacement":"new"}]}`).RewriteProgram
			e.scripts = scriptProgram(t, `{"enabled":true,"max_body_bytes":128,"http":[{"pattern":".","phase":"response","requires_body":true,"source":"$done({body:'bad'});"}]}`)
			client := debugClient(t, e, origin.Listener.Addr().String())
			client.Transport.(*http.Transport).DisableCompression = true
			response, err := client.Get("http://example.test/large")
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || !bytes.Equal(body, got) {
				t.Fatalf("passthrough corrupted %s: got %d, expected %d: %v", encoding, len(got), len(body), err)
			}
			detail, _ := store.Detail(waitComplete(t, store, 1)[0].ID)
			if !strings.Contains(detail.Note, "skipped") {
				t.Fatalf("missing skip reason: %s", detail.Note)
			}
		})
	}
}

func TestRewriteScriptsIsolationBinaryAndErrors(t *testing.T) {
	for _, source := range []string{
		"for (;;) {}",
		"/^(a+)+$/.test('a'.repeat(100) + '!'); $done();",
		"$done({get body(){for(;;){}}});",
		"$done({body:'x'});",
		"$done({headers:{'Content-Length':'999'}});",
		"console.log('forgot done');",
	} {
		content, _ := json.Marshal(map[string]any{"http": []any{map[string]any{"pattern": ".", "phase": "request", "source": source, "timeout_ms": 50, "requires_body": strings.Contains(source, "get body")}}})
		program := scriptProgram(t, string(content))
		request, _ := http.NewRequest("GET", "http://example.test/", nil)
		start := time.Now()
		if _, err := runHTTPScript(request, nil, &program.http[0], "test", nil, func(string) {}); err == nil {
			t.Errorf("script unexpectedly succeeded: %s", source)
		}
		if time.Since(start) > 3*time.Second {
			t.Fatal("script timeout did not interrupt execution")
		}
	}
	program := scriptProgram(t, `{"http":[{"pattern":".","phase":"request","requires_body":true,"binary_body":true,"source":"globalThis.counter=(globalThis.counter||0)+1; if(counter!==1) throw Error('shared runtime'); $request.body[0]=255; $done({body:$request.body});"}]}`)
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			request, _ := http.NewRequest("POST", "http://example.test/", nil)
			result, err := runHTTPScript(request, nil, &program.http[0], "test", []byte{0, 1, 2}, func(string) {})
			if err != nil || !bytes.Equal(result.body, []byte{255, 1, 2}) {
				t.Errorf("isolated binary script: %v %v", result.body, err)
			}
		}()
	}
	wait.Wait()
}

func TestRewriteBodyLimitAndHTTPFraming(t *testing.T) {
	program := rewriteProgram(t, `{"enabled":true,"max_body_bytes":4,"body_rewrite":[{"pattern":".","phase":"request","regex":"old","replacement":"new"}]}`)
	request, _ := http.NewRequest("POST", "http://example.test/", strings.NewReader("too large"))
	if _, err := program.request(request, "test", request.URL.String(), func(string) {}); !errors.Is(err, errRewriteSize) {
		t.Fatalf("oversized upload accepted: %v", err)
	}
	for _, status := range []int{200, 204, 205, 304} {
		for _, method := range []string{"GET", "HEAD"} {
			request, _ := http.NewRequest(method, "http://example.test/", nil)
			response := localRewriteResponse(request, status, make(http.Header), []byte("body"))
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if (method == "HEAD" || status != 200) && len(body) != 0 {
				t.Errorf("%s %d leaked response body", method, status)
			}
			if status == 204 || status == 304 {
				if response.Header.Get("Content-Length") != "" {
					t.Errorf("%d has prohibited length header", status)
				}
			}
		}
	}
}

func TestRewriteUpgradePreservesProtocolAndSkipsBody(t *testing.T) {
	for _, test := range []struct {
		name, source string
		requiresBody bool
		wantError    bool
	}{
		{"headers", "$done({headers:$response.headers});", false, false},
		{"body skipped", "$done({body:'not a websocket frame'});", true, false},
		{"status rejected", "$done({status:200});", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			content, _ := json.Marshal(map[string]any{"http": []any{map[string]any{
				"pattern": ".", "phase": "response", "source": test.source, "requires_body": test.requiresBody,
			}}})
			scripts := scriptProgram(t, string(content))
			program := &httpProgram{RewriteProgram: &RewriteProgram{limit: scripts.limit}, scripts: scripts}
			request, _ := http.NewRequest("GET", "http://example.test/socket", nil)
			body := io.NopCloser(strings.NewReader("opaque websocket frames"))
			response := &http.Response{StatusCode: http.StatusSwitchingProtocols, Body: body,
				Header: http.Header{"Connection": {"Upgrade"}, "Upgrade": {"websocket"}}}
			err := program.response(request, response, "test", request.URL.String(), func(string) {})
			if (err != nil) != test.wantError {
				t.Fatalf("unexpected script result: %v", err)
			}
			if response.StatusCode != http.StatusSwitchingProtocols || response.Body != body || response.Header.Get("Upgrade") != "websocket" || response.Header.Get("Connection") != "Upgrade" {
				t.Fatal("rewrite changed the protocol upgrade or replaced its stream")
			}
			data, err := io.ReadAll(body)
			body.Close()
			if err != nil || string(data) != "opaque websocket frames" {
				t.Fatalf("rewrite consumed protocol data: %q %v", data, err)
			}
		})
	}
}

func TestRewriteReplacementLimitsAndGroups(t *testing.T) {
	re, err := compileRewritePattern(`(?m)^(old)(.*)$`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := boundedReplace(re, "old a\nold b", `${2}:$1:$$`, 100)
	if err != nil || got != " a:old:$\n b:old:$" {
		t.Fatalf("replacement groups: %q %v", got, err)
	}
	if _, err = boundedReplace(re, strings.Repeat("old", 100), "$0$0", 100); !errors.Is(err, errRewriteSize) {
		t.Fatal("replacement growth was not bounded")
	}
}

func TestRewriteLifecycleKeepsRulesWhenRecordingStops(t *testing.T) {
	StopService()
	managerMu.Lock()
	previousStore, previousState := store, lastCaptureState
	store, lastCaptureState = nil, State{}
	managerMu.Unlock()
	t.Cleanup(func() {
		StopService()
		managerMu.Lock()
		store, lastCaptureState = previousStore, previousState
		managerMu.Unlock()
	})
	var config RewriteConfig
	if err := json.Unmarshal([]byte(`{"enabled":true,"map_local":[{"pattern":".","data":"profile"}]}`), &config); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	prepared, err := PrepareProcessing(directory, nil, &config, nil, "", nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if Current() != nil {
		t.Fatal("preparation activated interception")
	}
	ActivateProcessing(prepared, nil, nil)
	if state := Status(); !state.RewriteActive || state.Active || state.RewriteRules != 1 {
		t.Fatalf("profile status: %+v", state)
	}
	if err = Start(directory, `{}`, "", nil); err != nil {
		t.Fatal(err)
	}
	if state := Status(); !state.RewriteActive || !state.Active {
		t.Fatalf("recording replaced profile rules: %+v", state)
	}
	Stop()
	if state := Status(); !state.RewriteActive || state.Active || Current() == nil {
		t.Fatalf("stopping recording stopped rules: %+v", state)
	}
	client := debugClient(t, Current(), "127.0.0.1:1")
	response, err := client.Get("http://example.test/mock")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "profile" {
		t.Fatalf("rules inactive after recording stopped: %q %v", body, err)
	}
	StopService()
	if Current() != nil || Status().RewriteActive || Status().Active {
		t.Fatal("service stop retained processing")
	}
}
