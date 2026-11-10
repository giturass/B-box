package libbox

import (
	"strings"
	"testing"
)

func TestUIHTTPRewriteSchemaValidationAndFormatting(t *testing.T) {
	content := `{"outbounds":[{"type":"direct","tag":"kept"}],"experimental":{"cache_file":{"enabled":true},"mitm":{"enabled":true,"hosts":["-private.example.test","api?.example.test:8443","*.example.test:0"],"h2":true,"skip_server_cert_verify":true,"block_quic":false},"scripts":{"enabled":true,"http":[{"pattern":"/script$","phase":"response","requires_body":true,"source":"$done({body: JSON.stringify({ok:true})});"}],"cron":[{"cron":"0 2 * * *","source":"$done();"}]},"http_rewrite":{"enabled":true,"resources":{"mock.json":{"data":"{\"ok\":true}"}},"map_local":[{"pattern":"/mock$","resource":"mock.json"}]}}}`
	if err := CheckConfig(content); err != nil {
		t.Fatal(err)
	}
	needsCA, err := NeedsUIMITMCA(content)
	if err != nil || !needsCA {
		t.Fatalf("missing profile MITM requirement: %v %v", needsCA, err)
	}
	formatted, err := FormatConfig(content)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"http_rewrite", "mitm", "h2", "skip_server_cert_verify", "block_quic", "-private.example.test", "api?.example.test:8443", "scripts", "cron", "cache_file", "kept", "mock.json", "$done"} {
		if !strings.Contains(formatted.Value, field) {
			t.Errorf("format discarded %q", field)
		}
	}
	if err = CheckConfig(formatted.Value); err != nil {
		t.Fatalf("formatted profile rejected: %v", err)
	}
	options, err := parseConfig(baseContext(nil), formatted.Value)
	if err != nil {
		t.Fatal(err)
	}
	mitm := options.Experimental.MITM
	if !mitm.H2 || !mitm.SkipServerCertVerify || mitm.BlockQUIC {
		t.Fatalf("formatting changed MITM options: %+v", mitm)
	}
	disabled, err := FormatConfig(strings.Replace(content, `"h2":true`, `"h2":false`, 1))
	if err != nil {
		t.Fatal(err)
	}
	disabledOptions, err := parseConfig(baseContext(nil), disabled.Value)
	if err != nil || disabledOptions.Experimental.MITM.H2 || !strings.Contains(disabled.Value, `"h2": false`) {
		t.Fatal("explicit HTTP/2 opt-out disappeared during full profile formatting", err)
	}
	schema, err := GenerateConfigSchema()
	if err != nil || !strings.Contains(schema.Value, `"http_rewrite"`) || !strings.Contains(schema.Value, `"requires_body"`) {
		t.Fatalf("extension missing from schema: %v", err)
	}
	for _, field := range []string{`"h2"`, `"skip_server_cert_verify"`, `"block_quic"`} {
		if !strings.Contains(schema.Value, field) {
			t.Errorf("MITM option missing from schema: %s", field)
		}
	}
	for _, invalid := range []string{
		`{"experimental":{"http_rewrite":{"enabled":true,"unsupported":true}}}`,
		`{"experimental":{"scripts":{"http":[{"pattern":".","phase":"response","source":"const ="}]}}}`,
		`{"experimental":{"http_rewrite":{"body_rewrite":[{"pattern":"[","phase":"request","regex":"x","replacement":"y"}]}}}`,
	} {
		if err = CheckConfig(invalid); err == nil {
			t.Errorf("invalid rewrite bypassed complete config validation: %s", invalid)
		}
	}
	if needs, err := NeedsUIMITMCA(`{"experimental":{"mitm":{"enabled":false,"hosts":["api.test"]}}}`); err != nil || needs {
		t.Fatal("disabled profile requested a certificate")
	}
}

func TestUIHTTPRewriteRetiredOptionsDoNotActivateNewRules(t *testing.T) {
	content := `{"experimental":{"http_debug":{"enabled":true,"scripts":[{}]},"http_rewrite":{"enabled":false,"map_local":[{"pattern":".","data":"explicit"}]}}}`
	cleaned, err := RemoveUIRetiredCaptureOptions(content)
	if err != nil || strings.Contains(cleaned, "http_debug") || !strings.Contains(cleaned, "http_rewrite") || !strings.Contains(cleaned, "explicit") {
		t.Fatalf("migration modified new rules: %s %v", cleaned, err)
	}
	if err = CheckConfig(cleaned); err != nil {
		t.Fatal(err)
	}
	if needs, err := NeedsUIMITMCA(cleaned); err != nil || needs {
		t.Fatal("retired settings reactivated interception")
	}
}

func TestUIUnaryGRPCAndRejectActionsRoundTrip(t *testing.T) {
	content := `{"experimental":{"mitm":{"enabled":true,"h2":true,"hosts":["grpc.example.test"]},"scripts":{"enabled":true,"http":[{"pattern":"/service/Unary$","phase":"response","requires_body":true,"binary_body":true,"grpc_mode":"unary","source":"$done();"}]},"http_rewrite":{"enabled":true,"url_rewrite":[{"pattern":"/empty$","action":"reject-200"},{"pattern":"/json$","action":"reject-dict"}]}}}`
	if err := CheckConfig(content); err != nil {
		t.Fatal(err)
	}
	formatted, err := FormatConfig(content)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"grpc_mode": "unary"`, `"binary_body": true`, `"reject-200"`, `"reject-dict"`} {
		if !strings.Contains(formatted.Value, field) {
			t.Fatalf("format discarded %s: %s", field, formatted.Value)
		}
	}
	if err := CheckConfig(formatted.Value); err != nil {
		t.Fatal(err)
	}
	schema, err := GenerateConfigSchema()
	if err != nil || !strings.Contains(schema.Value, `"grpc_mode"`) {
		t.Fatal("gRPC mode missing from schema", err)
	}
	for _, mode := range []string{`"grpc_mode":"stream"`, `"grpc_mode":"unary","requires_body":false`, `"grpc_mode":"unary","binary_body":false`} {
		invalid := `{"experimental":{"scripts":{"http":[{"pattern":".","phase":"response","source":"$done();",` + mode + `}]}}}`
		if err := CheckConfig(invalid); err == nil {
			t.Fatal("invalid gRPC mode bypassed profile validation", invalid)
		}
	}
}

func TestUIProcessingMigrationPreservesScopeAndDisablesOldExclusions(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, excluded := range []bool{false, true} {
			flag := "false"
			if enabled {
				flag = "true"
			}
			exclusions := "[]"
			if excluded {
				exclusions = `["private.test"]`
			}
			content := `{"experimental":{"http_rewrite":{"enabled":` + flag + `,"mitm":{"enabled":true,"hosts":["*.test"],"excluded_hosts":` + exclusions + `},"scripts":[{"pattern":".","phase":"request","resource":"script.js"}],"resources":{"script.js":{"data":"$done();"},"body":{"data":"kept"}}}}}`
			cleaned, err := RemoveUIRetiredCaptureOptions(content)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(cleaned, "excluded_hosts") {
				t.Fatal("retired exclusions retained")
			}
			if err = CheckConfig(cleaned); err != nil {
				t.Fatal(err)
			}
			options, err := parseConfig(baseContext(nil), cleaned)
			if err != nil {
				t.Fatal(err)
			}
			c := options.Experimental
			if c.MITM.Enabled != (enabled && !excluded) || c.Scripts.Enabled != enabled {
				t.Fatal("migration changed enabled scope")
			}
			if len(c.MITM.Ports) != 4 || len(c.Scripts.HTTP) != 1 || len(c.Scripts.Resources) != 1 || len(c.HTTPRewrite.Resources) != 2 {
				t.Fatal("migration lost data or expanded ports")
			}
			if again, err := RemoveUIRetiredCaptureOptions(cleaned); err != nil || again != cleaned {
				t.Fatal("migration not idempotent")
			}
		}
	}
	if _, err := RemoveUIRetiredCaptureOptions(`{"experimental":{"mitm":{},"http_rewrite":{"mitm":{}}}}`); err == nil {
		t.Fatal("conflicting MITM settings silently overwritten")
	}
	for _, content := range []string{
		`{"experimental":{"mitm":{"enabled":true,"hosts":[]}}}`,
		`{"experimental":{"scripts":{"enabled":true,"cron":[{"cron":"* * * * *","source":"$done();"}]}}}`,
	} {
		if need, err := NeedsUIMITMCA(content); err != nil || need {
			t.Fatalf("unnecessary CA requirement: %v %v", need, err)
		}
	}
}

func TestUIMigratingEmptyMITMPortsPreservesHTTPPortDefaults(t *testing.T) {
	for _, ports := range []string{"[]", "null"} {
		content := `{"experimental":{"http_rewrite":{"enabled":true,"ports":` + ports + `,"mitm":{"enabled":true,"hosts":["api.test"]}}}}`
		cleaned, err := RemoveUIRetiredCaptureOptions(content)
		if err != nil {
			t.Fatal(err)
		}
		options, err := parseConfig(baseContext(nil), cleaned)
		if err != nil {
			t.Fatal(err)
		}
		if len(options.Experimental.MITM.Ports) != 4 {
			t.Fatal("empty legacy port filter expanded to every TLS port")
		}
	}
}

func TestUIScriptCapabilitiesSchemaAndValidation(t *testing.T) {
	content := `{"experimental":{"scripts":{"enabled":true,"http":[{"pattern":".","phase":"request","storage_namespace":"api-cache","http_client":{"allowed_origins":["https://api.test"],"max_requests":2},"source":"throw Error('validation must not execute scripts');"}],"cron":[{"cron":"* * * * *","storage_namespace":"api-cache","http_client":{"allowed_origins":[],"max_requests":1},"source":"setTimeout(()=>$done(),1);"}]}}}`
	if err := CheckConfig(content); err != nil {
		t.Fatal(err)
	}
	formatted, err := FormatConfig(content)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := GenerateConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"http_client", "allowed_origins", "max_requests", "storage_namespace"} {
		if !strings.Contains(formatted.Value, field) || !strings.Contains(schema.Value, `"`+field+`"`) {
			t.Errorf("script capability lost by formatting or schema: %s", field)
		}
	}
	for _, invalid := range []string{
		strings.Replace(content, "https://api.test", "https://*.test", 1),
		strings.Replace(content, `"max_requests":2`, `"max_requests":-1`, 1),
		strings.Replace(content, "api-cache", "../escape", 1),
		strings.Replace(content, `"max_requests":2`, `"proxy":"direct"`, 1),
	} {
		if err := CheckConfig(invalid); err == nil {
			t.Fatal("invalid script capability bypassed complete config validation")
		}
	}
}

func TestUISurgeProcessingFieldsSurviveFormattingWithoutLoadingResources(t *testing.T) {
	content := `{
		"experimental": {
			"http_rewrite": {
				"enabled": true, "max_request_body_bytes": 33554433, "max_response_body_bytes": 10485761,
				"resources": {"mock": {"data_type":"file", "data":"mock.json", "update_interval":60}},
				"map_local": [{"pattern":"(?<=/)mock$","resource":"mock","status":999}],
				"url_rewrite": [{"pattern":"/ws$","action":"header","replacement":"ws://example.test/socket"}],
				"body_rewrite": [{"pattern":".","phase":"response","jq":".ok=true"}]
			},
			"scripts": {
				"enabled":true, "max_body_bytes":-1,
				"http": [
					{"pattern":".","phase":"request","script_path":"missing.js","debug":true,"timeout_ms":5000,
					 "http_client":{"allowed_origins":[],"max_body_bytes":8192}},
					{"pattern":".","phase":"response","script_path":"https://missing.invalid/script.js","script_update_interval":60}
				]
			}
		}
	}`
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
	if scripts.HTTP[0].HTTPClient == nil || scripts.HTTP[0].HTTPClient.AllowedOrigins == nil || len(scripts.HTTP[0].HTTPClient.AllowedOrigins) != 0 {
		t.Fatal("formatting removed the explicit network prohibition")
	}
	if scripts.HTTP[1].HTTPClient != nil {
		t.Fatal("formatting restricted a default network client")
	}
	rewrite := options.Experimental.HTTPRewrite
	if rewrite.MaxRequestBodyBytes != 33554433 || rewrite.MaxResponseBodyBytes != 10485761 ||
		rewrite.BodyRewrite[0].JQ == nil || *rewrite.BodyRewrite[0].JQ != ".ok=true" || rewrite.MapLocal[0].Status != 999 {
		t.Fatal("formatting changed body limits, jq or mock status")
	}
	schema, err := GenerateConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"max_request_body_bytes", "max_response_body_bytes", "jq", "update_interval", "script_path", "script_update_interval", "debug"} {
		if !strings.Contains(formatted.Value, `"`+field+`"`) || !strings.Contains(schema.Value, `"`+field+`"`) {
			t.Errorf("field missing from formatting or schema: %s", field)
		}
	}
	for _, invalid := range []string{
		strings.Replace(content, `"jq":".ok=true"`, `"jq":".ok=true","regex":"x"`, 1),
		strings.Replace(content, "missing.js", "../outside.js", 1),
		strings.Replace(content, "33554433", "-2", 1),
		strings.Replace(content, "10485761", "-2", 1),
	} {
		if err := CheckConfig(invalid); err == nil {
			t.Fatal("invalid processing extension bypassed full config validation")
		}
	}
}
