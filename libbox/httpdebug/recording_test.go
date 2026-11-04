package httpdebug

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func recordingManager(t *testing.T, e *Engine) {
	t.Helper()
	StopService()
	managerMu.Lock()
	previousStore, previousState := store, lastCaptureState
	store, lastCaptureState = e.store, State{}
	active.Store(e)
	managerMu.Unlock()
	t.Cleanup(func() {
		StopService()
		managerMu.Lock()
		store, lastCaptureState = previousStore, previousState
		managerMu.Unlock()
	})
}

func TestRecordingTogglesKeepHTTP1AndHTTP2Connections(t *testing.T) {
	for _, h2 := range []bool{false, true} {
		t.Run(map[bool]string{false: "http1", true: "http2"}[h2], func(t *testing.T) {
			e, saved, ca := mitmEngine(t, Config{})
			e.recording.Store(nil)
			e.rewrite = rewriteProgram(t, `{"enabled":true,"map_local":[{"pattern":".","data":"profile"}]}`).RewriteProgram
			recordingManager(t, e)
			client, _ := mitmClient(t, e, "127.0.0.1:1", ca, nil, h2)
			get := func() {
				t.Helper()
				response, err := client.Get("https://origin.test/mock")
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil || string(body) != "profile" || h2 && response.ProtoMajor != 2 {
					t.Fatalf("profile processing failed: %q %v", body, err)
				}
			}
			get()
			for i := 0; i < 2; i++ {
				if err := Start(saved.directory, `{}`, "", nil); err != nil {
					t.Fatal(err)
				}
				if Current() != e {
					t.Fatal("starting recording replaced the processing engine")
				}
				get()
				items := waitComplete(t, saved, i+1)
				for _, item := range items {
					detail, _ := saved.Detail(item.ID)
					if detail.Metadata.ConnectionID != "mitm-client-1" {
						t.Fatal("recording toggle required a new TLS connection")
					}
				}
				Stop()
				get()
				if Current() != e || saved.Page(10, "").Total != i+1 || Status().Active {
					t.Fatal("stopped recording still captured or disconnected processing")
				}
			}
		})
	}
}

func TestStoppingRecordingFreezesAnSSERecordWithoutDisconnecting(t *testing.T) {
	more := make(chan string)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: first\n")
		w.(http.Flusher).Flush()
		for {
			select {
			case value := <-more:
				io.WriteString(w, value)
				w.(http.Flusher).Flush()
			case <-request.Context().Done():
				return
			}
		}
	}))
	defer origin.Close()
	e, saved := testEngine(t, Config{})
	defer e.Close()
	e.rewrite = rewriteProgram(t, `{"enabled":true,"body_rewrite":[{"pattern":".","phase":"response","regex":"data","replacement":"broken"}]}`).RewriteProgram
	recordingManager(t, e)
	client := debugClient(t, e, origin.Listener.Addr().String())
	response, err := client.Get("http://example.test/events")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	if line, err := reader.ReadString('\n'); err != nil || line != "data: first\n" {
		t.Fatalf("SSE was buffered or changed: %q %v", line, err)
	}
	Stop()
	item := waitComplete(t, saved, 1)[0]
	before, _ := saved.Detail(item.ID)
	if before.Response.BodyComplete || !strings.Contains(before.Note, "Recording stopped") {
		t.Fatal("stopped streaming record was labelled complete")
	}
	if err = Start(saved.directory, `{}`, "", nil); err != nil {
		t.Fatal(err)
	}
	select {
	case more <- "data: second\n":
	case <-time.After(2 * time.Second):
		t.Fatal("recording toggle disconnected the upstream SSE stream")
	}
	if line, err := reader.ReadString('\n'); err != nil || line != "data: second\n" {
		t.Fatalf("recording toggle interrupted SSE: %q %v", line, err)
	}
	response.Body.Close()
	e.Close()
	after, _ := saved.Detail(item.ID)
	if saved.Page(10, "").Total != 1 || string(after.ResponseBody) != string(before.ResponseBody) ||
		after.Response.BodyBytes != before.Response.BodyBytes || after.Response.BodyComplete || after.DurationMS != before.DurationMS {
		t.Fatal("late stream updates mutated the frozen record")
	}
}
