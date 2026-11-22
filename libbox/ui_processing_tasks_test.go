package libbox

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/experimental/httpdebug"
	"github.com/sagernet/sing-box/service/powerreport"
	"github.com/sagernet/sing/common/control"
	"github.com/sagernet/sing/service/filemanager"
)

func TestUIProcessingTaskSchemaPreservesEventAndGenericSourceIdentity(t *testing.T) {
	content := `{"outbounds":[{"type":"direct","tag":"kept"}],"experimental":{"scripts":{"enabled":true,"event":[{"name":"same","event":"network-changed","storage_namespace":"events","source":"throw Error('validation must not execute');"},{"event":"engine-started","script_path":"unread.js","disabled":true},{"event":"profile-reloaded","source":"$done();"}],"generic":[{"name":"same","disabled":true,"source":"$done();"},{"name":"same","source":"$done({ok:true});","debug":true,"timeout_ms":-1,"argument":"kept","http_client":{"allowed_origins":[]}}]}}}`
	if err := CheckConfig(content); err != nil {
		t.Fatal(err)
	}
	formatted, err := FormatConfig(content)
	if err != nil {
		t.Fatal(err)
	}
	options, err := parseConfig(baseContext(nil), formatted.Value)
	if err != nil {
		t.Fatal(err)
	}
	scripts := options.Experimental.Scripts
	if len(scripts.Event) != 3 || len(scripts.Generic) != 2 || !scripts.Generic[0].Disable || scripts.Generic[1].Disable ||
		scripts.Generic[0].Name != scripts.Generic[1].Name || scripts.Generic[1].TimeoutMS != -1 || scripts.Generic[1].Argument != "kept" {
		t.Fatal("formatting changed task kinds, array positions or disabled entries")
	}
	schema, err := GenerateConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"event", "generic", "disabled", "storage_namespace", "http_client"} {
		if !strings.Contains(schema.Value, `"`+field+`"`) || !strings.Contains(formatted.Value, `"`+field+`"`) {
			t.Errorf("script field missing from schema or formatting: %s", field)
		}
	}
	for _, invalid := range []string{
		strings.Replace(content, "network-changed", "network-change", 1),
		strings.Replace(content, "engine-started", "engine-start", 1),
		strings.Replace(content, "profile-reloaded", "config-reloaded", 1),
		strings.Replace(content, "unread.js", "../outside.js", 1),
	} {
		if err := CheckConfig(invalid); err == nil {
			t.Fatal("invalid event or resource passed full profile validation", invalid)
		}
	}
}

func TestUIProcessingTaskAdmissionRejectsReloadAndStoppedService(t *testing.T) {
	server := &CommandServer{StartedService: daemon.NewStartedService(daemon.ServiceOptions{
		Context: context.Background(), LogMaxLines: 32,
	})}
	t.Cleanup(server.StartedService.Close)
	if _, err := server.NewUIScriptTask("generic", 0); err == nil {
		t.Fatal("manual scripts were admitted without a running service")
	}
	func() {
		uiCaptureLifecycle.Lock()
		defer uiCaptureLifecycle.Unlock()
		uiProcessingTaskTestRejectWriter(t, server)
	}()
}

func TestUIProcessingManualBindingReturnsJSONLogsAndJoinsCancellation(t *testing.T) {
	httpdebug.StopService()
	t.Cleanup(httpdebug.StopService)
	directory := t.TempDir()
	prepared, err := httpdebug.PrepareProcessing(directory, nil, nil, &httpdebug.ScriptsConfig{
		Enabled: true,
		Generic: []httpdebug.GenericScript{
			{Name: "same", Disable: true, Source: "$done('disabled');"},
			{Name: "same", Source: "console.log('binding-log'); $done({position:1,trigger:$trigger});"},
			{Source: "console.warn('before-error'); throw Error('binding-error');"},
			{Source: "while(true){}", TimeoutMS: -1},
		},
	}, "", nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	httpdebug.ActivateProcessing(prepared, nil, nil, "")
	if _, err := prepared.RunScript(context.Background(), "generic", 0); !errors.Is(err, httpdebug.ErrScriptUnavailable) {
		t.Fatal("disabled original array position became runnable", err)
	}
	run := func(index int32) map[string]any {
		t.Helper()
		task, err := prepared.RunScript(context.Background(), "generic", int(index))
		if err != nil {
			t.Fatal(err)
		}
		binding := &UIScriptTask{task: task}
		defer binding.Close()
		text, err := binding.Run()
		if err != nil {
			t.Fatal(err)
		}
		binding.Join()
		var result map[string]any
		if err := json.Unmarshal([]byte(text), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	result := run(1)
	if result["result"] != `{"position":1,"trigger":"editor"}` || result["error"] != "" ||
		len(result["logs"].([]any)) != 1 || !strings.HasSuffix(result["logs"].([]any)[0].(string), "binding-log") {
		t.Fatal("manual output changed JSON text, original index or console output", result)
	}
	failed := run(2)
	if failed["result"] != "null" || !strings.Contains(failed["error"].(string), "binding-error") || len(failed["logs"].([]any)) != 1 {
		t.Fatal("failed invocation lost its error or preceding console output", failed)
	}
	task, err := prepared.RunScript(context.Background(), "generic", 3)
	if err != nil {
		t.Fatal(err)
	}
	binding := &UIScriptTask{task: task}
	binding.Close()
	joined := make(chan struct{})
	go func() { binding.Join(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(3 * time.Second):
		t.Fatal("binding cancellation left its native worker running")
	}
	text, err := binding.Run()
	if err != nil || !strings.Contains(text, "cancel") {
		t.Fatal("cancelled binding did not return a structured invocation error", text, err)
	}

	// The processing manager owns one store path for this process. These service
	// regressions reuse that path while replacing the actual daemon instance.
	profile := `{"outbounds":[{"type":"direct","tag":"direct"}],"experimental":{"scripts":{"enabled":true,"generic":[{"disabled":true,"source":"$done('disabled');"},{"source":"$done('old-profile');"},{"source":"while(true){}","timeout_ms":-1}]}}}`
	t.Run("egress-and-recording-readers-admit-manual-scripts", func(t *testing.T) {
		server := uiProcessingTaskTestServer(t)
		if err := server.StartOrReloadService(profile, &OverrideOptions{ProcessingDirectory: directory, ScriptTimezone: "UTC"}); err != nil {
			t.Fatal(err)
		}
		uiCaptureLifecycle.RLock()
		defer uiCaptureLifecycle.RUnlock()
		probeCreated := make(chan error, 1)
		go func() {
			probe, err := server.NewUIEgressProbe()
			if probe != nil {
				probe.Close()
			}
			probeCreated <- err
		}()
		select {
		case err := <-probeCreated:
			if err != nil {
				t.Fatal("a stable lifecycle reader prevented real egress admission", err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("real egress admission waited for another lifecycle reader")
		}
		captureStarted := make(chan error, 2)
		for range 2 {
			go func() { captureStarted <- server.StartUICapture(directory, `{}`, "", nil) }()
		}
		task, err := server.NewUIScriptTask("generic", 1)
		if err != nil {
			t.Fatal("egress or recording admission was mistaken for a service change", err)
		}
		defer func() { task.Close(); task.Join() }()
		result, invocationError := uiProcessingTaskTestOutput(t, task)
		if result != `"old-profile"` || invocationError != "" {
			t.Fatal("manual admission changed its current profile or original array index", result, invocationError)
		}
		started := 0
		for range 2 {
			select {
			case err := <-captureStarted:
				if err == nil {
					started++
				} else if !strings.Contains(err.Error(), "stop capture before changing its settings") {
					t.Fatal("concurrent recording start returned an unrelated error", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("recording settings waited for an ordinary lifecycle reader")
			}
		}
		if started != 1 || !httpdebug.Status().Active {
			t.Fatal("shared lifecycle readers bypassed recording's own serialization", started)
		}
	})
	t.Run("pending-and-active-service-writers-reject-stale-script-indices", func(t *testing.T) {
		server := uiProcessingTaskTestServer(t)
		overrides := &OverrideOptions{ProcessingDirectory: directory, ScriptTimezone: "UTC"}
		if err := server.StartOrReloadService(profile, overrides); err != nil {
			t.Fatal(err)
		}
		oldInstance, oldPrepared, oldRoutes := server.Instance(), server.uiProcessingState.prepared, server.uiProcessingRoutes
		oldTask, err := server.NewUIScriptTask("generic", 2)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { oldTask.Close(); oldTask.Join() })
		cancelled, release := uiProcessingTaskTestEgressDrain(t, server)
		uiCaptureLifecycle.RLock()
		releaseReader := sync.OnceFunc(uiCaptureLifecycle.RUnlock)
		defer releaseReader()
		reloaded := make(chan error, 1)
		go func() {
			reloaded <- server.StartOrReloadService(strings.Replace(profile, "old-profile", "new-profile", 1), overrides)
		}()
		deadline := time.Now().Add(3 * time.Second)
		for uiCaptureLifecycle.TryRLock() {
			uiCaptureLifecycle.RUnlock()
			if time.Now().After(deadline) {
				t.Fatal("real reload did not queue its lifecycle writer")
			}
			runtime.Gosched()
		}
		uiProcessingTaskTestRejectWriter(t, server)
		releaseReader()
		select {
		case <-cancelled:
		case <-time.After(3 * time.Second):
			t.Fatal("real reload did not cancel the prior egress operation")
		}
		uiProcessingTaskTestRejectWriter(t, server)
		release()
		select {
		case err := <-reloaded:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("real reload did not join egress and script cleanup")
		}
		if server.Instance() == oldInstance || server.uiProcessingState.prepared == oldPrepared || oldRoutes.ctx.Err() == nil {
			t.Fatal("reload retained an old instance, processor or routing lifetime")
		}
		if _, err := oldPrepared.RunScript(context.Background(), "generic", 1); !errors.Is(err, httpdebug.ErrScriptsInactive) {
			t.Fatal("a captured prior processor resolved its index after replacement", err)
		}
		if _, invocationError := uiProcessingTaskTestOutput(t, oldTask); !strings.Contains(invocationError, "cancel") {
			t.Fatal("real reload left the prior binding worker running", invocationError)
		}
		newTask, err := server.NewUIScriptTask("generic", 1)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { newTask.Close(); newTask.Join() })
		result, invocationError := uiProcessingTaskTestOutput(t, newTask)
		if result != `"new-profile"` || invocationError != "" {
			t.Fatal("a fresh request failed to resolve the replacement's original index", result, invocationError)
		}
		stoppingTask, err := server.NewUIScriptTask("generic", 2)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { stoppingTask.Close(); stoppingTask.Join() })
		cancelled, release = uiProcessingTaskTestEgressDrain(t, server)
		closed := make(chan error, 1)
		go func() { closed <- server.CloseService() }()
		select {
		case <-cancelled:
		case <-time.After(3 * time.Second):
			t.Fatal("real service close did not cancel egress routing")
		}
		uiProcessingTaskTestRejectWriter(t, server)
		release()
		select {
		case err := <-closed:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("real service close did not join script and egress cleanup")
		}
		if _, invocationError := uiProcessingTaskTestOutput(t, stoppingTask); !strings.Contains(invocationError, "cancel") {
			t.Fatal("service close left its binding worker running", invocationError)
		}
		if task, err := server.NewUIScriptTask("generic", 1); task != nil || err == nil || !strings.Contains(err.Error(), "not running") {
			if task != nil {
				task.Close()
				task.Join()
			}
			t.Fatal("stopped service admitted an index from its prior profile", err)
		}
	})
}

func uiProcessingTaskTestServer(t *testing.T) *CommandServer {
	t.Helper()
	directory := t.TempDir()
	basePath := sBasePath
	sBasePath = directory
	t.Cleanup(func() { sBasePath = basePath })
	ctx := filemanager.WithDefault(baseContext(nil), directory, directory, os.Getuid(), os.Getgid())
	server := &CommandServer{
		StartedService: daemon.NewStartedService(daemon.ServiceOptions{Context: ctx, LogMaxLines: 32}),
		ctx:            ctx,
		powerManager:   powerreport.NewManager(),
	}
	t.Cleanup(func() {
		if err := server.CloseService(); err != nil {
			t.Error(err)
		}
		server.StartedService.Close()
	})
	return server
}

func uiProcessingTaskTestRejectWriter(t *testing.T, server *CommandServer) {
	t.Helper()
	completed := make(chan error, 1)
	go func() {
		task, err := server.NewUIScriptTask("generic", 1)
		if task != nil {
			task.Close()
			task.Join()
		}
		completed <- err
	}()
	select {
	case err := <-completed:
		if err == nil || err.Error() != "local service is changing during script execution" {
			t.Fatal("script admission did not reject the lifecycle writer", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("script admission waited through a lifecycle writer")
	}
}

func uiProcessingTaskTestOutput(t *testing.T, task *UIScriptTask) (string, string) {
	t.Helper()
	completed := make(chan string, 1)
	go func() {
		text, err := task.Run()
		if err != nil {
			text = err.Error()
		}
		task.Join()
		completed <- text
	}()
	select {
	case text := <-completed:
		var output struct {
			Result string `json:"result"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal([]byte(text), &output); err != nil {
			t.Fatal("binding output was not structured JSON", text, err)
		}
		return output.Result, output.Error
	case <-time.After(3 * time.Second):
		t.Fatal("binding Run and Join did not finish after script completion or cancellation")
		return "", ""
	}
}

func uiProcessingTaskTestEgressDrain(t *testing.T, server *CommandServer) (<-chan struct{}, func()) {
	t.Helper()
	probe, err := server.NewUIEgressProbe()
	if err != nil {
		t.Fatal(err)
	}
	entered, cancelled, release, drained := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	releaseRouting := sync.OnceFunc(func() { close(release) })
	var drainError error
	go func() {
		_, drainError = probe.routes.dial(probe.ctx, func(ctx context.Context) (net.Conn, error) {
			close(entered)
			<-ctx.Done()
			close(cancelled)
			// DNS/dial cleanup can outlive request cancellation. The real service
			// writer must hold the instance until that admitted operation exits.
			<-release
			return nil, ctx.Err()
		})
		close(drained)
	}()
	t.Cleanup(func() {
		releaseRouting()
		probe.Close()
		select {
		case <-drained:
			if !errors.Is(drainError, context.Canceled) {
				t.Error("service teardown did not cancel the admitted egress operation", drainError)
			}
		case <-time.After(3 * time.Second):
			t.Error("cancelled egress operation did not leave its routing lifetime")
		}
	})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("real egress probe did not admit its routing operation")
	}
	return cancelled, releaseRouting
}

func TestUIProcessingNetworkEventsStartAfterPublicationAndEndWithLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []string
	callback := uiProcessingNetworkCallback(ctx, func(event string) error { events = append(events, event); return nil }, true)
	callback(nil, 0)
	if len(events) != 0 {
		t.Fatal("initial absence was treated as a network change")
	}
	callback(&control.Interface{Name: "wlan0", Index: 1}, 0)
	// A valid same-interface callback represents a confirmed core DNS update.
	callback(&control.Interface{Name: "wlan0", Index: 1}, 0)
	callback(nil, 0)
	callback(nil, 0)
	callback(&control.Interface{Name: "rmnet0", Index: 2}, 0)
	if len(events) != 4 {
		t.Fatal("network return, DNS update, loss or switch event was lost or duplicated", events)
	}
	for _, event := range events {
		if event != httpdebug.ScriptEventNetworkChanged {
			t.Fatal("default-interface callback used a different trigger", event)
		}
	}
	cancel()
	callback(&control.Interface{Name: "late", Index: 3}, 0)
	if len(events) != 4 {
		t.Fatal("a copied monitor callback escaped its closed service lifecycle")
	}
}
