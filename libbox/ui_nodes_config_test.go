package libbox

import (
	"strings"
	"testing"
)

// uiNodeTestConfig names the shared node A wherever a configuration names an
// outbound, and defines the node Own.
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
	// The detour of an inbound is another inbound, and a name the
	// configuration defines is not one it takes from the shared nodes.
	found, err := FindUINodeReferences(uiNodeTestConfig, `["A","Own","proxy"]`)
	want := `[{"tag":"A","path":"dns.servers[0].detour"},{"tag":"A","path":"outbounds[0].outbounds[0]"},` +
		`{"tag":"A","path":"route.rules[0].outbound"},{"tag":"A","path":"route.rule_set[0].http_client.detour"}]`
	if value := uiNodeTestValue(t, "references", found, err); value != want {
		t.Errorf("references are %s, want %s", value, want)
	}
	for _, config := range []string{"", "not a configuration", `{"route":{"final":"B"}}`} {
		found, err = FindUINodeReferences(config, `["A"]`)
		if value := uiNodeTestValue(t, "no reference", found, err); value != `[]` {
			t.Errorf("%q names %s", config, value)
		}
	}
	_, err = FindUINodeReferences(uiNodeTestConfig, `"A"`)
	uiNodeTestFails(t, "names that are no list", err, "a JSON array of names is required")
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
		{"shared node", "A", uiNodeTestNode("A", "198.51.100.9"), "node not found: A"},
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
	removed, err := RemoveUIConfigNodes(uiNodeTestConfig, `["Own"]`)
	root := uiNodeTestObject(t, uiNodeTestValue(t, "remove", removed, err))
	if want := `[{"type":"selector","tag":"proxy","outbounds":["A"]}]`; uiNodeTestText(root.list("outbounds")) != want {
		t.Errorf("a removed node left %s, want %s", uiNodeTestText(root.list("outbounds")), want)
	}
	for _, item := range []struct{ name, config, tags, reason string }{
		{"unknown node", uiNodeTestConfig, `["Own","Nope"]`, "node not found: Nope"},
		{"shared node", uiNodeTestConfig, `["A"]`, "node not found: A"},
		{"group in use", uiNodeTestConfig, `["proxy"]`, "proxy is still used at route.final"},
		{"node in use", strings.Replace(uiNodeTestConfig, `"final": "proxy"`, `"final": "Own"`, 1), `["Own"]`, "Own is still used at route.final"},
		{"last member", strings.Replace(uiNodeTestConfig, `["A", "Own"]`, `["Own"]`, 1), `["Own"]`, "group proxy would be left without a member"},
	} {
		_, err = RemoveUIConfigNodes(item.config, item.tags)
		uiNodeTestFails(t, item.name, err, item.reason)
	}
}

func TestUINodeConfigGet(t *testing.T) {
	wireguard := `{"type":"wireguard","tag":"WG","address":["10.0.0.2/32"],"private_key":"` + uiNodeTestKey(1) + `"}`
	local := strings.Replace(wireguard, "WG", "Local", 1)
	shared := uiNodeTestNode("A", "198.51.100.1")
	library := `{"version":1,"sources":[{"id":"sub"}],"nodes":[{"source":"sub","node":` + shared + `},{"source":"","node":` +
		uiNodeTestNode("Own", "198.51.100.2") + `},{"source":"","node":` + wireguard + `}]}`
	config := strings.Replace(uiNodeTestConfig, `"route":`, `"endpoints": [`+local+`], "route":`, 1)
	for _, item := range []struct {
		tag, origin, source string
		endpoint            bool
		node                string
	}{
		// The node the configuration defines hides the shared one.
		{"Own", "profile", "", false, uiNodeTestNode("Own", "198.51.100.8")},
		{"Local", "profile", "", true, local},
		{"A", "library", "sub", false, shared},
		{"WG", "library", "", true, wireguard},
	} {
		found, err := GetUINode(config, library, item.tag)
		result := uiNodeTestObject(t, uiNodeTestValue(t, item.tag, found, err))
		endpoint, _ := result.get("endpoint").(bool)
		if result.text("origin") != item.origin || result.text("source") != item.source || endpoint != item.endpoint ||
			!uiNodeEqual(uiNodeTestJSON(t, result.text("content")), uiNodeTestJSON(t, item.node)) {
			t.Errorf("%s: found as %s", item.tag, found.Value)
		}
	}
	_, err := GetUINode(config, library, "Nope")
	uiNodeTestFails(t, "unknown node", err, "node not found: Nope")
	// The node is shown the way the editor shows a configuration.
	found, err := GetUINode("", library, "Own")
	result := uiNodeTestObject(t, uiNodeTestValue(t, "shared node", found, err))
	if result.text("origin") != "library" || !strings.HasPrefix(result.text("content"), "{\n  \"type\": \"shadowsocks\",\n  \"tag\": \"Own\",\n  \"server\": \"198.51.100.2\",") {
		t.Errorf("the shared node is found as %s", found.Value)
	}
}

// The configuration offered first is one the core reads, with or without
// shared nodes, and takes its rule sets from the sing-box project.
func TestUINodeDefaultConfig(t *testing.T) {
	config := GetUIDefaultConfig().Value
	ruleSets := uiNodeTestObject(t, config).object("route").list("rule_set")
	for _, item := range ruleSets {
		ruleSet, _ := item.(*uiNodeObject)
		if !strings.HasPrefix(ruleSet.text("url"), "https://raw.githubusercontent.com/SagerNet/sing-geo") {
			t.Errorf("rule set %s comes from %s", ruleSet.text("tag"), ruleSet.text("url"))
		}
	}
	if len(ruleSets) != 4 {
		t.Errorf("%d rule sets", len(ruleSets))
	}
	chain := uiNodeTestShared(uiNodeTestNode("A", "198.51.100.1"), uiNodeTestVia("B", "198.51.100.2", "A"))
	for _, item := range []struct{ library, proxy, auto string }{{"", "auto", "direct"}, {chain, "auto,A,B", "A,B"}} {
		expanded := uiNodeTestExpand(t, config, item.library)
		if _, err := parseConfig(baseContext(nil), expanded); err != nil {
			t.Errorf("default configuration: %v", err)
		}
		root := uiNodeTestObject(t, expanded)
		if proxy, auto := uiNodeTestMembers(root, "proxy"), uiNodeTestMembers(root, "auto"); proxy != item.proxy || auto != item.auto {
			t.Errorf("default configuration selects among %q and tests %q", proxy, auto)
		}
	}
}
