//go:build with_xhttp

package libbox

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	stdjson "encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/protocol/vless/encryption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

const uiXHTTPUUID = "00000000-0000-4000-8000-000000000001"

func uiXHTTPJSON(t *testing.T, content any) string {
	t.Helper()
	encoded, err := stdjson.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func TestUIXHTTPEncryptionConfiguration(t *testing.T) {
	privateKey, clientKey, err := encryption.GenerateX25519("")
	if err != nil {
		t.Fatal(err)
	}
	content := uiXHTTPJSON(t, map[string]any{
		"inbounds": []any{map[string]any{
			"type": "vless", "listen": "127.0.0.1", "listen_port": 23456,
			"users":      []any{map[string]any{"uuid": uiXHTTPUUID, "flow": "xtls-rprx-vision"}},
			"decryption": "mlkem768x25519plus.random.600s." + privateKey,
			"transport":  map[string]any{"type": "xhttp", "mode": "packet-up", "path": "/xhttp"},
		}},
		"outbounds": []any{map[string]any{
			"type": "vless", "tag": "encrypted", "server": "127.0.0.1", "server_port": 23456,
			"uuid": uiXHTTPUUID, "flow": "xtls-rprx-vision",
			"encryption": "mlkem768x25519plus.random.0rtt." + clientKey,
			"transport": map[string]any{
				"type": "xhttp", "mode": "packet-up", "path": "/xhttp?a=b",
				"x_padding_bytes": "100-1000", "session_placement": "header", "seq_placement": "query",
				"download_settings": map[string]any{"server_port": 23457, "path": "/download"},
			},
		}},
	})
	if err = CheckConfig(content); err != nil {
		t.Fatal(err)
	}
	formatted, err := FormatConfig(content)
	if err != nil {
		t.Fatal(err)
	}
	if err = CheckConfig(formatted.Value); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = stdjson.Unmarshal([]byte(formatted.Value), &decoded); err != nil {
		t.Fatal(err)
	}
	outbound := decoded["outbounds"].([]any)[0].(map[string]any)
	inbound := decoded["inbounds"].([]any)[0].(map[string]any)
	if outbound["encryption"] != "mlkem768x25519plus.random.0rtt."+clientKey || inbound["decryption"] != "mlkem768x25519plus.random.600s."+privateKey {
		t.Fatal("formatting changed VLESS encryption credentials")
	}
	transport := outbound["transport"].(map[string]any)
	if transport["type"] != "xhttp" || transport["path"] != "/xhttp?a=b" || transport["download_settings"].(map[string]any)["server_port"] != float64(23457) {
		t.Fatal("formatting changed XHTTP options")
	}
	schema, err := GenerateConfigSchema()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(schema.Value, `"xhttp"`) || !strings.Contains(schema.Value, `"encryption"`) || !strings.Contains(schema.Value, `"decryption"`) {
		t.Fatal("config schema omits XHTTP or VLESS encryption")
	}
}

func TestUIXHTTPEncryptionErrorsHideCredentials(t *testing.T) {
	privateKey, clientKey, err := encryption.GenerateX25519("")
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"encryption", "decryption"} {
		for _, badValue := range []string{
			"mlkem768x25519plus.invalid.600s." + privateKey,
			"mlkem768x25519plus.native.invalid." + privateKey,
			"mlkem768x25519plus.native.0rtt." + privateKey + ".bad-padding",
			"mlkem768x25519plus.native.600s." + privateKey + "x",
		} {
			node := map[string]any{"type": "vless", "uuid": uiXHTTPUUID, "server": "127.0.0.1", "server_port": 23456, "encryption": badValue}
			config := map[string]any{"outbounds": []any{node}}
			if field == "decryption" {
				node = map[string]any{"type": "vless", "listen": "127.0.0.1", "listen_port": 23456, "users": []any{map[string]any{"uuid": uiXHTTPUUID}}, "decryption": badValue}
				config = map[string]any{"inbounds": []any{node}}
			}
			err := CheckConfig(uiXHTTPJSON(t, config))
			if err == nil {
				t.Fatalf("invalid VLESS %s accepted", field)
			}
			if strings.Contains(err.Error(), privateKey) || strings.Contains(err.Error(), clientKey) || strings.Contains(err.Error(), badValue) {
				t.Fatalf("VLESS %s error contains credentials", field)
			}
		}
	}
}

func uiXHTTPCertificate(t *testing.T) (tls.Certificate, string, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), DNSNames: []string{"xhttp.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key},
		string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
}

func uiXHTTPStartBox(t *testing.T, content map[string]any) *box.Box {
	t.Helper()
	ctx, cancel := context.WithCancel(baseContext(nil))
	t.Cleanup(cancel)
	options, err := parseConfig(ctx, uiXHTTPJSON(t, content))
	if err != nil {
		t.Fatal(err)
	}
	instance, err := box.New(box.Options{Context: ctx, Options: options})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); instance.Close() })
	if err = instance.Start(); err != nil {
		t.Fatal(err)
	}
	return instance
}

func TestUIXHTTPEncryptionInboundHandshakeCancellation(t *testing.T) {
	privateKey, _, err := encryption.GenerateX25519("")
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"caller-cancel", "service-close"} {
		t.Run(action, func(t *testing.T) {
			instance := uiXHTTPStartBox(t, map[string]any{
				"log": map[string]any{"disabled": true},
				"inbounds": []any{map[string]any{
					"type": "vless", "tag": "encrypted", "listen": "127.0.0.1",
					"users":      []any{map[string]any{"uuid": uiXHTTPUUID}},
					"decryption": "mlkem768x25519plus.native.600s." + privateKey,
				}},
			})
			inbound, found := instance.Inbound().Get("encrypted")
			if !found {
				t.Fatal("missing encrypted inbound")
			}
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			serverConn, peer := net.Pipe()
			t.Cleanup(func() { serverConn.Close(); peer.Close() })
			closed := make(chan error, 1)
			done := make(chan struct{})
			go func() {
				defer close(done)
				inbound.(adapter.ConnectionHandler).NewConnection(ctx, serverConn, adapter.InboundContext{
					Source: M.ParseSocksaddr("127.0.0.1:12345"),
				}, func(err error) { closed <- err })
			}()
			// A partial hello confirms the production handshake has entered its
			// blocking read before cancellation or the core lifecycle closes it.
			peer.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err = peer.Write([]byte{1}); err != nil {
				t.Fatal(err)
			}
			if action == "caller-cancel" {
				cancel()
			} else if err = instance.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("cancelled encryption handshake is still blocked")
			}
			if err = <-closed; !errors.Is(err, context.Canceled) {
				t.Fatal("handshake did not report cancellation", err)
			}
		})
	}
}

// Exercise both full sing-box inbound/outbound lifecycles, their transport
// registries, VLESS headers and Vision's inner TLS 1.3 copy path. No internet or
// Android VPN is involved. The second connection reuses Encryption's ticket.
func TestUIXHTTPEncryptionVisionCore(t *testing.T) {
	certificate, certPEM, keyPEM := uiXHTTPCertificate(t)
	echo := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(w, r.Body)
	}))
	echo.TLS = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS13}
	echo.StartTLS()
	t.Cleanup(echo.Close)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(certPEM))
	for _, transport := range []struct {
		name, kind, mode string
		outerTLS         bool
	}{
		{"xhttp-h1", "xhttp", "packet-up", false},
		{"xhttp-h2", "xhttp", "stream-one", true},
		{"websocket", "ws", "", false},
	} {
		for _, encryptionMode := range []string{"native", "xorpub", "random"} {
			t.Run(transport.name+"/"+encryptionMode, func(t *testing.T) {
				privateKey, clientKey, err := encryption.GenerateX25519("")
				if err != nil {
					t.Fatal(err)
				}
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				port := listener.Addr().(*net.TCPAddr).Port
				listener.Close()
				transportOptions := map[string]any{"type": transport.kind, "path": "/xhttp"}
				if transport.mode != "" {
					transportOptions["mode"] = transport.mode
				}
				inbound := map[string]any{
					"type": "vless", "listen": "127.0.0.1", "listen_port": port,
					"users":      []any{map[string]any{"uuid": uiXHTTPUUID, "flow": "xtls-rprx-vision"}},
					"decryption": "mlkem768x25519plus." + encryptionMode + ".600s." + privateKey,
					"transport":  transportOptions,
				}
				outbound := map[string]any{
					"type": "vless", "tag": "encrypted", "server": "127.0.0.1", "server_port": port,
					"uuid": uiXHTTPUUID, "flow": "xtls-rprx-vision",
					"encryption": "mlkem768x25519plus." + encryptionMode + ".0rtt." + clientKey,
					"transport":  transportOptions,
				}
				if transport.outerTLS {
					inbound["tls"] = map[string]any{"enabled": true, "certificate": []string{certPEM}, "key": []string{keyPEM}, "alpn": []string{"h2"}}
					outbound["tls"] = map[string]any{"enabled": true, "server_name": "xhttp.test", "certificate": []string{certPEM}, "alpn": []string{"h2"}}
				}
				uiXHTTPStartBox(t, map[string]any{"log": map[string]any{"disabled": true}, "inbounds": []any{inbound}, "outbounds": []any{map[string]any{"type": "direct"}}})
				client := uiXHTTPStartBox(t, map[string]any{"log": map[string]any{"disabled": true}, "outbounds": []any{outbound}})
				proxy, found := client.Outbound().Outbound("encrypted")
				if !found {
					t.Fatal("missing encrypted outbound")
				}
				httpTransport := &http.Transport{
					DisableKeepAlives: true,
					TLSClientConfig:   &tls.Config{ServerName: "xhttp.test", RootCAs: pool, MinVersion: tls.VersionTLS13},
					DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
						return proxy.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr(address))
					},
				}
				t.Cleanup(httpTransport.CloseIdleConnections)
				httpClient := &http.Client{Transport: httpTransport, Timeout: 15 * time.Second}
				payload := bytes.Repeat([]byte("XHTTP VLESS Encryption Vision\n"), 8192)
				for range 2 {
					response, err := httpClient.Post(echo.URL, "application/octet-stream", bytes.NewReader(payload))
					if err != nil {
						t.Fatal(err)
					}
					received, err := io.ReadAll(response.Body)
					response.Body.Close()
					if err != nil || response.StatusCode != 200 || !bytes.Equal(payload, received) {
						t.Fatal("payload did not traverse XHTTP/VLESS Encryption/Vision", err)
					}
				}
			})
		}
	}
}
