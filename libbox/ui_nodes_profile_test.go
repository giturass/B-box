package libbox

import (
	"slices"
	"strings"
	"testing"
)

func uiNodeTestLocal(t *testing.T, config string, want ...string) {
	t.Helper()
	tags, marked, err := uiNodeLocalTags(config)
	if err != nil || !marked || !slices.Equal(tags, want) {
		t.Errorf("local names %v, marked %v, error %v; want %v", tags, marked, err, want)
	}
}

func TestUINodeConfigAppend(t *testing.T) {
	current := `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"proxy","outbounds":["direct"]},` +
		uiNodeTestNode("Exit", "198.51.100.9") + `],"route":{"final":"proxy"}}`
	nodes := "[" + uiNodeTestVia("Hop", "198.51.100.1", "Exit") + "," + uiNodeTestNode("Exit", "198.51.100.2") + "]"
	update, err := AddUIConfigNodes(current, nodes, false)
	if err != nil {
		t.Fatal(err)
	}
	root := uiNodeTestObject(t, update.Config)
	if update.Added != 2 || update.Skipped != 0 || uiNodeTestTags(root.list("outbounds")) != "direct,proxy,Exit,Hop,Exit 2" ||
		uiNodeTestFind(root.list("outbounds"), "Hop").text("detour") != "Exit 2" || uiNodeTestMembers(root, "proxy") != "direct,Hop,Exit 2" {
		t.Errorf("append returned %+v", update)
	}
	uiNodeTestLocal(t, update.Config, "Hop", "Exit 2")
	if err := CheckConfig(update.Config); err != nil {
		t.Errorf("appended configuration: %v", err)
	}
	repeated, err := AddUIConfigNodes(update.Config, nodes, false)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Added != 0 || repeated.Skipped != 2 || repeated.Config != update.Config {
		t.Errorf("repeated import changed the configuration: %+v", repeated)
	}
	parsed, err := ParseUINodes(uiNodeTestList)
	if err != nil {
		t.Fatal(err)
	}
	update, err = AddUIConfigNodes(`{}`, parsed.Value, false)
	if err != nil || update.Added != 3 || update.Skipped != 0 {
		t.Errorf("parsed result append: %+v, %v", update, err)
	}
}

func TestUINodeConfigAppendRefusals(t *testing.T) {
	const secret = `"password":"SECRETSECRET"`
	for _, item := range []struct{ name, node, reason string }{
		{"no name", `{"type":"shadowsocks","server":"198.51.100.1","server_port":8388,"method":"aes-256-gcm",` + secret + `}`, "a node needs a name"},
		{"group", `{"type":"selector","tag":"Group","outbounds":["a"]}`, "Group: selector is not a proxy node type"},
		{"direct", `{"type":"direct","tag":"Direct"}`, "Direct: direct is not a proxy node type"},
		{"unknown type", `{"type":"nonesuch","tag":"Odd",` + secret + `}`, "Odd: nonesuch is not a proxy node type"},
		{"unknown option", `{"type":"shadowsocks","tag":"Option","server":"198.51.100.1","server_port":8388,"method":"aes-256-gcm","nonesuch":true,` + secret + `}`, "Option: "},
		{"unknown cipher", `{"type":"shadowsocks","tag":"Cipher","server":"198.51.100.1","server_port":8388,"method":"nonesuch",` + secret + `}`, "Cipher: "},
	} {
		_, err := AddUIConfigNodes(`{}`, "["+uiNodeTestNode("Fine", "198.51.100.1")+","+item.node+"]", false)
		exact := !strings.HasSuffix(item.reason, ": ")
		if err == nil {
			t.Errorf("%s: the node was accepted", item.name)
		} else if reason := err.Error(); exact && reason != item.reason ||
			!exact && (!strings.HasPrefix(reason, item.reason) || reason == item.reason) ||
			strings.Contains(reason, "SECRET") || strings.Contains(reason, "node-") {
			t.Errorf("%s: refused with %q, want %q", item.name, reason, item.reason)
		}
	}
	_, err := AddUIConfigNodes(`{"outbounds":{}}`, `[]`, false)
	uiNodeTestFails(t, "non-list outbounds", err, "outbounds must be a JSON array")
}

func TestUINodeConfigAppendEndpoint(t *testing.T) {
	parsed := uiNodeTestParse(t, uiNodeTestWireGuard(""))
	if len(parsed.list("nodes")) == 0 {
		t.Skip("this core build does not include WireGuard")
	}
	update, err := AddUIConfigNodes(`{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["direct"]},{"type":"direct","tag":"direct"}]}`, uiNodeTestText(parsed), false)
	if err != nil {
		t.Fatal(err)
	}
	root := uiNodeTestObject(t, update.Config)
	if len(root.list("endpoints")) != 1 || len(root.list("outbounds")) != 2 || uiNodeTestMembers(root, "proxy") != "direct,wg.example.com:51820" {
		t.Errorf("WireGuard was appended to %s", update.Config)
	}
}

func TestUINodeConfigMergeRefresh(t *testing.T) {
	current := `{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["Old"]},` + uiNodeTestNode("Old", "198.51.100.9") + `],"route":{"final":"proxy"}}`
	update, err := AddUIConfigNodes(current, "["+uiNodeTestVia("Hop", "198.51.100.1", "Exit")+","+uiNodeTestNode("Exit", "198.51.100.2")+"]", false)
	if err != nil {
		t.Fatal(err)
	}
	incoming := `{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["Exit"],"default":"Exit"},` +
		uiNodeTestNode("Exit", "203.0.113.2") + `],"route":{"final":"proxy"}}`
	merged, err := MergeUIConfigNodes(update.Config, incoming)
	config := uiNodeTestValue(t, "remote refresh", merged, err)
	root := uiNodeTestObject(t, config)
	if uiNodeTestTags(root.list("outbounds")) != "proxy,Exit,Hop,Exit 2" ||
		uiNodeTestFind(root.list("outbounds"), "Exit").text("server") != "203.0.113.2" ||
		uiNodeTestFind(root.list("outbounds"), "Exit 2").text("server") != "198.51.100.2" ||
		uiNodeTestFind(root.list("outbounds"), "Hop").text("detour") != "Exit 2" ||
		uiNodeTestMembers(root, "proxy") != "Exit,Hop,Exit 2" ||
		uiNodeTestFind(root.list("outbounds"), "proxy").text("default") != "Exit" {
		t.Errorf("refresh lost local nodes or changed upstream definitions: %s", config)
	}
	uiNodeTestLocal(t, config, "Hop", "Exit 2")
	if err := CheckConfig(config); err != nil {
		t.Errorf("merged configuration: %v", err)
	}
	// Saving and formatting must honor the renamed merged marker rather than
	// inheriting the old Exit name onto the new upstream Exit definition.
	preserved, err := PreserveUIConfigNodes(update.Config, config)
	if found := uiNodeTestValue(t, "preserve after refresh", preserved, err); found != config {
		t.Errorf("refresh ownership changed while saving: %s", found)
	}
	formatted, err := FormatConfig(config)
	config = uiNodeTestValue(t, "format refresh", formatted, err)
	uiNodeTestLocal(t, config, "Hop", "Exit 2")
	second, err := MergeUIConfigNodes(config, incoming)
	config = uiNodeTestValue(t, "repeat refresh", second, err)
	if got := uiNodeTestTags(uiNodeTestObject(t, config).list("outbounds")); got != "proxy,Exit,Hop,Exit 2" {
		t.Errorf("a repeated refresh grew the nodes: %s", got)
	}
}

func TestUINodeConfigRefreshKeepsText(t *testing.T) {
	incoming := "{ // keep the provider's text\n  \"outbounds\": [{\"type\":\"direct\",\"tag\":\"direct\"}]\n}" + strings.Repeat(" ", 17<<20)
	merged, err := MergeUIConfigNodes(`{"outbounds":[`+uiNodeTestNode("Removed", "198.51.100.1")+`]}`, incoming)
	if config := uiNodeTestValue(t, "refresh without local nodes", merged, err); config != incoming {
		t.Errorf("refresh without local additions changed the provider's text")
	}
	// Provider-supplied ownership comments never confer local ownership.
	merged, err = MergeUIConfigNodes(`{}`, uiNodeLocalPrefix+`["direct"]`+"\n"+incoming)
	config := uiNodeTestValue(t, "provider marker", merged, err)
	uiNodeTestLocal(t, config)
	if config != uiNodeLocalPrefix+`[]`+"\n"+incoming {
		t.Errorf("clearing provider ownership changed its document")
	}
	// An old marker with a now-deleted local node is authoritative empty.
	previous := uiNodeLocalPrefix + `["Removed"]` + "\n{}"
	merged, err = MergeUIConfigNodes(previous, incoming)
	config = uiNodeTestValue(t, "deleted local node", merged, err)
	uiNodeTestLocal(t, config)
	preserved, err := PreserveUIConfigNodes(previous, config)
	if got := uiNodeTestValue(t, "save empty refresh", preserved, err); got != config {
		t.Errorf("a deleted local tag was reintroduced")
	}
}

func TestUINodeConfigLocalEditing(t *testing.T) {
	update, err := AddUIConfigNodes(`{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["direct"]},{"type":"direct","tag":"direct"}]}`, "["+uiNodeTestNode("Local", "198.51.100.1")+"]", false)
	if err != nil {
		t.Fatal(err)
	}
	renamed, err := SetUIConfigNode(update.Config, "Local", uiNodeTestNode("Renamed", "198.51.100.2"))
	config := uiNodeTestValue(t, "rename local node", renamed, err)
	uiNodeTestLocal(t, config, "Renamed")
	if group := uiNodeTestMembers(uiNodeTestObject(t, config), "proxy"); group != "direct,Renamed" {
		t.Errorf("rename left group %s", group)
	}
	// Platform JSON edits strip comments; saving recovers only surviving names.
	raw := uiNodeTestText(uiNodeTestObject(t, config))
	preserved, err := PreserveUIConfigNodes(config, raw)
	config = uiNodeTestValue(t, "platform JSON edit", preserved, err)
	uiNodeTestLocal(t, config, "Renamed")
	formatted, err := FormatConfig(config)
	config = uiNodeTestValue(t, "format local node", formatted, err)
	uiNodeTestLocal(t, config, "Renamed")
	removed, err := RemoveUIConfigNodes(config, `["Renamed"]`, false)
	config = uiNodeTestValue(t, "remove local node", removed, err)
	uiNodeTestLocal(t, config)
	merged, err := MergeUIConfigNodes(config, `{"outbounds":[`+uiNodeTestNode("Renamed", "203.0.113.1")+`]}`)
	config = uiNodeTestValue(t, "refresh after local deletion", merged, err)
	uiNodeTestLocal(t, config)
	if got := uiNodeTestTags(uiNodeTestObject(t, config).list("outbounds")); got != "Renamed" {
		t.Errorf("deleted local node reappeared: %s", got)
	}
}

func TestUINodeConfigMissingRemoteDependency(t *testing.T) {
	current := `{"outbounds":[` + uiNodeTestNode("Exit", "198.51.100.2") + `]}`
	update, err := AddUIConfigNodes(current, "["+uiNodeTestVia("Local", "198.51.100.1", "Exit")+"]", false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = MergeUIConfigNodes(update.Config, `{"outbounds":[{"type":"direct","tag":"direct"}]}`)
	uiNodeTestFails(t, "removed remote detour", err, "local node Local refers to missing outbound Exit")
}

func TestUINodeConfigWriterKeepsValues(t *testing.T) {
	config := `{"outbounds":[],"future":{"integer":12345678901234567890123,"text":"<node>"}}`
	update, err := AddUIConfigNodes(config, "["+uiNodeTestNode("Local", "198.51.100.1")+"]", false)
	if err != nil {
		t.Fatal(err)
	}
	root := uiNodeTestObject(t, update.Config)
	if got := uiNodeTestText(root.object("future")); got != `{"integer":12345678901234567890123,"text":"<node>"}` ||
		strings.Join(root.keys, ",") != "outbounds,future" {
		t.Errorf("append changed unrelated fields: %s", update.Config)
	}
}
