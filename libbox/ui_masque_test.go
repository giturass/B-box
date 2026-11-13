package libbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	stdjson "encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	transportHTTP "github.com/sagernet/sing-box/transport/http"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func uiMASQUEAccount(t *testing.T) (map[string]any, tls.Certificate) {
	t.Helper()
	client, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalECPrivateKey(client)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&peer.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"warp-test.invalid"},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &peer.PublicKey, peer)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"type": "masque", "tag": "warp", "server": "127.0.0.1", "server_port": 443,
		"private_key": base64.StdEncoding.EncodeToString(private), "peer_public_key": base64.StdEncoding.EncodeToString(public),
		"address": []string{"172.16.0.2/32", "2001:db8::2/128"}, "version": 2,
	}, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: peer}
}

func uiMASQUEConfig(t *testing.T, account map[string]any) string {
	t.Helper()
	content, err := stdjson.Marshal(map[string]any{
		"outbounds": []any{account, map[string]any{"type": "selector", "tag": "proxy", "outbounds": []string{"warp"}}},
		"route":     map[string]any{"final": "proxy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestUIMASQUEConfiguration(t *testing.T) {
	account, _ := uiMASQUEAccount(t)
	for _, version := range []int{2, 3, 0} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			account["version"] = version
			content := uiMASQUEConfig(t, account)
			err := CheckConfig(content)
			if version != 2 && transportHTTP.NewHTTP3Client == nil {
				if err == nil {
					t.Fatal("HTTP/3 accepted without the QUIC build tag")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			formatted, err := FormatConfig(content)
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err = stdjson.Unmarshal([]byte(formatted.Value), &decoded); err != nil {
				t.Fatal(err)
			}
			outbound := decoded["outbounds"].([]any)[0].(map[string]any)
			for _, key := range []string{"type", "tag", "private_key", "peer_public_key", "address"} {
				original, _ := stdjson.Marshal(account[key])
				actual, _ := stdjson.Marshal(outbound[key])
				if !bytes.Equal(original, actual) {
					t.Errorf("formatting changed %s", key)
				}
			}
			if err = CheckConfig(formatted.Value); err != nil {
				t.Fatal(err)
			}
		})
	}
	// Keep the upstream RFC 9484 endpoint independently configurable.
	if err := CheckConfig(`{"endpoints":[{"type":"masque-client","tag":"standard","server":"127.0.0.1","server_port":443,"version":2,"tls":{"enabled":true}}]}`); err != nil {
		t.Fatal(err)
	}
}

func TestUIMASQUERejectsInvalidAccountConfiguration(t *testing.T) {
	for _, test := range []struct {
		field string
		value any
	}{
		{"server", ""}, {"server_port", 0}, {"version", 1}, {"version", 4},
		{"private_key", ""}, {"private_key", "invalid"}, {"peer_public_key", ""}, {"peer_public_key", "invalid"},
		{"address", []string{}}, {"address", []string{"172.16.0.2/24"}}, {"address", []string{"0.0.0.0/32"}},
		{"address", []string{"172.16.0.2/32", "172.16.0.3/32"}}, {"address", []string{"2001:db8::2/128", "2001:db8::3/128"}},
		{"mtu", 1279}, {"mtu", 65536}, {"network", "h2"}, {"tls", map[string]any{"insecure": true}},
	} {
		account, _ := uiMASQUEAccount(t)
		account[test.field] = test.value
		if err := CheckConfig(uiMASQUEConfig(t, account)); err == nil {
			t.Errorf("invalid %s accepted: %v", test.field, test.value)
		}
	}
}

func TestUIMASQUEVPNExample(t *testing.T) {
	content, err := os.ReadFile("../../../examples/warp-masque.json")
	if err != nil {
		t.Fatal(err)
	}
	var example map[string]any
	if err = stdjson.Unmarshal(content, &example); err != nil {
		t.Fatal(err)
	}
	account, _ := uiMASQUEAccount(t)
	outbound := example["outbounds"].([]any)[0].(map[string]any)
	outbound["private_key"], outbound["peer_public_key"] = account["private_key"], account["peer_public_key"]
	for _, version := range []int{2, 3} {
		if version == 3 && transportHTTP.NewHTTP3Client == nil {
			continue
		}
		outbound["version"] = version
		encoded, err := stdjson.Marshal(example)
		if err != nil {
			t.Fatal(err)
		}
		if err = CheckConfig(string(encoded)); err != nil {
			t.Fatal(err)
		}
		if hasTun, err := HasTunInbound(string(encoded)); err != nil || !hasTun {
			t.Fatal("WARP example must retain the Android VPN entry", err)
		}
	}
}

func TestUIMASQUEHTTP2OutboundUDPAndNetworkReset(t *testing.T) {
	account, certificate := uiMASQUEAccount(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.Method != http.MethodConnect || r.Header.Get("Cf-Connect-Proto") != "cf-connect-ip" {
			http.Error(w, "invalid WARP handshake", 400)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		reader := bufio.NewReader(r.Body)
		for {
			kind, _, err := transportHTTP.ReadVarint(reader)
			if err != nil {
				return
			}
			length, _, err := transportHTTP.ReadVarint(reader)
			if err != nil || kind != 0 || length < 28 || length > 65535 {
				return
			}
			packet := make([]byte, length)
			if _, err = io.ReadFull(reader, packet); err != nil || packet[0] != 0x45 || packet[9] != 17 {
				return
			}
			// This local WARP peer reflects a complete IPv4/UDP packet, so the
			// actual sing-box IP stack must recover the original socket payload.
			source := append([]byte(nil), packet[12:16]...)
			copy(packet[12:16], packet[16:20])
			copy(packet[16:20], source)
			packet[20], packet[22] = packet[22], packet[20]
			packet[21], packet[23] = packet[23], packet[21]
			packet[26], packet[27] = 0, 0 // UDP checksum is optional in IPv4.
			packet[10], packet[11] = 0, 0
			var checksum uint32
			for i := 0; i < 20; i += 2 {
				checksum += uint32(binary.BigEndian.Uint16(packet[i : i+2]))
			}
			for checksum > 65535 {
				checksum = checksum>>16 + checksum&65535
			}
			binary.BigEndian.PutUint16(packet[10:12], ^uint16(checksum))
			frame := make([]byte, 1+transportHTTP.VarintLen(length))
			transportHTTP.PutVarint(frame[1:], length)
			if _, err = w.Write(append(frame, packet...)); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	server.EnableHTTP2 = true
	server.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, ClientAuth: tls.RequireAnyClientCert}
	server.StartTLS()
	t.Cleanup(server.Close)
	account["server_port"] = server.Listener.Addr().(*net.TCPAddr).Port
	ctx, cancel := context.WithCancel(baseContext(nil))
	t.Cleanup(cancel)
	options, err := parseConfig(ctx, uiMASQUEConfig(t, account))
	if err != nil {
		t.Fatal(err)
	}
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { instance.Close() })
	if err = instance.Start(); err != nil {
		t.Fatal(err)
	}
	proxy, found := instance.Outbound().Outbound("proxy")
	if !found {
		t.Fatal("missing selector for WARP outbound")
	}
	for i := range 2 {
		dialCtx, cancelDial := context.WithTimeout(ctx, 5*time.Second)
		conn, err := proxy.DialContext(dialCtx, N.NetworkUDP, M.ParseSocksaddr("198.51.100.1:31337"))
		cancelDial()
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		payload := bytes.Repeat([]byte("WARP"), 256)
		if _, err = conn.Write(payload); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		response := make([]byte, 2048)
		n, err := conn.Read(response)
		conn.Close()
		if err != nil || !reflect.DeepEqual(response[:n], payload) {
			t.Fatal("UDP payload did not pass through the WARP outbound", err)
		}
		if i == 0 {
			instance.Network().ResetNetwork(ctx)
		}
	}
}
