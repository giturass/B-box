package libbox

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

const uiNodeTestUUID = "b831381d-6324-4d53-ad4f-8cda48b30811"

// uiNodeTestKey is a 32-byte key in padded standard base64.
func uiNodeTestKey(fill byte) string {
	return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{fill}, 32))
}

func uiNodeTestJSON(t *testing.T, content string) any {
	t.Helper()
	value, err := uiNodeDecode(content)
	if err != nil {
		t.Fatalf("not JSON: %v\n%s", err, content)
	}
	return value
}

func uiNodeTestObject(t *testing.T, content string) *uiNodeObject {
	t.Helper()
	object, isObject := uiNodeTestJSON(t, content).(*uiNodeObject)
	if !isObject {
		t.Fatalf("not a JSON object: %s", content)
	}
	return object
}

func uiNodeTestText(value any) string {
	content, err := uiNodeEncode(value, false)
	if err != nil {
		return err.Error()
	}
	return content
}

// uiNodeTestNode is a node every build of the core constructs.
func uiNodeTestNode(tag, server string) string {
	return `{"type":"shadowsocks","tag":"` + tag + `","server":"` + server + `","server_port":8388,"method":"aes-256-gcm","password":"password"}`
}

func uiNodeTestVia(tag, server, detour string) string {
	return strings.Replace(uiNodeTestNode(tag, server), `}`, `,"detour":"`+detour+`"}`, 1)
}

func uiNodeTestValue(t *testing.T, name string, result *StringBox, err error) string {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result.Value
}

func uiNodeTestFails(t *testing.T, name string, err error, want string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: no error, want %q", name, want)
	} else if err.Error() != want {
		t.Errorf("%s: %v, want %q", name, err, want)
	}
}

func uiNodeTestTags(list []any) string {
	var tags []string
	for _, item := range list {
		node, _ := item.(*uiNodeObject)
		tags = append(tags, node.text("tag"))
	}
	return strings.Join(tags, ",")
}

func uiNodeTestMembers(root *uiNodeObject, tag string) string {
	var members []string
	for _, item := range uiNodeTestFind(root.list("outbounds"), tag).list("outbounds") {
		member, _ := item.(string)
		members = append(members, member)
	}
	return strings.Join(members, ",")
}

// uiNodeTestSkipped joins the nodes a result left out as name: reason.
func uiNodeTestSkipped(result *uiNodeObject) string {
	var skipped []string
	for _, item := range result.list("skipped") {
		entry, _ := item.(*uiNodeObject)
		skipped = append(skipped, entry.text("name")+": "+entry.text("reason"))
	}
	return strings.Join(skipped, "; ")
}

func uiNodeTestParse(t *testing.T, content string) *uiNodeObject {
	t.Helper()
	result, err := ParseUINodes(content)
	if err != nil {
		t.Fatalf("parse nodes: %v", err)
	}
	return uiNodeTestObject(t, result.Value)
}

// uiNodeTestFind returns the node, outbound or endpoint named tag.
func uiNodeTestFind(list []any, tag string) *uiNodeObject {
	for _, item := range list {
		node, _ := item.(*uiNodeObject)
		if node.text("tag") == tag {
			return node
		}
	}
	return nil
}

type uiNodeTestLink struct{ name, link, node string }

func uiNodeTestLinks(t *testing.T, cases []uiNodeTestLink) {
	t.Helper()
	for _, item := range cases {
		node, err := uiNodeLinkParse(item.link)
		if err != nil {
			t.Errorf("%s: %v", item.name, err)
			continue
		}
		if !uiNodeEqual(node, uiNodeTestJSON(t, item.node)) {
			t.Errorf("%s: read as %s, want %s", item.name, uiNodeTestText(node), item.node)
		}
	}
}

func TestUINodeLinkShadowsocksAndVMess(t *testing.T) {
	vmess := func(fields string) string {
		return "vmess://" + base64.StdEncoding.EncodeToString([]byte(fields))
	}
	uiNodeTestLinks(t, []uiNodeTestLink{
		{
			"SIP002 with base64 user information",
			"ss://" + base64.RawURLEncoding.EncodeToString([]byte("aes-256-gcm:password")) + "@198.51.100.1:8388#SS%20One",
			`{"type":"shadowsocks","tag":"SS One","server":"198.51.100.1","server_port":8388,"method":"aes-256-gcm","password":"password"}`,
		},
		{
			"SIP002 in plain text with a plugin and an IPv6 server",
			"ss://chacha20-ietf-poly1305:pass%2Fword@[2001:db8::1]:443/?plugin=obfs-local%3Bobfs%3Dhttp%3Bobfs-host%3Dexample.com#v6",
			`{"type":"shadowsocks","tag":"v6","server":"2001:db8::1","server_port":443,"method":"chacha20-ietf-poly1305","password":"pass/word","plugin":"obfs-local","plugin_opts":"obfs=http;obfs-host=example.com"}`,
		},
		{
			"Shadowsocks with the whole body in base64",
			"ss://" + base64.StdEncoding.EncodeToString([]byte("aes-128-gcm:secret@192.0.2.10:8388")) + "#Legacy",
			`{"type":"shadowsocks","tag":"Legacy","server":"192.0.2.10","server_port":8388,"method":"aes-128-gcm","password":"secret"}`,
		},
		{
			"Shadowsocks without a name",
			"ss://aes-128-gcm:secret@[2001:db8::2]:8388",
			`{"type":"shadowsocks","tag":"[2001:db8::2]:8388","server":"2001:db8::2","server_port":8388,"method":"aes-128-gcm","password":"secret"}`,
		},
		{
			"v2rayN VMess with gRPC and TLS",
			vmess(`{"v":"2","ps":"VMess One","add":"vmess.example.com","port":"8443","id":"` + uiNodeTestUUID + `","aid":0,"scy":"auto","net":"grpc","type":"gun","host":"","path":"svc","tls":"tls","sni":"sni.example.com","alpn":"h2","fp":""}`),
			`{"type":"vmess","tag":"VMess One","server":"vmess.example.com","server_port":8443,"uuid":"` + uiNodeTestUUID + `","security":"` + uiNodeTestVMessAutoSecurity(t) + `","tls":{"enabled":true,"server_name":"sni.example.com","alpn":["h2"]},"transport":{"type":"grpc","service_name":"svc"}}`,
		},
		{
			"v2rayN VMess with numbers and a WebSocket in clear",
			vmess(`{"v":"2","ps":"VMess WS","add":"203.0.113.5","port":80,"id":"` + uiNodeTestUUID + `","aid":2,"net":"ws","host":"h.example.com","path":"/v","tls":""}`),
			`{"type":"vmess","tag":"VMess WS","server":"203.0.113.5","server_port":80,"uuid":"` + uiNodeTestUUID + `","security":"` + uiNodeTestVMessAutoSecurity(t) + `","alter_id":2,"transport":{"type":"ws","path":"/v","headers":{"Host":"h.example.com"}}}`,
		},
		{
			"VMess in the VLESS notation with HTTP camouflage",
			"vmess://" + uiNodeTestUUID + "@h.example.com:443?encryption=none&security=none&type=tcp&headerType=http&host=a.example.com,b.example.com&path=/one,/two#VM",
			`{"type":"vmess","tag":"VM","server":"h.example.com","server_port":443,"uuid":"` + uiNodeTestUUID + `","security":"none","transport":{"type":"http","method":"GET","host":["a.example.com","b.example.com"],"path":"/one"}}`,
		},
	})
}
