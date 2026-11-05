package httpdebug

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"
	"golang.org/x/net/publicsuffix"
)

const (
	maxScriptHTTPConcurrent = 20
	maxScriptHTTPOrigins    = 16
)

type ScriptHTTPOptions struct {
	// Omitted leaves network access unrestricted. An explicit empty list
	// disables it; existing exact-origin restrictions remain enforced.
	AllowedOrigins []string `json:"allowed_origins"`
	MaxRequests    int      `json:"max_requests,omitempty"`
	MaxBodyBytes   int64    `json:"max_body_bytes,omitempty"`
}

type scriptHTTPPolicy struct {
	origins      map[string]bool
	maxRequests  int
	unrestricted bool
	maxBodyBytes int64
}

type scriptEnvironment struct {
	dial      RewriteDialFunc
	roots     *x509.CertPool
	storage   *scriptStore
	gateway   func() string
	resources *processingResources
}

type scriptPolicyKey struct{}

// ScriptPolicy is consumed by libbox's active-instance dialer. A policy names
// an existing sing-box outbound, including a selector; it is never a device
// network fallback or a fabricated Surge policy.
func ScriptPolicy(ctx context.Context) string {
	value, _ := ctx.Value(scriptPolicyKey{}).(string)
	return value
}

func compileScriptHTTP(options *ScriptHTTPOptions, _ bool) (scriptHTTPPolicy, error) {
	policy := scriptHTTPPolicy{unrestricted: true, maxBodyBytes: MaxRewriteRequestBody}
	if options == nil {
		return policy, nil
	}
	if options.MaxRequests < 0 {
		return policy, errors.New("http_client.max_requests must be positive")
	}
	policy.maxRequests = options.MaxRequests
	if options.MaxBodyBytes != 0 {
		if options.MaxBodyBytes < 1 || options.MaxBodyBytes > MaxRewriteRequestBody {
			return policy, errors.New("http_client.max_body_bytes must be 1..33554432")
		}
		policy.maxBodyBytes = options.MaxBodyBytes
	}
	if len(options.AllowedOrigins) > maxScriptHTTPOrigins {
		return policy, errors.New("http_client supports at most 16 allowed_origins")
	}
	policy.unrestricted = options.AllowedOrigins == nil
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

type scriptHTTPRequestOptions struct {
	url         string
	method      string
	headers     http.Header
	body        []byte
	timeout     time.Duration
	policy      string
	insecure    bool
	redirect    bool
	cookie      bool
	binary      bool
	fullHeaders bool
}

// Workers only handle Go values and network I/O. All JavaScript, including
// callbacks and promise continuations, executes on the invocation's VM thread.
func installScriptHTTP(vm *goja.Runtime, parent context.Context, dial RewriteDialFunc, roots *x509.CertPool, _ int64, policy scriptHTTPPolicy) func() {
	ctx, cancel := context.WithCancel(parent)
	loop, _ := ctx.Value(scriptLoopKey{}).(*scriptTimers)
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	stringify, _ := goja.AssertFunction(vm.Get("JSON").ToObject(vm).Get("stringify"))
	limit := policy.maxBodyBytes
	if limit == 0 {
		limit = MaxRewriteRequestBody
	}
	var workers sync.WaitGroup
	api := make(map[string]any)
	calls, pending := 0, 0
	for _, method := range []string{"get", "post", "put", "patch", "delete", "head", "options"} {
		api[method] = func(call goja.FunctionCall) goja.Value {
			callback, ok := goja.AssertFunction(call.Argument(1))
			if !ok {
				panic(vm.NewTypeError("$httpClient requires a callback(error, response, data)"))
			}
			if loop == nil {
				panic(vm.NewGoError(errors.New("script HTTP event loop is unavailable")))
			}
			calls++
			options, err := parseScriptHTTPRequest(vm, stringify, method, call.Argument(0), limit, policy)
			if policy.maxRequests > 0 && calls > policy.maxRequests {
				err = fmt.Errorf("script HTTP request limit exceeded (%d)", policy.maxRequests)
			} else if pending >= maxScriptHTTPConcurrent {
				err = errors.New("script HTTP concurrent request limit exceeded (20)")
			} else if dial == nil {
				err = errors.New("script HTTP requires the running local service")
			}
			enqueue := func(response *http.Response, body []byte, failure error, release func()) {
				event := scriptHTTPEvent{release: release, call: func() error {
					pending--
					errorValue, responseValue, dataValue := goja.Value(goja.Null()), goja.Value(goja.Null()), goja.Value(goja.Undefined())
					if failure != nil {
						errorValue = vm.ToValue(failure.Error())
					} else {
						responseValue = vm.ToValue(map[string]any{"status": response.StatusCode,
							"headers": scriptHeaders(vm, response.Header, "", options.fullHeaders)})
						dataValue = vm.ToValue(scriptBodyValue(vm, body, options.binary))
					}
					_, callbackErr := callback(goja.Undefined(), errorValue, responseValue, dataValue)
					return callbackErr
				}}
				select {
				case loop.events <- event:
				case <-ctx.Done():
					release()
				}
			}
			pending++
			if err != nil {
				event := scriptHTTPEvent{release: func() {}, call: func() error {
					pending--
					_, callbackErr := callback(goja.Undefined(), vm.ToValue(err.Error()), goja.Null(), goja.Undefined())
					return callbackErr
				}}
				select {
				case loop.events <- event:
				default:
					panic(vm.NewGoError(errors.New("script HTTP callback queue is full")))
				}
				return goja.Undefined()
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				lease := reserveBodyWork(int64(len(options.body)))
				if lease == nil {
					enqueue(nil, nil, errors.New("script HTTP skipped: global body budget exhausted"), func() {})
					return
				}
				callCtx, stop := context.WithTimeout(ctx, options.timeout)
				defer stop()
				if options.policy != "" {
					callCtx = context.WithValue(callCtx, scriptPolicyKey{}, options.policy)
				}
				transport := newRewriteTransport(callCtx, dial, roots)
				transport.DisableKeepAlives = true
				transport.TLSClientConfig.InsecureSkipVerify = options.insecure
				defer transport.CloseIdleConnections()
				client := &http.Client{Transport: transport}
				if options.cookie {
					client.Jar = jar
				}
				client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
					if !options.redirect {
						return http.ErrUseLastResponse
					}
					if len(via) >= 10 {
						return errors.New("script HTTP redirect limit exceeded (10)")
					}
					return validateScriptHTTPDestination(request, policy)
				}
				response, body, err := performScriptHTTPRequest(callCtx, client, options, limit, policy, lease)
				enqueue(response, body, err, lease.release)
			}()
			return goja.Undefined()
		}
	}
	_ = vm.Set("$httpClient", api)
	return func() {
		cancel()
		workers.Wait()
		if loop != nil {
			for {
				select {
				case event := <-loop.events:
					event.release()
				default:
					return
				}
			}
		}
	}
}

func parseScriptHTTPRequest(vm *goja.Runtime, stringify goja.Callable, method string, value goja.Value, limit int64, policy scriptHTTPPolicy) (scriptHTTPRequestOptions, error) {
	options := scriptHTTPRequestOptions{method: strings.ToUpper(method), headers: make(http.Header), timeout: 5 * time.Second, redirect: true, cookie: true}
	if !missingScriptValue(value) && value.ExportType() == reflect.TypeFor[string]() {
		options.url = value.String()
	} else if object, ok := value.(*goja.Object); ok && object.ClassName() == "Object" {
		for _, key := range object.Keys() {
			field := object.Get(key)
			switch key {
			case "url", "policy":
				if missingScriptValue(field) || field.ExportType() != reflect.TypeFor[string]() {
					return options, fmt.Errorf("script HTTP %s must be a string", key)
				}
				if key == "url" {
					options.url = field.String()
				} else {
					options.policy = field.String()
				}
			case "policy-descriptor":
				// Surge descriptors are not sing-box outbound definitions.
				// Reject them explicitly rather than silently using a wrong route.
				return options, errors.New("policy-descriptor is not supported; use policy with a configured sing-box outbound tag")
			case "headers":
				var err error
				options.headers, err = decodeScriptHeaders(vm, field, nil)
				if err != nil {
					return options, err
				}
			case "body":
				// Process after headers, independently of object property order.
			case "timeout":
				if missingScriptValue(field) || field.ExportType() != reflect.TypeFor[int64]() && field.ExportType() != reflect.TypeFor[float64]() {
					return options, errors.New("script HTTP timeout must be a number of seconds")
				}
				seconds := field.ToFloat()
				if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > 86400 {
					return options, errors.New("script HTTP timeout must be greater than 0 and at most 86400 seconds")
				}
				options.timeout = time.Duration(seconds * float64(time.Second))
			case "insecure", "auto-redirect", "auto-cookie", "binary-mode", "full-header-mode":
				if missingScriptValue(field) || field.ExportType() != reflect.TypeFor[bool]() {
					return options, fmt.Errorf("script HTTP %s must be a boolean", key)
				}
				switch key {
				case "insecure":
					options.insecure = field.ToBoolean()
				case "auto-redirect":
					options.redirect = field.ToBoolean()
				case "auto-cookie":
					options.cookie = field.ToBoolean()
				case "binary-mode":
					options.binary = field.ToBoolean()
				case "full-header-mode":
					options.fullHeaders = field.ToBoolean()
				}
			default:
				return options, fmt.Errorf("unsupported script HTTP option %q", key)
			}
		}
		if body := object.Get("body"); !missingScriptValue(body) {
			var err error
			options.body, err = scriptHTTPBody(vm, stringify, body, options.headers, limit)
			if err != nil {
				return options, err
			}
		}
	} else {
		return options, errors.New("script HTTP options must be a URL string or object")
	}
	if len(options.policy) > 256 {
		return options, errors.New("script HTTP policy name is too long")
	}
	request, err := http.NewRequest(options.method, options.url, nil)
	if err != nil {
		return options, err
	}
	request.Header = options.headers
	if host := options.headers.Get("Host"); host != "" {
		if len(options.headers.Values("Host")) != 1 {
			return options, errors.New("script HTTP requires exactly one Host")
		}
		request.Host = host
	}
	if err = validateScriptHTTPDestination(request, policy); err != nil {
		return options, err
	}
	return options, nil
}

func scriptHTTPBody(vm *goja.Runtime, stringify goja.Callable, value goja.Value, header http.Header, limit int64) ([]byte, error) {
	if value.ExportType() == reflect.TypeFor[string]() {
		return decodeScriptBody(value, false, limit)
	}
	if object, ok := value.(*goja.Object); ok {
		kind := object.ExportType()
		if kind != nil && kind.Kind() == reflect.Slice && kind.Elem().Kind() != reflect.Interface {
			bufferValue := object.Get("buffer")
			if !missingScriptValue(bufferValue) {
				if buffer, ok := bufferValue.Export().(goja.ArrayBuffer); ok {
					start, length := object.Get("byteOffset").ToInteger(), object.Get("byteLength").ToInteger()
					data := buffer.Bytes()
					if start < 0 || length < 0 || length > limit || start > int64(len(data))-length {
						return nil, errRewriteSize
					}
					return bytesClone(data[start : start+length]), nil
				}
			}
		}
		if object.ClassName() == "Object" || object.ClassName() == "Array" {
			encoded, err := stringify(goja.Undefined(), value)
			if err != nil {
				return nil, err
			}
			if missingScriptValue(encoded) || encoded.ExportType() != reflect.TypeFor[string]() {
				return nil, errors.New("script HTTP body is not JSON serializable")
			}
			body, err := decodeScriptBody(encoded, false, limit)
			if err == nil && header.Get("Content-Type") == "" {
				header.Set("Content-Type", "application/json")
			}
			return body, err
		}
	}
	return nil, errors.New("script HTTP body must be a string, JSON object or TypedArray")
}

func validateScriptHTTPDestination(request *http.Request, policy scriptHTTPPolicy) error {
	if err := validRewriteRequest(request); err != nil {
		return err
	}
	if !policy.unrestricted {
		if !policy.origins[scriptHTTPOrigin(request.URL)] {
			return errors.New("script HTTP origin is not in http_client.allowed_origins")
		}
		host, err := parseRewriteURL(request.URL.Scheme + "://" + request.Host)
		if err != nil || scriptHTTPOrigin(host) != scriptHTTPOrigin(request.URL) {
			return errors.New("restricted script HTTP cannot override the URL's Host")
		}
	}
	return nil
}

func performScriptHTTPRequest(ctx context.Context, client *http.Client, options scriptHTTPRequestOptions, limit int64, policy scriptHTTPPolicy, lease *bodyLease) (*http.Response, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, options.method, options.url, bytes.NewReader(options.body))
	if err != nil {
		return nil, nil, err
	}
	request.Header = options.headers.Clone()
	if host := request.Header.Get("Host"); host != "" {
		request.Host = host
		request.Header.Del("Host")
	}
	if err = validateScriptHTTPDestination(request, policy); err != nil {
		return nil, nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, nil, err
	}
	defer response.Body.Close()
	if request.Method == http.MethodHead || response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusNotModified {
		return response, []byte{}, nil
	}
	body, err := readScriptHTTPBody(response.Body, limit, lease, int64(len(options.body)))
	if err != nil {
		return response, nil, err
	}
	encodings, err := contentEncodings(contentEncoding(response.Header))
	if err != nil {
		return response, nil, fmt.Errorf("script HTTP response: %w", err)
	}
	for _, encoding := range encodings {
		if encoding != "identity" && !lease.growWork(limit) {
			return response, nil, errors.New("script HTTP decompression: global body budget exhausted")
		}
	}
	body, err = decodeContent(body, encodings, limit)
	if errors.Is(err, errRewriteSize) {
		return response, nil, errors.New("script HTTP decoded response exceeds max_body_bytes")
	}
	if err != nil {
		return response, nil, fmt.Errorf("script HTTP response decompression failed: %w", err)
	}
	if !options.binary && !utf8.Valid(body) {
		return response, nil, errors.New("script HTTP response must be UTF-8 text (use binary-mode)")
	}
	for _, encoding := range encodings {
		if encoding != "identity" {
			response.Header.Del("Content-Encoding")
			response.Header.Del("Content-Length")
			response.ContentLength, response.Uncompressed = -1, true
			break
		}
	}
	return response, body, nil
}

func readScriptHTTPBody(reader io.Reader, limit int64, lease *bodyLease, inputSize int64) ([]byte, error) {
	var body bytes.Buffer
	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer[:min(int64(len(buffer)), limit+1-int64(body.Len()))])
		if n > 0 {
			if int64(body.Len()+n) > limit {
				return nil, errors.New("script HTTP response exceeds max_body_bytes")
			}
			if !lease.growWork(max(inputSize, int64(body.Len()+n))) {
				return nil, errors.New("script HTTP response: global body budget exhausted")
			}
			_, _ = body.Write(buffer[:n])
		}
		if err != nil {
			if err == io.EOF {
				return body.Bytes(), nil
			}
			return nil, err
		}
	}
}
