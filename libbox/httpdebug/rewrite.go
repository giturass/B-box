package httpdebug

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/http/httpguts"
)

// Compose separately scoped HTTP rules and scripts for one request. Neither
// extension enables the other, and only MITM may admit a TLS connection.
type httpProgram struct {
	*RewriteProgram
	scripts        *ScriptProgram
	environment    scriptEnvironment
	regex          *regexBudget
	jqRemaining    time.Duration
	candidates     []string
	matched        map[*rewriteRegexp]string
	connectionHost string
	connectionPort uint16
}

func (e *Engine) httpProgram(metadata Metadata) *httpProgram {
	var rewrite *RewriteProgram
	var scripts *ScriptProgram
	if e.rewrite != nil && e.rewrite.scope.matches(metadata) {
		rewrite = e.rewrite
	}
	if e.scripts != nil && len(e.scripts.http) > 0 && e.scripts.scope.matches(metadata) {
		scripts = e.scripts
	}
	if rewrite == nil && scripts == nil {
		return nil
	}
	if rewrite == nil {
		rewrite = &RewriteProgram{limit: scripts.limit}
	}
	return &httpProgram{RewriteProgram: rewrite, scripts: scripts, environment: e.scriptEnvironment,
		connectionHost: metadata.Host, connectionPort: metadata.Port}
}

func (p *httpProgram) prepareMatcher(request *http.Request, address string) {
	if p.regex != nil {
		return
	}
	p.regex = newRegexBudget(request.Context())
	p.jqRemaining = 500 * time.Millisecond
	p.matched = make(map[*rewriteRegexp]string)
	p.candidates = []string{address}
	base := *request.URL
	for _, host := range []string{request.Host, p.connectionHost} {
		if host == "" {
			continue
		}
		if host == p.connectionHost && p.connectionPort != 0 {
			port := strconv.Itoa(int(p.connectionPort))
			if (base.Scheme == "https" && port != "443") || (base.Scheme != "https" && port != "80") {
				host = net.JoinHostPort(host, port)
			} else if strings.Contains(host, ":") {
				host = "[" + host + "]"
			}
		}
		base.Host = host
		candidate := base.String()
		if candidate != address {
			p.candidates = append(p.candidates, candidate)
		}
	}
}

func (p *httpProgram) matchURL(pattern *rewriteRegexp, address string) (string, error) {
	if result, found := p.matched[pattern]; found {
		return result, nil
	}
	candidates := p.candidates
	if len(candidates) == 0 {
		candidates = []string{address}
	}
	for _, candidate := range candidates {
		matched, err := pattern.match(candidate, p.regex)
		if err != nil {
			return "", err
		}
		if matched {
			if p.matched != nil {
				p.matched[pattern] = candidate
			}
			return candidate, nil
		}
	}
	if p.matched != nil {
		p.matched[pattern] = ""
	}
	return "", nil
}

func (p *httpProgram) match(pattern *rewriteRegexp, address string) (bool, error) {
	matched, err := p.matchURL(pattern, address)
	return matched != "", err
}

func (p *httpProgram) request(request *http.Request, id, address string, note func(string)) (local *http.Response, err error) {
	p.regex = nil
	p.prepareMatcher(request, address)
	var lease *bodyLease
	bufferedRequest, scriptedResponse := false, false
	defer func() {
		if lease != nil {
			if err == nil {
				if bufferedRequest {
					request.Body = lease.wrap(request.Body)
				}
				if scriptedResponse {
					local.Body = lease.wrap(local.Body)
				}
			}
			lease.release()
		}
	}()
	script, err := p.script("request", address)
	if err != nil {
		return nil, err
	}
	bodyRules, err := p.hasBodyRules("request", address)
	if err != nil {
		return nil, err
	}
	grpc := unaryGRPCScript(request.Header, script)
	if bodyRules && grpcFormat(request.Header) != grpcNone {
		note("Body rewrite skipped for gRPC; use a binary unary script for framed messages")
		bodyRules = false
	}
	needsBody := bodyRules || script != nil && script.options.RequiresBody
	eligible := !needsBody || bodyEligible(request.Header, note, grpc != nil)
	encoding := contentEncoding(request.Header)
	if err := p.rewriteHeader("request", address, request.Header, &request.Host, note); err != nil {
		return nil, err
	}
	for _, rule := range p.urls {
		matched, err := p.matchURL(rule.pattern, address)
		if err != nil {
			return nil, err
		}
		if matched == "" {
			continue
		}
		note(rule.label)
		switch rule.options.Action {
		case "reject":
			return localRewriteResponse(request, http.StatusForbidden, make(http.Header), []byte("Request rejected by B-box rewrite\n")), nil
		case "reject-200":
			return localRewriteResponse(request, http.StatusOK, http.Header{"Content-Type": {"text/plain; charset=utf-8"}}, nil), nil
		case "reject-dict":
			return localRewriteResponse(request, http.StatusOK, http.Header{"Content-Type": {"application/json"}}, []byte("{}")), nil
		}
		replacement := rule.options.Replacement
		if rule.options.Action != "header" && strings.Contains(replacement, gatewayPlaceholder) {
			gateway := ""
			if p.environment.gateway != nil {
				gateway = p.environment.gateway()
			}
			ip := net.ParseIP(gateway)
			if ip == nil {
				note(rule.label + ": skipped: outgoing interface has no default gateway")
				break
			}
			gateway = ip.String()
			if ip.To4() == nil {
				gateway = "[" + gateway + "]"
			}
			replacement = strings.ReplaceAll(replacement, gatewayPlaceholder, gateway)
		}
		next, err := boundedReplace(rule.pattern, matched, replacement, 8192, p.regex)
		if err != nil {
			return nil, err
		}
		// ws is the HTTP Upgrade transport. Keep the URL's host/path/query
		// and existing Upgrade headers while using net/http's HTTP transport.
		if rule.options.Action == "header" && strings.HasPrefix(next, "ws://") {
			next = "http://" + strings.TrimPrefix(next, "ws://")
		}
		target, err := parseRewriteURL(next)
		if err != nil {
			note(rule.label + ": invalid rewritten URL discarded: " + err.Error())
			break
		}
		if rule.options.Action != "header" {
			status, _ := strconv.Atoi(rule.options.Action)
			return localRewriteResponse(request, status, http.Header{"Location": []string{target.String()}}, nil), nil
		}
		request.URL, request.Host = target, target.Host
		break
	}
	for _, rule := range p.remote {
		matched, err := p.matchURL(rule.pattern, address)
		if err != nil {
			return nil, err
		}
		if matched == "" {
			continue
		}
		next, err := boundedReplace(rule.pattern, matched, rule.options.Replacement, 8192, p.regex)
		if err != nil {
			return nil, err
		}
		target, err := parseRewriteURL(next)
		if err != nil {
			return nil, err
		}
		request.URL = target
		if !rule.options.PreserveHost {
			request.Host = target.Host
		}
		note(rule.label)
		break
	}
	var data []byte
	available := false
	// Even a header-only request script may generate a bounded mock body.
	if needsBody && eligible || script != nil && !script.options.RequiresBody {
		limit := int64(0)
		if bodyRules {
			limit = p.bodyLimit("request")
		}
		if script != nil {
			limit = max(limit, script.options.MaxBodyBytes)
		}
		lease = reserveBodyWork(limit)
		if lease == nil {
			note("Body processing skipped: global body budget exhausted; original stream preserved")
		}
	}
	if needsBody && eligible && lease != nil {
		bufferedRequest = true
		limit := p.bodyLimit("request")
		if !bodyRules && script != nil {
			limit = script.options.MaxBodyBytes
		}
		var err error
		data, available, err = readRewriteBody(request.Context(), &request.Body, encoding, limit, false, note)
		if err != nil {
			return nil, err
		}
		if available && grpc != nil {
			data, available, err = grpc.prepare(data, false, note)
			if err != nil {
				return nil, err
			}
		}
		if available && bodyRules {
			var changed bool
			data, changed, err = p.rewriteBody("request", address, data, note)
			if err != nil {
				return nil, err
			}
			if changed {
				replaceRequestBody(request, data)
			}
		}
	}
	if script != nil {
		if lease != nil && (!script.options.RequiresBody || available) {
			result, err := runHTTPScript(request, nil, script, id, data, p.environment, note)
			if err != nil {
				var invalid *scriptResultError
				if errors.As(err, &invalid) || errors.Is(err, errRewriteSize) {
					return nil, err
				}
				message := script.label + ": script skipped; original message preserved: " + err.Error()
				note(message)
				writeLog("warn", message)
				result = scriptResult{}
			}
			if result.abort {
				return nil, errScriptAbort
			}
			if grpc != nil {
				result, err = grpc.result(result, request.Header)
				if err != nil {
					return nil, fmt.Errorf("invalid gRPC script result: %w", err)
				}
			}
			if result.headers != nil {
				request.Header = result.headers
				if result.host != "" {
					request.Host = result.host
				}
			}
			if result.url != "" {
				request.URL, err = parseRewriteURL(result.url)
				if err != nil {
					return nil, err
				}
			}
			if result.method != "" {
				request.Method = result.method
			}
			if result.bodySet {
				if grpc != nil {
					replaceGRPCRequestBody(request, result.body)
				} else {
					replaceRequestBody(request, result.body)
				}
			}
			if result.response != nil {
				scriptedResponse = true
				return result.response, nil
			}
		} else {
			note(script.label + ": skipped unavailable body or exhausted body budget")
		}
	}
	for i, rule := range p.local {
		matched, err := p.match(rule.pattern, address)
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		data := p.data[i]
		var resourceLease *bodyLease
		var source *processingResource
		if i < len(p.sources) {
			source = p.sources[i]
		}
		if source != nil {
			value, lease, err := p.environment.resources.get(request.Context(), source)
			if err != nil {
				message := rule.label + ": Map Local skipped: " + err.Error()
				note(message)
				writeLog("warn", message)
				break
			}
			data, resourceLease = value.data, lease
		}
		headers, _ := rewriteHeaders(rule.options.Headers)
		if headers.Get("Content-Type") == "" {
			if source != nil {
				headers.Set("Content-Type", source.contentType())
			} else {
				switch rule.options.DataType {
				case "tiny-gif":
					headers.Set("Content-Type", "image/gif")
				case "base64":
					headers.Set("Content-Type", "application/octet-stream")
				default:
					headers.Set("Content-Type", "text/plain; charset=utf-8")
				}
			}
		}
		note(rule.label + ": local response")
		response := localRewriteResponse(request, rule.options.Status, headers, data)
		if resourceLease != nil {
			response.Body = resourceLease.wrap(response.Body)
			resourceLease.release()
		}
		return response, nil
	}
	if err := validRewriteRequest(request); err != nil {
		return nil, err
	}
	return nil, nil
}

func (p *httpProgram) response(request *http.Request, response *http.Response, id, address string, note func(string)) (err error) {
	p.prepareMatcher(request, address)
	var lease *bodyLease
	defer func() {
		if lease != nil {
			if err == nil {
				response.Body = lease.wrap(response.Body)
			}
			lease.release()
		}
	}()
	script, err := p.script("response", address)
	if err != nil {
		return err
	}
	bodyRules, err := p.hasBodyRules("response", address)
	if err != nil {
		return err
	}
	grpc := unaryGRPCScript(response.Header, script)
	if bodyRules && grpcFormat(response.Header) != grpcNone {
		note("Body rewrite skipped for gRPC; use a binary unary script for framed messages")
		bodyRules = false
	}
	needsBody := bodyRules || script != nil && script.options.RequiresBody
	eligible := true
	if needsBody {
		if response.StatusCode == http.StatusSwitchingProtocols || request.Method == http.MethodHead || response.StatusCode == http.StatusPartialContent {
			note("Body processing skipped for HEAD, partial content or protocol upgrade")
			eligible = false
		} else {
			eligible = bodyEligible(response.Header, note, grpc != nil)
		}
	}
	encoding := contentEncoding(response.Header)
	if err := p.rewriteHeader("response", address, response.Header, nil, note); err != nil {
		return err
	}
	var data []byte
	available := false
	if needsBody && eligible {
		budgetLimit := p.limit
		if script != nil {
			budgetLimit = max(budgetLimit, script.options.MaxBodyBytes)
		}
		lease = reserveBodyWork(budgetLimit)
		if lease == nil {
			note("Body processing skipped: global body budget exhausted; original stream preserved")
		} else {
			limit := p.limit
			if !bodyRules && script != nil {
				limit = script.options.MaxBodyBytes
			}
			var err error
			data, available, err = readRewriteBody(request.Context(), &response.Body, encoding, limit, true, note)
			if err != nil {
				return err
			}
			if available && grpc != nil {
				data, available, err = grpc.prepare(data, true, note)
				if err != nil {
					return err
				}
			}
			if available && bodyRules {
				var changed bool
				data, changed, err = p.rewriteBody("response", address, data, note)
				if err != nil {
					return err
				}
				if changed {
					replaceResponseBody(response, data)
				}
			}
		}
	}
	if script != nil {
		if script.options.RequiresBody && (!available || int64(len(data)) > script.options.MaxBodyBytes) {
			note(script.label + ": skipped unavailable or oversized response body")
		} else {
			result, err := runHTTPScript(request, response, script, id, data, p.environment, note)
			if err != nil {
				var invalid *scriptResultError
				if errors.As(err, &invalid) {
					return err
				}
				message := script.label + ": script skipped; original message preserved: " + err.Error()
				note(message)
				writeLog("warn", message)
				result = scriptResult{}
			}
			if result.abort {
				return errScriptAbort
			}
			if grpc != nil {
				result, err = grpc.result(result, response.Header)
				if err != nil {
					return fmt.Errorf("invalid gRPC script result: %w", err)
				}
			}
			if result.status != 0 {
				if response.StatusCode == http.StatusSwitchingProtocols {
					return errors.New("script cannot change the status of a protocol upgrade")
				}
				response.StatusCode = result.status
				response.Status = fmt.Sprintf("%d %s", result.status, http.StatusText(result.status))
			}
			if result.headers != nil {
				response.Header = result.headers
			}
			if result.bodySet {
				if grpc != nil {
					replaceGRPCResponseBody(response, result.body)
				} else {
					replaceResponseBody(response, result.body)
				}
			}
		}
	}
	normalizeResponseBody(request, response)
	return nil
}

func (p *httpProgram) rewriteHeader(phase, address string, header http.Header, host *string, note func(string)) error {
	for _, rule := range p.headers {
		if rule.options.Phase != phase {
			continue
		}
		matched, err := p.match(rule.pattern, address)
		if err != nil {
			return err
		}
		if !matched {
			continue
		}
		key := http.CanonicalHeaderKey(rule.options.Header)
		values := header.Values(key)
		isHost := host != nil && key == "Host"
		if isHost {
			values = []string{*host}
		}
		switch rule.options.Operation {
		case "add":
			values = append(append([]string(nil), values...), rule.options.Value)
		case "delete":
			values = nil
		case "replace":
			if len(values) > 0 {
				values = []string{rule.options.Value}
			}
		case "replace_regex":
			values = append([]string(nil), values...)
			for i, value := range values {
				var err error
				values[i], err = boundedReplace(rule.regex, value, rule.options.Replacement, MaxHeaderBytes, p.regex)
				if err != nil {
					return err
				}
				if !httpguts.ValidHeaderFieldValue(values[i]) {
					return errors.New("header rewrite produced an invalid field value")
				}
			}
		}
		if isHost {
			*host = values[0]
		} else if len(values) == 0 {
			header.Del(key)
		} else {
			header[key] = values
		}
		note(rule.label)
	}
	size := 0
	for name, values := range header {
		for _, value := range values {
			size += len(name) + len(value)
		}
	}
	if size > MaxHeaderBytes {
		return errors.New("rewritten headers exceed 64 KiB")
	}
	return nil
}

func (p *httpProgram) script(phase, address string) (*compiledScript, error) {
	if p.scripts == nil {
		return nil, nil
	}
	for i := range p.scripts.http {
		script := &p.scripts.http[i]
		if script.options.Phase == phase {
			matched, err := p.match(script.pattern, address)
			if err != nil {
				return nil, err
			}
			if matched {
				return script, nil
			}
		}
	}
	return nil, nil
}

func (p *httpProgram) hasBodyRules(phase, address string) (bool, error) {
	for _, rule := range p.bodies {
		if rule.options.Phase == phase {
			matched, err := p.match(rule.pattern, address)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
	}
	return false, nil
}

func localRewriteResponse(request *http.Request, status int, header http.Header, body []byte) *http.Response {
	response := &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: header,
		Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: request}
	header.Set("Content-Length", strconv.Itoa(len(body)))
	normalizeResponseBody(request, response)
	return response
}

func validRewriteRequest(request *http.Request) error {
	if _, err := parseRewriteURL(request.URL.String()); err != nil {
		return err
	}
	u, err := url.Parse("http://" + request.Host)
	if err != nil || u.Host != request.Host || u.User != nil || !validHost(normalizeHost(u.Hostname())) || strings.ContainsAny(request.Host, "\r\n") {
		return errors.New("rewrite produced an invalid Host")
	}
	if port := u.Port(); port != "" {
		if _, err = netPort(port); err != nil {
			return err
		}
	}
	if !httpguts.ValidHeaderFieldName(request.Method) || request.Method == http.MethodConnect {
		return errors.New("rewrite produced an unsupported HTTP method")
	}
	return nil
}

func sameRewriteOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if u.Port() != "" {
			return u.Port()
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return a.Scheme == b.Scheme && normalizeHost(a.Hostname()) == normalizeHost(b.Hostname()) && port(a) == port(b)
}
