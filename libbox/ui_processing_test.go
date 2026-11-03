package libbox

import (
	"strings"
	"testing"
)

func TestUIHTTPRewriteSchemaValidationAndFormatting(t *testing.T) {
	content := `{"outbounds":[{"type":"direct","tag":"kept"}],"experimental":{"cache_file":{"enabled":true},"mitm":{"enabled":true,"hosts":["api.example.test"]},"scripts":{"enabled":true,"http":[{"pattern":"/script$","phase":"response","requires_body":true,"source":"$done({body: JSON.stringify({ok:true})});"}],"cron":[{"cron":"0 2 * * *","source":"$done();"}]},"http_rewrite":{"enabled":true,"resources":{"mock.json":{"data":"{\"ok\":true}"}},"map_local":[{"pattern":"/mock$","resource":"mock.json"}]}}}`
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
	for _, field := range []string{"http_rewrite", "mitm", "scripts", "cron", "cache_file", "kept", "mock.json", "$done"} {
		if !strings.Contains(formatted.Value, field) {
			t.Errorf("format discarded %q", field)
		}
	}
	if err = CheckConfig(formatted.Value); err != nil {
		t.Fatalf("formatted profile rejected: %v", err)
	}
	schema, err := GenerateConfigSchema()
	if err != nil || !strings.Contains(schema.Value, `"http_rewrite"`) || !strings.Contains(schema.Value, `"requires_body"`) {
		t.Fatalf("extension missing from schema: %v", err)
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
