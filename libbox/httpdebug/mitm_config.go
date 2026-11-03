package httpdebug

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
)

// MITMConfig controls TLS interception independently of recording and HTTP
// processing. An empty allowlist never decrypts a connection, even when enabled.
type MITMConfig struct {
	Enabled   bool     `json:"enabled,omitempty"`
	Hosts     []string `json:"hosts,omitempty"`
	Ports     []uint16 `json:"ports,omitempty"`
	Packages  []string `json:"packages,omitempty"`
	TCP       bool     `json:"tcp,omitempty"`
	BlockQUIC bool     `json:"block_quic,omitempty"`
}

func (c *MITMConfig) UnmarshalJSON(data []byte) error {
	if len(data) > MaxConfigBytes || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("mitm must be an object of at most 64 KiB")
	}
	type plain MITMConfig
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*c = MITMConfig(value)
	return c.validate()
}

func (c *MITMConfig) validate() error {
	// Unlike HTTP recording/rewrite defaults, MITM may serve TLS on any port.
	// Only explicitly configured names are eligible on those ports.
	scope := Config{Ports: slices.Clone(c.Ports), Packages: slices.Clone(c.Packages)}
	if err := scope.validate(); err != nil {
		return err
	}
	if len(c.Hosts) > 256 {
		return errors.New("mitm supports at most 256 hosts")
	}
	for i, host := range c.Hosts {
		host = strings.TrimSpace(host)
		wildcard := strings.HasPrefix(host, "*.")
		host = normalizeHost(strings.TrimPrefix(host, "*."))
		if !validHost(host) || wildcard && net.ParseIP(host) != nil {
			return errors.New("MITM hosts must be exact names, IP addresses or *.example.com, without a URL or port")
		}
		if wildcard {
			host = "*." + host
		}
		c.Hosts[i] = host
	}
	return nil
}

func (c *MITMConfig) active() bool { return c != nil && c.Enabled && len(c.Hosts) > 0 }

func (c *MITMConfig) candidate(m Metadata) bool {
	if !c.active() || m.Protocol != "" && m.Protocol != "tls" {
		return false
	}
	if len(c.Ports) > 0 && !slices.Contains(c.Ports, m.Port) {
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
	return false
}

func (c *MITMConfig) matches(m Metadata) bool {
	return c.candidate(m) && validHost(normalizeHost(m.Host)) && hostMatches(m.Host, c.Hosts)
}
