package httpdebug

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func newCompoundRequest(t *testing.T, method, address string, body io.Reader) *http.Request {
	t.Helper()
	request, err := http.NewRequest(method, address, body)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if request.Body != nil {
			request.Body.Close()
		}
	})
	return request
}

func compoundBody(t *testing.T, body io.ReadCloser) string {
	t.Helper()
	data, err := io.ReadAll(body)
	closeErr := body.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read compound-match result: %v; close: %v", err, closeErr)
	}
	return string(data)
}

func TestRewriteCompoundMatchPredicates(t *testing.T) {
	for _, test := range []struct {
		name, match, method, host string
		headers                   http.Header
		want                      bool
	}{
		{"exact full URL", `{"subject":"url","equals":"http://example.test/item?raw=%2F"}`, "POST", "", nil, true},
		{"URL equality is not a substring", `{"subject":"url","equals":"/item"}`, "POST", "", nil, false},
		{"URL regex finds a substring", `{"subject":"url","regex":"item\\?raw="}`, "POST", "", nil, true},
		{"inline regex flags", `{"subject":"url","regex":"(?ims)EXAMPLE\\.TEST/ITEM"}`, "POST", "", nil, true},
		{"method equality", `{"subject":"request.method","equals":"POST"}`, "POST", "", nil, true},
		{"method equality preserves case", `{"subject":"request.method","equals":"post"}`, "POST", "", nil, false},
		{"method regex", `{"subject":"request.method","regex":"(?i)^post$"}`, "POST", "", nil, true},
		{"header name preserves HTTP case insensitivity", `{"subject":"request.header","header":"x-LABEL","equals":"yes"}`, "POST", "", http.Header{"x-label": {"yes"}}, true},
		{"header equality preserves value case", `{"subject":"request.header","header":"X-Label","equals":"YES"}`, "POST", "", http.Header{"X-Label": {"yes"}}, false},
		{"duplicate header equality", `{"subject":"request.header","header":"X-Label","equals":"yes"}`, "POST", "", http.Header{"X-Label": {"no", "yes"}}, true},
		{"duplicate header regex", `{"subject":"request.header","header":"X-Label","regex":"(?i)^yes$"}`, "POST", "", http.Header{"X-Label": {"no", "YES"}}, true},
		{"present empty header", `{"subject":"request.header","header":"X-Label","equals":""}`, "POST", "", http.Header{"X-Label": {""}}, true},
		{"missing header is not an empty value", `{"subject":"request.header","header":"X-Label","equals":""}`, "POST", "", nil, false},
		{"missing header does not satisfy an empty regex", `{"subject":"request.header","header":"X-Label","regex":"^$"}`, "POST", "", nil, false},
		{"Host uses request authority", `{"subject":"request.header","header":"host","equals":"visible.test"}`, "POST", "visible.test", http.Header{"Host": {"decoy.test"}}, true},
		{"Host ignores a decoy header", `{"subject":"request.header","header":"Host","equals":"decoy.test"}`, "POST", "visible.test", http.Header{"Host": {"decoy.test"}}, false},
		{"nested all and any", `{"all":[{"subject":"url","regex":"^http://example\\.test/"},{"subject":"request.method","equals":"POST"},{"any":[{"subject":"request.header","header":"User-Agent","equals":"unused"},{"subject":"request.header","header":"User-Agent","regex":"(?i)^example/"}]}]}`, "POST", "", http.Header{"User-Agent": {"Example/1"}}, true},
		{"nested all needs every condition", `{"all":[{"subject":"request.method","equals":"POST"},{"any":[{"subject":"request.header","header":"X-Label","equals":"one"},{"subject":"request.header","header":"X-Label","equals":"two"}]}]}`, "POST", "", http.Header{"X-Label": {"three"}}, false},
		{"any may combine different subjects", `{"any":[{"subject":"url","equals":"http://unused.test/"},{"all":[{"subject":"request.method","equals":"POST"},{"subject":"request.header","header":"X-Label","equals":"yes"}]}]}`, "POST", "", http.Header{"X-Label": {"yes"}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			program := rewriteProgram(t, `{"map_local":[{"match":`+test.match+`,"data":"matched"}]}`)
			request := newCompoundRequest(t, test.method, "http://example.test/item?raw=%2F", nil)
			if test.host != "" {
				request.Host = test.host
			}
			if test.headers != nil {
				request.Header = test.headers.Clone()
			}
			response, err := program.request(request, "compound", request.URL.String(), func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			if (response != nil) != test.want {
				t.Fatalf("matched = %v, want %v", response != nil, test.want)
			}
			if response != nil && compoundBody(t, response.Body) != "matched" {
				t.Fatal("compound matcher returned the wrong local response")
			}
		})
	}
}

func TestRewriteCompoundURLCandidatesAndCaptures(t *testing.T) {
	for _, test := range []struct {
		name, pattern, match, replacement, want string
	}{
		{"pattern only remains compatible", `^http://line\.test/(\d+)\?raw=%2F$`, "", "http://mapped.test/$1", "http://mapped.test/42"},
		{"pattern and match on the request URL", `^http://line\.test/(\d+)\?raw=%2F$`, `{"subject":"url","equals":"http://line.test/42?raw=%2F"}`, "http://mapped.test/$1", "http://mapped.test/42"},
		{"pattern and match on visible Host", `^http://visible\.test/(\d+)\?raw=%2F$`, `{"all":[{"subject":"url","regex":"visible\\.test"},{"subject":"url","equals":"http://visible.test/42?raw=%2F"}]}`, "http://mapped.test/$1", "http://mapped.test/42"},
		{"pattern and match on connection host", `^http://connection\.test:8080/(\d+)\?raw=%2F$`, `{"subject":"url","regex":"^http://connection\\.test:8080/"}`, "http://mapped.test/$1", "http://mapped.test/42"},
		{"pattern and leaf cannot mix candidates", `^http://line\.test/(\d+)\?raw=%2F$`, `{"subject":"url","regex":"^http://visible\\.test/"}`, "http://mapped.test/$1", "http://line.test/42?raw=%2F"},
		{"two URL leaves cannot mix candidates", "", `{"all":[{"subject":"url","regex":"^http://line\\.test/"},{"subject":"url","regex":"^http://visible\\.test/"}]}`, "http://mapped.test/fixed", "http://line.test/42?raw=%2F"},
		{"omitted pattern replaces the full URL", "", `{"subject":"request.method","equals":"POST"}`, "http://mapped.test/fixed", "http://mapped.test/fixed"},
		{"leaf captures do not replace pattern captures", `^http://line\.test/(\d+)\?raw=%2F$`, `{"subject":"url","regex":"(line)\\.test"}`, "http://mapped.test/$1", "http://mapped.test/42"},
		{"omitted pattern does not take leaf captures", "", `{"subject":"url","regex":"line\\.test/(42)"}`, "http://mapped.test/$1", "http://mapped.test/"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rule := map[string]any{"action": "header", "replacement": test.replacement}
			if test.pattern != "" {
				rule["pattern"] = test.pattern
			}
			if test.match != "" {
				rule["match"] = json.RawMessage(test.match)
			}
			content, err := json.Marshal(map[string]any{"url_rewrite": []any{rule}})
			if err != nil {
				t.Fatal(err)
			}
			program := rewriteProgram(t, string(content))
			program.connectionHost, program.connectionPort = "connection.test", 8080
			request := newCompoundRequest(t, "POST", "http://line.test/42?raw=%2F", nil)
			request.Host = "visible.test"
			if _, err := program.request(request, "compound", request.URL.String(), func(string) {}); err != nil {
				t.Fatal(err)
			}
			if request.URL.String() != test.want {
				t.Fatalf("URL = %q, want %q", request.URL, test.want)
			}
		})
	}
}

func TestRewriteCompoundPipelineUsesOriginalRequestAndResponse(t *testing.T) {
	program := rewriteProgram(t, `{
		"header_rewrite":[
			{"match":{"subject":"request.method","equals":"POST"},"phase":"request","operation":"replace","header":"X-Original","value":"new"},
			{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"request.header","header":"X-Original","equals":"old"}]},"phase":"request","operation":"replace","header":"Host","value":"header.test"},
			{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"request.header","header":"X-Original","equals":"old"}]},"phase":"request","operation":"add","header":"X-Rule","value":"yes"},
			{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"response.status","equals":201}]},"phase":"response","operation":"replace","header":"X-Origin","value":"new"},
			{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"response.header","header":"X-Origin","equals":"old"}]},"phase":"response","operation":"add","header":"X-Rule","value":"yes"}
		],
		"url_rewrite":[{"pattern":"^http://source\\.test/original$","match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"request.header","header":"Host","equals":"source.test"}]},"action":"header","replacement":"http://url.test/changed"}],
		"map_remote":[{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"request.header","header":"X-Original","equals":"old"}]},"replacement":"http://remote.test/final","preserve_host":true}],
		"body_rewrite":[
			{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"request.header","header":"X-Original","equals":"old"}]},"phase":"request","regex":"old","replacement":"new"},
			{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"response.status","equals":201},{"subject":"response.header","header":"X-Origin","equals":"old"}]},"phase":"response","regex":"old","replacement":"new"}
		],
		"map_local":[{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"request.header","header":"Host","equals":"source.test"}]},"status":201,"headers":[{"name":"X-Origin","value":"old"}],"data":"old"}]
	}`)
	program.scripts = scriptProgram(t, `{"http":[
		{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"request.header","header":"X-Original","equals":"old"}]},"phase":"request","requires_body":true,"source":"$done({method:'PATCH',body:$request.body+'-script'});"},
		{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"response.status","equals":201},{"subject":"response.header","header":"X-Origin","equals":"old"}]},"phase":"response","requires_body":true,"source":"$done({status:202,body:$response.body+'-script'});"}
	]}`)
	for _, method := range []string{"POST", "GET"} {
		t.Run(method, func(t *testing.T) {
			request := newCompoundRequest(t, method, "http://source.test/original", strings.NewReader("old"))
			request.Header.Set("X-Original", "old")
			address := request.URL.String()
			response, err := program.request(request, "compound", address, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			if method == "POST" {
				if response == nil || request.URL.String() != "http://remote.test/final" || request.Host != "url.test" || request.Method != "PATCH" ||
					request.Header.Get("X-Original") != "new" || request.Header.Get("X-Rule") != "yes" || compoundBody(t, request.Body) != "new-script" {
					t.Fatalf("request rules did not share the original snapshot: %s %s %s %v, local=%v", request.Method, request.URL, request.Host, request.Header, response != nil)
				}
			} else {
				if response != nil || request.URL.String() != address || request.Host != "source.test" || request.Method != "GET" || request.Header.Get("X-Rule") != "" ||
					request.Header.Get("X-Original") != "old" || compoundBody(t, request.Body) != "old" {
					t.Fatal("a previous request's predicates affected the next request")
				}
				response = &http.Response{StatusCode: 201, Header: http.Header{"X-Origin": {"old"}}, Body: io.NopCloser(strings.NewReader("old"))}
			}
			t.Cleanup(func() { response.Body.Close() })
			if err := program.response(request, response, "compound", address, func(string) {}); err != nil {
				t.Fatal(err)
			}
			body := compoundBody(t, response.Body)
			if method == "POST" {
				if response.StatusCode != 202 || response.Header.Get("X-Origin") != "new" || response.Header.Get("X-Rule") != "yes" || body != "new-script" {
					t.Fatalf("response rules did not share the original snapshots: %d %v %q", response.StatusCode, response.Header, body)
				}
			} else if response.StatusCode != 201 || response.Header.Get("X-Origin") != "old" || response.Header.Get("X-Rule") != "" || body != "old" {
				t.Fatalf("unmatched response changed: %d %v %q", response.StatusCode, response.Header, body)
			}
		})
	}
}

func TestRewriteCompoundUnmatchedBodiesStayStreamingAndScriptsFallThrough(t *testing.T) {
	program := rewriteProgram(t, `{"max_body_bytes":1,"body_rewrite":[
		{"match":{"subject":"request.header","header":"X-Process","equals":"yes"},"phase":"request","regex":"old","replacement":"new"},
		{"match":{"subject":"response.status","equals":202},"phase":"response","jq":".ok=true"}
	]}`)
	program.scripts = scriptProgram(t, `{"max_body_bytes":1,"http":[
		{"match":{"subject":"request.method","equals":"DELETE"},"phase":"request","requires_body":true,"source":"$done({body:'unmatched request script'});"},
		{"match":{"subject":"request.method","equals":"POST"},"phase":"request","source":"$request.headers['X-Script']='request'; $done({headers:$request.headers});"},
		{"match":{"subject":"response.status","equals":202},"phase":"response","requires_body":true,"source":"$done({body:'unmatched response script'});"},
		{"match":{"subject":"response.status","equals":200},"phase":"response","source":"$response.headers['X-Script']='response'; $done({headers:$response.headers});"}
	]}`)
	requestBody := &observedBody{Reader: strings.NewReader("old upload exceeds the configured limit")}
	request := newCompoundRequest(t, "POST", "http://example.test/upload", requestBody)
	if response, err := program.request(request, "compound", request.URL.String(), func(string) {}); err != nil || response != nil {
		t.Fatalf("unmatched body processing changed the upload: local=%v, %v", response != nil, err)
	}
	responseBody := &observedBody{Reader: strings.NewReader(`{"old":"download exceeds the configured limit"}`)}
	response := &http.Response{StatusCode: 200, Header: make(http.Header), Body: responseBody}
	if err := program.response(request, response, "compound", request.URL.String(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if requestBody.reads != 0 || responseBody.reads != 0 || request.Body != requestBody || response.Body != responseBody ||
		request.Header.Get("X-Script") != "request" || response.Header.Get("X-Script") != "response" {
		t.Fatal("unmatched body rules or scripts consumed a stream or prevented the later matching script")
	}
}

func TestRewriteCompoundResponseStatusHeadersAndJQ(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		header http.Header
		want   bool
	}{
		{"numeric status and duplicate header", 201, http.Header{"X-Origin": {"other", "YES"}}, true},
		{"different status", 200, http.Header{"X-Origin": {"YES"}}, false},
		{"missing response header", 201, nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			program := rewriteProgram(t, `{"body_rewrite":[{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"response.status","equals":201},{"subject":"response.status","regex":"^20[12]$"},{"subject":"response.header","header":"x-ORIGIN","regex":"(?i)^yes$"}]},"phase":"response","jq":".ok=true"}]}`)
			request := newCompoundRequest(t, "POST", "http://example.test/data", nil)
			if _, err := program.request(request, "compound", request.URL.String(), func(string) {}); err != nil {
				t.Fatal(err)
			}
			response := &http.Response{StatusCode: test.status, Header: test.header.Clone(), Body: io.NopCloser(strings.NewReader(`{"ok":false}`))}
			if response.Header == nil {
				response.Header = make(http.Header)
			}
			if err := program.response(request, response, "compound", request.URL.String(), func(string) {}); err != nil {
				t.Fatal(err)
			}
			var value struct{ OK bool }
			if err := json.Unmarshal([]byte(compoundBody(t, response.Body)), &value); err != nil {
				t.Fatal(err)
			}
			if value.OK != test.want {
				t.Fatalf("conditional jq applied = %v, want %v", value.OK, test.want)
			}
		})
	}
}

func TestRewriteCompoundShortCircuitAndCancellation(t *testing.T) {
	const slowLeaf = `{"subject":"request.header","header":"X-Work","regex":"^(?=a)(a+)+$"}`
	for _, test := range []struct {
		name, match string
		want        bool
	}{
		{"all stops after false", `{"all":[{"subject":"request.method","equals":"GET"},` + slowLeaf + `]}`, false},
		{"any stops after true", `{"any":[{"subject":"request.method","equals":"POST"},` + slowLeaf + `]}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			program := rewriteProgram(t, `{"map_local":[{"match":`+test.match+`,"data":"matched"}]}`)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			request := newCompoundRequest(t, "POST", "http://example.test/", nil).WithContext(ctx)
			request.Header.Set("X-Work", strings.Repeat("a", 100)+"!")
			response, err := program.request(request, "compound", request.URL.String(), func(string) {})
			if err != nil || (response != nil) != test.want {
				t.Fatalf("short-circuit result: local=%v, want=%v, %v", response != nil, test.want, err)
			}
			if response != nil {
				compoundBody(t, response.Body)
			}
		})
	}

	compiled := rewriteProgram(t, `{"map_local":[{"match":`+slowLeaf+`,"data":"matched"}]}`).RewriteProgram
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	request := newCompoundRequest(t, "POST", "http://example.test/", nil).WithContext(ctx)
	request.Header.Set("X-Work", strings.Repeat("a", 100)+"!")
	program := &httpProgram{RewriteProgram: compiled}
	if _, err := program.request(request, "compound", request.URL.String(), func(string) {}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("compound regex ignored request cancellation: %v", err)
	}
	var workers sync.WaitGroup
	for worker := range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			request, err := http.NewRequest("POST", "http://example.test/", nil)
			if err != nil {
				t.Error(err)
				return
			}
			want := worker%2 == 0
			value := "bbbb"
			if want {
				value = "aaaa"
			}
			request.Header.Set("X-Work", value)
			program := &httpProgram{RewriteProgram: compiled}
			response, err := program.request(request, "compound", request.URL.String(), func(string) {})
			if err != nil || (response != nil) != want {
				t.Errorf("shared compiled matcher leaked another request's cancellation or result: local=%v, want=%v, %v", response != nil, want, err)
			}
			if response != nil {
				response.Body.Close()
			}
		}()
	}
	workers.Wait()
}

func TestRewriteCompoundResponseUsesRequestCancellation(t *testing.T) {
	program := rewriteProgram(t, `{"header_rewrite":[{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"response.header","header":"X-Work","regex":"(?<=a)a"}]},"phase":"response","operation":"add","header":"X-Matched","value":"yes"}]}`)
	ctx, cancel := context.WithCancel(context.Background())
	request := newCompoundRequest(t, "POST", "http://example.test/", nil).WithContext(ctx)
	if _, err := program.request(request, "compound", request.URL.String(), func(string) {}); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	response := &http.Response{StatusCode: 200, Header: http.Header{"X-Work": {"aa"}}, Body: http.NoBody}
	if err := program.response(request, response, "compound", request.URL.String(), func(string) {}); !errors.Is(err, context.Canceled) {
		t.Fatalf("response matching did not retain the request cancellation context: %v", err)
	}
	request = newCompoundRequest(t, "POST", "http://example.test/", nil)
	if _, err := program.request(request, "compound", request.URL.String(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	response = &http.Response{StatusCode: 200, Header: http.Header{"X-Work": {"aa"}}, Body: http.NoBody}
	if err := program.response(request, response, "compound", request.URL.String(), func(string) {}); err != nil || response.Header.Get("X-Matched") != "yes" {
		t.Fatalf("a cancelled exchange poisoned the next exchange: %v %v", response.Header, err)
	}
}

func TestRewriteCompoundMockOverHTTP1AndHTTP2(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		name := "http1"
		if h2 {
			name = "http2"
		}
		t.Run(name, func(t *testing.T) {
			e, _, ca := mitmEngine(t, Config{})
			e.recording.Store(nil)
			e.mitm.H2 = h2
			e.rewrite = rewriteProgram(t, `{"enabled":true,"map_local":[
				{"match":{"all":[{"subject":"url","regex":"/mock$"},{"subject":"request.method","equals":"POST"},{"subject":"request.header","header":"x-variant","equals":"enabled"}]},"status":201,"data":"old"},
				{"pattern":".","data":"fallback"}
			],"header_rewrite":[{"match":{"subject":"response.status","equals":201},"phase":"response","operation":"add","header":"X-Compound","value":"yes"}]}`).RewriteProgram
			e.scripts = scriptProgram(t, `{"enabled":true,"http":[{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"response.status","equals":201}]},"phase":"response","requires_body":true,"source":"$done({body:$response.body+'-script'});"}]}`)
			client, dials := mitmClient(t, e, "127.0.0.1:1", ca, nil, h2)
			for _, method := range []string{"POST", "GET"} {
				request := newCompoundRequest(t, method, "https://origin.test/mock", nil)
				request.Header.Add("X-Variant", "other")
				request.Header.Add("X-Variant", "enabled")
				response, err := client.Do(request)
				if err != nil {
					t.Fatal(err)
				}
				body := compoundBody(t, response.Body)
				wantProtocol := 1
				if h2 {
					wantProtocol = 2
				}
				if response.ProtoMajor != wantProtocol {
					t.Fatalf("conditional mock used HTTP/%d, want HTTP/%d", response.ProtoMajor, wantProtocol)
				}
				if method == "POST" {
					if response.StatusCode != 201 || response.Header.Get("X-Compound") != "yes" || body != "old-script" {
						t.Fatalf("matched HTTP mock: %d %v %q", response.StatusCode, response.Header, body)
					}
				} else if response.StatusCode != 200 || response.Header.Get("X-Compound") != "" || body != "fallback" {
					t.Fatalf("unmatched HTTP mock: %d %v %q", response.StatusCode, response.Header, body)
				}
			}
			if dials.Load() != 0 {
				t.Fatal("conditional offline mocks dialed the origin")
			}
		})
	}
}

func TestRewriteCompoundMatchRejectsInvalidObjects(t *testing.T) {
	for _, invalid := range []string{
		`null`, `[]`, `true`, `"url"`, `{}`,
		`{"all":[]}`, `{"any":[]}`, `{"all":null}`, `{"any":[null]}`,
		`{"all":[{"subject":"url","regex":"."}],"any":[{"subject":"url","regex":"."}]}`,
		`{"all":[{"subject":"url","regex":"."}],"subject":"url","regex":"."}`,
		`{"all":[{"subject":"url","regex":"."}],"unknown":true}`,
		`{"subject":"request.body","equals":"text"}`, `{"subject":"URL","regex":"."}`,
		`{"subject":"url"}`, `{"subject":"url","equals":"x","regex":"x"}`,
		`{"subject":"url","equals":1}`, `{"subject":"url","equals":null}`,
		`{"subject":"request.method","equals":true}`, `{"subject":"request.method","equals":[]}`,
		`{"subject":"url","regex":true}`, `{"subject":"url","regex":null}`, `{"subject":"url","regex":"["}`,
		`{"subject":"request.header","equals":"yes"}`, `{"subject":"request.header","header":"","regex":"."}`,
		`{"subject":"request.header","header":"X Bad","regex":"."}`,
		`{"subject":"request.header","header":"X-Test","equals":{}}`,
		`{"subject":"url","header":"Host","equals":"http://example.test/"}`,
		`{"subject":"url","equals":"x","not":true}`,
	} {
		t.Run(invalid, func(t *testing.T) {
			var rewrite RewriteConfig
			if err := json.Unmarshal([]byte(`{"map_local":[{"pattern":".","match":`+invalid+`,"data":"mock"}]}`), &rewrite); err == nil {
				t.Error("invalid compound matcher passed rewrite validation")
			}
			var scripts ScriptsConfig
			if err := json.Unmarshal([]byte(`{"http":[{"pattern":".","match":`+invalid+`,"phase":"request","source":"$done();"}]}`), &scripts); err == nil {
				t.Error("invalid compound matcher passed script validation")
			}
		})
	}
}

func TestRewriteCompoundResponseConditionsRequireResponsePhase(t *testing.T) {
	for _, leaf := range []string{
		`{"subject":"response.status","equals":200}`,
		`{"subject":"response.header","header":"Content-Type","regex":"json"}`,
	} {
		for _, disabled := range []bool{false, true} {
			for _, test := range []struct {
				category string
				fields   map[string]any
			}{
				{"header_rewrite", map[string]any{"phase": "request", "operation": "add", "header": "X-Test", "value": "yes"}},
				{"url_rewrite", map[string]any{"action": "reject"}},
				{"map_remote", map[string]any{"replacement": "http://mapped.test/"}},
				{"body_rewrite", map[string]any{"phase": "request", "regex": "old", "replacement": "new"}},
				{"map_local", map[string]any{"data": "mock"}},
				{"http", map[string]any{"phase": "request", "source": "$done();"}},
			} {
				fields := test.fields
				fields["pattern"], fields["disabled"] = ".", disabled
				fields["match"] = json.RawMessage(`{"any":[{"subject":"request.method","equals":"GET"},` + leaf + `]}`)
				content, err := json.Marshal(map[string]any{test.category: []any{fields}})
				if err != nil {
					t.Fatal(err)
				}
				if test.category == "http" {
					var config ScriptsConfig
					if err := json.Unmarshal(content, &config); err == nil {
						t.Errorf("response condition accepted by request script, disabled=%v", disabled)
					}
				} else {
					var config RewriteConfig
					if err := json.Unmarshal(content, &config); err == nil {
						t.Errorf("response condition accepted by %s, disabled=%v", test.category, disabled)
					}
				}
			}
		}
	}
	for _, equals := range []string{`"200"`, `200.5`, `99`, `1000`, `true`, `null`, `[]`} {
		var config RewriteConfig
		content := `{"header_rewrite":[{"match":{"subject":"response.status","equals":` + equals + `},"phase":"response","operation":"add","header":"X-Test","value":"yes"}]}`
		if err := json.Unmarshal([]byte(content), &config); err == nil {
			t.Errorf("invalid response status accepted: %s", equals)
		}
	}
	for _, content := range []string{
		`{"map_local":[{"data":"missing matcher"}]}`,
		`{"map_local":[{"match":null,"data":"missing matcher"}]}`,
		`{"url_rewrite":[{"match":{"subject":"request.method","equals":"POST"},"pattern":"[","action":"reject","disabled":true}]}`,
	} {
		var config RewriteConfig
		if err := json.Unmarshal([]byte(content), &config); err == nil {
			t.Fatalf("invalid pattern or missing matcher accepted: %s", content)
		}
	}
}
