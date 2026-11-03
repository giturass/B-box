package libbox

import (
	"strings"
	"testing"
)

func TestUICaptureSettingsAreLocalAndRejectRetiredFeatures(t *testing.T) {
	if _, err := ValidateUICaptureConfig(`{"version":3,"ports":[8443],"extractHosts":true}`); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		`{"rewriteEnabled":true}`, `{"scripts":[]}`, `{"mapLocal":[]}`, `{"version":99}`, `{"tlsOffsets":[]}`,
		`{"captureNetwork":true}`, `{"captureTlsKeys":true}`, `{"captureNetwork":false}`, `{"captureTlsKeys":false}`,
		`{"includeHosts":["api.test"]}`, `{"excludeHosts":["private.test"]}`, `{"includeHosts":[]}`, `{"excludeHosts":[]}`,
		`{"tlsLibraries":["relative.so"]}`, `{"https":true}`, `{"decryptHosts":[]}`, `{"decryptBypassHosts":[]}`, `{"blockQUIC":true}`,
	} {
		if _, err := ValidateUICaptureConfig(invalid); err == nil {
			t.Fatalf("accepted retired or invalid settings: %s", invalid)
		}
	}
	schema, err := GenerateConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(schema.Value, `"http_debug"`) {
		t.Fatal("retired extension remains in the core schema")
	}
}

func TestUICaptureRetiredOptionsMigration(t *testing.T) {
	original := `{"log":{"level":"warn"},"outbounds":[{"type":"direct","tag":"kept"}],"experimental":{"cache_file":{"enabled":true},"http_debug":{"scripts":[{"invalid":true}]}}}`
	cleaned, err := RemoveUIRetiredCaptureOptions(original)
	if err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{"kept", "warn", "cache_file"} {
		if !strings.Contains(cleaned, kept) {
			t.Fatal("migration removed unrelated content")
		}
	}
	if strings.Contains(cleaned, "http_debug") {
		t.Fatal("extension not removed")
	}
	if err = CheckConfig(original); err != nil {
		t.Fatalf("old profile cannot start: %v", err)
	}
	formatted, err := FormatConfig(original)
	if err != nil || strings.Contains(formatted.Value, "http_debug") {
		t.Fatalf("format did not drop retired settings: %v", err)
	}
	if again, err := RemoveUIRetiredCaptureOptions(cleaned); err != nil || again != cleaned {
		t.Fatal("migration is not idempotent")
	}
	without := `{"log": { "level": "warn" }}`
	if got, err := RemoveUIRetiredCaptureOptions(without); err != nil || got != without {
		t.Fatal("unrelated configuration was reformatted")
	}
	// Unrelated unknown settings still fail full core validation.
	if err = CheckConfig(`{"experimental":{"http_debug":{},"unsupported":true}}`); err == nil {
		t.Fatal("unknown core option was silently removed")
	}
}
