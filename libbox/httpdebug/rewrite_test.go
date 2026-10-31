package httpdebug

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

func anyURL() Rule { return Rule{Pattern: `^http://example\.test/`} }

func TestRewritePipelineAndRecordedMessages(t *testing.T) {
	seen := make(chan string, 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		seen <- r.URL.String() + "|" + r.Header.Get("X-Stage") + "|" + r.Header.Get("X-Script") + "|" + string(data)
		w.Header().Set("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		io.WriteString(w, "upstream")
	}))
	defer origin.Close()
	config := Config{RewriteEnabled: true,
		HeaderRewrite: []HeaderRule{
			{Rule: anyURL(), Action: "header-add", Header: "X-Stage", Value: "header"},
			{Rule: anyURL(), Type: responseDirection, Action: "header-add", Header: "X-Response", Value: "yes"},
		},
		URLRewrite: []URLRule{{Rule: Rule{Pattern: `^http://example\.test/old/(.*)`}, Replacement: `http://example.test/new/${1}`}},
		BodyRewrite: []BodyRule{
			{Rule: anyURL(), Regex: "old", Replacement: "body"},
			{Rule: anyURL(), Type: responseDirection, Regex: "upstream", Replacement: "rewritten"},
		},
		Scripts: []ScriptRule{
			{Rule: anyURL(), RequiresBody: true, Source: `$done({headers: {...$request.headers, "X-Script": $request.body}, body: $request.body + "-js"});`},
			{Rule: anyURL(), Type: responseDirection, RequiresBody: true, FullHeaderMode: true,
				Source: `$done({headers: $response.headers, body: $response.body + "-js"});`},
		},
	}
	e, store := testEngine(t, config, nil)
	client := debugClient(t, e, origin.Listener.Addr().String(), false, nil, nil)
	response, err := client.Post("http://example.test/old/item?x=1%3B2", "text/plain", strings.NewReader("old"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "rewritten-js" || response.Header.Get("X-Response") != "yes" || len(response.Header.Values("Set-Cookie")) != 2 {
		t.Fatalf("response: %q %+v %v", body, response.Header, err)
	}
	if got := <-seen; got != "/new/item?x=1%3B2|header|body|body-js" {
		t.Fatal(got)
	}
	items := waitComplete(t, store, 1)
	detail, err := store.Detail(items[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(detail.RequestBody) != "body-js" || string(detail.ResponseBody) != "rewritten-js" || !strings.Contains(detail.OriginalURL, "/old/") {
		t.Fatalf("capture does not describe effective messages: %+v", detail)
	}
	for _, kind := range []string{"Header Rewrite", "URL Rewrite", "Body Rewrite", "Script"} {
		if !strings.Contains(detail.Note, kind) {
			t.Fatalf("missing note %s: %s", kind, detail.Note)
		}
	}
}

func TestLocalResponsesAndRedirectsDoNotDial(t *testing.T) {
	var calls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); io.WriteString(w, "unexpected") }))
	defer origin.Close()
	for _, test := range []struct {
		name   string
		config Config
		method string
		status int
		body   string
	}{
		{"map", Config{MapLocal: []LocalRule{{Rule: anyURL(), Status: 201, Data: "local"}}}, "GET", 201, "local"},
		{"head", Config{MapLocal: []LocalRule{{Rule: anyURL(), Data: "local"}}}, "HEAD", 200, ""},
		{"no content", Config{MapLocal: []LocalRule{{Rule: anyURL(), Status: 204, Data: "ignored"}}}, "GET", 204, ""},
		{"binary", Config{MapLocal: []LocalRule{{Rule: anyURL(), DataType: "base64", Data: "AAEC"}}}, "GET", 200, "\x00\x01\x02"},
		{"script", Config{Scripts: []ScriptRule{{Rule: anyURL(), Source: `$done({response:{status:202,headers:{"X-Local":"yes"},body:"mock"}});`}}}, "GET", 202, "mock"},
		{"redirect", Config{URLRewrite: []URLRule{{Rule: anyURL(), Type: "307", Replacement: "https://other.test/"}}}, "GET", 307, ""},
		{"reject", Config{URLRewrite: []URLRule{{Rule: anyURL(), Type: "reject"}}}, "GET", 403, "B-box: rejected by URL Rewrite"},
	} {
		t.Run(test.name, func(t *testing.T) {
			test.config.RewriteEnabled = true
			e, _ := testEngine(t, test.config, nil)
			client := debugClient(t, e, origin.Listener.Addr().String(), false, nil, nil)
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			request, _ := http.NewRequest(test.method, "http://example.test/", nil)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || response.StatusCode != test.status || string(body) != test.body {
				t.Fatalf("%d %q %v", response.StatusCode, body, err)
			}
			if test.method == "HEAD" && response.ContentLength != 5 {
				t.Fatalf("HEAD length: %d", response.ContentLength)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatal("a local response dialed the origin")
	}
}

func TestRewriteDisabledAndFirstScriptOnly(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(w, r.Body) }))
	defer origin.Close()
	for _, enabled := range []bool{false, true} {
		e, _ := testEngine(t, Config{RewriteEnabled: enabled, BodyRewrite: []BodyRule{{Rule: Rule{Pattern: ".*", Disabled: true}, Regex: ".*", Replacement: "disabled"}},
			Scripts: []ScriptRule{
				{Rule: anyURL(), RequiresBody: true, Source: `$done({body: "first"}); while(true) {}`},
				{Rule: anyURL(), RequiresBody: true, Source: `$done({body: "second"});`},
			}}, nil)
		client := debugClient(t, e, origin.Listener.Addr().String(), false, nil, nil)
		response, err := client.Post("http://example.test/", "text/plain", strings.NewReader("original"))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(response.Body)
		response.Body.Close()
		want := "original"
		if enabled {
			want = "first"
		}
		if string(body) != want {
			t.Fatalf("enabled %v: %q", enabled, body)
		}
	}
}

func TestBodyRewriteCompressionAndOverflow(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate", "br"} {
		t.Run(encoding, func(t *testing.T) {
			var compressed bytes.Buffer
			var writer io.WriteCloser
			switch encoding {
			case "gzip":
				writer = gzip.NewWriter(&compressed)
			case "deflate":
				writer = zlib.NewWriter(&compressed)
			default:
				writer = brotli.NewWriter(&compressed)
			}
			io.WriteString(writer, "before")
			writer.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Encoding", encoding)
				w.Header().Set("ETag", "old-tag")
				w.Write(compressed.Bytes())
			}))
			defer origin.Close()
			e, _ := testEngine(t, Config{RewriteEnabled: true, BodyRewrite: []BodyRule{{Rule: anyURL(), Type: responseDirection, Regex: "before", Replacement: "after"}}}, nil)
			client := debugClient(t, e, origin.Listener.Addr().String(), false, nil, nil)
			client.Transport.(*http.Transport).DisableCompression = true
			response, err := client.Get("http://example.test/")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if string(body) != "after" || response.Header.Get("Content-Encoding") != "" || response.Header.Get("ETag") != "" || response.ContentLength != 5 {
				t.Fatalf("%q %+v", body, response)
			}
		})
	}
	large := strings.Repeat("x", MaxRewriteBody+1024)
	for _, compress := range []bool{false, true} {
		var wire bytes.Buffer
		if compress {
			writer := gzip.NewWriter(&wire)
			io.WriteString(writer, large)
			writer.Close()
		} else {
			wire.WriteString(large)
		}
		origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if compress {
				w.Header().Set("Content-Encoding", "gzip")
			}
			w.Write(wire.Bytes())
		}))
		e, store := testEngine(t, Config{RewriteEnabled: true, BodyRewrite: []BodyRule{{Rule: anyURL(), Type: responseDirection, Regex: "x+", Replacement: "wrong"}}}, nil)
		client := debugClient(t, e, origin.Listener.Addr().String(), false, nil, nil)
		client.Transport.(*http.Transport).DisableCompression = true
		response, err := client.Get("http://example.test/")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		origin.Close()
		if err != nil || !bytes.Equal(body, wire.Bytes()) {
			t.Fatalf("overflow lost wire bytes (compressed %v): %v", compress, err)
		}
		detail, _ := store.Detail(waitComplete(t, store, 1)[0].ID)
		if !strings.Contains(detail.Note, "skipped") {
			t.Fatal(detail.Note)
		}
	}
}

func TestSSEWithBodyRulesStillStreams(t *testing.T) {
	release := make(chan struct{})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "data: last\n\n")
	}))
	defer func() { close(release); origin.Close() }()
	e, _ := testEngine(t, Config{RewriteEnabled: true, BodyRewrite: []BodyRule{{Rule: anyURL(), Type: responseDirection, Regex: "first", Replacement: "wrong"}},
		Scripts: []ScriptRule{{Rule: anyURL(), Type: responseDirection, RequiresBody: true, Source: `$done({body:"wrong"});`}}}, nil)
	client := debugClient(t, e, origin.Listener.Addr().String(), false, nil, nil)
	response, err := client.Get("http://example.test/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	first := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(response.Body, first); err != nil || string(first) != "data: first\n\n" {
		t.Fatalf("stream blocked/changed: %q %v", first, err)
	}
}

func TestScriptTimeoutFramingAndBinaryBody(t *testing.T) {
	for _, source := range []string{`while (true) {}`, `let x = 1;`, `$done({headers:{"Content-Length":"999"}});`, `$done({headers:{"Bad\r\nHeader":"x"}});`, `$done({body: "no requiresBody"});`, `$httpClient.get("https://example.test", () => $done({}));`} {
		e, _ := testEngine(t, Config{RewriteEnabled: true, Scripts: []ScriptRule{{Rule: anyURL(), Source: source, TimeoutMS: 20}}}, nil)
		request, _ := http.NewRequest("POST", "http://example.test/", strings.NewReader("one"))
		started := time.Now()
		if _, err := e.rewriteRequest("", request); err == nil {
			t.Fatalf("invalid script accepted: %s", source)
		}
		if time.Since(started) > time.Second {
			t.Fatal("script did not terminate promptly")
		}
	}
	e, _ := testEngine(t, Config{RewriteEnabled: true, Scripts: []ScriptRule{{Rule: anyURL(), RequiresBody: true, BinaryBodyMode: true,
		Source: `$request.body[0] = 255; $done({body: $request.body});`}}}, nil)
	request, _ := http.NewRequest("POST", "http://example.test/", bytes.NewReader([]byte{0, 1, 2}))
	if _, err := e.rewriteRequest("", request); err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(request.Body)
	request.Body.Close()
	if !bytes.Equal(body, []byte{255, 1, 2}) || request.ContentLength != 3 {
		t.Fatalf("binary body: %v", body)
	}
}

func TestRewriteURLUsesRoutedDialAndVerifiesNewTLSHost(t *testing.T) {
	ca, roots := testCA(t)
	certificate, _ := ca.certificate("replacement.test")
	var oldCalls, newCalls atomic.Int32
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { oldCalls.Add(1) }))
	defer original.Close()
	replacement := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newCalls.Add(1)
		if r.Host != "replacement.test" {
			t.Errorf("rewritten Host: %s", r.Host)
		}
		io.WriteString(w, "routed")
	}))
	replacement.TLS = &tls.Config{Certificates: []tls.Certificate{*certificate}}
	replacement.StartTLS()
	defer replacement.Close()
	e, store := testEngine(t, Config{RewriteEnabled: true, URLRewrite: []URLRule{{Rule: anyURL(), Replacement: "https://replacement.test/"}}}, nil)
	client := debugClient(t, e, original.Listener.Addr().String(), false, roots, nil, func(ctx context.Context, address string) (net.Conn, error) {
		if address != "replacement.test:443" {
			t.Errorf("route destination: %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", replacement.Listener.Addr().String())
	})
	response, err := client.Get("http://example.test/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if string(body) != "routed" || oldCalls.Load() != 0 || newCalls.Load() != 1 {
		t.Fatal("rewrite did not use the new routed destination")
	}
	detail, _ := store.Detail(waitComplete(t, store, 1)[0].ID)
	if detail.Metadata.Outbound != "" || detail.Metadata.Rule != "" || detail.Metadata.Destination != "replacement.test:443" {
		t.Fatalf("misleading route metadata: %+v", detail.Metadata)
	}
	if got := strings.Join(store.Domains(""), ","); got != "example.test,replacement.test" {
		t.Fatal(got)
	}
}

func TestMITMBypassAndPassiveDomainExtraction(t *testing.T) {
	e, store := testEngine(t, Config{HTTPS: true, ExtractHosts: true, BlockQUIC: true,
		DecryptBypassHosts: []string{"pinned.test"}, ExcludeHosts: []string{"excluded.test"}}, nil)
	metadata := Metadata{Host: "pinned.test", Port: 443, Protocol: "tls"}
	if !e.Candidate(metadata) || e.Matches(metadata) || e.RejectQUIC(metadata) {
		t.Fatal("MITM bypass did not preserve encrypted transport")
	}
	e.Observe(metadata, "TLS")
	metadata.Host = "excluded.test"
	e.Observe(metadata, "TLS")
	metadata.Host = ""
	e.Observe(metadata, "TLS")
	if got := strings.Join(store.Domains(""), ","); got != "pinned.test" {
		t.Fatal(got)
	}
	for _, item := range store.Page(300, "").Items {
		if !item.DomainOnly || item.Bytes != 0 || item.Status != 0 {
			t.Fatalf("fabricated HTTP request: %+v", item)
		}
	}
	var har bytes.Buffer
	if err := store.WriteHAR(&har, ""); err != nil || !strings.Contains(har.String(), `"entries":[]`) {
		t.Fatalf("SNI observations were exported as HTTP exchanges: %s %v", har.String(), err)
	}
	store.Clear()
	if len(store.Domains("")) != 0 {
		t.Fatal("domain extraction retained cleared records")
	}
}

func TestRewriteValidationAndBoundedExpansion(t *testing.T) {
	for _, raw := range []string{
		`{"unknown":true}`, `{"headerRewrite":[{"pattern":".","header":"Content-Length","action":"header-add","value":"5"}]}`,
		`{"bodyRewrite":[{"pattern":"(?<=x)y","regex":"a","replacement":"b"}]}`,
		`{"mapLocal":[{"pattern":".","dataType":"base64","data":"!"}]}`,
		`{"scripts":[{"pattern":".","source":"let = ;"}]}`,
		`{"scripts":[{"pattern":".","source":"$done({});","timeoutMs":99999}]}`,
		`{"scripts":[{"pattern":".","source":"$done({});","scriptPath":"https://remote/script.js"}]}`,
	} {
		if _, err := Validate(raw); err == nil {
			t.Fatalf("invalid configuration accepted: %s", raw)
		}
	}
	for _, value := range []string{"http://user:pass@example.test/", "file:///private", "http://example.test:0/", "http://example.test:/"} {
		if _, err := rewriteURL(value); err == nil {
			t.Fatalf("invalid URL accepted: %s", value)
		}
	}
	for _, value := range []string{"example.test/path", "example.test?q=1", "example.test#fragment"} {
		if err := validateHostHeader(value); err == nil {
			t.Fatalf("invalid Host accepted: %s", value)
		}
	}
	pattern := regexp.MustCompile(`(?P<word>foo)(bar)?`)
	for _, replacement := range []string{"$1", "${word}x", "$0-$2", "$$${word}", "$missing", "$", "${", "$01"} {
		got, err := replaceBounded(pattern, "foo foobar", replacement, 4096)
		if err != nil || got != pattern.ReplaceAllString("foo foobar", replacement) {
			t.Fatalf("replacement %q: %q %v", replacement, got, err)
		}
	}
	if _, err := replaceBounded(regexp.MustCompile(".+"), strings.Repeat("x", MaxRewriteBody), strings.Repeat("$0", 65536), MaxRewriteBody); err == nil {
		t.Fatal("unbounded replacement accepted")
	}
	if _, err := replaceBounded(regexp.MustCompile(strings.Repeat("()", 64)), strings.Repeat("x", 4096), "", MaxRewriteBody); err == nil {
		t.Fatal("unbounded capture-index allocation accepted")
	}
	encoded, err := Validate(`{"scripts":[{"pattern":".","source":"$done({});"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	if err := json.Unmarshal([]byte(encoded), &config); err != nil || config.Scripts[0].program == nil {
		t.Fatalf("script did not survive config round-trip: %v", err)
	}
}

func TestRequestBodyLimitsAndChunkedPassthrough(t *testing.T) {
	e, _ := testEngine(t, Config{RewriteEnabled: true,
		BodyRewrite: []BodyRule{{Rule: anyURL(), Regex: "x", Replacement: "changed"}},
		Scripts:     []ScriptRule{{Rule: anyURL(), RequiresBody: true, Source: `$done({headers:{...$request.headers,"X-Script":"ran"},body:"ignored"});`}},
	}, nil)
	request, _ := http.NewRequest("POST", "http://example.test/", strings.NewReader(strings.Repeat("x", MaxRewriteBody+1)))
	if _, err := e.rewriteRequest("", request); err == nil {
		t.Fatal("oversized request body was accepted")
	}
	request.Body.Close()
	for _, expect := range []bool{false, true} {
		request, _ := http.NewRequest("POST", "http://example.test/", strings.NewReader("x"))
		if expect {
			request.Header.Set("Expect", "100-continue")
		} else {
			request.TransferEncoding = []string{"chunked"}
			request.ContentLength = -1
		}
		if _, err := e.rewriteRequest("", request); err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(request.Body)
		request.Body.Close()
		if string(body) != "x" || request.Header.Get("X-Script") != "ran" {
			t.Fatalf("chunked/Expect framing changed: %q %+v", body, request.Header)
		}
	}
}
