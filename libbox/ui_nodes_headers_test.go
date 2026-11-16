package libbox

import (
	"strings"
	"testing"
)

func TestUINodeConfigHeadersAreData(t *testing.T) {
	exit := uiNodeTestNode("Exit", "198.51.100.1")
	web := `{"type":"vless","tag":"Web","server":"198.51.100.3","server_port":443,"uuid":"` + uiNodeTestUUID +
		`","detour":"Exit","transport":{"type":"ws","path":"/ws","headers":{"default":["Exit"],"detour":["literal-header-token"],"outbounds":["literal-body"]}}}`
	headers := uiNodeTestObject(t, web).object("transport").object("headers")
	current := `{"outbounds":[` + uiNodeTestNode("Exit", "203.0.113.1") + `]}`
	update, err := AddUIConfigNodes(current, "["+exit+","+web+"]", false)
	if err != nil {
		t.Fatal(err)
	}
	root := uiNodeTestObject(t, update.Config)
	node := uiNodeTestFind(root.list("outbounds"), "Web")
	if node.text("detour") != "Exit 2" || !uiNodeEqual(node.object("transport").object("headers"), headers) {
		t.Errorf("append changed header data: %s", update.Config)
	}
	merged, err := MergeUIConfigNodes(update.Config, current)
	config := uiNodeTestValue(t, "merge arbitrary headers", merged, err)
	node = uiNodeTestFind(uiNodeTestObject(t, config).list("outbounds"), "Web")
	if !uiNodeEqual(node.object("transport").object("headers"), headers) {
		t.Errorf("merge changed header data: %s", config)
	}
	// A renamed dial target updates the real detour, not HTTP header values.
	current = `{"outbounds":[` + exit + "," + web + `]}`
	renamed, err := SetUIConfigNode(current, "Exit", strings.Replace(exit, `"tag":"Exit"`, `"tag":"Renamed"`, 1))
	config = uiNodeTestValue(t, "rename beside arbitrary headers", renamed, err)
	node = uiNodeTestFind(uiNodeTestObject(t, config).list("outbounds"), "Web")
	if node.text("detour") != "Renamed" || !uiNodeEqual(node.object("transport").object("headers"), headers) {
		t.Errorf("rename changed header data: %s", config)
	}
}

func TestUINodeConfigOrdinaryDirectMember(t *testing.T) {
	current := `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"urltest","tag":"auto","outbounds":["direct"]}]}`
	update, err := AddUIConfigNodes(current, "["+uiNodeTestNode("Local", "198.51.100.1")+"]", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := uiNodeTestMembers(uiNodeTestObject(t, update.Config), "auto"); got != "direct,Local" {
		t.Errorf("ordinary append removed declared direct member: %s", got)
	}
	merged, err := MergeUIConfigNodes(update.Config, current)
	config := uiNodeTestValue(t, "refresh declared direct member", merged, err)
	if got := uiNodeTestMembers(uiNodeTestObject(t, config), "auto"); got != "direct,Local" {
		t.Errorf("refresh removed declared direct member: %s", got)
	}
}
