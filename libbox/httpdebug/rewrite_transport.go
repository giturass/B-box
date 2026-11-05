package httpdebug

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// RewriteDialFunc routes a new host:port through the same sing-box instance.
// There is deliberately no net.Dial fallback for a missing or failed route.
type RewriteDialFunc func(context.Context, string) (net.Conn, error)

func newRewriteTransport(lifetime context.Context, dial RewriteDialFunc, roots *x509.CertPool) *http.Transport {
	transport := newTransport(nil)
	transport.DialContext = func(ctx context.Context, _, address string) (net.Conn, error) {
		return bindDialLifetime(lifetime, func(ctx context.Context) (net.Conn, error) { return dial(ctx, address) })(ctx)
	}
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ForceAttemptHTTP2 = true
	return transport
}

// Profile rules may answer offline or map the URL to another origin. Complete
// the client handshake without opening the original server. Real upstream TLS
// connections follow the profile's MITM verification and HTTP/2 options.
func (e *Engine) openRewriteMITM(ctx context.Context, raw net.Conn, metadata Metadata, dial DialFunc, roots *x509.CertPool) (net.Conn, *http.Transport, Metadata, func(), error) {
	e.tlsOpened.Add(1)
	metadata.CaptureSource, metadata.TLSAdapter = "MITM", "Go crypto/tls"
	client := tls.Server(raw, &tls.Config{MinVersion: tls.VersionTLS12, SessionTicketsDisabled: true,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			if hello.ServerName != "" && normalizeHost(hello.ServerName) != normalizeHost(metadata.Host) {
				return nil, errors.New("server name changed after MITM selection")
			}
			certificate, err := e.ca.leaf(metadata.Host)
			if err != nil {
				return nil, err
			}
			return &tls.Config{MinVersion: tls.VersionTLS12, SessionTicketsDisabled: true,
				Certificates: []tls.Certificate{*certificate}, NextProtos: e.mitm.httpProtocols()}, nil
		}})
	handshake, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := client.HandshakeContext(handshake); err != nil {
		e.tlsFailed.Add(1)
		failure := fmt.Errorf("Client TLS for %s: %w", metadata.Host, err)
		e.reportError(failure.Error())
		e.recordTLSFailure(metadata, failure)
		return nil, nil, metadata, nil, failure
	}
	state := client.ConnectionState()
	metadata.TLS = &TLSMetadata{ServerName: metadata.Host, ClientVersion: tlsVersion(state.Version), ALPN: state.NegotiatedProtocol}
	e.plaintextConnections.Add(1)
	transport := newTransport(dial)
	transport.ForceAttemptHTTP2 = e.mitm.H2
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: metadata.Host,
		NextProtos: e.mitm.httpProtocols(), InsecureSkipVerify: e.mitm.SkipServerCertVerify}
	transport.DialTLSContext = func(callCtx context.Context, _, _ string) (net.Conn, error) {
		deadline, cancel := context.WithTimeout(callCtx, 10*time.Second)
		defer cancel()
		conn, err := dialUpstreamTLS(callCtx, deadline, dial, metadata.Host, e.mitm.httpProtocols(), roots, e.mitm.SkipServerCertVerify)
		if err != nil {
			e.tlsFailed.Add(1)
			e.reportError(fmt.Sprintf("Rewrite upstream TLS for %s: %v", metadata.Host, err))
		}
		return conn, err
	}
	return client, transport, metadata, func() {}, nil
}
