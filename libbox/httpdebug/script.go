package httpdebug

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"golang.org/x/net/http/httpguts"
)

var errScriptAbort = errors.New("request aborted by HTTP script")

type scriptResult struct {
	URL        *string         `json:"url"`
	Method     *string         `json:"method"`
	Status     *int            `json:"status"`
	Headers    json.RawMessage `json:"headers"`
	Body       *string         `json:"body"`
	BodyBase64 *string         `json:"bodyBase64"`
	Response   *scriptResult   `json:"response"`
	Abort      bool            `json:"abort"`
}

// Capture the builtins before user code runs. Only bounded, explicitly supported
// values cross back into Go; accessors and JSON serialization run inside the VM's
// timeout. $done ends execution immediately, including code following the call.
const scriptAdapter = `(function(finish, binary) {
  const keys = Object.keys, array = Array.isArray, stringify = JSON.stringify;
  const Bytes = Uint8Array;
  function clean(value, nested) {
    if (value === undefined) value = {};
    if (!value || typeof value !== 'object' || array(value)) throw Error('$done needs an object');
    const out = {};
    for (const key of keys(value)) {
      const v = value[key];
      if (key === 'headers') {
        let size = 0;
        function pair(field, text) {
          if (typeof field !== 'string' || typeof text !== 'string') throw Error('headers must contain strings');
          size += field.length + text.length + 4;
          if (size > 65536) throw Error('headers exceed 64 KiB');
          return {field: field, value: text};
        }
        const rows = [];
        if (array(v)) {
          if (v.length > 1024) throw Error('too many headers');
          for (const row of v) rows.push(pair(row.field, row.value));
        } else {
          if (!v || typeof v !== 'object') throw Error('invalid headers');
          for (const field of keys(v)) {
            if (rows.length >= 1024) throw Error('too many headers');
            rows.push(pair(field, v[field]));
          }
        }
        out.headers = rows;
      } else if (key === 'body') {
        if (typeof v === 'string') {
          if (v.length > 1048576) throw Error('body exceeds 1 MiB');
          out.body = v;
        } else if (v instanceof Bytes) {
          if (v.byteLength > 1048576) throw Error('body exceeds 1 MiB');
          out.bodyBase64 = binary(v);
        } else throw Error('body must be a string or Uint8Array');
      } else if (key === 'response' && !nested) {
        out.response = clean(v, true);
      } else if (key === 'status') {
        if (typeof v !== 'number' || v % 1 !== 0 || v < 200 || v > 999) throw Error('status must be 200–999');
        out.status = v;
      } else if ((key === 'url' || key === 'method') && !nested) {
        if (typeof v !== 'string' || v.length > 65536) throw Error('invalid URL or method');
        out[key] = v;
      } else if (key === 'abort' && !nested) {
        if (typeof v !== 'boolean') throw Error('abort must be a Boolean');
        out.abort = v;
      } else throw Error('unsupported result field: ' + key);
    }
    return out;
  }
  globalThis.$done = function(value) { finish(stringify(clean(value, false))); };
})(__bboxFinish, __bboxBinary);`

func scriptHeaders(headers http.Header, host string, full bool) any {
	copy := headers.Clone()
	if host != "" {
		copy.Set("Host", host)
	}
	if full {
		rows := make([]map[string]string, 0)
		names := make([]string, 0, len(copy))
		for name := range copy {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			for _, value := range copy[name] {
				rows = append(rows, map[string]string{"field": name, "value": value})
			}
		}
		return rows
	}
	values := make(map[string]string, len(copy))
	for name, value := range copy {
		values[name] = strings.Join(value, ", ")
	}
	return values
}

func runScript(ctx context.Context, rule ScriptRule, id string, request *http.Request, response *http.Response, body []byte, available bool, note func(string)) (*scriptResult, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(rule.TimeoutMS)*time.Millisecond)
	defer cancel()
	vm := goja.New()
	vm.SetMaxCallStackSize(256)
	stop := context.AfterFunc(ctx, func() { vm.Interrupt(ctx.Err()) })
	defer stop()
	requestValue := map[string]any{"id": id, "url": request.URL.String(), "method": request.Method,
		"headers": scriptHeaders(request.Header, request.Host, rule.FullHeaderMode)}
	responseValue := map[string]any{}
	if response != nil {
		responseValue["status"], responseValue["headers"] = response.StatusCode, scriptHeaders(response.Header, "", rule.FullHeaderMode)
	}
	if rule.RequiresBody && available {
		var value any = string(body)
		if rule.BinaryBodyMode {
			constructor, ok := goja.AssertConstructor(vm.Get("Uint8Array"))
			if !ok {
				return nil, errors.New("Uint8Array unavailable")
			}
			var err error
			value, err = constructor(nil, vm.ToValue(vm.NewArrayBuffer(append([]byte(nil), body...))))
			if err != nil {
				return nil, err
			}
		}
		if response != nil {
			responseValue["body"] = value
		} else {
			requestValue["body"] = value
		}
	}
	_ = vm.Set("$request", requestValue)
	if response != nil {
		_ = vm.Set("$response", responseValue)
	}
	_ = vm.Set("$argument", rule.Argument)
	_ = vm.Set("$script", map[string]any{"name": rule.Name, "type": rule.Type, "binaryBodyMode": rule.BinaryBodyMode})
	logs := 0
	_ = vm.Set("console", map[string]any{"log": func(call goja.FunctionCall) goja.Value {
		if logs < 8 {
			logs++
			var output strings.Builder
			for _, value := range call.Arguments {
				text := value.String()
				if len(text) > 1024 {
					text = text[:1024]
				}
				if output.Len()+len(text) > 2048 {
					break
				}
				output.WriteString(text)
				output.WriteByte(' ')
			}
			note("Script log: " + output.String())
		}
		return goja.Undefined()
	}})
	var encoded string
	completed := new(int)
	_ = vm.Set("__bboxBinary", func(call goja.FunctionCall) goja.Value {
		data, ok := call.Argument(0).Export().([]byte)
		if !ok || len(data) > MaxRewriteBody {
			panic(vm.NewTypeError("invalid binary body"))
		}
		return vm.ToValue(base64.StdEncoding.EncodeToString(data))
	})
	_ = vm.Set("__bboxFinish", func(call goja.FunctionCall) goja.Value {
		encoded = call.Argument(0).String()
		if len(encoded) > 3*MaxRewriteBody {
			panic(vm.NewTypeError("script result exceeds size limit"))
		}
		vm.Interrupt(completed)
		return goja.Undefined()
	})
	if _, err := vm.RunString(scriptAdapter); err != nil {
		return nil, err
	}
	_ = vm.GlobalObject().Delete("__bboxFinish")
	_ = vm.GlobalObject().Delete("__bboxBinary")
	_, err := vm.RunProgram(rule.program)
	var interrupted *goja.InterruptedError
	if !errors.As(err, &interrupted) || interrupted.Value() != completed {
		if err != nil {
			return nil, err
		}
		return nil, errors.New("script must synchronously call $done()")
	}
	var result scriptResult
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func resultBody(result *scriptResult) ([]byte, bool, error) {
	var data []byte
	if result.Body != nil {
		data = []byte(*result.Body)
	} else if result.BodyBase64 != nil {
		var err error
		data, err = base64.StdEncoding.DecodeString(*result.BodyBase64)
		if err != nil {
			return nil, false, err
		}
	} else {
		return nil, false, nil
	}
	if len(data) > MaxRewriteBody {
		return nil, false, errors.New("script body exceeds 1 MiB")
	}
	return data, true, nil
}

func resultHeaders(raw json.RawMessage, original http.Header, length int64, data []byte, changed bool) (http.Header, error) {
	var rows []struct {
		Field string `json:"field"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, err
	}
	headers := make(http.Header)
	for _, row := range rows {
		headers.Add(row.Field, row.Value)
	}
	if err := validateHeaders(headers); err != nil {
		return nil, err
	}
	for _, name := range []string{"Transfer-Encoding", "Trailer"} {
		if !slices.Equal(headers.Values(name), original.Values(name)) {
			return nil, errors.New("script cannot change HTTP framing headers")
		}
	}
	if values := headers.Values("Content-Length"); len(values) > 0 {
		if len(values) != 1 {
			return nil, errors.New("multiple Content-Length headers")
		}
		n, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || n < 0 || n != length && (!changed || n != int64(len(data))) {
			return nil, errors.New("script returned inconsistent Content-Length")
		}
	}
	return headers, nil
}

func applyScriptResult(rule ScriptRule, result *scriptResult, request *http.Request, response *http.Response, bodyAvailable bool) (*http.Response, error) {
	if result.Abort {
		return nil, errScriptAbort
	}
	data, bodyChanged, err := resultBody(result)
	if err != nil {
		return nil, err
	}
	if bodyChanged && !rule.RequiresBody {
		return nil, errors.New("script needs requiresBody to replace a body")
	}
	if bodyChanged && result.BodyBase64 != nil && !rule.BinaryBodyMode {
		return nil, errors.New("binary script output needs binaryBodyMode")
	}
	if bodyChanged && !bodyAvailable {
		bodyChanged = false
	}
	headers, length := request.Header, request.ContentLength
	if response != nil {
		headers, length = response.Header, response.ContentLength
	}
	if result.Headers != nil {
		headers, err = resultHeaders(result.Headers, headers, length, data, bodyChanged)
		if err != nil {
			return nil, err
		}
		if response == nil {
			if values := headers.Values("Host"); len(values) > 0 {
				if len(values) != 1 {
					return nil, errors.New("script must return one Host header")
				}
				if err := validateHostHeader(values[0]); err != nil {
					return nil, err
				}
				request.Host = values[0]
				headers.Del("Host")
			}
			request.Header = headers
		} else {
			response.Header = headers
		}
	}
	if response == nil {
		if result.Status != nil {
			return nil, errors.New("request script status belongs inside response")
		}
		if result.URL != nil {
			request.URL, err = rewriteURL(*result.URL)
			if err != nil {
				return nil, err
			}
		}
		if result.Method != nil {
			if !httpguts.ValidHeaderFieldName(*result.Method) || *result.Method == http.MethodConnect {
				return nil, errors.New("invalid script request method")
			}
			request.Method = *result.Method
		}
	} else {
		if result.URL != nil || result.Method != nil || result.Response != nil {
			return nil, errors.New("response scripts cannot change request fields or return response")
		}
		if result.Status != nil {
			if response.StatusCode == http.StatusSwitchingProtocols {
				return nil, errors.New("cannot rewrite an upgraded response status")
			}
			response.StatusCode = *result.Status
			response.Status = fmt.Sprintf("%d %s", *result.Status, http.StatusText(*result.Status))
		}
	}
	if bodyChanged {
		setRewriteBody(request, response, data)
	}
	if response != nil && (response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusNotModified || request.Method == http.MethodHead) {
		response.Body.Close()
		response.Body = http.NoBody
		if request.Method != http.MethodHead {
			response.ContentLength = 0
			response.Header.Del("Content-Length")
		}
	}
	if result.Response != nil {
		local := result.Response
		data, _, err := resultBody(local)
		if err != nil {
			return nil, err
		}
		headers := make(http.Header)
		if local.Headers != nil {
			headers, err = resultHeaders(local.Headers, headers, int64(len(data)), data, true)
			if err != nil {
				return nil, err
			}
		}
		if headers.Get("Content-Type") == "" {
			headers.Set("Content-Type", "text/plain; charset=utf-8")
		}
		status := http.StatusOK
		if local.Status != nil {
			status = *local.Status
		}
		return localResponse(request, status, headers, data), nil
	}
	return nil, nil
}
