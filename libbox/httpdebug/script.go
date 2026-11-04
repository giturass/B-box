package httpdebug

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/dlclark/regexp2/v2"
	"github.com/dop251/goja"
	"golang.org/x/net/http/httpguts"
)

// Limit concurrent interpreters across all intercepted connections. Runtimes
// never share globals or expose host files/processes. Cron gets routed HTTP only.
var httpScriptSlots = make(chan struct{}, 4)
var cronScriptSlots = make(chan struct{}, 2)
var scriptFinished = errors.New("script called $done")

func init() {
	// Interrupt stops VM bytecode, while regexp2 runs inside a native Go call.
	// Configure its own deadline once, before any runtime compiles expressions.
	regexp2.DefaultMatchTimeout = 200 * time.Millisecond
	regexp2.SetTimeoutCheckPeriod(10 * time.Millisecond)
}

type scriptResult struct {
	url, method, host string
	status            int
	headers           http.Header
	body              []byte
	bodySet           bool
	abort             bool
	response          *http.Response
}

func runHTTPScript(request *http.Request, response *http.Response, script *compiledScript, id string, data []byte, note func(string)) (scriptResult, error) {
	if script.options.RequiresBody {
		if int64(len(data)) > script.options.MaxBodyBytes {
			return scriptResult{}, errRewriteSize
		}
		if !script.options.BinaryBody && !utf8.Valid(data) {
			note(script.label + ": skipped non-UTF-8 body (use binary_body)")
			return scriptResult{}, nil
		}
	}
	var result scriptResult
	err := executeScript(request.Context(), script.program, script.label, "http-"+script.options.Phase,
		script.options.TimeoutMS, script.options.Argument, script.options.BinaryBody,
		func(vm *goja.Runtime, ctx context.Context) {
			requestValue := map[string]any{"url": request.URL.String(), "method": request.Method,
				"headers": scriptHeaders(vm, request.Header, request.Host, script.options.FullHeaders), "id": id}
			if response == nil && script.options.RequiresBody {
				requestValue["body"] = scriptBodyValue(vm, data, script.options.BinaryBody)
			}
			_ = vm.Set("$request", requestValue)
			if response != nil {
				value := map[string]any{"status": response.StatusCode, "headers": scriptHeaders(vm, response.Header, "", script.options.FullHeaders)}
				if script.options.RequiresBody {
					value["body"] = scriptBodyValue(vm, data, script.options.BinaryBody)
				}
				_ = vm.Set("$response", value)
			}
		}, func(vm *goja.Runtime, value goja.Value) error {
			var err error
			result, err = decodeScriptResult(vm, value, request, response, script.options)
			return err
		})
	if err == nil {
		note(script.label)
	}
	return result, err
}

// Each invocation has one VM and a cancellation deadline. All host callbacks
// execute on its calling goroutine; only Interrupt is called concurrently.
func executeScript(parent context.Context, program *goja.Program, label, kind string, timeout int, argument string, binary bool,
	setup func(*goja.Runtime, context.Context), done func(*goja.Runtime, goja.Value) error) error {
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeout)*time.Millisecond)
	defer cancel()
	slots := httpScriptSlots
	if kind == "cron" {
		slots = cronScriptSlots
	}
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return fmt.Errorf("%s: waiting for %s interpreter capacity: %w", label, kind, ctx.Err())
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	vm := goja.New()
	vm.SetMaxCallStackSize(256)
	stop := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer stop()
	_ = vm.Set("$argument", argument)
	_ = vm.Set("$script", map[string]any{"name": label, "type": kind, "binaryBodyMode": binary})
	logs := 0
	console := make(map[string]any)
	for _, level := range []string{"log", "info", "warn", "error", "debug"} {
		console[level] = func(call goja.FunctionCall) goja.Value {
			if logs >= 10 {
				return goja.Undefined()
			}
			logs++
			var message strings.Builder
			for _, value := range call.Arguments {
				if message.Len() >= 2048 {
					break
				}
				if message.Len() > 0 {
					message.WriteByte(' ')
				}
				part := value.String()
				message.WriteString(part[:min(len(part), 2048-message.Len())])
			}
			logLevel := level
			if logLevel == "log" {
				logLevel = "info"
			}
			writeLog(logLevel, label+": "+strings.ToValidUTF8(message.String(), ""))
			return goja.Undefined()
		}
	}
	_ = vm.Set("console", console)
	setup(vm, ctx)
	completed := false
	_ = vm.Set("$done", func(call goja.FunctionCall) goja.Value {
		if err := done(vm, call.Argument(0)); err != nil {
			panic(vm.NewGoError(err))
		}
		completed = true
		// Like Surge, $done terminates this invocation. A script cannot retain a
		// runtime or run work after returning its result.
		vm.Interrupt(scriptFinished)
		return goja.Undefined()
	})
	_, err := vm.RunProgram(program)
	var interrupted *goja.InterruptedError
	if completed && errors.As(err, &interrupted) && interrupted.Value() == scriptFinished {
		if ctx.Err() != nil {
			return fmt.Errorf("%s: %w", label, ctx.Err())
		}
		return nil
	}
	if err == nil {
		err = errors.New("script must call $done()")
	}
	return fmt.Errorf("%s: %w", label, err)
}

func scriptHeaders(vm *goja.Runtime, headers http.Header, host string, full bool) goja.Value {
	values := headers.Clone()
	if host != "" {
		values.Set("Host", host)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if full {
		fields := make([]any, 0, len(values))
		for _, key := range keys {
			for _, value := range values[key] {
				field := vm.NewObject()
				_ = field.Set("field", key)
				_ = field.Set("value", value)
				fields = append(fields, field)
			}
		}
		return vm.NewArray(fields...)
	}
	fields := vm.NewObject()
	for _, key := range keys {
		// Full-header mode is required to preserve repeated fields in a result.
		if len(values[key]) > 0 {
			_ = fields.Set(key, values[key][0])
		}
	}
	return fields
}

func scriptBodyValue(vm *goja.Runtime, data []byte, binary bool) any {
	if !binary {
		return string(data)
	}
	buffer := vm.NewArrayBuffer(bytesClone(data))
	array, err := vm.New(vm.Get("Uint8Array"), vm.ToValue(buffer))
	if err != nil {
		panic(err)
	}
	return array
}

func bytesClone(value []byte) []byte { return append([]byte{}, value...) }

func decodeScriptResult(vm *goja.Runtime, value goja.Value, request *http.Request, response *http.Response, options HTTPScript) (scriptResult, error) {
	var result scriptResult
	if missingScriptValue(value) {
		return result, nil
	}
	object, ok := value.(*goja.Object)
	if !ok || object.ClassName() != "Object" {
		return result, errors.New("$done expects an object")
	}
	allowed := []string{"headers", "body", "abort"}
	if response == nil {
		allowed = append(allowed, "url", "method", "response")
	} else {
		allowed = append(allowed, "status")
	}
	for _, key := range object.Keys() {
		if !slices.Contains(allowed, key) {
			return result, fmt.Errorf("unsupported $done field %q", key)
		}
	}
	if abort := object.Get("abort"); !missingScriptValue(abort) {
		if abort.ExportType() != reflect.TypeFor[bool]() {
			return result, errors.New("abort must be a boolean")
		}
		var valid bool
		result.abort, valid = abort.Export().(bool)
		if !valid {
			return result, errors.New("abort must be a boolean")
		}
	}
	for name, target := range map[string]*string{"url": &result.url, "method": &result.method} {
		if field := object.Get(name); !missingScriptValue(field) {
			if field.ExportType() != reflect.TypeFor[string]() {
				return result, fmt.Errorf("invalid script %s", name)
			}
			var valid bool
			*target, valid = field.Export().(string)
			if !valid || *target == "" || len(*target) > 8192 {
				return result, fmt.Errorf("invalid script %s", name)
			}
		}
	}
	if field := object.Get("status"); !missingScriptValue(field) {
		var err error
		result.status, err = scriptStatus(field)
		if err != nil {
			return result, err
		}
	}
	if field := object.Get("body"); !missingScriptValue(field) {
		if !options.RequiresBody {
			return result, errors.New("returning body requires requires_body=true")
		}
		var err error
		result.body, err = decodeScriptBody(field, options.BinaryBody, options.MaxBodyBytes)
		if err != nil {
			return result, err
		}
		result.bodySet = true
	}
	if field := object.Get("headers"); !missingScriptValue(field) {
		original := request.Header
		if response != nil {
			original = response.Header
		}
		var err error
		result.headers, err = decodeScriptHeaders(vm, field, original)
		if err != nil {
			return result, err
		}
		if response == nil {
			if values, found := result.headers["Host"]; found {
				if len(values) != 1 || values[0] == "" {
					return result, errors.New("script must supply exactly one Host")
				}
				result.host = values[0]
				result.headers.Del("Host")
			}
		}
	}
	if field := object.Get("response"); !missingScriptValue(field) {
		mock, ok := field.(*goja.Object)
		if !ok || mock.ClassName() != "Object" {
			return result, errors.New("script response must be an object")
		}
		for _, key := range mock.Keys() {
			if key != "status" && key != "headers" && key != "body" {
				return result, fmt.Errorf("unsupported mock response field %q", key)
			}
		}
		status := http.StatusOK
		var err error
		if field = mock.Get("status"); !missingScriptValue(field) {
			status, err = scriptStatus(field)
			if err != nil {
				return result, err
			}
		}
		headers := make(http.Header)
		if field = mock.Get("headers"); !missingScriptValue(field) {
			headers, err = decodeScriptHeaders(vm, field, nil)
			if err != nil {
				return result, err
			}
		}
		var body []byte
		if field = mock.Get("body"); !missingScriptValue(field) {
			body, err = decodeScriptBody(field, options.BinaryBody, options.MaxBodyBytes)
			if err != nil {
				return result, err
			}
		}
		result.response = localRewriteResponse(request, status, headers, body)
	}
	return result, nil
}

func missingScriptValue(value goja.Value) bool { return value == nil || goja.IsUndefined(value) }

func scriptStatus(value goja.Value) (int, error) {
	if value.ExportType() != reflect.TypeFor[int64]() && value.ExportType() != reflect.TypeFor[float64]() {
		return 0, errors.New("script status must be a number")
	}
	number := value.ToFloat()
	status := int(number)
	if status < 200 || status > 599 || float64(status) != number {
		return 0, errors.New("script status must be an integer between 200 and 599")
	}
	return status, nil
}

func decodeScriptBody(value goja.Value, binary bool, limit int64) ([]byte, error) {
	var data []byte
	if binary {
		if value.ExportType() != reflect.TypeFor[[]uint8]() {
			return nil, errors.New("binary script body must be a Uint8Array")
		}
		var ok bool
		data, ok = value.Export().([]uint8)
		if !ok {
			return nil, errors.New("binary script body must be a Uint8Array")
		}
	} else {
		if value.ExportType() != reflect.TypeFor[string]() {
			return nil, errors.New("script body must be a string")
		}
		text, ok := value.Export().(string)
		if !ok {
			return nil, errors.New("script body must be a string")
		}
		if int64(len(text)) > limit {
			return nil, errRewriteSize
		}
		data = []byte(text)
	}
	if int64(len(data)) > limit {
		return nil, errRewriteSize
	}
	return bytesClone(data), nil
}

func decodeScriptHeaders(vm *goja.Runtime, value goja.Value, original http.Header) (http.Header, error) {
	object, ok := value.(*goja.Object)
	if !ok {
		return nil, errors.New("script headers must be an object or array")
	}
	headers := make(http.Header)
	size := 0
	add := func(name string, value goja.Value) error {
		if missingScriptValue(value) {
			return errors.New("missing script header value")
		}
		if value.ExportType() != reflect.TypeFor[string]() {
			return errors.New("script header value must be a string")
		}
		text, ok := value.Export().(string)
		if !ok || !httpguts.ValidHeaderFieldName(name) || !httpguts.ValidHeaderFieldValue(text) {
			return errors.New("invalid script header")
		}
		size += len(name) + len(text)
		if size > MaxHeaderBytes {
			return errors.New("script headers exceed 64 KiB")
		}
		headers.Add(name, text)
		return nil
	}
	if object.ClassName() == "Array" {
		length := object.Get("length").ToInteger()
		if length < 0 || length > 1024 {
			return nil, errors.New("too many script headers")
		}
		for i := int64(0); i < length; i++ {
			entry, ok := object.Get(fmt.Sprint(i)).(*goja.Object)
			if !ok {
				return nil, errors.New("full_headers entries need field and value")
			}
			field := entry.Get("field")
			if missingScriptValue(field) {
				return nil, errors.New("full_headers entry is missing field")
			}
			name, ok := field.Export().(string)
			if !ok {
				return nil, errors.New("invalid full_headers field")
			}
			if err := add(name, entry.Get("value")); err != nil {
				return nil, err
			}
		}
	} else if object.ClassName() == "Object" {
		keys := object.Keys()
		if len(keys) > 1024 {
			return nil, errors.New("too many script headers")
		}
		for _, name := range keys {
			if err := add(name, object.Get(name)); err != nil {
				return nil, err
			}
		}
	} else {
		return nil, errors.New("script headers must be an object or array")
	}
	for name, values := range headers {
		if protectedRewriteHeader(name) && !slices.Equal(values, original.Values(name)) {
			return nil, fmt.Errorf("script cannot change framing header %s", name)
		}
	}
	// Omitting a framing header is also a framing change. Body replacement
	// subsequently normalizes these fields from the actual replacement bytes.
	for name, values := range original {
		if protectedRewriteHeader(name) && !slices.Equal(values, headers.Values(name)) {
			return nil, fmt.Errorf("script cannot remove framing header %s", name)
		}
	}
	return headers, nil
}
