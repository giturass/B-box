package libbox

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
)

const uiLANTestAddresses = `[
	{"address":"192.168.1.2","interface":"wlan0"},
	{"address":"2001:db8::2","interface":"wlan0"},
	{"address":"10.0.0.2","interface":"eth0"},
	{"address":"192.168.1.2","interface":"wlan0"},
	{"address":"127.0.0.1","interface":"lo"},
	{"address":"fe80::1","interface":"wlan0"},
	{"address":"not-an-address","interface":"wlan0"}
]`

func uiLANRead(t *testing.T, content string) []uiLANListener {
	t.Helper()
	result, err := ReadUILANProxyListeners(content, uiLANTestAddresses)
	if err != nil {
		t.Fatal(err)
	}
	var listeners []uiLANListener
	if err = json.Unmarshal([]byte(result.Value), &listeners); err != nil {
		t.Fatal(err)
	}
	return listeners
}

func uiLANConfig(t *testing.T, content string) map[string]any {
	t.Helper()
	formatted, err := FormatConfig(content)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err = json.Unmarshal([]byte(formatted.Value), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestUILANListenerAddresses(t *testing.T) {
	for _, test := range []struct {
		name      string
		fields    string
		port      uint16
		listen    string
		lan       bool
		endpoints []string
	}{
		{"omitted listen is loopback", "", 2080, "127.0.0.1", false, nil},
		{"IPv4 loopback", `,"listen":"127.0.0.1"`, 2080, "127.0.0.1", false, nil},
		{"IPv6 loopback", `,"listen":"::1"`, 2080, "::1", false, nil},
		{"link-local bind is not an unscoped copy endpoint", `,"listen":"fe80::1"`, 2080, "fe80::1", true, nil},
		{"IPv4 wildcard", `,"listen":"0.0.0.0"`, 2080, "0.0.0.0", true, []string{"192.168.1.2:2080", "10.0.0.2:2080"}},
		{"dual stack wildcard", `,"listen":"::"`, 2080, "::", true, []string{"192.168.1.2:2080", "[2001:db8::2]:2080", "10.0.0.2:2080"}},
		{"specific IPv4", `,"listen":"192.168.1.2"`, 2080, "192.168.1.2", true, []string{"192.168.1.2:2080"}},
		{"specific IPv6", `,"listen":"2001:db8::2"`, 2080, "2001:db8::2", true, []string{"[2001:db8::2]:2080"}},
		{"bound interface", `,"listen":"0.0.0.0","bind_interface":"eth0"`, 2080, "0.0.0.0", true, []string{"10.0.0.2:2080"}},
		{"disconnected address", `,"listen":"192.168.1.3"`, 2080, "192.168.1.3", true, nil},
		{"OS assigned port is not advertised", `,"listen":"0.0.0.0"`, 0, "0.0.0.0", true, nil},
		{"another namespace is not the device LAN", `,"listen":"0.0.0.0","netns":"other"`, 2080, "0.0.0.0", true, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			profile := fmt.Sprintf(`{"inbounds":[{"type":"mixed","tag":"proxy","listen_port":%d%s}]}`, test.port, test.fields)
			listeners := uiLANRead(t, profile)
			if len(listeners) != 1 {
				t.Fatalf("listeners: %+v", listeners)
			}
			got := listeners[0]
			if got.Listen != test.listen || got.LAN != test.lan || got.Port != test.port ||
				!reflect.DeepEqual(got.Endpoints, append([]string{}, test.endpoints...)) {
				t.Fatalf("listener: %+v, expected listen=%s LAN=%v endpoints=%v", got, test.listen, test.lan, test.endpoints)
			}
		})
	}
}

func TestUILANTogglePreservesNativeConfiguration(t *testing.T) {
	const profile = `{
		"inbounds":[
			{"type":"mixed","tag":"mixed-in","listen_port":2080,"users":[{"username":"fixture","password":"fixture-only"}],"udp_timeout":"2m"},
			{"type":"socks","tag":"socks-in","listen":"::1","listen_port":1080,"users":[{"username":"fixture","password":"fixture-only"}]},
			{"type":"http","tag":"http-in","listen":"192.168.1.2","listen_port":3128,"bind_interface":"wlan0","tls":{"enabled":true,"server_name":"proxy.example","certificate_path":"proxy.crt","key_path":"proxy.key"}},
			{"type":"shadowsocks","tag":"encrypted-in","listen":"0.0.0.0","listen_port":8388,"method":"aes-128-gcm","password":"fixture-only"}
		],
		"outbounds":[{"type":"direct","tag":"direct"}],
		"route":{"rules":[{"inbound":["mixed-in"],"action":"route","outbound":"direct"}],"final":"direct"},
		"log":{"disabled":true}
	}`
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			updated, err := SetUILANProxyEnabled(profile, enabled, 7890)
			if err != nil {
				t.Fatal(err)
			}
			before, after := uiLANConfig(t, profile), uiLANConfig(t, updated.Value)
			prior := before["inbounds"].([]any)
			next := after["inbounds"].([]any)
			if len(prior) != len(next) {
				t.Fatal("a profile with proxy listeners must not gain another mixed inbound")
			}
			expected := []string{"127.0.0.1", "::1", "127.0.0.1"}
			if enabled {
				expected = []string{"0.0.0.0", "::", "192.168.1.2"}
			}
			for index := range expected {
				inbound := next[index].(map[string]any)
				actual, _ := inbound["listen"].(string)
				if actual == "" {
					actual = "127.0.0.1"
				}
				if actual != expected[index] {
					t.Fatalf("inbound %d listen=%v, expected %s", index, inbound["listen"], expected[index])
				}
				delete(inbound, "listen")
				delete(prior[index].(map[string]any), "listen")
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("ports, authentication, TLS, interface binding, non-proxy inbounds or routing changed")
			}
			listeners := uiLANRead(t, updated.Value)
			if len(listeners) != 3 || !listeners[0].Authentication || !listeners[1].Authentication || !listeners[2].TLS ||
				listeners[0].Port != 2080 || listeners[1].Port != 1080 || listeners[2].Port != 3128 {
				t.Fatalf("native listener capabilities: %+v", listeners)
			}
		})
	}
}

func TestUILANCreateAndDisableNativeMixedInbound(t *testing.T) {
	const profile = `{"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`
	unchanged, err := SetUILANProxyEnabled(profile, false, 0)
	if err != nil || len(uiLANRead(t, unchanged.Value)) != 0 {
		t.Fatal("disabling must not create a proxy listener")
	}
	created, err := SetUILANProxyEnabled(profile, true, 2080)
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckConfig(created.Value); err != nil {
		t.Fatalf("new mixed inbound is rejected by the matching sing-box core: %v", err)
	}
	listeners := uiLANRead(t, created.Value)
	if len(listeners) != 1 || listeners[0].Type != "mixed" || listeners[0].Tag != "b-box-lan" ||
		listeners[0].Port != 2080 || !listeners[0].LAN {
		t.Fatalf("created listener: %+v", listeners)
	}
	disabled, err := SetUILANProxyEnabled(created.Value, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckConfig(disabled.Value); err != nil {
		t.Fatal(err)
	}
	listeners = uiLANRead(t, disabled.Value)
	if len(listeners) != 1 || listeners[0].LAN || listeners[0].Port != 2080 || len(listeners[0].Endpoints) != 0 {
		t.Fatalf("disabled listener: %+v", listeners)
	}
}

func TestUILANNewListenerConflictsAndValidation(t *testing.T) {
	for _, port := range []int32{-1, 0, 65536} {
		if _, err := SetUILANProxyEnabled("{}", true, port); err == nil {
			t.Fatalf("invalid initial port %d was accepted", port)
		}
	}
	const profile = `{"inbounds":[{"type":"shadowsocks","tag":"b-box-lan","listen":"127.0.0.1","listen_port":2080,"method":"aes-128-gcm","password":"fixture-only"}]}`
	if _, err := SetUILANProxyEnabled(profile, true, 2080); err == nil {
		t.Fatal("new mixed inbound conflicts with an existing inbound port")
	}
	created, err := SetUILANProxyEnabled(profile, true, 3128)
	if err != nil {
		t.Fatal(err)
	}
	listeners := uiLANRead(t, created.Value)
	if len(listeners) != 1 || listeners[0].Tag != "b-box-lan-2" {
		t.Fatalf("an existing tag must be preserved: %+v", listeners)
	}
	for _, invalid := range []string{
		"mixed-port: 7890\nallow-lan: true",
		`{"inbounds":[{"type":"mixed","listen":"invalid-address","listen_port":2080}]}`,
		`{"inbounds":[{"type":"mixed","listen_port":65536}]}`,
	} {
		if _, err := SetUILANProxyEnabled(invalid, true, 2080); err == nil {
			t.Fatalf("invalid sing-box profile accepted: %s", invalid)
		}
	}
}
