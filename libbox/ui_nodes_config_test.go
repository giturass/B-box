package libbox

import (
	"strings"
	"testing"
)

// uiNodeTestConfig names A in dial options, while defining Own and proxy.
// The root inbound detour belongs to the inbound namespace.
const uiNodeTestConfig = `{
  "dns": {"servers": [{"type": "https", "tag": "remote", "server": "1.1.1.1", "detour": "A"}], "final": "remote"},
  "inbounds": [{"type": "mixed", "tag": "in", "listen": "127.0.0.1", "listen_port": 6080, "detour": "A"}],
  "outbounds": [
    {"type": "selector", "tag": "proxy", "outbounds": ["A", "Own"], "default": "Own"},
    {"type": "shadowsocks", "tag": "Own", "server": "198.51.100.8", "server_port": 8388, "method": "aes-256-gcm", "password": "password"}
  ],
  "route": {
    "rules": [{"domain_suffix": "example.com", "outbound": "A"}],
    "rule_set": [{"type": "remote", "tag": "rules", "url": "https://example.com/rules.srs", "http_client": {"detour": "A"}}],
    "final": "proxy"
  }
}`

func TestUINodeConfigReferences(t *testing.T) {
	root := uiNodeTestObject(t, uiNodeTestConfig)
	var found []any
	for _, reference := range uiNodeReferences(root) {
		if reference.tag == "A" {
			found = append(found, uiNodeOf("tag", reference.tag, "path", reference.path))
		}
	}
	want := `[{"tag":"A","path":"dns.servers[0].detour"},{"tag":"A","path":"outbounds[0].outbounds[0]"},` +
		`{"tag":"A","path":"route.rules[0].outbound"},{"tag":"A","path":"route.rule_set[0].http_client.detour"}]`
	if value := uiNodeTestText(found); value != want {
		t.Errorf("references are %s, want %s", value, want)
	}
}

func TestUINodeConfigEdit(t *testing.T) {
	mine := uiNodeTestNode("Mine", "198.51.100.9")
	edited, err := SetUIConfigNode(uiNodeTestConfig, "Own", mine)
	root := uiNodeTestObject(t, uiNodeTestValue(t, "rename", edited, err))
	outbounds := root.list("outbounds")
	if uiNodeTestMembers(root, "proxy") != "A,Mine" || uiNodeTestFind(outbounds, "proxy").text("default") != "Mine" ||
		len(outbounds) != 2 || uiNodeTestText(outbounds[1]) != mine {
		t.Errorf("a renamed node left %s", uiNodeTestText(outbounds))
	}
	// A group is edited the same way.
	edited, err = SetUIConfigNode(uiNodeTestConfig, "proxy", `{"type":"urltest","tag":"auto","outbounds":["A","Own"]}`)
	root = uiNodeTestObject(t, uiNodeTestValue(t, "rename a group", edited, err))
	if final := root.object("route").text("final"); final != "auto" || uiNodeTestTags(root.list("outbounds")) != "auto,Own" {
		t.Errorf("a renamed group left %s and the final outbound %q", uiNodeTestText(root.list("outbounds")), final)
	}
	for _, item := range []struct{ name, tag, node, reason string }{
		{"taken name", "Own", uiNodeTestNode("proxy", "198.51.100.9"), "a node named proxy already exists"},
		{"undefined node", "A", uiNodeTestNode("A", "198.51.100.9"), "node not found: A"},
		{"no name", "Own", `{"type":"direct"}`, "a node needs a name"},
	} {
		_, err = SetUIConfigNode(uiNodeTestConfig, item.tag, item.node)
		uiNodeTestFails(t, item.name, err, item.reason)
	}
	_, err = SetUIConfigNode(uiNodeTestConfig, "Own", strings.Replace(mine, `}`, `,"nonesuch":true}`, 1))
	if err == nil || !strings.HasPrefix(err.Error(), "Mine: ") || err.Error() == "Mine: " {
		t.Errorf("an unknown option was answered with %v", err)
	}
}

func TestUINodeConfigRemove(t *testing.T) {
	removed, err := RemoveUIConfigNodes(uiNodeTestConfig, `["Own"]`, false)
	root := uiNodeTestObject(t, uiNodeTestValue(t, "remove", removed, err))
	if want := `[{"type":"selector","tag":"proxy","outbounds":["A"]}]`; uiNodeTestText(root.list("outbounds")) != want {
		t.Errorf("a removed node left %s, want %s", uiNodeTestText(root.list("outbounds")), want)
	}
	for _, item := range []struct{ name, config, tags, reason string }{
		{"unknown node", uiNodeTestConfig, `["Own","Nope"]`, "node not found: Nope"},
		{"undefined node", uiNodeTestConfig, `["A"]`, "node not found: A"},
		{"group in use", uiNodeTestConfig, `["proxy"]`, "proxy is still used at route.final"},
		{"node in use", strings.Replace(uiNodeTestConfig, `"final": "proxy"`, `"final": "Own"`, 1), `["Own"]`, "Own is still used at route.final"},
		{"last member", strings.Replace(uiNodeTestConfig, `["A", "Own"]`, `["Own"]`, 1), `["Own"]`, "group proxy would be left without a member"},
	} {
		_, err = RemoveUIConfigNodes(item.config, item.tags, false)
		uiNodeTestFails(t, item.name, err, item.reason)
	}
}

func TestUINodeConfigGet(t *testing.T) {
	local := `{"type":"wireguard","tag":"Local","address":["10.0.0.2/32"],"private_key":"` + uiNodeTestKey(1) + `"}`
	config := strings.Replace(uiNodeTestConfig, `"route":`, `"endpoints": [`+local+`], "route":`, 1)
	for _, item := range []struct {
		tag      string
		endpoint bool
		node     string
	}{
		{"Own", false, uiNodeTestNode("Own", "198.51.100.8")},
		{"Local", true, local},
	} {
		found, err := GetUINode(config, item.tag)
		result := uiNodeTestObject(t, uiNodeTestValue(t, item.tag, found, err))
		endpoint, _ := result.get("endpoint").(bool)
		if endpoint != item.endpoint || !uiNodeEqual(uiNodeTestJSON(t, result.text("content")), uiNodeTestJSON(t, item.node)) ||
			result.get("origin") != nil || result.get("source") != nil {
			t.Errorf("%s: found as %s", item.tag, found.Value)
		}
	}
	_, err := GetUINode(config, "Nope")
	uiNodeTestFails(t, "unknown node", err, "node not found: Nope")
}

func TestUINodeDefaultConfig(t *testing.T) {
	config := GetUIDefaultConfig().Value
	if err := CheckConfig(config); err != nil {
		t.Fatalf("default configuration: %v", err)
	}
	root := uiNodeTestObject(t, config)
	if store := root.object("certificate").text("store"); store != "mozilla" {
		t.Errorf("default certificate store is %q, want mozilla", store)
	}
	if proxy, auto := uiNodeTestMembers(root, "proxy"), uiNodeTestMembers(root, "auto"); proxy != "auto" || auto != "direct" {
		t.Errorf("default groups select %q and test %q", proxy, auto)
	}
	ruleSets := root.object("route").list("rule_set")
	for _, item := range ruleSets {
		ruleSet, _ := item.(*uiNodeObject)
		if !strings.HasPrefix(ruleSet.text("url"), "https://raw.githubusercontent.com/SagerNet/sing-geo") {
			t.Errorf("rule set %s comes from %s", ruleSet.text("tag"), ruleSet.text("url"))
		}
	}
	if len(ruleSets) != 4 {
		t.Errorf("%d rule sets", len(ruleSets))
	}
	update, err := AddUIConfigNodes(config, "["+uiNodeTestNode("A", "198.51.100.1")+","+uiNodeTestVia("B", "198.51.100.2", "A")+"]", true)
	if err != nil {
		t.Fatal(err)
	}
	root = uiNodeTestObject(t, update.Config)
	if store := root.object("certificate").text("store"); store != "mozilla" {
		t.Errorf("appending nodes changed the certificate store to %q", store)
	}
	if proxy, auto := uiNodeTestMembers(root, "proxy"), uiNodeTestMembers(root, "auto"); proxy != "auto,A,B" || auto != "A,B" {
		t.Errorf("appended groups select %q and test %q", proxy, auto)
	}
	if err := CheckConfig(update.Config); err != nil {
		t.Errorf("default configuration after append: %v", err)
	}
	removed, err := RemoveUIConfigNodes(update.Config, `["A","B"]`, true)
	config = uiNodeTestValue(t, "remove the last default nodes", removed, err)
	root = uiNodeTestObject(t, config)
	uiNodeTestLocal(t, config)
	if proxy, auto := uiNodeTestMembers(root, "proxy"), uiNodeTestMembers(root, "auto"); proxy != "auto" || auto != "direct" {
		t.Errorf("empty default groups select %q and test %q", proxy, auto)
	}
}
