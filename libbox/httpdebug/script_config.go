package httpdebug

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/dop251/goja"
	"github.com/dop251/goja/parser"
	"github.com/robfig/cron/v3"
)

// ScriptsConfig is independent of HTTP rewrites and MITM. Validating a profile
// compiles code and schedules without executing it or making network requests.
type ScriptsConfig struct {
	Enabled      bool                       `json:"enabled,omitempty"`
	Ports        []uint16                   `json:"ports,omitempty"`
	Packages     []string                   `json:"packages,omitempty"`
	MaxBodyBytes int64                      `json:"max_body_bytes,omitempty"`
	HTTP         []HTTPScript               `json:"http,omitempty"`
	Cron         []CronScript               `json:"cron,omitempty"`
	Resources    map[string]RewriteResource `json:"resources,omitempty"`
}

type HTTPScript struct {
	RewriteMatch
	Phase                string             `json:"phase"`
	Source               string             `json:"source,omitempty"`
	Resource             string             `json:"resource,omitempty"`
	ScriptPath           string             `json:"script_path,omitempty"`
	ScriptUpdateInterval int64              `json:"script_update_interval,omitempty"`
	Debug                bool               `json:"debug,omitempty"`
	RequiresBody         bool               `json:"requires_body,omitempty"`
	BinaryBody           bool               `json:"binary_body,omitempty"`
	GRPCMode             string             `json:"grpc_mode,omitempty"`
	FullHeaders          bool               `json:"full_headers,omitempty"`
	MaxBodyBytes         int64              `json:"max_body_bytes,omitempty"`
	TimeoutMS            int                `json:"timeout_ms,omitempty"`
	Argument             string             `json:"argument,omitempty"`
	HTTPClient           *ScriptHTTPOptions `json:"http_client,omitempty"`
	StorageNamespace     string             `json:"storage_namespace,omitempty"`
}

type CronScript struct {
	Name                 string             `json:"name,omitempty"`
	Disable              bool               `json:"disabled,omitempty"`
	Cron                 string             `json:"cron"`
	Timezone             string             `json:"timezone,omitempty"`
	Source               string             `json:"source,omitempty"`
	Resource             string             `json:"resource,omitempty"`
	ScriptPath           string             `json:"script_path,omitempty"`
	ScriptUpdateInterval int64              `json:"script_update_interval,omitempty"`
	Debug                bool               `json:"debug,omitempty"`
	TimeoutMS            int                `json:"timeout_ms,omitempty"`
	Argument             string             `json:"argument,omitempty"`
	HTTPClient           *ScriptHTTPOptions `json:"http_client,omitempty"`
	StorageNamespace     string             `json:"storage_namespace,omitempty"`
}

type compiledScript struct {
	compiledRule[HTTPScript]
	program    *goja.Program
	client     scriptHTTPPolicy
	source     *processingResource
	storageKey string
}

type compiledCron struct {
	options    CronScript
	label      string
	program    *goja.Program
	schedule   cron.Schedule
	client     scriptHTTPPolicy
	source     *processingResource
	storageKey string
}

type ScriptProgram struct {
	scope     Config
	limit     int64
	http      []compiledScript
	cron      []compiledCron
	storage   *scriptStore
	resources *processingResources
}

func (c *ScriptsConfig) UnmarshalJSON(data []byte) error {
	if len(data) > MaxRewriteConfigBytes || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("scripts must be an object of at most 2 MiB")
	}
	type plain ScriptsConfig
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*c = ScriptsConfig(value)
	_, err := CompileScripts(c, time.UTC)
	return err
}

func compileScriptSource(label, source, resource string, resources map[string][]byte) (*goja.Program, error) {
	if resource != "" {
		if source != "" {
			return nil, fmt.Errorf("%s: use source or resource, not both", label)
		}
		data, found := resources[resource]
		if !found {
			return nil, fmt.Errorf("%s: unknown script resource %q", label, resource)
		}
		source = string(data)
	}
	if source == "" || len(source) > MaxScriptBytes {
		return nil, fmt.Errorf("%s: script source must contain 1 to 131072 bytes", label)
	}
	ast, err := parser.ParseFile(nil, label+".js", source, 0, parser.WithDisableSourceMaps)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	program, err := goja.CompileAST(ast, false)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	return program, nil
}

func CompileScripts(c *ScriptsConfig, location *time.Location) (*ScriptProgram, error) {
	if c == nil {
		return nil, nil
	}
	if c.MaxBodyBytes == 0 {
		c.MaxBodyBytes = MaxBodyLimit
	}
	if c.MaxBodyBytes == -1 {
		c.MaxBodyBytes = MaxRewriteRequestBody
	}
	if c.MaxBodyBytes < 1 || c.MaxBodyBytes > MaxRewriteRequestBody {
		return nil, errors.New("max_body_bytes must be between 1 and 33554432, or -1")
	}
	if len(c.HTTP)+len(c.Cron) > MaxRewriteRules || len(c.Resources) > MaxRewriteRules {
		return nil, errors.New("scripts supports at most 256 rules and resources")
	}
	scope := Config{Ports: append([]uint16(nil), c.Ports...), Packages: append([]string(nil), c.Packages...)}
	if err := scope.validate(); err != nil {
		return nil, fmt.Errorf("scripts scope: %w", err)
	}
	p := &ScriptProgram{scope: scope, limit: c.MaxBodyBytes}
	for name, resource := range c.Resources {
		if name == "" || len(name) > 256 {
			return nil, errors.New("invalid script resource name")
		}
		var err error
		if resource.DataType == "file" {
			_, err = compileProcessingResource(resource.Data, MaxScriptBytes, true, resource.UpdateInterval, false)
		} else {
			_, err = decodeRewriteData(resource.DataType, resource.Data, MaxScriptBytes)
		}
		if err != nil {
			return nil, fmt.Errorf("resource %q: %w", name, err)
		}
	}
	for i, rule := range c.HTTP {
		item, err := compileRewriteRule(rule, rule.RewriteMatch, "scripts.http", i)
		if err != nil {
			return nil, err
		}
		if !rewritePhase(rule.Phase) {
			return nil, fmt.Errorf("%s: phase must be request or response", item.label)
		}
		if rule.GRPCMode != "" && (rule.GRPCMode != "unary" || !rule.RequiresBody || !rule.BinaryBody) {
			return nil, fmt.Errorf("%s: grpc_mode must be unary and requires requires_body=true and binary_body=true", item.label)
		}
		if rule.TimeoutMS == 0 {
			item.options.TimeoutMS = 5000
		}
		if item.options.TimeoutMS < 1 || item.options.TimeoutMS > maxScriptTimeoutMS {
			return nil, fmt.Errorf("%s: timeout_ms must be between 1 and %d", item.label, maxScriptTimeoutMS)
		}
		if rule.MaxBodyBytes == 0 {
			item.options.MaxBodyBytes = c.MaxBodyBytes
		}
		if item.options.MaxBodyBytes == -1 {
			item.options.MaxBodyBytes = MaxRewriteRequestBody
		}
		if item.options.MaxBodyBytes < 1 || item.options.MaxBodyBytes > MaxRewriteRequestBody {
			return nil, fmt.Errorf("%s: script body limit must be 1..33554432, or -1", item.label)
		}
		client, err := compileScriptHTTP(rule.HTTPClient, false)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", item.label, err)
		}
		if err = validateStorageNamespace(rule.StorageNamespace); err != nil {
			return nil, fmt.Errorf("%s: %w", item.label, err)
		}
		program, source, err := compileScriptResource(item.label, rule.Source, rule.ScriptPath, rule.Resource, c.Resources, rule.ScriptUpdateInterval, rule.Debug)
		if err != nil {
			return nil, err
		}
		if !rule.Disable {
			p.http = append(p.http, compiledScript{compiledRule: item, program: program, client: client, source: source,
				storageKey: scriptDefaultStorageKey(rule.Source, rule.ScriptPath, rule.Resource, c.Resources)})
		}
	}
	for i, rule := range c.Cron {
		label := fmt.Sprintf("scripts.cron[%d] %s", i, rule.Name)
		if len(label) > 256 {
			return nil, errors.New("script name is too long")
		}
		if rule.TimeoutMS == 0 {
			rule.TimeoutMS = 5000
		}
		if rule.TimeoutMS < 1 || rule.TimeoutMS > maxScriptTimeoutMS {
			return nil, fmt.Errorf("%s: timeout_ms must be between 1 and %d", label, maxScriptTimeoutMS)
		}
		client, err := compileScriptHTTP(rule.HTTPClient, true)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		if err = validateStorageNamespace(rule.StorageNamespace); err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		schedule, err := compileCron(rule.Cron, rule.Timezone, location)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		program, source, err := compileScriptResource(label, rule.Source, rule.ScriptPath, rule.Resource, c.Resources, rule.ScriptUpdateInterval, rule.Debug)
		if err != nil {
			return nil, err
		}
		if !rule.Disable {
			p.cron = append(p.cron, compiledCron{options: rule, label: label, program: program, schedule: schedule, client: client, source: source,
				storageKey: scriptDefaultStorageKey(rule.Source, rule.ScriptPath, rule.Resource, c.Resources)})
		}
	}
	return p, nil
}

func compileCron(expression, zone string, location *time.Location) (cron.Schedule, error) {
	fields := strings.Fields(expression)
	if len(expression) > 256 || len(fields) < 5 || len(fields) > 6 || strings.ContainsAny(expression, "@=?") {
		return nil, errors.New("cron requires five fields, or six with seconds first")
	}
	// Reject empty list elements that the dependency otherwise ignores.
	for _, field := range fields {
		if strings.HasPrefix(field, ",") || strings.HasSuffix(field, ",") || strings.Contains(field, ",,") {
			return nil, errors.New("invalid cron field list")
		}
	}
	if zone != "" {
		var err error
		location, err = time.LoadLocation(zone)
		if err != nil {
			return nil, fmt.Errorf("invalid cron timezone: %w", err)
		}
	}
	if location == nil {
		location = time.UTC
	}
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	schedule, err := parser.Parse(expression)
	if err != nil {
		return nil, err
	}
	schedule.(*cron.SpecSchedule).Location = location
	if schedule.Next(time.Date(2024, 1, 1, 0, 0, 0, 0, location)).IsZero() {
		return nil, errors.New("cron has no possible execution date")
	}
	return schedule, nil
}
