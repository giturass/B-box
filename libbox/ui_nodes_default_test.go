package libbox

import "testing"

func uiNodeDefaultTestRoot(t *testing.T, members string, nodes ...string) *uiNodeObject {
	t.Helper()
	root := uiNodeTestObject(t, GetUIDefaultConfig().Value)
	uiNodeTestFind(root.list("outbounds"), "auto").set("outbounds", uiNodeTestJSON(t, members))
	for _, node := range nodes {
		root.set("outbounds", append(root.list("outbounds"), uiNodeTestObject(t, node)))
	}
	return root
}

func uiNodeDefaultTestPreserved(t *testing.T, previous, updated *uiNodeObject) {
	t.Helper()
	for _, key := range previous.keys {
		if key != "outbounds" && !uiNodeEqual(previous.get(key), updated.get(key)) {
			t.Errorf("node import changed %s", key)
		}
	}
	if !uiNodeEqual(uiNodeTestFind(previous.list("outbounds"), "direct"), uiNodeTestFind(updated.list("outbounds"), "direct")) {
		t.Error("node import changed the direct outbound")
	}
}

func TestUINodeDefaultAutoAppendRemovesDirectMembers(t *testing.T) {
	previous := uiNodeDefaultTestRoot(t, `["direct","Old","bypass"]`,
		uiNodeTestNode("Old", "198.51.100.1"),
		`{"type":"direct","tag":"bypass"}`,
		`{"type":"urltest","tag":"custom","outbounds":["direct","Old"]}`)
	proxy := uiNodeTestFind(previous.list("outbounds"), "proxy")
	proxy.set("outbounds", []any{"auto", "direct", "Old"})
	proxy.set("default", "direct")
	current := uiNodeTestText(previous)
	nodes := `[` + uiNodeTestNode("New", "198.51.100.2") + `]`
	for _, item := range []struct {
		name           string
		defaultProfile bool
		wantAuto       string
	}{
		{"default", true, "Old,New"},
		{"ordinary", false, "direct,Old,bypass,New"},
	} {
		t.Run(item.name, func(t *testing.T) {
			update, err := AddUIConfigNodes(current, nodes, item.defaultProfile)
			if err != nil {
				t.Fatal(err)
			}
			root := uiNodeTestObject(t, update.Config)
			if update.Added != 1 || update.Skipped != 0 || uiNodeTestMembers(root, "auto") != item.wantAuto {
				t.Errorf("automatic group after append: %+v", update)
			}
			if uiNodeTestMembers(root, "proxy") != "auto,direct,Old,New" ||
				uiNodeTestFind(root.list("outbounds"), "proxy").text("default") != "direct" ||
				uiNodeTestMembers(root, "custom") != "direct,Old,New" {
				t.Errorf("append changed another group's explicit direct member: %s", update.Config)
			}
			if !uiNodeEqual(uiNodeTestFind(previous.list("outbounds"), "bypass"), uiNodeTestFind(root.list("outbounds"), "bypass")) {
				t.Error("append removed the alternate direct outbound")
			}
			uiNodeDefaultTestPreserved(t, previous, root)
			uiNodeTestLocal(t, update.Config, "New")
			if err := CheckConfig(update.Config); err != nil {
				t.Errorf("configuration after append: %v", err)
			}
		})
	}
}

func TestUINodeDefaultAutoDuplicateImportRepairsDirectFallback(t *testing.T) {
	via := uiNodeTestVia("Via", "198.51.100.2", "upstream")
	previous := uiNodeDefaultTestRoot(t, `["direct","Via"]`,
		`{"type":"selector","tag":"upstream","outbounds":["Exit"]}`,
		uiNodeTestNode("Exit", "198.51.100.1"), via)
	current := uiNodeLocalPrefix + `["Via"]` + "\n" + uiNodeTestText(previous)
	update, err := AddUIConfigNodes(current, `[`+via+`]`, true)
	if err != nil {
		t.Fatal(err)
	}
	root := uiNodeTestObject(t, update.Config)
	if update.Added != 0 || update.Skipped != 1 || uiNodeTestMembers(root, "auto") != "Via" ||
		uiNodeTestMembers(root, "upstream") != "Exit" ||
		!uiNodeEqual(uiNodeTestFind(root.list("outbounds"), "Via"), uiNodeTestObject(t, via)) {
		t.Errorf("duplicate import did not repair a valid existing member: %+v", update)
	}
	uiNodeDefaultTestPreserved(t, previous, root)
	uiNodeTestLocal(t, update.Config, "Via")
	if err := CheckConfig(update.Config); err != nil {
		t.Errorf("configuration after duplicate import: %v", err)
	}
	repeated, err := AddUIConfigNodes(update.Config, `[`+via+`]`, true)
	if err != nil {
		t.Fatal(err)
	}
	if repeated.Added != 0 || repeated.Skipped != 1 || repeated.Config != update.Config {
		t.Errorf("repeated import changed the repaired configuration: %+v", repeated)
	}
}

func TestUINodeDefaultAutoSubscriptionSourcesRemoveDirectFallback(t *testing.T) {
	previous := uiNodeDefaultTestRoot(t, `["direct","Old","Local","Other"]`,
		uiNodeTestNode("Old", "198.51.100.1"),
		uiNodeTestNode("Local", "198.51.100.2"),
		uiNodeTestNode("Other", "198.51.100.3"))
	current := uiNodeLocalPrefix + `["Local"]` + "\n" + uiNodeTestText(previous)
	added := uiSubscriptionTestApply(t, current, `[`+uiNodeTestNode("New", "203.0.113.1")+`]`, `[]`, true)
	root := uiNodeTestObject(t, added.Config)
	if added.Added != 1 || added.Tags != `["New"]` || uiNodeTestMembers(root, "auto") != "Old,Local,Other,New" {
		t.Errorf("new source did not remove the default fallback: %+v", added)
	}
	uiNodeDefaultTestPreserved(t, previous, root)
	uiNodeTestLocal(t, added.Config, "Local")

	// Older saved configurations can still contain direct alongside proxies.
	uiNodeTestFind(root.list("outbounds"), "auto").set("outbounds", []any{"direct", "Old", "Local", "Other", "New"})
	current = uiNodeLocalPrefix + `["Local"]` + "\n" + uiNodeTestText(root)
	subscription := `[` + uiNodeTestVia("Replacement", "203.0.113.2", "Other") + `]`
	updated := uiSubscriptionTestApply(t, current, subscription, `["Old"]`, true)
	root = uiNodeTestObject(t, updated.Config)
	if updated.Added != 1 || updated.Tags != `["Replacement"]` ||
		uiNodeTestTags(root.list("outbounds")) != "direct,proxy,auto,Local,Other,New,Replacement" ||
		uiNodeTestMembers(root, "auto") != "Local,Other,New,Replacement" {
		t.Errorf("source update did not repair the automatic group: %+v", updated)
	}
	for _, tag := range []string{"Local", "Other"} {
		if !uiNodeEqual(uiNodeTestFind(previous.list("outbounds"), tag), uiNodeTestFind(root.list("outbounds"), tag)) {
			t.Errorf("source update changed retained node %s", tag)
		}
	}
	if !uiNodeEqual(uiNodeTestFind(uiNodeTestObject(t, added.Config).list("outbounds"), "New"), uiNodeTestFind(root.list("outbounds"), "New")) {
		t.Error("source update changed the other source's new node")
	}
	uiNodeDefaultTestPreserved(t, previous, root)
	uiNodeTestLocal(t, updated.Config, "Local")
	if err := CheckConfig(updated.Config); err != nil {
		t.Errorf("configuration after source update: %v", err)
	}
	repeated := uiSubscriptionTestApply(t, updated.Config, subscription, updated.Tags, true)
	if repeated.Config != updated.Config || repeated.Tags != updated.Tags || repeated.Added != 1 {
		t.Errorf("repeated source update changed the repaired configuration: %+v", repeated)
	}
}

func TestUINodeDefaultAutoKeepsFallbackWithoutEligibleMembers(t *testing.T) {
	current := GetUIDefaultConfig().Value
	previous := uiNodeTestObject(t, current)
	nodes := `[` + uiNodeTestVia("Via", "198.51.100.1", "auto") + `]`
	// Selecting Via from auto would route back to auto. Saving the node is
	// valid, but it must not displace direct or become a member of the group.
	t.Run("append", func(t *testing.T) {
		update, err := AddUIConfigNodes(current, nodes, true)
		if err != nil {
			t.Fatal(err)
		}
		root := uiNodeTestObject(t, update.Config)
		if update.Added != 1 || uiNodeTestMembers(root, "auto") != "direct" || uiNodeTestMembers(root, "proxy") != "auto" {
			t.Errorf("ineligible append removed the default fallback: %+v", update)
		}
		uiNodeDefaultTestPreserved(t, previous, root)
		uiNodeTestLocal(t, update.Config, "Via")
		if err := CheckConfig(update.Config); err != nil {
			t.Errorf("configuration with an ungrouped node: %v", err)
		}
	})
	t.Run("subscription", func(t *testing.T) {
		update := uiSubscriptionTestApply(t, current, nodes, `[]`, true)
		root := uiNodeTestObject(t, update.Config)
		if update.Added != 1 || update.Tags != `["Via"]` || uiNodeTestMembers(root, "auto") != "direct" || uiNodeTestMembers(root, "proxy") != "auto" {
			t.Errorf("ineligible subscription removed the default fallback: %+v", update)
		}
		uiNodeDefaultTestPreserved(t, previous, root)
		uiNodeTestLocal(t, update.Config)
		if err := CheckConfig(update.Config); err != nil {
			t.Errorf("configuration with an ungrouped subscription node: %v", err)
		}
	})
}
