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
// replayed, including on timeout, oversized/unsupported hellos and unmatched names.
// The probe cannot write an alert or a ServerHello to the real client.
func (e *Engine) SelectTLS(ctx context.Context, conn net.Conn, metadata Metadata) (net.Conn, Metadata, bool) {
	if e.ca == nil || !e.mitm.candidate(metadata) {
		metadata.ProcessingReason = "TLS passed through: MITM disabled or outside configured port/application scope"
		return conn, metadata, false
	}
	selection := e.trackTLS(ctx, conn)
	if selection == nil {
		metadata.ProcessingReason = "TLS processing skipped: service stopped"
		e.connectionSkipped(metadata, metadata.ProcessingReason)
		return conn, metadata, false
	}
	defer e.wg.Done()
	selected := false
	defer func() {
		if !selected {
			selection.release(false)
		}
	}()
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
		metadata.ProcessingReason = "TLS passed through: ClientHello could not be inspected"
		writeLog("warn", metadata.ProcessingReason+"; connection="+metadata.ConnectionID+" host="+metadata.Host)
		return replay, metadata, false
	}
	if name != "" {
		metadata.Host = normalizeHost(name)
	} else if net.ParseIP(metadata.Host) == nil {
		// Do not turn a DNS mapping or an outbound override into a claimed SNI.
		metadata.ProcessingReason = "TLS passed through: no SNI or known IP target"
		return replay, metadata, false
	}
	metadata.sniffedSNI = name != ""
	// Any non-HTTP offer requires upstream-first negotiation, even when HTTP
	// is also offered. openMITM chooses the final mode from the negotiated ALPN.
	metadata.RawTCP = e.mitm.TCP && (len(protos) == 0 || slices.ContainsFunc(protos, func(proto string) bool {
		return proto != "h2" && proto != "http/1.1"
	}))
	if len(protos) > 0 && !metadata.RawTCP && !slices.Contains(protos, "h2") && !slices.Contains(protos, "http/1.1") {
		metadata.ProcessingReason = "TLS passed through: non-HTTP ALPN and TCP decryption disabled"
		writeLog("debug", metadata.ProcessingReason+"; connection="+metadata.ConnectionID+" host="+metadata.Host)
		return replay, metadata, false
	}
	selected = e.mitm.matches(metadata)
	if !selected {
		metadata.ProcessingReason = "TLS passed through: SNI is outside the configured MITM scope"
		return replay, metadata, false
	}
	return &selectedTLSConn{Conn: replay, selection: selection}, metadata, true
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

// Keep the upstream-first handshake order, applying the profile's certificate
// verification choice. Reuse that same connection for HTTP or raw TLS traffic.
func (e *Engine) openMITM(ctx context.Context, raw net.Conn, metadata Metadata, dial DialFunc, roots *x509.CertPool) (net.Conn, *http.Transport, Metadata, func(), error) {
	if !metadata.RawTCP && e.httpProgram(metadata) != nil {
		return e.openRewriteMITM(ctx, raw, metadata, dial, roots)
	}
	e.tlsOpened.Add(1)
	metadata.CaptureSource = "MITM"
	metadata.TLSAdapter = "Go crypto/tls"
	var upstream net.Conn
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
				if proto == "http/1.1" || proto == "h2" && e.mitm.H2 ||
					proto != "h2" && proto != "http/1.1" && e.mitm.TCP {
					offered = append(offered, proto)
				}
			}
			if len(hello.SupportedProtos) > 0 && len(offered) == 0 {
				if !slices.Contains(hello.SupportedProtos, "h2") {
					return nil, errors.New("non-HTTP TLS must be passed through")
				}
				offered = []string{"http/1.1"}
			}
			if !metadata.RawTCP {
				// A known HTTP connection can use either HTTP version upstream,
				// independently of the client's offer. Unknown TCP stays unchanged.
				offered = e.mitm.httpProtocols()
			}
			phase = "Upstream TLS"
			remote, err := dialUpstreamTLS(ctx, handshakeCtx, dial, metadata.Host, offered, roots, e.mitm.SkipServerCertVerify)
			if err != nil {
				return nil, err
			}
			upstream = remote
			upstreamState = remote.ConnectionState()
			phase = "Client TLS"
			leaf, err := e.ca.leaf(metadata.Host)
			if err != nil {
				return nil, err
			}
			// HTTP versions are independent on the two TLS legs. A client may
			// use h2 while the origin uses HTTP/1.1, as in Surge's HTTP MITM.
			protos := e.mitm.httpProtocols()
			negotiated := upstreamState.NegotiatedProtocol
			if negotiated != "" && negotiated != "h2" && negotiated != "http/1.1" {
				protos = []string{negotiated}
			} else if negotiated == "" && e.mitm.TCP && (len(hello.SupportedProtos) == 0 ||
				slices.ContainsFunc(hello.SupportedProtos, func(p string) bool { return p != "h2" && p != "http/1.1" })) {
				protos = nil
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
	metadata.RawTCP = e.mitm != nil && e.mitm.TCP && clientState.NegotiatedProtocol != "h2" && clientState.NegotiatedProtocol != "http/1.1"
	metadata.TLS = &TLSMetadata{ServerName: metadata.Host, ClientVersion: tlsVersion(clientState.Version),
		UpstreamVersion: tlsVersion(upstreamState.Version), ALPN: clientState.NegotiatedProtocol, UpstreamALPN: upstreamState.NegotiatedProtocol}
	if len(upstreamState.PeerCertificates) > 0 {
		fingerprint := sha256.Sum256(upstreamState.PeerCertificates[0].Raw)
		metadata.TLS.UpstreamCertificate = hex.EncodeToString(fingerprint[:])
	}
	e.plaintextConnections.Add(1)
	var downstream net.Conn = client
	if metadata.RawTCP && clientState.NegotiatedProtocol == "" {
		var http1 bool
		var err error
		downstream, upstream, http1, err = inspectTLSProtocol(ctx, client, upstream.(*tls.Conn))
		if err != nil {
			cleanup()
			return nil, nil, metadata, nil, err
		}
		metadata.RawTCP = !http1
	}
	writeLog("info", fmt.Sprintf("MITM connected: %s; ALPN=%s; client=%s; upstream=%s", metadata.Host,
		clientState.NegotiatedProtocol, metadata.TLS.ClientVersion, metadata.TLS.UpstreamVersion))
	transport := newTransport(dial)
	transport.ForceAttemptHTTP2 = e.mitm.H2
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: metadata.Host,
		NextProtos: e.mitm.httpProtocols(), InsecureSkipVerify: e.mitm.SkipServerCertVerify}
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
		return dialUpstreamTLS(callCtx, deadlineCtx, dial, metadata.Host, offered, roots, e.mitm.SkipServerCertVerify)
	}
	return downstream, transport, metadata, cleanup, nil
}

func dialUpstreamTLS(lifetime, handshake context.Context, dial DialFunc, host string, protos []string, roots *x509.CertPool, skipVerify bool) (*tls.Conn, error) {
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
		ServerName: normalizeHost(host), RootCAs: roots, NextProtos: protos, InsecureSkipVerify: skipVerify})
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
	e.recordTLSDiagnostic(metadata, "TLS handshake failed before any HTTP request was received", err)
}

func (e *Engine) recordTLSDiagnostic(metadata Metadata, reason string, err error) {
	request := &http.Request{Method: "TLS", Proto: "TLS", Header: make(http.Header),
		URL: &url.URL{Scheme: "https", Host: net.JoinHostPort(metadata.Host, strconv.Itoa(int(metadata.Port)))}}
	id := e.beginRecording(metadata, request)
	e.store.domainOnly(id)
	e.store.addNote(id, reason)
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
