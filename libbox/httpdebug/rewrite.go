package httpdebug

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/andybalholm/brotli"
)

// Body buffers and JS runtimes share a process-wide limit, including across
// engine restarts. Header-only rewrites do not wait on this queue.
var rewriteSlots = make(chan struct{}, 2)

func acquireRewrite(ctx context.Context) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	select {
	case rewriteSlots <- struct{}{}:
		return func() { <-rewriteSlots }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("HTTP rewrite queue: %w", ctx.Err())
	}
}

func (e *Engine) note(id, kind string, rule Rule) {
	name := rule.Name
	if name == "" {
		name = rule.Pattern
	}
	e.store.addNote(id, kind+": "+name)
}

func (e *Engine) rewriteHeaders(id, direction, requestURL string, headers http.Header, host *string) error {
	for _, rule := range e.config.HeaderRewrite {
		if rule.Type != direction || !rule.matches(requestURL) {
			continue
		}
		name := http.CanonicalHeaderKey(rule.Header)
		isHost := strings.EqualFold(name, "Host") && host != nil
		values := headers.Values(name)
		if isHost && *host != "" {
			values = []string{*host}
		}
		switch rule.Action {
		case "header-add":
			values = append(values, rule.Value)
		case "header-del":
			values = nil
		case "header-replace":
			if len(values) > 0 {
				values = []string{rule.Value}
			}
		case "header-replace-regex":
			values = append([]string(nil), values...)
			for i, value := range values {
				var err error
				values[i], err = replaceBounded(rule.valuePattern, value, rule.Replacement, MaxHeaderBytes)
				if err != nil {
					return err
				}
			}
		}
		if isHost {
			if len(values) != 1 {
				return errors.New("request must have exactly one Host header")
			}
			if err := validateHostHeader(values[0]); err != nil {
				return err
			}
			*host = values[0]
		} else if len(values) == 0 {
			headers.Del(name)
		} else {
			headers[name] = values
		}
		e.note(id, "Header Rewrite", rule.Rule)
	}
	return validateHeaders(headers)
}

func (e *Engine) rewriteRequest(id string, request *http.Request) (*http.Response, error) {
	wireEncoding := request.Header.Get("Content-Encoding")
	if err := e.rewriteHeaders(id, requestDirection, request.URL.String(), request.Header, &request.Host); err != nil {
		return nil, err
	}
	for _, rule := range e.config.URLRewrite {
		if !rule.matches(request.URL.String()) {
			continue
		}
		e.note(id, "URL Rewrite", rule.Rule)
		if rule.Type == "reject" {
			return localResponse(request, http.StatusForbidden, http.Header{"Content-Type": {"text/plain; charset=utf-8"}}, []byte("B-box: rejected by URL Rewrite")), nil
		}
		value, err := replaceBounded(rule.pattern, request.URL.String(), rule.Replacement, MaxHeaderBytes)
		if err != nil {
			return nil, err
		}
		u, err := rewriteURL(value)
		if err != nil {
			return nil, err
		}
		if rule.Type == "header" {
			request.URL, request.Host = u, u.Host
		} else {
			status, _ := strconv.Atoi(rule.Type)
			return localResponse(request, status, http.Header{"Location": {u.String()}}, nil), nil
		}
		break // Only the first matching transparent URL rewrite is applied.
	}
	if response, err := e.rewriteMessage(id, request, nil, wireEncoding); err != nil || response != nil {
		return response, err
	}
	for _, rule := range e.config.MapLocal {
		if !rule.matches(request.URL.String()) {
			continue
		}
		headers, err := ruleHeaders(rule.Headers)
		if err != nil {
			return nil, err
		}
		if headers.Get("Content-Type") == "" {
			contentType := "application/octet-stream"
			if rule.DataType == "text" {
				contentType = "text/plain; charset=utf-8"
			}
			if rule.DataType == "tiny-gif" {
				contentType = "image/gif"
			}
			headers.Set("Content-Type", contentType)
		}
		e.note(id, "Map Local", rule.Rule)
		return localResponse(request, rule.Status, headers, rule.data), nil
	}
	return nil, nil
}

func (e *Engine) rewriteResponse(id string, request *http.Request, response *http.Response) error {
	wireEncoding := response.Header.Get("Content-Encoding")
	if err := e.rewriteHeaders(id, responseDirection, request.URL.String(), response.Header, nil); err != nil {
		return err
	}
	_, err := e.rewriteMessage(id, request, response, wireEncoding)
	return err
}

func localResponse(request *http.Request, status int, headers http.Header, data []byte) *http.Response {
	response := &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Header: headers, Request: request,
		Body: io.NopCloser(bytes.NewReader(data)), ContentLength: int64(len(data))}
	response.Header.Set("Content-Length", strconv.Itoa(len(data)))
	if status == http.StatusNoContent || status == http.StatusNotModified {
		response.Body = http.NoBody
		response.ContentLength = 0
		response.Header.Del("Content-Length")
	} else if request.Method == http.MethodHead {
		response.Body = http.NoBody
	}
	return response
}

// rewriteMessage only buffers a body if a matching body rule or script needs it.
// On response overflow, the already-read prefix is replayed before the remaining
// stream, including compressed bytes. SSE, upgrades and partial responses stream.
func (e *Engine) rewriteMessage(id string, request *http.Request, response *http.Response, wireEncoding string) (*http.Response, error) {
	direction := requestDirection
	if response != nil {
		direction = responseDirection
	}
	requestURL := request.URL.String()
	var rules []BodyRule
	for _, rule := range e.config.BodyRewrite {
		if rule.Type == direction && rule.matches(requestURL) {
			rules = append(rules, rule)
		}
	}
	var script *ScriptRule
	for i := range e.config.Scripts {
		rule := &e.config.Scripts[i]
		if rule.Type == direction && rule.matches(requestURL) {
			script = rule
			break
		}
	}
	if len(rules) == 0 && script == nil {
		return nil, nil
	}
	release, err := acquireRewrite(request.Context())
	if err != nil {
		return nil, err
	}
	defer release()
	wantsBody := len(rules) > 0 || script != nil && script.RequiresBody
	bodyAllowed := true
	if response == nil {
		bodyAllowed = len(request.TransferEncoding) == 0 && request.Header.Get("Expect") == "" && request.Header.Get("Upgrade") == ""
	} else {
		contentType := strings.ToLower(response.Header.Get("Content-Type"))
		bodyAllowed = request.Method != http.MethodHead && response.StatusCode >= 200 &&
			response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotModified &&
			response.StatusCode != http.StatusPartialContent && response.Header.Get("Content-Range") == "" &&
			!strings.HasPrefix(contentType, "text/event-stream") && !strings.HasPrefix(contentType, "application/grpc")
	}
	var data []byte
	available := false
	if wantsBody && bodyAllowed {
		body := &request.Body
		if response != nil {
			body = &response.Body
		}
		data, available, err = loadRewriteBody(body, wireEncoding, response != nil)
		if err != nil {
			return nil, err
		}
	}
	if wantsBody && !available {
		e.store.addNote(id, "Body processing skipped: streaming, partial, oversized or unsupported encoding")
	}
	changed := false
	if available && len(rules) > 0 {
		if !utf8.Valid(data) {
			e.store.addNote(id, "Body Rewrite skipped: body is not UTF-8")
		} else {
			value := string(data)
			for _, rule := range rules {
				next, err := replaceBounded(rule.bodyPattern, value, rule.Replacement, MaxRewriteBody)
				if err != nil {
					return nil, err
				}
				changed = changed || value != next
				value = next
				e.note(id, "Body Rewrite", rule.Rule)
			}
			data = []byte(value)
		}
	}
	// A response script requiring a body is skipped if buffering is unavailable;
	// request scripts can still edit headers for chunked/Expect requests.
	if script != nil && script.RequiresBody && response != nil && !available {
		script = nil
	}
	if script != nil && script.RequiresBody && available && len(data) > script.MaxSize {
		if response == nil {
			return nil, errors.New("request body exceeds script maxSize")
		}
		e.store.addNote(id, "Script skipped: response body exceeds maxSize")
		script = nil
	}
	if script != nil && script.RequiresBody && available && !script.BinaryBodyMode && !utf8.Valid(data) {
		e.store.addNote(id, "Script skipped: text body is not UTF-8; use binaryBodyMode")
		script = nil
	}
	if changed {
		setRewriteBody(request, response, data)
	}
	if script != nil {
		result, err := runScript(request.Context(), *script, id, request, response, data, available, func(note string) { e.store.addNote(id, note) })
		if err != nil {
			return nil, fmt.Errorf("script %s: %w", script.Name, err)
		}
		e.note(id, "Script", script.Rule)
		return applyScriptResult(*script, result, request, response, available)
	}
	return nil, nil
}

type replayBody struct {
	io.Reader
	io.Closer
}

func loadRewriteBody(body *io.ReadCloser, encoding string, response bool) ([]byte, bool, error) {
	if *body == nil || *body == http.NoBody {
		return []byte{}, true, nil
	}
	original := *body
	// Closing an upstream response body interrupts a stalled buffering read.
	var stop *time.Timer
	if response {
		stop = time.AfterFunc(5*time.Second, func() { original.Close() })
		defer stop.Stop()
	}
	raw, err := io.ReadAll(io.LimitReader(original, MaxRewriteBody+1))
	*body = &replayBody{Reader: io.MultiReader(bytes.NewReader(raw), original), Closer: original}
	if err != nil {
		return nil, false, err
	}
	if len(raw) > MaxRewriteBody {
		if response {
			return nil, false, nil
		}
		return nil, false, errors.New("request body exceeds 1 MiB rewrite limit")
	}
	original.Close()
	*body = io.NopCloser(bytes.NewReader(raw))
	var reader io.Reader = bytes.NewReader(raw)
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
		return raw, true, nil
	case "gzip":
		gzipReader, err := gzip.NewReader(reader)
		if err != nil {
			return nil, false, err
		}
		defer gzipReader.Close()
		reader = gzipReader
	case "deflate":
		deflateReader, err := zlib.NewReader(reader)
		if err != nil {
			deflateReader = flate.NewReader(bytes.NewReader(raw))
		}
		defer deflateReader.Close()
		reader = deflateReader
	case "br":
		reader = brotli.NewReader(reader)
	default:
		return nil, false, nil
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, MaxRewriteBody+1))
	if err != nil {
		return nil, false, err
	}
	if len(decoded) > MaxRewriteBody {
		if response {
			return nil, false, nil
		}
		return nil, false, errors.New("decoded request body exceeds 1 MiB rewrite limit")
	}
	return decoded, true, nil
}

func setRewriteBody(request *http.Request, response *http.Response, data []byte) {
	body, headers := &request.Body, request.Header
	if response != nil {
		body, headers = &response.Body, response.Header
	}
	if *body != nil {
		(*body).Close()
	}
	*body = io.NopCloser(bytes.NewReader(data))
	for _, name := range []string{"Content-Encoding", "Transfer-Encoding", "Trailer", "Content-MD5", "Digest", "ETag"} {
		headers.Del(name)
	}
	headers.Set("Content-Length", strconv.Itoa(len(data)))
	if response != nil {
		response.ContentLength, response.TransferEncoding, response.Trailer, response.Uncompressed = int64(len(data)), nil, nil, true
	} else {
		request.ContentLength, request.TransferEncoding, request.Trailer = int64(len(data)), nil, nil
	}
}
