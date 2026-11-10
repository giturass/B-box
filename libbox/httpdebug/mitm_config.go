package httpdebug

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
)

// MITMConfig controls TLS interception independently of recording and HTTP
// processing. An empty allowlist never decrypts a connection, even when enabled.
type MITMConfig struct {
	Enabled              bool     `json:"enabled,omitempty"`
	Hosts                []string `json:"hosts,omitempty"`
	Ports                []uint16 `json:"ports,omitempty"`
	Packages             []string `json:"packages,omitempty"`
	TCP                  bool     `json:"tcp,omitempty"`
	H2                   bool     `json:"h2,omitempty"`
	SkipServerCertVerify bool     `json:"skip_server_cert_verify,omitempty"`
	// Keep explicit false when formatting a profile: omission means true.
	BlockQUIC bool `json:"block_quic"`
}

func (c *MITMConfig) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("mitm must be an object")
	}
	type plain MITMConfig
	value := plain{BlockQUIC: true}
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
	for i, host := range c.Hosts {
		rule, err := parseMITMHost(host)
		if err != nil {
			return fmt.Errorf("MITM host %d: %w", i+1, err)
		}
		c.Hosts[i] = rule.String()
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
	if !c.candidate(m) || !validHost(normalizeHost(m.Host)) {
		return false
	}
	for _, pattern := range c.Hosts {
		rule, err := parseMITMHost(pattern)
		if err == nil && rule.matches(m) {
			return !rule.exclude
		}
	}
	return false
}

func (c *MITMConfig) httpProtocols() []string {
	if c != nil && c.H2 {
		return []string{"h2", "http/1.1"}
	}
	return []string{"http/1.1"}
}
