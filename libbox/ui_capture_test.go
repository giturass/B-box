package libbox

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUICaptureProfileRoundTripAndValidation(t *testing.T) {
	original := `{"log":{"level":"warn"},"outbounds":[{"type":"direct","tag":"saved-direct"}],"route":{"final":"saved-direct"},"experimental":{"cache_file":{"enabled":true}}}`
	if value, err := ReadUICaptureConfig(original); err != nil || value != "" {
		t.Fatalf("missing settings: %q %v", value, err)
	}
	rules := `{"https":true,"decryptHosts":["example.test"],"decryptBypassHosts":["pinned.test"],"rewriteEnabled":true,"scripts":[{"pattern":"^https://example\\.test/","source":"$done({headers: $request.headers});"}],"mapLocal":[{"pattern":"/mock$","data":"{}"}]}`
	updated, err := SetUICaptureConfig(original, rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckConfig(updated.Value); err != nil {
		t.Fatal(err)
	}
	formatted, err := FormatConfig(updated.Value)
	if err != nil {
		t.Fatal(err)
	}
	value, err := ReadUICaptureConfig(formatted.Value)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(value), &config); err != nil {
		t.Fatal(err)
	}
	if config["https"] != true || config["rewriteEnabled"] != true || len(config["scripts"].([]any)) != 1 {
		t.Fatalf("lost capture settings: %s", value)
	}
	for _, retained := range []string{"saved-direct", `"level": "warn"`, `"cache_file"`} {
		if !strings.Contains(formatted.Value, retained) {
			t.Fatalf("lost original profile field %s", retained)
		}
	}
	for _, invalid := range []string{
		`{"experimental":{"http_debug":{"headerRewrite":[{"pattern":".","header":"Transfer-Encoding","action":"header-add","value":"chunked"}]}}}`,
		`{"experimental":{"http_debug":{"scripts":[{"pattern":".","source":"const = ;"}]}}}`,
		`{"experimental":{"http_debug":{"mapLocal":[{"pattern":"[","data":"hello"}]}}}`,
		`{"experimental":{"http_debug":{"unsupported":true}}}`,
	} {
		if err := CheckConfig(invalid); err == nil {
			t.Fatalf("checkConfig accepted invalid HTTP rules: %s", invalid)
		}
	}
	empty, err := SetUICaptureConfig(original, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := ReadUICaptureConfig(empty.Value); err != nil || value == "" {
		t.Fatalf("explicit empty settings confused with legacy fallback: %q %v", value, err)
	}
}

func TestUICaptureOptionsAppearInGeneratedSchema(t *testing.T) {
	schema, err := GenerateConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"http_debug", "headerRewrite", "urlRewrite", "bodyRewrite", "mapLocal", "scripts", "decryptBypassHosts"} {
		if !strings.Contains(schema.Value, `"`+field+`"`) {
			t.Fatalf("schema is missing %s", field)
		}
	}
}
