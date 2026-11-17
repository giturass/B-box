package libbox

import (
	"encoding/base64"
	"strings"
	"testing"
)

// The nodes of these tests are ones every build of the core constructs.
const uiNodeTestList = `# a subscription
ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ@198.51.100.1:8388#SS%20One
trojan://password@trojan.example.com:443?sni=sni.example.com#Trojan One
a line without a link
shared with you: vless://` + uiNodeTestUUID + `@vless.example.com:443?security=tls&sni=sni.example.com&type=ws&path=/ws#VLESS One
`

func uiNodeTestResult(t *testing.T, name string, result *uiNodeObject, format, tags, skipped, hint string) {
	t.Helper()
	if result.text("format") != format {
		t.Errorf("%s: format %q, want %q", name, result.text("format"), format)
	}
	if found := uiNodeTestTags(result.list("nodes")); found != tags {
		t.Errorf("%s: nodes %q, want %q", name, found, tags)
	}
	if found := uiNodeTestSkipped(result); found != skipped {
		t.Errorf("%s: skipped %q, want %q", name, found, skipped)
	}
	if result.text("hint") != hint {
		t.Errorf("%s: hint %q, want %q", name, result.text("hint"), hint)
	}
}

func TestUINodeParseLinkList(t *testing.T) {
	for name, content := range map[string]string{
		"plain":           uiNodeTestList,
		"URL base64":      base64.RawURLEncoding.EncodeToString([]byte(uiNodeTestList)),
		"standard base64": base64.StdEncoding.EncodeToString([]byte(uiNodeTestList)) + "\n",
		"wrapped base64":  strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(uiNodeTestList)), "W", "W\r\n"),
	} {
		result := uiNodeTestParse(t, content)
		uiNodeTestResult(t, name, result, "links", "SS One,Trojan One,VLESS One", "", "")
		node := uiNodeTestFind(result.list("nodes"), "VLESS One")
		if node.text("server") != "vless.example.com" || node.object("transport").text("path") != "/ws" {
			t.Errorf("%s: the quoted link was read as %s", name, uiNodeTestText(node))
		}
	}
}

func TestUINodeParseLinkListLeavesOut(t *testing.T) {
	result := uiNodeTestParse(t, "ssr://U0VDUkVU#Old\n"+
		"vless://SECRET@refused.example:443?type=kcp&security=tls#Split\n"+
		"ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ@198.51.100.1:8388#Kept\n"+
		"ss://nonesuch:SECRETSECRET@198.51.100.2:8388#Cipher\n")
	uiNodeTestResult(t, "list", result, "links", "Kept", uiNodeTestSkipped(result), "")
	skipped := result.list("skipped")
	if len(skipped) != 3 {
		t.Fatalf("left out %s", uiNodeTestText(skipped))
	}
	for index, want := range []struct{ name, reason string }{
		{"Old", "ShadowsocksR was removed from the core"},
		{"Split", "the transport of this node is not supported by this core"},
		{"Cipher", ""},
	} {
		entry, _ := skipped[index].(*uiNodeObject)
		reason := entry.text("reason")
		if entry.text("name") != want.name || want.reason != "" && reason != want.reason {
			t.Errorf("left out %s, want %s: %s", uiNodeTestText(entry), want.name, want.reason)
		}
		// The core refuses the unknown cipher in words of its own.
		if reason == "" || strings.Contains(reason, "SECRET") || strings.Contains(reason, "node-") {
			t.Errorf("reason of %s: %q", want.name, reason)
		}
	}
	// A list of nothing but refused links is still a result.
	result = uiNodeTestParse(t, "ssr://U0VDUkVU#Old")
	uiNodeTestResult(t, "refused", result, "links", "", "Old: ShadowsocksR was removed from the core", "")
	if !strings.Contains(uiNodeTestText(result), `"nodes":[]`) {
		t.Errorf("no empty node list in %s", uiNodeTestText(result))
	}
}

func TestUINodeParseUnknown(t *testing.T) {
	for _, content := range []string{
		"",
		"   \n",
		"just some text, nothing else.",
		"https://example.com/subscription",
		`{"hello":"world"}`,
		`[1,2]`,
		`{"servers":[{"address":"1.1.1.1"}]}`,
		"[Interface]\nAddress = 10.0.0.2/32\n",
	} {
		if result, err := ParseUINodes(content); err == nil {
			t.Errorf("%q was read as %s", content, result.Value)
		} else if err.Error() != "the text is not in a known form of proxy nodes" {
			t.Errorf("%q: %v", content, err)
		}
	}
}
