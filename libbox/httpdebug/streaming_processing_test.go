package httpdebug

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGRPCStreamsBeforeUploadCompletionAndPreservesTrailers(t *testing.T) {
	frame := []byte{0, 0, 0, 0, 3, 'o', 'l', 'd'}
	cert, roots := originCertificate(t, "origin.test", false)
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		first := make([]byte, len(frame))
		if _, err := io.ReadFull(request.Body, first); err != nil {
			return
		}
		w.Header().Set("Content-Type", "application/grpc+proto")
		w.Header().Set("Trailer", "Grpc-Status")
		w.Write(first)
		w.(http.Flusher).Flush()
		io.Copy(io.Discard, request.Body)
		w.Header().Set("Grpc-Status", "0")
	}))
	origin.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	origin.EnableHTTP2 = true
	origin.StartTLS()
	defer origin.Close()
	e, saved, ca := mitmEngine(t, Config{})
	e.mitm.H2 = true
	defer e.Close()
	e.rewrite = rewriteProgram(t, `{"enabled":true,"body_rewrite":[{"pattern":".","phase":"request","regex":"old","replacement":"new"},{"pattern":".","phase":"response","regex":"old","replacement":"new"}]}`).RewriteProgram
	client, _ := mitmClient(t, e, origin.Listener.Addr().String(), ca, roots, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reader, writer := io.Pipe()
	defer writer.Close()
	request, _ := http.NewRequestWithContext(ctx, "POST", "https://origin.test/service/Call", reader)
	request.Header.Set("Content-Type", "application/grpc+proto")
	type result struct {
		response *http.Response
		err      error
	}
	responses := make(chan result, 1)
	go func() { response, err := client.Do(request); responses <- result{response, err} }()
	if _, err := writer.Write(frame); err != nil {
		t.Fatal("gRPC upload was blocked:", err)
	}
	got := <-responses
	if got.err != nil {
		t.Fatal("gRPC response waited for upload EOF:", got.err)
	}
	defer got.response.Body.Close()
	first := make([]byte, len(frame))
	if _, err := io.ReadFull(got.response.Body, first); err != nil || !bytes.Equal(first, frame) || got.response.ProtoMajor != 2 {
		t.Fatalf("gRPC was buffered or rewritten: %v %v", first, err)
	}
	writer.Close()
	io.Copy(io.Discard, got.response.Body)
	got.response.Body.Close()
	if got.response.Trailer.Get("Grpc-Status") != "0" {
		t.Fatal("gRPC trailers were lost")
	}
	detail, err := saved.Detail(waitComplete(t, saved, 1)[0].ID)
	if err != nil || !bytes.Equal(detail.RequestBody, frame) || !bytes.Equal(detail.ResponseBody, frame) || !detail.Response.BodyComplete {
		t.Fatal("gRPC capture lost bytes or completion", err)
	}
}

func TestExpectContinueUploadCanBeRewrittenWithoutDeadlock(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != "new" {
			t.Errorf("upload not rewritten: %q %v", body, err)
		}
		w.Write(body)
	}))
	defer origin.Close()
	e, _ := testEngine(t, Config{})
	defer e.Close()
	e.rewrite = rewriteProgram(t, `{"enabled":true,"body_rewrite":[{"pattern":".","phase":"request","regex":"old","replacement":"new"}]}`).RewriteProgram
	client := debugClient(t, e, origin.Listener.Addr().String())
	client.Transport.(*http.Transport).ExpectContinueTimeout = 2 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	continued := make(chan struct{})
	var once sync.Once
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{Got100Continue: func() { once.Do(func() { close(continued) }) }})
	reader, writer := io.Pipe()
	defer writer.Close()
	request, _ := http.NewRequestWithContext(ctx, "POST", "http://example.test/upload", reader)
	request.Header.Set("Expect", "100-continue")
	request.ContentLength = 3
	written := make(chan error, 1)
	go func() {
		select {
		case <-continued:
			_, err := io.WriteString(writer, "old")
			writer.Close()
			written <- err
		case <-ctx.Done():
			writer.CloseWithError(ctx.Err())
			written <- ctx.Err()
		}
	}()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("Expect: 100-continue deadlocked:", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if writeErr := <-written; writeErr != nil || err != nil || string(body) != "new" {
		t.Fatalf("continued upload failed: %q %v %v", body, err, writeErr)
	}
}

func TestCancelledSlowUploadReleasesBodyBudget(t *testing.T) {
	e, _ := testEngine(t, Config{})
	e.rewrite = rewriteProgram(t, `{"enabled":true,"body_rewrite":[{"pattern":".","phase":"request","regex":"old","replacement":"new"}]}`).RewriteProgram
	client := debugClient(t, e, "127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer writer.Close()
	request, _ := http.NewRequestWithContext(ctx, "POST", "http://example.test/slow", reader)
	finished := make(chan error, 1)
	go func() {
		response, err := client.Do(request)
		if response != nil {
			response.Body.Close()
		}
		finished <- err
	}()
	writer.Write([]byte("o"))
	cancel()
	writer.Close()
	if err := <-finished; err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("cancelled upload was accepted: %v", err)
	}
	e.Close()
	if processingBodies.used.Load() != 0 {
		t.Fatal("cancelled upload leaked a body reservation")
	}
}
