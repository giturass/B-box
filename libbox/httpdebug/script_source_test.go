package httpdebug

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestLargeScriptSourcesExecuteAcrossLoaders(t *testing.T) {
	source := `const data = ` + strconv.Quote(strings.Repeat("x", 256*1024)) + `;
		if(data.length !== 262144) throw Error('incomplete source');
		$done({body:String(data.length)});`
	for _, loader := range []string{"inline", "text", "base64", "file", "file-reference", "url", "url-reference",
		"url-gzip", "url-deflate", "url-br", "url-zstd", "url-gzip,br,zstd"} {
		t.Run(loader, func(t *testing.T) {
			profile := t.TempDir()
			if err := os.WriteFile(filepath.Join(profile, "script.js"), []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			rule := HTTPScript{RewriteMatch: RewriteMatch{Pattern: "."}, Phase: "response", RequiresBody: true}
			config := ScriptsConfig{MaxBodyBytes: 8, Resources: make(map[string]RewriteResource)}
			var origin *httptest.Server
			environment := scriptEnvironment{}
			switch loader {
			case "inline":
				rule.Source = source
			case "text", "base64":
				data := source
				if loader == "base64" {
					data = base64.StdEncoding.EncodeToString([]byte(source))
				}
				rule.Resource = "script"
				config.Resources["script"] = RewriteResource{DataType: loader, Data: data}
			case "file":
				rule.ScriptPath = "script.js"
			case "file-reference":
				rule.Resource = "script"
				config.Resources["script"] = RewriteResource{DataType: "file", Data: "script.js"}
			default:
				var encodings []string
				if strings.HasPrefix(loader, "url-") && loader != "url-reference" {
					encodings = strings.Split(strings.TrimPrefix(loader, "url-"), ",")
				}
				wire := encodedBody(t, []byte(source), encodings...)
				origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					for _, encoding := range encodings {
						w.Header().Add("Content-Encoding", encoding)
					}
					_, _ = w.Write(wire)
				}))
				defer origin.Close()
				environment = routedTestEnvironment(origin)
				if loader == "url-reference" {
					rule.Resource = "script"
					config.Resources["script"] = RewriteResource{DataType: "file", Data: "http://resource.test/script.js"}
				} else {
					rule.ScriptPath = "http://resource.test/script.js"
				}
			}
			config.HTTP = []HTTPScript{rule}
			config.Cron = []CronScript{{Cron: "0 2 * * *", Source: rule.Source, Resource: rule.Resource, ScriptPath: rule.ScriptPath}}
			content, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			program := scriptProgram(t, string(content))
			directory := t.TempDir()
			resources := testProcessingResources(t, directory, profile, environment, nil)
			run := func(resources *processingResources) {
				t.Helper()
				environment.resources = resources
				request := httptest.NewRequest("GET", "http://example.test/", nil)
				for _, phase := range []string{"request", "response"} {
					script := program.http[0]
					script.options.Phase = phase
					var response *http.Response
					if phase == "response" {
						response = &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}
					}
					result, err := runHTTPScript(request, response, &script, "large", []byte("original"), environment, func(string) {})
					if err != nil || !result.bodySet || string(result.body) != "262144" {
						t.Fatalf("large %s script failed: body %q, error %v", phase, result.body, err)
					}
				}
				program.resources = resources
				runner := &cronRunner{ctx: context.Background(), program: program}
				if err := runner.run(program.cron[0]); err != nil {
					t.Fatal("large Cron script failed:", err)
				}
				if processingBodies.used.Load() != 0 {
					t.Fatal("large script retained resource reservations")
				}
			}
			run(resources)
			if origin != nil {
				resources.Close()
				origin.Close()
				run(testProcessingResources(t, directory, profile, scriptEnvironment{}, nil))
			}
		})
	}
}

func TestScriptSourceLoadingRetainsSharedBodyBudget(t *testing.T) {
	source := []byte(`/*` + strings.Repeat("x", 256*1024) + `*/ $done();`)
	// Decoding the outer gzip expands past the budget, even though the final
	// plaintext would be empty. Intermediate representations must reserve work.
	intermediate := bytes.Repeat(encodedBody(t, nil, "gzip"), 1024)
	for _, test := range []struct {
		name      string
		wire      []byte
		encodings []string
	}{
		{"local", source, nil},
		{"plain", source, nil},
		{"decoded", encodedBody(t, source, "gzip"), []string{"gzip"}},
		{"intermediate", encodedBody(t, intermediate, "gzip"), []string{"gzip", "gzip"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := t.TempDir()
			environment := scriptEnvironment{}
			path := "script.js"
			if test.name == "local" {
				if err := os.WriteFile(filepath.Join(profile, path), test.wire, 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					for _, encoding := range test.encodings {
						w.Header().Add("Content-Encoding", encoding)
					}
					_, _ = w.Write(test.wire)
				}))
				defer origin.Close()
				environment = routedTestEnvironment(origin)
				path = "http://resource.test/script.js"
			}
			spec, err := compileProcessingResource(path, 0, true, 0, false)
			if err != nil {
				t.Fatal(err)
			}
			resources := testProcessingResources(t, t.TempDir(), profile, environment, nil)
			budget := bodyBudget{limit: 16*1024 + 1}
			lease := budget.acquire(0)
			_, err = resources.load(context.Background(), spec, lease, false)
			lease.release()
			if err == nil || !strings.Contains(err.Error(), "global body budget exhausted") {
				t.Fatalf("source loading bypassed its shared budget: %v", err)
			}
			if budget.used.Load() != 0 {
				t.Fatal("failed source loading retained its body reservation")
			}
		})
	}
}
