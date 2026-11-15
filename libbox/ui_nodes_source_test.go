package libbox

import (
	"strings"
	"testing"
)

// uiNodeTestVia is uiNodeTestNode connecting through another node.
func uiNodeTestVia(tag, server, detour string) string {
	return strings.Replace(uiNodeTestNode(tag, server), `}`, `,"detour":"`+detour+`"}`, 1)
}

func TestUINodeLibraryDetours(t *testing.T) {
	update, err := AddUINodes("", "", "["+uiNodeTestVia("Hop", "198.51.100.1", "Exit")+","+uiNodeTestNode("Exit", "198.51.100.2")+"]")
	library := uiNodeTestAdd(t, "chain", update, err, 2, 0, "Hop,Exit")
	_, err = RemoveUINodes(library, `["Exit"]`)
	uiNodeTestFails(t, "remove a detour", err, "Exit is the detour of Hop")
	removed, err := RemoveUINodes(library, `["Exit","Hop","Unknown"]`)
	if value := uiNodeTestValue(t, "remove a chain", removed, err); value != `{"version":1,"sources":[],"nodes":[]}` {
		t.Errorf("a chain was removed to %s", value)
	}
	// A new name is carried into the nodes that connect through it.
	renamed, err := SetUINode(library, "Exit", uiNodeTestNode("Exit B", "198.51.100.3"))
	library = uiNodeTestValue(t, "rename", renamed, err)
	nodes := uiNodeTestObject(t, library).list("nodes")
	if uiNodeTestTags(nodes) != "Hop,Exit B" || uiNodeTestFind(nodes, "Hop").text("detour") != "Exit B" ||
		uiNodeTestFind(nodes, "Exit B").text("server") != "198.51.100.3" {
		t.Errorf("a renamed detour left %s", library)
	}
	added, err := SetUINode(library, "", uiNodeTestNode("Manual", "198.51.100.4"))
	library = uiNodeTestValue(t, "add by hand", added, err)
	if tags := uiNodeTestTags(uiNodeTestObject(t, library).list("nodes")); tags != "Hop,Exit B,Manual" {
		t.Errorf("a node entered by hand gave %s", tags)
	}
	_, err = SetUINode(library, "Exit B", uiNodeTestNode("Hop", "198.51.100.3"))
	uiNodeTestFails(t, "rename onto a node", err, "a node named Hop already exists")
	_, err = SetUINode(library, "", uiNodeTestNode("Hop", "198.51.100.5"))
	uiNodeTestFails(t, "add under a taken name", err, "a node named Hop already exists")
	_, err = SetUINode(library, "Missing", uiNodeTestNode("Missing", "198.51.100.5"))
	uiNodeTestFails(t, "edit a missing node", err, "node not found: Missing")
}

// A node of a subscription follows the name its detour was given, also when
// the subscription is read again.
func TestUINodeLibraryRenamedDetour(t *testing.T) {
	update, err := AddUINodes("", "", "["+uiNodeTestNode("Exit", "198.51.100.9")+"]")
	created, err := SetUINodeSource(uiNodeTestAdd(t, "by hand", update, err, 1, 0, "Exit"), `{"id":"sub"}`)
	library := uiNodeTestValue(t, "source", created, err)
	chain := "[" + uiNodeTestVia("Hop", "198.51.100.1", "Exit") + "," + uiNodeTestNode("Exit", "198.51.100.2") + "]"
	update, err = AddUINodes(library, "sub", chain)
	library = uiNodeTestAdd(t, "chain", update, err, 2, 0, "Exit,Hop,Exit 2")
	if detour := uiNodeTestFind(uiNodeTestObject(t, library).list("nodes"), "Hop").text("detour"); detour != "Exit 2" {
		t.Errorf("the node connects through %q", detour)
	}
	update, err = AddUINodes(library, "sub", chain)
	if repeated := uiNodeTestAdd(t, "repeated chain", update, err, 0, 2, "Exit,Hop,Exit 2"); repeated != library {
		t.Errorf("a repeated import turned %s into %s", library, repeated)
	}
	update, err = ReplaceUINodeSource(library, "sub", chain)
	if replaced := uiNodeTestAdd(t, "updated chain", update, err, 2, 0, "Exit,Hop,Exit 2"); replaced != library {
		t.Errorf("an unchanged update turned %s into %s", library, replaced)
	}
}

func TestUINodeLibrarySources(t *testing.T) {
	created, err := SetUINodeSource("", `{"id":"sub","name":"One"}`)
	created, err = SetUINodeSource(uiNodeTestValue(t, "source", created, err), `{"id":"other","name":"Two"}`)
	// What a source says beyond its id is the caller's and is kept as written.
	created, err = SetUINodeSource(uiNodeTestValue(t, "second source", created, err), `{"id":"sub","name":"Renamed","updated":12345678901234567890123}`)
	library := uiNodeTestValue(t, "edited source", created, err)
	if want := `{"version":1,"sources":[{"id":"sub","name":"Renamed","updated":12345678901234567890123},{"id":"other","name":"Two"}],"nodes":[]}`; library != want {
		t.Fatalf("sources are %s, want %s", library, want)
	}
	_, err = SetUINodeSource(library, `{"name":"Nameless"}`)
	uiNodeTestFails(t, "source without an id", err, "a node source needs an id")
	update, err := AddUINodes(library, "sub", "["+uiNodeTestNode("S1", "198.51.100.1")+"]")
	library = uiNodeTestAdd(t, "subscription", update, err, 1, 0, "S1")
	update, err = AddUINodes(library, "", "["+uiNodeTestVia("Hand", "198.51.100.2", "S1")+"]")
	_, err = RemoveUINodeSource(uiNodeTestAdd(t, "by hand", update, err, 1, 0, "S1,Hand"), "sub")
	uiNodeTestFails(t, "remove a source in use", err, "S1 is the detour of Hand")
	removed, err := RemoveUINodeSource(library, "sub")
	if value := uiNodeTestValue(t, "remove a source", removed, err); value != `{"version":1,"sources":[{"id":"other","name":"Two"}],"nodes":[]}` {
		t.Errorf("a source was removed to %s", value)
	}
	removed, err = RemoveUINodeSource(library, "nope")
	if value := uiNodeTestValue(t, "remove an unknown source", removed, err); value != library {
		t.Errorf("removing an unknown source turned %s into %s", library, value)
	}
}

func TestUINodeLibraryDocument(t *testing.T) {
	entry := `{"source":"","node":` + uiNodeTestNode("A", "198.51.100.1") + `}`
	for library, reason := range map[string]string{
		`{"version":2,"sources":[],"nodes":[]}`:                    "unsupported node library version",
		`{"sources":[],"nodes":[]}`:                                "unsupported node library version",
		`{"version":1,"sources":[{"id":"a"},{"id":"a"}]}`:          "invalid node library source",
		`{"version":1,"sources":[{"name":"a"}]}`:                   "invalid node library source",
		`{"version":1,"nodes":[` + entry + `,` + entry + `]}`:      "invalid node library entry",
		`{"version":1,"nodes":[{"source":"gone","node":{}}]}`:      "invalid node library entry",
		`{"version":1,"nodes":[{"source":"","node":{"tag":"A"}}]}`: "invalid node library entry",
		`{"version":1,"nodes":[1]}`:                                "invalid node library entry",
		`[]`:                                                       "decode node library: a JSON object is required",
	} {
		_, err := RemoveUINodes(library, `[]`)
		uiNodeTestFails(t, library, err, reason)
	}
	// Nothing stored yet is an empty library.
	for _, library := range []string{"", " \n"} {
		result, err := RemoveUINodes(library, `[]`)
		if value := uiNodeTestValue(t, "empty library", result, err); value != `{"version":1,"sources":[],"nodes":[]}` {
			t.Errorf("an empty library is %s", value)
		}
	}
}
