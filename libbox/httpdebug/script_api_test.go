package httpdebug

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func httpAPIScript(t *testing.T, rule HTTPScript) *compiledScript {
	t.Helper()
	if rule.Pattern == "" {
		rule.Pattern = "."
	}
	if rule.Phase == "" {
		rule.Phase = "request"
	}
	if rule.TimeoutMS == 0 {
		rule.TimeoutMS = 2000
	}
	program, err := CompileScripts(&ScriptsConfig{HTTP: []HTTPScript{rule}}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	return &program.http[0]
}

func TestScriptCapabilitiesValidateWithoutExecuting(t *testing.T) {
	for _, invalid := range []string{
		`{"http_client":{"allowed_origins":["https://*.test"]}}`,
		`{"http_client":{"allowed_origins":["https://api.test/path"]}}`,
		`{"http_client":{"allowed_origins":["https://api.test?"]}}`,
		`{"http_client":{"allowed_origins":["https://api.test#"]}}`,
		`{"http_client":{"allowed_origins":["https://user@api.test"]}}`,
		`{"http_client":{"allowed_origins":["file:///tmp"]}}`,
		`{"http_client":{"allowed_origins":["http://api.test:0"]}}`,
		`{"http_client":{"allowed_origins":["http://api.test:65536"]}}`,
		`{"http_client":{"max_requests":-1}}`,
		`{"http_client":{"max_body_bytes":33554433}}`,
		`{"http_client":{"proxy":"http://other.test"}}`,
		`{"storage_namespace":"../other"}`,
		`{"storage_namespace":"/absolute"}`,
		`{"storage_namespace":"."}`,
		`{"storage_namespace":"with space"}`,
		`{"storage_namespace":"名字"}`,
	} {
		for _, kind := range []string{"http", "cron"} {
			var rule map[string]any
			if err := json.Unmarshal([]byte(invalid), &rule); err != nil {
				t.Fatal(err)
			}
			rule["source"] = "throw Error('validation executed a script');"
			rule["disabled"] = true
			if kind == "http" {
				rule["pattern"], rule["phase"] = ".", "request"
			} else {
				rule["cron"] = "* * * * *"
			}
			content, _ := json.Marshal(map[string]any{kind: []any{rule}})
			var config ScriptsConfig
			if err := json.Unmarshal(content, &config); err == nil {
				t.Errorf("accepted invalid %s capability: %s", kind, invalid)
			}
		}
	}
	if policy, err := compileScriptHTTP(&ScriptHTTPOptions{MaxRequests: 5}, false); err != nil || policy.maxRequests != 5 {
		t.Fatal("explicit total request cap was not preserved", err)
	}
	if err := validateStorageNamespace(strings.Repeat("n", 65)); err == nil {
		t.Fatal("unbounded namespace")
	}
	if _, err := compileScriptHTTP(&ScriptHTTPOptions{AllowedOrigins: make([]string, 17)}, false); err == nil {
		t.Fatal("unbounded origin allowlist")
	}
	policy, err := compileScriptHTTP(&ScriptHTTPOptions{AllowedOrigins: []string{"https://API.TEST.:443/", "http://[::1]:8080"}}, false)
	if err != nil || !policy.origins["https://api.test:443"] || !policy.origins["http://[::1]:8080"] {
		t.Fatalf("origin normalization: %+v %v", policy, err)
	}
}

func TestHTTPProcessingScriptsUseRoutedClientInBothPhases(t *testing.T) {
	wire := encodedBody(t, []byte("reply"), "gzip")
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || r.Method != "POST" || string(body) != "sent" || r.Header.Get("X-Input") != "GET" || r.Header.Get("Accept-Encoding") != "gzip" || r.Host != "aux.test:8080" {
			t.Errorf("unexpected script request: %s %s %q %v", r.Method, r.Host, body, err)
		}
		w.Header().Set("X-Reply", "routed")
		w.Header().Set("Content-Encoding", "gzip")
		w.Write(wire)
	}))
	defer origin.Close()
	for _, phase := range []string{"request", "response"} {
		t.Run(phase, func(t *testing.T) {
			done := "$done({response:{body:data}})"
			if phase == "response" {
				done = "$done({body:data})"
			}
			rule := httpAPIScript(t, HTTPScript{Phase: phase, RequiresBody: phase == "response", MaxBodyBytes: 256,
				HTTPClient: &ScriptHTTPOptions{AllowedOrigins: []string{"http://aux.test:8080"}},
				Source: `$httpClient.post({url:'http://aux.test:8080/data',headers:{'X-Input':$request.method,'Accept-Encoding':'gzip'},body:'sent'},
					function(error,response,data) {
						if(error || response.status!==200 || response.headers['X-Reply']!=='routed') throw Error(error || 'response');
						` + done + `; throw Error('after done');
					});`})
			var dials atomic.Int32
			environment := scriptEnvironment{dial: func(ctx context.Context, address string) (net.Conn, error) {
				if address != "aux.test:8080" {
					t.Errorf("wrong route target %s", address)
				}
				dials.Add(1)
				return (&net.Dialer{}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
			}}
			request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
			var response *http.Response
			if phase == "response" {
				response = localRewriteResponse(request, 200, make(http.Header), []byte("original"))
				defer response.Body.Close()
			}
			result, err := runHTTPScript(request, response, rule, "test", []byte("original"), environment, func(string) {})
			if err != nil || dials.Load() != 1 {
				t.Fatalf("HTTP script client: %+v %v", result, err)
			}
			body := result.body
			if phase == "request" {
				if result.response == nil {
					t.Fatal("missing mock")
				}
				body, err = io.ReadAll(result.response.Body)
				result.response.Body.Close()
			}
			if err != nil || string(body) != "reply" {
				t.Fatalf("lost callback result %q %v", body, err)
			}
			if processingBodies.used.Load() != 0 {
				t.Fatal("script retained its HTTP body budget")
			}
		})
	}
}

func TestHTTPClientDeniesUnconfiguredDestinationsAndHostOverrides(t *testing.T) {
	allowed := &ScriptHTTPOptions{AllowedOrigins: []string{"http://allowed.test:8080"}}
	for _, test := range []struct {
		options *ScriptHTTPOptions
		request string
	}{
		{&ScriptHTTPOptions{AllowedOrigins: []string{}}, `'http://allowed.test:8080/'`},
		{allowed, `'http://allowed.test/'`},
		{allowed, `'https://allowed.test:8080/'`},
		{allowed, `'http://sub.allowed.test:8080/'`},
		{allowed, `'http://allowed.test.evil:8080/'`},
		{allowed, `'http://allowed.test:8080@evil.test/'`},
		{allowed, `'file:///etc/passwd'`},
		{allowed, `{url:'http://allowed.test:8080/',headers:{Host:'evil.test:8080'}}`},
		{allowed, `{url:'http://allowed.test:8080/',headers:[{field:'Host',value:'allowed.test:8080'},{field:'Host',value:'evil.test:8080'}]}`},
		{allowed, `{url:'http://allowed.test:8080/',insecure:'true'}`},
	} {
		rule := httpAPIScript(t, HTTPScript{HTTPClient: test.options,
			Source: "$httpClient.get(" + test.request + ",function(error,response,data){if(!error || response!==null || data!==undefined) throw Error('allowed'); $done();});"})
		request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
		var dials atomic.Int32
		_, err := runHTTPScript(request, nil, rule, "test", nil, scriptEnvironment{dial: func(context.Context, string) (net.Conn, error) {
			dials.Add(1)
			return nil, errors.New("unexpected dial")
		}}, func(string) {})
		if err != nil || dials.Load() != 0 {
			t.Fatalf("policy bypass for %s: %v (%d dials)", test.request, err, dials.Load())
		}
	}
}

func TestHTTPClientLimitsRedirectsBodiesAndRequests(t *testing.T) {
	var requests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/large":
			io.WriteString(w, "123456789")
		case "/binary":
			w.Write([]byte{0xff})
		case "/redirect":
			w.Header().Set("Location", "http://forbidden.test/")
			w.WriteHeader(302)
			io.WriteString(w, "redirect")
		default:
			io.WriteString(w, "ok")
		}
	}))
	defer origin.Close()
	environment := scriptEnvironment{dial: func(ctx context.Context, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
	}}
	for _, test := range []struct {
		source   string
		requests int32
	}{
		{`$httpClient.get('http://aux.test/large',(e)=>{if(!e || !e.includes('max_body_bytes')) throw Error('size'); $done();});`, 1},
		{`$httpClient.get('http://aux.test/binary',(e)=>{if(!e || !e.includes('UTF-8')) throw Error('binary'); $done();});`, 1},
		{`$httpClient.post({url:'http://aux.test/',body:'123456789'},(e)=>{if(!e) throw Error('upload'); $done();});`, 0},
		{`$httpClient.get({url:'http://aux.test/redirect','auto-redirect':false},(e,r,d)=>{if(e || r.status!==302 || d!=='redirect') throw Error('redirect'); $done();});`, 1},
		{`$httpClient.get('http://aux.test/redirect',(e)=>{if(!e || !e.includes('allowed_origins')) throw Error('redirect restriction'); $done();});`, 1},
		{`let completed=0; for(let i=0;i<3;i++) $httpClient.get('http://aux.test/',(e)=>{if(!!e!==(i===2)) throw Error('request limit'); if(++completed===3) $done();});`, 2},
		{`for(let i=0;i<2;i++) $httpClient.get('http://denied.test/',(e)=>{if(!e) throw Error('origin');});
			$httpClient.get('http://aux.test/',(e)=>{if(!e || !e.includes('request limit')) throw Error('failed calls not counted'); $done();});`, 0},
	} {
		before := requests.Load()
		rule := httpAPIScript(t, HTTPScript{Source: test.source, MaxBodyBytes: 8,
			HTTPClient: &ScriptHTTPOptions{AllowedOrigins: []string{"http://aux.test"}, MaxRequests: 2, MaxBodyBytes: 8}})
		request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
		if _, err := runHTTPScript(request, nil, rule, "test", nil, environment, func(string) {}); err != nil {
			t.Fatal(err)
		}
		if received := requests.Load() - before; received != test.requests {
			t.Fatalf("expected %d requests, received %d: %s", test.requests, received, test.source)
		}
	}
}

func TestHTTPClientNeverFallsBackAndVerifiesTLS(t *testing.T) {
	var received atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		io.WriteString(w, "trusted")
	}))
	defer origin.Close()
	for _, mode := range []string{"trusted", "untrusted", "missing-route", "failed-route"} {
		expected := "true"
		if mode == "trusted" {
			expected = "false"
		}
		rule := httpAPIScript(t, HTTPScript{Argument: origin.URL, HTTPClient: &ScriptHTTPOptions{AllowedOrigins: []string{origin.URL}},
			Source: "$httpClient.get($argument,(e,r,d)=>{if(!!e!==" + expected + ") throw Error('route or trust'); if(!e && d!=='trusted') throw Error('body'); $done();});"})
		roots := x509.NewCertPool()
		if mode != "untrusted" {
			roots.AddCert(origin.Certificate())
		}
		environment := scriptEnvironment{roots: roots}
		if mode != "missing-route" {
			environment.dial = func(ctx context.Context, address string) (net.Conn, error) {
				if mode == "failed-route" {
					return nil, errors.New("route rejected")
				}
				return (&net.Dialer{}).DialContext(ctx, "tcp", address)
			}
		}
		request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
		if _, err := runHTTPScript(request, nil, rule, "test", nil, environment, func(string) {}); err != nil {
			t.Fatalf("%s: %v", mode, err)
		}
	}
	if received.Load() != 1 {
		t.Fatal("client bypassed the route or TLS verification")
	}
}

func TestHTTPClientCancellationReleasesResources(t *testing.T) {
	started := make(chan struct{}, 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer origin.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rule := httpAPIScript(t, HTTPScript{HTTPClient: &ScriptHTTPOptions{AllowedOrigins: []string{"http://aux.test"}},
		Source: `$httpClient.get('http://aux.test/',()=>{$done();});`})
	request, _ := http.NewRequestWithContext(ctx, "GET", "http://intercepted.test/", nil)
	finished := make(chan error, 1)
	go func() {
		_, err := runHTTPScript(request, nil, rule, "test", nil, scriptEnvironment{dial: func(ctx context.Context, address string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
		}}, func(string) {})
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("script HTTP did not start")
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled HTTP was successful")
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP cancellation did not stop the script")
	}
	if len(httpScriptSlots) != 0 || processingBodies.used.Load() != 0 {
		t.Fatal("HTTP cancellation retained interpreter or body capacity")
	}
}

func TestHeaderOnlyScriptHTTPHonorsGlobalBodyBudget(t *testing.T) {
	lease := processingBodies.acquire(maxBufferedBodyBytes)
	if lease == nil {
		t.Fatal("body budget already in use")
	}
	defer lease.release()
	rule := httpAPIScript(t, HTTPScript{Phase: "response", HTTPClient: &ScriptHTTPOptions{AllowedOrigins: []string{"http://aux.test"}},
		Source: `$httpClient.get('http://aux.test/',(e)=>{if(!e || !e.includes('budget')) throw Error('budget'); $done();});`})
	request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
	response := localRewriteResponse(request, 200, make(http.Header), nil)
	defer response.Body.Close()
	var dials atomic.Int32
	_, err := runHTTPScript(request, response, rule, "test", nil, scriptEnvironment{dial: func(context.Context, string) (net.Conn, error) {
		dials.Add(1)
		return nil, errors.New("unexpected dial")
	}}, func(string) {})
	if err != nil || dials.Load() != 0 {
		t.Fatalf("header-only script bypassed body budget: %v", err)
	}
}
