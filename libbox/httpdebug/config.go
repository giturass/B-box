// Package httpdebug records routed HTTP streams and opt-in HTTPS MITM sessions.
// Config contains recording filters. MITM, rewriting and scripts are separate
// validated profile extensions with a shared local-service lifecycle.
package httpdebug

import (
	"bytes"
	"encoding/json"
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
	MaxConfigBytes   = 64 * 1024
)

type Config struct {
	Version      int      `json:"version"`
	Packages     []string `json:"packages"`
	Ports        []uint16 `json:"ports"`
	BodyLimit    int64    `json:"bodyLimit"`
	ExtractHosts bool     `json:"extractHosts,omitempty"`
}

func (c *Config) UnmarshalJSON(data []byte) error {
	if len(data) > MaxConfigBytes || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("capture settings must be an object of at most 64 KiB")
	}
	type plain Config
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*c = Config(value)
	return c.validate()
}

func (c *Config) validate() error {
	if c.Version == 0 {
		c.Version = 3
	}
	if c.Version != 3 {
		return errors.New("unsupported capture settings version")
	}
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
	if len(c.Packages) > 256 {
		return errors.New("too many capture filters")
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
	// ConnectionID identifies the original routed client connection, when tracked.
	// Each exchange still has its own ID, including multiplexed HTTP/2 requests.
	ConnectionID        string          `json:"connectionId,omitempty"`
	ProcessingReason    string          `json:"processingReason,omitempty"`
	Host                string          `json:"host"`
	Port                uint16          `json:"port"`
	Protocol            string          `json:"protocol"`
	Source              string          `json:"source"`
	Destination         string          `json:"destination"`
	OriginalDestination string          `json:"originalDestination,omitempty"`
	Inbound             string          `json:"inbound"`
	Outbound            string          `json:"outbound"`
	Rule                string          `json:"rule"`
	UID                 int32           `json:"uid"`
	Packages            []string        `json:"packages"`
	CaptureSource       string          `json:"captureSource,omitempty"`
	PID                 uint32          `json:"pid,omitempty"`
	CaptureID           string          `json:"captureId,omitempty"`
	TLSAdapter          string          `json:"tlsAdapter,omitempty"`
	Socket              *SocketMetadata `json:"socket,omitempty"`
	RawTCP              bool            `json:"rawTCP,omitempty"`
	TLS                 *TLSMetadata    `json:"tls,omitempty"`
}

// Versions and ALPN are the negotiated values on each side, not estimates.
type TLSMetadata struct {
	ServerName          string `json:"serverName"`
	ClientVersion       string `json:"clientVersion"`
	UpstreamVersion     string `json:"upstreamVersion"`
	ALPN                string `json:"alpn"`
	UpstreamCertificate string `json:"upstreamCertificate"`
	UpstreamServerName  string `json:"upstreamServerName,omitempty"`
}

type SocketMetadata struct {
	FD          int32  `json:"fd"`
	Inode       string `json:"inode"`
	NetNS       uint32 `json:"netns"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

func (c Config) candidate(m Metadata) bool {
	if !slices.Contains(c.Ports, m.Port) {
		return false
	}
	if m.Protocol != "" && m.Protocol != "http" && m.Protocol != "tls" {
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

func (c Config) matches(m Metadata) bool { return c.candidate(m) }

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
