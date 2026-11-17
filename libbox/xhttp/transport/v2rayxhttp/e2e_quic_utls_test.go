//go:build with_quic && with_utls

package v2rayxhttp

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"net"
	"testing"

	boxtls "github.com/sagernet/sing-box/common/tls"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/logger"
	M "github.com/sagernet/sing/common/metadata"
)

// 订阅常见写法: alpn=h3 + fp=chrome。QUIC 握手无法套用 uTLS 指纹，应退回标准库
// TLS 且保持证书校验 (这里不开 insecure，靠 certificate 信任自签证书)。

func startTestH3Server(t *testing.T) M.Socksaddr {
	t.Helper()
	server, err := NewServer(context.Background(), logger.NOP(), &option.V2RayXHTTPOptions{Path: "/xhttp"}, newTestTLSServer(t, "h3"), echoHandler{})
	if err != nil {
		t.Fatal(err)
	}
	packetConn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.ServePacket(packetConn)
	t.Cleanup(func() {
		_ = server.Close()
		_ = packetConn.Close()
	})
	return M.SocksaddrFromNet(packetConn.LocalAddr())
}

func utlsH3Options(t *testing.T, serverName string) option.OutboundTLSOptions {
	certPEM, _ := testCertificate(t)
	return option.OutboundTLSOptions{
		Enabled:     true,
		ServerName:  serverName,
		ALPN:        []string{"h3"},
		Certificate: []string{certPEM},
		UTLS:        &option.OutboundUTLSOptions{Enabled: true, Fingerprint: "chrome"},
	}
}

func newUTLSH3Client(t *testing.T, serverName string) boxtls.Config {
	t.Helper()
	config, err := boxtls.NewClient(context.Background(), logger.NOP(), serverName, utlsH3Options(t, serverName))
	if err != nil {
		t.Fatal(err)
	}
	if _, isUTLS := config.(*boxtls.UTLSClientConfig); !isUTLS {
		t.Fatalf("expected uTLS config, got %T", config)
	}
	return config
}

func TestE2E_HTTP3_UTLS(t *testing.T) {
	serverAddr := startTestH3Server(t)
	for _, mode := range []string{"packet-up", "stream-up", "stream-one"} {
		t.Run(mode, func(t *testing.T) {
			client := newTestClient(t, smallPosts(option.V2RayXHTTPOptions{Path: "/xhttp", Mode: mode}), serverAddr, newUTLSH3Client(t, "example.com"))
			checkEcho(t, client, 4, 512*1024)
		})
	}
}

func TestE2E_HTTP3_UTLS_RejectsWrongCertificate(t *testing.T) {
	serverAddr := startTestH3Server(t)
	client := newTestClient(t, smallPosts(option.V2RayXHTTPOptions{Path: "/xhttp", Mode: "packet-up"}), serverAddr, newUTLSH3Client(t, "wrong.invalid"))
	if err := echoOnce(client, 1024); err == nil {
		t.Fatal("expected certificate verification failure")
	}
}

// download_settings 自带的 TLS (alpn h3 + fingerprint) 在传输层内部构造，同样要能走 HTTP/3。
func TestE2E_DownloadSettingsHTTP3_UTLS(t *testing.T) {
	serverAddr := startTestH3Server(t)
	downloadTLS := utlsH3Options(t, "example.com")
	client := newTestClient(t, smallPosts(option.V2RayXHTTPOptions{
		Path: "/xhttp",
		Mode: "packet-up",
		DownloadSettings: &option.V2RayXHTTPDownloadOptions{
			ServerOptions:               option.ServerOptions{Server: serverAddr.AddrString(), ServerPort: serverAddr.Port},
			OutboundTLSOptionsContainer: option.OutboundTLSOptionsContainer{TLS: &downloadTLS},
			V2RayXHTTPOptions:           option.V2RayXHTTPOptions{Path: "/xhttp"},
		},
	}), serverAddr, newUTLSH3Client(t, "example.com"))
	checkEcho(t, client, 3, 256*1024)
}

// Pin authentication must survive conversion to standard TLS with or without
// SNI. The self-signed certificate is deliberately absent from the trust pool.
func TestE2E_HTTP3_UTLS_CertificatePins(t *testing.T) {
	serverAddr := startTestH3Server(t)
	certPEM, _ := testCertificate(t)
	block, _ := pem.Decode([]byte(certPEM))
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, pinKind := range []string{"certificate", "public-key"} {
		for _, disableSNI := range []bool{false, true} {
			for _, wrongPin := range []bool{false, true} {
				name := pinKind
				if disableSNI {
					name += "/disable-sni"
				}
				if wrongPin {
					name += "/wrong-pin"
				}
				t.Run(name, func(t *testing.T) {
					options := utlsH3Options(t, "example.com")
					options.Certificate = nil
					options.DisableSNI = disableSNI
					pin := sha256.Sum256(certificate.Raw)
					if pinKind == "public-key" {
						pin = sha256.Sum256(certificate.RawSubjectPublicKeyInfo)
					}
					if wrongPin {
						pin[0] ^= 1
					}
					if pinKind == "public-key" {
						options.CertificatePublicKeySHA256 = [][]byte{pin[:]}
					} else {
						options.CertificateSHA256 = [][]byte{pin[:]}
					}
					config, err := boxtls.NewClient(context.Background(), logger.NOP(), "example.com", options)
					if err != nil {
						t.Fatal(err)
					}
					client := newTestClient(t, smallPosts(option.V2RayXHTTPOptions{Path: "/xhttp", Mode: "packet-up"}), serverAddr, config)
					if wrongPin {
						if err = echoOnce(client, 1024); err == nil {
							t.Fatal("expected certificate pin verification failure")
						}
					} else {
						checkEcho(t, client, 1, 64*1024)
					}
				})
			}
		}
	}
}
