package httpdebug

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"unicode/utf8"

	"github.com/dop251/goja"
)

// Stream scripts receive complete WebSocket data messages and TCP read chunks.
// TCP chunks have no application-message boundary. HTTP scripts stay separate.
type StreamScript struct {
	GenericScript
	Type         string `json:"type"`
	Direction    string `json:"direction,omitempty"`
	Pattern      string `json:"pattern,omitempty"`
	BinaryBody   bool   `json:"binary_body,omitempty"`
	MaxBodyBytes int64  `json:"max_body_bytes,omitempty"`
}

type compiledStreamScript struct {
	compiledTask
	options StreamScript
	pattern *rewriteRegexp
}

func compileStreamScripts(c *ScriptsConfig, p *ScriptProgram) error {
	for index, rule := range c.Stream {
		if rule.Type != "tcp" && rule.Type != "websocket" {
			return fmt.Errorf("scripts.stream[%d]: type must be tcp or websocket", index)
		}
		if rule.Direction != "" && !streamDirection(rule.Direction) {
			return fmt.Errorf("scripts.stream[%d]: direction must be client or server", index)
		}
		task, err := compileTask(scriptTaskKey{"stream", index}, rule.GenericScript, c.Resources)
		if err != nil {
			return err
		}
		rule.TimeoutMS = task.options.TimeoutMS
		if rule.MaxBodyBytes == 0 {
			rule.MaxBodyBytes = c.MaxBodyBytes
		}
		rule.MaxBodyBytes, err = configuredBodyLimit(rule.MaxBodyBytes)
		if err != nil {
			return fmt.Errorf("%s: %w", task.label, err)
		}
		pattern := rule.Pattern
		if pattern == "" {
			pattern = ".*"
		}
		compiledPattern, err := compileRewritePattern(pattern)
		if err != nil {
			return fmt.Errorf("%s: pattern: %w", task.label, err)
		}
		if !rule.Disable {
			p.stream = append(p.stream, compiledStreamScript{compiledTask: task, options: rule, pattern: compiledPattern})
		}
	}
	return nil
}

func (e *Engine) matchingStreamScripts(ctx context.Context, metadata Metadata, kind, direction string) ([]*compiledStreamScript, error) {
	if e.scripts == nil || len(e.scripts.stream) == 0 {
		return nil, nil
	}
	scope := e.scripts.scope
	scope.RawTCP = true
	if !scope.matches(metadata) {
		return nil, nil
	}
	var matches []*compiledStreamScript
	for index := range e.scripts.stream {
		script := &e.scripts.stream[index]
		if script.options.Type != kind || script.options.Direction != "" && script.options.Direction != direction {
			continue
		}
		matched, err := script.pattern.match(streamAddress(metadata, kind), newRegexContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", script.label, err)
		}
		if matched {
			matches = append(matches, script)
		}
	}
	return matches, nil
}

func (e *Engine) streamMessageNeedsBody(ctx context.Context, metadata Metadata, kind, direction string) bool {
	matches, err := e.matchingStreamScripts(ctx, metadata, kind, direction)
	return err != nil || len(matches) > 0
}

func (e *Engine) streamMessageBodyLimit(ctx context.Context, metadata Metadata, kind, direction string) int64 {
	matches, _ := e.matchingStreamScripts(ctx, metadata, kind, direction)
	if len(matches) == 0 {
		return 0
	}
	// Only the first matching rule receives the original payload. Later rules
	// check their own limits against the preceding rule's result, which may be
	// smaller. Zero is the compiled representation of an unlimited input.
	return matches[0].options.MaxBodyBytes
}

func (e *Engine) processStreamMessage(ctx context.Context, metadata Metadata, kind, direction string, opcode int, data []byte) (streamResult, error) {
	result := streamResult{body: data}
	matches, err := e.matchingStreamScripts(ctx, metadata, kind, direction)
	if err != nil {
		return result, err
	}
	if len(matches) > 0 && metadata.messageID == "" {
		metadata.messageID = newStreamID()
	}
	for _, script := range matches {
		if exceedsBodyLimit(int64(len(result.body)), script.options.MaxBodyBytes) {
			return result, fmt.Errorf("%s: %w", script.label, errRewriteSize)
		}
		if !script.options.BinaryBody && !utf8.Valid(result.body) {
			writeLog("debug", script.label+": skipped non-UTF-8 stream message (use binary_body)")
			continue
		}
		next, err := runStreamScript(ctx, script, metadata, kind, direction, opcode, result.body, e.scriptEnvironment)
		if err != nil {
			return result, err
		}
		result.body, result.drop = next.body, next.drop
		result.inject = append(result.inject, next.inject...)
		if result.drop {
			break
		}
	}
	if !result.drop {
		if err = validateStreamPayload(kind, opcode, result.body); err != nil {
			return result, err
		}
	}
	return result, nil
}

func runStreamScript(parent context.Context, script *compiledStreamScript, metadata Metadata, kind, direction string, opcode int, data []byte, environment scriptEnvironment) (streamResult, error) {
	result := streamResult{body: data}
	ctx, cancel := scriptContext(parent, script.options.TimeoutMS)
	defer cancel()
	program := script.program
	if script.source != nil {
		value, lease, err := environment.resources.get(ctx, script.source)
		if err != nil {
			return result, fmt.Errorf("%s: script loading: %w", script.label, err)
		}
		defer lease.release()
		program = value.program
	}
	cleanup := func() {}
	defer func() { cleanup() }()
	info := scriptRunInfo{name: script.options.Name}
	if script.options.Debug {
		info.note = streamNote(metadata, kind)
	}
	err := executeScript(ctx, program, script.label, "stream", script.options.TimeoutMS, script.options.Argument, script.options.BinaryBody,
		func(vm *goja.Runtime, ctx context.Context) {
			cleanup = installScriptHTTP(vm, ctx, environment.dial, environment.roots, script.options.MaxBodyBytes, script.client)
			key := script.storageKey
			if script.source != nil {
				key = "script." + environment.resources.key(script.source)
			}
			installScriptStorage(vm, ctx, environment.storage, script.options.StorageNamespace, script.label, key)
			_ = vm.Set("$message", map[string]any{"body": scriptBodyValue(vm, data, script.options.BinaryBody),
				"type": kind, "direction": direction, "opcode": opcode, "url": streamAddress(metadata, kind),
				"id": metadata.messageID, "streamId": metadata.StreamID, "parentId": metadata.ParentID})
		}, func(vm *goja.Runtime, value goja.Value) error {
			decoded, err := decodeStreamResult(ctx, value, data, kind, opcode, script.options.MaxBodyBytes)
			if err == nil {
				result = decoded
			}
			return err
		}, info)
	var resultError *scriptResultError
	if errors.As(err, &resultError) && errors.Is(resultError.error, errScriptAbort) {
		return result, errScriptAbort
	}
	return result, err
}

func decodeStreamResult(ctx context.Context, value goja.Value, original []byte, kind string, opcode int, limit int64) (streamResult, error) {
	result := streamResult{body: original}
	if missingScriptValue(value) {
		return result, nil
	}
	object, ok := value.(*goja.Object)
	if !ok || object.ClassName() != "Object" {
		return result, errors.New("$done expects a stream result object")
	}
	for _, key := range object.Keys() {
		if !slices.Contains([]string{"body", "drop", "abort", "inject"}, key) {
			return result, fmt.Errorf("unsupported stream result field %q", key)
		}
	}
	for _, name := range []string{"drop", "abort"} {
		field := object.Get(name)
		if missingScriptValue(field) {
			continue
		}
		if field.ExportType() != reflect.TypeFor[bool]() {
			return result, fmt.Errorf("%s must be a boolean", name)
		}
		if field.ToBoolean() {
			if name == "abort" {
				return result, errScriptAbort
			}
			result.drop = true
		}
	}
	if body := object.Get("body"); !missingScriptValue(body) {
		var err error
		result.body, err = decodeStreamBody(body, limit)
		if err != nil {
			return result, err
		}
	}
	if field := object.Get("inject"); !missingScriptValue(field) {
		array, ok := field.(*goja.Object)
		if !ok || array.ClassName() != "Array" {
			return result, errors.New("inject must be an array")
		}
		count := array.Get("length").ToInteger()
		for index := int64(0); index < count; index++ {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			item, ok := array.Get(fmt.Sprint(index)).(*goja.Object)
			if !ok || item.ClassName() != "Object" {
				return result, errors.New("injected message must be an object")
			}
			for _, key := range item.Keys() {
				if key != "body" && key != "direction" && key != "opcode" {
					return result, fmt.Errorf("unsupported injected message field %q", key)
				}
			}
			direction := item.Get("direction")
			if missingScriptValue(direction) || direction.ExportType() != reflect.TypeFor[string]() {
				return result, errors.New("injected message requires a string direction")
			}
			injection := streamInjection{Direction: direction.String(), Opcode: opcode}
			if !streamDirection(injection.Direction) {
				return result, errors.New("injected message direction must be client or server")
			}
			if code := item.Get("opcode"); !missingScriptValue(code) {
				if code.ExportType() != reflect.TypeFor[int64]() && code.ExportType() != reflect.TypeFor[float64]() {
					return result, errors.New("injected message opcode must be an integer")
				}
				injection.Opcode = int(code.ToInteger())
				if float64(injection.Opcode) != code.ToFloat() {
					return result, errors.New("injected message opcode must be an integer")
				}
			}
			if missingScriptValue(item.Get("body")) {
				return result, errors.New("injected message requires body")
			}
			var err error
			injection.Body, err = decodeStreamBody(item.Get("body"), limit)
			if err == nil {
				err = validateStreamPayload(kind, injection.Opcode, injection.Body)
			}
			if err != nil {
				return result, err
			}
			result.inject = append(result.inject, injection)
		}
	}
	return result, nil
}

func decodeStreamBody(value goja.Value, limit int64) ([]byte, error) {
	_, text := value.(goja.String)
	return decodeScriptBody(value, !text, limit)
}
