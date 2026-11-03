package httpdebug

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/http/httpguts"
)

func (p *RewriteProgram) request(request *http.Request, id, address string, note func(string)) (*http.Response, error) {
	if err := p.rewriteHeader("request", address, request.Header, &request.Host, note); err != nil {
		return nil, err
	}
	for _, rule := range p.urls {
		if !rule.pattern.MatchString(address) {
			continue
		}
		note(rule.label)
		if rule.options.Action == "reject" {
			return localRewriteResponse(request, http.StatusForbidden, make(http.Header), []byte("Request rejected by B-box rewrite\n")), nil
		}
		next, err := boundedReplace(rule.pattern, address, rule.options.Replacement, 8192)
		if err != nil {
			return nil, err
		}
		target, err := parseRewriteURL(next)
		if err != nil {
			return nil, err
		}
		if rule.options.Action != "header" {
			status, _ := strconv.Atoi(rule.options.Action)
			return localRewriteResponse(request, status, http.Header{"Location": []string{target.String()}}, nil), nil
		}
		request.URL, request.Host = target, target.Host
		break
	}
	for _, rule := range p.remote {
		if !rule.pattern.MatchString(address) {
			continue
		}
		next, err := boundedReplace(rule.pattern, address, rule.options.Replacement, 8192)
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
	script := p.script("request", address)
	needsBody := p.hasBodyRules("request", address) || script != nil && script.options.RequiresBody
	var data []byte
	available := false
	if needsBody {
		limit := p.limit
		if !p.hasBodyRules("request", address) && script != nil {
			limit = script.options.MaxBodyBytes
		}
		var err error
		data, available, err = readRewriteBody(request.Context(), &request.Body, request.Header.Get("Content-Encoding"), limit, false, note)
		if err != nil {
			return nil, err
		}
		if available {
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
		if !script.options.RequiresBody || available {
			result, err := p.runScript(request, nil, script, id, data, note)
			if err != nil {
				return nil, err
			}
			if result.abort {
				return nil, errors.New("request aborted by rewrite script")
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
				replaceRequestBody(request, result.body)
			}
			if result.response != nil {
				return result.response, nil
			}
		} else {
			note(script.label + ": skipped unavailable body")
		}
	}
	for i, rule := range p.local {
		if !rule.pattern.MatchString(address) {
			continue
		}
		headers, _ := rewriteHeaders(rule.options.Headers)
		if headers.Get("Content-Type") == "" {
			switch rule.options.DataType {
			case "tiny-gif":
				headers.Set("Content-Type", "image/gif")
			case "base64":
				headers.Set("Content-Type", "application/octet-stream")
			default:
				headers.Set("Content-Type", "text/plain; charset=utf-8")
			}
		}
		note(rule.label + ": local response")
		return localRewriteResponse(request, rule.options.Status, headers, p.data[i]), nil
	}
	if err := validRewriteRequest(request); err != nil {
		return nil, err
	}
	return nil, nil
}

func (p *RewriteProgram) response(request *http.Request, response *http.Response, id, address string, note func(string)) error {
	if err := p.rewriteHeader("response", address, response.Header, nil, note); err != nil {
		return err
	}
	script := p.script("response", address)
	needsBody := p.hasBodyRules("response", address) || script != nil && script.options.RequiresBody
	var data []byte
	available := false
	if needsBody {
		if response.StatusCode == http.StatusSwitchingProtocols || request.Method == http.MethodHead || response.StatusCode == http.StatusPartialContent || strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
			note("Body processing skipped for HEAD, partial content, event stream or protocol upgrade")
		} else {
			limit := p.limit
			if !p.hasBodyRules("response", address) && script != nil {
				limit = script.options.MaxBodyBytes
			}
			var err error
			data, available, err = readRewriteBody(request.Context(), &response.Body, response.Header.Get("Content-Encoding"), limit, true, note)
			if err != nil {
				return err
			}
			if available {
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
			result, err := p.runScript(request, response, script, id, data, note)
			if err != nil {
				return err
			}
			if result.abort {
				return errors.New("response aborted by rewrite script")
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
				replaceResponseBody(response, result.body)
			}
		}
	}
	normalizeResponseBody(request, response)
	return nil
}

func (p *RewriteProgram) rewriteHeader(phase, address string, header http.Header, host *string, note func(string)) error {
	for _, rule := range p.headers {
		if rule.options.Phase != phase || !rule.pattern.MatchString(address) {
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
				values[i], err = boundedReplace(rule.regex, value, rule.options.Replacement, MaxHeaderBytes)
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

func (p *RewriteProgram) script(phase, address string) *compiledScript {
	for i := range p.scripts {
		script := &p.scripts[i]
		if script.options.Phase == phase && script.pattern.MatchString(address) {
			return script
		}
	}
	return nil
}

func (p *RewriteProgram) hasBodyRules(phase, address string) bool {
	for _, rule := range p.bodies {
		if rule.options.Phase == phase && rule.pattern.MatchString(address) {
			return true
		}
	}
	return false
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
