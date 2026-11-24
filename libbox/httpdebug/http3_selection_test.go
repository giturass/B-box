//go:build with_quic

package httpdebug

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
)

func http3TestHello(t *testing.T, name string, protocols []string) []byte {
	t.Helper()
	client := tls.QUICClient(&tls.QUICConfig{TLSConfig: &tls.Config{
		MinVersion: tls.VersionTLS13, ServerName: name, NextProtos: protocols, InsecureSkipVerify: true,
	}})
	defer client.Close()
	client.SetTransportParameters(nil)
	if err := client.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var hello []byte
	for {
		event := client.NextEvent()
		if event.Kind == tls.QUICNoEvent {
			break
		}
		if event.Kind == tls.QUICErrorEvent {
			t.Fatal(event.Err)
		}
		if event.Kind == tls.QUICWriteData && event.Level == tls.QUICEncryptionLevelInitial {
			hello = append(hello, event.Data...)
		}
	}
	if len(hello) == 0 {
		t.Fatal("QUIC TLS client did not produce a ClientHello")
	}
	// Match the native Initial sniffer's envelope without putting the QUIC
	// handshake through TCP TLS record framing.
	record := make([]byte, 5, 5+len(hello))
	record[0], record[1], record[2] = 0x16, 0x03, 0x03
	binary.BigEndian.PutUint16(record[3:5], uint16(len(hello)))
	return append(record, hello...)
}

func TestHTTP3ReadOnlySelectionUsesActualHelloAndScope(t *testing.T) {
	e, _, _ := mitmEngine(t, Config{})
	e.mitm = mitmConfig(t, `{"enabled":true,"h3":true,"hosts":["origin.test","198.51.100.9"],"ports":[443],"packages":["com.test"]}`)
	largeProtocols := make([]string, 96, 97)
	for index := range largeProtocols {
		largeProtocols[index] = fmt.Sprintf("%03d-", index) + strings.Repeat("p", 236)
	}
	largeProtocols = append(largeProtocols, "h3")
	for _, test := range []struct {
		name, sni, routeHost, pkg string
		port                      uint16
		protocols                 []string
		want                      bool
	}{
		{"large-hello", "origin.test", "routing-name.test", "com.test", 443, largeProtocols, true},
		{"unmatched-SNI", "outside.test", "origin.test", "com.test", 443, []string{"h3"}, false},
		{"non-HTTP-ALPN", "origin.test", "origin.test", "com.test", 443, []string{"raw-quic"}, false},
		{"unmatched-port", "origin.test", "origin.test", "com.test", 8443, []string{"h3"}, false},
		{"unmatched-owner", "origin.test", "origin.test", "com.other", 443, []string{"h3"}, false},
		{"no-SNI-name", "", "origin.test", "com.test", 443, []string{"h3"}, false},
		{"no-SNI-known-IP", "", "198.51.100.9", "com.test", 443, []string{"h3"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			hello := http3TestHello(t, test.sni, test.protocols)
			if test.name == "large-hello" && len(hello) <= 16*1024 {
				t.Fatal("large QUIC ClientHello did not exceed the TCP TLS record limit")
			}
			metadata, matched := e.SelectQUIC(Metadata{Host: test.routeHost, Port: test.port, Protocol: "quic",
				Packages: []string{test.pkg}, ConnectionID: "existing-route", RawTCP: true}, hello)
			if matched != test.want {
				t.Fatalf("HTTP/3 offer selection = %v, want %v", matched, test.want)
			}
			if matched {
				wantHost := test.sni
				if wantHost == "" {
					wantHost = test.routeHost
				}
				if metadata.Host != wantHost || metadata.Protocol != "tls" || metadata.RawTCP || metadata.sniffedSNI != (test.sni != "") ||
					metadata.ConnectionID != "existing-route" || metadata.Port != test.port {
					t.Fatalf("HTTP/3 selection changed routing identity or fabricated SNI: %+v", metadata)
				}
			}
		})
	}
	if _, matched := e.SelectQUIC(Metadata{Host: "origin.test", Port: 443, Packages: []string{"com.test"}}, []byte{0x16, 0x03}); matched {
		t.Fatal("truncated QUIC ClientHello was selected")
	}
	e.mitm.H3 = false
	if _, matched := e.SelectQUIC(Metadata{Host: "origin.test", Port: 443, Packages: []string{"com.test"}}, http3TestHello(t, "origin.test", []string{"h3"})); matched {
		t.Fatal("HTTP/3 selection ignored the profile's h3 opt-out")
	}
}
