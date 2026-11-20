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
	for _, item := range previous.list("outbounds") {
		node, _ := item.(*uiNodeObject)
		if node.text("type") == "direct" && !uiNodeEqual(node, uiNodeTestFind(updated.list("outbounds"), node.text("tag"))) {
			t.Errorf("node import changed direct outbound %s", node.text("tag"))
		}
	}
}

func TestUINodeDefaultAutoAppendRemovesDirectMembers(t *testing.T) {
	for _, item := range []struct {
		name             string
		defaultProfile   bool
		proxyNamedDirect bool
		wantAuto         string
	}{
		{"default", true, false, "Old,Second,New"},
		{"ordinary", false, false, "direct,Old,bypass,Second,New"},
		{"proxy named direct", true, true, "direct,Old,Second,New"},
	} {
		t.Run(item.name, func(t *testing.T) {
			previous := uiNodeDefaultTestRoot(t, `["direct","Old","bypass","Second"]`,
				uiNodeTestNode("Old", "198.51.100.1"),
				`{"type":"direct","tag":"bypass"}`,
				uiNodeTestNode("Second", "198.51.100.2"),
				`{"type":"urltest","tag":"custom","outbounds":["direct","Old"]}`)
			if item.proxyNamedDirect {
				key, index := uiNodeFind(previous, "direct")
				previous.list(key)[index] = uiNodeTestObject(t, uiNodeTestNode("direct", "198.51.100.3"))
			}
			proxy := uiNodeTestFind(previous.list("outbounds"), "proxy")
			proxy.set("outbounds", []any{"auto", "direct", "Old"})
			proxy.set("default", "direct")
			current := uiNodeTestText(previous)
			if err := CheckConfig(current); err != nil {
				t.Fatalf("current configuration: %v", err)
			}
			update, err := AddUIConfigNodes(current, `[`+uiNodeTestVia("New", "203.0.113.1", "direct")+`]`, item.defaultProfile)
			if err != nil {
				t.Fatal(err)
			}
			root := uiNodeTestObject(t, update.Config)
			if update.Added != 1 || update.Skipped != 0 || uiNodeTestMembers(root, "auto") != item.wantAuto {
				t.Errorf("automatic group after append: %+v", update)
			}
			if uiNodeTestMembers(root, "proxy") != "auto,direct,Old,New" ||
				uiNodeTestFind(root.list("outbounds"), "proxy").text("default") != "direct" ||
				uiNodeTestMembers(root, "custom") != "direct,Old,New" ||
				!uiNodeEqual(uiNodeTestFind(previous.list("outbounds"), "direct"), uiNodeTestFind(root.list("outbounds"), "direct")) {
				t.Errorf("append changed another group or the outbound named direct: %s", update.Config)
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
	previous := uiNodeDefaultTestRoot(t, `["direct","Via","bypass"]`,
		`{"type":"selector","tag":"upstream","outbounds":["Exit"]}`,
		uiNodeTestNode("Exit", "198.51.100.1"), via,
		`{"type":"direct","tag":"bypass"}`)
	current := uiNodeLocalPrefix + `["Via"]` + "\n" + uiNodeTestText(previous)
	if err := CheckConfig(current); err != nil {
		t.Fatalf("valid existing group detour: %v", err)
	}
	update, err := AddUIConfigNodes(current, `[`+via+`]`, true)
	if err != nil {
		t.Fatal(err)
	}
	root := uiNodeTestObject(t, update.Config)
	if update.Added != 0 || update.Skipped != 1 || uiNodeTestMembers(root, "auto") != "Via" ||
		uiNodeTestMembers(root, "proxy") != "auto" || uiNodeTestMembers(root, "upstream") != "Exit" ||
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
	for _, item := range []struct {
		name                        string
		defaultProfile              bool
		wantAddedAuto, wantNextAuto string
	}{
		{"default", true, "Old,Local,Other,New", "Local,Other,New,Replacement"},
		{"ordinary", false, "direct,Old,Local,bypass,Other,New", "direct,Local,bypass,Other,New,Replacement"},
	} {
		t.Run(item.name, func(t *testing.T) {
			previous := uiNodeDefaultTestRoot(t, `["direct","Old","Local","bypass","Other"]`,
				uiNodeTestNode("Old", "198.51.100.1"),
				uiNodeTestNode("Local", "198.51.100.2"),
				`{"type":"direct","tag":"bypass"}`,
				uiNodeTestNode("Other", "198.51.100.3"))
			current := uiNodeLocalPrefix + `["Local"]` + "\n" + uiNodeTestText(previous)
			added := uiSubscriptionTestApply(t, current, `[`+uiNodeTestNode("New", "203.0.113.1")+`]`, `[]`, item.defaultProfile)
			root := uiNodeTestObject(t, added.Config)
			if added.Added != 1 || added.Tags != `["New"]` || uiNodeTestMembers(root, "auto") != item.wantAddedAuto {
				t.Errorf("automatic group after adding a source: %+v", added)
			}
			uiNodeDefaultTestPreserved(t, previous, root)
			uiNodeTestLocal(t, added.Config, "Local")
			if err := CheckConfig(added.Config); err != nil {
				t.Errorf("configuration after adding a source: %v", err)
			}

			// A saved default can still have direct mixed with source and local nodes.
			uiNodeTestFind(root.list("outbounds"), "auto").set("outbounds", []any{"direct", "Old", "Local", "bypass", "Other", "New"})
			current = uiNodeLocalPrefix + `["Local"]` + "\n" + uiNodeTestText(root)
			subscription := `[` + uiNodeTestVia("Replacement", "203.0.113.2", "Other") + `]`
			updated := uiSubscriptionTestApply(t, current, subscription, `["Old"]`, item.defaultProfile)
			root = uiNodeTestObject(t, updated.Config)
			if updated.Added != 1 || updated.Tags != `["Replacement"]` ||
				uiNodeTestTags(root.list("outbounds")) != "direct,proxy,auto,Local,bypass,Other,New,Replacement" ||
				uiNodeTestMembers(root, "auto") != item.wantNextAuto ||
				uiNodeTestMembers(root, "proxy") != "auto,New,Replacement" {
				t.Errorf("source update did not preserve retained members: %+v", updated)
			}
			for _, tag := range []string{"Local", "Other", "New"} {
				if !uiNodeEqual(uiNodeTestFind(uiNodeTestObject(t, added.Config).list("outbounds"), tag), uiNodeTestFind(root.list("outbounds"), tag)) {
					t.Errorf("source update changed retained node %s", tag)
				}
			}
			uiNodeDefaultTestPreserved(t, previous, root)
			uiNodeTestLocal(t, updated.Config, "Local")
			if err := CheckConfig(updated.Config); err != nil {
				t.Errorf("configuration after source update: %v", err)
			}
			repeated := uiSubscriptionTestApply(t, updated.Config, subscription, updated.Tags, item.defaultProfile)
			if repeated.Config != updated.Config || repeated.Tags != updated.Tags || repeated.Added != 1 {
				t.Errorf("repeated source update changed the configuration: %+v", repeated)
			}
		})
	}
}

func TestUINodeDefaultAutoKeepsFallbackWithoutEligibleMembers(t *testing.T) {
	for _, action := range []string{"append", "new source", "source refresh"} {
		t.Run(action, func(t *testing.T) {
			current := GetUIDefaultConfig().Value
			previousTags := `[]`
			if action == "source refresh" {
				current = uiNodeTestText(uiNodeDefaultTestRoot(t, `["direct","Via"]`, uiNodeTestNode("Via", "198.51.100.1")))
				previousTags = `["Via"]`
			}
			previous := uiNodeTestObject(t, current)
			nodes := `[` + uiNodeTestVia("Via", "203.0.113.1", "auto") + `]`
			// Via must remain outside auto because selecting it would return to auto.
			var config string
			if action == "append" {
				update, err := AddUIConfigNodes(current, nodes, true)
				if err != nil {
					t.Fatal(err)
				}
				if update.Added != 1 {
					t.Errorf("ungrouped node was not saved: %+v", update)
				}
				config = update.Config
				uiNodeTestLocal(t, config, "Via")
			} else {
				update := uiSubscriptionTestApply(t, current, nodes, previousTags, true)
				if update.Added != 1 || update.Tags != `["Via"]` {
					t.Errorf("ungrouped source node was not saved: %+v", update)
				}
				config = update.Config
				uiNodeTestLocal(t, config)
			}
			root := uiNodeTestObject(t, config)
			if uiNodeTestMembers(root, "auto") != "direct" || uiNodeTestMembers(root, "proxy") != "auto" {
				t.Errorf("ineligible node displaced the default fallback: %s", config)
			}
			uiNodeDefaultTestPreserved(t, previous, root)
			if err := CheckConfig(config); err != nil {
				t.Errorf("configuration with an ungrouped node: %v", err)
			}
		})
	}
}

func TestUINodeDefaultAutoLastNodeRemovalRestoresDirect(t *testing.T) {
	previous := uiNodeDefaultTestRoot(t, `["direct","Old","bypass"]`,
		uiNodeTestNode("Old", "198.51.100.1"), `{"type":"direct","tag":"bypass"}`)
	update, err := AddUIConfigNodes(uiNodeTestText(previous), `[`+uiNodeTestNode("New", "203.0.113.1")+`]`, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := uiNodeTestMembers(uiNodeTestObject(t, update.Config), "auto"); got != "Old,New" {
		t.Fatalf("automatic group before deletion: %s", got)
	}
	removed, err := RemoveUIConfigNodes(update.Config, `["Old","New"]`, true)
	config := uiNodeTestValue(t, "remove the last default nodes", removed, err)
	root := uiNodeTestObject(t, config)
	if uiNodeTestMembers(root, "auto") != "direct" || uiNodeTestMembers(root, "proxy") != "auto" ||
		uiNodeTestTags(root.list("outbounds")) != "direct,proxy,auto,bypass" {
		t.Errorf("last node removal did not restore the empty default: %s", config)
	}
	uiNodeDefaultTestPreserved(t, previous, root)
	uiNodeTestLocal(t, config)
	if err := CheckConfig(config); err != nil {
		t.Errorf("configuration after last node removal: %v", err)
	}
}
