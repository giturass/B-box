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
	policy := scriptHTTPPolicy{unrestricted: true}
	if options == nil {
		return policy, nil
	}
	if options.MaxRequests < 0 {
		return policy, errors.New("http_client.max_requests must be positive")
	}
	policy.maxRequests = options.MaxRequests
	var err error
	policy.maxBodyBytes, err = configuredBodyLimit(options.MaxBodyBytes)
	if err != nil {
		return policy, fmt.Errorf("http_client.max_body_bytes: %w", err)
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
	var workers sync.WaitGroup
	api := make(map[string]any)
	calls := 0
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
			var options scriptHTTPRequestOptions
			err := ctx.Err()
			if policy.maxRequests > 0 && calls > policy.maxRequests {
				err = fmt.Errorf("script HTTP request limit exceeded (%d)", policy.maxRequests)
			} else if dial == nil {
				err = errors.New("script HTTP requires the running local service")
			}
			// Admission precedes getters, JSON serialization and host body copies.
			// The VM already owns its input; reserve the exact output length before
			// copying it, then transfer this lease to the worker and callback.
			lease := reserveBodyWork(0)
			if err == nil && lease == nil {
				err = errors.New("script HTTP body budget exhausted before parsing")
			}
			transferred := false
			defer func() {
				if !transferred {
					lease.release()
				}
			}()
			if err == nil {
				options, err = parseScriptHTTPRequest(vm, stringify, method, call.Argument(0), limit, policy, lease)
			}
			enqueue := func(response *http.Response, body []byte, failure error, release func()) {
				event := scriptHTTPEvent{release: release, call: func() error {
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
				if ctx.Err() != nil {
					release()
					return
				}
				loop.enqueue(event)
			}
			if err != nil {
				event := scriptHTTPEvent{release: func() {}, call: func() error {
					_, callbackErr := callback(goja.Undefined(), vm.ToValue(err.Error()), goja.Null(), goja.Undefined())
					return callbackErr
				}}
				loop.enqueue(event)
				return goja.Undefined()
			}
			workers.Add(1)
			transferred = true
			go func() {
				defer workers.Done()
				callCtx, stop := context.WithCancel(ctx)
				if options.timeout > 0 {
					stop()
					callCtx, stop = context.WithTimeout(ctx, options.timeout)
				}
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
					for _, previous := range via {
						if previous.URL.String() == request.URL.String() && previous.Method == request.Method {
							return errors.New("script HTTP redirect loop")
						}
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
			loop.releaseEvents()
		}
	}
}

func parseScriptHTTPRequest(vm *goja.Runtime, stringify goja.Callable, method string, value goja.Value, limit int64, policy scriptHTTPPolicy, leases ...*bodyLease) (scriptHTTPRequestOptions, error) {
	options := scriptHTTPRequestOptions{method: strings.ToUpper(method), headers: make(http.Header), timeout: 5 * time.Second, redirect: true, cookie: true}
	var bodyObject *goja.Object
	if !missingScriptValue(value) && value.ExportType() == reflect.TypeFor[string]() {
		options.url = value.String()
	} else if object, ok := value.(*goja.Object); ok && object.ClassName() == "Object" {
		bodyObject = object
		for _, key := range object.Keys() {
			if key == "body" {
				continue // Do not invoke a body getter before validating the destination.
			}
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
			case "timeout":
				if missingScriptValue(field) || field.ExportType() != reflect.TypeFor[int64]() && field.ExportType() != reflect.TypeFor[float64]() {
					return options, errors.New("script HTTP timeout must be a number of seconds")
				}
				seconds := field.ToFloat()
				if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 && seconds != -1 || seconds >= float64(math.MaxInt64)/float64(time.Second) {
					return options, errors.New("script HTTP timeout must be positive and representable as a duration, or -1 for unlimited")
				}
				options.timeout = time.Duration(max(seconds, 0) * float64(time.Second))
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
	} else {
		return options, errors.New("script HTTP options must be a URL string or object")
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
	if bodyObject != nil {
		if body := bodyObject.Get("body"); !missingScriptValue(body) {
			var lease *bodyLease
			if len(leases) > 0 {
				lease = leases[0]
			}
			options.body, err = scriptHTTPBody(vm, stringify, body, options.headers, limit, lease)
			if err != nil {
				return options, err
			}
		}
	}
	return options, nil
}

func scriptHTTPBody(vm *goja.Runtime, stringify goja.Callable, value goja.Value, header http.Header, limit int64, lease *bodyLease) ([]byte, error) {
	if value.ExportType() == reflect.TypeFor[string]() {
		return decodeScriptBody(value, false, limit, lease)
	}
	if object, ok := value.(*goja.Object); ok {
		kind := object.ExportType()
		if kind != nil && kind.Kind() == reflect.Slice && kind.Elem().Kind() != reflect.Interface {
			bufferValue := object.Get("buffer")
			if !missingScriptValue(bufferValue) {
				if buffer, ok := bufferValue.Export().(goja.ArrayBuffer); ok {
					start, length := object.Get("byteOffset").ToInteger(), object.Get("byteLength").ToInteger()
					data := buffer.Bytes()
					if start < 0 || length < 0 || exceedsBodyLimit(length, limit) || start > int64(len(data))-length {
						return nil, errRewriteSize
					}
					if !lease.growWork(length) {
						return nil, errors.New("script HTTP body: body budget exhausted before copying")
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
			body, err := decodeScriptBody(encoded, false, limit, lease)
			if err == nil && header.Get("Content-Type") == "" {
				header.Set("Content-Type", "application/json")
			}
			return body, err
		}
	}
	return nil, errors.New("script HTTP body must be a string, JSON object or TypedArray")
}

func validateScriptHTTPDestination(request *http.Request, policy scriptHTTPPolicy) error {
	// net/http leaves Host empty on generated redirects to mean URL.Host.
	// Validate the effective authority without making it a custom Host that
	// later redirects might inherit.
	if request.Host == "" {
		effective := *request
		effective.Host = request.URL.Host
		request = &effective
	}
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
	// Apply the user's limit to every layer, growing only with received data.
	inputSize := int64(len(options.body)) + int64(len(body))
	body, err = decodeContentWithReader(body, encodings, func(reader io.Reader) ([]byte, error) {
		decoded, readErr := readScriptHTTPBody(scriptContextReader{ctx, reader}, limit, lease, inputSize)
		inputSize += int64(len(decoded))
		return decoded, readErr
	})
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

// A zero limit means no per-body cap. Check limits and reserve before each copy.
func readScriptHTTPBody(reader io.Reader, limit int64, lease *bodyLease, inputSize int64) ([]byte, error) {
	var body bytes.Buffer
	buffer := make([]byte, 4096)
	for {
		readSize := int64(len(buffer))
		if limit > 0 && limit < math.MaxInt64 {
			readSize = min(readSize, limit+1-int64(body.Len()))
		}
		n, err := reader.Read(buffer[:readSize])
		if n > 0 {
			if limit > 0 && int64(body.Len()+n) > limit {
				return nil, fmt.Errorf("script HTTP response exceeds max_body_bytes: %w", errRewriteSize)
			}
			if !lease.growWork(inputSize + int64(body.Len()+n)) {
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
