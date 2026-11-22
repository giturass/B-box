package libbox

import (
	"encoding/json"
	"reflect"
	"testing"
)

func processingMatchRules(t *testing.T, content, extension, category string) []map[string]json.RawMessage {
	t.Helper()
	var profile struct {
		Experimental map[string]json.RawMessage `json:"experimental"`
	}
	if err := json.Unmarshal([]byte(content), &profile); err != nil {
		t.Fatal(err)
	}
	var processing map[string]json.RawMessage
	if err := json.Unmarshal(profile.Experimental[extension], &processing); err != nil {
		t.Fatal(err)
	}
	var rules []map[string]json.RawMessage
	if err := json.Unmarshal(processing[category], &rules); err != nil {
		t.Fatal(err)
	}
	return rules
}

func TestUIProcessingCompoundMatchCheckAndRoundTrip(t *testing.T) {
	content := `{
		"outbounds":[{"type":"direct","tag":"kept"}],
		"experimental":{
			"cache_file":{"enabled":true},
			"http_rewrite":{
				"enabled":true,
				"header_rewrite":[{"match":{"subject":"request.header","header":"Host","equals":"api.example.test"},"phase":"request","operation":"add","header":"X-Test","value":"yes"}],
				"url_rewrite":[{"pattern":"^http://api\\.example\\.test/(.*)$","match":{"subject":"request.method","equals":"POST"},"action":"header","replacement":"http://mapped.test/$1"}],
				"map_remote":[{"match":{"any":[{"subject":"url","equals":"http://api.example.test/one"},{"subject":"request.header","header":"X-Remote","regex":"(?i)^yes$"}]},"replacement":"http://mapped.test/remote"}],
				"body_rewrite":[{"match":{"all":[{"subject":"request.method","equals":"POST"},{"subject":"response.status","equals":999},{"subject":"response.header","header":"Content-Type","regex":"(?i)json"}]},"phase":"response","jq":".ok=true"}],
				"map_local":[
					{"match":{"all":[{"subject":"url","regex":"/mock$"},{"any":[{"subject":"request.method","equals":"GET"},{"subject":"request.header","header":"X-Empty","equals":""}]}]},"data_type":"file","data":"missing-mock.json"},
					{"pattern":"/legacy$","data":"pattern-only"}
				]
			},
			"scripts":{
				"enabled":true,
				"http":[
					{"match":{"all":[{"subject":"url","regex":"/script$"},{"subject":"request.method","equals":"POST"}]},"phase":"request","source":"throw Error('validation must not run scripts');"},
					{"pattern":"/script$","match":{"any":[{"subject":"response.status","equals":100},{"all":[{"subject":"request.header","header":"X-Request","equals":"kept"},{"subject":"response.header","header":"X-Response","regex":"(?i)^yes$"}]}]},"phase":"response","script_path":"https://missing.invalid/script.js"}
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
	if err := CheckConfig(formatted.Value); err != nil {
		t.Fatalf("formatted compound-match profile rejected: %v", err)
	}
	for extension, categories := range map[string][]string{
		"http_rewrite": {"header_rewrite", "url_rewrite", "map_remote", "body_rewrite", "map_local"},
		"scripts":      {"http"},
	} {
		for _, category := range categories {
			original := processingMatchRules(t, content, extension, category)
			actual := processingMatchRules(t, formatted.Value, extension, category)
			if len(original) != len(actual) {
				t.Fatalf("formatting changed %s.%s rule count", extension, category)
			}
			for index, rule := range original {
				for _, key := range []string{"pattern", "match"} {
					var before, after any
					if raw, exists := rule[key]; exists {
						if err := json.Unmarshal(raw, &before); err != nil {
							t.Fatal(err)
						}
					}
					if raw, exists := actual[index][key]; exists {
						if err := json.Unmarshal(raw, &after); err != nil {
							t.Fatal(err)
						}
					}
					if !reflect.DeepEqual(before, after) {
						t.Fatalf("formatting changed %s.%s[%d].%s: %v -> %v", extension, category, index, key, before, after)
					}
				}
			}
		}
	}
	needsCA, err := NeedsUIMITMCA(formatted.Value)
	if err != nil || needsCA {
		t.Fatalf("compound rules enabled MITM or required a CA: %v, %v", needsCA, err)
	}
}

func findProcessingCompoundSchema(value any) []any {
	switch node := value.(type) {
	case map[string]any:
		if variants, ok := node["oneOf"].([]any); ok {
			var all, anyGroup bool
			for _, variant := range variants {
				object, _ := variant.(map[string]any)
				properties, _ := object["properties"].(map[string]any)
				all = all || properties["all"] != nil
				anyGroup = anyGroup || properties["any"] != nil
			}
			if all && anyGroup {
				return variants
			}
		}
		for _, child := range node {
			if found := findProcessingCompoundSchema(child); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range node {
			if found := findProcessingCompoundSchema(child); found != nil {
				return found
			}
		}
	}
	return nil
}

func TestUIProcessingCompoundMatchSchemaUsesRecursiveTypedConditions(t *testing.T) {
	result, err := GenerateConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal([]byte(result.Value), &document); err != nil {
		t.Fatal(err)
	}
	variants := findProcessingCompoundSchema(document)
	if len(variants) == 0 {
		t.Fatal("schema omitted the recursive compound-match union")
	}
	want := map[string]bool{"all": false, "any": false}
	for _, subject := range []string{"url", "request.method", "request.header", "response.status", "response.header"} {
		want[subject+" equals"], want[subject+" regex"] = false, false
	}
	for _, variant := range variants {
		node, _ := variant.(map[string]any)
		properties, _ := node["properties"].(map[string]any)
		required := make(map[string]bool)
		if fields, ok := node["required"].([]any); ok {
			for _, field := range fields {
				if name, ok := field.(string); ok {
					required[name] = true
				}
			}
		}
		if node["additionalProperties"] != false {
			t.Fatal("compound condition schema allows unknown fields")
		}
		for _, group := range []string{"all", "any"} {
			if array, ok := properties[group].(map[string]any); ok {
				items, _ := array["items"].(map[string]any)
				if array["type"] != "array" || items["$ref"] == nil || !required[group] || len(properties) != 1 {
					t.Fatalf("%s group schema lost its recursive items", group)
				}
				want[group] = true
			}
		}
		subjectNode, _ := properties["subject"].(map[string]any)
		subject, _ := subjectNode["const"].(string)
		if subject == "" {
			continue
		}
		for _, comparison := range []string{"equals", "regex"} {
			if operand, ok := properties[comparison].(map[string]any); ok {
				if !required["subject"] || !required[comparison] {
					t.Fatalf("%s %s schema lost a required comparison field", subject, comparison)
				}
				if subject == "request.header" || subject == "response.header" {
					header, _ := properties["header"].(map[string]any)
					if header["type"] != "string" || !required["header"] {
						t.Fatalf("%s schema lost the required header name", subject)
					}
				} else if properties["header"] != nil {
					t.Fatalf("%s schema permits an unrelated header name", subject)
				}
				wantType := "string"
				if subject == "response.status" && comparison == "equals" {
					wantType = "integer"
					if operand["minimum"] != float64(100) || operand["maximum"] != float64(999) {
						t.Fatal("response status schema omitted the valid HTTP status range")
					}
				}
				if operand["type"] != wantType {
					t.Fatalf("%s %s schema uses %v, want %s", subject, comparison, operand["type"], wantType)
				}
				want[subject+" "+comparison] = true
			}
		}
	}
	for condition, found := range want {
		if !found {
			t.Errorf("schema omitted %s", condition)
		}
	}
}

func TestUIProcessingCompoundMatchInvalidProfilesAreRejected(t *testing.T) {
	for _, invalid := range []string{
		`{"experimental":{"http_rewrite":{"map_local":[{"data":"missing matcher"}]}}}`,
		`{"experimental":{"http_rewrite":{"map_local":[{"pattern":".","match":null,"data":"null matcher"}]}}}`,
		`{"experimental":{"http_rewrite":{"map_local":[{"match":{},"data":"empty matcher"}]}}}`,
		`{"experimental":{"http_rewrite":{"map_local":[{"match":{"all":[]},"data":"empty group"}]}}}`,
		`{"experimental":{"http_rewrite":{"url_rewrite":[{"match":{"subject":"request.method","equals":"POST","unknown":true},"action":"reject","disabled":true}]}}}`,
		`{"experimental":{"http_rewrite":{"header_rewrite":[{"match":{"subject":"request.header","header":"X Bad","regex":"."},"phase":"request","operation":"delete","header":"X-Test"}]}}}`,
		`{"experimental":{"http_rewrite":{"body_rewrite":[{"match":{"subject":"url","regex":"["},"phase":"response","jq":".ok=true","disabled":true}]}}}`,
		`{"experimental":{"http_rewrite":{"map_remote":[{"pattern":".","match":{"any":[{"subject":"request.method","equals":"GET"},{"subject":"response.status","equals":200}]},"replacement":"http://mapped.test/","disabled":true}]}}}`,
		`{"experimental":{"scripts":{"http":[{"match":{"any":[{"subject":"request.method","equals":"GET"},{"subject":"response.header","header":"X-Test","regex":"."}]},"phase":"request","source":"$done();","disabled":true}]}}}`,
		`{"experimental":{"scripts":{"http":[{"match":{"subject":"response.status","equals":"200"},"phase":"response","source":"$done();"}]}}}`,
		`{"experimental":{"scripts":{"http":[{"match":{"subject":"response.status","equals":200.5},"phase":"response","source":"$done();"}]}}}`,
		`{"experimental":{"scripts":{"http":[{"match":{"subject":"request.method","equals":"POST","regex":"POST"},"phase":"request","source":"$done();"}]}}}`,
		`{"experimental":{"scripts":{"http":[{"pattern":".","match":null,"phase":"request","source":"$done();"}]}}}`,
	} {
		if err := CheckConfig(invalid); err == nil {
			t.Errorf("invalid compound matcher passed complete profile validation: %s", invalid)
		}
		if _, err := FormatConfig(invalid); err == nil {
			t.Errorf("formatting accepted an invalid compound matcher: %s", invalid)
		}
	}
}
