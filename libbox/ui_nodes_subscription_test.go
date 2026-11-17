package libbox

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestUINodeSubscriptionSourceReportsUnsupportedNodes(t *testing.T) {
	current := uiNodeLocalPrefix + `["Local"]` + "\n" + `{"outbounds":[{"type":"direct","tag":"direct"},
		{"type":"selector","tag":"proxy","outbounds":["Local","Other"]},` +
		uiNodeTestNode("Local", "198.51.100.1") + `,` + uiNodeTestNode("Other", "198.51.100.2") + `],"route":{"final":"proxy"}}`
	valid := "ss://aes-256-gcm:password@203.0.113.1:8388#New"
	for _, item := range []struct{ name, body string }{
		{"plain", valid + "\nvless://SECRETSECRET@203.0.113.2:443?type=kcp&security=tls#SECRETNAME\nvless://SECRETSECRET@203.0.113.3:443?type=kcp#SECRETNAME"},
		{"Clash", "proxies:\n  - {name: New, type: ss, server: 203.0.113.1, port: 8388, cipher: aes-256-gcm, password: password}\n  - {name: SECRETNAME, type: vless, server: 203.0.113.2, port: 443, uuid: SECRETSECRET, network: kcp}\n  - {name: SECRETNAME, type: vless, server: 203.0.113.3, port: 443, uuid: SECRETSECRET, network: kcp}\n"},
	} {
		for _, encoded := range []bool{false, true} {
			body := item.body
			if encoded {
				body = base64.StdEncoding.EncodeToString([]byte(body))
			}
			updated := uiSubscriptionTestApply(t, current, body, `[]`, false)
			if updated.Added != 1 || updated.Tags != `["New"]` || updated.Skipped != 2 ||
				updated.Warnings != `[{"reason":"the transport of this node is not supported by this core","count":2}]` ||
				strings.Contains(updated.Warnings, "SECRET") {
				t.Errorf("%s encoded=%v: unsupported nodes were not reported safely: %+v", item.name, encoded, updated)
			}
			root := uiNodeTestObject(t, updated.Config)
			if uiNodeTestTags(root.list("outbounds")) != "direct,proxy,Local,Other,New" {
				t.Errorf("%s encoded=%v: retained nodes changed: %s", item.name, encoded, updated.Config)
			}
			uiNodeTestLocal(t, updated.Config, "Local")
			if err := CheckConfig(updated.Config); err != nil {
				t.Errorf("%s encoded=%v: supported configuration: %v", item.name, encoded, err)
			}
			repeated := uiSubscriptionTestApply(t, updated.Config, body, updated.Tags, false)
			if repeated.Config != updated.Config || repeated.Warnings != updated.Warnings || repeated.Skipped != 2 {
				t.Errorf("%s encoded=%v: repeated mixed source update changed ownership/report", item.name, encoded)
			}
			clean := uiSubscriptionTestApply(t, repeated.Config, valid, repeated.Tags, false)
			if clean.Skipped != 0 || clean.Warnings != `[]` || clean.Config != repeated.Config {
				t.Errorf("%s encoded=%v: resolved warnings were not cleared: %+v", item.name, encoded, clean)
			}
		}
	}
	for _, body := range []string{
		"vless://SECRETSECRET@203.0.113.2:443?type=kcp#SECRETNAME",
		valid + "\nvless://SECRETSECRET@203.0.113.2:443?type=kcp\nss://nonesuch:SECRETSECRET@203.0.113.3:8388#Invalid",
	} {
		result, err := ApplyUISubscriptionNodes(current, body, `["Other"]`, false)
		if result != nil || err == nil || strings.Contains(err.Error(), "SECRETSECRET") {
			t.Errorf("empty supported batch or damaged node was accepted or leaked credentials: result=%v, err=%v", result, err)
		}
	}
	// The legacy StringBox binding cannot show a warning report, so it keeps
	// rejecting partial updates instead of silently applying them.
	if result, err := UpdateUISubscriptionNodes(current, valid+"\nvless://SECRET@203.0.113.2:443?type=kcp"); result != nil || err == nil || !strings.Contains(err.Error(), "unsupported proxy nodes") {
		t.Errorf("legacy update silently dropped unsupported nodes: result=%v, err=%v", result, err)
	}
}

func TestUINodeSubscriptionUnsupportedDetoursDoNotRebindRetainedNames(t *testing.T) {
	for _, local := range []bool{false, true} {
		for _, alias := range []string{"Exit", "203.0.113.2:443"} {
			current := `{"outbounds":[{"type":"direct","tag":"direct"},
				{"type":"selector","tag":"proxy","outbounds":["Owned","` + alias + `"]},` +
				uiNodeTestNode("Owned", "198.51.100.1") + `,` + uiNodeTestNode(alias, "198.51.100.2") + `],"route":{"final":"proxy"}}`
			if local {
				current = uiNodeLocalPrefix + `["` + alias + `"]` + "\n" + current
			}
			name := ""
			if alias == "Exit" {
				name = "name: Exit, "
			}
			hop := "  - {name: Hop, type: ss, server: 203.0.113.1, port: 8388, cipher: aes-256-gcm, password: password, dialer-proxy: '" + alias + "'}\n"
			refused := "  - {" + name + "type: vless, server: 203.0.113.2, port: 443, uuid: " + uiNodeTestUUID + ", network: kcp}\n"
			for _, sameBatchAlias := range []bool{false, true} {
				body := "proxies:\n" + hop + refused
				if sameBatchAlias {
					body += "  - {name: '" + alias + "', type: ss, server: 203.0.113.3, port: 8388, cipher: aes-256-gcm, password: password}\n"
				}
				for _, encoded := range []bool{false, true} {
					subscription := body
					if encoded {
						subscription = base64.StdEncoding.EncodeToString([]byte(body))
					}
					result, err := ApplyUISubscriptionNodes(current, subscription, `["Owned"]`, false)
					if result != nil || err == nil || !strings.Contains(err.Error(), "unsupported or unnamed subscription node") {
						t.Errorf("skipped detour silently rebound: local=%v, alias=%s, same-batch=%v, base64=%v, result=%v, err=%v", local, alias, sameBatchAlias, encoded, result, err)
					}
				}
			}
			// A named, unrelated unsupported proxy must not prevent a node
			// from deliberately dialing through the retained configuration.
			body := "proxies:\n" + hop + strings.Replace(refused, "{"+name+"type:", "{name: Unrelated, type:", 1)
			updated := uiSubscriptionTestApply(t, current, body, `["Owned"]`, false)
			if updated.Skipped != 1 || updated.Tags != `["Hop"]` ||
				uiNodeTestFind(uiNodeTestObject(t, updated.Config).list("outbounds"), "Hop").text("detour") != alias {
				t.Errorf("unrelated unsupported node changed an intentional retained detour: %+v", updated)
			}
		}
	}
}

func TestUINodeSubscriptionRejectsMalformedClashProtocols(t *testing.T) {
	valid := `{"name":"New","type":"ss","server":"203.0.113.1","port":8388,"cipher":"aes-256-gcm","password":"password"}`
	for _, invalid := range []string{`{}`, `{"type":""}`, `{"type":"  "}`, `{"type":null}`, `{"type":12}`, `{"type":true}`} {
		for _, body := range []string{
			`{"proxies":[` + valid + `,` + invalid + `]}`,
			"proxies:\n  - " + valid + "\n  - " + invalid + "\n",
		} {
			for _, encoded := range []bool{false, true} {
				subscription := body
				if encoded {
					subscription = base64.StdEncoding.EncodeToString([]byte(body))
				}
				result, err := ApplyUISubscriptionNodes(uiNodeDefaultConfig, subscription, `[]`, true)
				if result != nil || err == nil || !strings.Contains(err.Error(), "proxy protocol is missing") {
					t.Errorf("damaged Clash proxy was treated as a skipped capability: base64=%v, invalid=%s, result=%v, err=%v", encoded, invalid, result, err)
				}
			}
		}
	}
}

func TestUINodeSubscriptionRefusedVMessUsesItsJSONName(t *testing.T) {
	for _, name := range []string{"Exit", ""} {
		body := `{"ps":"` + name + `","add":"203.0.113.2","port":443,"id":"` + uiNodeTestUUID + `","net":"kcp"}`
		link := "vmess://" + base64.StdEncoding.EncodeToString([]byte(body)) + "#Outer"
		parsed := uiSubscriptionParse(baseContext(nil), link)
		if parsed == nil || len(parsed.skipped) != 1 {
			t.Fatalf("unsupported VMess JSON was not reported")
		}
		entry, _ := parsed.skipped[0].(*uiNodeObject)
		if entry.text("name") != name || entry.get("unsupported") != true {
			t.Errorf("VMess refusal used its ignored outer fragment instead of its actual name: %s", uiNodeTestText(entry))
		}
	}
}

func uiSubscriptionTestUpdate(t *testing.T, current, subscription string) string {
	t.Helper()
	result, err := UpdateUISubscriptionNodes(current, subscription)
	return uiNodeTestValue(t, "subscription update", result, err)
}

func uiSubscriptionTestApply(t *testing.T, current, subscription, tags string, defaultProfile bool) *UISubscriptionNodeUpdate {
	t.Helper()
	result, err := ApplyUISubscriptionNodes(current, subscription, tags, defaultProfile)
	if err != nil || result == nil {
		t.Fatalf("apply subscription source: %v", err)
	}
	return result
}

func TestUINodeSubscriptionKeepConfiguration(t *testing.T) {
	current := uiNodeLocalPrefix + `["Local"]` + "\n" + `{
		"log":{"disabled":true},
		"dns":{"servers":[{"type":"local","tag":"system"}],"final":"system"},
		"inbounds":[{"type":"mixed","tag":"local-in","listen":"127.0.0.1","listen_port":6080}],
		"outbounds":[{"type":"direct","tag":"direct"},
			{"type":"selector","tag":"proxy","outbounds":["auto","Old","Local"],"default":"Old"},
			{"type":"urltest","tag":"auto","outbounds":["Old"]},` +
		uiNodeTestNode("Old", "198.51.100.1") + `,` + uiNodeTestNode("Local", "198.51.100.2") + `],
		"endpoints":[{"type":"tailscale","tag":"tail","hostname":"local-tail"}],
		"route":{"final":"proxy","rules":[{"domain_suffix":"example.test","outbound":"direct"}]},
		"experimental":{"cache_file":{"enabled":true}},
		"future":{"integer":12345678901234567890123,"text":"<keep>"}
	}`
	subscription := uiNodeLocalPrefix + `["New"]` + "\n" + `{
		"log":{"level":"debug"},"dns":{"final":"remote-dns"},
		"inbounds":[{"type":"tun","tag":"remote-in"}],"route":{"final":"remote-group"},
		"outbounds":[{"type":"direct","tag":"remote-direct"},
			{"type":"selector","tag":"remote-group","outbounds":["New"]},` +
		strings.Replace(uiNodeTestNode("New", "203.0.113.1"), `}`, `,"domain_resolver":"remote-dns"}`, 1) + `],
		"endpoints":[{"type":"tailscale","tag":"remote-tail"}],
		"processing":{"scripts":[{"source":"remote-script"}]},"proxy-providers":{"remote":{}}
	}`
	config := uiSubscriptionTestUpdate(t, current, subscription)
	previous, root := uiNodeTestObject(t, current), uiNodeTestObject(t, config)
	for _, key := range previous.keys {
		if key != "outbounds" && key != "endpoints" && !uiNodeEqual(previous.get(key), root.get(key)) {
			t.Errorf("subscription changed local %s: %s", key, config)
		}
	}
	if strings.Join(root.keys, ",") != strings.Join(previous.keys, ",") ||
		uiNodeTestTags(root.list("outbounds")) != "direct,proxy,auto,Local,New" ||
		uiNodeTestTags(root.list("endpoints")) != "tail" ||
		uiNodeTestMembers(root, "proxy") != "auto,Local,New" || uiNodeTestMembers(root, "auto") != "New" ||
		uiNodeTestFind(root.list("outbounds"), "proxy").get("default") != nil ||
		uiNodeTestFind(root.list("outbounds"), "New").get("domain_resolver") != nil {
		t.Errorf("subscription imported settings or left old nodes: %s", config)
	}
	uiNodeTestLocal(t, config, "Local")
	if repeated := uiSubscriptionTestUpdate(t, config, subscription); repeated != config {
		t.Errorf("repeated subscription changed the document: %s", repeated)
	}
}

func TestUINodeSubscriptionReadSupportedFormats(t *testing.T) {
	link := "ss://aes-256-gcm:password@198.51.100.1:8388#New"
	json := `{"outbounds":[` + uiNodeTestNode("New", "198.51.100.1") + `]}`
	mixed := strings.TrimSuffix(json, "}") + `,"proxy-providers":{"ignored":{"url":"https://example.test/nodes"}},"proxies":[{"type":"ssr","name":"Ignored"}]}`
	for _, item := range []struct{ name, subscription string }{
		{"link", link},
		{"base64 links", base64.StdEncoding.EncodeToString([]byte(link))},
		{"base64 JSON", base64.RawURLEncoding.EncodeToString([]byte(json))},
		{"sing-box", json},
		{"sing-box with ignored provider and Clash proxies", mixed},
		{"base64 sing-box with ignored provider", base64.StdEncoding.EncodeToString([]byte(mixed))},
		{"node array", `[` + uiNodeTestNode("New", "198.51.100.1") + `]`},
		{"SIP008", `{"version":1,"servers":[{"remarks":"New","server":"198.51.100.1","server_port":8388,"method":"aes-256-gcm","password":"password"}]}`},
		{"Clash", "proxies:\n  - {name: New, type: ss, server: 198.51.100.1, port: 8388, cipher: aes-256-gcm, password: password}\nproxy-groups:\n  - {name: ignored-group, type: select, proxies: [New]}\nproxy-providers:\n  source: {type: http, url: https://example.test/nodes}\ndns: {enable: true}\nrules: [MATCH,ignored-group]\n"},
	} {
		t.Run(item.name, func(t *testing.T) {
			config := uiSubscriptionTestUpdate(t, "", item.subscription)
			root := uiNodeTestObject(t, config)
			defaults := uiNodeTestObject(t, uiNodeDefaultConfig)
			for _, key := range defaults.keys {
				if key != "outbounds" && !uiNodeEqual(defaults.get(key), root.get(key)) {
					t.Errorf("new subscription changed default %s", key)
				}
			}
			if uiNodeTestTags(root.list("outbounds")) != "direct,proxy,auto,New" ||
				uiNodeTestMembers(root, "proxy") != "auto,New" || uiNodeTestMembers(root, "auto") != "New" {
				t.Errorf("subscription did not use the default groups: %s", config)
			}
			uiNodeTestLocal(t, config)
			if err := CheckConfig(config); err != nil {
				t.Errorf("new subscription configuration: %v", err)
			}
		})
	}
}

func TestUINodeSubscriptionRejectInvalidAndEmptyUpdates(t *testing.T) {
	valid := uiNodeTestNode("Valid", "198.51.100.1")
	for _, item := range []struct{ name, subscription, reason string }{
		{"empty", "", "not in a known form"},
		{"unknown document", `{}`, "not in a known form"},
		{"malformed JSON", `{"outbounds":[`, "not in a known form"},
		{"no nodes", `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"group","outbounds":["direct"]}],"endpoints":[{"type":"tailscale","tag":"tail"}]}`, "no usable proxy nodes"},
		{"providers only", "proxy-providers:\n  source: {type: http, url: https://example.test/nodes}\n", "providers are not imported"},
		{"unknown protocol", `{"outbounds":[` + valid + `,{"type":"nonesuch","tag":"Unknown"}]}`, "Unknown: this protocol is not supported"},
		{"invalid options", `{"outbounds":[` + valid + `,` + strings.Replace(uiNodeTestNode("Invalid", "198.51.100.2"), `}`, `,"nonesuch":true}`, 1) + `]}`, "Invalid:"},
		{"invalid cipher", `{"outbounds":[` + valid + `,` + strings.Replace(uiNodeTestNode("Cipher", "198.51.100.2"), "aes-256-gcm", "nonesuch", 1) + `]}`, "Cipher:"},
		{"mixed node array", `[` + valid + `,null,{}]`, "subscription node list is not a proxy node"},
		{"base64 mixed node array", base64.StdEncoding.EncodeToString([]byte(`[` + valid + `,null,{}]`)), "subscription node list is not a proxy node"},
		{"mixed SIP008 servers", `{"servers":[{"remarks":"Valid","server":"198.51.100.1","server_port":8388,"method":"aes-256-gcm","password":"password"},null,{}]}`, "subscription SIP008 server list is not a proxy node"},
		{"missing detour", `{"outbounds":[` + uiNodeTestVia("Hop", "198.51.100.1", "Absent") + `]}`, "missing outbound Absent"},
		{"group detour", `{"outbounds":[{"type":"selector","tag":"group","outbounds":["Hop"]},` + uiNodeTestVia("Hop", "198.51.100.1", "group") + `]}`, "discarded subscription group group"},
		{"cyclic detours", `{"outbounds":[` + uiNodeTestVia("One", "198.51.100.1", "Two") + `,` + uiNodeTestVia("Two", "198.51.100.2", "One") + `]}`, "detour"},
	} {
		t.Run(item.name, func(t *testing.T) {
			result, err := UpdateUISubscriptionNodes(`{}`, item.subscription)
			if result != nil || err == nil || !strings.Contains(err.Error(), item.reason) {
				t.Errorf("invalid update: result=%v, err=%v, want %s", result, err, item.reason)
			}
		})
	}
	secret := strings.Replace(uiNodeTestNode("Private", "198.51.100.1"), `"password":"password"`, `"password":"SECRETSECRET"`, 1)
	secret = strings.Replace(secret, "aes-256-gcm", "nonesuch", 1)
	_, err := UpdateUISubscriptionNodes(`{}`, `{"outbounds":[`+secret+`]}`)
	if err == nil || strings.Contains(err.Error(), "SECRETSECRET") {
		t.Errorf("invalid node leaked credentials: %v", err)
	}
	for _, key := range []string{"outbounds", "endpoints"} {
		_, err := UpdateUISubscriptionNodes(`{"`+key+`":{}}`, `[`+valid+`]`)
		uiNodeTestFails(t, "non-list "+key, err, key+" must be a JSON array")
	}
}

func TestUINodeSubscriptionCleanGroupsAndPreserveDirectMembers(t *testing.T) {
	current := `{"outbounds":[{"type":"direct","tag":"direct"},
		{"type":"selector","tag":"proxy","outbounds":["direct","Old"],"default":"Old"},
		{"type":"urltest","tag":"mixed","outbounds":["Old","direct"]},
		{"type":"urltest","tag":"fallback","outbounds":["direct"]},` + uiNodeTestNode("Old", "198.51.100.1") + `],"route":{"final":"proxy"}}`
	config := uiSubscriptionTestUpdate(t, current, `{"outbounds":[`+uiNodeTestNode("New", "203.0.113.1")+`]}`)
	root := uiNodeTestObject(t, config)
	if uiNodeTestMembers(root, "proxy") != "direct,New" || uiNodeTestMembers(root, "mixed") != "direct,New" ||
		uiNodeTestMembers(root, "fallback") != "direct,New" || uiNodeTestFind(root.list("outbounds"), "proxy").get("default") != nil {
		t.Errorf("group cleanup or direct fallback: %s", config)
	}
	if err := CheckConfig(config); err != nil {
		t.Errorf("refreshed groups: %v", err)
	}
	if repeated := uiSubscriptionTestUpdate(t, config, `[`+uiNodeTestNode("New", "203.0.113.1")+`]`); repeated != config {
		t.Errorf("refresh changed group order: %s", repeated)
	}
}

func TestUINodeSubscriptionKeepLocalNamesAndRewriteBatchDetours(t *testing.T) {
	current := uiNodeLocalPrefix + `["Exit","Local"]` + "\n" + `{"outbounds":[
		{"type":"selector","tag":"proxy","outbounds":["Old","Exit","Local"]},` +
		uiNodeTestNode("Old", "198.51.100.9") + `,` + uiNodeTestNode("Exit", "198.51.100.2") + `,` +
		uiNodeTestVia("Local", "198.51.100.3", "Exit") + `],"route":{"final":"proxy"}}`
	subscription := `{"outbounds":[` + uiNodeTestVia("Hop", "203.0.113.1", "Exit") + `,` + uiNodeTestNode("Exit", "203.0.113.2") + `]}`
	config := uiSubscriptionTestUpdate(t, current, subscription)
	root := uiNodeTestObject(t, config)
	if uiNodeTestTags(root.list("outbounds")) != "proxy,Exit,Local,Hop,Exit 2" ||
		uiNodeTestFind(root.list("outbounds"), "Exit").text("server") != "198.51.100.2" ||
		uiNodeTestFind(root.list("outbounds"), "Hop").text("detour") != "Exit 2" ||
		uiNodeTestFind(root.list("outbounds"), "Local").text("detour") != "Exit" ||
		uiNodeTestMembers(root, "proxy") != "Exit,Local,Hop,Exit 2" {
		t.Errorf("refresh changed local names or detours: %s", config)
	}
	uiNodeTestLocal(t, config, "Exit", "Local")
	if repeated := uiSubscriptionTestUpdate(t, config, subscription); repeated != config {
		t.Errorf("repeated collision grew nodes: %s", repeated)
	}
	if err := CheckConfig(config); err != nil {
		t.Errorf("collision configuration: %v", err)
	}
	identical := uiSubscriptionTestUpdate(t, config, `[`+uiNodeTestNode("Exit", "198.51.100.2")+`]`)
	if got := uiNodeTestTags(uiNodeTestObject(t, identical).list("outbounds")); got != "proxy,Exit,Local,Exit 2" {
		t.Errorf("subscription granted local ownership to an identical node: %s", identical)
	}
}

func TestUINodeSubscriptionRefuseMissingStableReferences(t *testing.T) {
	old := uiNodeTestNode("Old", "198.51.100.1")
	for _, item := range []struct{ name, current, path string }{
		{"route", `{"outbounds":[` + old + `],"route":{"final":"Old"}}`, "route.final"},
		{"DNS", `{"outbounds":[` + old + `],"dns":{"servers":[{"type":"https","tag":"remote","server":"1.1.1.1","detour":"Old"}]}}`, "dns.servers[0].detour"},
		{"local detour", uiNodeLocalPrefix + `["Local"]` + "\n" + `{"outbounds":[` + old + `,` + uiNodeTestVia("Local", "198.51.100.2", "Old") + `]}`, "outbounds[0].detour"},
	} {
		t.Run(item.name, func(t *testing.T) {
			result, err := UpdateUISubscriptionNodes(item.current, `[`+uiNodeTestNode("New", "203.0.113.1")+`]`)
			if result != nil || err == nil || err.Error() != "missing outbound Old is still used at "+item.path {
				t.Errorf("lost stable reference: %v, %v", result, err)
			}
			// Retaining the same subscription name satisfies an explicit route
			// or detour without rewriting the user's settings.
			config := uiSubscriptionTestUpdate(t, item.current, `[`+uiNodeTestNode("Old", "203.0.113.1")+`]`)
			if len(uiNodeReferences(uiNodeTestObject(t, config))) != len(uiNodeReferences(uiNodeTestObject(t, item.current))) {
				t.Errorf("stable references were removed: %s", config)
			}
		})
	}
}

func TestUINodeSubscriptionDetoursCanUseRetainedLocalNodes(t *testing.T) {
	current := uiNodeLocalPrefix + `["Local"]` + "\n" + `{"outbounds":[{"type":"direct","tag":"direct"},
		{"type":"selector","tag":"proxy","outbounds":["direct","Local"]},` + uiNodeTestNode("Local", "198.51.100.1") + `],"route":{"final":"proxy"}}`
	subscription := `{"outbounds":[` + uiNodeTestVia("ViaLocal", "203.0.113.1", "Local") + `,` + uiNodeTestVia("ViaDirect", "203.0.113.2", "direct") + `]}`
	config := uiSubscriptionTestUpdate(t, current, subscription)
	root := uiNodeTestObject(t, config)
	if uiNodeTestFind(root.list("outbounds"), "ViaLocal").text("detour") != "Local" ||
		uiNodeTestFind(root.list("outbounds"), "ViaDirect").text("detour") != "direct" ||
		uiNodeTestMembers(root, "proxy") != "direct,Local,ViaLocal,ViaDirect" {
		t.Errorf("subscription lost retained detours: %s", config)
	}
	uiNodeTestLocal(t, config, "Local")
	if err := CheckConfig(config); err != nil {
		t.Errorf("detours to local nodes and direct: %v", err)
	}
	// A detour that intentionally names a local group is not inserted back
	// into that group, which would create a group-routing cycle.
	group := uiSubscriptionTestUpdate(t, current, `[`+uiNodeTestVia("ViaGroup", "203.0.113.1", "proxy")+`]`)
	if uiNodeTestMembers(uiNodeTestObject(t, group), "proxy") != "direct,Local" {
		t.Errorf("detour through a local group was added back to the group: %s", group)
	}
	if err := CheckConfig(group); err != nil {
		t.Errorf("detour to a local group: %v", err)
	}
	previous := strings.Replace(current, `"direct","Local"`, `"direct","Local","ViaGroup"`, 1)
	previous = strings.Replace(previous, `],"route":`, `,`+uiNodeTestNode("ViaGroup", "198.51.100.2")+`],"route":`, 1)
	renamed := uiSubscriptionTestUpdate(t, previous, `[`+uiNodeTestVia("ViaGroup", "203.0.113.1", "proxy")+`]`)
	if uiNodeTestMembers(uiNodeTestObject(t, renamed), "proxy") != "direct,Local" || renamed != group {
		t.Errorf("same-name detour left a previous group-routing cycle: %s", renamed)
	}
	empty := `{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["ViaGroup"]},` + uiNodeTestNode("ViaGroup", "198.51.100.2") + `]}`
	_, err := UpdateUISubscriptionNodes(empty, `[`+uiNodeTestVia("ViaGroup", "203.0.113.1", "proxy")+`]`)
	uiNodeTestFails(t, "group cycle without a fallback", err, "group proxy would be left without a member")
}

func TestUINodeSubscriptionDoesNotBindDiscardedGroupsToLocalNames(t *testing.T) {
	current := `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"proxy","outbounds":["direct"]}],"route":{"final":"proxy"}}`
	json := `{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["ViaGroup"]},` + uiNodeTestVia("ViaGroup", "198.51.100.1", "proxy") + `]}`
	clash := "proxies:\n  - {name: ViaGroup, type: ss, server: 198.51.100.1, port: 8388, cipher: aes-256-gcm, password: password, dialer-proxy: proxy}\nproxy-groups:\n  - {name: proxy, type: select, proxies: [ViaGroup]}\n"
	for _, item := range []struct{ name, subscription string }{
		{"sing-box", json},
		{"base64", base64.StdEncoding.EncodeToString([]byte(json))},
		{"Clash", clash},
	} {
		t.Run(item.name, func(t *testing.T) {
			result, err := UpdateUISubscriptionNodes(current, item.subscription)
			uiNodeTestFails(t, "discarded group", err, "subscription node ViaGroup refers to discarded subscription group proxy")
			if result != nil {
				t.Errorf("discarded group was rebound to local proxy: %s", result.Value)
			}
		})
	}
}

func TestUINodeSubscriptionReplaceProxyEndpoints(t *testing.T) {
	wireguard := `{"type":"wireguard","tag":"WG","address":["10.0.0.2/32"],"private_key":"` + uiNodeTestKey(1) +
		`","peers":[{"address":"wg.example.com","port":51820,"public_key":"` + uiNodeTestKey(2) + `","allowed_ips":["0.0.0.0/0"]}]}`
	if len(uiNodeTestParse(t, `{"endpoints":[`+wireguard+`]}`).list("nodes")) == 0 {
		t.Skip("this core build does not include WireGuard")
	}
	current := uiNodeLocalPrefix + `["LocalWG"]` + "\n" + `{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["OldWG","LocalWG"]}],"endpoints":[
		{"type":"tailscale","tag":"tail","hostname":"local-tail"},` + strings.Replace(wireguard, `"WG"`, `"OldWG"`, 1) + `,` +
		strings.Replace(wireguard, `"WG"`, `"LocalWG"`, 1) + `],"route":{"final":"proxy"}}`
	config := uiSubscriptionTestUpdate(t, current, `{"endpoints":[`+wireguard+`,{"type":"tailscale","tag":"remote-tail"}]}`)
	root := uiNodeTestObject(t, config)
	if uiNodeTestTags(root.list("endpoints")) != "tail,LocalWG,WG" || uiNodeTestTags(root.list("outbounds")) != "proxy" ||
		uiNodeTestMembers(root, "proxy") != "LocalWG,WG" {
		t.Errorf("endpoint subscription replaced local capabilities: %s", config)
	}
	uiNodeTestLocal(t, config, "LocalWG")
	if repeated := uiSubscriptionTestUpdate(t, config, `{"endpoints":[`+wireguard+`]}`); repeated != config {
		t.Errorf("repeated endpoint refresh changed the document: %s", repeated)
	}
	file := "[Interface]\nPrivateKey = " + uiNodeTestKey(1) + "\nAddress = 10.0.0.2/32\n[Peer]\nPublicKey = " + uiNodeTestKey(2) + "\nAllowedIPs = 0.0.0.0/0\nEndpoint = wg.example.com:51820\n"
	if got := uiNodeTestTags(uiNodeTestObject(t, uiSubscriptionTestUpdate(t, `{}`, file)).list("endpoints")); got != "wg.example.com:51820" {
		t.Errorf("wg-quick did not become a proxy endpoint: %s", got)
	}
	first := uiSubscriptionTestApply(t, current, `{"endpoints":[`+wireguard+`]}`, `[]`, false)
	second := uiSubscriptionTestApply(t, first.Config, `{"endpoints":[`+wireguard+`]}`, `[]`, false)
	updated := uiSubscriptionTestApply(t, second.Config, `{"endpoints":[`+strings.Replace(wireguard, "wg.example.com", "updated.example.com", 1)+`]}`, first.Tags, false)
	root = uiNodeTestObject(t, updated.Config)
	if first.Tags != `["WG"]` || second.Tags != `["WG 2"]` || updated.Tags != first.Tags ||
		uiNodeTestTags(root.list("endpoints")) != "tail,OldWG,LocalWG,WG 2,WG" ||
		uiNodeTestFind(root.list("endpoints"), "WG 2").list("peers")[0].(*uiNodeObject).text("address") != "wg.example.com" {
		t.Errorf("WireGuard source refresh changed other endpoints: %s", updated.Config)
	}
	uiNodeTestLocal(t, updated.Config, "LocalWG")
}

func TestUINodeSubscriptionSourcesPreserveOtherNodes(t *testing.T) {
	current := uiNodeLocalPrefix + `["Local"]` + "\n" + `{"outbounds":[{"type":"direct","tag":"direct"},
		{"type":"selector","tag":"proxy","outbounds":["Existing","Local"]},` + uiNodeTestNode("Existing", "198.51.100.1") + `,` +
		uiNodeTestNode("Local", "198.51.100.2") + `],"route":{"final":"proxy"}}`
	first := uiSubscriptionTestApply(t, current, `[`+uiNodeTestNode("Shared", "203.0.113.1")+`]`, `[]`, false)
	second := uiSubscriptionTestApply(t, first.Config, `[`+uiNodeTestNode("Shared", "203.0.113.2")+`]`, `[]`, false)
	if first.Tags != `["Shared"]` || second.Tags != `["Shared 2"]` || first.Added != 1 || second.Added != 1 {
		t.Errorf("source ownership: first=%+v, second=%+v", first, second)
	}
	chained := uiSubscriptionTestApply(t, second.Config, `[`+uiNodeTestVia("ViaOther", "203.0.113.3", "Shared 2")+`]`, first.Tags, false)
	if err := CheckConfig(chained.Config); err != nil {
		t.Errorf("a source cannot dial through another retained source: %v", err)
	}
	if result, err := ApplyUISubscriptionNodes(chained.Config, `[`+uiNodeTestNode("Other", "203.0.113.4")+`]`, second.Tags, false); result != nil || err == nil || !strings.Contains(err.Error(), "missing outbound Shared 2") {
		t.Errorf("source refresh discarded another source's dependency: %v, %v", result, err)
	}
	updated := uiSubscriptionTestApply(t, second.Config, `[`+uiNodeTestNode("Newest", "203.0.113.3")+`]`, first.Tags, false)
	root := uiNodeTestObject(t, updated.Config)
	if updated.Tags != `["Newest"]` || uiNodeTestTags(root.list("outbounds")) != "direct,proxy,Existing,Local,Shared 2,Newest" ||
		uiNodeTestFind(root.list("outbounds"), "Shared 2").text("server") != "203.0.113.2" ||
		uiNodeTestFind(root.list("outbounds"), "Existing").text("server") != "198.51.100.1" ||
		uiNodeTestMembers(root, "proxy") != "Existing,Local,Shared 2,Newest" ||
		!uiNodeEqual(root.object("route"), uiNodeTestObject(t, current).object("route")) {
		t.Errorf("one source overwrote another source or existing nodes: %s", updated.Config)
	}
	uiNodeTestLocal(t, updated.Config, "Local")
	if repeated := uiSubscriptionTestApply(t, updated.Config, `[`+uiNodeTestNode("Newest", "203.0.113.3")+`]`, updated.Tags, false); repeated.Config != updated.Config || repeated.Tags != updated.Tags {
		t.Errorf("repeated source refresh grew nodes: %+v", repeated)
	}
	other := uiSubscriptionTestApply(t, updated.Config, `[`+uiNodeTestNode("Shared", "203.0.113.4")+`]`, second.Tags, false)
	if other.Tags != `["Shared"]` || uiNodeTestFind(uiNodeTestObject(t, other.Config).list("outbounds"), "Newest").text("server") != "203.0.113.3" {
		t.Errorf("second source refresh changed first source: %+v", other)
	}
	if err := CheckConfig(other.Config); err != nil {
		t.Errorf("two-source configuration: %v", err)
	}
	// Local and infrastructure tags are never removable source ownership.
	protected := uiSubscriptionTestApply(t, other.Config, `[`+uiNodeTestNode("Extra", "203.0.113.5")+`]`, `["Local","direct","Missing"]`, false)
	if !strings.Contains(uiNodeTestTags(uiNodeTestObject(t, protected.Config).list("outbounds")), "direct,proxy,Existing,Local") {
		t.Errorf("stale source ownership removed protected nodes: %s", protected.Config)
	}
	uiNodeTestLocal(t, protected.Config, "Local")
}

func TestUINodeSubscriptionSourceCollisionsRewriteOnlyTheirDetours(t *testing.T) {
	current := uiNodeLocalPrefix + `["Exit"]` + "\n" + `{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["Exit"]},` + uiNodeTestNode("Exit", "198.51.100.1") + `],"route":{"final":"proxy"}}`
	subscription := `[` + uiNodeTestVia("Hop", "203.0.113.1", "Exit") + `,` + uiNodeTestNode("Exit", "203.0.113.2") + `]`
	first := uiSubscriptionTestApply(t, current, subscription, `[]`, false)
	second := uiSubscriptionTestApply(t, first.Config, subscription, `[]`, false)
	if first.Tags != `["Hop","Exit 2"]` || second.Tags != `["Hop 2","Exit 3"]` {
		t.Errorf("colliding source names were not distinct: first=%s, second=%s", first.Tags, second.Tags)
	}
	updated := uiSubscriptionTestApply(t, second.Config, strings.ReplaceAll(subscription, "203.0.113", "192.0.2"), first.Tags, false)
	root := uiNodeTestObject(t, updated.Config)
	if updated.Tags != first.Tags || uiNodeTestFind(root.list("outbounds"), "Hop").text("detour") != "Exit 2" ||
		uiNodeTestFind(root.list("outbounds"), "Hop 2").text("detour") != "Exit 3" ||
		uiNodeTestFind(root.list("outbounds"), "Exit 3").text("server") != "203.0.113.2" ||
		uiNodeTestFind(root.list("outbounds"), "Exit").text("server") != "198.51.100.1" {
		t.Errorf("source refresh rewrote another source or local detour: %s", updated.Config)
	}
	uiNodeTestLocal(t, updated.Config, "Exit")
	if repeated := uiSubscriptionTestApply(t, updated.Config, strings.ReplaceAll(subscription, "203.0.113", "192.0.2"), updated.Tags, false); repeated.Config != updated.Config {
		t.Errorf("repeat collision changed source names: %+v", repeated)
	}
	if err := CheckConfig(updated.Config); err != nil {
		t.Errorf("colliding sources: %v", err)
	}
}

func TestUINodeSubscriptionSourceUpdateCleansIndirectGroupCycles(t *testing.T) {
	for _, local := range []bool{false, true} {
		name := "other source"
		marker := ""
		if local {
			name = "local node"
			marker = uiNodeLocalPrefix + `["B"]` + "\n"
		}
		t.Run(name, func(t *testing.T) {
			current := marker + `{"outbounds":[{"type":"direct","tag":"direct"},
				{"type":"selector","tag":"proxy","outbounds":["auto","A","B"],"default":"B"},
				{"type":"urltest","tag":"auto","outbounds":["direct","A","B"]},` +
				uiNodeTestNode("A", "198.51.100.1") + `,` + uiNodeTestVia("B", "198.51.100.2", "A") + `],"route":{"final":"proxy"}}`
			subscription := `[` + uiNodeTestVia("A", "203.0.113.1", "proxy") + `]`
			updated := uiSubscriptionTestApply(t, current, subscription, `["A"]`, false)
			root := uiNodeTestObject(t, updated.Config)
			if uiNodeTestMembers(root, "proxy") != "auto" || uiNodeTestMembers(root, "auto") != "direct" ||
				uiNodeTestFind(root.list("outbounds"), "proxy").get("default") != nil ||
				!uiNodeEqual(uiNodeTestFind(root.list("outbounds"), "B"), uiNodeTestFind(uiNodeTestObject(t, current).list("outbounds"), "B")) ||
				updated.Tags != `["A"]` {
				t.Errorf("indirect proxy -> B -> A -> proxy cycle survived: %s", updated.Config)
			}
			if local {
				uiNodeTestLocal(t, updated.Config, "B")
			} else {
				uiNodeTestLocal(t, updated.Config)
			}
			if err := CheckConfig(updated.Config); err != nil {
				t.Errorf("indirect group-cycle cleanup: %v", err)
			}
			withoutFallback := marker + `{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["A","B"]},` +
				uiNodeTestNode("A", "198.51.100.1") + `,` + uiNodeTestVia("B", "198.51.100.2", "A") + `]}`
			result, err := ApplyUISubscriptionNodes(withoutFallback, subscription, `["A"]`, false)
			uiNodeTestFails(t, "indirect cycle without a fallback", err, "group proxy would be left without a member")
			if result != nil {
				t.Errorf("indirect group cycle accepted without a valid member: %s", result.Config)
			}
		})
	}
}

func TestUINodeSubscriptionSourcesPreserveValidGroupDetourMemberships(t *testing.T) {
	for _, local := range []bool{false, true} {
		name := "other source"
		marker := ""
		if local {
			name = "local node"
			marker = uiNodeLocalPrefix + `["Via"]` + "\n"
		}
		for _, importing := range []bool{false, true} {
			action := "update"
			previous, tag := `["Owned"]`, "Owned"
			if importing {
				action = "append"
				previous, tag = `[]`, "New"
			}
			t.Run(name+" "+action, func(t *testing.T) {
				current := marker + `{"outbounds":[{"type":"direct","tag":"direct"},
					{"type":"selector","tag":"upstream","outbounds":["Exit"]},
					{"type":"selector","tag":"proxy","outbounds":["Via","Owned"],"default":"Via"},` +
					uiNodeTestNode("Exit", "198.51.100.1") + `,` + uiNodeTestVia("Via", "198.51.100.2", "upstream") + `,` +
					uiNodeTestNode("Owned", "198.51.100.3") + `],"route":{"final":"proxy"}}`
				if err := CheckConfig(current); err != nil {
					t.Fatalf("valid current group detour configuration: %v", err)
				}
				subscription := `[` + uiNodeTestNode(tag, "203.0.113.1") + `]`
				updated := uiSubscriptionTestApply(t, current, subscription, previous, false)
				root := uiNodeTestObject(t, updated.Config)
				want := "Via,Owned"
				if importing {
					want += ",New"
				}
				if uiNodeTestMembers(root, "proxy") != want ||
					uiNodeTestFind(root.list("outbounds"), "proxy").text("default") != "Via" ||
					!uiNodeEqual(uiNodeTestFind(root.list("outbounds"), "Via"), uiNodeTestFind(uiNodeTestObject(t, current).list("outbounds"), "Via")) ||
					updated.Tags != `["`+tag+`"]` {
					t.Errorf("unrelated source changed a valid group detour membership: %s", updated.Config)
				}
				if local {
					uiNodeTestLocal(t, updated.Config, "Via")
				} else {
					uiNodeTestLocal(t, updated.Config)
				}
				if err := CheckConfig(updated.Config); err != nil {
					t.Errorf("valid group detour after source update: %v", err)
				}
				if repeated := uiSubscriptionTestApply(t, updated.Config, subscription, updated.Tags, false); repeated.Config != updated.Config {
					t.Errorf("repeated source update changed a valid membership: %s", repeated.Config)
				}
			})
		}
	}
}

func TestUINodeSubscriptionSourcesRejectUnknownLinkEntries(t *testing.T) {
	current := `{"outbounds":[{"type":"selector","tag":"proxy","outbounds":["Owned"]},` + uiNodeTestNode("Owned", "198.51.100.1") + `]}`
	valid := "ss://aes-256-gcm:password@203.0.113.1:8388#New"
	for _, scheme := range []string{"unknown", "future-proxy", "future.proxy", "FUTURE+proxy"} {
		body := valid + "\n" + scheme + "://SECRETSECRET@203.0.113.2:443#Unreadable"
		for _, encoded := range []bool{false, true} {
			subscription := body
			if encoded {
				subscription = base64.StdEncoding.EncodeToString([]byte(body))
			}
			result, err := ApplyUISubscriptionNodes(current, subscription, `["Owned"]`, false)
			if result != nil || err == nil || !strings.Contains(err.Error(), "unsupported URI scheme") || strings.Contains(err.Error(), "SECRETSECRET") {
				t.Errorf("unknown source link was partly applied or leaked credentials: scheme=%s, encoded=%v, result=%v, err=%v", scheme, encoded, result, err)
			}
		}
	}
	// The general one-time importer continues to report only recognized
	// node links. Its skip-and-review behavior is not made strict here.
	if parsed := uiNodeTestParse(t, valid+"\nunknown://SECRETSECRET@203.0.113.2:443"); len(parsed.list("nodes")) != 1 || len(parsed.list("skipped")) != 0 {
		t.Errorf("strict source link checks changed one-time importing: %s", uiNodeTestText(parsed))
	}
	for _, body := range []string{
		"# unknown://SECRETSECRET@203.0.113.2:443\n// unknown://SECRETSECRET@203.0.113.2:443\nSubscription expires tomorrow\nDocumentation: unknown://describes-a-format\nhttps://example.test/help\n" + valid,
		"proxies:\n  - {name: New, type: ss, server: 203.0.113.1, port: 8388, cipher: aes-256-gcm, password: password}\nextra: unknown://SECRETSECRET@203.0.113.2:443\n",
	} {
		for _, encoded := range []bool{false, true} {
			subscription := body
			if encoded {
				subscription = base64.StdEncoding.EncodeToString([]byte(body))
			}
			updated := uiSubscriptionTestApply(t, current, subscription, `["Owned"]`, false)
			if updated.Tags != `["New"]` || updated.Added != 1 {
				t.Errorf("subscription notes or unrelated YAML URLs were rejected: encoded=%v, update=%+v", encoded, updated)
			}
		}
	}
}

func TestUINodeSubscriptionSourceRequiresConfigurationAndUsesDefaultIdentity(t *testing.T) {
	subscription := `[` + uiNodeTestNode("Source", "198.51.100.1") + `]`
	_, err := ApplyUISubscriptionNodes("", subscription, `[]`, true)
	uiNodeTestFails(t, "no selected file", err, "a current configuration is required")
	for _, tags := range []string{`{}`, `["Source",12]`} {
		if result, err := ApplyUISubscriptionNodes(`{}`, subscription, tags, false); result != nil || err == nil {
			t.Errorf("invalid source names accepted: %s, %v", tags, err)
		}
	}
	for _, defaults := range []bool{false, true} {
		result := uiSubscriptionTestApply(t, uiNodeDefaultConfig, subscription, `[]`, defaults)
		want := "direct,Source"
		if defaults {
			want = "Source"
		}
		if got := uiNodeTestMembers(uiNodeTestObject(t, result.Config), "auto"); got != want {
			t.Errorf("default identity %v: auto=%s, want %s", defaults, got, want)
		}
	}
}

func TestUINodeSubscriptionSourceRejectsUnreadableEntriesBeforeReplacingNodes(t *testing.T) {
	current := `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"proxy","outbounds":["Owned"]},` + uiNodeTestNode("Owned", "198.51.100.1") + `],"route":{"final":"proxy"}}`
	valid := uiNodeTestNode("Replacement", "203.0.113.1")
	for _, body := range []string{
		`[` + valid + `,{}]`,
		`[null,` + valid + `]`,
		`{"servers":[{"remarks":"Replacement","server":"203.0.113.1","server_port":8388,"method":"aes-256-gcm","password":"password"},{}]}`,
	} {
		for _, encoded := range []bool{false, true} {
			subscription := body
			if encoded {
				subscription = base64.StdEncoding.EncodeToString([]byte(body))
			}
			result, err := ApplyUISubscriptionNodes(current, subscription, `["Owned"]`, false)
			if result != nil || err == nil || !strings.Contains(err.Error(), "subscription contains invalid proxy nodes") {
				t.Errorf("mixed invalid source was partially applied: encoded=%v, result=%v, err=%v", encoded, result, err)
			}
		}
	}
	// The explicit one-time importer keeps its existing skip-and-review
	// behavior; strict source updates use only the new subscription checks.
	if parsed := uiNodeTestParse(t, `[`+valid+`,null,{}]`); len(parsed.list("nodes")) != 1 {
		t.Errorf("subscription checks changed the one-time importer: %s", uiNodeTestText(parsed))
	}
}

func TestUINodeSubscriptionSourceEndpointListsIgnoreOtherFormats(t *testing.T) {
	endpoint := `{"type":"masque-client","tag":"standard","server":"127.0.0.1","server_port":443,"version":2,"tls":{"enabled":true}}`
	current := `{"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"proxy","outbounds":["direct"]}],"endpoints":[{"type":"tailscale","tag":"tail","hostname":"local-tail"}],"route":{"final":"proxy"}}`
	body := `{"endpoints":[` + endpoint + `],"servers":[{"server":"ignored","method":"nonesuch"}],"proxy-providers":{"ignored":{}}}`
	first := uiSubscriptionTestApply(t, current, body, `[]`, false)
	second := uiSubscriptionTestApply(t, first.Config, base64.StdEncoding.EncodeToString([]byte(body)), `[]`, false)
	if first.Tags != `["standard"]` || second.Tags != `["standard 2"]` ||
		uiNodeTestTags(uiNodeTestObject(t, second.Config).list("endpoints")) != "tail,standard,standard 2" {
		t.Errorf("metadata hid native endpoints: first=%+v, second=%+v", first, second)
	}
	updated := uiSubscriptionTestApply(t, second.Config, strings.Replace(body, `"127.0.0.1"`, `"127.0.0.2"`, 1), first.Tags, false)
	root := uiNodeTestObject(t, updated.Config)
	if updated.Tags != first.Tags || uiNodeTestFind(root.list("endpoints"), "standard 2").text("server") != "127.0.0.1" ||
		uiNodeTestMembers(root, "proxy") != "direct,standard,standard 2" {
		t.Errorf("endpoint source replaced another source: %s", updated.Config)
	}
	for _, key := range []string{"outbounds", "endpoints"} {
		invalid := `{"outbounds":[` + uiNodeTestNode("Valid", "198.51.100.1") + `],"endpoints":[` + endpoint + `]}`
		root := uiNodeTestObject(t, invalid)
		root.set(key, uiNodeOf())
		_, err := ApplyUISubscriptionNodes(current, uiNodeTestText(root), `[]`, false)
		if err == nil || !strings.Contains(err.Error(), key+" must be a JSON array") {
			t.Errorf("malformed source node list was partly accepted: %s, %v", key, err)
		}
	}
}

func TestUINodeSubscriptionRetainedSourceNodesBecomeLocal(t *testing.T) {
	current := uiNodeLocalPrefix + `["Local"]` + "\n" + `{"outbounds":[{"type":"direct","tag":"direct"},` +
		uiNodeTestNode("Local", "198.51.100.1") + `,` + uiNodeTestNode("Released", "198.51.100.2") + `],"future":{"integer":12345678901234567890}}`
	result, err := RetainUISubscriptionNodes(current, `["Released","direct","Missing","Released"]`)
	retained := uiNodeTestValue(t, "release source nodes", result, err)
	uiNodeTestLocal(t, retained, "Local", "Released")
	_, body, _ := strings.Cut(current, "\n")
	_, gotBody, _ := strings.Cut(retained, "\n")
	if gotBody != body {
		t.Errorf("releasing a source rewrote the configuration: %s", retained)
	}
	// A stale source or the legacy single-URL updater cannot reclaim nodes
	// once removing their source has made them local.
	updated := uiSubscriptionTestApply(t, retained, `[`+uiNodeTestNode("Remote", "203.0.113.1")+`]`, `["Released"]`, false)
	legacy := uiSubscriptionTestUpdate(t, updated.Config, `[`+uiNodeTestNode("Legacy", "203.0.113.2")+`]`)
	root := uiNodeTestObject(t, legacy)
	if uiNodeTestTags(root.list("outbounds")) != "direct,Local,Released,Legacy" || uiNodeTestFind(root.list("outbounds"), "Released").text("server") != "198.51.100.2" {
		t.Errorf("later refresh reclaimed released local nodes: %s", legacy)
	}
	uiNodeTestLocal(t, legacy, "Local", "Released")
}
