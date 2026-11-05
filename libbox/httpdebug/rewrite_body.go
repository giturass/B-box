package httpdebug

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var errRewriteSize = errors.New("HTTP rewrite body exceeds max_body_bytes")

type replayBody struct {
	io.Reader
	io.Closer
}

// Buffer only messages selected for body processing. On an oversized response
// or unsupported encoding, replay every consumed byte and keep streaming.
// The wire and decoded sizes are independently bounded (including gzip bombs).
func readRewriteBody(ctx context.Context, body *io.ReadCloser, encoding string, limit int64, response bool, note func(string)) ([]byte, bool, error) {
	encodings, err := contentEncodings(encoding)
	if err != nil {
		note("Body processing skipped: " + err.Error() + "; original stream preserved")
		return nil, false, nil
	}
	if *body == nil || *body == http.NoBody {
		return []byte{}, true, nil
	}
	original := *body
	readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stop := context.AfterFunc(readCtx, func() { original.Close() })
	defer stop()
	wire, err := readBodyPrefix(original, limit)
	*body = &replayBody{io.MultiReader(bytes.NewReader(wire), original), original}
	if err != nil {
		return nil, false, err
	}
	if readCtx.Err() != nil {
		return nil, false, readCtx.Err()
	}
	if int64(len(wire)) > limit {
		if response {
			note("Body processing skipped: response exceeds max_body_bytes; original stream preserved")
			return nil, false, nil
		}
		return nil, false, errRewriteSize
	}
	decoded, err := decodeContent(wire, encodings, limit)
	if errors.Is(err, errRewriteSize) {
		if response {
			note("Body processing skipped: decoded response exceeds max_body_bytes; original stream preserved")
			return nil, false, nil
		}
		return nil, false, errRewriteSize
	}
	if err != nil {
		note("Body processing skipped: invalid or unsupported compressed body: " + err.Error() + "; original stream preserved")
		return nil, false, nil
	}
	return decoded, true, nil
}

func replaceRequestBody(request *http.Request, data []byte) {
	if request.Body != nil {
		request.Body.Close()
	}
	request.Body = io.NopCloser(bytes.NewReader(data))
	if len(data) == 0 {
		request.Body = http.NoBody
	}
	request.ContentLength = int64(len(data))
	request.GetBody = nil
	request.TransferEncoding = nil
	request.Trailer = nil
	normalizeBodyHeaders(request.Header, int64(len(data)))
}

func replaceResponseBody(response *http.Response, data []byte) {
	if response.Body != nil {
		response.Body.Close()
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	response.ContentLength = int64(len(data))
	response.TransferEncoding = nil
	response.Trailer = nil
	normalizeBodyHeaders(response.Header, int64(len(data)))
}

func normalizeBodyHeaders(header http.Header, length int64) {
	for _, name := range []string{"Content-Encoding", "Transfer-Encoding", "Trailer", "Content-MD5", "Digest", "Content-Digest", "Repr-Digest", "ETag", "Accept-Ranges", "Content-Range"} {
		header.Del(name)
	}
	header.Set("Content-Length", strconv.FormatInt(length, 10))
}

func normalizeResponseBody(request *http.Request, response *http.Response) {
	if request.Method != http.MethodHead && response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusNotModified && response.StatusCode != http.StatusResetContent {
		return
	}
	if response.Body != nil {
		response.Body.Close()
	}
	response.Body = http.NoBody
	response.TransferEncoding, response.Trailer = nil, nil
	response.Header.Del("Transfer-Encoding")
	response.Header.Del("Trailer")
	if response.StatusCode == http.StatusNoContent || response.StatusCode == http.StatusNotModified {
		response.Header.Del("Content-Length")
		response.ContentLength = 0
	} else if response.StatusCode == http.StatusResetContent {
		response.Header.Set("Content-Length", "0")
		response.ContentLength = 0
	}
}

// Expand Go/RE2 replacement templates without allowing a small template and a
// large capture to allocate an unbounded intermediate replacement string.
func boundedReplace(re *rewriteRegexp, value, template string, limit int64, budgets ...*regexBudget) (string, error) {
	var budget *regexBudget
	if len(budgets) > 0 {
		budget = budgets[0]
	}
	var result strings.Builder
	appendPart := func(part string) error {
		if int64(result.Len())+int64(len(part)) > limit {
			return errRewriteSize
		}
		result.WriteString(part)
		return nil
	}
	position := 0
	err := re.eachMatch(value, budget, func(match []int) error {
		if err := appendPart(value[position:match[0]]); err != nil {
			return err
		}
		for rest := template; rest != ""; {
			at := strings.IndexByte(rest, '$')
			if at < 0 {
				if err := appendPart(rest); err != nil {
					return err
				}
				break
			}
			if err := appendPart(rest[:at]); err != nil {
				return err
			}
			rest = rest[at+1:]
			if strings.HasPrefix(rest, "$") {
				if err := appendPart("$"); err != nil {
					return err
				}
				rest = rest[1:]
				continue
			}
			name, consumed := replacementName(rest)
			if consumed == 0 {
				if err := appendPart("$"); err != nil {
					return err
				}
				continue
			}
			rest = rest[consumed:]
			index, err := strconv.Atoi(name)
			if err != nil {
				index = re.SubexpIndex(name)
			}
			if index >= 0 && index < len(match)/2 && match[index*2] >= 0 {
				if err := appendPart(value[match[index*2]:match[index*2+1]]); err != nil {
					return err
				}
			}
		}
		position = match[1]
		return nil
	})
	if err != nil {
		return "", err
	}
	if err := appendPart(value[position:]); err != nil {
		return "", err
	}
	return result.String(), nil
}

func replacementName(value string) (string, int) {
	braced := strings.HasPrefix(value, "{")
	start := 0
	if braced {
		start = 1
	}
	end := start
	for end < len(value) {
		c := value[end]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			break
		}
		end++
	}
	if end == start {
		return "", 0
	}
	if braced {
		if end == len(value) || value[end] != '}' {
			return "", 0
		}
		return value[start:end], end + 1
	}
	return value[:end], end
}

func (p *httpProgram) rewriteBody(phase, address string, data []byte, note func(string)) ([]byte, bool, error) {
	changed := false
	for _, rule := range p.bodies {
		if rule.options.Phase != phase {
			continue
		}
		matched, err := p.match(rule.pattern, address)
		if err != nil {
			return nil, false, err
		}
		if !matched {
			continue
		}
		if rule.options.JQ != nil {
			if rule.jqError != nil {
				message := rule.label + ": jq skipped: " + rule.jqError.Error()
				note(message)
				writeLog("warn", message)
				continue
			}
			start := time.Now()
			next, jqErr := rewriteJSONBody(p.regex.ctx, rule.jq, data, p.bodyLimit(phase), p.jqRemaining)
			p.jqRemaining -= time.Since(start)
			if jqErr != nil {
				message := rule.label + ": jq skipped: " + jqErr.Error()
				note(message)
				writeLog("warn", message)
				continue
			}
			if !bytes.Equal(next, data) {
				data, changed = next, true
				note(rule.label)
			}
			continue
		}
		if !utf8.Valid(data) {
			note(rule.label + ": skipped non-UTF-8 body")
			continue
		}
		next, err := boundedReplace(rule.regex, string(data), rule.options.Replacement, p.bodyLimit(phase), p.regex)
		if err != nil {
			return nil, false, err
		}
		if next != string(data) {
			changed = true
			data = []byte(next)
			note(rule.label)
		}
	}
	return data, changed, nil
}

func netPort(value string) (uint16, error) {
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil || port == 0 {
		return 0, errors.New("invalid rewrite destination port")
	}
	return uint16(port), nil
}
