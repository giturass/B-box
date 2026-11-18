package libbox

import (
	"strings"
	"testing"
)

func TestUINodeShareSelectedConfigNodes(t *testing.T) {
	const config = `{
		"dns": {"servers": []},
		"outbounds": [
			{"type":"socks","tag":"SOCKS","server":"2001:db8::1","server_port":1080,"version":"5","username":"a b","password":"p:@/#+"},
			{"type":"shadowsocks","tag":"加密","server":"ss.example","server_port":8388,"method":"chacha20-ietf-poly1305","password":"p:@/#+"},
			{"type":"vless","tag":"WS","server":"vless.example","server_port":443,"uuid":"` + uiNodeTestUUID + `","packet_encoding":"xudp",
			 "tls":{"enabled":true,"server_name":"cdn.example"},"transport":{"type":"ws","path":"/ws","headers":{"Host":"cdn.example"}}},
			{"type":"vmess","tag":"VMess","server":"vmess.example","server_port":443,"uuid":"` + uiNodeTestUUID + `",
			 "tls":{"enabled":true},"transport":{"type":"grpc","service_name":"svc"}},
			{"type":"trojan","tag":"Pinned node","server":"pinned.example","server_port":443,"password":"pinned-password",
			 "tls":{"enabled":true,"certificate_path":"/private/custom-ca.pem"}},
			{"type":"selector","tag":"Group","outbounds":["SOCKS"]}
		]
	}`
	box, err := ShareUIConfigNodes(config, `["WS","SOCKS","加密","Pinned node","VMess","SOCKS"]`)
	result := uiNodeTestObject(t, uiNodeTestValue(t, "share", box, err))
	wantTags := []string{"WS", "SOCKS", "加密", "Pinned node", "VMess"}
	wantSchemes := []string{"vless://", "socks5://", "ss://", "", "vmess://"}
	nodes := result.list("nodes")
	copied, err := uiNodeList(result.text("json"))
	if err != nil || len(nodes) != len(wantTags) || len(copied) != len(wantTags) {
		t.Fatalf("unexpected shared node count: %d metadata, %d JSON, %v", len(nodes), len(copied), err)
	}
	var links []string
	for i, node := range nodes {
		entry := node.(*uiNodeObject)
		if entry.text("tag") != wantTags[i] || copied[i].text("tag") != wantTags[i] {
			t.Fatalf("the share changed the requested order at position %d", i)
		}
		link := entry.text("link")
		if wantSchemes[i] == "" {
			if link != "" || copied[i].object("tls").text("certificate_path") != "/private/custom-ca.pem" {
				t.Fatal("the fallback lost the certificate setting or exported a URI without it")
			}
			continue
		}
		if !strings.HasPrefix(link, wantSchemes[i]) {
			t.Fatalf("missing %s link for %s", wantSchemes[i], wantTags[i])
		}
		links = append(links, link)
	}
	reimported := uiNodeParseLinks(strings.Join(links, "\n"))
	if reimported == nil || len(reimported.nodes) != len(links) || len(reimported.skipped) != 0 {
		t.Fatal("newline-separated standard links could not be imported")
	}
	if socks := reimported.nodes[1]; socks.text("server") != "2001:db8::1" ||
		socks.text("username") != "a b" || socks.text("password") != "p:@/#+" {
		t.Fatal("the SOCKS link changed the IPv6 address or credentials")
	}
	if vmess := reimported.nodes[3]; vmess.text("security") != "zero" {
		t.Fatal("the VMess link changed the effective cipher")
	}
	for _, tags := range []string{`[]`, `["Missing"]`, `["Group"]`} {
		if box, err := ShareUIConfigNodes(config, tags); err == nil || box != nil {
			t.Fatalf("accepted a request without shareable nodes: %s", tags)
		}
	}
}
