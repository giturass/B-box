package libbox

import (
	"strings"
	"testing"
)

// uiNodeTestWireGuard is a wg-quick file; extra goes into its interface.
func uiNodeTestWireGuard(extra string) string {
	return `[Interface]
# the device
PrivateKey = ` + uiNodeTestKey(1) + `
Address = 10.0.0.2/32, fd00::2
DNS = 1.1.1.1
MTU = 1280
` + extra + `
[Peer]
PublicKey = ` + uiNodeTestKey(2) + `
PresharedKey = ` + uiNodeTestKey(3) + `
AllowedIPs = 0.0.0.0/0
AllowedIPs = ::/0
Endpoint = wg.example.com:51820
PersistentKeepalive = 25
`
}

// The reader is called directly: a build without WireGuard reads the file
// the same way and then leaves the node out.
func TestUINodeParseWireGuardFile(t *testing.T) {
	parsed := uiNodeParseWireGuard(uiNodeTestWireGuard(""))
	if parsed == nil || parsed.format != "wireguard" || len(parsed.nodes) != 1 || len(parsed.skipped) != 0 {
		t.Fatalf("wg-quick file read as %+v", parsed)
	}
	want := `{"type":"wireguard","tag":"wg.example.com:51820","mtu":1280,"address":["10.0.0.2/32","fd00::2/128"],"private_key":"` + uiNodeTestKey(1) +
		`","peers":[{"address":"wg.example.com","port":51820,"public_key":"` + uiNodeTestKey(2) + `","pre_shared_key":"` + uiNodeTestKey(3) +
		`","allowed_ips":["0.0.0.0/0","::/0"],"persistent_keepalive_interval":25}]}`
	if !uiNodeEqual(parsed.nodes[0], uiNodeTestJSON(t, want)) {
		t.Errorf("wg-quick file read as %s, want %s", uiNodeTestText(parsed.nodes[0]), want)
	}
	// The options are ones the core reads, whether or not it carries WireGuard.
	if _, err := parseConfig(baseContext(nil), `{"endpoints":[`+uiNodeTestText(parsed.nodes[0])+`]}`); err != nil {
		t.Errorf("WireGuard endpoint options: %v", err)
	}
	for extra, reason := range map[string]string{
		"Jc = 4\n": "AmneziaWG is not supported by this core",
		"\n[Peer]\nPublicKey = " + uiNodeTestKey(4) + "\nAllowedIPs = 10.0.0.3/32\n": "a peer of this WireGuard file has no endpoint",
	} {
		parsed = uiNodeParseWireGuard(uiNodeTestWireGuard(extra))
		if parsed == nil || len(parsed.nodes) != 0 || len(parsed.skipped) != 1 {
			t.Errorf("wg-quick file to refuse with %q read as %+v", reason, parsed)
		} else if entry, _ := parsed.skipped[0].(*uiNodeObject); entry.text("reason") != reason || entry.text("name") != "" {
			t.Errorf("refused with %s, want %q", uiNodeTestText(entry), reason)
		}
	}
	for _, content := range []string{
		"",
		"[Interface]\nAddress = 10.0.0.2/32\n[Peer]\nEndpoint = wg.example.com:51820\n",
		"[Interface]\nPrivateKey = " + uiNodeTestKey(1) + "\n",
		strings.Replace(uiNodeTestWireGuard(""), "[Peer]", "[Interface]", 1),
	} {
		if parsed = uiNodeParseWireGuard(content); parsed != nil {
			t.Errorf("%q read as a wg-quick file", content)
		}
	}
}
