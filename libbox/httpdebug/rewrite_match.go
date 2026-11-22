package httpdebug

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/sagernet/sing-box/schema"
	"golang.org/x/net/http/httpguts"
)

// HTTPMatch is a declarative condition on one original HTTP exchange. A node
// contains an all/any group or a single equals/regex comparison. Conditions
// never read bodies, execute code or supply URL replacement captures.
type HTTPMatch struct {
	All     []HTTPMatch     `json:"all,omitempty"`
	Any     []HTTPMatch     `json:"any,omitempty"`
	Subject string          `json:"subject,omitempty"`
	Header  string          `json:"header,omitempty"`
	Equals  json.RawMessage `json:"equals,omitempty"`
	Regex   *string         `json:"regex,omitempty"`
}

// A pointer's UnmarshalJSON is bypassed for null. Reject an explicitly null
// optional match rather than silently removing conditions from a valid pattern.
func validateHTTPMatchObjects(data []byte, groups ...string) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(data, &root); err != nil {
		return err
	}
	for _, group := range groups {
		raw, found := root[group]
		if !found {
			continue
		}
		var rules []map[string]json.RawMessage
		if err := json.Unmarshal(raw, &rules); err != nil {
			return err
		}
		for i, rule := range rules {
			if match, found := rule["match"]; found && bytes.Equal(bytes.TrimSpace(match), []byte("null")) {
				return fmt.Errorf("%s[%d]: match must be an object", group, i)
			}
		}
	}
	return nil
}

func (m *HTTPMatch) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("match condition must be an object")
	}
	type plain HTTPMatch
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"all", "any", "subject", "header", "equals", "regex"} {
		if raw, found := fields[key]; found && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("match %s must not be null", key)
		}
	}
	_, all := fields["all"]
	_, any := fields["any"]
	if (all || any) && len(fields) != 1 {
		return errors.New("match group must contain only all or any")
	}
	if _, header := fields["header"]; header && value.Subject != "request.header" && value.Subject != "response.header" {
		return errors.New("match header requires a request.header or response.header subject")
	}
	*m = HTTPMatch(value)
	return nil
}

// Describe the recursive union explicitly: RawMessage holds a string or an
// integer depending on the subject, and is not a byte array in the JSON schema.
func (HTTPMatch) DescribeSchema(builder schema.Builder) (*schema.Node, error) {
	return builder.Define("HTTPMatch", func() (*schema.Node, error) {
		var variants []*schema.Node
		for _, group := range []string{"all", "any"} {
			node := schema.StrictObject()
			node.Properties.Put(group, &schema.Node{Type: "array", Items: schema.RefNode("HTTPMatch")})
			node.Required = []string{group}
			variants = append(variants, node)
		}
		for _, subject := range []string{"url", "request.method", "request.header", "response.status", "response.header"} {
			for _, comparison := range []string{"equals", "regex"} {
				node := schema.StrictObject()
				node.Properties.Put("subject", schema.StringConst(subject))
				node.Required = []string{"subject", comparison}
				if strings.HasSuffix(subject, ".header") {
					node.Properties.Put("header", schema.StringNode())
					node.Required = append(node.Required, "header")
				}
				value := schema.StringNode()
				if subject == "response.status" && comparison == "equals" {
					minimum, maximum := int64(100), uint64(999)
					value = &schema.Node{Type: "integer", Minimum: &minimum, Maximum: &maximum}
				}
				node.Properties.Put(comparison, value)
				variants = append(variants, node)
			}
		}
		return schema.OneOf(variants...), nil
	})
}

type compiledHTTPMatch struct {
	children []*compiledHTTPMatch
	any      bool
	subject  string
	header   string
	equals   string
	regex    *rewriteRegexp
}

func compileHTTPMatch(m *HTTPMatch, phase string) (*compiledHTTPMatch, error) {
	if m == nil {
		return nil, nil
	}
	group := m.All != nil || m.Any != nil
	if group {
		if m.All != nil && m.Any != nil || m.Subject != "" || m.Header != "" || m.Equals != nil || m.Regex != nil {
			return nil, errors.New("group must contain only all or any")
		}
		children, name := m.All, "all"
		if m.Any != nil {
			children, name = m.Any, "any"
		}
		if len(children) == 0 {
			return nil, fmt.Errorf("%s must not be empty", name)
		}
		result := &compiledHTTPMatch{any: m.Any != nil}
		for i := range children {
			child, err := compileHTTPMatch(&children[i], phase)
			if err != nil {
				return nil, fmt.Errorf("%s[%d]: %w", name, i, err)
			}
			result.children = append(result.children, child)
		}
		return result, nil
	}
	switch m.Subject {
	case "url", "request.method", "request.header":
	case "response.status", "response.header":
		if phase != "response" {
			return nil, errors.New("response conditions require a response rule")
		}
	default:
		return nil, errors.New("subject must be url, request.method, request.header, response.status or response.header")
	}
	if strings.HasSuffix(m.Subject, ".header") {
		if !httpguts.ValidHeaderFieldName(m.Header) {
			return nil, errors.New("header subject requires a valid header name")
		}
	} else if m.Header != "" {
		return nil, errors.New("header requires a header subject")
	}
	if (m.Equals != nil) == (m.Regex != nil) {
		return nil, errors.New("condition requires exactly one of equals or regex")
	}
	result := &compiledHTTPMatch{subject: m.Subject, header: http.CanonicalHeaderKey(m.Header)}
	if m.Regex != nil {
		var err error
		result.regex, err = compileRewritePattern(*m.Regex)
		if err != nil {
			return nil, fmt.Errorf("regex: %w", err)
		}
	} else if m.Subject == "response.status" {
		var status int
		if err := json.Unmarshal(m.Equals, &status); err != nil || status < 100 || status > 999 {
			return nil, errors.New("response.status equals must be an integer between 100 and 999")
		}
		result.equals = strconv.Itoa(status)
	} else {
		if bytes.Equal(bytes.TrimSpace(m.Equals), []byte("null")) {
			return nil, errors.New("equals must be a string")
		}
		if err := json.Unmarshal(m.Equals, &result.equals); err != nil {
			return nil, errors.New("equals must be a string")
		}
	}
	return result, nil
}

type httpMatchMessage struct {
	method          string
	requestHeaders  http.Header
	status          string
	responseHeaders http.Header
}

// Canonicalize before copying so differently cased keys and repeated values
// have the same semantics as parsed HTTP headers. Preserve present empty values.
func snapshotMatchHeaders(header http.Header) http.Header {
	result := make(http.Header, len(header))
	for name, values := range header {
		key := http.CanonicalHeaderKey(name)
		result[key] = append(result[key], values...)
	}
	return result
}

func (m *compiledHTTPMatch) matches(address string, message *httpMatchMessage, scope *regexContext) (bool, error) {
	if scope != nil && scope.ctx.Err() != nil {
		return false, scope.ctx.Err()
	}
	if len(m.children) > 0 {
		for _, child := range m.children {
			matched, err := child.matches(address, message, scope)
			if err != nil {
				return false, err
			}
			if matched == m.any {
				return matched, nil
			}
		}
		return !m.any, nil
	}
	var values []string
	switch m.subject {
	case "url":
		values = []string{address}
	case "request.method":
		values = []string{message.method}
	case "request.header":
		values = message.requestHeaders.Values(m.header)
	case "response.status":
		if message.status != "" {
			values = []string{message.status}
		}
	case "response.header":
		values = message.responseHeaders.Values(m.header)
	}
	for _, value := range values {
		if m.regex == nil {
			if value == m.equals {
				return true, nil
			}
			continue
		}
		matched, err := m.regex.match(value, scope)
		if err != nil || matched {
			return matched, err
		}
	}
	return false, nil
}
