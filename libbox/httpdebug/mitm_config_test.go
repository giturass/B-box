package httpdebug

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMITMHostListOrderWildcardsAndPorts(t *testing.T) {
	for _, test := range []struct {
		name, pattern, host string
		port                uint16
		sni, want           bool
	}{
		{"any", "*", "api.test", 443, false, true},
		{"bare port", "*", "api.test", 8443, false, false},
		{"SNI bare port", "*", "api.test", 8443, true, true},
		{"explicit port", "api.test:8443", "api.test", 8443, false, true},
		{"SNI does not override port", "api.test:443", "api.test", 8443, true, false},
		{"all ports", "api.test:0", "api.test", 8883, false, true},
		{"one character", "api?.test", "api1.test", 443, false, true},
		{"one is not many", "api?.test", "api12.test", 443, false, false},
		{"substring", "*example*", "api.example.test", 443, false, true},
		{"multiple stars", "a*b*c.test", "ab.bcc.test", 443, false, true},
		{"v4", "192.0.2.1:8443", "192.0.2.1", 8443, false, true},
		{"v6 port", "[2001:db8::1]:8443", "2001:db8::1", 8443, false, true},
		{"v6 default", "[2001:db8::1]", "2001:db8::1", 8443, false, false},
		{"ip token", "<ip-address>:0", "2001:db8::1", 8443, false, true},
		{"v4 token", "<ipv4-address>", "192.0.2.1", 443, false, true},
		{"v6 token", "<ipv6-address>", "192.0.2.1", 443, false, false},
		{"simple host", "<simple-hostname>", "printer", 443, false, true},
		{"simple excludes dotted", "<simple-hostname>", "printer.local", 443, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			data, _ := json.Marshal(MITMConfig{Enabled: true, Hosts: []string{test.pattern}})
			config := mitmConfig(t, string(data))
			if got := config.matches(Metadata{Host: test.host, Port: test.port, Protocol: "tls", sniffedSNI: test.sni}); got != test.want {
				t.Fatalf("%s matched %s:%d (SNI=%v): %v", test.pattern, test.host, test.port, test.sni, got)
			}
		})
	}
	config := mitmConfig(t, `{"enabled":true,"hosts":["-PRIVATE.Example.:8443","*.example:0","*"]}`)
	if config.Hosts[0] != "-private.example:8443" {
		t.Fatal("normalization lost the exclusion or port", config.Hosts)
	}
	for _, port := range []uint16{443, 8443} {
		if config.matches(Metadata{Host: "private.example", Port: port, Protocol: "tls", sniffedSNI: true}) != (port == 443) {
			t.Fatal("ordered port exclusion was ignored")
		}
	}
	config.Hosts = []string{"*", "-private.example"}
	if !config.matches(Metadata{Host: "private.example", Port: 443, Protocol: "tls"}) {
		t.Fatal("a later exclusion overrode the first match")
	}
	config.Ports = []uint16{443}
	if config.matches(Metadata{Host: "public.example", Port: 8443, Protocol: "tls", sniffedSNI: true}) {
		t.Fatal("a host wildcard bypassed the independent port filter")
	}
	config.Packages = []string{"com.allowed"}
	if config.matches(Metadata{Host: "public.example", Port: 443, Protocol: "tls"}) {
		t.Fatal("a host wildcard bypassed the application filter")
	}
}

func TestMITMDefaultsAndExplicitQUICOptOutSurviveFormatting(t *testing.T) {
	config := mitmConfig(t, `{"enabled":true,"hosts":["-private.test","*"]}`)
	if config.H2 || config.SkipServerCertVerify || !config.BlockQUIC {
		t.Fatalf("incorrect MITM defaults: %+v", config)
	}
	e, _ := testEngine(t, Config{})
	e.mitm = config
	if !e.RejectQUIC(Metadata{Host: "public.test", Port: 8443}) || e.RejectQUIC(Metadata{Host: "private.test", Port: 8443}) ||
		e.RejectQUIC(Metadata{Host: "", Port: 443}) {
		t.Fatal("default QUIC blocking did not follow the observed SNI Host List")
	}
	config = mitmConfig(t, `{"enabled":true,"hosts":["*"],"h2":true,"skip_server_cert_verify":true,"block_quic":false}`)
	data, err := json.Marshal(config)
	if err != nil || !strings.Contains(string(data), `"block_quic":false`) {
		t.Fatalf("explicit false disappeared: %s %v", data, err)
	}
	config = mitmConfig(t, string(data))
	if !config.H2 || !config.SkipServerCertVerify || config.BlockQUIC {
		t.Fatalf("formatting changed MITM options: %+v", config)
	}
	e.mitm = config
	if e.RejectQUIC(Metadata{Host: "public.test", Port: 443}) {
		t.Fatal("explicit QUIC opt-out was ignored")
	}
}
