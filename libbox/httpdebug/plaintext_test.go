package httpdebug

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func feedPlaintext(t *testing.T, engine *Engine, id, direction string, data []byte, fragment int) {
	t.Helper()
	for len(data) > 0 {
		n := min(fragment, len(data))
		if err := engine.passive.accept(plaintextEvent{Type: "data", ID: id, Direction: direction, Data: append([]byte(nil), data[:n]...)}); err != nil {
			t.Fatal(err)
		}
		data = data[n:]
	}
}
func openPlaintext(t *testing.T, engine *Engine, id string) {
	t.Helper()
	if err := engine.passive.accept(plaintextEvent{Type: "open", ID: id, PID: 123, UID: 10001, Packages: []string{"com.test"}}); err != nil {
		t.Fatal(err)
	}
}
func closePlaintext(t *testing.T, engine *Engine, id string) {
	t.Helper()
	if err := engine.passive.accept(plaintextEvent{Type: "close", ID: id}); err != nil {
		t.Fatal(err)
	}
}

func TestPlaintextHTTP1FragmentsPipeliningAndInformationals(t *testing.T) {
	engine, store := testEngine(t, Config{HTTPS: true, BodyLimit: 4})
	openPlaintext(t, engine, "one")
	// Response parsing must safely wait for the corresponding request.
	feedPlaintext(t, engine, "one", "read", []byte("HTTP/1.1 100 Continue\r\n\r\nHTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\nSet-Cookie: a=1\r\nSet-Cookie: a=2\r\n\r\n6\r\nabcdef\r\n0\r\n\r\nHTTP/1.1 204 No Content\r\n\r\n"), 3)
	feedPlaintext(t, engine, "one", "write", []byte("POST /first HTTP/1.1\r\nHost: api.test\r\nContent-Length: 3\r\n\r\nabcGET /second HTTP/1.1\r\nHost: api.test\r\n\r\n"), 1)
	items := waitComplete(t, store, 2)
	for _, item := range items {
		d, err := store.Detail(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if d.Error != "" || !d.Request.BodyComplete || !d.Response.BodyComplete || d.Metadata.PID != 123 || d.Metadata.CaptureSource != "eBPF" {
			t.Fatalf("bad capture: %+v", d)
		}
		if strings.Contains(d.Request.URL, "first") {
			if string(d.RequestBody) != "abc" || string(d.ResponseBody) != "abcd" || !d.Response.BodyTruncated || d.Response.BodyBytes != 6 {
				t.Fatalf("body framing failed: %+v", d)
			}
			count := 0
			for _, header := range d.Response.Headers {
				if header.Name == "Set-Cookie" {
					count++
				}
			}
			if count != 2 {
				t.Fatal("repeated response headers lost")
			}
		}
	}
	closePlaintext(t, engine, "one")
}

func TestPlaintextGapNeverRestartsOrExportsPartialBody(t *testing.T) {
	engine, store := testEngine(t, Config{HTTPS: true})
	openPlaintext(t, engine, "gap")
	feedPlaintext(t, engine, "gap", "write", []byte("POST / HTTP/1.1\r\nHost: api.test\r\nContent-Length: 10\r\n\r\nabc"), 4096)
	deadline := time.Now().Add(time.Second)
	for store.Page(10, "").Total == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	reason := "TLS call tracking was lost: " + strings.Repeat("x", 4096)
	if err := engine.passive.accept(plaintextEvent{Type: "data", ID: "gap", Direction: "write", Gap: true, Error: reason}); err != nil {
		t.Fatal(err)
	}
	item := waitComplete(t, store, 1)[0]
	d, _ := store.Detail(item.ID)
	if d.Error == "" || d.Request.BodyComplete {
		t.Fatal("gap claimed a complete request")
	}
	if !strings.HasPrefix(d.Error, "TLS call tracking was lost:") || len(d.Error) > 1100 || !strings.Contains(d.Error, "reconnect") {
		t.Fatal("connection gap explanation was lost or unbounded", d.Error)
	}
	if _, err := store.Curl(item.ID); err == nil {
		t.Fatal("partial upload exported as complete")
	}
	feedPlaintext(t, engine, "gap", "write", []byte("GET /fake HTTP/1.1\r\nHost: api.test\r\n\r\n"), 4096)
	if store.Page(10, "").Total != 1 {
		t.Fatal("flow restarted after loss")
	}
}

func TestPlaintextRejectsPreexistingConnectionsAndOversizedHeaders(t *testing.T) {
	engine, store := testEngine(t, Config{HTTPS: true})
	feedPlaintext(t, engine, "old", "write", []byte("GET / HTTP/1.1\r\nHost: api.test\r\n\r\n"), 4096)
	if store.Page(1, "").Total != 0 {
		t.Fatal("connection without SSL_new was captured")
	}
	openPlaintext(t, engine, "large")
	feedPlaintext(t, engine, "large", "write", []byte("GET / HTTP/1.1\r\nHost: api.test\r\nX-Large: "+strings.Repeat("x", MaxHeaderBytes)+"\r\n\r\n"), 4096)
	closePlaintext(t, engine, "large")
	engine.Close()
	if store.Page(1, "").Total != 0 || engine.skipped.Load() == 0 {
		t.Fatal("oversized headers were accepted")
	}
}

func h2Headers(t *testing.T, framer *http2.Framer, stream uint32, end bool, fields ...string) {
	t.Helper()
	var buffer bytes.Buffer
	encoder := hpack.NewEncoder(&buffer)
	for i := 0; i < len(fields); i += 2 {
		if err := encoder.WriteField(hpack.HeaderField{Name: fields[i], Value: fields[i+1]}); err != nil {
			t.Fatal(err)
		}
	}
	if err := framer.WriteHeaders(http2.HeadersFrameParam{StreamID: stream, EndStream: end, EndHeaders: true, BlockFragment: buffer.Bytes()}); err != nil {
		t.Fatal(err)
	}
}

func TestPlaintextHTTP2MultiplexingAndCoalescedHostFilters(t *testing.T) {
	for iteration := 0; iteration < 10; iteration++ {
		engine, store := testEngine(t, Config{HTTPS: true, IncludeHosts: []string{"selected.test"}})
		var request, response bytes.Buffer
		request.WriteString(http2.ClientPreface)
		req, res := http2.NewFramer(&request, nil), http2.NewFramer(&response, nil)
		_ = req.WriteSettings()
		_ = res.WriteSettings()
		h2Headers(t, req, 1, true, ":method", "GET", ":scheme", "https", ":authority", "other.test", ":path", "/")
		h2Headers(t, req, 3, false, ":method", "POST", ":scheme", "https", ":authority", "selected.test", ":path", "/data", "content-length", "3")
		_ = req.WriteData(3, true, []byte("abc"))
		h2Headers(t, res, 3, false, ":status", "200", "content-length", "6", "set-cookie", "a=1", "set-cookie", "b=2")
		_ = res.WriteData(3, true, []byte("answer"))
		h2Headers(t, res, 1, true, ":status", "204")
		id := fmt.Sprint(iteration)
		openPlaintext(t, engine, id)
		feedPlaintext(t, engine, id, "read", response.Bytes(), 7)
		feedPlaintext(t, engine, id, "write", request.Bytes(), 2)
		closePlaintext(t, engine, id)
		items := waitComplete(t, store, 1)
		d, _ := store.Detail(items[0].ID)
		if d.Error != "" || d.Request.Protocol != "HTTP/2.0" || d.Response.Status != 200 ||
			string(d.RequestBody) != "abc" || string(d.ResponseBody) != "answer" || !d.Response.BodyComplete {
			t.Fatalf("HTTP/2 capture failed: %+v", d)
		}
		engine.Close()
	}
}

func TestPlaintextIncompleteSSEAndHTTP2LengthMismatch(t *testing.T) {
	engine, store := testEngine(t, Config{HTTPS: true})
	openPlaintext(t, engine, "sse")
	feedPlaintext(t, engine, "sse", "write", []byte("GET /events HTTP/1.1\r\nHost: api.test\r\n\r\n"), 4096)
	feedPlaintext(t, engine, "sse", "read", []byte("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nTransfer-Encoding: chunked\r\n\r\na\r\ndata: hi\n\n\r\n"), 4096)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		page := store.Page(10, "")
		if len(page.Items) > 0 && page.Items[0].Bytes >= 10 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	engine.Close()
	d, _ := store.Detail(waitComplete(t, store, 1)[0].ID)
	if d.Response.BodyComplete || d.Error == "" {
		t.Fatal("stopped SSE claimed complete")
	}

	engine, store = testEngine(t, Config{HTTPS: true})
	var request, response bytes.Buffer
	request.WriteString(http2.ClientPreface)
	req, res := http2.NewFramer(&request, nil), http2.NewFramer(&response, nil)
	_ = req.WriteSettings()
	_ = res.WriteSettings()
	h2Headers(t, req, 1, true, ":method", "GET", ":scheme", "https", ":authority", "api.test", ":path", "/")
	h2Headers(t, res, 1, false, ":status", "200", "content-length", "7")
	_ = res.WriteData(1, true, []byte("short"))
	openPlaintext(t, engine, "length")
	feedPlaintext(t, engine, "length", "write", request.Bytes(), 4096)
	feedPlaintext(t, engine, "length", "read", response.Bytes(), 4096)
	closePlaintext(t, engine, "length")
	d, _ = store.Detail(waitComplete(t, store, 1)[0].ID)
	if d.Response.BodyComplete || !strings.Contains(d.Error, "Content-Length") {
		t.Fatalf("length mismatch hidden: %+v", d)
	}
}

func TestPlaintextCollectorExitStopsSessionAndClosesSource(t *testing.T) {
	managerMu.Lock()
	store = nil
	lastCaptureError = ""
	managerMu.Unlock()
	t.Cleanup(func() { Stop(); managerMu.Lock(); store = nil; managerMu.Unlock() })
	if err := Start(t.TempDir(), `{"https":true}`, nil, nil); err == nil {
		t.Fatal("HTTPS started without eBPF")
	}
	reader, writer := io.Pipe()
	closed := make(chan struct{}, 1)
	if err := Start(t.TempDir(), `{"https":true}`, reader, func() { closed <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(writer)
	_ = encoder.Encode(plaintextEvent{Type: "ready", Version: 1, Libraries: 1})
	_ = writer.Close()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("collector exit left capture active")
	}
	if state := Status(); state.Active || state.Error == "" {
		t.Fatalf("collector failure not reported: %+v", state)
	}
}

func TestPlaintextLibraryWarningsKeepCaptureAndCountsActive(t *testing.T) {
	ClearCaptureLogs()
	t.Cleanup(ClearCaptureLogs)
	managerMu.Lock()
	store = nil
	lastCaptureError = ""
	managerMu.Unlock()
	t.Cleanup(func() { Stop(); managerMu.Lock(); store = nil; managerMu.Unlock() })
	reader, writer := io.Pipe()
	defer writer.Close()
	if err := Start(t.TempDir(), `{"https":true}`, reader, func() {}); err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(writer)
	warning := "Chrome/Chromium HTTPS plaintext is unavailable: unverified function layout"
	for _, event := range []plaintextEvent{
		{Type: "ready", Version: 1, Libraries: 1, Scanning: true, Error: warning},
		{Type: "status", Version: 1, Libraries: 2, Scanning: true, Error: warning},
		{Type: "log", Version: 1, Time: 1700000000000, Level: "debug", Message: "libssl.so SSL_read=0x1020"},
		{Type: "open", ID: "warning", PID: 42, UID: 10001},
		{Type: "data", ID: "warning", Direction: "write", Data: []byte("GET / HTTP/1.1\r\nHost: supported.test\r\n\r\n")},
		{Type: "data", ID: "warning", Direction: "read", Data: []byte("HTTP/1.1 204 No Content\r\n\r\n")},
		{Type: "close", ID: "warning"},
	} {
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	items := waitComplete(t, store, 1)
	if state := Status(); !state.Active || state.Libraries != 2 || !state.Scanning || state.Error != warning {
		t.Fatalf("library warning was lost or stopped capture: %+v", state)
	}
	detail, err := store.Detail(items[0].ID)
	if err != nil || detail.Error != "" || detail.Response.Status != 204 {
		t.Fatalf("warning interrupted a supported application: %+v, %v", detail, err)
	}
	if err := encoder.Encode(plaintextEvent{Type: "status", Version: 1, Libraries: 2, Error: warning}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for Status().Scanning && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if state := Status(); !state.Active || state.Scanning || state.Libraries != 2 || state.Error != warning {
		t.Fatalf("discovery completion lost capture state: %+v", state)
	}
	Stop()
	var found bool
	for _, log := range CaptureLogs() {
		if log.Message == "libssl.so SSL_read=0x1020" {
			found = log.Level == "debug" && log.ReceivedAt == 1700000000000
		}
	}
	if !found {
		t.Fatal("diagnostics were lost without a visible log reader, or after stopping")
	}
	ClearCaptureLogs()
	if len(CaptureLogs()) != 0 || store.Page(10, "").Total != 1 {
		t.Fatal("clearing diagnostics removed requests or retained old log entries")
	}
}

func TestPlaintextDiagnosticsKeepEveryFailureAndCollectorExit(t *testing.T) {
	managerMu.Lock()
	store = nil
	lastCaptureError = ""
	managerMu.Unlock()
	ClearCaptureLogs()
	t.Cleanup(func() { Stop(); ClearCaptureLogs(); managerMu.Lock(); store = nil; managerMu.Unlock() })
	reader, writer := io.Pipe()
	defer writer.Close()
	closed := make(chan struct{}, 1)
	if err := Start(t.TempDir(), `{"https":true}`, reader, func() { closed <- struct{}{} }); err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(writer)
	if err := encoder.Encode(plaintextEvent{Type: "ready", Version: 1, Libraries: 1}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if err := encoder.Encode(plaintextEvent{Type: "log", Version: 1, Level: "warn", Message: fmt.Sprintf("incompatible-library-%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := encoder.Encode(plaintextEvent{Type: "error", Error: "perf events lost"}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("collector error left the session active")
	}
	if state := Status(); state.Active || !strings.Contains(state.Error, "perf events lost") {
		t.Fatalf("collector failure did not stop capture: %+v", state)
	}
	var warnings int
	var failure bool
	for _, log := range CaptureLogs() {
		if log.Level == "warn" && strings.HasPrefix(log.Message, "incompatible-library-") {
			warnings++
		}
		failure = failure || log.Level == "error" && strings.Contains(log.Message, "perf events lost")
	}
	if warnings != 6 || !failure {
		t.Fatalf("history discarded library or final errors: warnings=%d failure=%t", warnings, failure)
	}
}

func TestPlaintextCloseDelimitedBodyIsNotClaimedCompleteOnSSLFree(t *testing.T) {
	engine, store := testEngine(t, Config{HTTPS: true})
	openPlaintext(t, engine, "no-length")
	feedPlaintext(t, engine, "no-length", "write", []byte("GET / HTTP/1.1\r\nHost: api.test\r\n\r\n"), 4096)
	feedPlaintext(t, engine, "no-length", "read", []byte("HTTP/1.0 200 OK\r\n\r\nprefix"), 4096)
	closePlaintext(t, engine, "no-length")
	detail, _ := store.Detail(waitComplete(t, store, 1)[0].ID)
	if detail.Response.BodyComplete || !strings.Contains(detail.Error, "verifiable HTTP body length") {
		t.Fatalf("SSL_free cannot prove a close-delimited body finished: %+v", detail)
	}
}

func TestPlaintextHEADWithoutLengthKeepsCapturingTheConnection(t *testing.T) {
	engine, store := testEngine(t, Config{HTTPS: true})
	openPlaintext(t, engine, "head")
	feedPlaintext(t, engine, "head", "write", []byte("HEAD /metadata HTTP/1.1\r\nHost: api.test\r\n\r\nGET /body HTTP/1.1\r\nHost: api.test\r\n\r\n"), 17)
	feedPlaintext(t, engine, "head", "read", []byte("HTTP/1.1 200 OK\r\n\r\nHTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok"), 11)
	closePlaintext(t, engine, "head")
	for _, item := range waitComplete(t, store, 2) {
		detail, err := store.Detail(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Error != "" || !detail.Response.BodyComplete {
			t.Fatalf("bodyless HEAD ended connection capture: %+v", detail)
		}
		if detail.Request.Method == "HEAD" && len(detail.ResponseBody) != 0 ||
			detail.Request.Method == "GET" && string(detail.ResponseBody) != "ok" {
			t.Fatalf("response boundary after HEAD was lost: %+v", detail)
		}
	}
}
