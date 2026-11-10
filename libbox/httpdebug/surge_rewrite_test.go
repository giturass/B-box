package httpdebug

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSurgeLookaroundBackreferencesAndReplacementOffsets(t *testing.T) {
	for _, test := range []struct{ pattern, input, replacement, want string }{
		{`(?=猫)`, "猫猫", "[", "[猫[猫"},
		{`(?!foo)\b\w+`, "foo bar", "x", "foo x"},
		{`(?<=id=)(\d+)-\1(?=&|$)`, "id=123-123&x=1", "$1", "id=123&x=1"},
		{`(?<=id=)(\d+)-\1(?=&|$)`, "id=123-456&x=1", "$1", "id=123-456&x=1"},
		{`(?<!前)猫`, "前猫 后猫 猫", "虎", "前猫 后虎 虎"},
		{`(?P<word>猫)(?=😊)(😊)`, "前猫😊后", `${word}:$2:$1:$$`, "前猫:😊:猫:$后"},
		{`(😊)\1`, "中😊😊文", "<$1>", "中<😊>文"},
		{`(?<word>猫)\k<word>`, "前猫猫后", "<$1>", "前<猫>后"},
		{`a*(?=$)`, "a", "x", "x"},
		{`(?<=猫)|(?=😊)`, "猫😊", "/", "猫/😊"},
	} {
		t.Run(test.pattern, func(t *testing.T) {
			pattern, err := compileRewritePattern(test.pattern)
			if err != nil {
				t.Fatal(err)
			}
			got, err := boundedReplace(pattern, test.input, test.replacement, 4096)
			if err != nil || got != test.want {
				t.Fatalf("got %q, want %q: %v", got, test.want, err)
			}
		})
	}
	for _, pattern := range []string{`(?m)^(old)(.*)$`, `(?P<name>\w+)`, `a*`, `\b`} {
		re, err := compileRewritePattern(pattern)
		if err != nil || re.linear == nil {
			t.Fatalf("ordinary RE2 pattern changed engine: %s %v", pattern, err)
		}
		input, template := "old a\nold b 猫😊", `${name}:$1:$$`
		got, err := boundedReplace(re, input, template, 4096)
		if want := regexp.MustCompile(pattern).ReplaceAllString(input, template); err != nil || got != want {
			t.Fatalf("RE2 compatibility: %s %q != %q: %v", pattern, got, want, err)
		}
	}
	re, _ := compileRewritePattern(`(?=(猫))`)
	if got, err := boundedReplace(re, strings.Repeat("猫", 8193), "$1", 100000); err != nil || got != strings.Repeat("猫猫", 8193) {
		t.Fatal("replacement beyond the former count cap failed", err)
	}
	re, _ = compileRewritePattern(`(?=(猫))猫`)
	if _, err := boundedReplace(re, "猫", "$1$1", 5); !errors.Is(err, errRewriteSize) {
		t.Fatalf("UTF-8 output size escaped limit: %v", err)
	}
}

func TestSurgeRegexCancellationAndIndependentConcurrentUse(t *testing.T) {
	re, err := compileRewritePattern(`^(?=a)(a+)+$`)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err = re.match(strings.Repeat("a", 100)+"!", newRegexContext(ctx)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("native backtracking did not observe cancellation", err)
	}
	if matched, err := re.match("aaaa", newRegexContext(context.Background())); err != nil || !matched {
		t.Fatal("one invocation's cancellation poisoned the compiled expression", err)
	}
	re, _ = compileRewritePattern(`(?<=猫)(😊)\1`)
	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := re.match("猫😊😊", newRegexContext(cancelled)); !errors.Is(err, context.Canceled) {
		t.Fatal("already-cancelled match started", err)
	}
	var workers sync.WaitGroup
	for range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			got, err := boundedReplace(re, "猫😊😊", "$1", 64)
			if err != nil || got != "猫😊" {
				t.Errorf("concurrent replacement: %q %v", got, err)
			}
		}()
	}
	workers.Wait()
}

func TestSurgeURLCandidatesWebSocketGatewayAndInvalidTarget(t *testing.T) {
	for _, authority := range []string{"line.test", "host.test", "connection.test:8080"} {
		pattern := "^http://" + regexp.QuoteMeta(authority) + `/(?<=/)(\d+)-\1(?=\?|$)`
		content, _ := json.Marshal(RewriteConfig{URLRewrite: []URLRewrite{{
			RewriteMatch: RewriteMatch{Pattern: pattern}, Action: "header", Replacement: "ws://mapped.test/$1",
		}}})
		program := rewriteProgram(t, string(content))
		program.connectionHost, program.connectionPort = "connection.test", 8080
		request, _ := http.NewRequest("GET", "http://line.test/42-42?raw=%2F;2", nil)
		request.Host = "host.test"
		request.Header.Set("Connection", "Upgrade")
		request.Header.Set("Upgrade", "websocket")
		if _, err := program.request(request, "test", request.URL.String(), func(string) {}); err != nil {
			t.Fatal(err)
		}
		if request.URL.String() != "http://mapped.test/42?raw=%2F;2" || request.Host != "mapped.test" || request.Header.Get("Upgrade") != "websocket" {
			t.Fatalf("candidate %s lost target or upgrade: %s %s", authority, request.URL, request.Host)
		}
	}
	for _, gateway := range []string{"192.0.2.1", "2001:db8::1", ""} {
		program := rewriteProgram(t, `{"url_rewrite":[{"pattern":"^http://line.test/.*","action":"307","replacement":"http://{{{GATEWAY_ADDRESS}}}:8080/router"}]}`)
		program.environment.gateway = func() string { return gateway }
		request, _ := http.NewRequest("GET", "http://line.test/original", nil)
		response, err := program.request(request, "test", request.URL.String(), func(string) {})
		if err != nil {
			t.Fatal(err)
		}
		if gateway == "" {
			if response != nil || request.URL.Path != "/original" {
				t.Fatal("missing gateway fabricated a redirect")
			}
		} else {
			want := "http://" + gateway + ":8080/router"
			if strings.Contains(gateway, ":") {
				want = "http://[" + gateway + "]:8080/router"
			}
			if response == nil || response.StatusCode != 307 || response.Header.Get("Location") != want {
				t.Fatalf("gateway redirect: %+v", response)
			}
			response.Body.Close()
		}
	}
	program := rewriteProgram(t, `{"url_rewrite":[{"pattern":"^http://line.test/(.*)","action":"header","replacement":"http://$1/"}]}`)
	request, _ := http.NewRequest("GET", "http://line.test/%20", nil)
	var notes []string
	if _, err := program.request(request, "test", request.URL.String(), func(s string) { notes = append(notes, s) }); err != nil {
		t.Fatal(err)
	}
	if request.URL.Host != "line.test" || !strings.Contains(strings.Join(notes, " "), "discarded") {
		t.Fatal("invalid target was applied or not reported")
	}
}

func TestSurgeJQBodyRulesAndFailurePreservation(t *testing.T) {
	for _, test := range []struct{ expression, input, want, note string }{
		{`.id += 1 | .ok = true`, `{"id":9007199254740993}`, `{"id":9007199254740994,"ok":true}`, ""},
		{`.headers |= with_entries(select(.key | test("^X-") | not))`, `{"headers":{"X-Test":"drop","Accept":"keep"}}`, `{"headers":{"Accept":"keep"}}`, ""},
		{`empty`, "{ \"kept\": true }\n", "{ \"kept\": true }\n", ""},
		{"", `{"old":1}`, `{"old":1}`, "jq skipped"},
		{`.`, "invalid json", "invalid json", "jq skipped"},
		{`[broken`, `{"old":1}`, `{"old":1}`, "jq skipped"},
		{`error("failure")`, `{"old":1}`, `{"old":1}`, "failure"},
		{`.a,.b`, `{"a":1,"b":2}`, "1\n2", ""},
		{`"x" * 1024`, `{"old":1}`, `{"old":1}`, "max_body_bytes"},
		{`def f: f; f`, `{"old":1}`, `{"old":1}`, "deadline"},
	} {
		for _, phase := range []string{"request", "response"} {
			t.Run(phase+"/"+test.expression, func(t *testing.T) {
				content, _ := json.Marshal(RewriteConfig{MaxBodyBytes: 256, BodyRewrite: []BodyRewrite{{
					RewriteMatch: RewriteMatch{Pattern: "."}, Phase: phase, JQ: &test.expression,
				}}})
				program := rewriteProgram(t, string(content))
				request, _ := http.NewRequest("POST", "http://example.test/json", strings.NewReader(test.input))
				if test.note == "deadline" {
					ctx, cancel := context.WithTimeout(request.Context(), 30*time.Millisecond)
					defer cancel()
					request = request.WithContext(ctx)
				}
				var notes []string
				note := func(s string) { notes = append(notes, s) }
				var body io.ReadCloser
				if phase == "request" {
					if _, err := program.request(request, "test", request.URL.String(), note); err != nil {
						t.Fatal(err)
					}
					body = request.Body
				} else {
					request.Body.Close()
					response := localRewriteResponse(request, 200, make(http.Header), []byte(test.input))
					if err := program.response(request, response, "test", request.URL.String(), note); err != nil {
						t.Fatal(err)
					}
					body = response.Body
				}
				got, err := io.ReadAll(body)
				body.Close()
				if err != nil || string(got) != test.want {
					t.Fatalf("body %q != %q: %v", got, test.want, err)
				}
				if test.note != "" && !strings.Contains(strings.Join(notes, " "), test.note) {
					t.Fatalf("missing note: %v", notes)
				}
				if processingBodies.used.Load() != 0 {
					t.Fatal("jq leaked body capacity")
				}
			})
		}
	}
}

func TestSurgeJQRunsBeforeScriptsAndScriptExceptionsPreserveMessages(t *testing.T) {
	for _, failure := range []string{`$request.headers['X-Bad']='mutated'; throw Error('boom');`, `setTimeout(()=>{throw Error('callback boom')},0);`, `for(;;){}`} {
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			body, _ := io.ReadAll(request.Body)
			if string(body) != `{"ok":true}` || request.Header.Get("X-Bad") != "" {
				t.Errorf("failed script changed request: %q %v", body, request.Header)
			}
			w.Header().Set("X-Original", "kept")
			_, _ = io.WriteString(w, `{"ok":false}`)
		}))
		engine, saved := testEngine(t, Config{})
		engine.rewrite = rewriteProgram(t, `{"body_rewrite":[{"pattern":".","phase":"request","jq":".ok=true"},{"pattern":".","phase":"response","jq":".ok=true"}]}`).RewriteProgram
		encoded, _ := json.Marshal(ScriptsConfig{HTTP: []HTTPScript{
			{RewriteMatch: RewriteMatch{Pattern: "."}, Phase: "request", RequiresBody: true, Source: failure, TimeoutMS: 30},
			{RewriteMatch: RewriteMatch{Pattern: "."}, Phase: "response", RequiresBody: true,
				Source: `if(JSON.parse($response.body).ok!==true) throw Error('jq order'); $response.headers['X-Bad']='mutated'; throw Error('response boom');`},
		}})
		engine.scripts = scriptProgram(t, string(encoded))
		client := debugClient(t, engine, origin.Listener.Addr().String())
		response, err := client.Post("http://example.test/json", "application/json", strings.NewReader(`{"ok":false}`))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || string(body) != `{"ok":true}` || response.Header.Get("X-Bad") != "" || response.Header.Get("X-Original") != "kept" {
			t.Fatalf("failed response script changed response: %d %q %v", response.StatusCode, body, err)
		}
		detail, _ := saved.Detail(waitComplete(t, saved, 1)[0].ID)
		if !strings.Contains(detail.Note, "script skipped") {
			t.Fatal("missing script failure journal")
		}
		engine.Close()
		origin.Close()
	}
}

func TestSurgeBodySizeDefaultsAndNonstandardMockStatus(t *testing.T) {
	program := rewriteProgram(t, `{"body_rewrite":[{"pattern":".","phase":"request","regex":"^x","replacement":"y"}]}`)
	if program.bodyLimit("request") != 0 || program.bodyLimit("response") != 0 {
		t.Fatal("default body processing should have no size cap")
	}
	data := bytes.Repeat([]byte("x"), 2*1024*1024)
	request, _ := http.NewRequest("POST", "http://example.test/large", bytes.NewReader(data))
	if _, err := program.request(request, "test", request.URL.String(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(request.Body)
	request.Body.Close()
	if err != nil || len(got) != len(data) || got[0] != 'y' {
		t.Fatal("request above the old 1 MiB limit was not rewritten", err)
	}
	request, _ = http.NewRequest("POST", "http://example.test/large", bytes.NewReader(bytes.Repeat([]byte("x"), 32*1024*1024+1)))
	if _, err := program.request(request, "test", request.URL.String(), func(string) {}); err != nil {
		t.Fatal("request beyond the former 32 MiB cap failed", err)
	}
	large, err := io.ReadAll(request.Body)
	request.Body.Close()
	if err != nil || len(large) != 32*1024*1024+1 || large[0] != 'y' {
		t.Fatal("large request was truncated or skipped", err)
	}
	for _, limit := range []int64{0, 1024 * 1024, 10 * 1024 * 1024} {
		encoded, _ := json.Marshal(RewriteConfig{MaxResponseBodyBytes: limit, BodyRewrite: []BodyRewrite{{RewriteMatch: RewriteMatch{Pattern: "."}, Phase: "response", Regex: "^x", Replacement: "y"}}})
		p := rewriteProgram(t, string(encoded))
		request, _ := http.NewRequest("GET", "http://example.test/large", nil)
		response := localRewriteResponse(request, 200, make(http.Header), data)
		if err := p.response(request, response, "test", request.URL.String(), func(string) {}); err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(response.Body)
		response.Body.Close()
		want := byte('x')
		if limit == 0 || limit >= int64(len(data)) {
			want = 'y'
		}
		if err != nil || len(got) != len(data) || got[0] != want {
			t.Fatal("response buffer limit corrupted passthrough", err)
		}
	}
	for _, h2 := range []bool{false, true} {
		engine, _, ca := mitmEngine(t, Config{})
		engine.mitm = mitmConfig(t, `{"enabled":true,"hosts":["origin.test"]}`)
		engine.mitm.H2 = h2
		engine.rewrite = rewriteProgram(t, `{"map_local":[{"pattern":".","status":999,"data":"mock"}]}`).RewriteProgram
		client, _ := mitmClient(t, engine, "127.0.0.1:1", ca, nil, h2)
		response, err := client.Get("https://origin.test/mock")
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 999 || string(data) != "mock" {
			t.Fatalf("HTTP/2=%v nonstandard mock: %v", h2, err)
		}
		engine.Close()
	}
	if processingBodies.used.Load() != 0 {
		t.Fatal("large body retained processing capacity")
	}
}
