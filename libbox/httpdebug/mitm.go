package httpdebug

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"
)

var errHelloInspected = errors.New("ClientHello inspected without a TLS response")

// SelectTLS reads only the ClientHello, using Go's TLS parser. All bytes are
// replayed, including on timeout, oversized/unsupported hellos and exclusions.
// The probe cannot write an alert or a ServerHello to the real client.
func (e *Engine) SelectTLS(ctx context.Context, conn net.Conn, metadata Metadata) (net.Conn, Metadata, bool) {
	if !e.config.HTTPS || !e.Candidate(metadata) {
		return conn, metadata, false
	}
	e.mu.Lock()
	if e.closed || len(e.connections)+e.preparing >= MaxConnections {
		e.skipped.Add(1)
		e.mu.Unlock()
		return conn, metadata, false
	}
	e.preparing++
	e.wg.Add(1)
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.preparing--; e.mu.Unlock(); e.wg.Done() }()
	stopEngine := context.AfterFunc(e.ctx, func() { conn.Close() })
	stopCaller := context.AfterFunc(ctx, func() { conn.Close() })
	defer stopEngine()
	defer stopCaller()
	probe := &helloProbe{Conn: conn}
	var name string
	var protos []string
	inspected := false
	server := tls.Server(probe, &tls.Config{GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
		name, protos, inspected = hello.ServerName, slices.Clone(hello.SupportedProtos), true
		return nil, errHelloInspected
	}})
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_ = server.HandshakeContext(ctx)
	_ = conn.SetReadDeadline(time.Time{})
	replay := &replayConn{Conn: conn, reader: io.MultiReader(bytes.NewReader(probe.data), conn)}
	if !inspected {
		e.skipped.Add(1)
		AppendCaptureLog("warn", "TLS passed through: ClientHello could not be inspected for "+metadata.Host)
		return replay, metadata, false
	}
	if name != "" {
		metadata.Host = normalizeHost(name)
	} else if net.ParseIP(metadata.Host) == nil {
		// Do not turn a DNS mapping or an outbound override into a claimed SNI.
		return replay, metadata, false
	}
	if len(protos) > 0 && !slices.Contains(protos, "h2") && !slices.Contains(protos, "http/1.1") {
		AppendCaptureLog("debug", "TLS passed through: non-HTTP ALPN for "+metadata.Host)
		return replay, metadata, false
	}
	return replay, metadata, e.Matches(metadata)
}

type helloProbe struct {
	net.Conn
	data []byte
}

func (p *helloProbe) Read(buffer []byte) (int, error) {
	remaining := MaxHeaderBytes - len(p.data)
	if remaining == 0 {
		return 0, errors.New("ClientHello exceeds inspection limit")
	}
	n, err := p.Conn.Read(buffer[:min(len(buffer), remaining)])
	p.data = append(p.data, buffer[:n]...)
	return n, err
}
func (p *helloProbe) Write(buffer []byte) (int, error) { return len(buffer), nil }
func (p *helloProbe) Close() error                     { return nil }

type replayConn struct {
	net.Conn
	reader io.Reader
}

func (c *replayConn) Read(buffer []byte) (int, error) { return c.reader.Read(buffer) }

// Match mitmproxy's upstream-first handshake order. A trusted local certificate
// is never presented before the original route's server chain/name is verified.
// The verified upstream connection is then consumed by the HTTP transport; it
// is not a disposable probe followed by an unrelated second connection.
func (e *Engine) openMITM(ctx context.Context, raw net.Conn, metadata Metadata, dial DialFunc, roots *x509.CertPool) (net.Conn, *http.Transport, Metadata, func(), error) {
	e.tlsOpened.Add(1)
	metadata.CaptureSource = "MITM"
	metadata.TLSAdapter = "Go crypto/tls"
	var upstream *tls.Conn
	var upstreamState tls.ConnectionState
	var offered []string
	var primeMu sync.Mutex
	closed := false
	cleanup := func() {
		primeMu.Lock()
		defer primeMu.Unlock()
		closed = true
		if upstream != nil {
			upstream.Close()
			upstream = nil
		}
	}
	handshakeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	phase := "Client TLS"
	client := tls.Server(raw, &tls.Config{
		MinVersion: tls.VersionTLS12, SessionTicketsDisabled: true,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			if hello.ServerName != "" && normalizeHost(hello.ServerName) != normalizeHost(metadata.Host) {
				return nil, errors.New("server name changed after MITM selection")
			}
			for _, proto := range hello.SupportedProtos {
				if proto == "h2" || proto == "http/1.1" {
					offered = append(offered, proto)
				}
			}
			if len(hello.SupportedProtos) > 0 && len(offered) == 0 {
				return nil, errors.New("non-HTTP TLS must be passed through")
			}
			phase = "Upstream TLS"
			var err error
			upstream, err = verifiedTLS(ctx, handshakeCtx, dial, metadata.Host, offered, roots)
			if err != nil {
				return nil, err
			}
			upstreamState = upstream.ConnectionState()
			phase = "Client TLS"
			leaf, err := e.ca.leaf(metadata.Host)
			if err != nil {
				return nil, err
			}
			protos := []string(nil)
			if upstreamState.NegotiatedProtocol != "" {
				protos = []string{upstreamState.NegotiatedProtocol}
			}
			return &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{*leaf},
				NextProtos: protos, SessionTicketsDisabled: true}, nil
		},
	})
	if err := client.HandshakeContext(handshakeCtx); err != nil {
		cleanup()
		e.tlsFailed.Add(1)
		failure := fmt.Errorf("%s for %s: %w", phase, metadata.Host, err)
		e.reportError(failure.Error())
		e.recordTLSFailure(metadata, failure)
		return nil, nil, metadata, nil, failure
	}
	clientState := client.ConnectionState()
	metadata.TLS = &TLSMetadata{ServerName: metadata.Host, ClientVersion: tlsVersion(clientState.Version),
		UpstreamVersion: tlsVersion(upstreamState.Version), ALPN: clientState.NegotiatedProtocol}
	if len(upstreamState.PeerCertificates) > 0 {
		fingerprint := sha256.Sum256(upstreamState.PeerCertificates[0].Raw)
		metadata.TLS.UpstreamCertificate = hex.EncodeToString(fingerprint[:])
	}
	e.plaintextConnections.Add(1)
	AppendCaptureLog("info", fmt.Sprintf("MITM connected: %s; ALPN=%s; client=%s; upstream=%s", metadata.Host,
		clientState.NegotiatedProtocol, metadata.TLS.ClientVersion, metadata.TLS.UpstreamVersion))
	transport := newTransport(dial)
	transport.ForceAttemptHTTP2 = true
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: metadata.Host}
	transport.DialTLSContext = func(callCtx context.Context, _, _ string) (net.Conn, error) {
		primeMu.Lock()
		if closed {
			primeMu.Unlock()
			return nil, net.ErrClosed
		}
		if upstream != nil {
			result := upstream
			upstream = nil
			primeMu.Unlock()
			return result, nil
		}
		primeMu.Unlock()
		deadlineCtx, stop := context.WithTimeout(callCtx, 10*time.Second)
		defer stop()
		// Each replacement connection verifies the chain and the same SNI again.
		return verifiedTLS(callCtx, deadlineCtx, dial, metadata.Host, offered, roots)
	}
	return client, transport, metadata, cleanup, nil
}

func verifiedTLS(lifetime, handshake context.Context, dial DialFunc, host string, protos []string, roots *x509.CertPool) (*tls.Conn, error) {
	// Bound both dialing and handshaking, but retain the route context after a
	// successful handshake. Cancelling a short-lived handshake context must not
	// tear down the verified connection while HTTP is still using it.
	connCtx, cancel := context.WithCancel(lifetime)
	stopDeadline := context.AfterFunc(handshake, cancel)
	defer stopDeadline()
	conn, err := dial(connCtx)
	if err != nil {
		cancel()
		return nil, err
	}
	client := tls.Client(&cancelConn{Conn: conn, cancel: cancel}, &tls.Config{MinVersion: tls.VersionTLS12,
		ServerName: normalizeHost(host), RootCAs: roots, NextProtos: protos})
	if err = client.HandshakeContext(handshake); err != nil {
		client.Close()
		return nil, err
	}
	if !stopDeadline() || handshake.Err() != nil {
		client.Close()
		return nil, handshake.Err()
	}
	return client, nil
}

type cancelConn struct {
	net.Conn
	cancel context.CancelFunc
}

func (c *cancelConn) Close() error { c.cancel(); return c.Conn.Close() }

func (e *Engine) recordTLSFailure(metadata Metadata, err error) {
	request := &http.Request{Method: "TLS", Proto: "TLS", Header: make(http.Header),
		URL: &url.URL{Scheme: "https", Host: net.JoinHostPort(metadata.Host, strconv.Itoa(int(metadata.Port)))}}
	id := e.store.begin(metadata, request, e.config.BodyLimit)
	e.store.domainOnly(id)
	e.store.addNote(id, "TLS handshake failed before any HTTP request was received")
	e.store.setError(id, err)
	e.store.finish(id)
}

func tlsVersion(version uint16) string {
	switch version {
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	}
	return fmt.Sprintf("TLS 0x%04x", version)
}
