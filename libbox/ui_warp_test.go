package libbox

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func uiWARPTestPeer(t *testing.T) (string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})), base64.StdEncoding.EncodeToString(der)
}
func uiWARPTestEnrollment(t *testing.T, peer, ipv4, ipv6 string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"id": "mock-device", "token": "mock-secret-api-token", "account": map[string]string{"license": "mock-secret-license"},
		"config": map[string]any{"interface": map[string]any{"addresses": map[string]string{"v4": ipv4, "v6": ipv6}}, "peers": []any{map[string]any{
			"public_key": peer, "endpoint": map[string]string{"v4": "162.159.198.1:443", "host": "162.159.198.1:443"},
		}}}})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
func uiWARPTestRegister(t *testing.T, ctx context.Context, server *httptest.Server, probe *UIEgressProbe) (string, error) {
	return uiWARPTestRegisterTunnel(t, ctx, server, probe, false)
}
func uiWARPTestRegisterTunnel(t *testing.T, ctx context.Context, server *httptest.Server, probe *UIEgressProbe, wireGuard bool) (string, error) {
	t.Helper()
	task := newUIWARPRegistrationTask(probe)
	defer task.Close()
	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(probe.ctx, cancel)
	defer stop()
	defer cancel()
	if wireGuard {
		return uiWARPRegisterWireGuard(requestCtx, task.probe, server.URL, "WARP")
	}
	return uiWARPRegister(requestCtx, task.probe, server.URL, "WARP", 3)
}

func uiWARPTestWireGuardResponse(t *testing.T, peer, clientID, ipv4, ipv6 string, endpoint map[string]string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"id": "mock-device", "token": "mock-secret-api-token", "account": map[string]string{"license": "mock-secret-license"},
		"config": map[string]any{
			"client_id": clientID, "interface": map[string]any{"addresses": map[string]string{"v4": ipv4, "v6": ipv6}},
			"peers": []any{map[string]any{"public_key": peer, "endpoint": endpoint}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func uiWARPTestWireGuardPeer(t *testing.T) string {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
}

func uiWARPTestRequireWireGuard(t *testing.T) {
	t.Helper()
	// Check an independent known-good endpoint. A malformed generated WARP
	// endpoint must fail the assertions below, rather than skip its own test.
	reference := `{"endpoints":[{"type":"wireguard","tag":"WG","address":["10.0.0.2/32"],"private_key":"` + uiNodeTestKey(1) +
		`","peers":[{"address":"198.51.100.1","port":51820,"public_key":"` + uiNodeTestKey(2) + `","allowed_ips":["0.0.0.0/0"]}]}]}`
	parsed := uiNodeTestParse(t, reference)
	if len(parsed.list("nodes")) == 1 {
		return
	}
	if skipped := parsed.list("skipped"); len(skipped) == 1 {
		entry, ok := skipped[0].(*uiNodeObject)
		if ok && entry.text("reason") == "WireGuard is not included in this build, rebuild with -tags with_wireguard" {
			t.Skip("this core build does not include WireGuard")
		}
	}
	t.Fatalf("the reference WireGuard endpoint failed: %s", uiNodeTestSkipped(parsed))
}

func TestUIWARPWireGuardRegistrationAndEndpointMerge(t *testing.T) {
	peerKey := uiWARPTestWireGuardPeer(t)
	var calls atomic.Int32
	registeredPublicKey := make(chan string, 1)
	response := uiWARPTestWireGuardResponse(t, peerKey, "AID/", "172.16.0.2", "2001:db8::2", map[string]string{
		"host": "162.159.192.12:500", "v4": "162.159.192.1:2408", "v6": "[2606:4700:d0::1]:2408",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) != 1 {
			t.Error("WireGuard registration repeated an API request or enrolled a MASQUE key")
		}
		if r.Method != http.MethodPost || r.URL.Path != "/reg" || r.Header.Get("Authorization") != "" ||
			r.Header.Get("User-Agent") != "WARP for Android" || r.Header.Get("CF-Client-Version") != "a-6.35-4471" ||
			r.Header.Get("Content-Type") != "application/json; charset=UTF-8" {
			t.Error("incorrect WireGuard registration request")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		if body["key_type"] != "curve25519" || body["tunnel_type"] != "wireguard" || len(body["serial_number"]) != 16 || body["tos"] == "" {
			t.Error("incorrect WireGuard device metadata")
		}
		registeredPublicKey <- body["key"]
		io.WriteString(w, response)
	}))
	defer server.Close()
	result, err := uiWARPTestRegisterTunnel(t, context.Background(), server, NewUIDirectEgressProbe(), true)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("WireGuard must register exactly once without MASQUE enrollment")
	}
	for _, secret := range []string{"mock-device", "mock-secret-api-token", "mock-secret-license"} {
		if strings.Contains(result, secret) {
			t.Fatal("unneeded WireGuard API credential crossed the binding")
		}
	}
	var node map[string]any
	if err = json.Unmarshal([]byte(result), &node); err != nil {
		t.Fatal(err)
	}
	if node["type"] != "wireguard" || node["tag"] != "WARP" || node["mtu"] != float64(1280) ||
		!reflect.DeepEqual(node["address"], []any{"172.16.0.2/32", "2001:db8::2/128"}) {
		t.Fatal("incorrect native WireGuard endpoint metadata")
	}
	for _, field := range []string{"server", "server_port", "local_address", "peer_public_key", "tls", "http_version"} {
		if _, exists := node[field]; exists {
			t.Fatal("WireGuard endpoint contains outbound or MASQUE fields", field)
		}
	}
	peers, ok := node["peers"].([]any)
	if !ok || len(peers) != 1 {
		t.Fatal("WireGuard endpoint must contain its registered peer")
	}
	peer := peers[0].(map[string]any)
	if peer["address"] != "162.159.192.12" || peer["port"] != float64(500) || peer["public_key"] != peerKey ||
		!reflect.DeepEqual(peer["allowed_ips"], []any{"0.0.0.0/0", "::/0"}) ||
		!reflect.DeepEqual(peer["reserved"], []any{float64(0), float64(128), float64(255)}) {
		t.Fatal("WireGuard peer did not retain its API endpoint, key or reserved bytes")
	}
	privateBytes, err := base64.StdEncoding.DecodeString(node["private_key"].(string))
	if err != nil || len(privateBytes) != 32 {
		t.Fatal("WireGuard private key must contain exactly 32 raw bytes")
	}
	if privateBytes[0]&7 != 0 || privateBytes[31]&128 != 0 || privateBytes[31]&64 == 0 {
		t.Fatal("stored WireGuard private key was not clamped")
	}
	privateKey, err := ecdh.X25519().NewPrivateKey(privateBytes)
	if err != nil || base64.StdEncoding.EncodeToString(privateKey.PublicKey().Bytes()) != <-registeredPublicKey {
		t.Fatal("stored WireGuard private key does not match the registered public key")
	}
	// Options remain checked even in the base workflow build without WireGuard.
	if _, err = parseConfig(baseContext(nil), `{"endpoints":[`+result+`]}`); err != nil {
		t.Fatal("generated WireGuard endpoint does not match the current core schema", err)
	}
	t.Run("selected configuration append", func(t *testing.T) {
		uiWARPTestRequireWireGuard(t)
		existing := strings.Replace(result, `"tag":"WARP"`, `"tag":"Existing"`, 1)
		existing = strings.Replace(existing, `"private_key":"`+node["private_key"].(string)+`"`, `"private_key":"`+uiNodeTestKey(7)+`"`, 1)
		current := `{"dns":{"servers":[{"type":"local","tag":"local"}]},"route":{"final":"proxy"},` +
			`"inbounds":[{"type":"mixed","tag":"mixed","listen":"127.0.0.1","listen_port":2080}],` +
			`"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"proxy","outbounds":["auto","direct"],"default":"auto"},` +
			`{"type":"urltest","tag":"auto","outbounds":["direct"]}],"endpoints":[` + existing + `]}`
		update, err := AddUIConfigNodes(current, "["+result+"]", false)
		if err != nil || update.Added != 1 || update.Skipped != 0 {
			t.Fatal("registered WireGuard endpoint was not appended", err)
		}
		if err = CheckConfig(update.Config); err != nil {
			t.Fatal("registered WireGuard endpoint failed complete config validation", err)
		}
		root, err := uiNodeDecodeObject(update.Config)
		if err != nil {
			t.Fatal(err)
		}
		before, err := uiNodeDecodeObject(current)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{"dns", "route", "inbounds"} {
			if !uiNodeEqual(root.get(field), before.get(field)) {
				t.Fatal("WireGuard append replaced selected config settings", field)
			}
		}
		if uiNodeTestTags(root.list("endpoints")) != "Existing,WARP" || uiNodeTestTags(root.list("outbounds")) != "direct,proxy,auto" ||
			!uiNodeEqual(root.list("endpoints")[0], before.list("endpoints")[0]) ||
			uiNodeTestMembers(root, "proxy") != "auto,direct,WARP" || uiNodeTestMembers(root, "auto") != "direct,WARP" ||
			uiNodeTestFind(root.list("outbounds"), "proxy").text("default") != "auto" {
			t.Fatal("WireGuard append changed existing nodes or failed to join the proxy groups")
		}
		uiNodeTestLocal(t, update.Config, "WARP")
		repeated, err := AddUIConfigNodes(update.Config, "["+result+"]", false)
		if err != nil || repeated.Added != 0 || repeated.Skipped != 1 || repeated.Config != update.Config {
			t.Fatal("retrying the saved WireGuard endpoint duplicated or changed the selected config", err)
		}
	})
}

func TestUIWARPTwoPhaseRegistrationAndConfigMerge(t *testing.T) {
	peerPEM, peerDER := uiWARPTestPeer(t)
	var calls atomic.Int32
	enrolledKeys := make(chan *ecdsa.PublicKey, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step := calls.Add(1)
		if r.Header.Get("User-Agent") != "WARP for Android" || r.Header.Get("CF-Client-Version") != "a-6.35-4471" ||
			r.Header.Get("Content-Type") != "application/json; charset=UTF-8" {
			t.Error("incorrect registration API headers")
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		der, err := base64.StdEncoding.DecodeString(body["key"])
		if err != nil {
			t.Error("invalid API public key")
			return
		}
		switch step {
		case 1:
			if r.Method != http.MethodPost || r.URL.Path != "/reg" || r.Header.Get("Authorization") != "" || body["key_type"] != "curve25519" ||
				body["tunnel_type"] != "wireguard" || len(body["serial_number"]) != 16 || body["tos"] == "" {
				t.Error("incorrect initial device registration")
			}
			if _, err = ecdh.X25519().NewPublicKey(der); err != nil {
				t.Error(err)
			}
			io.WriteString(w, `{"id":"mock-device","token":"mock-secret-api-token"}`)
		case 2:
			if r.Method != http.MethodPatch || r.URL.Path != "/reg/mock-device" || r.Header.Get("Authorization") != "Bearer mock-secret-api-token" ||
				body["key_type"] != "secp256r1" || body["tunnel_type"] != "masque" {
				t.Error("incorrect MASQUE key enrollment")
			}
			parsed, parseErr := x509.ParsePKIXPublicKey(der)
			if parseErr != nil {
				t.Error(parseErr)
				return
			}
			enrolled, ok := parsed.(*ecdsa.PublicKey)
			if !ok || enrolled.Curve != elliptic.P256() {
				t.Error("enrolled key must be P-256")
				return
			}
			enrolledKeys <- enrolled
			io.WriteString(w, uiWARPTestEnrollment(t, peerPEM, "172.16.0.2", "2001:db8::2"))
		default:
			t.Error("registration repeated a non-idempotent request")
		}
	}))
	defer server.Close()
	result, err := uiWARPTestRegister(t, context.Background(), server, NewUIDirectEgressProbe())
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("registration must use exactly POST then PATCH")
	}
	for _, secret := range []string{"mock-device", "mock-secret-api-token", "mock-secret-license"} {
		if strings.Contains(result, secret) {
			t.Fatal("unneeded API credential crossed the binding")
		}
	}
	var node map[string]any
	if err = json.Unmarshal([]byte(result), &node); err != nil {
		t.Fatal(err)
	}
	if node["server"] != "162.159.198.2" || node["server_port"] != float64(443) || node["http_version"] != float64(3) ||
		node["peer_public_key"] != peerDER || node["tls"].(map[string]any)["server_name"] != "consumer-masque.cloudflareclient.com" {
		t.Fatal("incorrect MASQUE node metadata")
	}
	if !reflect.DeepEqual(node["address"], []any{"172.16.0.2/32", "2001:db8::2/128"}) {
		t.Fatal("API addresses were not saved as host prefixes")
	}
	privateDER, err := base64.StdEncoding.DecodeString(node["private_key"].(string))
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.ParseECPrivateKey(privateDER)
	if err != nil || !private.PublicKey.Equal(<-enrolledKeys) {
		t.Fatal("saved private key does not match the enrolled key")
	}
	current := `{"dns":{"servers":[{"type":"local","tag":"local"}]},"route":{"final":"proxy"},"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","tag":"proxy","outbounds":["direct"]}]}`
	update, err := AddUIConfigNodes(current, "["+result+"]", false)
	if err != nil {
		t.Fatal(err)
	}
	if update.Added != 1 || update.Skipped != 0 {
		t.Fatal("registered node was not appended")
	}
	if err = CheckConfig(update.Config); err != nil {
		t.Fatal("registered node failed full config validation", err)
	}
	root, err := uiNodeDecodeObject(update.Config)
	if err != nil {
		t.Fatal(err)
	}
	if root.object("route").text("final") != "proxy" || root.object("dns").list("servers")[0].(*uiNodeObject).text("tag") != "local" {
		t.Fatal("registration replaced the selected config's routing or DNS")
	}
}

func TestUIWARPHTTPFailuresNeverRetryOrExposeResponse(t *testing.T) {
	for _, tunnel := range []string{"masque", "wireguard"} {
		for _, status := range []int{302, 400, 403, 429, 500} {
			t.Run(tunnel+"/"+http.StatusText(status), func(t *testing.T) {
				var calls, redirects atomic.Int32
				destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirects.Add(1) }))
				defer destination.Close()
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					w.Header().Set("Location", destination.URL+"/mock-secret-api-token")
					w.WriteHeader(status)
					io.WriteString(w, `{"errors":[{"message":"mock-secret-api-token mock-secret-private-key"}]}`)
				}))
				defer server.Close()
				result, err := uiWARPTestRegisterTunnel(t, context.Background(), server, NewUIDirectEgressProbe(), tunnel == "wireguard")
				if err == nil || result != "" {
					t.Fatal("HTTP rejection returned a node")
				}
				if strings.Contains(err.Error(), "mock-secret") || strings.Contains(err.Error(), server.URL) {
					t.Fatal("API error exposed credentials or URL")
				}
				if calls.Load() != 1 || redirects.Load() != 0 {
					t.Fatal("POST retried or followed a redirect")
				}
			})
		}
	}
}

func TestUIWARPCancellationAtBothAPIPhases(t *testing.T) {
	for _, phase := range []string{"register", "enroll", "wireguard"} {
		t.Run(phase, func(t *testing.T) {
			arrived := make(chan struct{}, 1)
			cancelled := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if phase == "enroll" && r.Method == http.MethodPost {
					io.WriteString(w, `{"id":"mock-device","token":"mock-token"}`)
					return
				}
				// Drain the request so net/http can observe peer cancellation.
				io.Copy(io.Discard, r.Body)
				arrived <- struct{}{}
				<-r.Context().Done()
				cancelled <- struct{}{}
			}))
			defer server.Close()
			probe := NewUIDirectEgressProbe()
			defer probe.Close()
			result := make(chan error, 1)
			go func() {
				_, err := uiWARPTestRegisterTunnel(t, context.Background(), server, probe, phase == "wireguard")
				result <- err
			}()
			select {
			case <-arrived:
			case <-time.After(3 * time.Second):
				t.Fatal("API phase was not reached")
			}
			probe.Close()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancelled registration returned success or another error", err)
				}
			case <-time.After(time.Second):
				t.Fatal("registration cancellation did not unblock")
			}
			select {
			case <-cancelled:
			case <-time.After(time.Second):
				t.Fatal("API connection remained alive after cancellation")
			}
		})
	}
}

func TestUIWARPWireGuardAPIEndpoints(t *testing.T) {
	peer := uiWARPTestWireGuardPeer(t)
	for _, test := range []struct {
		name     string
		endpoint map[string]string
		address  string
		port     float64
		ipv4     string
		ipv6     string
	}{
		{"domain host", map[string]string{"host": "Engage.CloudflareClient.com:2408"}, "engage.cloudflareclient.com", 2408, "172.16.0.2", ""},
		{"IPv4 host", map[string]string{"host": "162.159.192.1:500"}, "162.159.192.1", 500, "172.16.0.2", ""},
		{"IPv6 host", map[string]string{"host": "[2606:4700:d0::1]:2408"}, "2606:4700:d0::1", 2408, "", "2001:db8::2"},
		{"IPv4 API address", map[string]string{"v4": "162.159.192.1:4500", "v6": "[2606:4700:d0::1]:2408"}, "162.159.192.1", 4500, "172.16.0.2", ""},
		{"IPv6 API address", map[string]string{"v6": "[2606:4700:d0::1]:2408"}, "2606:4700:d0::1", 2408, "", "2001:db8::2"},
		{"missing API endpoint", nil, "engage.cloudflareclient.com", 2408, "172.16.0.2", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := uiWARPTestWireGuardResponse(t, peer, "AQID", test.ipv4, test.ipv6, test.endpoint)
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				io.WriteString(w, body)
			}))
			defer server.Close()
			result, err := uiWARPTestRegisterTunnel(t, context.Background(), server, NewUIDirectEgressProbe(), true)
			if err != nil || calls.Load() != 1 {
				t.Fatal("valid WireGuard response failed single-phase registration", err)
			}
			var node map[string]any
			if err = json.Unmarshal([]byte(result), &node); err != nil {
				t.Fatal(err)
			}
			peer := node["peers"].([]any)[0].(map[string]any)
			if peer["address"] != test.address || peer["port"] != test.port {
				t.Fatal("API endpoint was discarded or incorrectly split")
			}
			if _, err = parseConfig(baseContext(nil), `{"endpoints":[`+result+`]}`); err != nil {
				t.Fatal("API endpoint failed current-core options validation", err)
			}
		})
	}
}

func TestUIWARPWireGuardRejectsMalformedRegistration(t *testing.T) {
	peer := uiWARPTestWireGuardPeer(t)
	valid := uiWARPTestWireGuardResponse(t, peer, "AQID", "172.16.0.2", "2001:db8::2", map[string]string{"host": "engage.cloudflareclient.com:2408"})
	response := func(peer, clientID, ipv4, ipv6, endpoint string) string {
		return uiWARPTestWireGuardResponse(t, peer, clientID, ipv4, ipv6, map[string]string{"host": endpoint})
	}
	for _, test := range []struct{ name, body string }{
		{"invalid JSON", "not json"},
		{"missing config", `{"id":"mock-device","token":"mock-secret-api-token"}`},
		{"missing device ID", strings.Replace(valid, `"id":"mock-device"`, `"id":""`, 1)},
		{"invalid device ID", strings.Replace(valid, `"id":"mock-device"`, `"id":"mock-secret/device"`, 1)},
		{"missing token", strings.Replace(valid, `"token":"mock-secret-api-token"`, `"token":""`, 1)},
		{"invalid token", strings.Replace(valid, `"token":"mock-secret-api-token"`, `"token":"mock-secret\r\n"`, 1)},
		{"missing addresses", response(peer, "AQID", "", "", "engage.cloudflareclient.com:2408")},
		{"wrong address family", response(peer, "AQID", "2001:db8::2", "", "engage.cloudflareclient.com:2408")},
		{"CIDR address", response(peer, "AQID", "172.16.0.2/32", "", "engage.cloudflareclient.com:2408")},
		{"unspecified address", response(peer, "AQID", "0.0.0.0", "", "engage.cloudflareclient.com:2408")},
		{"multicast address", response(peer, "AQID", "224.0.0.1", "", "engage.cloudflareclient.com:2408")},
		{"zoned IPv6 address", response(peer, "AQID", "", "fe80::2%test", "engage.cloudflareclient.com:2408")},
		{"missing peer key", response("", "AQID", "172.16.0.2", "", "engage.cloudflareclient.com:2408")},
		{"invalid peer key", response("mock-secret-peer-key", "AQID", "172.16.0.2", "", "engage.cloudflareclient.com:2408")},
		{"short peer key", response("AQID", "AQID", "172.16.0.2", "", "engage.cloudflareclient.com:2408")},
		{"zero peer key", response(base64.StdEncoding.EncodeToString(make([]byte, 32)), "AQID", "172.16.0.2", "", "engage.cloudflareclient.com:2408")},
		{"missing client ID", response(peer, "", "172.16.0.2", "", "engage.cloudflareclient.com:2408")},
		{"invalid client ID", response(peer, "mock-secret-client-id", "172.16.0.2", "", "engage.cloudflareclient.com:2408")},
		{"short client ID", response(peer, "AQI=", "172.16.0.2", "", "engage.cloudflareclient.com:2408")},
		{"long client ID", response(peer, "AQIDBA==", "172.16.0.2", "", "engage.cloudflareclient.com:2408")},
		{"endpoint URL", response(peer, "AQID", "172.16.0.2", "", "https://engage.cloudflareclient.com:2408")},
		{"missing endpoint host", response(peer, "AQID", "172.16.0.2", "", ":2408")},
		{"missing endpoint port", response(peer, "AQID", "172.16.0.2", "", "engage.cloudflareclient.com")},
		{"zero endpoint port", response(peer, "AQID", "172.16.0.2", "", "engage.cloudflareclient.com:0")},
		{"overflow endpoint port", response(peer, "AQID", "172.16.0.2", "", "engage.cloudflareclient.com:65536")},
		{"named endpoint port", response(peer, "AQID", "172.16.0.2", "", "engage.cloudflareclient.com:https")},
		{"unspecified endpoint", response(peer, "AQID", "172.16.0.2", "", "[::]:2408")},
		{"multicast endpoint", response(peer, "AQID", "172.16.0.2", "", "224.0.0.1:2408")},
		{"invalid hostname", response(peer, "AQID", "172.16.0.2", "", "engage..cloudflareclient.com:2408")},
		{"API IPv4 family mismatch", uiWARPTestWireGuardResponse(t, peer, "AQID", "172.16.0.2", "", map[string]string{"v4": "[2606:4700:d0::1]:2408"})},
		{"API IPv6 family mismatch", uiWARPTestWireGuardResponse(t, peer, "AQID", "172.16.0.2", "", map[string]string{"v6": "162.159.192.1:2408"})},
		{"oversized response", strings.Repeat(" ", uiWARPMaxResponse+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				io.WriteString(w, test.body)
			}))
			defer server.Close()
			result, err := uiWARPTestRegisterTunnel(t, context.Background(), server, NewUIDirectEgressProbe(), true)
			if err == nil || result != "" || calls.Load() != 1 {
				t.Fatal("invalid registration returned an endpoint or retried the request")
			}
			if strings.Contains(err.Error(), "mock-secret") || strings.Contains(err.Error(), server.URL) {
				t.Fatal("WireGuard response validation exposed API credentials or URL")
			}
		})
	}
}

func TestUIWARPWireGuardTaskCompatibility(t *testing.T) {
	peer := uiWARPTestWireGuardPeer(t)
	body := uiWARPTestWireGuardResponse(t, peer, "AQID", "172.16.0.2", "", map[string]string{"host": "162.159.192.1:2408"})
	var calls atomic.Int32
	probe := NewUIDirectEgressProbe()
	probe.client.Transport = uiWARPRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: request}, nil
	})
	task := newUIWARPRegistrationTask(probe)
	result, err := task.RegisterWireGuard(" WARP WireGuard ")
	if err != nil || calls.Load() != 1 {
		t.Fatal("WireGuard registration binding failed", err)
	}
	var node map[string]any
	if err = json.Unmarshal([]byte(result), &node); err != nil || node["tag"] != "WARP WireGuard" {
		t.Fatal("WireGuard binding did not validate and trim its node name", err)
	}
	if _, err = task.Register("WARP MASQUE", 2); err == nil || !strings.Contains(err.Error(), "already used") || calls.Load() != 1 {
		t.Fatal("a completed WireGuard task could register another MASQUE device")
	}
	for _, name := range []string{"", "\n", "WARP\x00", "WARP\rName"} {
		task = NewUIWARPRegistrationTask()
		if _, err = task.RegisterWireGuard(name); err == nil {
			t.Fatal("invalid WireGuard name began registration")
		}
		if _, err = task.Register("WARP MASQUE", 2); err == nil || !strings.Contains(err.Error(), "already used") {
			t.Fatal("a rejected WireGuard request allowed another registration")
		}
	}
	task = NewUIWARPRegistrationTask()
	if _, err = task.Register("WARP MASQUE", 4); err == nil {
		t.Fatal("invalid MASQUE carrier accepted")
	}
	if _, err = task.RegisterWireGuard("WARP WireGuard"); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatal("a rejected MASQUE task could register a WireGuard device")
	}
	task = NewUIWARPRegistrationTask()
	task.Close()
	if result, err = task.RegisterWireGuard("WARP WireGuard"); result != "" || !errors.Is(err, context.Canceled) {
		t.Fatal("a closed WireGuard registration task began registration")
	}
}

func TestUIWARPRejectsMalformedEnrollment(t *testing.T) {
	peer, _ := uiWARPTestPeer(t)
	for _, body := range []string{
		`not json`, `{"config":{}}`, uiWARPTestEnrollment(t, peer, "", ""), uiWARPTestEnrollment(t, peer, "0.0.0.0", ""),
		uiWARPTestEnrollment(t, peer, "2001:db8::2", ""), uiWARPTestEnrollment(t, peer, "172.16.0.2/32", ""),
		uiWARPTestEnrollment(t, "malformed-peer-key", "172.16.0.2", ""), strings.Repeat(" ", uiWARPMaxResponse+1),
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				io.WriteString(w, `{"id":"mock-device","token":"mock-token"}`)
				return
			}
			io.WriteString(w, body)
		}))
		result, err := uiWARPTestRegister(t, context.Background(), server, NewUIDirectEgressProbe())
		server.Close()
		if err == nil || result != "" {
			t.Fatal("invalid enrollment returned a node")
		}
	}
}

func TestUIWARPServiceChangeAndTaskReuse(t *testing.T) {
	var current atomic.Bool
	current.Store(true)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		current.Store(false)
		io.WriteString(w, `{"id":"mock-device","token":"mock-token"}`)
	}))
	defer server.Close()
	probe := newUIEgressProbe(current.Load, nil)
	result, err := uiWARPTestRegister(t, context.Background(), server, probe)
	if err == nil || result != "" || calls.Load() != 1 {
		t.Fatal("service change allowed enrollment or direct fallback")
	}
	for _, version := range []int32{1, 4} {
		task := NewUIWARPRegistrationTask()
		if _, err = task.Register("WARP", version); err == nil {
			t.Fatal("invalid carrier accepted")
		}
		if _, err = task.Register("WARP", 2); err == nil || !strings.Contains(err.Error(), "already used") {
			t.Fatal("task may repeat POST")
		}
	}
	task := NewUIWARPRegistrationTask()
	task.Close()
	if result, err = task.Register("WARP", 2); result != "" || !errors.Is(err, context.Canceled) {
		t.Fatal("closed task began registration")
	}
}

type uiWARPRoundTripper func(*http.Request) (*http.Response, error)

func (f uiWARPRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type uiWARPCompleteBody struct {
	io.Reader
	cancel context.CancelFunc
}

func (b *uiWARPCompleteBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		b.cancel()
	}
	return n, err
}
func (b *uiWARPCompleteBody) Close() error { return nil }

func TestUIWARPCompletedEnrollmentSurvivesCancellation(t *testing.T) {
	peer, _ := uiWARPTestPeer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := NewUIDirectEgressProbe()
	defer probe.Close()
	probe.client.Transport = uiWARPRoundTripper(func(request *http.Request) (*http.Response, error) {
		var body io.ReadCloser = io.NopCloser(strings.NewReader(`{"id":"mock-device","token":"mock-token"}`))
		if request.Method == http.MethodPatch {
			// A full enrollment has already reached the client when cancellation
			// wins at EOF. Its credentials must reach Android's pending store.
			body = &uiWARPCompleteBody{Reader: strings.NewReader(uiWARPTestEnrollment(t, peer, "172.16.0.2", "")), cancel: cancel}
		}
		return &http.Response{StatusCode: 200, Body: body, Header: make(http.Header), Request: request}, nil
	})
	task := newUIWARPRegistrationTask(probe)
	result, err := uiWARPRegister(ctx, task.probe, uiWARPAPI, "WARP", 3)
	if err != nil || result == "" || !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("complete enrollment was lost at the cancellation boundary", err)
	}
	if _, err = AddUIConfigNodes(`{}`, "["+result+"]", false); err != nil {
		t.Fatal("completed pending node is invalid", err)
	}
}

func TestUIWARPCompletedWireGuardRegistrationSurvivesCancellation(t *testing.T) {
	peer := uiWARPTestWireGuardPeer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	probe := NewUIDirectEgressProbe()
	defer probe.Close()
	body := uiWARPTestWireGuardResponse(t, peer, "AQID", "172.16.0.2", "", map[string]string{"host": "162.159.192.1:2408"})
	var calls atomic.Int32
	probe.client.Transport = uiWARPRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: 200, Header: make(http.Header), Request: request,
			Body: &uiWARPCompleteBody{Reader: strings.NewReader(body), cancel: cancel},
		}, nil
	})
	task := newUIWARPRegistrationTask(probe)
	result, err := uiWARPRegisterWireGuard(ctx, task.probe, uiWARPAPI, "WARP")
	if err != nil || result == "" || !errors.Is(ctx.Err(), context.Canceled) || calls.Load() != 1 {
		t.Fatal("completed WireGuard registration was lost at the cancellation boundary", err)
	}
	if _, err = parseConfig(baseContext(nil), `{"endpoints":[`+result+`]}`); err != nil {
		t.Fatal("completed WireGuard pending endpoint has invalid options", err)
	}
	t.Run("pending config append", func(t *testing.T) {
		uiWARPTestRequireWireGuard(t)
		update, err := AddUIConfigNodes(`{}`, "["+result+"]", false)
		if err != nil {
			t.Fatal("completed WireGuard pending endpoint cannot be appended", err)
		}
		if err = CheckConfig(update.Config); err != nil {
			t.Fatal("completed WireGuard pending endpoint failed config validation", err)
		}
	})
}
