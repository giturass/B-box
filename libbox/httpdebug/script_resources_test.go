package httpdebug

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSlowCronHTTPDoesNotOccupyHTTPInterpreterCapacity(t *testing.T) {
	started := make(chan struct{}, cap(cronScriptSlots))
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		started <- struct{}{}
		<-request.Context().Done()
	}))
	defer origin.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	content, _ := json.Marshal(map[string]any{"cron": []any{map[string]any{
		"cron": "* * * * *", "timeout_ms": 30000,
		"source": "$httpClient.get($argument, function(error) { if(error) throw Error(error); $done(); });", "argument": origin.URL,
	}}})
	program := scriptProgram(t, string(content))
	runner := &cronRunner{ctx: ctx, program: program, dial: func(ctx context.Context, address string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}}
	finished := make(chan error, cap(cronScriptSlots))
	for i := 0; i < cap(cronScriptSlots); i++ {
		go func() { finished <- runner.run(program.cron[0]) }()
	}
	for i := 0; i < cap(cronScriptSlots); i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("cron HTTP did not occupy its interpreter pool")
		}
	}
	httpProgram := &httpProgram{RewriteProgram: &RewriteProgram{limit: MaxBodyLimit},
		scripts: scriptProgram(t, `{"http":[{"pattern":".","phase":"request","source":"$done({response:{body:'immediate'}});"}]}`)}
	request, _ := http.NewRequest("GET", "http://example.test/", nil)
	response, err := httpProgram.request(request, "test", request.URL.String(), func(string) {})
	if err != nil || response == nil {
		t.Fatal("HTTP script was starved by cron:", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "immediate" {
		t.Fatal("HTTP script did not return its result")
	}
	cancel()
	for i := 0; i < cap(cronScriptSlots); i++ {
		select {
		case err := <-finished:
			if err == nil {
				t.Fatal("cancelled cron HTTP was reported as successful")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cancelled cron retained its interpreter")
		}
	}
	if len(cronScriptSlots) != 0 || len(httpScriptSlots) != 0 || processingBodies.used.Load() != 0 {
		t.Fatal("script cancellation leaked execution or body capacity")
	}
}
