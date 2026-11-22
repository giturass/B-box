package libbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/daemon"
	"github.com/sagernet/sing-box/experimental/httpdebug"
	"github.com/sagernet/sing/common/control"
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
		if _, err := server.NewUIScriptTask("generic", 0); err == nil || !strings.Contains(err.Error(), "changing") {
			t.Fatal("manual script admission waited through a reload", err)
		}
	}()
}

func TestUIProcessingManualBindingReturnsJSONLogsAndJoinsCancellation(t *testing.T) {
	httpdebug.StopService()
	t.Cleanup(httpdebug.StopService)
	prepared, err := httpdebug.PrepareProcessing(t.TempDir(), nil, nil, &httpdebug.ScriptsConfig{
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
