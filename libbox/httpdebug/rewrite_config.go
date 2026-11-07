package httpdebug

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/itchyny/gojq"
	"golang.org/x/net/http/httpguts"
)

const (
	MaxRewriteConfigBytes  = 2 * 1024 * 1024
	MaxRewriteRules        = 256
	MaxRewriteRequestBody  = 32 * 1024 * 1024
	MaxRewriteResponseBody = 10 * 1024 * 1024
	MaxLocalResourceBytes  = 15 * 1024 * 1024
)

// RewriteConfig is the B-box experimental.http_rewrite profile extension.
// It is deliberately separate from the device's private capture settings.
// Resources may be embedded or loaded by the active service; validation never reads files,
// downloads code, runs scripts, installs certificates or starts interception.
type RewriteConfig struct {
	Enabled              bool                       `json:"enabled,omitempty"`
	Ports                []uint16                   `json:"ports,omitempty"`
	Packages             []string                   `json:"packages,omitempty"`
	MaxBodyBytes         int64                      `json:"max_body_bytes,omitempty"`
	MaxRequestBodyBytes  int64                      `json:"max_request_body_bytes,omitempty"`
	MaxResponseBodyBytes int64                      `json:"max_response_body_bytes,omitempty"`
	HeaderRewrite        []HeaderRewrite            `json:"header_rewrite,omitempty"`
	URLRewrite           []URLRewrite               `json:"url_rewrite,omitempty"`
	MapRemote            []MapRemote                `json:"map_remote,omitempty"`
	BodyRewrite          []BodyRewrite              `json:"body_rewrite,omitempty"`
	MapLocal             []MapLocal                 `json:"map_local,omitempty"`
	Resources            map[string]RewriteResource `json:"resources,omitempty"`
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
	Phase       string  `json:"phase"`
	Regex       string  `json:"regex,omitempty"`
	Replacement string  `json:"replacement,omitempty"`
	JQ          *string `json:"jq,omitempty"`
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
	DataType       string `json:"data_type,omitempty"`
	Data           string `json:"data"`
	UpdateInterval int64  `json:"update_interval,omitempty"`
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
	pattern *rewriteRegexp
	regex   *rewriteRegexp
	label   string
	jq      *gojq.Code
	jqError error
}

// RewriteProgram is immutable and can be shared by concurrent HTTP/2 streams.
type RewriteProgram struct {
	scope        Config
	limit        int64
	requestLimit int64
	headers      []compiledRule[HeaderRewrite]
	urls         []compiledRule[URLRewrite]
	remote       []compiledRule[MapRemote]
	bodies       []compiledRule[BodyRewrite]
	local        []compiledRule[MapLocal]
	data         [][]byte
	sources      []*processingResource
	count        int
}

func CompileRewrite(c *RewriteConfig) (*RewriteProgram, error) {
	if c == nil {
		return nil, nil
	}
	requestLimit, responseLimit := c.MaxRequestBodyBytes, c.MaxResponseBodyBytes
	if c.MaxBodyBytes < 0 || c.MaxBodyBytes > MaxRewriteResponseBody {
		return nil, errors.New("max_body_bytes must be between 1 and 10485760")
	}
	if requestLimit == 0 {
		requestLimit = c.MaxBodyBytes
	}
	if responseLimit == 0 {
		responseLimit = c.MaxBodyBytes
	}
	if requestLimit == 0 {
		requestLimit = MaxRewriteRequestBody
	}
	if responseLimit == 0 {
		responseLimit = MaxBodyLimit
	}
	if requestLimit < 1 || requestLimit > MaxRewriteRequestBody || responseLimit < 1 || responseLimit > MaxRewriteResponseBody {
		return nil, errors.New("request body limit must be 1..33554432; response body limit must be 1..10485760")
	}
	if len(c.HeaderRewrite)+len(c.URLRewrite)+len(c.MapRemote)+len(c.BodyRewrite)+len(c.MapLocal) > MaxRewriteRules {
		return nil, errors.New("http_rewrite supports at most 256 rules")
	}
	scope := Config{Ports: append([]uint16(nil), c.Ports...), Packages: append([]string(nil), c.Packages...)}
	if err := scope.validate(); err != nil {
		return nil, fmt.Errorf("http_rewrite scope: %w", err)
	}
	p := &RewriteProgram{scope: scope, limit: responseLimit, requestLimit: requestLimit}
	resources := make(map[string][]byte)
	resourceSources := make(map[string]*processingResource)
	if len(c.Resources) > MaxRewriteRules {
		return nil, errors.New("too many rewrite resources")
	}
	for name, resource := range c.Resources {
		if name == "" || len(name) > 256 {
			return nil, errors.New("invalid rewrite resource name")
		}
		if resource.DataType == "file" {
			spec, err := compileProcessingResource(resource.Data, MaxLocalResourceBytes, false, resource.UpdateInterval, false)
			if err != nil {
				return nil, fmt.Errorf("resource %q: %w", name, err)
			}
			resources[name], resourceSources[name] = nil, spec
			continue
		}
		data, err := decodeRewriteData(resource.DataType, resource.Data, MaxLocalResourceBytes)
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
			if err = validateRewriteURLTemplate(rule.Replacement, rule.Action); err != nil {
				return nil, fmt.Errorf("%s: %w", item.label, err)
			}
		case "reject", "reject-200", "reject-dict":
		default:
			return nil, fmt.Errorf("%s: action must be header, 302, 307, reject, reject-200 or reject-dict", item.label)
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
		if rule.JQ != nil {
			if rule.Regex != "" || rule.Replacement != "" {
				return nil, fmt.Errorf("%s: jq cannot be combined with regex/replacement", item.label)
			}
			if len(*rule.JQ) > 4096 {
				return nil, fmt.Errorf("%s: jq expression exceeds 4096 bytes", item.label)
			}
			query, parseErr := gojq.Parse(*rule.JQ)
			if parseErr == nil {
				item.jq, parseErr = gojq.Compile(query)
			}
			// Surge skips invalid jq rules with a journal warning. Unlike the
			// regexp URL selector, jq errors do not invalidate the profile.
			item.jqError = parseErr
		} else {
			item.regex, err = compileRewritePattern("(?m)" + rule.Regex)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", item.label, err)
			}
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
		if item.options.Status < 200 || item.options.Status > 999 {
			return nil, fmt.Errorf("%s: status must be between 200 and 999", item.label)
		}
		if _, err = rewriteHeaders(rule.Headers); err != nil {
			return nil, fmt.Errorf("%s: %w", item.label, err)
		}
		var data []byte
		var source *processingResource
		if rule.Resource != "" {
			if rule.Data != "" || rule.DataType != "" {
				return nil, fmt.Errorf("%s: resource cannot be combined with data/data_type", item.label)
			}
			var found bool
			data, found = resources[rule.Resource]
			source = resourceSources[rule.Resource]
			if !found {
				return nil, fmt.Errorf("%s: unknown resource %q", item.label, rule.Resource)
			}
			item.options.DataType = c.Resources[rule.Resource].DataType
		} else if rule.DataType == "file" {
			source, err = compileProcessingResource(rule.Data, MaxLocalResourceBytes, false, 0, false)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", item.label, err)
			}
		} else {
			data, err = decodeRewriteData(rule.DataType, rule.Data, MaxLocalResourceBytes)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", item.label, err)
			}
		}
		if !rule.Disable {
			p.local = append(p.local, item)
			p.data = append(p.data, data)
			p.sources = append(p.sources, source)
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

// A zero limit leaves embedded script sizes to the enclosing configuration limit.
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
		return nil, errors.New("embedded data_type must be text, base64 or tiny-gif")
	}
	if err != nil {
		return nil, err
	}
	if limit > 0 && int64(len(data)) > limit {
		return nil, errors.New("embedded data exceeds resource size limit")
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

const gatewayPlaceholder = "{{{GATEWAY_ADDRESS}}}"

func validateRewriteURLTemplate(value, action string) error {
	if action == "header" && strings.HasPrefix(value, "ws://") {
		value = "http://" + strings.TrimPrefix(value, "ws://")
	}
	if action == "302" || action == "307" {
		value = strings.ReplaceAll(value, gatewayPlaceholder, "192.0.2.1")
	}
	return validateURLTemplate(value)
}

func (p *RewriteProgram) bodyLimit(phase string) int64 {
	if phase == "request" && p.requestLimit > 0 {
		return p.requestLimit
	}
	return p.limit
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
