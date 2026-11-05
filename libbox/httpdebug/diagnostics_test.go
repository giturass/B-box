package httpdebug

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTLSCloseWithoutHTTPRequestRecordsTentativeDiagnostic(t *testing.T) {
	e, saved, ca := mitmEngine(t, Config{})
	e.rewrite = rewriteProgram(t, `{"enabled":true,"map_local":[{"pattern":".","data":"unused"}]}`).RewriteProgram
	raw, inbound, err := mitmTCPPair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	defer inbound.Close()
	done := make(chan error, 1)
	if !e.Handle(context.Background(), inbound, Metadata{ConnectionID: "no-request", Host: "origin.test", Protocol: "tls", Port: 443}, nil, nil, func(err error) { done <- err }) {
		t.Fatal("TLS flow not accepted")
	}
	client := tls.Client(raw, &tls.Config{RootCAs: ca, ServerName: "origin.test", NextProtos: []string{"http/1.1"}})
	client.SetDeadline(time.Now().Add(2 * time.Second))
	if err = client.Handshake(); err != nil {
		t.Fatal(err)
	}
	client.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("closed client left its processing connection running")
	}
	detail, err := saved.Detail(waitComplete(t, saved, 1)[0].ID)
	if err != nil || !detail.DomainOnly || detail.Error != "" || detail.Metadata.ConnectionID != "no-request" ||
		!strings.Contains(detail.Note, "possible certificate pinning or client cancellation") || e.tlsFailed.Load() != 0 || e.tlsNoRequest.Load() != 1 {
		t.Fatalf("missing or overstated TLS diagnostic: %+v %v", detail, err)
	}
}

func TestProcessingFailurePointsToExistingServiceLogs(t *testing.T) {
	e, saved := testEngine(t, Config{})
	e.scripts = scriptProgram(t, `{"http":[{"pattern":".","phase":"request","source":"$done({intentionalFailure:true});"}]}`)
	client := debugClient(t, e, "127.0.0.1:1")
	response, err := client.Get("http://example.test/failure")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusBadGateway || !strings.Contains(string(body), "service logs") || strings.Contains(string(body), "capture logs") {
		t.Fatal("error page points to a removed feature")
	}
	detail, err := saved.Detail(waitComplete(t, saved, 1)[0].ID)
	if err != nil || !strings.Contains(detail.Error, `unsupported $done field "intentionalFailure"`) {
		t.Fatal("request lost its concrete script error")
	}
}

func TestStoppedRecordRejectsLateMetadataAndCompletion(t *testing.T) {
	saved, request := bodyTestStore(t)
	id := saved.begin(Metadata{}, request, 64)
	saved.appendBody(id, false, []byte("prefix"))
	saved.stopRecording()
	before, _ := saved.Detail(id)
	response := httptest.NewRecorder().Result()
	defer response.Body.Close()
	saved.response(id, response)
	saved.rewrittenRequest(id, request, "http://changed.test/")
	saved.appendBody(id, false, []byte("late"))
	saved.bodyComplete(id, false)
	saved.trailers(id, false, http.Header{"Late": {"value"}})
	saved.addNote(id, "late note")
	saved.setError(id, io.ErrUnexpectedEOF)
	saved.finish(id)
	after, _ := saved.Detail(id)
	if after.Request.BodyComplete || after.Request.BodyBytes != before.Request.BodyBytes || after.Error != before.Error ||
		after.Response.Status != 0 || len(after.Request.Trailers) != 0 || after.Note != before.Note || after.DurationMS != before.DurationMS || after.OriginalURL != "" {
		t.Fatal("late processing mutated a stopped recording")
	}
}
