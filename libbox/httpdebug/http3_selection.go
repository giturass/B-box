package httpdebug

import (
	"context"
	"crypto/tls"
	"net"
	"slices"
)

// PacketDialFunc opens a packet connection through the already selected route.
// The peer is a routing identity, and must never trigger a second DNS lookup.
type PacketDialFunc func(context.Context) (net.PacketConn, net.Addr, error)

// H3Enabled distinguishes a requested HTTP/3 MITM from builds that omit QUIC.
// Existing profiles retain their opt-in QUIC blocking unless h3 is enabled.
func (e *Engine) H3Enabled() bool {
	return e != nil && http3Included && e.mitm.active() && e.mitm.H3
}

// SelectQUIC receives the reassembled ClientHello from the core's read-only
// Initial-packet sniffer. Inspect ALPN with Go's TLS parser before taking over:
// a QUIC connection to an allowed hostname need not carry HTTP/3.
func (e *Engine) SelectQUIC(metadata Metadata, clientHello []byte) (Metadata, bool) {
	metadata.Protocol = "tls"
	if !e.H3Enabled() || e.ca == nil || !e.mitm.candidate(metadata) || len(clientHello) < 5 || clientHello[0] != 0x16 {
		return metadata, false
	}
	var name string
	var protocols []string
	inspected := false
	server := tls.QUICServer(&tls.QUICConfig{TLSConfig: &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			name, protocols, inspected = hello.ServerName, slices.Clone(hello.SupportedProtos), true
			return nil, errHelloInspected
		},
	}})
	defer server.Close()
	server.SetTransportParameters(nil)
	if err := server.Start(context.Background()); err != nil {
		return metadata, false
	}
	// The native sniffer supplies a synthetic TLS record header. QUIC carries
	// handshake bytes directly, so avoid the smaller TCP TLS record-size limit.
	// The inspection callback ends the handshake before any transport is needed.
	_ = server.HandleData(tls.QUICEncryptionLevelInitial, clientHello[5:])
	if !inspected || !slices.Contains(protocols, "h3") {
		return metadata, false
	}
	if name != "" {
		metadata.Host = normalizeHost(name)
	} else if net.ParseIP(metadata.Host) == nil {
		return metadata, false
	}
	metadata.sniffedSNI = name != ""
	metadata.RawTCP = false
	return metadata, e.mitm.matches(metadata)
}
