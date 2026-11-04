package httpdebug

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/dop251/goja"
)

const (
	maxHTTPScriptRequests = 4
	maxCronScriptRequests = 16
	maxScriptHTTPOrigins  = 16
)

// HTTP scripts must opt in to exact scheme/host/port destinations. Existing
// Cron jobs retain their routed client; supplying options restricts them too.
type ScriptHTTPOptions struct {
	AllowedOrigins []string `json:"allowed_origins"`
	MaxRequests    int      `json:"max_requests,omitempty"`
}

type scriptHTTPPolicy struct {
	origins      map[string]bool
	maxRequests  int
	unrestricted bool
}

type scriptEnvironment struct {
	dial    RewriteDialFunc
	roots   *x509.CertPool
	storage *scriptStore
}

func compileScriptHTTP(options *ScriptHTTPOptions, cron bool) (scriptHTTPPolicy, error) {
	limit := maxHTTPScriptRequests
	if cron {
		limit = maxCronScriptRequests
	}
	policy := scriptHTTPPolicy{maxRequests: limit, unrestricted: cron && options == nil}
	if options == nil {
		return policy, nil
	}
	if options.MaxRequests != 0 {
		if options.MaxRequests < 1 || options.MaxRequests > limit {
			return policy, fmt.Errorf("http_client.max_requests must be between 1 and %d", limit)
		}
		policy.maxRequests = options.MaxRequests
	}
	if len(options.AllowedOrigins) > maxScriptHTTPOrigins {
		return policy, errors.New("http_client supports at most 16 allowed_origins")
	}
	policy.origins = make(map[string]bool)
	for _, origin := range options.AllowedOrigins {
		target, err := parseRewriteURL(origin)
		if err != nil || target.Path != "" && target.Path != "/" || strings.ContainsAny(origin, "?#") {
			return policy, fmt.Errorf("invalid http_client origin %q: use an exact HTTP(S) scheme, host and optional port", origin)
		}
		policy.origins[scriptHTTPOrigin(target)] = true
	}
	return policy, nil
}

func scriptHTTPOrigin(target *url.URL) string {
	host := normalizeHost(target.Hostname())
	if address := net.ParseIP(host); address != nil {
		host = address.String()
	}
	port := target.Port()
	if port == "" {
		port = "80"
		if target.Scheme == "https" {
			port = "443"
		}
	}
	number, _ := strconv.Atoi(port)
	return target.Scheme + "://" + net.JoinHostPort(host, strconv.Itoa(number))
}

// The callback API executes sequentially on the VM goroutine. Connections
// use the active sing-box route and never fall back to the device network.
func installScriptHTTP(vm *goja.Runtime, ctx context.Context, dial RewriteDialFunc, roots *x509.CertPool, limit int64, policy scriptHTTPPolicy) func() {
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
			if calls > policy.maxRequests {
				err = fmt.Errorf("script HTTP request limit exceeded (%d)", policy.maxRequests)
			} else if !policy.unrestricted && len(policy.origins) == 0 {
				err = errors.New("script HTTP requires http_client.allowed_origins")
			} else if dial == nil {
				err = errors.New("script HTTP requires the running local service")
			} else {
				// Include response-only/header-only scripts and nested callbacks
				// in the same global body budget as intercepted bodies and Cron.
				lease := reserveBodyWork(limit)
				if lease == nil {
					err = errors.New("script HTTP skipped: global body budget exhausted")
				} else {
					defer lease.release()
					response, body, err = scriptHTTPRequest(vm, ctx, client, method, call.Argument(0), limit, policy)
				}
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

func scriptHTTPRequest(vm *goja.Runtime, ctx context.Context, client *http.Client, method string, options goja.Value, limit int64, policy scriptHTTPPolicy) (*http.Response, []byte, error) {
	var address string
	headers := make(http.Header)
	var body []byte
	if options.ExportType() == reflect.TypeFor[string]() {
		address, _ = options.Export().(string)
	} else if object, ok := options.(*goja.Object); ok && object.ClassName() == "Object" {
		for _, key := range object.Keys() {
			if key != "url" && key != "headers" && key != "body" {
				return nil, nil, fmt.Errorf("unsupported script HTTP option %q", key)
			}
		}
		field := object.Get("url")
		if !missingScriptValue(field) && field.ExportType() == reflect.TypeFor[string]() {
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
	target, err := parseRewriteURL(address)
	if err != nil {
		return nil, nil, err
	}
	if !policy.unrestricted && !policy.origins[scriptHTTPOrigin(target)] {
		return nil, nil, errors.New("script HTTP origin is not in http_client.allowed_origins")
	}
	request, err := http.NewRequestWithContext(ctx, httpMethod(method), address, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	request.Header = headers
	if host := headers.Get("Host"); host != "" {
		if len(headers.Values("Host")) != 1 {
			return nil, nil, errors.New("script HTTP requires exactly one Host")
		}
		request.Host = host
		headers.Del("Host")
	}
	if err = validRewriteRequest(request); err != nil {
		return nil, nil, err
	}
	if !policy.unrestricted {
		host, err := parseRewriteURL(target.Scheme + "://" + request.Host)
		if err != nil || scriptHTTPOrigin(host) != scriptHTTPOrigin(target) {
			return nil, nil, errors.New("restricted script HTTP cannot override the URL's Host")
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	body, err = io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err == nil && int64(len(body)) > limit {
		err = errors.New("script HTTP response exceeds max_body_bytes")
	}
	if err == nil && !utf8.Valid(body) {
		err = errors.New("script HTTP response must be UTF-8 text")
	}
	return response, body, err
}

func httpMethod(method string) string {
	return map[string]string{"get": "GET", "post": "POST", "put": "PUT", "patch": "PATCH", "delete": "DELETE", "head": "HEAD", "options": "OPTIONS"}[method]
}
