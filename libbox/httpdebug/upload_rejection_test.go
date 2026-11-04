package httpdebug

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func TestRejectedHTTP1UploadDoesNotWaitForRemainingBody(t *testing.T) {
	for _, processing := range []struct {
		name, rewrite, scripts string
		status                 int
	}{
		{
			name:    "rewrite limit",
			rewrite: `{"enabled":true,"max_body_bytes":4,"body_rewrite":[{"pattern":".","phase":"request","regex":"old","replacement":"new"}]}`,
			status:  http.StatusRequestEntityTooLarge,
		},
		{
			name:    "script limit",
			scripts: `{"enabled":true,"max_body_bytes":4,"http":[{"pattern":".","phase":"request","requires_body":true,"source":"$done();"}]}`,
			status:  http.StatusRequestEntityTooLarge,
		},
		{
			name:    "script error before reading",
			scripts: `{"enabled":true,"http":[{"pattern":".","phase":"request","source":"throw new Error('rejected');"}]}`,
			status:  http.StatusBadGateway,
		},
	} {
		for _, upload := range []struct{ name, headers, body string }{
			{"content length", "Content-Length: 6\r\n", "12345"},
			{"chunked", "Transfer-Encoding: chunked\r\n", "5\r\n12345\r\n"},
		} {
			t.Run(processing.name+"/"+upload.name, func(t *testing.T) {
				e, saved := testEngine(t, Config{})
				if processing.rewrite != "" {
					e.rewrite = rewriteProgram(t, processing.rewrite).RewriteProgram
				}
				if processing.scripts != "" {
					e.scripts = scriptProgram(t, processing.scripts)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				client, inbound, err := mitmTCPPair(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				defer inbound.Close()
				deadline, _ := ctx.Deadline()
				if err := client.SetDeadline(deadline); err != nil {
					t.Fatal(err)
				}
				closed := make(chan error, 1)
				var dials atomic.Int32
				if !e.Handle(context.Background(), inbound, Metadata{Host: "example.test", Port: 80, Protocol: "http"},
					func(context.Context) (net.Conn, error) {
						dials.Add(1)
						return nil, errors.New("rejected upload reached upstream")
					}, nil, func(err error) { closed <- err }) {
					t.Fatal("upload connection was not accepted")
				}
				// Send limit+1 bytes, leaving either one declared byte or the
				// terminating chunk unsent. The client stays connected and reads.
				if _, err := io.WriteString(client, "POST /upload HTTP/1.1\r\nHost: example.test\r\n"+upload.headers+"\r\n"+upload.body); err != nil {
					t.Fatal(err)
				}
				reader := bufio.NewReader(client)
				response, err := http.ReadResponse(reader, &http.Request{Method: http.MethodPost})
				if err != nil {
					t.Fatal("rejection waited for the unfinished upload:", err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || response.StatusCode != processing.status || !response.Close || !strings.Contains(string(body), "request processing failed") {
					t.Fatalf("incomplete rejection: status=%d close=%v body=%q error=%v", response.StatusCode, response.Close, body, err)
				}
				if _, err := reader.ReadByte(); err != io.EOF {
					t.Fatalf("rejected upload connection remained open: %v", err)
				}
				select {
				case err := <-closed:
					if err != nil {
						t.Fatal("rejected connection failed to finish:", err)
					}
				case <-ctx.Done():
					t.Fatal("engine retained the rejected connection")
				}
				if dials.Load() != 0 || processingBodies.used.Load() != 0 {
					t.Fatal("rejected upload reached upstream or retained its body budget")
				}
				items := saved.Page(MaxEntries, "").Items
				if len(items) != 1 || !items[0].Complete || items[0].Error == "" {
					t.Fatalf("rejected request did not finish recording: %+v", items)
				}
			})
		}
	}
}

func TestRejectedHTTP2UploadKeepsConnectionUsable(t *testing.T) {
	e, saved := testEngine(t, Config{})
	e.rewrite = rewriteProgram(t, `{"enabled":true,"max_body_bytes":4,"body_rewrite":[{"pattern":"/upload$","phase":"request","regex":"old","replacement":"new"}],"map_local":[{"pattern":"/ok$","data":"ok"}]}`).RewriteProgram
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	client, inbound, err := mitmTCPPair(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	defer inbound.Close()
	deadline, _ := ctx.Deadline()
	if err := client.SetDeadline(deadline); err != nil {
		t.Fatal(err)
	}
	if !e.Handle(context.Background(), inbound, Metadata{Host: "example.test", Port: 80, Protocol: "http"},
		func(context.Context) (net.Conn, error) {
			return nil, errors.New("local response reached upstream")
		}, nil, nil) {
		t.Fatal("upload connection was not accepted")
	}
	transport := &http2.Transport{AllowHTTP: true}
	connection, err := transport.NewClientConn(client)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	reader, writer := io.Pipe()
	defer writer.Close()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://example.test/upload", reader)
	request.ContentLength = 6
	type result struct {
		response *http.Response
		err      error
	}
	responses := make(chan result, 1)
	go func() {
		response, err := connection.RoundTrip(request)
		responses <- result{response, err}
	}()
	if _, err := writer.Write([]byte("12345")); err != nil {
		t.Fatal(err)
	}
	var got result
	select {
	case got = <-responses:
	case <-ctx.Done():
		t.Fatal("HTTP/2 rejection waited for upload completion")
	}
	if got.err != nil {
		t.Fatal("HTTP/2 upload was reset before returning its rejection:", got.err)
	}
	body, err := io.ReadAll(got.response.Body)
	got.response.Body.Close()
	if err != nil || got.response.StatusCode != http.StatusRequestEntityTooLarge || got.response.ProtoMajor != 2 || !strings.Contains(string(body), "request processing failed") {
		t.Fatalf("incomplete HTTP/2 rejection: status=%d body=%q error=%v", got.response.StatusCode, body, err)
	}
	// Reuse this exact connection so reconnecting cannot hide a connection-wide
	// deadline or Connection: close accidentally applied to HTTP/2.
	next, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.test/ok", nil)
	response, err := connection.RoundTrip(next)
	if err != nil {
		t.Fatal("HTTP/2 rejection closed the connection:", err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || response.ProtoMajor != 2 || string(body) != "ok" {
		t.Fatalf("HTTP/2 connection failed after rejection: status=%d body=%q error=%v", response.StatusCode, body, err)
	}
	waitComplete(t, saved, 2)
	if processingBodies.used.Load() != 0 {
		t.Fatal("HTTP/2 rejection retained its body budget")
	}
}
