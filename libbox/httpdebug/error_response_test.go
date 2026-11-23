package httpdebug

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestLocalHTTPErrorsRecordTheResponseReturnedToHTTP1AndHTTP2(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		for _, bodyLimit := range []int64{-1, 7} {
			for _, test := range []struct {
				name, method, body, rewrite, scripts, cause string
				status                                      int
				upstreamFailure                             bool
			}{
				{
					name: "request limit", method: http.MethodPost, body: "12345",
					rewrite: `{"enabled":true,"max_body_bytes":4,"body_rewrite":[{"pattern":".","phase":"request","regex":"old","replacement":"new"}]}`,
					status:  http.StatusRequestEntityTooLarge, cause: "max_body_bytes",
				},
				{
					name: "request processing", method: http.MethodGet,
					scripts: `{"http":[{"pattern":".","phase":"request","source":"$done({intentionalFailure:true});"}]}`,
					status:  http.StatusBadGateway, cause: "intentionalFailure",
				},
				{
					name: "upstream connection", method: http.MethodGet,
					status: http.StatusBadGateway, cause: "origin unavailable", upstreamFailure: true,
				},
				{
					name: "response processing", method: http.MethodGet,
					scripts: `{"http":[{"pattern":".","phase":"response","source":"$done({intentionalFailure:true});"}]}`,
					status:  http.StatusBadGateway, cause: "intentionalFailure",
				},
				{
					name: "HEAD upstream connection", method: http.MethodHead,
					status: http.StatusBadGateway, cause: "origin unavailable", upstreamFailure: true,
				},
				{
					name: "HEAD request processing", method: http.MethodHead,
					scripts: `{"http":[{"pattern":".","phase":"request","source":"$done({intentionalFailure:true});"}]}`,
					status:  http.StatusBadGateway, cause: "intentionalFailure",
				},
			} {
				t.Run(fmt.Sprintf("h2=%t/limit=%d/%s", h2, bodyLimit, test.name), func(t *testing.T) {
					e, saved := testEngine(t, Config{BodyLimit: bodyLimit})
					if test.rewrite != "" {
						e.rewrite = rewriteProgram(t, test.rewrite).RewriteProgram
					}
					if test.scripts != "" {
						e.scripts = scriptProgram(t, test.scripts)
					}
					target, _ := url.Parse("http://example.test")
					upstream := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
						if test.upstreamFailure {
							return nil, errors.New(test.cause)
						}
						return &http.Response{StatusCode: http.StatusOK, Proto: "HTTP/1.1", Header: http.Header{"X-Upstream": {"discarded"}},
							Body: io.NopCloser(strings.NewReader("upstream")), ContentLength: 8, Request: request}, nil
					})
					server := httptest.NewUnstartedServer(e.handler(Metadata{Host: "example.test", Port: 80, Protocol: "http"}, target, upstream, nil))
					server.EnableHTTP2 = h2
					server.StartTLS()
					defer server.Close()
					client := server.Client()
					client.Timeout = 3 * time.Second
					client.Transport.(*http.Transport).ForceAttemptHTTP2 = h2
					request, _ := http.NewRequest(test.method, server.URL+"/error", strings.NewReader(test.body))
					response, err := client.Do(request)
					if err != nil {
						t.Fatal(err)
					}
					body, err := io.ReadAll(response.Body)
					response.Body.Close()
					wantProtocol := 1
					if h2 {
						wantProtocol = 2
					}
					if err != nil || response.StatusCode != test.status || response.ProtoMajor != wantProtocol ||
						test.method != http.MethodHead && !strings.Contains(string(body), "B-box:") ||
						test.method == http.MethodHead && len(body) != 0 {
						t.Fatalf("wrong client response: status=%d protocol=%s body=%q error=%v", response.StatusCode, response.Proto, body, err)
					}
					items := waitComplete(t, saved, 1)
					detail, err := saved.Detail(items[0].ID)
					if err != nil {
						t.Fatal(err)
					}
					captured := len(body)
					if bodyLimit > 0 {
						captured = min(captured, int(bodyLimit))
					}
					if items[0].Status != test.status || !strings.Contains(detail.Error, test.cause) ||
						detail.Response.Status != test.status || detail.Response.Protocol != response.Proto ||
						!detail.Complete || !detail.Response.BodyComplete || detail.Response.BodyBytes != int64(len(body)) ||
						detail.Response.CapturedBytes != int64(captured) || detail.Response.BodyTruncated != (captured < len(body)) ||
						detail.ResponsePreview.Text != string(body[:captured]) || detail.ResponsePreview.HasMore {
						t.Fatalf("capture differs from the returned response: %+v error=%v", detail, err)
					}
					for _, name := range []string{"Content-Type", "X-Content-Type-Options", "Connection"} {
						want := response.Header.Get(name)
						// Go's HTTP/1 client removes Connection: close from Header
						// and exposes it through Close. The capture keeps the header.
						if name == "Connection" && wantProtocol == 1 && response.Close {
							want = "close"
						}
						if got := recordedErrorHeader(detail.Response, name); got != want {
							t.Fatalf("response header %s was not recorded: got %q want %q", name, got, want)
						}
					}
					if recordedErrorHeader(detail.Response, "X-Upstream") != "" {
						t.Fatal("failed response processing recorded the unsent upstream headers")
					}
				})
			}
		}
	}
}

func recordedErrorHeader(message Message, name string) string {
	for _, header := range message.Headers {
		if strings.EqualFold(header.Name, name) {
			return header.Value
		}
	}
	return ""
}

type failingErrorWriter struct {
	header http.Header
	status int
	wrote  string
	limit  int
	err    error
}

func (w *failingErrorWriter) Header() http.Header    { return w.header }
func (w *failingErrorWriter) WriteHeader(status int) { w.status = status }
func (w *failingErrorWriter) Write(p []byte) (int, error) {
	n := min(len(p), w.limit)
	w.wrote += string(p[:n])
	return n, w.err
}

func TestLocalHTTPErrorWriteFailureRetainsOnlyAcceptedBytes(t *testing.T) {
	for _, test := range []struct {
		name  string
		limit int
		err   error
	}{
		{"no body accepted", 0, io.ErrClosedPipe},
		{"partial body accepted", 5, io.ErrClosedPipe},
		{"short write without error", 5, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			e, saved := testEngine(t, Config{BodyLimit: 3})
			target, _ := url.Parse("http://example.test")
			upstream := roundTripperFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("origin unavailable") })
			request := httptest.NewRequest(http.MethodGet, "http://example.test/error", nil)
			writer := &failingErrorWriter{header: make(http.Header), limit: test.limit, err: test.err}
			e.handler(Metadata{Host: "example.test", Port: 80, Protocol: "http"}, target, upstream, nil).ServeHTTP(writer, request)
			detail, err := saved.Detail(waitComplete(t, saved, 1)[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			captured := min(len(writer.wrote), 3)
			if writer.status != http.StatusBadGateway || detail.Response.Status != writer.status ||
				detail.Response.BodyComplete || detail.Response.BodyBytes != int64(len(writer.wrote)) ||
				detail.Response.CapturedBytes != int64(captured) || detail.ResponsePreview.Text != writer.wrote[:captured] ||
				!strings.Contains(detail.Error, "origin unavailable") {
				t.Fatalf("failed write fabricated a complete body or lost accepted bytes: %+v %v", detail, err)
			}
		})
	}
}

func TestCancelledHTTPExchangeDoesNotRecordAnUnsentErrorResponse(t *testing.T) {
	e, saved := testEngine(t, Config{})
	target, _ := url.Parse("http://example.test")
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "http://example.test/error", nil).WithContext(ctx)
	upstream := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		cancel()
		return nil, request.Context().Err()
	})
	writer := &failingErrorWriter{header: make(http.Header), limit: 1024}
	e.handler(Metadata{Host: "example.test", Port: 80, Protocol: "http"}, target, upstream, nil).ServeHTTP(writer, request)
	detail, err := saved.Detail(waitComplete(t, saved, 1)[0].ID)
	if err != nil || writer.status != 0 || writer.wrote != "" || detail.Response.Status != 0 ||
		detail.Response.BodyBytes != 0 || detail.Response.CapturedBytes != 0 || detail.Response.BodyComplete ||
		!strings.Contains(detail.Error, context.Canceled.Error()) {
		t.Fatalf("cancelled exchange recorded an error response it did not send: %+v %v", detail, err)
	}
}

func TestScriptAbortDoesNotRecordAnUnsentLocalOrUpstreamResponse(t *testing.T) {
	for _, phase := range []string{"request", "response"} {
		t.Run(phase, func(t *testing.T) {
			e, saved := testEngine(t, Config{})
			e.scripts = scriptProgram(t, fmt.Sprintf(`{"http":[{"pattern":".","phase":%q,"source":"$done({abort:true});"}]}`, phase))
			target, _ := url.Parse("http://example.test")
			upstream := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Proto: "HTTP/1.1", Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader("not sent")), ContentLength: 8, Request: request}, nil
			})
			writer := &failingErrorWriter{header: make(http.Header), limit: 1024}
			request := httptest.NewRequest(http.MethodGet, "http://example.test/error", nil)
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				e.handler(Metadata{Host: "example.test", Port: 80, Protocol: "http"}, target, upstream, nil).ServeHTTP(writer, request)
			}()
			detail, err := saved.Detail(waitComplete(t, saved, 1)[0].ID)
			if err != nil || recovered != http.ErrAbortHandler || writer.status != 0 || writer.wrote != "" ||
				detail.Response.Status != 0 || detail.Response.BodyComplete || detail.Response.BodyBytes != 0 || detail.Response.CapturedBytes != 0 {
				t.Fatalf("script abort fabricated a response: %+v recovered=%v error=%v", detail, recovered, err)
			}
		})
	}
}

func TestLocalHTTPAuthorityAndConnectErrorsAreRecordedWhenRecordingIsEnabled(t *testing.T) {
	for _, test := range []struct {
		name, host, method, protocol string
		status                       int
	}{
		{"invalid authority", "user@example.test", http.MethodGet, "http", http.StatusBadRequest},
		{"different TLS origin", "other.test", http.MethodGet, "tls", http.StatusMisdirectedRequest},
		{"nested CONNECT", "example.test", http.MethodConnect, "http", http.StatusNotImplemented},
	} {
		t.Run(test.name, func(t *testing.T) {
			e, saved := testEngine(t, Config{})
			target, _ := url.Parse("https://example.test")
			request := httptest.NewRequest(test.method, "https://example.test/error", nil)
			request.Host = test.host
			writer := httptest.NewRecorder()
			e.handler(Metadata{Host: "example.test", Port: 443, Protocol: test.protocol}, target, nil, nil).ServeHTTP(writer, request)
			detail, err := saved.Detail(waitComplete(t, saved, 1)[0].ID)
			if err != nil || writer.Code != test.status || detail.Response.Status != writer.Code ||
				!detail.Response.BodyComplete || detail.ResponsePreview.Text != writer.Body.String() || detail.Error == "" {
				t.Fatalf("local request rejection did not record the returned response: %+v %v", detail, err)
			}
		})
	}
}

func TestStoppedServiceErrorDoesNotRecreateRecording(t *testing.T) {
	e, saved := testEngine(t, Config{})
	e.Close()
	target, _ := url.Parse("http://example.test")
	writer := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://example.test/error", nil)
	e.handler(Metadata{Host: "example.test", Port: 80, Protocol: "http"}, target, nil, nil).ServeHTTP(writer, request)
	if writer.Code != http.StatusServiceUnavailable || saved.Page(0, "").Total != 0 {
		t.Fatal("stopped service recreated capture state while rejecting a request")
	}
}
