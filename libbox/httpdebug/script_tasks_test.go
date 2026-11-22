package httpdebug

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func isolateScriptService(t *testing.T) string {
	t.Helper()
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
	return t.TempDir()
}

func prepareScriptService(t *testing.T, directory string, config *ScriptsConfig) *PreparedProcessing {
	t.Helper()
	prepared, err := PrepareProcessing(directory, nil, nil, config, "", nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	return prepared
}

func awaitScriptTask(t *testing.T, task *ScriptTask) ScriptRunResult {
	t.Helper()
	finished := make(chan ScriptRunResult, 1)
	go func() { finished <- task.Wait() }()
	select {
	case output := <-finished:
		return output
	case <-time.After(3 * time.Second):
		task.Cancel()
		t.Fatal("script invocation did not finish")
		return ScriptRunResult{}
	}
}

func TestEventAndGenericValidationDoesNotReadOrExecuteSources(t *testing.T) {
	for _, invalid := range []string{
		`{"event":[{"event":"engine-start","source":"$done()"}]}`,
		`{"event":[{"event":"config-reloaded","source":"$done()"}]}`,
		`{"event":[{"source":"$done()"}]}`,
		`{"event":[{"event":"unknown","source":"$done()","disabled":true}]}`,
		`{"event":[{"event":"network-changed","source":"function {","disabled":true}]}`,
		`{"generic":[{"source":"const =","disabled":true}]}`,
		`{"generic":[{"source":"$done()","event":"network-changed"}]}`,
		`{"generic":[{"source":"$done()","cron":"* * * * *"}]}`,
		`{"generic":[{"source":"$done()","script_path":"task.js"}]}`,
		`{"event":[{"event":"profile-reloaded","resource":"missing"}]}`,
		`{"generic":[{"script_path":"../outside.js"}]}`,
		`{"generic":[{"source":"$done()","timeout_ms":-2}]}`,
		`{"event":[{"event":"network-changed","source":"$done()","timeout_ms":-2}]}`,
	} {
		var config ScriptsConfig
		if err := json.Unmarshal([]byte(invalid), &config); err == nil {
			t.Errorf("accepted invalid task configuration: %s", invalid)
		}
	}
	directory := isolateScriptService(t)
	var requests atomic.Int32
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write([]byte("throw Error('resource executed during validation');"))
	}))
	defer origin.Close()
	content, err := json.Marshal(ScriptsConfig{Enabled: true,
		Event: []EventScript{
			{Event: ScriptEventNetworkChanged, Source: "throw Error('inline event executed during validation');"},
			{Event: ScriptEventEngineStarted, ScriptPath: origin.URL + "/event.js"},
		},
		Generic: []GenericScript{
			{Source: "throw Error('generic executed during validation');"},
			{ScriptPath: "missing-local-task.js"},
			{ScriptPath: origin.URL + "/generic.js"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var config ScriptsConfig
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	prepared := prepareScriptService(t, directory, &config)
	if prepared.tasks != nil || Current() != nil || requests.Load() != 0 {
		t.Fatal("checking/preparing task scripts activated code or downloaded sources")
	}
	if _, err := RunScript(context.Background(), "generic", 0); !errors.Is(err, ErrScriptsInactive) {
		t.Fatal("prepared configuration could execute before activation", err)
	}
}

func TestManualTasksMetadataOutputAndIndependentVMs(t *testing.T) {
	directory := isolateScriptService(t)
	const common = `if (typeof $request !== 'undefined' || typeof $response !== 'undefined' || $trigger !== 'editor' ||
		!$script.sessionID || $script.name !== 'same' || $script.binaryBodyMode !== false || $script.startTime <= 0 ||
		typeof $utils.ungzip !== 'function' || $argument !== 'configured') throw Error('wrong manual context');
		var seen = (typeof seen === 'undefined' ? 0 : seen) + 1;
		console.log('first', {value:42});
		setTimeout(function() { console.warn('second'); $done({title:'Ready',content:'done',seen:seen,session:$script.sessionID,type:$script.type});
		console.error('after done'); }, 1);`
	config := &ScriptsConfig{Enabled: true,
		Cron: []CronScript{{Name: "same", Cron: "0 2 * * *", Argument: "configured",
			Source: `if($script.type !== 'cron' || $cronexp !== '0 2 * * *' || typeof $event !== 'undefined')throw Error('cron');` + common}},
		Event: []EventScript{{Name: "same", Event: ScriptEventNetworkChanged, Argument: "configured",
			Source: `if($script.type !== 'event' || $event.name !== 'manually' || Object.keys($event.data).length || typeof $cronexp !== 'undefined')throw Error('event');` + common}},
		Generic: []GenericScript{{Name: "same", Argument: "configured",
			Source: `if($script.type !== 'generic' || typeof $event !== 'undefined' || typeof $cronexp !== 'undefined')throw Error('generic');` + common}},
	}
	prepared := prepareScriptService(t, directory, config)
	ActivateProcessing(prepared, nil, nil, "")
	if state := Status(); state.CronJobs != 1 || state.EventJobs != 1 || state.GenericJobs != 1 || state.ScriptRules != 0 {
		t.Fatal("task-only profile status did not report independent kinds", state)
	}
	if Current().Candidate(Metadata{Host: "example.test", Port: 80, Protocol: "http"}) {
		t.Fatal("task-only profile intercepted HTTP")
	}
	for _, kind := range []string{"cron", "event", "generic"} {
		lastSession := ""
		for range 2 {
			task, err := prepared.RunScript(context.Background(), kind, 0)
			if err != nil {
				t.Fatal(err)
			}
			output := awaitScriptTask(t, task)
			var result struct {
				Title, Content, Session, Type string
				Seen                          int
			}
			if err := json.Unmarshal([]byte(output.Result), &result); err != nil || output.Error != "" || result.Title != "Ready" ||
				result.Content != "done" || result.Seen != 1 || result.Type != kind || result.Session == lastSession {
				t.Fatal("task output or VM identity was lost", output, err)
			}
			lastSession = result.Session
			if len(output.Logs) != 2 || !strings.HasSuffix(output.Logs[0], `first {"value":42}`) || !strings.HasSuffix(output.Logs[1], "second") {
				t.Fatal("manual console output was lost or continued after $done", output.Logs)
			}
			output.Logs[0] = "changed by caller"
			if task.Wait().Logs[0] == output.Logs[0] {
				t.Fatal("a result reader could alter the completed task's output")
			}
		}
	}
	for _, kind := range []string{"http", "unknown"} {
		if _, err := RunScript(context.Background(), kind, 0); !errors.Is(err, ErrScriptUnavailable) {
			t.Fatal("manual API accepted an unsupported task kind", kind, err)
		}
	}
}

func TestManualTaskJSONResultsAndFailuresRetainLogs(t *testing.T) {
	for _, test := range []struct {
		source, result, failure string
	}{
		{`$done();`, "null", ""},
		{`$done(null);`, "null", ""},
		{`$done('猫😊');`, `"猫😊"`, ""},
		{`$done([1,true,'text']);`, `[1,true,"text"]`, ""},
		{`JSON.stringify = () => 'broken'; $done({ok:true});`, `{"ok":true}`, ""},
		{`const x={};x.self=x;$done(x);`, "null", "JSON serializable"},
		{`$done(function(){});`, "null", "JSON serializable"},
		{`throw Error('debug failure');`, "null", "debug failure"},
		{`for(;;){}`, "null", "deadline"},
		{`setInterval(function(){},1);`, "null", "deadline"},
	} {
		t.Run(test.source, func(t *testing.T) {
			program, err := CompileScripts(&ScriptsConfig{Generic: []GenericScript{{TimeoutMS: 200, Source: "console.log('before');" + test.source}}}, time.UTC)
			if err != nil {
				t.Fatal(err)
			}
			runner := newScriptTaskRunner(context.Background(), program, scriptEnvironment{})
			defer runner.Close()
			task, err := runner.runManual(context.Background(), "generic", 0)
			if err != nil {
				t.Fatal(err)
			}
			output := awaitScriptTask(t, task)
			if output.Result != test.result || len(output.Logs) != 1 || !strings.HasSuffix(output.Logs[0], "before") ||
				test.failure == "" && output.Error != "" || test.failure != "" && !strings.Contains(output.Error, test.failure) {
				t.Fatal("manual result/error/console output was not preserved", output)
			}
		})
	}
}

func TestManualTasksUseOriginalIndexesAndPermitDuplicateNames(t *testing.T) {
	config := &ScriptsConfig{Generic: []GenericScript{
		{Disable: true, Name: "same", Source: "throw Error('disabled script ran');"},
		{Name: "same", Source: "$done('second');"},
		{Name: "same", Source: "$done('third');"},
		{Source: "if(!$script.name)throw Error('missing script name');$done('unnamed');"},
	}, Cron: []CronScript{
		{Disable: true, Cron: "0 0 1 1 *", Source: "throw Error('disabled cron ran');"},
		{Cron: "0 0 1 1 *", Source: "$done('enabled cron');"},
	}}
	program, err := CompileScripts(config, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	runner := newScriptTaskRunner(context.Background(), program, scriptEnvironment{})
	defer runner.Close()
	for _, kind := range []string{"generic", "cron"} {
		if _, err := runner.runManual(context.Background(), kind, 0); !errors.Is(err, ErrScriptUnavailable) {
			t.Fatal("disabled task was admitted", kind, err)
		}
	}
	for _, test := range []struct {
		kind  string
		index int
		want  string
	}{{"generic", 1, `"second"`}, {"generic", 2, `"third"`}, {"generic", 3, `"unnamed"`}, {"cron", 1, `"enabled cron"`}} {
		task, err := runner.runManual(context.Background(), test.kind, test.index)
		if err != nil {
			t.Fatal(err)
		}
		if output := awaitScriptTask(t, task); output.Error != "" || output.Result != test.want {
			t.Fatal("task was selected by enabled-list position/name instead of original index", test, output)
		}
	}
}

func TestCronAutomaticAndManualInvocationsShareTaskAdmission(t *testing.T) {
	f := newCronQueueTest(t, 0)
	f.trigger()
	f.nextStarted(t)
	if _, err := f.runner.tasks.runManual(context.Background(), "cron", 0); !errors.Is(err, ErrScriptBusy) {
		t.Fatal("manual invocation overlapped automatic Cron", err)
	}
	f.release[0] <- struct{}{}
	f.waitIdle(t, 0)
	task, err := f.runner.tasks.runManual(context.Background(), "cron", 0)
	if err != nil {
		t.Fatal(err)
	}
	f.nextStarted(t)
	for range 3 {
		f.trigger()
	}
	select {
	case <-f.started:
		t.Fatal("automatic Cron overlapped a manual invocation")
	default:
	}
	task.Cancel()
	if output := awaitScriptTask(t, task); !strings.Contains(output.Error, "cancel") {
		t.Fatal("manual cancellation was not returned", output)
	}
	f.trigger()
	f.nextStarted(t)
}

func TestLifecycleEventsRunOnlyForActivatedProfileAndIgnoreOldCallbacks(t *testing.T) {
	directory := isolateScriptService(t)
	config := &ScriptsConfig{Enabled: true, Generic: []GenericScript{{Source: "console.log('EVENT generic'); $done();"}}}
	for _, event := range []string{ScriptEventEngineStarted, ScriptEventProfileReloaded, ScriptEventNetworkChanged} {
		config.Event = append(config.Event, EventScript{Event: event, Source: fmt.Sprintf(`if(typeof $trigger !== 'undefined' || typeof $request !== 'undefined' || $script.type !== 'event' || $event.name !== %q || Object.keys($event.data).length)throw Error('wrong event'); console.log('EVENT', $event.name); $done();`, event)})
	}
	first := prepareScriptService(t, directory, config)
	events := make(chan string, 8)
	SetLogger(func(_, line string) {
		if marker := strings.Index(line, ": EVENT "); marker >= 0 {
			events <- line[marker+8:]
		}
	})
	awaitEvent := func(want string) {
		t.Helper()
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("got event %s, want %s", got, want)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("lifecycle event was not delivered", want)
		}
	}
	ActivateProcessing(first, nil, nil)
	awaitEvent(ScriptEventEngineStarted)
	// Interface callbacks must not join the manager lock used while shutdown
	// waits for existing network and script work to exit.
	managerMu.Lock()
	unlock := sync.OnceFunc(managerMu.Unlock)
	defer unlock()
	notified := make(chan error, 1)
	go func() { notified <- first.NotifyScriptEvent(ScriptEventNetworkChanged) }()
	select {
	case err := <-notified:
		if err != nil {
			unlock()
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		unlock()
		t.Fatal("network callback waited for the shutdown manager lock")
	}
	unlock()
	awaitEvent(ScriptEventNetworkChanged)
	if err := first.NotifyScriptEvent("unknown"); err == nil {
		t.Fatal("unknown event was accepted")
	}
	if err := Start(directory, `{}`, "", nil); err != nil {
		t.Fatal(err)
	}
	Stop()
	second := prepareScriptService(t, directory, config)
	ActivateProcessing(second, nil, nil, ScriptEventProfileReloaded)
	awaitEvent(ScriptEventProfileReloaded)
	if err := first.NotifyScriptEvent(ScriptEventNetworkChanged); !errors.Is(err, ErrScriptsInactive) {
		t.Fatal("old network callback notified replacement profile", err)
	}
	if _, err := first.RunScript(context.Background(), "generic", 0); !errors.Is(err, ErrScriptsInactive) {
		t.Fatal("old profile could start task on replacement service", err)
	}
	StopService()
	if err := second.NotifyScriptEvent(ScriptEventNetworkChanged); !errors.Is(err, ErrScriptsInactive) {
		t.Fatal("stopped profile accepted a network event", err)
	}
	select {
	case got := <-events:
		t.Fatal("generic/recording/reload/stale callback caused an extra automatic invocation", got)
	default:
	}
}

func TestEventManualGateSkipsRepeatedNotificationsAndPermitsOtherTasks(t *testing.T) {
	program, err := CompileScripts(&ScriptsConfig{
		Event: []EventScript{{Event: ScriptEventNetworkChanged, TimeoutMS: -1,
			Source: "$httpClient.get({url:'http://event.test/task',timeout:-1},()=>{$done();});"}},
		Generic: []GenericScript{{Name: "same", Source: "$done(42);"}, {Name: "same", Source: "$done(43);"}},
	}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 8)
	runner := newScriptTaskRunner(context.Background(), program, scriptEnvironment{dial: func(ctx context.Context, _ string) (net.Conn, error) {
		started <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	defer runner.Close()
	awaitStarted := func() {
		t.Helper()
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("event HTTP worker did not start")
		}
	}
	task, err := runner.runManual(context.Background(), "event", 0)
	if err != nil {
		t.Fatal(err)
	}
	awaitStarted()
	for range 3 {
		if err := runner.notify(ScriptEventNetworkChanged); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := runner.runManual(context.Background(), "event", 0); !errors.Is(err, ErrScriptBusy) {
		t.Fatal("a manual event overlapped itself", err)
	}
	for index, want := range []string{"42", "43"} {
		other, err := runner.runManual(context.Background(), "generic", index)
		if err != nil {
			t.Fatal(err)
		}
		if output := awaitScriptTask(t, other); output.Error != "" || output.Result != want {
			t.Fatal("event task gate blocked another task with duplicate names", output)
		}
	}
	select {
	case <-started:
		t.Fatal("repeated event ran while manual event was unfinished")
	default:
	}
	task.Cancel()
	awaitScriptTask(t, task)
	if err := runner.notify(ScriptEventNetworkChanged); err != nil {
		t.Fatal(err)
	}
	awaitStarted()
	if _, err := runner.runManual(context.Background(), "event", 0); !errors.Is(err, ErrScriptBusy) {
		t.Fatal("manual event overlapped an automatic event", err)
	}
}

func TestManualTaskStopCancelsAndWaitsForRoutedWorker(t *testing.T) {
	directory := isolateScriptService(t)
	config := &ScriptsConfig{Enabled: true, Generic: []GenericScript{{TimeoutMS: -1,
		Source: "console.log('started'); $httpClient.get({url:'http://task.test/',timeout:-1}, function(){ $done(); });"}}}
	prepared := prepareScriptService(t, directory, config)
	started, cancelled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseWorker := sync.OnceFunc(func() { close(release) })
	defer releaseWorker()
	ActivateProcessing(prepared, func(ctx context.Context, _ string) (net.Conn, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		<-release
		return nil, ctx.Err()
	}, nil, "")
	task, err := RunScript(context.Background(), "generic", 0)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("routed worker did not start")
	}
	stopped := make(chan struct{})
	joined := make(chan struct{})
	go func() { task.Join(); close(joined) }()
	go func() { StopService(); close(stopped) }()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not cancel routed worker")
	}
	select {
	case <-stopped:
		t.Fatal("service returned before cancelled worker exited")
	default:
	}
	select {
	case <-joined:
		t.Fatal("task join returned before cancelled worker exited")
	default:
	}
	releaseWorker()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("service did not wait for routed worker exit")
	}
	select {
	case <-joined:
	case <-time.After(3 * time.Second):
		t.Fatal("task join did not finish after routed worker exit")
	}
	output := awaitScriptTask(t, task)
	if !strings.Contains(output.Error, "cancel") || len(output.Logs) != 1 || Status().ScriptsActive || genericScriptsRunning.Load() != 0 || processingBodies.used.Load() != 0 {
		t.Fatal("stop retained manual task, logs, VM or source/body work", output)
	}
	if _, err := prepared.RunScript(context.Background(), "generic", 0); !errors.Is(err, ErrScriptsInactive) {
		t.Fatal("stopped profile admitted new work", err)
	}
}

func TestManualGenericFileSourceRefreshHTTPAndSharedStorage(t *testing.T) {
	directory := isolateScriptService(t)
	profileDirectory := t.TempDir()
	path := filepath.Join(profileDirectory, "generic.js")
	const source = `if(typeof $argument !== 'undefined')throw Error('unexpected empty argument');
		$httpClient.get('http://allowed.test/value', function(error,response,data) {
			if(error||response.status!==200||data!=='routed')throw Error(error||'HTTP');
			if(!$persistentStore.write(data,'shared'))throw Error('write');
			setTimeout(()=>{$done($persistentStore.read('shared'));},1);
		});`
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	config := &ScriptsConfig{Enabled: true, Generic: []GenericScript{{ScriptPath: "generic.js", Debug: true,
		HTTPClient: &ScriptHTTPOptions{AllowedOrigins: []string{"http://allowed.test"}}}},
		Event: []EventScript{{Event: ScriptEventNetworkChanged, Source: "$done($persistentStore.read('shared'));"}}}
	prepared := prepareScriptService(t, directory, config)
	prepared.SetProfileDirectory(profileDirectory)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("routed"))
	}))
	defer origin.Close()
	var dials atomic.Int32
	ActivateProcessing(prepared, func(ctx context.Context, address string) (net.Conn, error) {
		if address != "allowed.test:80" {
			return nil, fmt.Errorf("unexpected routed destination %s", address)
		}
		dials.Add(1)
		return (&net.Dialer{}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
	}, nil, "")
	run := func(kind string, index int, want string) {
		t.Helper()
		task, err := prepared.RunScript(context.Background(), kind, index)
		if err != nil {
			t.Fatal(err)
		}
		if output := awaitScriptTask(t, task); output.Error != "" || output.Result != want {
			t.Fatal("source/HTTP/timer/storage task failed", output)
		}
	}
	run("generic", 0, `"routed"`)
	run("event", 0, `"routed"`)
	if dials.Load() != 1 || processingBodies.used.Load() != 0 {
		t.Fatal("generic route or resource accounting failed")
	}
	if err := os.WriteFile(path, []byte("$done('updated source');"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("generic", 0, `"updated source"`)
}
