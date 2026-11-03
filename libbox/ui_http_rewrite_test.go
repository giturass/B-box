package libbox

import (
	"strings"
	"testing"
)

func TestUIHTTPRewriteSchemaValidationAndFormatting(t *testing.T) {
	content := `{"outbounds":[{"type":"direct","tag":"kept"}],"experimental":{"cache_file":{"enabled":true},"http_rewrite":{"enabled":true,"mitm":{"enabled":true,"hosts":["api.example.test"]},"resources":{"mock.json":{"data":"{\"ok\":true}"}},"map_local":[{"pattern":"/mock$","resource":"mock.json"}],"scripts":[{"pattern":"/script$","phase":"response","requires_body":true,"source":"$done({body: JSON.stringify({ok:true})});"}]}}}`
	if err := CheckConfig(content); err != nil {
		t.Fatal(err)
	}
	needsCA, err := NeedsUIHTTPRewriteCA(content)
	if err != nil || !needsCA {
		t.Fatalf("missing profile MITM requirement: %v %v", needsCA, err)
	}
	formatted, err := FormatConfig(content)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"http_rewrite", "cache_file", "kept", "mock.json", "$done"} {
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
		`{"experimental":{"http_rewrite":{"scripts":[{"pattern":".","phase":"response","source":"const ="}]}}}`,
		`{"experimental":{"http_rewrite":{"body_rewrite":[{"pattern":"[","phase":"request","regex":"x","replacement":"y"}]}}}`,
	} {
		if err = CheckConfig(invalid); err == nil {
			t.Errorf("invalid rewrite bypassed complete config validation: %s", invalid)
		}
	}
	if needs, err := NeedsUIHTTPRewriteCA(`{"experimental":{"http_rewrite":{"enabled":false,"mitm":{"enabled":true,"hosts":["api.test"]}}}}`); err != nil || needs {
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
	if needs, err := NeedsUIHTTPRewriteCA(cleaned); err != nil || needs {
		t.Fatal("retired settings reactivated interception")
	}
}
