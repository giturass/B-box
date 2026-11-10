package httpdebug

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSlowUploadStillRewritesAfterFormerBodyDeadline(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write(body)
	}))
	defer origin.Close()
	e, _ := testEngine(t, Config{})
	e.rewrite = rewriteProgram(t, `{"body_rewrite":[{"pattern":".","phase":"request","regex":"old","replacement":"new"}]}`).RewriteProgram
	client := debugClient(t, e, origin.Listener.Addr().String())
	client.Timeout = 25 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	go func() {
		defer writer.Close()
		if _, err := io.WriteString(writer, "o"); err != nil {
			return
		}
		// The old fixed body deadline rejected this otherwise valid upload.
		timer := time.NewTimer(16 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
			_, _ = io.WriteString(writer, "ld")
		case <-ctx.Done():
		}
	}()
	request, err := http.NewRequestWithContext(ctx, "POST", "http://example.test/slow", reader)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal("slow upload was aborted", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK || string(body) != "new" {
		t.Fatal("slow upload skipped its rewrite", response.Status, string(body), err)
	}
}
