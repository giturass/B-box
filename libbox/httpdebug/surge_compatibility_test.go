package httpdebug

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dop251/goja"
)

func TestScriptEmptyBodyArgumentsAndMetadata(t *testing.T) {
	seen := make(map[string]bool)
	for _, phase := range []string{"request", "response"} {
		for _, binary := range []bool{false, true} {
			for _, argument := range []string{"", `{"enabled":true}`} {
				source := `if(typeof $argument==='string') JSON.parse($argument);
					if(typeof $` + phase + `.body!=='undefined' || Object.prototype.hasOwnProperty.call($` + phase + `,'body')) throw Error('empty body must be absent');
					if($script.name!=='actual-name' || typeof $script.sessionID!=='string' || !$script.sessionID || typeof $trigger!=='undefined') throw Error('metadata');
					$done({headers:{'X-Session':$script.sessionID,'X-Argument':typeof $argument}});`
				rule := httpAPIScript(t, HTTPScript{RewriteMatch: RewriteMatch{Name: "actual-name"}, Phase: phase, RequiresBody: true, BinaryBody: binary, Argument: argument, Source: source})
				request, _ := http.NewRequest("GET", "https://example.test/", nil)
				var response *http.Response
				if phase == "response" {
					response = &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}
				}
				result, err := runHTTPScript(request, response, rule, "test", nil, scriptEnvironment{}, func(string) {})
				if err != nil {
					t.Fatal(err)
				}
				id := result.headers.Get("X-Session")
				if seen[id] || id == "" || (result.headers.Get("X-Argument") == "undefined") != (argument == "") {
					t.Fatal("session identity or absent argument is wrong", result.headers)
				}
				seen[id] = true
			}
		}
	}
}

func TestScriptUngzipBinaryViewsAndFailure(t *testing.T) {
	wire := encodedBody(t, []byte{0, 255, 7}, "gzip")
	input := append(append([]byte{99}, wire...), 98)
	rule := httpAPIScript(t, HTTPScript{RequiresBody: true, BinaryBody: true, Source: `
		const input=$request.body;
		const decoded=$utils.ungzip(input.subarray(1,input.length-1));
		if(!(decoded instanceof Uint8Array) || decoded.length!==3 || decoded[1]!==255) throw Error('gzip view');
		for(const value of [undefined,null,'gzip',[],new Uint8Array([1,2,3]),new Uint8Array()]) {
			if($utils.ungzip(value)!==null) throw Error('invalid gzip must return null');
		}
		$done({body:decoded});`})
	request, _ := http.NewRequest("POST", "https://example.test/", nil)
	result, err := runHTTPScript(request, nil, rule, "test", input, scriptEnvironment{}, func(string) {})
	if err != nil || !bytes.Equal(result.body, []byte{0, 255, 7}) || processingBodies.used.Load() != 0 {
		t.Fatal("ungzip failed or retained its budget", result.body, err)
	}
	for _, data := range [][]byte{wire[:len(wire)-1], encodedBody(t, bytes.Repeat([]byte("x"), maxScriptUngzipBytes+1), "gzip")} {
		rule := httpAPIScript(t, HTTPScript{RequiresBody: true, BinaryBody: true, Source: `if($utils.ungzip($request.body)!==null) throw Error('corrupt or oversized gzip accepted'); $done();`})
		if _, err := runHTTPScript(request, nil, rule, "test", data, scriptEnvironment{}, func(string) {}); err != nil || processingBodies.used.Load() != 0 {
			t.Fatal("gzip failure did not release its budget", err)
		}
	}
}

func TestScriptUngzipRetainsBudgetUntilInvocationEnds(t *testing.T) {
	vm := goja.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleanup := installScriptUtils(vm, ctx)
	defer cleanup()
	wire := encodedBody(t, []byte("value"), "gzip")
	_ = vm.Set("input", scriptBodyValue(vm, wire, true))
	// Leave capacity for exactly one small result. Keeping the first array in
	// the VM must prevent a second allocation until invocation cleanup.
	occupied := processingBodies.acquire(maxBufferedBodyBytes - 8*4096)
	if occupied == nil {
		t.Fatal("budget not initially empty")
	}
	defer occupied.release()
	if _, err := vm.RunString(`let first=$utils.ungzip(input); if(!(first instanceof Uint8Array)||$utils.ungzip(input)!==null) throw Error('result reservation not held');`); err != nil {
		t.Fatal(err)
	}
	cleanup()
	cleanup = func() {}
	if processingBodies.used.Load() != maxBufferedBodyBytes-8*4096 {
		t.Fatal("utility result reservation not released")
	}
}

func TestScriptDebugConsoleNotesUseJSON(t *testing.T) {
	for _, debug := range []bool{false, true} {
		rule := httpAPIScript(t, HTTPScript{Debug: debug, Source: `console.log({value:1},['a',2]); $done();`})
		request, _ := http.NewRequest("GET", "https://example.test/", nil)
		var notes []string
		if _, err := runHTTPScript(request, nil, rule, "test", nil, scriptEnvironment{}, func(s string) { notes = append(notes, s) }); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.Join(notes, "\n"), `{"value":1} ["a",2]`) != debug {
			t.Fatal("debug console notes differ from script logging", notes)
		}
	}
}

func TestScriptAbortClosesOnlyTheSelectedHTTPExchange(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, phase := range []string{"request", "response"} {
			t.Run(fmt.Sprintf("h2=%t/%s", h2, phase), func(t *testing.T) {
				var abortedCalls atomic.Int32
				cert, roots := originCertificate(t, "origin.test", false)
				origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
					if request.URL.Path == "/abort" {
						abortedCalls.Add(1)
					}
					io.WriteString(w, "kept")
				}))
				origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
				origin.EnableHTTP2 = h2
				origin.StartTLS()
				defer origin.Close()
				e, _, ca := mitmEngine(t, Config{})
				e.mitm.H2 = h2
				defer e.Close()
				config, _ := json.Marshal(ScriptsConfig{HTTP: []HTTPScript{{RewriteMatch: RewriteMatch{Pattern: "/abort$"}, Phase: phase, Source: "$done({abort:true});"}}})
				e.scripts = scriptProgram(t, string(config))
				client, _ := mitmClient(t, e, origin.Listener.Addr().String(), ca, roots, h2)
				var first, second net.Conn
				request, _ := http.NewRequest("POST", "https://origin.test/abort", nil)
				request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { first = info.Conn }}))
				response, err := client.Do(request)
				if response != nil {
					response.Body.Close()
					t.Fatalf("script abort returned an HTTP response: %d", response.StatusCode)
				}
				if err == nil {
					t.Fatal("script abort did not terminate the exchange")
				}
				request, _ = http.NewRequest("POST", "https://origin.test/kept", nil)
				request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { second = info.Conn }}))
				response, err = client.Do(request)
				if err != nil {
					t.Fatal("abort affected a subsequent exchange:", err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || string(body) != "kept" || h2 && (first != second || response.ProtoMajor != 2) {
					t.Fatal("abort closed another HTTP/2 stream or lost the connection", err)
				}
				want := int32(0)
				if phase == "response" {
					want = 1
				}
				if abortedCalls.Load() != want {
					t.Fatal("request abort reached the origin or response abort ran too early")
				}
			})
		}
	}
}

func TestRejectResponseActionsPreserveStatusBodyAndContentType(t *testing.T) {
	for _, test := range []struct {
		action string
		status int
		body   string
		mime   string
	}{
		{"reject", 403, "Request rejected by B-box rewrite\n", ""},
		{"reject-200", 200, "", "text/plain; charset=utf-8"},
		{"reject-dict", 200, "{}", "application/json"},
	} {
		for _, method := range []string{"GET", "HEAD"} {
			config, _ := json.Marshal(RewriteConfig{URLRewrite: []URLRewrite{{RewriteMatch: RewriteMatch{Pattern: "."}, Action: test.action, Replacement: "_"}}})
			program := rewriteProgram(t, string(config))
			request, _ := http.NewRequest(method, "http://example.test/blocked", nil)
			response, err := program.request(request, "test", request.URL.String(), func(string) {})
			if err != nil || response == nil {
				t.Fatal("rejection did not produce a local response", err)
			}
			if err := program.response(request, response, "test", request.URL.String(), func(string) {}); err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			want := test.body
			if method == "HEAD" {
				want = ""
			}
			if err != nil || response.StatusCode != test.status || string(body) != want || response.Header.Get("Content-Type") != test.mime {
				t.Fatalf("%s %s: status=%d body=%q mime=%q: %v", method, test.action, response.StatusCode, body, response.Header.Get("Content-Type"), err)
			}
		}
	}
}
