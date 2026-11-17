package libbox

import (
	"encoding/base64"
	"strconv"
	"testing"

	"github.com/sagernet/sing-vmess"
)

func uiNodeTestVMessAutoSecurity(t *testing.T) string {
	t.Helper()
	switch vmess.AutoSecurityType() {
	case vmess.SecurityTypeAes128Gcm:
		return "aes-128-gcm"
	case vmess.SecurityTypeChacha20Poly1305:
		return "chacha20-poly1305"
	default:
		t.Fatal("the core selected an unexpected VMess auto cipher")
		return ""
	}
}

func uiNodeTestVMessClash(cipher string, tls bool) string {
	field := ""
	if cipher != "" {
		field = ", cipher: " + cipher
	}
	return "proxies:\n  - {name: VM, type: vmess, server: 198.51.100.1, port: 443, uuid: " +
		uiNodeTestUUID + field + ", tls: " + strconv.FormatBool(tls) +
		", network: grpc, grpc-opts: {grpc-service-name: svc}}\n"
}

func TestUINodeVMessForeignCipher(t *testing.T) {
	auto := uiNodeTestVMessAutoSecurity(t)
	formats := []struct {
		name string
		body func(string, bool) string
	}{
		{"Clash", uiNodeTestVMessClash},
		{"v2rayN", func(cipher string, tls bool) string {
			field, security := "", ""
			if cipher != "" {
				field = `,"scy":"` + cipher + `"`
			}
			if tls {
				security = "tls"
			}
			body := `{"ps":"VM","add":"198.51.100.1","port":443,"id":"` +
				uiNodeTestUUID + `"` + field + `,"net":"grpc","path":"svc","tls":"` + security + `"}`
			return "vmess://" + base64.StdEncoding.EncodeToString([]byte(body))
		}},
		{"URI", func(cipher string, tls bool) string {
			field, security := "", "none"
			if cipher != "" {
				field = "&encryption=" + cipher
			}
			if tls {
				security = "tls"
			}
			return "vmess://" + uiNodeTestUUID + "@198.51.100.1:443?type=grpc&serviceName=svc&security=" +
				security + field + "#VM"
		}},
	}
	for _, format := range formats {
		for _, enabled := range []bool{false, true} {
			for _, item := range []struct{ name, cipher, want string }{
				{"auto", "auto", auto},
				{"uppercase auto", "AUTO", auto},
				{"missing", "", auto},
				{"unknown fallback", "nonesuch", auto},
				{"none", "none", "none"},
				{"zero", "zero", "zero"},
				{"AES-GCM", "aes-128-gcm", "aes-128-gcm"},
				{"ChaCha20", "chacha20-poly1305", "chacha20-poly1305"},
				{"legacy", "aes-128-cfb", "aes-128-cfb"},
			} {
				t.Run(format.name+"/TLS="+strconv.FormatBool(enabled)+"/"+item.name, func(t *testing.T) {
					result := uiNodeTestParse(t, format.body(item.cipher, enabled))
					if len(result.list("nodes")) != 1 || len(result.list("skipped")) != 0 {
						t.Fatalf("VMess node was not imported: %s", uiNodeTestText(result))
					}
					node := uiNodeTestFind(result.list("nodes"), "VM")
					if node == nil {
						t.Fatal("the imported VMess node lost its name")
					}
					if node.text("security") != item.want {
						t.Errorf("security = %q, want %q", node.text("security"), item.want)
					}
					if enabled {
						if !uiNodeEqual(node.get("tls"), uiNodeTestJSON(t, `{"enabled":true}`)) {
							t.Errorf("TLS changed: %s", uiNodeTestText(node.get("tls")))
						}
					} else if node.get("tls") != nil {
						t.Errorf("TLS was enabled for a cleartext node: %s", uiNodeTestText(node.get("tls")))
					}
					if !uiNodeEqual(node.get("transport"), uiNodeTestJSON(t, `{"type":"grpc","service_name":"svc"}`)) {
						t.Errorf("gRPC transport changed: %s", uiNodeTestText(node.get("transport")))
					}
				})
			}
		}
	}
}

func TestUINodeVMessNativeSecurityPreserved(t *testing.T) {
	for _, security := range []string{"", "auto", "none", "zero", "aes-128-gcm", "chacha20-poly1305"} {
		field := ""
		if security != "" {
			field = `,"security":"` + security + `"`
		}
		native := `{"type":"vmess","tag":"VM","server":"198.51.100.1","server_port":443,"uuid":"` +
			uiNodeTestUUID + `"` + field + `,"tls":{"enabled":true}}`
		for _, body := range []string{native, `{"outbounds":[` + native + `]}`} {
			result := uiNodeTestParse(t, body)
			node := uiNodeTestFind(result.list("nodes"), "VM")
			if node == nil || !uiNodeEqual(node, uiNodeTestJSON(t, native)) {
				t.Errorf("native security %q changed: %s", security, uiNodeTestText(result))
			}
		}
	}
}

func TestUINodeSubscriptionVMessAutoCipherRefresh(t *testing.T) {
	old := `{"type":"vmess","tag":"VM","server":"198.51.100.1","server_port":443,"uuid":"` +
		uiNodeTestUUID + `","security":"auto","tls":{"enabled":true}}`
	native := `{"type":"vmess","tag":"Native","server":"198.51.100.2","server_port":443,"uuid":"` +
		uiNodeTestUUID + `","security":"auto","tls":{"enabled":true}}`
	current := `{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["VM","Native"]},` +
		old + `,` + native + `],"route":{"final":"proxy"}}`
	body := uiNodeTestVMessClash("auto", true)
	updated := uiSubscriptionTestApply(t, current, body, `["VM"]`, false)
	root := uiNodeTestObject(t, updated.Config)
	if updated.Tags != `["VM"]` || updated.Skipped != 0 || updated.Warnings != `[]` {
		t.Errorf("refresh changed source ownership/report: %+v", updated)
	}
	if node := uiNodeTestFind(root.list("outbounds"), "VM"); node == nil || node.text("security") != uiNodeTestVMessAutoSecurity(t) {
		t.Errorf("refreshed node did not keep its AEAD cipher: %s", updated.Config)
	}
	if node := uiNodeTestFind(root.list("outbounds"), "Native"); node == nil || !uiNodeEqual(node, uiNodeTestJSON(t, native)) {
		t.Errorf("refresh changed the unrelated native node: %s", updated.Config)
	}
	if err := CheckConfig(updated.Config); err != nil {
		t.Fatalf("refreshed VMess configuration: %v", err)
	}
	repeated := uiSubscriptionTestApply(t, updated.Config, body, updated.Tags, false)
	if repeated.Config != updated.Config || repeated.Tags != updated.Tags {
		t.Error("repeating the source refresh changed the configuration")
	}
}
