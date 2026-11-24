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
	Stream       []StreamScript             `json:"stream,omitempty"`
	Cron         []CronScript               `json:"cron,omitempty"`
	Event        []EventScript              `json:"event,omitempty"`
	Generic      []GenericScript            `json:"generic,omitempty"`
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

type EventScript struct {
	Name                 string             `json:"name,omitempty"`
	Disable              bool               `json:"disabled,omitempty"`
	Event                string             `json:"event"`
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

type GenericScript struct {
	Name                 string             `json:"name,omitempty"`
	Disable              bool               `json:"disabled,omitempty"`
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

const (
	ScriptEventNetworkChanged  = "network-changed"
	ScriptEventEngineStarted   = "engine-started"
	ScriptEventProfileReloaded = "profile-reloaded"
)

func validScriptEvent(event string) bool {
	return event == ScriptEventNetworkChanged || event == ScriptEventEngineStarted || event == ScriptEventProfileReloaded
}

type scriptTaskKey struct {
	kind  string
	index int // Original configuration position, including preceding disabled entries.
}

type compiledTask struct {
	key        scriptTaskKey
	options    GenericScript
	label      string
	event      string
	cron       string
	program    *goja.Program
	client     scriptHTTPPolicy
	source     *processingResource
	storageKey string
}

type compiledScript struct {
	compiledRule[HTTPScript]
	program    *goja.Program
	client     scriptHTTPPolicy
	source     *processingResource
	storageKey string
}

type compiledCron struct {
	index      int
	options    CronScript
	label      string
	program    *goja.Program
	schedule   cron.Schedule
	client     scriptHTTPPolicy
	source     *processingResource
	storageKey string
}

type ScriptProgram struct {
	scope      Config
	limit      int64
	http       []compiledScript
	stream     []compiledStreamScript
	cron       []compiledCron
	event      []compiledTask
	generic    []compiledTask
	tasks      map[scriptTaskKey]compiledTask
	storage    *scriptStore
	resources  *processingResources
	needsMatch bool
}

func (c *ScriptsConfig) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("scripts must be an object")
	}
	type plain ScriptsConfig
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	if err := validateHTTPMatchObjects(data, "http"); err != nil {
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
	if source == "" {
		return nil, fmt.Errorf("%s: script source must not be empty", label)
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
	limit, err := configuredBodyLimit(c.MaxBodyBytes)
	if err != nil {
		return nil, err
	}
	scope := Config{Ports: append([]uint16(nil), c.Ports...), Packages: append([]string(nil), c.Packages...)}
	if err := scope.validate(); err != nil {
		return nil, fmt.Errorf("scripts scope: %w", err)
	}
	p := &ScriptProgram{scope: scope, limit: limit, tasks: make(map[scriptTaskKey]compiledTask)}
	for name, resource := range c.Resources {
		if name == "" {
			return nil, errors.New("invalid script resource name")
		}
		var err error
		if resource.DataType == "file" {
			_, err = compileProcessingResource(resource.Data, 0, true, resource.UpdateInterval, false)
		} else {
			_, err = decodeRewriteData(resource.DataType, resource.Data, 0)
		}
		if err != nil {
			return nil, fmt.Errorf("resource %q: %w", name, err)
		}
	}
	for i, rule := range c.HTTP {
		item, err := compileRewriteRule(rule, rule.RewriteMatch, "scripts.http", i, rule.Phase)
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
		if !validTimeoutMS(item.options.TimeoutMS) {
			return nil, fmt.Errorf("%s: timeout_ms must be positive and representable as a duration, or -1 for unlimited", item.label)
		}
		if rule.MaxBodyBytes == 0 {
			item.options.MaxBodyBytes = c.MaxBodyBytes
		}
		item.options.MaxBodyBytes, err = configuredBodyLimit(item.options.MaxBodyBytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", item.label, err)
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
			p.needsMatch = p.needsMatch || item.match != nil
			p.http = append(p.http, compiledScript{compiledRule: item, program: program, client: client, source: source,
				storageKey: scriptDefaultStorageKey(rule.Source, rule.ScriptPath, rule.Resource, c.Resources)})
		}
	}
	if err := compileStreamScripts(c, p); err != nil {
		return nil, err
	}
	for i, rule := range c.Cron {
		label := fmt.Sprintf("scripts.cron[%d] %s", i, rule.Name)
		schedule, err := compileCron(rule.Cron, rule.Timezone, location)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		task, err := compileTask(scriptTaskKey{"cron", i}, cronTaskOptions(rule), c.Resources)
		if err != nil {
			return nil, err
		}
		rule.TimeoutMS = task.options.TimeoutMS
		if !rule.Disable {
			task.cron = rule.Cron
			p.tasks[task.key] = task
			p.cron = append(p.cron, compiledCron{index: i, options: rule, label: label, program: task.program, schedule: schedule, client: task.client,
				source: task.source, storageKey: task.storageKey})
		}
	}
	for i, rule := range c.Event {
		if !validScriptEvent(rule.Event) {
			return nil, fmt.Errorf("scripts.event[%d] %s: event must be network-changed, engine-started or profile-reloaded", i, rule.Name)
		}
		task, err := compileTask(scriptTaskKey{"event", i}, eventTaskOptions(rule), c.Resources)
		if err != nil {
			return nil, err
		}
		if !rule.Disable {
			task.event = rule.Event
			p.tasks[task.key] = task
			p.event = append(p.event, task)
		}
	}
	for i, rule := range c.Generic {
		task, err := compileTask(scriptTaskKey{"generic", i}, rule, c.Resources)
		if err != nil {
			return nil, err
		}
		if !rule.Disable {
			p.tasks[task.key] = task
			p.generic = append(p.generic, task)
		}
	}
	return p, nil
}

func cronTaskOptions(rule CronScript) GenericScript {
	return GenericScript{Name: rule.Name, Disable: rule.Disable, Source: rule.Source, Resource: rule.Resource,
		ScriptPath: rule.ScriptPath, ScriptUpdateInterval: rule.ScriptUpdateInterval, Debug: rule.Debug,
		TimeoutMS: rule.TimeoutMS, Argument: rule.Argument, HTTPClient: rule.HTTPClient, StorageNamespace: rule.StorageNamespace}
}

func eventTaskOptions(rule EventScript) GenericScript {
	return GenericScript{Name: rule.Name, Disable: rule.Disable, Source: rule.Source, Resource: rule.Resource,
		ScriptPath: rule.ScriptPath, ScriptUpdateInterval: rule.ScriptUpdateInterval, Debug: rule.Debug,
		TimeoutMS: rule.TimeoutMS, Argument: rule.Argument, HTTPClient: rule.HTTPClient, StorageNamespace: rule.StorageNamespace}
}

func compileTask(key scriptTaskKey, rule GenericScript, resources map[string]RewriteResource) (compiledTask, error) {
	task := compiledTask{key: key, options: rule, label: fmt.Sprintf("scripts.%s[%d] %s", key.kind, key.index, rule.Name)}
	if task.options.TimeoutMS == 0 {
		task.options.TimeoutMS = 5000
	}
	if !validTimeoutMS(task.options.TimeoutMS) {
		return task, fmt.Errorf("%s: timeout_ms must be positive and representable as a duration, or -1 for unlimited", task.label)
	}
	var err error
	task.client, err = compileScriptHTTP(rule.HTTPClient, true)
	if err != nil {
		return task, fmt.Errorf("%s: %w", task.label, err)
	}
	if err = validateStorageNamespace(rule.StorageNamespace); err != nil {
		return task, fmt.Errorf("%s: %w", task.label, err)
	}
	task.program, task.source, err = compileScriptResource(task.label, rule.Source, rule.ScriptPath, rule.Resource, resources, rule.ScriptUpdateInterval, rule.Debug)
	if err != nil {
		return task, err
	}
	task.storageKey = scriptDefaultStorageKey(rule.Source, rule.ScriptPath, rule.Resource, resources)
	return task, nil
}

func compileCron(expression, zone string, location *time.Location) (cron.Schedule, error) {
	fields := strings.Fields(expression)
	if len(fields) < 5 || len(fields) > 6 || strings.ContainsAny(expression, "@=?") {
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
