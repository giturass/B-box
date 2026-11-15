package libbox

import (
	"strings"
	"testing"
)

// uiNodeTestShared is a library written by hand, which nothing has checked.
func uiNodeTestShared(nodes ...string) string {
	entries := make([]string, len(nodes))
	for index, node := range nodes {
		entries[index] = `{"source":"","node":` + node + `}`
	}
	return `{"version":1,"sources":[],"nodes":[` + strings.Join(entries, ",") + `]}`
}

// uiNodeTestExpand returns the expanded configuration on one line.
func uiNodeTestExpand(t *testing.T, config, library string) string {
	t.Helper()
	expanded, err := ExpandUINodes(config, library)
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	return uiNodeTestText(uiNodeTestJSON(t, expanded.Value))
}

// uiNodeTestMembers joins the members of a group.
func uiNodeTestMembers(root *uiNodeObject, tag string) string {
	var members []string
	for _, item := range uiNodeTestFind(root.list("outbounds"), tag).list("outbounds") {
		member, _ := item.(string)
		members = append(members, member)
	}
	return strings.Join(members, ",")
}

func TestUINodeExpand(t *testing.T) {
	const direct = `{"type":"direct","tag":"direct"}`
	a, b := uiNodeTestNode("A", "198.51.100.1"), uiNodeTestNode("B", "198.51.100.2")
	own := uiNodeTestNode("A", "own.example.com")
	hop, exit := uiNodeTestVia("Hop", "198.51.100.3", "Exit"), uiNodeTestNode("Exit", "198.51.100.4")
	groups := func(proxy, auto string) string {
		return `{"outbounds":[` + direct + `,{"type":"selector","tag":"proxy","outbounds":[` + proxy +
			`],"default":"auto"},{"type":"urltest","tag":"auto","outbounds":[` + auto + `]}`
	}
	const route = `"route":{"rules":[{"domain_suffix":"example.com","outbound":"Hop"}],"final":"direct"}}`
	for _, item := range []struct{ name, config, library, want string }{
		{
			"every shared node in place of the empty member",
			groups(`"auto",""`, `""`) + `],"route":{"final":"proxy"}}`, uiNodeTestShared(a, b),
			groups(`"auto","A","B"`, `"A","B"`) + `,` + a + `,` + b + `],"route":{"final":"proxy"}}`,
		},
		{
			"no shared node",
			groups(`"auto",""`, `""`) + `]}`, "",
			groups(`"auto"`, `"direct"`) + `]}`,
		},
		{
			"no shared node and no direct outbound",
			`{"outbounds":[{"type":"urltest","tag":"direct","outbounds":[""]}]}`, uiNodeTestShared(),
			`{"outbounds":[{"type":"urltest","tag":"direct","outbounds":["direct 2"]},{"type":"direct","tag":"direct 2"}]}`,
		},
		{
			"members named beside the empty one",
			`{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["B","","direct",""]},` + direct + `]}`, uiNodeTestShared(a, b),
			`{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["B","A","direct"]},` + direct + `,` + a + `,` + b + `]}`,
		},
		{
			"a name the configuration defines itself",
			`{"outbounds":[` + own + `,{"type":"urltest","tag":"auto","outbounds":["A",""]}]}`, uiNodeTestShared(a, b),
			`{"outbounds":[` + own + `,{"type":"urltest","tag":"auto","outbounds":["A","B"]},` + b + `]}`,
		},
		{
			"a node named where no group takes every shared node",
			`{"outbounds":[` + direct + `],` + route, uiNodeTestShared(a, hop, exit),
			`{"outbounds":[` + direct + `,` + hop + `,` + exit + `],` + route,
		},
	} {
		if expanded := uiNodeTestExpand(t, item.config, item.library); expanded != item.want {
			t.Errorf("%s: expanded to %s, want %s", item.name, expanded, item.want)
		}
	}
}

// Only a node that can be dialled from the configuration becomes a member.
func TestUINodeExpandMembers(t *testing.T) {
	library := uiNodeTestShared(
		uiNodeTestVia("Hop", "198.51.100.1", "Exit"),
		uiNodeTestNode("Exit", "198.51.100.2"),
		uiNodeTestVia("Lost", "198.51.100.3", "Nowhere"),
		uiNodeTestVia("Loop", "198.51.100.4", "Back"),
		uiNodeTestVia("Back", "198.51.100.5", "Loop"),
		uiNodeTestVia("Grouped", "198.51.100.6", "auto"),
		uiNodeTestVia("Plain", "198.51.100.7", "direct"),
		`{"type":"nonesuch","tag":"Odd"}`,
		`{"type":"selector","tag":"Group","outbounds":["Exit"]}`,
		`{"type":"wireguard","tag":"WG","address":["10.0.0.2/32"],"private_key":"`+uiNodeTestKey(1)+`"}`,
	)
	config := `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"urltest","tag":"auto","outbounds":[""]}]}`
	root := uiNodeTestObject(t, uiNodeTestExpand(t, config, library))
	if members := uiNodeTestMembers(root, "auto"); members != "Hop,Exit,Plain,WG" {
		t.Errorf("members %q", members)
	}
	if tags := uiNodeTestTags(root.list("outbounds")); tags != "direct,auto,Hop,Exit,Plain" {
		t.Errorf("outbounds %q", tags)
	}
	if tags := uiNodeTestTags(root.list("endpoints")); tags != "WG" {
		t.Errorf("endpoints %q", tags)
	}
}

func TestUINodeExpandKeepsText(t *testing.T) {
	library := uiNodeTestShared(uiNodeTestNode("A", "198.51.100.1"))
	// A configuration that takes nothing from the library, or that the core
	// is to refuse in its own words, is returned as it was written.
	for _, config := range []string{
		"{ \"outbounds\": [ {\"type\": \"direct\", \"tag\": \"direct\"} ], // no shared node\n  \"route\": {\"final\": \"direct\"} }",
		`{"outbounds":{}}`,
		`{"outbounds":[],"endpoints":""}`,
		`not a configuration`,
		``,
	} {
		if expanded, err := ExpandUINodes(config, library); err != nil || expanded.Value != config {
			t.Errorf("%q expanded to %v, %v", config, expanded, err)
		}
	}
	_, err := ExpandUINodes(`{}`, `{"version":2}`)
	uiNodeTestFails(t, "unknown library", err, "unsupported node library version")
	// Numbers pass as they were written, beyond what a float64 holds.
	const group = `{"outbounds":[{"type":"urltest","tag":"auto","outbounds":["%"],"idle_timeout":9007199254740993}`
	big := strings.Replace(uiNodeTestNode("Big", "198.51.100.1"), "8388", "12345678901234567890123", 1)
	expanded, err := ExpandUINodes(strings.Replace(group, "%", "", 1)+`]}`, uiNodeTestShared(big))
	if err != nil {
		t.Fatal(err)
	}
	if want := strings.Replace(group, "%", "Big", 1) + `,` + big + `]}`; uiNodeTestText(uiNodeTestJSON(t, expanded.Value)) != want {
		t.Errorf("expanded to %s, want %s", expanded.Value, want)
	}
	// The configuration is written the way the editor shows it.
	if !strings.HasPrefix(expanded.Value, "{\n  \"outbounds\": [\n    {\n      \"type\": \"urltest\",") {
		t.Errorf("expanded as %s", expanded.Value)
	}
}
