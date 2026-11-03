package httpdebug

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"unicode/utf8"

	"github.com/dop251/goja"
)

// Cron's callback API executes sequentially on the VM goroutine. Connections
// use the active sing-box route and never fall back to the device network.
func installCronHTTP(vm *goja.Runtime, ctx context.Context, dial RewriteDialFunc, roots *x509.CertPool, limit int64) func() {
	transport := newRewriteTransport(ctx, dial, roots)
	transport.DisableKeepAlives = true
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	api := make(map[string]any)
	calls := 0
	for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options"} {
		api[method] = func(call goja.FunctionCall) goja.Value {
			callback, ok := goja.AssertFunction(call.Argument(1))
			if !ok {
				panic(vm.NewGoError(errors.New("$httpClient requires a callback(error, response, data)")))
			}
			calls++
			var response *http.Response
			var body []byte
			var err error
			if calls > 16 {
				err = errors.New("cron HTTP request limit exceeded (16)")
			} else if dial == nil {
				err = errors.New("cron HTTP requires the running local service")
			} else {
				response, body, err = cronHTTPRequest(vm, ctx, client, method, call.Argument(0), limit)
			}
			failure, value, data := goja.Value(goja.Null()), goja.Value(goja.Null()), goja.Value(goja.Undefined())
			if err != nil {
				failure = vm.ToValue(err.Error())
			} else {
				value = vm.ToValue(map[string]any{"status": response.StatusCode, "headers": scriptHeaders(vm, response.Header, "", false)})
				data = vm.ToValue(string(body))
			}
			if _, err = callback(goja.Undefined(), failure, value, data); err != nil {
				// Preserve VM interruptions ($done/deadline) through native callbacks.
				panic(err)
			}
			return goja.Undefined()
		}
	}
	_ = vm.Set("$httpClient", api)
	return transport.CloseIdleConnections
}

func cronHTTPRequest(vm *goja.Runtime, ctx context.Context, client *http.Client, method string, options goja.Value, limit int64) (*http.Response, []byte, error) {
	var address string
	headers := make(http.Header)
	var body []byte
	if text, ok := options.Export().(string); ok {
		address = text
	} else if object, ok := options.(*goja.Object); ok && object.ClassName() == "Object" {
		for _, key := range object.Keys() {
			if key != "url" && key != "headers" && key != "body" {
				return nil, nil, fmt.Errorf("unsupported cron HTTP option %q", key)
			}
		}
		field := object.Get("url")
		if !missingScriptValue(field) {
			address, _ = field.Export().(string)
		}
		if field = object.Get("headers"); !missingScriptValue(field) {
			var err error
			headers, err = decodeScriptHeaders(vm, field, nil)
			if err != nil {
				return nil, nil, err
			}
		}
		if field = object.Get("body"); !missingScriptValue(field) {
			var err error
			body, err = decodeScriptBody(field, false, limit)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	if _, err := parseRewriteURL(address); err != nil {
		return nil, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, httpMethod(method), address, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	request.Header = headers
	if host := headers.Get("Host"); host != "" {
		request.Host = host
		headers.Del("Host")
	}
	if err = validRewriteRequest(request); err != nil {
		return nil, nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	body, err = io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err == nil && int64(len(body)) > limit {
		err = errors.New("cron HTTP response exceeds max_body_bytes")
	}
	if err == nil && !utf8.Valid(body) {
		err = errors.New("cron HTTP response must be UTF-8 text")
	}
	return response, body, err
}

func httpMethod(method string) string {
	return map[string]string{"get": "GET", "post": "POST", "put": "PUT", "patch": "PATCH", "delete": "DELETE", "head": "HEAD", "options": "OPTIONS"}[method]
}
