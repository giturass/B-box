package httpdebug

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestScriptsValidateIndependentlyAndCronSchedule(t *testing.T) {
	for _, invalid := range []string{
		`null`, `{"unknown":true}`, `{"http":[{"pattern":".","phase":"request","source":"function {"}]}`,
		`{"http":[{"pattern":".","phase":"request","source":"$done()","timeout_ms":2001}]}`,
		`{"cron":[{"cron":"* * * *","source":"$done()"}]}`,
		`{"cron":[{"cron":"61 * * * *","source":"$done()"}]}`,
		`{"cron":[{"cron":"0 0 31 2 *","source":"$done()"}]}`,
		`{"cron":[{"cron":"*/0 * * * *","source":"$done()"}]}`,
		`{"cron":[{"cron":"0,,2 * * * *","source":"$done()"}]}`,
		`{"cron":[{"cron":"TZ=UTC","source":"$done()"}]}`,
		`{"cron":[{"cron":"* * * * *","timezone":"bad/zone","source":"$done()"}]}`,
		`{"cron":[{"cron":"* * * * *","source":"const =","disabled":true}]}`,
		`{"cron":[{"cron":"* * * * *","resource":"missing"}]}`,
		`{"cron":[{"cron":"* * * * *","source":"$done()","timeout_ms":30001}]}`,
	} {
		var config ScriptsConfig
		if err := json.Unmarshal([]byte(invalid), &config); err == nil {
			t.Errorf("accepted invalid scripts: %s", invalid)
		}
	}
	for _, test := range []struct{ cron, zone, from, next string }{
		{"0 2 * * *", "UTC", "2026-11-03T01:59:59Z", "2026-11-03T02:00:00Z"},
		{"0 2 * * *", "Asia/Shanghai", "2026-11-02T17:59:59Z", "2026-11-02T18:00:00Z"},
		{"*/5 * * * * *", "UTC", "2026-11-03T00:00:03Z", "2026-11-03T00:00:05Z"},
		{"0 17 * * sun", "UTC", "2026-11-03T00:00:00Z", "2026-11-08T17:00:00Z"},
		{"30 2 * * *", "America/New_York", "2026-03-08T06:59:59Z", "2026-03-09T06:30:00Z"},
	} {
		schedule, err := compileCron(test.cron, test.zone, time.UTC)
		if err != nil {
			t.Fatal(err)
		}
		from, _ := time.Parse(time.RFC3339, test.from)
		if next := schedule.Next(from).UTC().Format(time.RFC3339); next != test.next {
			t.Fatalf("%s %s: %s != %s", test.cron, test.zone, next, test.next)
		}
	}
	e, _ := testEngine(t, Config{})
	e.captureEnabled = false
	e.scripts = scriptProgram(t, `{"enabled":true,"http":[{"pattern":"/mock$","phase":"request","source":"$done({response:{body:'script-only'}});"}]}`)
	client := debugClient(t, e, "127.0.0.1:1")
	response, err := client.Get("http://example.test/mock")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "script-only" || e.rewrite != nil {
		t.Fatal("script required HTTP rewrite to be enabled")
	}
	if e.Matches(Metadata{Host: "example.test", Protocol: "tls", Port: 443}) {
		t.Fatal("script silently enabled MITM")
	}
}

func TestCronHTTPCallbackCompletionLimitsAndTLSVerification(t *testing.T) {
	var received atomic.Int32
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received.Add(1)
		io.WriteString(w, "cron-response")
	}))
	defer origin.Close()
	for _, trusted := range []bool{true, false} {
		roots := x509.NewCertPool()
		if trusted {
			roots.AddCert(origin.Certificate())
		}
		expectError := "false"
		if !trusted {
			expectError = "true"
		}
		source := `if (typeof $request !== 'undefined' || $script.type !== 'cron' || $cronexp !== '0 2 * * *') throw Error('wrong context');
            $httpClient.get($argument, function(error,response,data) {
                if (!!error !== ` + expectError + `) throw Error('wrong trust result');
                if (!error && (response.status !== 200 || data !== 'cron-response')) throw Error('wrong HTTP result');
                $done();
                throw Error('executed after done');
            });`
		content, _ := json.Marshal(map[string]any{"cron": []any{map[string]any{"cron": "0 2 * * *", "source": source, "argument": origin.URL}}})
		program := scriptProgram(t, string(content))
		var dials atomic.Int32
		runner := &cronRunner{ctx: context.Background(), program: program, roots: roots, dial: func(ctx context.Context, address string) (net.Conn, error) {
			dials.Add(1)
			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		}}
		err := runner.run(program.cron[0])
		if err != nil || dials.Load() != 1 {
			t.Fatalf("cron callback failed: %v", err)
		}
	}
	if received.Load() != 1 {
		t.Fatal("cron request bypassed TLS verification")
	}
	for _, source := range []string{"for(;;){}", "console.log('missing done')", "$httpClient.get('file:///invalid', () => {throw Error('bad request')});"} {
		content, _ := json.Marshal(map[string]any{"cron": []any{map[string]any{"cron": "* * * * *", "timeout_ms": 20, "source": source}}})
		program := scriptProgram(t, string(content))
		runner := &cronRunner{ctx: context.Background(), program: program}
		start := time.Now()
		err := runner.run(program.cron[0])
		if time.Since(start) > time.Second || err == nil {
			t.Fatalf("cron failure was ignored: %v", err)
		}
	}
}

func TestCronLifecycleDoesNotRestartOnCaptureAndCancelsRequests(t *testing.T) {
	StopService()
	managerMu.Lock()
	previousStore, previousState := store, lastCaptureState
	store, lastCaptureState = nil, State{}
	managerMu.Unlock()
	t.Cleanup(func() {
		StopService()
		managerMu.Lock()
		store, lastCaptureState = previousStore, previousState
		managerMu.Unlock()
	})
	started, cancelled := make(chan struct{}, 1), make(chan struct{}, 1)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		started <- struct{}{}
		<-request.Context().Done()
		cancelled <- struct{}{}
	}))
	defer origin.Close()
	var config ScriptsConfig
	content := `{"enabled":true,"cron":[{"cron":"* * * * * *","timeout_ms":30000,"source":"$httpClient.get('http://cron.test/task', function(error) { if(error) throw Error(error); $done(); });"}]}`
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	prepared, err := PrepareProcessing(directory, nil, nil, &config, "", nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if Current() != nil {
		t.Fatal("validation ran cron")
	}
	var calls atomic.Int32
	ActivateProcessing(prepared, func(ctx context.Context, address string) (net.Conn, error) {
		if address != "cron.test:80" {
			t.Errorf("wrong routed script target: %s", address)
		}
		calls.Add(1)
		return (&net.Dialer{}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
	}, nil)
	defer StopService()
	runner := prepared.cron
	if state := Status(); !state.ScriptsActive || state.MITMActive || state.RewriteActive || state.CronJobs != 1 {
		t.Fatalf("cron is not independent: %+v", state)
	}
	if Current().Candidate(Metadata{Host: "test", Port: 80, Protocol: "http"}) {
		t.Fatal("cron-only profile intercepts HTTP")
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("cron did not run")
	}
	if err = Start(directory, `{}`, "", nil); err != nil {
		t.Fatal(err)
	}
	Stop()
	if prepared.cron != runner || runner.ctx.Err() != nil {
		t.Fatal("recording toggle replaced the cron scheduler")
	}
	// An overdue, still-running task is skipped, with no catch-up burst.
	runner.runDue(time.Now().Add(24 * time.Hour))
	if calls.Load() != 1 {
		t.Fatal("cron invocation overlapped")
	}
	StopService()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("service stop left cron HTTP running")
	}
	runner.runDue(time.Now().Add(48 * time.Hour))
	if calls.Load() != 1 || Current() != nil || Status().ScriptsActive {
		t.Fatal("cron survived service stop")
	}
}
