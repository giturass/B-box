// Package httpdebug implements B-box's opt-in HTTP debugging, after sing-box routing.
// It has no listeners or direct network dialer: every upstream connection is supplied
// by the router, with the original destination, outbound and routing options.
package httpdebug

import (
	"errors"
	"net"
	"net/http"
	"slices"
	"strings"
)

const (
	DefaultBodyLimit = 64 * 1024
	MaxBodyLimit     = 1024 * 1024
	MaxEntries       = 300
	MaxStorageBytes  = 32 * 1024 * 1024
	MaxHeaderBytes   = 64 * 1024
	MaxConnections   = 32
)

type Config struct {
	HTTPS        bool     `json:"https"`
	Packages     []string `json:"packages"`
	IncludeHosts []string `json:"includeHosts"`
	ExcludeHosts []string `json:"excludeHosts"`
	DecryptHosts []string `json:"decryptHosts"`
	Ports        []uint16 `json:"ports"`
	BodyLimit    int64    `json:"bodyLimit"`
	BlockQUIC    bool     `json:"blockQUIC"`
}

func (c *Config) validate() error {
	if c.BodyLimit == 0 {
		c.BodyLimit = DefaultBodyLimit
	}
	if c.BodyLimit < 0 || c.BodyLimit > MaxBodyLimit {
		return errors.New("body limit must be between 1 and 1048576 bytes")
	}
	if len(c.Ports) == 0 {
		c.Ports = []uint16{80, 443, 8080, 8443}
	}
	if len(c.Ports) > 32 || slices.Contains(c.Ports, 0) {
		return errors.New("specify up to 32 ports between 1 and 65535")
	}
	if len(c.Packages) > 256 || len(c.IncludeHosts)+len(c.ExcludeHosts)+len(c.DecryptHosts) > 256 {
		return errors.New("too many capture filters")
	}
	for _, hosts := range [][]string{c.IncludeHosts, c.ExcludeHosts, c.DecryptHosts} {
		for i, host := range hosts {
			host = strings.TrimSpace(host)
			wildcard := strings.HasPrefix(host, "*.")
			host = strings.TrimPrefix(host, "*.")
			host = normalizeHost(host)
			if !validHost(host) {
				return errors.New("host filters must be hostnames or IP addresses, without a URL or port")
			}
			if wildcard {
				if net.ParseIP(host) != nil {
					return errors.New("IP addresses cannot have wildcard filters")
				}
				host = "*." + host
			}
			hosts[i] = host
		}
	}
	for _, name := range c.Packages {
		if name == "" || len(name) > 255 || strings.ContainsAny(name, " \t\r\n/\\") {
			return errors.New("invalid application package name")
		}
	}
	return nil
}

func validHost(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

type Metadata struct {
	Host                string   `json:"host"`
	Port                uint16   `json:"port"`
	Protocol            string   `json:"protocol"`
	Source              string   `json:"source"`
	Destination         string   `json:"destination"`
	OriginalDestination string   `json:"originalDestination,omitempty"`
	Inbound             string   `json:"inbound"`
	Outbound            string   `json:"outbound"`
	Rule                string   `json:"rule"`
	UID                 int32    `json:"uid"`
	Packages            []string `json:"packages"`
}

func (c Config) candidate(m Metadata) bool {
	if !slices.Contains(c.Ports, m.Port) {
		return false
	}
	if m.Protocol != "" && m.Protocol != "http" && m.Protocol != "tls" {
		return false
	}
	if m.Protocol == "tls" && !c.HTTPS {
		return false
	}
	if len(c.Packages) == 0 {
		return true
	}
	for _, name := range m.Packages {
		if slices.Contains(c.Packages, name) {
			return true
		}
	}
	return false // An unknown owner never matches an application allowlist.
}

func (c Config) matches(m Metadata) bool {
	return c.candidate(m) && !hostMatches(m.Host, c.ExcludeHosts) &&
		(len(c.IncludeHosts) == 0 || hostMatches(m.Host, c.IncludeHosts)) &&
		(m.Protocol != "tls" || len(c.DecryptHosts) == 0 || hostMatches(m.Host, c.DecryptHosts))
}

func hostMatches(host string, patterns []string) bool {
	host = normalizeHost(host)
	for _, pattern := range patterns {
		if host == pattern || (strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, pattern[1:])) {
			return true
		}
	}
	return false
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
}

type Header struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Limit recorded headers independently of the protocol parser's header limit.
func captureHeaders(h http.Header, host string) ([]Header, bool) {
	result := make([]Header, 0)
	size := 0
	if host != "" {
		result = append(result, Header{"Host", host})
		size += len(host) + 4
	}
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		for _, value := range h[key] {
			size += len(key) + len(value)
			if size > MaxHeaderBytes {
				return result, true
			}
			result = append(result, Header{key, value})
		}
	}
	return result, false
}
