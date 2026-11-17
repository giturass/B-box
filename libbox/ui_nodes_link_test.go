package libbox

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestUINodeLinkProtocols(t *testing.T) {
	realityKey := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
	privateKey, publicKey := uiNodeTestKey(1), uiNodeTestKey(2)
	uiNodeTestLinks(t, []uiNodeTestLink{
		{
			"VLESS over a WebSocket with early data",
			"vless://" + uiNodeTestUUID + "@example.com:443?encryption=none&security=tls&type=ws&host=cdn.example.com&path=%2Fws%3Fed%3D2048&fp=chrome&alpn=h2%2Chttp%2F1.1#VLESS%20WS",
			`{"type":"vless","tag":"VLESS WS","server":"example.com","server_port":443,"uuid":"` + uiNodeTestUUID + `","tls":{"enabled":true,"server_name":"cdn.example.com","alpn":["h2","http/1.1"],"utls":{"enabled":true,"fingerprint":"chrome"}},"transport":{"type":"ws","path":"/ws","headers":{"Host":"cdn.example.com"},"max_early_data":2048,"early_data_header_name":"Sec-WebSocket-Protocol"}}`,
		},
		{
			"VLESS with REALITY and Vision",
			"vless://" + uiNodeTestUUID + "@reality.example.com:443?security=reality&sni=www.example.org&fp=firefox&pbk=" + realityKey + "&sid=0123abcd&flow=xtls-rprx-vision&type=tcp#REALITY",
			`{"type":"vless","tag":"REALITY","server":"reality.example.com","server_port":443,"uuid":"` + uiNodeTestUUID + `","flow":"xtls-rprx-vision","tls":{"enabled":true,"server_name":"www.example.org","utls":{"enabled":true,"fingerprint":"firefox"},"reality":{"enabled":true,"public_key":"` + realityKey + `","short_id":"0123abcd"}}}`,
		},
		{
			"Trojan with the default port and TLS",
			"trojan://pass%40word@trojan.example.com?sni=sni.example.com&allowInsecure=1#Trojan",
			`{"type":"trojan","tag":"Trojan","server":"trojan.example.com","server_port":443,"password":"pass@word","tls":{"enabled":true,"server_name":"sni.example.com","insecure":true}}`,
		},
		{
			"Trojan in clear with an unescaped password",
			"trojan://pa:ss/word@trojan.example.com:8443?security=none&type=ws&path=/t#T2",
			`{"type":"trojan","tag":"T2","server":"trojan.example.com","server_port":8443,"password":"pa:ss/word","transport":{"type":"ws","path":"/t"}}`,
		},
		{
			"Hysteria",
			"hysteria://hy.example.com:443?protocol=udp&auth=secret&peer=sni.example.com&insecure=1&upmbps=20&downmbps=100&alpn=h3&obfsParam=obfs#Hysteria",
			`{"type":"hysteria","tag":"Hysteria","server":"hy.example.com","server_port":443,"up_mbps":20,"down_mbps":100,"obfs":"obfs","auth_str":"secret","tls":{"enabled":true,"server_name":"sni.example.com","insecure":true,"alpn":["h3"]}}`,
		},
		{
			"Hysteria without rates",
			"hysteria://hy.example.com:443#Slow",
			`{"type":"hysteria","tag":"Slow","server":"hy.example.com","server_port":443,"up_mbps":10,"down_mbps":50,"tls":{"enabled":true}}`,
		},
		{
			"Hysteria 2 with port hopping, obfuscation and a pin",
			"hy2://pass@hy2.example.com:8443,9000-9010/?sni=sni.example.com&obfs=salamander&obfs-password=obfspass&pinSHA256=" + strings.Repeat("AB:", 31) + "AB#Hy2",
			`{"type":"hysteria2","tag":"Hy2","server":"hy2.example.com","server_port":8443,"server_ports":["8443:8443","9000:9010"],"password":"pass","obfs":{"type":"salamander","password":"obfspass"},"tls":{"enabled":true,"server_name":"sni.example.com","certificate_sha256":["` + uiNodeTestKey(0xab) + `"]}}`,
		},
		{
			"TUIC",
			"tuic://" + uiNodeTestUUID + ":pass@tuic.example.com:443?congestion_control=bbr&udp_relay_mode=native&alpn=h3&sni=sni.example.com&reduce_rtt=1#TUIC",
			`{"type":"tuic","tag":"TUIC","server":"tuic.example.com","server_port":443,"uuid":"` + uiNodeTestUUID + `","password":"pass","congestion_control":"bbr","udp_relay_mode":"native","zero_rtt_handshake":true,"tls":{"enabled":true,"server_name":"sni.example.com","alpn":["h3"]}}`,
		},
		{
			"AnyTLS",
			"anytls://pass@anytls.example.com:8443?sni=sni.example.com&insecure=1&idle_session_check_interval=30#AnyTLS",
			`{"type":"anytls","tag":"AnyTLS","server":"anytls.example.com","server_port":8443,"password":"pass","idle_session_check_interval":"30s","tls":{"enabled":true,"server_name":"sni.example.com","insecure":true}}`,
		},
		{
			"WireGuard",
			"wireguard://" + privateKey + "@wg.example.com:51820?publickey=" + publicKey + "&address=10.0.0.2%2F32%2Cfd00%3A%3A2&mtu=1280&reserved=1%2C2%2C3&keepalive=25#WG",
			`{"type":"wireguard","tag":"WG","mtu":1280,"address":["10.0.0.2/32","fd00::2/128"],"private_key":"` + privateKey + `","peers":[{"address":"wg.example.com","port":51820,"public_key":"` + publicKey + `","allowed_ips":["0.0.0.0/0","::/0"],"persistent_keepalive_interval":25,"reserved":[1,2,3]}]}`,
		},
		{
			"SOCKS5 with the login in base64",
			"socks5://" + base64.RawURLEncoding.EncodeToString([]byte("user:pass")) + "@192.0.2.20:1080#SOCKS",
			`{"type":"socks","tag":"SOCKS","server":"192.0.2.20","server_port":1080,"username":"user","password":"pass"}`,
		},
		{
			"SOCKS4a with the default port",
			"socks4a://192.0.2.21#S4",
			`{"type":"socks","tag":"S4","server":"192.0.2.21","server_port":1080,"version":"4a"}`,
		},
		{
			"NaiveProxy",
			"naive+https://user:pass@naive.example.com:443?insecure-concurrency=2#Naive",
			`{"type":"naive","tag":"Naive","server":"naive.example.com","server_port":443,"username":"user","password":"pass","insecure_concurrency":2,"tls":{"enabled":true}}`,
		},
	})
}

// Every refused link carries SECRET and refused.example; a reason is compared
// in full, so that it can hold neither.
func TestUINodeLinkRefusals(t *testing.T) {
	realityKey := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x11}, 32))
	for _, item := range []struct{ link, reason string }{
		{"SECRET", "this is not a share link"},
		{"ssr://U0VDUkVU", "ShadowsocksR was removed from the core"},
		{"juicity://SECRET:SECRET@refused.example:443", "this protocol is not supported by this core"},
		{"unknown://SECRET@refused.example:443", "this protocol is not supported by this core"},
		{"trojan://SECRET@:443", "the node has no server address"},
		{"trojan://SECRET@refused.example:70000", "the server port must be a number between 1 and 65535"},
		{"trojan://SECRET@refused.example:443?pcs=SECRET", "a certificate pin of this node is not a SHA-256 hash"},
		{"ss://*SECRET*", "the Shadowsocks link is not readable"},
		{"ss://aes-256-gcm:SECRET@refused.example:8388?plugin=shadow-tls%3Bhost%3DSECRET", "the Shadowsocks plugin of this node is not supported by this core"},
		{"vless://SECRET@refused.example:443?type=kcp&seed=SECRET", "the transport of this node is not supported by this core"},
		{"vless://SECRET@refused.example:443?security=reality&pbk=SECRET&sid=01", "REALITY needs the public key of the server"},
		{"vless://SECRET@refused.example:443?security=reality&pbk=" + realityKey + "&sid=SECRET", "the REALITY short ID must be at most 16 hexadecimal digits"},
		{"vless://SECRET@refused.example:443?security=reality&pbk=" + realityKey + "&pqv=SECRET", "REALITY with an ML-DSA-65 verification key is not supported by this core"},
		{"vmess://SECRET@refused.example:443?type=quic&quicSecurity=aes-128-gcm&key=SECRET", "the QUIC transport of this core takes neither encryption nor a header"},
		{"vmess://" + base64.StdEncoding.EncodeToString([]byte(`{"add":"refused.example","port":"443","id":"SECRET","tls":"reality"}`)), "the TLS setting of this VMess link is not supported by this core"},
		{"tuic://SECRET@refused.example:443", "TUIC with a token in place of a user ID and password is not supported by this core"},
		{"hysteria://refused.example:443?protocol=faketcp&auth=SECRET", "Hysteria over this transport is not supported by this core"},
		{"hysteria2://SECRET@refused.example:1-x", "the port list of this node is not readable"},
	} {
		node, err := uiNodeLinkParse(item.link)
		if err == nil {
			t.Errorf("a link to refuse with %q was read as %s", item.reason, uiNodeTestText(node))
		} else if err.Error() != item.reason {
			t.Errorf("refused with %q, want %q", err, item.reason)
		}
	}
}
