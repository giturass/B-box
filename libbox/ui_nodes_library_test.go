package libbox

import (
	"strings"
	"testing"
)

// uiNodeTestNode is a node every build of the core constructs.
func uiNodeTestNode(tag, server string) string {
	return `{"type":"shadowsocks","tag":"` + tag + `","server":"` + server + `","server_port":8388,"method":"aes-256-gcm","password":"password"}`
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

// uiNodeTestAdd checks what was added and returns the library.
func uiNodeTestAdd(t *testing.T, name string, update *UINodeUpdate, err error, added, skipped int32, tags string) string {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if update.Added != added || update.Skipped != skipped {
		t.Errorf("%s: added %d and skipped %d, want %d and %d", name, update.Added, update.Skipped, added, skipped)
	}
	if found := uiNodeTestTags(uiNodeTestObject(t, update.Library).list("nodes")); found != tags {
		t.Errorf("%s: nodes %q, want %q", name, found, tags)
	}
	return update.Library
}

func TestUINodeLibraryAdd(t *testing.T) {
	const source = `{"id":"sub","name":"Subscription","url":"https://example.com/sub"}`
	created, err := SetUINodeSource("", source)
	library := uiNodeTestValue(t, "source", created, err)
	if want := `{"version":1,"sources":[` + source + `],"nodes":[]}`; library != want {
		t.Fatalf("library with a source is %s, want %s", library, want)
	}
	shared := "[" + uiNodeTestNode("Shared", "198.51.100.1") + "," + uiNodeTestNode("Shared", "198.51.100.2") + "]"
	update, err := AddUINodes(library, "", "["+uiNodeTestNode("Manual", "198.51.100.9")+"]")
	library = uiNodeTestAdd(t, "by hand", update, err, 1, 0, "Manual")
	update, err = AddUINodes(library, "sub", shared)
	library = uiNodeTestAdd(t, "subscription", update, err, 2, 0, "Manual,Shared,Shared 2")
	// The same nodes again are the ones the source already holds.
	update, err = AddUINodes(library, "sub", shared)
	if repeated := uiNodeTestAdd(t, "repeated", update, err, 0, 2, "Manual,Shared,Shared 2"); repeated != library {
		t.Errorf("a repeated import turned %s into %s", library, repeated)
	}
	// A name held by another source is taken, and a node joins its own kind.
	update, err = AddUINodes(library, "", "["+uiNodeTestNode("Shared", "198.51.100.3")+"]")
	library = uiNodeTestAdd(t, "taken name", update, err, 1, 0, "Manual,Shared 3,Shared,Shared 2")
	// An update takes the place the nodes of the source had.
	update, err = ReplaceUINodeSource(library, "sub", "["+uiNodeTestNode("New", "198.51.100.4")+","+uiNodeTestNode("Shared", "198.51.100.1")+"]")
	library = uiNodeTestAdd(t, "update", update, err, 2, 0, "Manual,Shared 3,New,Shared")
	var owners []string
	for _, item := range uiNodeTestObject(t, library).list("nodes") {
		entry, _ := item.(*uiNodeObject)
		owners = append(owners, entry.object("node").text("tag")+"@"+entry.text("source"))
	}
	if found := strings.Join(owners, ","); found != "Manual@,Shared 3@,New@sub,Shared@sub" {
		t.Errorf("nodes belong to %s", found)
	}
	_, err = ReplaceUINodeSource(library, "sub", "[]")
	uiNodeTestFails(t, "empty update", err, "the subscription contains no usable node")
	_, err = AddUINodes(library, "nope", "[]")
	uiNodeTestFails(t, "unknown source", err, "unknown node source: nope")
	_, err = ReplaceUINodeSource(library, "", shared)
	uiNodeTestFails(t, "update of the nodes entered by hand", err, "unknown node source: ")
	// What ParseUINodes returns is taken as it is.
	parsed, err := ParseUINodes(uiNodeTestList)
	if err != nil {
		t.Fatal(err)
	}
	update, err = AddUINodes("", "", parsed.Value)
	uiNodeTestAdd(t, "parsed result", update, err, 3, 0, "SS One,Trojan One,VLESS One")
}

func TestUINodeLibraryRefusesNodes(t *testing.T) {
	const secret = `"password":"SECRETSECRET"`
	for _, item := range []struct{ name, node, reason string }{
		{"no name", `{"type":"shadowsocks","server":"198.51.100.1","server_port":8388,"method":"aes-256-gcm",` + secret + `}`, "a node needs a name"},
		{"group", `{"type":"selector","tag":"Group","outbounds":["a"]}`, "Group: selector is not a proxy node type"},
		{"direct", `{"type":"direct","tag":"Direct"}`, "Direct: direct is not a proxy node type"},
		{"unknown type", `{"type":"nonesuch","tag":"Odd",` + secret + `}`, "Odd: nonesuch is not a proxy node type"},
		// The core says the rest in words of its own.
		{"unknown option", `{"type":"shadowsocks","tag":"Option","server":"198.51.100.1","server_port":8388,"method":"aes-256-gcm","nonesuch":true,` + secret + `}`, "Option: "},
		{"unknown cipher", `{"type":"shadowsocks","tag":"Cipher","server":"198.51.100.1","server_port":8388,"method":"nonesuch",` + secret + `}`, "Cipher: "},
	} {
		_, added := AddUINodes("", "", "["+uiNodeTestNode("Fine", "198.51.100.1")+","+item.node+"]")
		_, edited := SetUINode("", "", item.node)
		exact := !strings.HasSuffix(item.reason, ": ")
		for _, err := range []error{added, edited} {
			if err == nil {
				t.Errorf("%s: the node was taken", item.name)
			} else if reason := err.Error(); exact && reason != item.reason ||
				!exact && (!strings.HasPrefix(reason, item.reason) || reason == item.reason) ||
				strings.Contains(reason, "SECRET") || strings.Contains(reason, "node-") {
				t.Errorf("%s: refused with %q, want %q", item.name, reason, item.reason)
			}
		}
	}
}

func TestUINodeLibraryMerge(t *testing.T) {
	held := func(source, node string) string { return `{"source":"` + source + `","node":` + node + `}` }
	current := `{"version":1,"sources":[{"id":"sub","name":"Old"},{"id":"kept","name":"Kept"}],"nodes":[` +
		held("", uiNodeTestNode("Manual", "198.51.100.1")) + "," + held("sub", uiNodeTestNode("S1", "198.51.100.2")) + "," +
		held("kept", uiNodeTestNode("K1", "198.51.100.3")) + `]}`
	backup := `{"version":1,"sources":[{"id":"sub","name":"New"},{"id":"more","name":"More"}],"nodes":[` +
		held("more", uiNodeTestNode("M1", "198.51.100.4")) + "," + held("sub", uiNodeTestNode("Manual", "198.51.100.5")) + `]}`
	// An empty library becomes the one that comes in.
	written, err := RemoveUINodes(backup, `[]`)
	alone, mergeErr := MergeUINodes("", backup)
	if value := uiNodeTestValue(t, "into nothing", alone, mergeErr); value != uiNodeTestValue(t, "backup", written, err) {
		t.Errorf("an empty library became %s", value)
	}
	// What comes in replaces what is held under that id or name, in its place.
	merged, err := MergeUINodes(current, backup)
	library := uiNodeTestObject(t, uiNodeTestValue(t, "merge", merged, err))
	var sources, owners []string
	for _, item := range library.list("sources") {
		source, _ := item.(*uiNodeObject)
		sources = append(sources, source.text("id")+":"+source.text("name"))
	}
	for _, item := range library.list("nodes") {
		entry, _ := item.(*uiNodeObject)
		owners = append(owners, entry.object("node").text("tag")+"@"+entry.text("source"))
	}
	if found := strings.Join(sources, ","); found != "sub:New,kept:Kept,more:More" {
		t.Errorf("sources are %s", found)
	}
	if found := strings.Join(owners, ","); found != "Manual@sub,S1@sub,K1@kept,M1@more" {
		t.Errorf("nodes belong to %s", found)
	}
	if server := uiNodeTestFind(library.list("nodes"), "Manual").text("server"); server != "198.51.100.5" {
		t.Errorf("the replaced node connects to %s", server)
	}
	// Nothing comes in unchecked.
	_, err = MergeUINodes(current, `{"version":1,"nodes":[{"source":"","node":{"type":"direct","tag":"Direct"}}]}`)
	uiNodeTestFails(t, "a group in a backup", err, "Direct: direct is not a proxy node type")
	_, err = MergeUINodes(current, `{"version":2}`)
	uiNodeTestFails(t, "an unknown backup", err, "unsupported node library version")
	_, err = MergeUINodes(`{"version":2}`, backup)
	uiNodeTestFails(t, "an unknown library", err, "unsupported node library version")
}
