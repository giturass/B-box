package httpdebug

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/http/httpguts"
)

const (
	MaxRewriteConfigBytes = 2 * 1024 * 1024
	MaxRewriteRules       = 256
	MaxScriptBytes        = 128 * 1024
)

// RewriteConfig is the B-box experimental.http_rewrite profile extension.
// It is deliberately separate from the device's private capture settings.
// Resources are embedded in the JSON profile; validation never reads files,
// downloads code, runs scripts, installs certificates or starts interception.
type RewriteConfig struct {
	Enabled       bool                       `json:"enabled,omitempty"`
	Ports         []uint16                   `json:"ports,omitempty"`
	Packages      []string                   `json:"packages,omitempty"`
	MaxBodyBytes  int64                      `json:"max_body_bytes,omitempty"`
	HeaderRewrite []HeaderRewrite            `json:"header_rewrite,omitempty"`
	URLRewrite    []URLRewrite               `json:"url_rewrite,omitempty"`
	MapRemote     []MapRemote                `json:"map_remote,omitempty"`
	BodyRewrite   []BodyRewrite              `json:"body_rewrite,omitempty"`
	MapLocal      []MapLocal                 `json:"map_local,omitempty"`
	Resources     map[string]RewriteResource `json:"resources,omitempty"`
}

type RewriteMatch struct {
	Name    string `json:"name,omitempty"`
	Pattern string `json:"pattern"`
	Disable bool   `json:"disabled,omitempty"`
}

type HeaderRewrite struct {
	RewriteMatch
	Phase       string `json:"phase"`
	Operation   string `json:"operation"`
	Header      string `json:"header"`
	Value       string `json:"value,omitempty"`
	Regex       string `json:"regex,omitempty"`
	Replacement string `json:"replacement,omitempty"`
}

type URLRewrite struct {
	RewriteMatch
	Action      string `json:"action"`
	Replacement string `json:"replacement,omitempty"`
}

type MapRemote struct {
	RewriteMatch
	Replacement  string `json:"replacement"`
	PreserveHost bool   `json:"preserve_host,omitempty"`
}

type BodyRewrite struct {
	RewriteMatch
	Phase       string `json:"phase"`
	Regex       string `json:"regex"`
	Replacement string `json:"replacement"`
}

type MapLocal struct {
	RewriteMatch
	Status   int      `json:"status,omitempty"`
	Headers  []Header `json:"headers,omitempty"`
	DataType string   `json:"data_type,omitempty"`
	Data     string   `json:"data,omitempty"`
	Resource string   `json:"resource,omitempty"`
}

type RewriteResource struct {
	DataType string `json:"data_type,omitempty"`
	Data     string `json:"data"`
}

func (c *RewriteConfig) UnmarshalJSON(data []byte) error {
	if len(data) > MaxRewriteConfigBytes || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("http_rewrite must be an object of at most 2 MiB")
	}
	type plain RewriteConfig
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*c = RewriteConfig(value)
	_, err := CompileRewrite(c)
	return err
}

type compiledRule[T any] struct {
	options T
	pattern *regexp.Regexp
	regex   *regexp.Regexp
	label   string
}

// RewriteProgram is immutable and can be shared by concurrent HTTP/2 streams.
type RewriteProgram struct {
	scope   Config
	limit   int64
	headers []compiledRule[HeaderRewrite]
	urls    []compiledRule[URLRewrite]
	remote  []compiledRule[MapRemote]
	bodies  []compiledRule[BodyRewrite]
	local   []compiledRule[MapLocal]
	data    [][]byte
	count   int
}

func CompileRewrite(c *RewriteConfig) (*RewriteProgram, error) {
	if c == nil {
		return nil, nil
	}
	if c.MaxBodyBytes == 0 {
		c.MaxBodyBytes = MaxBodyLimit
	}
	if c.MaxBodyBytes < 1 || c.MaxBodyBytes > MaxBodyLimit {
		return nil, errors.New("max_body_bytes must be between 1 and 1048576")
	}
	if len(c.HeaderRewrite)+len(c.URLRewrite)+len(c.MapRemote)+len(c.BodyRewrite)+len(c.MapLocal) > MaxRewriteRules {
		return nil, errors.New("http_rewrite supports at most 256 rules")
	}
	scope := Config{Ports: append([]uint16(nil), c.Ports...), Packages: append([]string(nil), c.Packages...)}
	if err := scope.validate(); err != nil {
		return nil, fmt.Errorf("http_rewrite scope: %w", err)
	}
	p := &RewriteProgram{scope: scope, limit: c.MaxBodyBytes}
	resources := make(map[string][]byte)
	if len(c.Resources) > MaxRewriteRules {
		return nil, errors.New("too many embedded rewrite resources")
	}
	for name, resource := range c.Resources {
		if name == "" || len(name) > 256 {
			return nil, errors.New("invalid rewrite resource name")
		}
		data, err := decodeRewriteData(resource.DataType, resource.Data, c.MaxBodyBytes)
		if err != nil {
			return nil, fmt.Errorf("resource %q: %w", name, err)
		}
		resources[name] = data
	}
	for i, rule := range c.HeaderRewrite {
		item, err := compileRewriteRule(rule, rule.RewriteMatch, "header_rewrite", i)
		if err != nil {
			return nil, err
		}
		if !rewritePhase(rule.Phase) || !httpguts.ValidHeaderFieldName(rule.Header) || protectedRewriteHeader(rule.Header) || !httpguts.ValidHeaderFieldValue(rule.Value) {
			return nil, fmt.Errorf("%s: invalid phase, header, value or message framing header", item.label)
		}
		switch rule.Operation {
		case "add", "delete", "replace":
		case "replace_regex":
			item.regex, err = compileRewritePattern(rule.Regex)
		default:
			err = errors.New("operation must be add, delete, replace or replace_regex")
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", item.label, err)
		}
		if rule.Phase == "request" && strings.EqualFold(rule.Header, "Host") && (rule.Operation == "add" || rule.Operation == "delete") {
			return nil, fmt.Errorf("%s: Host only supports replace and replace_regex", item.label)
		}
		if !rule.Disable {
			p.headers = append(p.headers, item)
		}
	}
	for i, rule := range c.URLRewrite {
		item, err := compileRewriteRule(rule, rule.RewriteMatch, "url_rewrite", i)
		if err != nil {
			return nil, err
		}
		switch rule.Action {
		case "header", "302", "307":
			if err = validateURLTemplate(rule.Replacement); err != nil {
				return nil, fmt.Errorf("%s: %w", item.label, err)
			}
		case "reject":
		default:
			return nil, fmt.Errorf("%s: action must be header, 302, 307 or reject", item.label)
		}
		if !rule.Disable {
			p.urls = append(p.urls, item)
		}
	}
	for i, rule := range c.MapRemote {
		item, err := compileRewriteRule(rule, rule.RewriteMatch, "map_remote", i)
		if err != nil {
			return nil, err
		}
		if err = validateURLTemplate(rule.Replacement); err != nil {
			return nil, fmt.Errorf("%s: %w", item.label, err)
		}
		if !rule.Disable {
			p.remote = append(p.remote, item)
		}
	}
	for i, rule := range c.BodyRewrite {
		item, err := compileRewriteRule(rule, rule.RewriteMatch, "body_rewrite", i)
		if err != nil {
			return nil, err
		}
		if !rewritePhase(rule.Phase) {
			return nil, fmt.Errorf("%s: phase must be request or response", item.label)
		}
		item.regex, err = compileRewritePattern("(?m)" + rule.Regex)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", item.label, err)
		}
		if !rule.Disable {
			p.bodies = append(p.bodies, item)
		}
	}
	for i, rule := range c.MapLocal {
		item, err := compileRewriteRule(rule, rule.RewriteMatch, "map_local", i)
		if err != nil {
			return nil, err
		}
		if rule.Status == 0 {
			item.options.Status = http.StatusOK
		}
		if item.options.Status < 200 || item.options.Status > 599 {
			return nil, fmt.Errorf("%s: status must be between 200 and 599", item.label)
		}
		if _, err = rewriteHeaders(rule.Headers); err != nil {
			return nil, fmt.Errorf("%s: %w", item.label, err)
		}
		var data []byte
		if rule.Resource != "" {
			if rule.Data != "" || rule.DataType != "" {
				return nil, fmt.Errorf("%s: resource cannot be combined with data/data_type", item.label)
			}
			var found bool
			data, found = resources[rule.Resource]
			if !found {
				return nil, fmt.Errorf("%s: unknown embedded resource %q", item.label, rule.Resource)
			}
		} else {
			data, err = decodeRewriteData(rule.DataType, rule.Data, c.MaxBodyBytes)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", item.label, err)
			}
		}
		if !rule.Disable {
			p.local = append(p.local, item)
			p.data = append(p.data, data)
		}
	}
	p.count = len(p.headers) + len(p.urls) + len(p.remote) + len(p.bodies) + len(p.local)
	return p, nil
}

func compileRewriteRule[T any](options T, match RewriteMatch, kind string, index int) (compiledRule[T], error) {
	label := fmt.Sprintf("%s[%d]", kind, index)
	if match.Name != "" {
		label += " " + match.Name
	}
	if len(label) > 256 {
		return compiledRule[T]{}, errors.New("rewrite rule name is too long")
	}
	if match.Pattern == "" {
		return compiledRule[T]{}, fmt.Errorf("%s: URL pattern is required", label)
	}
	pattern, err := compileRewritePattern(match.Pattern)
	if err != nil {
		return compiledRule[T]{}, fmt.Errorf("%s: %w", label, err)
	}
	return compiledRule[T]{options: options, pattern: pattern, label: label}, nil
}

func compileRewritePattern(pattern string) (*regexp.Regexp, error) {
	if len(pattern) > 4096 {
		return nil, errors.New("regular expression exceeds 4096 bytes")
	}
	re, err := regexp.Compile(pattern)
	if err == nil && re.NumSubexp() > 32 {
		return nil, errors.New("regular expression exceeds 32 capture groups")
	}
	return re, err
}

func rewritePhase(phase string) bool { return phase == "request" || phase == "response" }

func protectedRewriteHeader(name string) bool {
	switch strings.ToLower(name) {
	case "content-length", "transfer-encoding", "trailer", "connection", "proxy-connection", "upgrade":
		return true
	}
	return false
}

func rewriteHeaders(values []Header) (http.Header, error) {
	headers := make(http.Header)
	size := 0
	for _, value := range values {
		size += len(value.Name) + len(value.Value)
		if size > MaxHeaderBytes || !httpguts.ValidHeaderFieldName(value.Name) || !httpguts.ValidHeaderFieldValue(value.Value) || protectedRewriteHeader(value.Name) {
			return nil, errors.New("invalid, oversized or framing header in local response")
		}
		headers.Add(value.Name, value.Value)
	}
	return headers, nil
}

func decodeRewriteData(kind, value string, limit int64) ([]byte, error) {
	var data []byte
	var err error
	switch kind {
	case "", "text":
		data = []byte(value)
	case "base64":
		data, err = base64.StdEncoding.DecodeString(value)
	case "tiny-gif":
		data, err = base64.StdEncoding.DecodeString("R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7")
	default:
		return nil, errors.New("data_type must be text, base64 or tiny-gif")
	}
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("embedded data exceeds max_body_bytes")
	}
	return data, nil
}

func validateURLTemplate(value string) error {
	if len(value) > 8192 || strings.ContainsAny(value, "\r\n") || (!strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://")) {
		return errors.New("replacement must be an absolute http:// or https:// URL template, at most 8192 bytes")
	}
	if !strings.Contains(value, "$") {
		_, err := parseRewriteURL(value)
		return err
	}
	return nil
}

func parseRewriteURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil {
		return nil, err
	}
	if len(value) > 8192 || u.Opaque != "" || u.User != nil || u.Fragment != "" || (u.Scheme != "http" && u.Scheme != "https") || !validHost(normalizeHost(u.Hostname())) || strings.ContainsAny(value, "\r\n") {
		return nil, errors.New("rewrite produced an invalid HTTP(S) URL")
	}
	if port := u.Port(); port != "" {
		if _, err = netPort(port); err != nil {
			return nil, err
		}
	}
	return u, nil
}
