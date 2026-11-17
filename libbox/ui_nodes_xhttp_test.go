package libbox

import (
	"bytes"
	"encoding/base64"
	"net/url"
	"strings"
	"testing"
)

func uiNodeXHTTPTestEncryption() string {
	return "mlkem768x25519plus.native.0rtt." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x71}, 32))
}

func TestUINodeXHTTPShareLinkFields(t *testing.T) {
	extra := `{"host":"ignored.example","path":"/ignored","mode":"packet-up","headers":{"Host":"fallback.example","User-Agent":"Test + agent"},"xPaddingBytes":"100-300","noGRPCHeader":true,"sessionIDPlacement":"header","sessionIDKey":"Session","xmux":{"maxConnections":"2-3","hKeepAlivePeriod":-1}}`
	transport := `{"type":"xhttp","host":["a.example","b.example"],"path":"/outer?token=1","mode":"stream-up","headers":{"User-Agent":"Test + agent"},"x_padding_bytes":"100-300","no_grpc_header":true,"session_placement":"header","session_key":"Session","xmux":{"max_connections":"2-3","h_keep_alive_period":-1}}`
	for _, scheme := range []string{"vless", "vmess", "trojan"} {
		for _, kind := range []string{"xhttp", "splithttp"} {
			credential := uiNodeTestUUID
			if scheme == "trojan" {
				credential = "password"
			}
			link := scheme + "://" + credential + "@proxy.example:443?security=none&type=" + kind +
				"&host=a.example,b.example&path=" + url.PathEscape("/outer?token=1") + "&mode=stream-up&extra=" + url.PathEscape(extra) + "#Node"
			node, err := uiNodeLinkParse(link)
			if err != nil {
				t.Fatalf("%s/%s: %v", scheme, kind, err)
			}
			if !uiNodeEqual(node.get("transport"), uiNodeTestJSON(t, transport)) {
				t.Errorf("%s/%s transport: %s", scheme, kind, uiNodeTestText(node.get("transport")))
			}
		}
	}
	vmess := `{"ps":"VMess XHTTP","add":"proxy.example","port":443,"id":"` + uiNodeTestUUID + `","scy":"auto","net":"xhttp","host":"a.example","path":"/vmess","mode":"packet-up","extra":{"no_sse_header":true}}`
	node, err := uiNodeLinkParse("vmess://" + base64.StdEncoding.EncodeToString([]byte(vmess)))
	if err != nil || !uiNodeEqual(node.get("transport"), uiNodeTestJSON(t, `{"type":"xhttp","host":["a.example"],"path":"/vmess","mode":"packet-up","no_sse_header":true}`)) {
		t.Fatalf("VMess JSON XHTTP settings: %s, error=%v", uiNodeTestText(node), err)
	}
	node, err = uiNodeLinkParse("vless://" + uiNodeTestUUID + "@proxy.example:443?type=xhttp&security=tls&extra=" + url.PathEscape(`{"host":"ignored.example","path":"/ignored","mode":"packet-up","headers":{"Host":"fallback.example"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if options := node.object("transport"); options.get("path") != nil || options.get("mode") != nil ||
		!uiNodeEqual(options.get("host"), uiNodeTestJSON(t, `["fallback.example"]`)) {
		t.Errorf("extra replaced the outer share-link fields: %s", uiNodeTestText(options))
	}
}

func TestUINodeXHTTPShareDownloadSettings(t *testing.T) {
	extra := `{"downloadSettings":{"address":"download.example","port":8443,"network":"splithttp","security":"tls","tlsSettings":{"serverName":"sni.example","alpn":["h2"]},"xhttpSettings":{"host":"cdn.example","path":"/down?query=1","mode":"stream-one","noSSEHeader":true,"xmux":{"maxConnections":2}}}}`
	node, err := uiNodeLinkParse("vless://" + uiNodeTestUUID + "@upload.example:443?type=xhttp&host=upload-cdn.example&path=/up&extra=" + url.PathEscape(extra))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"host":["cdn.example"],"path":"/down?query=1","mode":"stream-one","no_sse_header":true,"xmux":{"max_connections":2},"server":"download.example","server_port":8443,"tls":{"enabled":true,"server_name":"sni.example","alpn":["h2"]}}`
	if download := node.object("transport").get("download_settings"); !uiNodeEqual(download, uiNodeTestJSON(t, want)) {
		t.Errorf("Xray download changed its independent settings: %s", uiNodeTestText(download))
	}
	native := `{"download_settings":{"server":"native.example","server_port":9443,"path":"/native","tls":{"enabled":true,"server_name":"native-sni.example"},"bind_interface":"test0","xmux":{"max_connections":2}}}`
	node, err = uiNodeLinkParse("vless://" + uiNodeTestUUID + "@upload.example:443?type=xhttp&host=upload-cdn.example&path=/up&extra=" + url.PathEscape(native))
	if err != nil {
		t.Fatal(err)
	}
	download := node.object("transport").object("download_settings")
	if download.text("server") != "native.example" || download.text("bind_interface") != "test0" || download.get("host") != nil || download.text("path") != "/native" {
		t.Errorf("native download inherited upload fields or lost dialer settings: %s", uiNodeTestText(download))
	}
	for _, item := range []struct{ extra, want string }{
		{`{"downloadSettings":{"port":8443}}`, `{"server_port":8443}`},
		{`{"download_settings":{"server_port":8443}}`, `{"server_port":8443}`},
		{`{"downloadSettings":{}}`, `{}`},
	} {
		node, err := uiNodeLinkParse("vless://" + uiNodeTestUUID + "@upload.example:443?type=xhttp&path=/up&extra=" + url.PathEscape(item.extra))
		if err != nil {
			t.Fatalf("download with inherited address: %s, error=%v", item.extra, err)
		}
		if download := node.object("transport").get("download_settings"); !uiNodeEqual(download, uiNodeTestJSON(t, item.want)) {
			t.Errorf("download port changed or inherited XHTTP upload fields: %s", uiNodeTestText(download))
		}
		if err := CheckConfig(uiNodeTestText(uiNodeOf("outbounds", []any{node}))); err != nil {
			t.Errorf("download with inherited address failed core validation: %s, error=%v", item.extra, err)
		}
	}
}

func TestUINodeXHTTPClashDownloadInheritance(t *testing.T) {
	proxy := uiNodeTestObject(t, `{"name":"Clash XHTTP","type":"vless","server":"upload.example","port":443,"uuid":"`+uiNodeTestUUID+`","tls":true,"network":"xhttp","encryption":"`+uiNodeXHTTPTestEncryption()+`","xhttp_opts":{"host":"upload-cdn.example","path":"/up","mode":"stream-up","headers":{"User-Agent":"upload"},"xPaddingBytes":"100-200","reuse_settings":{"maxConnections":3},"download_settings":{"server":"download.example","port":8443,"headers":{"Host":"down-cdn.example","Accept":"test"},"reuse-settings":{"max-connections":2},"alpn":["h2"]}}}`)
	node, err := uiNodeClashProxy(proxy)
	if err != nil {
		t.Fatal(err)
	}
	if node.text("encryption") != uiNodeXHTTPTestEncryption() {
		t.Error("Clash encryption changed case-sensitive public keys")
	}
	want := `{"host":["down-cdn.example"],"path":"/up","mode":"stream-up","headers":{"Accept":"test"},"x_padding_bytes":"100-200","xmux":{"max_connections":2},"server":"download.example","server_port":8443,"tls":{"enabled":true,"server_name":"download.example","alpn":["h2"]}}`
	if download := node.object("transport").get("download_settings"); !uiNodeEqual(download, uiNodeTestJSON(t, want)) {
		t.Errorf("Clash download inheritance/overrides: %s", uiNodeTestText(download))
	}
	proxy = uiNodeTestObject(t, `{"name":"Clear","type":"vless","server":"upload.example","port":443,"uuid":"`+uiNodeTestUUID+`","tls":true,"network":"splithttp","splithttp-opts":{"host":"upload-cdn.example","path":"/up","download-settings":{"host":"","path":"","tls":false,"headers":{"Host":"ignored.example"}}}}`)
	node, err = uiNodeClashProxy(proxy)
	if err != nil {
		t.Fatal(err)
	}
	download := node.object("transport").object("download_settings")
	if len(download.list("host")) != 0 || download.text("path") != "" || download.get("tls") != nil {
		t.Errorf("explicit empty/disabled download overrides were lost: %s", uiNodeTestText(download))
	}
	proxy = uiNodeTestObject(t, `{"name":"SNI override","type":"vless","server":"upload.example","port":443,"uuid":"`+uiNodeTestUUID+`","tls":true,"servername":"upload-sni.example","network":"xhttp","xhttp-opts":{"download-settings":{"sni":"download-sni.example"}}}`)
	node, err = uiNodeClashProxy(proxy)
	if err != nil || node.object("transport").object("download_settings").object("tls").text("server_name") != "download-sni.example" {
		t.Errorf("download SNI did not replace the parent's other spelling: %s, error=%v", uiNodeTestText(node), err)
	}
}

func TestUINodeXHTTPRejectsUnknownAndMalformedSettings(t *testing.T) {
	for _, extra := range []string{
		`{"unknown":"SECRET"}`, `{"xmux":{"unknown":"SECRET"}}`,
		`{"downloadSettings":{"network":"ws","address":"SECRET"}}`,
		`{"downloadSettings":{"network":"xhttp","sockopt":{"mark":"SECRET"}}}`,
		`{"downloadSettings":{"network":"xhttp","xhttpSettings":{"downloadSettings":{}}}}`,
	} {
		_, err := uiNodeLinkParse("vless://" + uiNodeTestUUID + "@proxy.example:443?type=xhttp&extra=" + url.PathEscape(extra))
		if err == nil || err.Error() != uiNodeXHTTPUnsupported || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("unsupported extra was accepted or leaked its value: %s, error=%v", extra, err)
		}
	}
	for _, extra := range []string{
		`{SECRET`, `[]`, `{"xmux":"SECRET"}`, `{"headers":{"Test":23}}`,
		`{"xPaddingBytes":100,"x_padding_bytes":200}`, `{"downloadSettings":"SECRET"}`,
		`{"downloadSettings":{"network":true}}`,
		`{"downloadSettings":{"network":"xhttp","security":"tls","tlsSettings":{"allowInsecure":"SECRET"}}}`,
	} {
		_, err := uiNodeLinkParse("vless://" + uiNodeTestUUID + "@proxy.example:443?type=xhttp&extra=" + url.PathEscape(extra))
		if err == nil || err.Error() == uiNodeXHTTPUnsupported || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("malformed extra was accepted, ignored or leaked its value: %s, error=%v", extra, err)
		}
	}
	for _, options := range []string{`{"unknown":"SECRET"}`, `{"download-settings":{"unknown":"SECRET"}}`} {
		proxy := uiNodeTestObject(t, `{"name":"Refused","type":"vless","server":"proxy.example","port":443,"uuid":"`+uiNodeTestUUID+`","network":"xhttp","xhttp-opts":`+options+`}`)
		if _, err := uiNodeClashProxy(proxy); err == nil || err.Error() != uiNodeXHTTPUnsupported || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("unsupported Clash settings were discarded: %s, error=%v", options, err)
		}
	}
	for _, options := range []string{`{"download-settings":{"tls":"SECRET"}}`, `{"download-settings":{"alpn":42}}`} {
		proxy := uiNodeTestObject(t, `{"name":"Refused","type":"vless","server":"proxy.example","port":443,"uuid":"`+uiNodeTestUUID+`","network":"xhttp","xhttp-opts":`+options+`}`)
		if _, err := uiNodeClashProxy(proxy); err == nil || err.Error() == uiNodeXHTTPUnsupported || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("malformed Clash download TLS was discarded: %s, error=%v", options, err)
		}
	}
}

func TestUINodeXHTTPEncryptionConfigAndSubscription(t *testing.T) {
	encryption := uiNodeXHTTPTestEncryption()
	link := "vless://" + uiNodeTestUUID + "@proxy.example:443?encryption=" + encryption + "&type=xhttp&mode=packet-up&host=cdn.example&path=/encrypted#Encrypted"
	for _, content := range []string{link, base64.StdEncoding.EncodeToString([]byte(link)),
		`{"dns":{"servers":[{"type":"local","tag":"ignored-source-dns"}]},"outbounds":[{"type":"vless","tag":"Encrypted","server":"proxy.example","server_port":443,"uuid":"` + uiNodeTestUUID + `","encryption":"` + encryption + `","domain_resolver":"ignored-source-dns","transport":{"type":"xhttp","host":["cdn.example"],"path":"/encrypted","mode":"packet-up","download_settings":{"path":"/down","domain_resolver":"ignored-source-dns"}}}]}`} {
		parsed := uiNodeTestParse(t, content)
		if len(parsed.list("nodes")) != 1 || len(parsed.list("skipped")) != 0 || uiNodeTestFind(parsed.list("nodes"), "Encrypted").text("encryption") != encryption {
			t.Fatalf("supported XHTTP/encryption node was skipped or changed: %s", uiNodeTestText(parsed))
		}
		updated := uiSubscriptionTestApply(t, uiNodeDefaultConfig, content, `[]`, true)
		if updated.Added != 1 || updated.Skipped != 0 || updated.Warnings != `[]` {
			t.Fatalf("XHTTP/encryption source reported unsupported nodes: %+v", updated)
		}
		root := uiNodeTestObject(t, updated.Config)
		node := uiNodeTestFind(root.list("outbounds"), "Encrypted")
		if !uiNodeEqual(root.get("dns"), uiNodeTestObject(t, uiNodeDefaultConfig).get("dns")) ||
			node.get("domain_resolver") != nil || node.object("transport").object("download_settings").get("domain_resolver") != nil {
			t.Errorf("source DNS leaked into a downloaded node or replaced target DNS: %s", updated.Config)
		}
		if err := CheckConfig(updated.Config); err != nil {
			t.Fatalf("XHTTP/encryption failed complete core validation: %v", err)
		}
		repeated := uiSubscriptionTestApply(t, updated.Config, content, updated.Tags, true)
		if repeated.Config != updated.Config || repeated.Tags != updated.Tags || repeated.Skipped != 0 {
			t.Error("repeated XHTTP source accumulated nodes or changed fields")
		}
		legacy, err := UpdateUISubscriptionNodes(updated.Config, content)
		if err != nil || legacy == nil {
			t.Errorf("supported XHTTP source was rejected by the legacy update: %v", err)
		}
	}
	for _, encryption := range []string{"SECRET-INVALID", "mlkem768x25519plus.native.0rtt.SECRET-INVALID",
		"mlkem768x25519plus.native.SECRET-RTT." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x71}, 32))} {
		parsed := uiNodeTestParse(t, "vless://"+uiNodeTestUUID+"@proxy.example:443?type=xhttp&encryption="+encryption+"#Invalid")
		if len(parsed.list("nodes")) != 0 || len(parsed.list("skipped")) != 1 || strings.Contains(uiNodeTestText(parsed), "SECRET") {
			t.Errorf("invalid encryption was accepted or leaked its handshake value: %s", uiNodeTestText(parsed))
		}
		result, err := ApplyUISubscriptionNodes(uiNodeDefaultConfig, link+"\nvless://"+uiNodeTestUUID+"@proxy.example:443?type=xhttp&encryption="+encryption+"#Invalid", `[]`, true)
		if result != nil || err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Errorf("invalid supported encryption did not abort the source safely: result=%v, error=%v", result, err)
		}
	}
}

func TestUINodeXHTTPNestedDetourDeduplication(t *testing.T) {
	web := `{"type":"vless","tag":"Web","server":"web.example","server_port":443,"uuid":"` + uiNodeTestUUID + `","transport":{"type":"xhttp","path":"/up","headers":{"detour":"Exit"},"download_settings":{"path":"/down","detour":"Exit"}}}`
	current := `{"outbounds":[` + uiNodeTestNode("Exit", "198.51.100.1") + `,` + web + `]}`
	incoming := `[` + web + `,` + uiNodeTestNode("Exit", "198.51.100.2") + `]`
	updated, err := AddUIConfigNodes(current, incoming, false)
	if err != nil {
		t.Fatal(err)
	}
	root := uiNodeTestObject(t, updated.Config)
	old := uiNodeTestFind(root.list("outbounds"), "Web")
	added := uiNodeTestFind(root.list("outbounds"), "Web 2")
	if updated.Added != 2 || updated.Skipped != 0 || old.object("transport").object("download_settings").text("detour") != "Exit" ||
		added.object("transport").object("download_settings").text("detour") != "Exit 2" || added.object("transport").object("headers").text("detour") != "Exit" {
		t.Fatalf("nested detour was deduplicated before batch names were resolved: %+v", updated)
	}
	if err := CheckConfig(updated.Config); err != nil {
		t.Fatalf("nested detour import failed core validation: %v", err)
	}
	repeated, err := AddUIConfigNodes(updated.Config, incoming, false)
	if err != nil || repeated.Added != 0 || repeated.Skipped != 2 || repeated.Config != updated.Config {
		t.Errorf("nested detour import was not idempotent: result=%+v, error=%v", repeated, err)
	}
}

func TestUINodeXHTTPNestedDetourGraphs(t *testing.T) {
	web := func(tag, detour string) string {
		return `{"type":"vless","tag":"` + tag + `","server":"web.example","server_port":443,"uuid":"` + uiNodeTestUUID + `","transport":{"type":"xhttp","path":"/up","download_settings":{"path":"/down","detour":"` + detour + `"}}}`
	}
	current := `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"proxy","outbounds":["direct","Old"]},` + uiNodeTestNode("Old", "198.51.100.1") + `]}`
	added, err := AddUIConfigNodes(current, `[`+web("ViaGroup", "proxy")+`]`, false)
	if err != nil || uiNodeTestMembers(uiNodeTestObject(t, added.Config), "proxy") != "direct,Old" {
		t.Fatalf("XHTTP download through a group was inserted back into that group: result=%+v, error=%v", added, err)
	}
	current = `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"proxy","outbounds":["direct","A","B"],"default":"B"},` + uiNodeTestNode("A", "198.51.100.1") + `,` + web("B", "A") + `]}`
	updated := uiSubscriptionTestApply(t, current, `[`+web("A", "proxy")+`]`, `["A"]`, false)
	root := uiNodeTestObject(t, updated.Config)
	if uiNodeTestMembers(root, "proxy") != "direct" || uiNodeTestFind(root.list("outbounds"), "proxy").get("default") != nil {
		t.Errorf("indirect nested download/group cycle survived: %s", updated.Config)
	}
	if err := CheckConfig(updated.Config); err != nil {
		t.Errorf("nested group-cycle cleanup failed core validation: %v", err)
	}
	current = `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"upstream","outbounds":["Exit"]},{"type":"selector","tag":"proxy","outbounds":["Via","Owned"],"default":"Via"},` + uiNodeTestNode("Exit", "198.51.100.1") + `,` + web("Via", "upstream") + `,` + uiNodeTestNode("Owned", "198.51.100.2") + `]}`
	updated = uiSubscriptionTestApply(t, current, `[`+uiNodeTestNode("Owned", "203.0.113.1")+`]`, `["Owned"]`, false)
	root = uiNodeTestObject(t, updated.Config)
	if uiNodeTestMembers(root, "proxy") != "Via,Owned" || uiNodeTestFind(root.list("outbounds"), "proxy").text("default") != "Via" ||
		uiNodeTestFind(root.list("outbounds"), "Via").object("transport").object("download_settings").text("detour") != "upstream" {
		t.Errorf("legal download through another group lost its existing membership: %s", updated.Config)
	}
	cycle := `[` + web("A", "B") + `,` + web("B", "A") + `]`
	if result, err := ApplyUISubscriptionNodes(uiNodeDefaultConfig, cycle, `[]`, true); result != nil || err == nil || !strings.Contains(err.Error(), "cyclic detour") {
		t.Errorf("nested download cycle was accepted: result=%+v, error=%v", result, err)
	}
	if result, err := AddUIConfigNodes(`{}`, cycle, false); result != nil || err == nil || !strings.Contains(err.Error(), "cyclic detour") {
		t.Errorf("local nested download cycle was accepted: result=%+v, error=%v", result, err)
	}
	parsed := uiNodeTestParse(t, cycle)
	if len(parsed.list("nodes")) != 0 || len(parsed.list("skipped")) != 2 {
		t.Errorf("isolated nested cycle was offered as valid nodes: %s", uiNodeTestText(parsed))
	}
	current = `{"outbounds":[` + uiNodeTestNode("Exit", "198.51.100.1") + `]}`
	link := "vless://" + uiNodeTestUUID + "@web.example:443?type=xhttp&extra=" + url.PathEscape(`{"download_settings":{"detour":"Exit"}}`) + "#Web"
	source := link + "\nvless://" + uiNodeTestUUID + "@refused.example:443?type=kcp#Exit"
	if result, err := ApplyUISubscriptionNodes(current, source, `[]`, false); result != nil || err == nil || !strings.Contains(err.Error(), "unsupported or unnamed subscription node") {
		t.Errorf("nested detour rebound to a retained skipped-node name: result=%+v, error=%v", result, err)
	}
	parsed = uiNodeTestParse(t, link)
	if len(parsed.list("nodes")) != 0 || len(parsed.list("skipped")) != 1 {
		t.Errorf("isolated XHTTP download's missing detour was ignored: %s", uiNodeTestText(parsed))
	}
	source = `{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["Web"]},` + web("Web", "proxy") + `]}`
	if result, err := ApplyUISubscriptionNodes(uiNodeDefaultConfig, source, `[]`, true); result != nil || err == nil || !strings.Contains(err.Error(), "discarded subscription group proxy") {
		t.Errorf("nested detour rebound to a retained discarded group: result=%+v, error=%v", result, err)
	}
}
