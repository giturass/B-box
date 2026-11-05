package httpdebug

import (
	"errors"
	"net"
	"strconv"
	"strings"
)

// Surge Host Lists use ordered exclusions and glob matching. A bare MITM
// entry uses port 443, except when the name was recovered from TLS/QUIC SNI.
type mitmHost struct {
	pattern      string
	port         uint16
	explicitPort bool
	exclude      bool
}

func parseMITMHost(value string) (mitmHost, error) {
	value = strings.TrimSpace(value)
	rule := mitmHost{port: 443, exclude: strings.HasPrefix(value, "-")}
	if rule.exclude {
		value = strings.TrimPrefix(value, "-")
	}
	if value == "" || strings.ContainsAny(value, " \t\r\n/\\,;@") {
		return rule, errors.New("expected a host pattern with an optional - exclusion and :port")
	}
	var port string
	if strings.HasPrefix(value, "[") {
		end := strings.IndexByte(value, ']')
		if end < 0 || net.ParseIP(value[1:end]) == nil {
			return rule, errors.New("invalid bracketed IP address")
		}
		if suffix := value[end+1:]; suffix != "" {
			if !strings.HasPrefix(suffix, ":") {
				return rule, errors.New("invalid text after IP address")
			}
			port, rule.explicitPort = suffix[1:], true
		}
		value = value[1:end]
	} else if strings.Count(value, ":") == 1 {
		value, port, _ = strings.Cut(value, ":")
		rule.explicitPort = true
	} else if strings.Contains(value, ":") && net.ParseIP(value) == nil {
		return rule, errors.New("IPv6 with a port must use [address]:port")
	}
	if strings.ContainsAny(value, "[]") {
		return rule, errors.New("brackets are only valid around an IP address")
	}
	if rule.explicitPort {
		if port == "" || strings.Trim(port, "0123456789") != "" {
			return rule, errors.New("host port must be between 0 and 65535; 0 matches every port")
		}
		number, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return rule, errors.New("host port must be between 0 and 65535; 0 matches every port")
		}
		rule.port = uint16(number)
	}
	rule.pattern = normalizeHost(value)
	switch rule.pattern {
	case "<ip-address>", "<ipv4-address>", "<ipv6-address>", "<simple-hostname>":
		return rule, nil
	}
	// Replacing the two supported wildcards also rejects bracket expressions,
	// URLs, empty labels, non-ASCII names and malformed IP literals.
	if !validHost(strings.NewReplacer("*", "a", "?", "a").Replace(rule.pattern)) {
		return rule, errors.New("invalid host pattern; use names, IPs, * or ? wildcards, or a Host List token")
	}
	return rule, nil
}

func (r mitmHost) String() string {
	value := r.pattern
	if r.explicitPort {
		if strings.Contains(value, ":") {
			value = "[" + value + "]"
		}
		value += ":" + strconv.Itoa(int(r.port))
	}
	if r.exclude {
		value = "-" + value
	}
	return value
}

func (r mitmHost) matches(m Metadata) bool {
	if r.explicitPort {
		if r.port != 0 && r.port != m.Port {
			return false
		}
	} else if !m.sniffedSNI && m.Port != 443 {
		return false
	}
	host := normalizeHost(m.Host)
	ip := net.ParseIP(host)
	switch r.pattern {
	case "<ip-address>":
		return ip != nil
	case "<ipv4-address>":
		return ip != nil && ip.To4() != nil
	case "<ipv6-address>":
		return ip != nil && ip.To4() == nil
	case "<simple-hostname>":
		return ip == nil && !strings.Contains(host, ".")
	}
	return matchHostGlob(r.pattern, host)
}

// Match only * and ?, without treating dots as separators or accepting regexp
// syntax. The last star is the only backtracking point needed for glob matches.
func matchHostGlob(pattern, host string) bool {
	p, h, star, retry := 0, 0, -1, 0
	for h < len(host) {
		switch {
		case p < len(pattern) && (pattern[p] == '?' || pattern[p] == host[h]):
			p++
			h++
		case p < len(pattern) && pattern[p] == '*':
			star, retry = p, h
			p++
		case star >= 0:
			retry++
			p, h = star+1, retry
		default:
			return false
		}
	}
	return strings.Trim(pattern[p:], "*") == ""
}
