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

func TestSlowCronHTTPDoesNotBlockOtherInvocations(t *testing.T) {
	const concurrent = 6
	started := make(chan struct{}, concurrent)
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
	finished := make(chan error, concurrent)
	for i := 0; i < concurrent; i++ {
		go func() { finished <- runner.run(program.cron[0]) }()
	}
	for i := 0; i < concurrent; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatal("independent cron HTTP did not start")
		}
	}
	httpProgram := &httpProgram{RewriteProgram: &RewriteProgram{limit: (1024 * 1024)},
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
	for i := 0; i < concurrent; i++ {
		select {
		case err := <-finished:
			if err == nil {
				t.Fatal("cancelled cron HTTP was reported as successful")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cancelled cron retained its interpreter")
		}
	}
	if cronScriptsRunning.Load() != 0 || httpScriptsRunning.Load() != 0 || processingBodies.used.Load() != 0 {
		t.Fatal("script cancellation leaked execution or body capacity")
	}
}
