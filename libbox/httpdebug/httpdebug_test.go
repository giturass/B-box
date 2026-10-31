package httpdebug

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDomainSelection(t *testing.T) {
	for _, test := range []struct {
		name           string
		config         Config
		host, protocol string
		want           bool
	}{
		{"http default", Config{}, "example.com", "http", true},
		{"https off", Config{}, "example.com", "tls", false},
		{"empty decrypt list is unrestricted", Config{HTTPS: true}, "any.example", "tls", true},
		{"exact", Config{HTTPS: true, DecryptHosts: []string{"EXAMPLE.com."}}, "example.com", "tls", true},
		{"exact excludes subdomain", Config{HTTPS: true, DecryptHosts: []string{"example.com"}}, "api.example.com", "tls", false},
		{"wildcard", Config{HTTPS: true, DecryptHosts: []string{"*.example.com"}}, "api.example.com", "tls", true},
		{"wildcard excludes apex", Config{HTTPS: true, DecryptHosts: []string{"*.example.com"}}, "example.com", "tls", false},
		{"suffix boundary", Config{HTTPS: true, DecryptHosts: []string{"*.example.com"}}, "otherexample.com", "tls", false},
		{"exclude wins", Config{HTTPS: true, DecryptHosts: []string{"*.example.com"}, ExcludeHosts: []string{"api.example.com"}}, "api.example.com", "tls", false},
		{"empty list still honors excludes", Config{HTTPS: true, ExcludeHosts: []string{"private.example"}}, "private.example", "tls", false},
		{"capture filter still applies", Config{HTTPS: true, IncludeHosts: []string{"api.example"}}, "other.example", "tls", false},
		{"ipv6", Config{HTTPS: true, DecryptHosts: []string{"::1"}}, "[::1]", "tls", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.config.validate(); err != nil {
				t.Fatal(err)
			}
			if got := test.config.matches(Metadata{Host: test.host, Port: 443, Protocol: test.protocol}); got != test.want {
				t.Fatalf("matches = %v, want %v", got, test.want)
			}
		})
	}
	for _, host := range []string{"*", "https://example.com", "example.com:443", "a..b", "-bad.example", "*.127.0.0.1", ".example.com", "hello world", "example.com/"} {
		c := Config{DecryptHosts: []string{host}}
		if c.validate() == nil {
			t.Errorf("accepted invalid filter %q", host)
		}
	}
	c := Config{Packages: []string{"com.test"}}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	if c.matches(Metadata{Host: "example.com", Port: 80, Protocol: "http"}) {
		t.Fatal("unknown application matched allowlist")
	}
	if !c.matches(Metadata{Host: "example.com", Port: 80, Protocol: "http", Packages: []string{"com.shared", "com.test"}}) {
		t.Fatal("shared UID did not match")
	}
}

func testEngine(t *testing.T, c Config, ca *authority) (*Engine, *Store) {
	t.Helper()
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e := newEngine(c, ca, s)
	t.Cleanup(e.Close)
	return e, s
}

func testCA(t *testing.T) (*authority, *x509.CertPool) {
	t.Helper()
	cert, key, err := GenerateCA()
	if err != nil {
		t.Fatal(err)
	}
	ca, err := loadAuthority(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(cert)) {
		t.Fatal("invalid CA")
	}
	return ca, pool
}

func debugClient(t *testing.T, e *Engine, origin string, secure bool, upstreamRoots, clientRoots *x509.CertPool, rewriteDial ...RewriteDialFunc) *http.Client {
	t.Helper()
	m := Metadata{Host: "example.test", Port: 80, Protocol: "http", Outbound: "selected-proxy", Rule: "test rule", UID: 1234, Packages: []string{"com.test"}}
	if secure {
		m.Port, m.Protocol = 443, "tls"
	}
	transport := &http.Transport{ForceAttemptHTTP2: true, TLSClientConfig: &tls.Config{RootCAs: clientRoots},
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			client, server := net.Pipe()
			if !e.Handle(context.Background(), server, m, func(ctx context.Context) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", origin)
			}, upstreamRoots, nil, rewriteDial...) {
				t.Error("connection not captured")
				client.Close()
				server.Close()
			}
			return client, nil
		}}
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: 5 * time.Second}
}

func waitComplete(t *testing.T, s *Store, count int) []Summary {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		page := s.Page(MaxEntries, "")
		if len(page.Items) == count {
			complete := true
			for _, item := range page.Items {
				complete = complete && item.Complete
			}
			if complete {
				return page.Items
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("incomplete capture: %+v", s.Page(MaxEntries, ""))
	return nil
}

func TestHTTPStreamingBodiesHeadersAndOriginalRoute(t *testing.T) {
	var calls atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		if r.Host != "example.test" || r.Header.Get("X-Forwarded-For") != "original" {
			t.Error("request headers changed")
		}
		if r.URL.RawQuery != "q=1;raw=2&escaped=%2F" {
			t.Error("request query changed")
		}
		w.Header().Add("Set-Cookie", "first=1")
		w.Header().Add("Set-Cookie", "second=2")
		w.WriteHeader(201)
		_, _ = w.Write(body)
	}))
	defer origin.Close()
	e, store := testEngine(t, Config{BodyLimit: 32}, nil)
	client := debugClient(t, e, origin.Listener.Addr().String(), false, nil, nil)
	body := strings.Repeat("payload", 1000)
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest("POST", "http://example.test/path?q=1;raw=2&escaped=%2F", strings.NewReader(body))
		req.Header.Set("X-Forwarded-For", "original")
		response, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(got) != body || response.StatusCode != 201 {
			t.Fatalf("response mismatch: status=%d bytes=%d want=%d error=%v", response.StatusCode, len(got), len(body), err)
		}
	}
	for _, item := range waitComplete(t, store, 2) {
		d, err := store.Detail(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.RequestBody) != 32 || len(d.ResponseBody) != 32 || !d.Request.BodyTruncated || !d.Response.BodyComplete {
			t.Fatalf("limits/completion: %+v", d.Exchange)
		}
		if d.Metadata.Outbound != "selected-proxy" || d.Metadata.UID != 1234 {
			t.Fatal("route metadata lost")
		}
		cookies := 0
		for _, header := range d.Response.Headers {
			if header.Name == "Set-Cookie" {
				cookies++
			}
		}
		if cookies != 2 {
			t.Fatal("repeated headers lost")
		}
		if _, err := store.Curl(item.ID); err == nil {
			t.Fatal("exported a truncated request as replayable")
		}
	}
	if calls.Load() != 2 {
		t.Fatal("request duplicated")
	}
	var har bytes.Buffer
	if err := store.WriteHAR(&har, "POST"); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(har.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPSHTTP2AndUpstreamVerification(t *testing.T) {
	originCA, originRoots := testCA(t)
	leaf, err := originCA.certificate("example.test")
	if err != nil {
		t.Fatal(err)
	}
	var observed atomic.Value
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed.Store(r.Proto)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "HTTP2 body")
	}))
	origin.EnableHTTP2 = true
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{*leaf}}
	origin.StartTLS()
	defer origin.Close()
	ca, roots := testCA(t)
	e, store := testEngine(t, Config{HTTPS: true, DecryptHosts: []string{"example.test"}}, ca)
	client := debugClient(t, e, origin.Listener.Addr().String(), true, originRoots, roots)
	response, err := client.Get("https://example.test/test")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "HTTP2 body" || response.ProtoMajor != 2 || observed.Load() != "HTTP/2.0" {
		t.Fatalf("HTTP2 exchange failed: %v, %s, %v", err, response.Proto, observed.Load())
	}
	d, _ := store.Detail(waitComplete(t, store, 1)[0].ID)
	if d.Request.Protocol != "HTTP/2.0" || d.Response.Protocol != "HTTP/2.0" {
		t.Fatal("HTTP2 metadata missing")
	}

	badEngine, badStore := testEngine(t, Config{HTTPS: true}, ca)
	badClient := debugClient(t, badEngine, origin.Listener.Addr().String(), true, x509.NewCertPool(), roots)
	response, err = badClient.Get("https://example.test/test")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 502 {
		t.Fatal("untrusted upstream was accepted")
	}
	if item := waitComplete(t, badStore, 1)[0]; !strings.Contains(item.Error, "certificate") {
		t.Fatalf("missing certificate error: %s", item.Error)
	}
}

func TestSSEVisibleBeforeCompletionAndStop(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer origin.Close()
	e, store := testEngine(t, Config{}, nil)
	client := debugClient(t, e, origin.Listener.Addr().String(), false, nil, nil)
	response, err := client.Get("http://example.test/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	line, err := bufio.NewReader(response.Body).ReadString('\n')
	if err != nil || line != "data: first\n" {
		t.Fatalf("stream not flushed: %q, %v", line, err)
	}
	page := store.Page(10, "")
	if len(page.Items) != 1 || page.Items[0].Complete {
		t.Fatal("stream visibility/completion incorrect")
	}
	d, _ := store.Detail(page.Items[0].ID)
	if !strings.Contains(string(d.ResponseBody), "first") {
		t.Fatal("stream body not visible")
	}
	stopped := make(chan struct{})
	go func() { e.Close(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop leaked a stream")
	}
}

func TestStoreBoundsPersistenceClearAndCurl(t *testing.T) {
	_, store := testEngine(t, Config{}, nil)
	req, _ := http.NewRequest("GET", "http://example.test/a'b", nil)
	req.Proto = "HTTP/1.1"
	for i := 0; i < MaxEntries+3; i++ {
		id := store.begin(Metadata{Packages: []string{"com.test"}}, req, 16)
		store.finish(id)
	}
	page := store.Page(20, "com.test")
	if page.Total != MaxEntries || len(page.Items) != 20 {
		t.Fatalf("bad page: %d, %d", page.Total, len(page.Items))
	}
	reopened, err := OpenStore(store.directory)
	if err != nil || reopened.Page(1, "").Total != MaxEntries {
		t.Fatalf("persistence: %v", err)
	}
	command, err := reopened.Curl(page.Items[0].ID)
	if err != nil || !strings.Contains(command, "'\"'\"'") {
		t.Fatalf("unsafe cURL quoting: %q, %v", command, err)
	}
	if _, err := reopened.Detail("../../outside"); err == nil {
		t.Fatal("invalid id accepted")
	}
	keep := filepath.Join(store.directory, "unrelated")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	reopened.Clear()
	if reopened.Page(1, "").Total != 0 {
		t.Fatal("clear failed")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("clear removed unrelated file")
	}
}

func TestQUICAndStoppedEngine(t *testing.T) {
	e, _ := testEngine(t, Config{HTTPS: true, BlockQUIC: true, DecryptHosts: []string{"example.test"}}, nil)
	if e.RejectQUIC(Metadata{Port: 443}) || e.RejectQUIC(Metadata{Port: 443, Host: "other.test"}) {
		t.Fatal("QUIC blocked outside selection")
	}
	if !e.RejectQUIC(Metadata{Port: 443, Host: "example.test"}) {
		t.Fatal("selected QUIC not blocked")
	}
	e.Close()
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if e.Handle(context.Background(), a, Metadata{Port: 80, Protocol: "http"}, nil, nil, nil) {
		t.Fatal("closed engine accepted a connection")
	}
}

func TestHARContentDecodingAndCompressionLimit(t *testing.T) {
	compress := func(body string) []byte {
		var buffer bytes.Buffer
		writer := gzip.NewWriter(&buffer)
		if _, err := io.WriteString(writer, body); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return buffer.Bytes()
	}
	for _, body := range []string{"a decoded response", strings.Repeat("abc", previewLimit)} {
		raw := compress(body)
		message := Message{Headers: []Header{{"Content-Encoding", "gzip"}}, BodyComplete: true, BodyBytes: int64(len(raw))}
		content := harContent(message, raw)
		decoded, err := base64.StdEncoding.DecodeString(content["text"].(string))
		if err != nil || string(decoded) != body[:min(len(body), previewLimit)] {
			t.Fatal("HAR text is not the bounded decoded response", err)
		}
		if content["_rawBody"] != base64.StdEncoding.EncodeToString(raw) {
			t.Fatal("HAR lost the original compressed bytes")
		}
		if content["_truncated"] != (len(body) > previewLimit) {
			t.Fatal("HAR did not label its decoded prefix")
		}
		if got := preview(message, raw); got.Encoding != "text" || len(got.Text) > previewLimit {
			t.Fatalf("unbounded compressed preview: %+v", got)
		}
	}
	message := Message{Headers: []Header{{"Content-Encoding", "unsupported"}}, BodyTruncated: true}
	content := harContent(message, []byte{0, 1, 2})
	if _, ok := content["text"]; ok {
		t.Fatal("unknown content encoding was represented as decoded HAR text")
	}
	if got := preview(message, []byte{0, 1, 2}); got.Encoding != "base64" || !got.Truncated {
		t.Fatal("binary preview lost truncation state")
	}
}

func TestWebSocketUpgradeForwardsFramesAndStops(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, stream, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, _ = stream.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
		if err := stream.Flush(); err != nil {
			return
		}
		// Echo opaque frames to verify the upgrade uses bidirectional forwarding.
		_, _ = io.Copy(conn, stream)
	}))
	defer origin.Close()
	e, store := testEngine(t, Config{}, nil)
	client, inbound := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	if !e.Handle(context.Background(), inbound, Metadata{Host: "example.test", Port: 80, Protocol: "http"},
		func(ctx context.Context) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
		}, nil, nil) {
		t.Fatal("upgrade was not captured")
	}
	_, err := io.WriteString(client, "GET /socket HTTP/1.1\r\nHost: example.test\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n\r\n")
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(client)
	response, err := http.ReadResponse(reader, nil)
	if err != nil || response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatal("upgrade failed", err)
	}
	frame := []byte{0x81, 0x02, 'o', 'k'}
	if _, err := client.Write(frame); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, len(frame))
	if _, err := io.ReadFull(reader, echo); err != nil || !bytes.Equal(echo, frame) {
		t.Fatal("upgraded stream changed the frame", err)
	}
	e.Close()
	item := waitComplete(t, store, 1)[0]
	detail, err := store.Detail(item.ID)
	if err != nil || detail.Response.Status != 101 || len(detail.ResponseBody) != 0 || detail.Note == "" {
		t.Fatal("WebSocket payload was mistaken for an HTTP body", err)
	}
}

func TestMissingBodyNeverExportsAsACompleteRequest(t *testing.T) {
	_, store := testEngine(t, Config{}, nil)
	request, _ := http.NewRequest("POST", "http://example.test/", strings.NewReader("required body"))
	id := store.begin(Metadata{}, request, 64)
	store.appendBody(id, false, []byte("required body"))
	store.bodyComplete(id, false)
	store.finish(id)
	if err := os.Remove(filepath.Join(store.directory, id+".request")); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Curl(id); err == nil {
		t.Fatal("missing request bytes were exported as an empty request")
	}
	reopened, err := OpenStore(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := reopened.Detail(id)
	if err != nil || !detail.Request.BodyTruncated || detail.Request.CapturedBytes != 0 {
		t.Fatal("restart did not reconcile the missing body file", err)
	}
}
