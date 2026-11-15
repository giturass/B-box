package libbox

import (
	"strings"
	"testing"
)

const uiNodeTestClash = `mixed-port: 7890
proxies:
  - name: One
    type: ss
    server: 198.51.100.1
    port: 8388
    cipher: AES-256-GCM
    password: password
    udp-over-tcp: true
  - {name: Old, type: ssr, server: 198.51.100.2, port: 1, cipher: x, password: SECRET}
  - name: Web
    type: vless
    server: vless.example.com
    port: 443
    uuid: ` + uiNodeTestUUID + `
    tls: true
    servername: sni.example.com
    network: ws
    ws-opts:
      path: /ws
      headers:
        Host: cdn.example.com
        User-Agent: test
    smux:
      enabled: true
      protocol: h2mux
  - name: Chained
    type: trojan
    server: trojan.example.com
    port: 443
    password: password
    dialer-proxy: Missing
  - name: Through
    type: socks5
    server: 198.51.100.4
    port: 1080
    dialer_proxy: One
  - name: Built-in
    type: direct
proxy-groups:
  - name: Proxy
    type: select
    proxies: [One]
`

const uiNodeTestProviders = `proxy-providers:
  remote:
    type: http
    url: https://example.com/subscription
`

func TestUINodeParseClash(t *testing.T) {
	const skipped = "Old: ShadowsocksR was removed from the core; " +
		"Chained: this node connects through another that is not among the nodes read"
	result := uiNodeTestParse(t, uiNodeTestClash)
	uiNodeTestResult(t, "document", result, "clash", "One,Web,Through", skipped, "")
	nodes := result.list("nodes")
	for _, item := range []struct{ tag, path, want string }{
		{"One", "method", `"aes-256-gcm"`},
		{"One", "udp_over_tcp", `{"enabled":true,"version":1}`},
		{"Web", "tls", `{"enabled":true,"server_name":"sni.example.com"}`},
		{"Web", "transport", `{"type":"ws","path":"/ws","headers":{"Host":"cdn.example.com","User-Agent":"test"}}`},
		{"Web", "multiplex", `{"enabled":true,"protocol":"h2mux"}`},
		{"Through", "detour", `"One"`},
	} {
		node := uiNodeTestFind(nodes, item.tag)
		if !uiNodeEqual(node.get(item.path), uiNodeTestJSON(t, item.want)) {
			t.Errorf("%s of %s is %s, want %s", item.path, item.tag, uiNodeTestText(node.get(item.path)), item.want)
		}
	}
	result = uiNodeTestParse(t, uiNodeTestClash+uiNodeTestProviders)
	uiNodeTestResult(t, "document with providers", result, "clash", "One,Web,Through", skipped, "providers")
	result = uiNodeTestParse(t, uiNodeTestProviders)
	uiNodeTestResult(t, "providers alone", result, "clash", "", "", "providers")
	result = uiNodeTestParse(t, `{"proxies":[{"name":"J","type":"ss","server":"198.51.100.1","port":"8388","cipher":"aes-256-gcm","password":"password"}]}`)
	uiNodeTestResult(t, "JSON", result, "clash", "J", "", "")
	result = uiNodeTestParse(t, `{proxies: [{name: F, type: ss, server: 198.51.100.1, port: 8388, cipher: aes-256-gcm, password: password}]}`)
	uiNodeTestResult(t, "YAML in braces", result, "clash", "F", "", "")
	if strings.Contains(uiNodeTestText(uiNodeTestParse(t, uiNodeTestClash)), "SECRET") {
		t.Error("a result repeats a node that was left out")
	}
}

func TestUINodeParseJSONDocuments(t *testing.T) {
	const node = `{"type":"shadowsocks","tag":"Box","server":"box.example.com","server_port":8388,"method":"aes-256-gcm","password":"password","domain_resolver":"local"}`
	result := uiNodeTestParse(t, `{
  // Only the nodes of a configuration are read.
  "dns": {"servers": [{"type": "local", "tag": "local"}]},
  "inbounds": [{"type": "mixed", "tag": "in"}],
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "selector", "tag": "proxy", "outbounds": ["Box"]},
    `+node+`,
    {"type": "nonesuch", "tag": "Odd"},
  ],
}`)
	uiNodeTestResult(t, "configuration", result, "sing-box", "Box", "Odd: this protocol is not supported by this core", "config")
	box := uiNodeTestFind(result.list("nodes"), "Box")
	if box.get("domain_resolver") != nil || box.text("server") != "box.example.com" {
		t.Errorf("node of a configuration read as %s", uiNodeTestText(box))
	}
	result = uiNodeTestParse(t, `{"outbounds":[`+node+`]}`)
	uiNodeTestResult(t, "outbounds", result, "sing-box", "Box", "", "")
	result = uiNodeTestParse(t, node)
	uiNodeTestResult(t, "one outbound", result, "sing-box", "Box", "", "")
	result = uiNodeTestParse(t, `[`+node+`,{"type":"trojan","server":"198.51.100.9","server_port":443,"password":"password","tls":{"enabled":true}}]`)
	uiNodeTestResult(t, "list of outbounds", result, "sing-box", "Box,198.51.100.9:443", "", "")
	result = uiNodeTestParse(t, `{"version":1,"servers":[
		{"id":"1","remarks":"S8","server":"198.51.100.3","server_port":8388,"password":"password","method":"AES-256-GCM"},
		{"id":"2","remarks":"Plugin","server":"198.51.100.3","server_port":"8389","password":"SECRET","method":"aes-256-gcm","plugin":"shadow-tls"}
	],"bytes_used":1}`)
	uiNodeTestResult(t, "SIP008", result, "sip008", "S8", "Plugin: the Shadowsocks plugin of this node is not supported by this core", "")
	if server := uiNodeTestFind(result.list("nodes"), "S8"); server.text("method") != "aes-256-gcm" || server.text("type") != "shadowsocks" {
		t.Errorf("SIP008 server read as %s", uiNodeTestText(server))
	}
}
