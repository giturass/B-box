package httpdebug

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/dop251/goja"
	"golang.org/x/net/http/httpguts"
)

const (
	MaxConfigBytes    = 4 * 1024 * 1024
	MaxRewriteBody    = 1024 * 1024
	MaxScriptBytes    = 128 * 1024
	MaxRules          = 128
	requestDirection  = "http-request"
	responseDirection = "http-response"
)

type Rule struct {
	Name     string `json:"name,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	Pattern  string `json:"pattern"`
	pattern  *regexp.Regexp
}

type HeaderRule struct {
	Rule
	Type         string `json:"type,omitempty"`
	Action       string `json:"action"`
	Header       string `json:"header"`
	Value        string `json:"value,omitempty"`
	Replacement  string `json:"replacement,omitempty"`
	valuePattern *regexp.Regexp
}

type URLRule struct {
	Rule
	Type        string `json:"type,omitempty"`
	Replacement string `json:"replacement,omitempty"`
}

type BodyRule struct {
	Rule
	Type        string `json:"type,omitempty"`
	Regex       string `json:"regex"`
	Replacement string `json:"replacement"`
	bodyPattern *regexp.Regexp
}

type LocalRule struct {
	Rule
	DataType string   `json:"dataType,omitempty"`
	Data     string   `json:"data,omitempty"`
	Status   int      `json:"status,omitempty"`
	Headers  []Header `json:"headers,omitempty"`
	data     []byte
}

type ScriptRule struct {
	Rule
	Type           string `json:"type,omitempty"`
	Source         string `json:"source"`
	Argument       string `json:"argument,omitempty"`
	RequiresBody   bool   `json:"requiresBody,omitempty"`
	BinaryBodyMode bool   `json:"binaryBodyMode,omitempty"`
	FullHeaderMode bool   `json:"fullHeaderMode,omitempty"`
	MaxSize        int    `json:"maxSize,omitempty"`
	TimeoutMS      int    `json:"timeoutMs,omitempty"`
	program        *goja.Program
}

func (r *Rule) compile() error {
	if len(r.Name) > 128 || r.Pattern == "" || len(r.Pattern) > 8192 {
		return errors.New("rule needs a URL pattern (up to 8192 bytes) and a name of at most 128 bytes")
	}
	var err error
	r.pattern, err = regexp.Compile(r.Pattern)
	return err
}

func (r Rule) matches(value string) bool { return !r.Disabled && r.pattern.MatchString(value) }

func direction(value *string) error {
	if *value == "" {
		*value = requestDirection
	}
	if *value != requestDirection && *value != responseDirection {
		return errors.New("type must be http-request or http-response")
	}
	return nil
}

func (c *Config) validateRules() error {
	if len(c.HeaderRewrite)+len(c.URLRewrite)+len(c.BodyRewrite)+len(c.MapLocal)+len(c.Scripts) > MaxRules || len(c.Scripts) > 16 {
		return errors.New("use at most 128 HTTP rules, including at most 16 scripts")
	}
	check := func(kind string, i int, r *Rule, validate func() error) error {
		err := r.compile()
		if err == nil {
			err = validate()
		}
		if err != nil {
			return fmt.Errorf("%s rule %d (%s): %w", kind, i+1, r.Name, err)
		}
		return nil
	}
	for i := range c.HeaderRewrite {
		r := &c.HeaderRewrite[i]
		if err := check("Header Rewrite", i, &r.Rule, func() error {
			if err := direction(&r.Type); err != nil {
				return err
			}
			if !httpguts.ValidHeaderFieldName(r.Header) || framingHeader(r.Header) {
				return errors.New("invalid header or protected HTTP framing header")
			}
			if !httpguts.ValidHeaderFieldValue(r.Value) || !httpguts.ValidHeaderFieldValue(r.Replacement) || len(r.Value)+len(r.Replacement) > 8192 {
				return errors.New("invalid or oversized header value")
			}
			switch r.Action {
			case "header-add", "header-del", "header-replace":
			case "header-replace-regex":
				var err error
				r.valuePattern, err = regexp.Compile(r.Value)
				return err
			default:
				return errors.New("unknown header action")
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for i := range c.URLRewrite {
		r := &c.URLRewrite[i]
		if err := check("URL Rewrite", i, &r.Rule, func() error {
			if r.Type == "" {
				r.Type = "header"
			}
			if len(r.Replacement) > 8192 {
				return errors.New("URL replacement exceeds 8192 bytes")
			}
			switch r.Type {
			case "header", "301", "302", "307", "308", "reject":
				return nil
			default:
				return errors.New("URL type must be header, 301, 302, 307, 308 or reject")
			}
		}); err != nil {
			return err
		}
	}
	for i := range c.BodyRewrite {
		r := &c.BodyRewrite[i]
		if err := check("Body Rewrite", i, &r.Rule, func() error {
			if err := direction(&r.Type); err != nil {
				return err
			}
			if len(r.Regex) > 8192 || len(r.Replacement) > MaxScriptBytes {
				return errors.New("body pattern or replacement too large")
			}
			var err error
			r.bodyPattern, err = regexp.Compile("(?m)" + r.Regex)
			return err
		}); err != nil {
			return err
		}
	}
	for i := range c.MapLocal {
		r := &c.MapLocal[i]
		if err := check("Map Local", i, &r.Rule, func() error {
			if r.Status == 0 {
				r.Status = http.StatusOK
			}
			if r.Status < 200 || r.Status > 999 {
				return errors.New("local response status must be between 200 and 999")
			}
			if r.DataType == "" {
				r.DataType = "text"
			}
			switch r.DataType {
			case "text":
				r.data = []byte(r.Data)
			case "base64":
				var err error
				r.data, err = base64.StdEncoding.DecodeString(r.Data)
				if err != nil {
					return err
				}
			case "tiny-gif":
				r.data, _ = base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7")
			default:
				return errors.New("dataType must be text, base64 or tiny-gif; import files as base64 data")
			}
			if len(r.data) > MaxRewriteBody {
				return errors.New("local response exceeds 1 MiB")
			}
			_, err := ruleHeaders(r.Headers)
			return err
		}); err != nil {
			return err
		}
	}
	for i := range c.Scripts {
		r := &c.Scripts[i]
		if err := check("Script", i, &r.Rule, func() error {
			if err := direction(&r.Type); err != nil {
				return err
			}
			if r.Source == "" || len(r.Source) > MaxScriptBytes || len(r.Argument) > 8192 {
				return errors.New("script source must be 1–131072 bytes; argument at most 8192 bytes")
			}
			if r.MaxSize == 0 {
				r.MaxSize = MaxRewriteBody
			}
			if r.TimeoutMS == 0 {
				r.TimeoutMS = 1000
			}
			if r.MaxSize < 1 || r.MaxSize > MaxRewriteBody || r.TimeoutMS < 10 || r.TimeoutMS > 3000 {
				return errors.New("script maxSize must be 1–1048576 bytes and timeoutMs 10–3000")
			}
			var err error
			r.program, err = goja.Compile(r.Name, r.Source, false)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func framingHeader(name string) bool {
	switch strings.ToLower(name) {
	case "content-length", "transfer-encoding", "trailer":
		return true
	default:
		return false
	}
}

func ruleHeaders(headers []Header) (http.Header, error) {
	h := make(http.Header)
	for _, item := range headers {
		if framingHeader(item.Name) || strings.EqualFold(item.Name, "Host") {
			return nil, errors.New("local headers cannot set Host or HTTP framing")
		}
		h.Add(item.Name, item.Value)
	}
	return h, validateHeaders(h)
}

func validateHeaders(headers http.Header) error {
	size := 0
	for name, values := range headers {
		if !httpguts.ValidHeaderFieldName(name) {
			return errors.New("invalid HTTP header name")
		}
		for _, value := range values {
			if !httpguts.ValidHeaderFieldValue(value) {
				return errors.New("invalid HTTP header value")
			}
			size += len(name) + len(value) + 4
			if size > MaxHeaderBytes {
				return errors.New("rewritten headers exceed 64 KiB")
			}
		}
	}
	return nil
}

func rewriteURL(value string) (*url.URL, error) {
	if len(value) > MaxHeaderBytes {
		return nil, errors.New("rewritten URL exceeds 64 KiB")
	}
	u, err := url.Parse(value)
	if err != nil {
		return nil, err
	}
	if (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "ws" && u.Scheme != "wss") || !validHost(normalizeHost(u.Hostname())) || u.User != nil || u.Fragment != "" || u.Opaque != "" || strings.HasSuffix(u.Host, ":") {
		return nil, errors.New("rewrite needs an absolute HTTP(S)/WS(S) URL without credentials or fragment")
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return nil, errors.New("invalid URL port")
		}
	}
	return u, nil
}

func validateHostHeader(value string) error {
	u, err := rewriteURL("http://" + value + "/")
	if err != nil {
		return err
	}
	if u.Host != value || u.Path != "/" || u.RawQuery != "" || u.ForceQuery {
		return errors.New("invalid Host header authority")
	}
	return nil
}

// Bound both output expansion and match bookkeeping. Go regexps use RE2
// semantics; backreferences in patterns and look-around are not supported.
func replaceBounded(pattern *regexp.Regexp, value, replacement string, limit int) (string, error) {
	matches := pattern.FindAllStringSubmatchIndex(value, 16385)
	if len(matches) > 16384 {
		return "", errors.New("rewrite exceeds 16384 replacements")
	}
	var output strings.Builder
	last := 0
	for _, match := range matches {
		expanded, err := expandBounded(pattern, replacement, value, match, limit-output.Len()-(match[0]-last))
		if err != nil {
			return "", err
		}
		if output.Len()+match[0]-last+len(expanded) > limit {
			return "", errors.New("rewrite exceeds output size limit")
		}
		output.WriteString(value[last:match[0]])
		output.WriteString(expanded)
		last = match[1]
	}
	if output.Len()+len(value)-last > limit {
		return "", errors.New("rewrite exceeds output size limit")
	}
	output.WriteString(value[last:])
	return output.String(), nil
}

// regexp.ExpandString itself allocates before returning; a short template with
// repeated $0 references can otherwise expand a 1 MiB input into gigabytes.
func expandBounded(pattern *regexp.Regexp, template, value string, match []int, limit int) (string, error) {
	var output strings.Builder
	write := func(text string) error {
		if len(text) > limit-output.Len() {
			return errors.New("rewrite exceeds output size limit")
		}
		output.WriteString(text)
		return nil
	}
	for len(template) > 0 {
		literal, rest, found := strings.Cut(template, "$")
		if err := write(literal); err != nil {
			return "", err
		}
		if !found {
			break
		}
		template = rest
		if strings.HasPrefix(template, "$") {
			if err := write("$"); err != nil {
				return "", err
			}
			template = template[1:]
			continue
		}
		name := template
		braced := strings.HasPrefix(name, "{")
		if braced {
			name = name[1:]
		}
		length := 0
		for length < len(name) {
			r, size := utf8.DecodeRuneInString(name[length:])
			if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
				break
			}
			length += size
		}
		if length == 0 || braced && (length == len(name) || name[length] != '}') {
			if err := write("$"); err != nil {
				return "", err
			}
			continue
		}
		name = name[:length]
		consumed := length
		if braced {
			consumed += 2
		}
		template = template[consumed:]
		index, err := strconv.Atoi(name)
		if err != nil || len(name) > 1 && name[0] == '0' {
			index = pattern.SubexpIndex(name)
		}
		if index >= 0 && index < len(match)/2 && match[2*index] >= 0 {
			if err := write(value[match[2*index]:match[2*index+1]]); err != nil {
				return "", err
			}
		}
	}
	return output.String(), nil
}
