//go:build with_quic

package httpdebug

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
)

const http3Included = true

// HandleQUIC takes ownership only after a read-only ClientHello inspection.
// Routing has already restored every sniffed datagram to conn and registered
// its original connection tracker and interruption handler.
func (e *Engine) HandleQUIC(ctx context.Context, conn net.PacketConn, metadata Metadata, dial PacketDialFunc, mapped RewriteDialFunc, roots *x509.CertPool, onClose func(error)) bool {
	metadata.Protocol = "tls"
	if !e.H3Enabled() || e.ca == nil || !e.mitm.matches(metadata) {
		return false
	}
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(ctx)
	socket := &http3Socket{PacketConn: conn, cancel: cancel}
	e.packetConnections[socket] = cancel
	e.wg.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.wg.Done()
		stopClose := context.AfterFunc(ctx, func() { _ = socket.Close() })
		err := e.serveQUIC(ctx, socket, metadata, dial, mapped, roots)
		stopClose()
		_ = socket.Close()
		e.mu.Lock()
		delete(e.packetConnections, socket)
		e.mu.Unlock()
		if onClose != nil {
			onClose(err)
		}
	}()
	return true
}

func debugQUICConfig() *quic.Config {
	// UDP framing and flow control retain quic-go's native settings. As with
	// TCP MITM, there is no additional fixed handshake, idle or stream quota;
	// the peer's QUIC idle timeout and service cancellation still apply.
	return &quic.Config{
		HandshakeIdleTimeout:    time.Duration(1<<62 - 1),
		MaxIdleTimeout:          time.Duration(1<<62 - 1),
		MaxIncomingStreams:      1 << 60,
		MaxIncomingUniStreams:   1 << 60,
		DisablePathMTUDiscovery: true,
	}
}

func debugHTTP3HeaderLimit() int {
	// HTTP/3 settings use QUIC varints. Preserve architecture-sized parser
	// capacity without advertising a value beyond the protocol's 62 bits.
	return int(min(uint64(^uint(0)>>1)-4096, uint64(1<<62-1)))
}

func (e *Engine) serveQUIC(ctx context.Context, socket *http3Socket, metadata Metadata, dial PacketDialFunc, mapped RewriteDialFunc, roots *x509.CertPool) error {
	e.tlsOpened.Add(1)
	metadata.CaptureSource, metadata.TLSAdapter = "MITM", "Go crypto/tls + quic-go"
	upstream := newHTTP3Upstream(ctx, dial, metadata.Host, roots, e.mitm.SkipServerCertVerify)
	defer upstream.Close()
	var upstreamState *tls.ConnectionState
	if e.httpProgram(metadata) == nil {
		// Pure interception verifies the real origin before responding to the
		// client's TLS handshake. Rules may instead answer an offline Mock.
		conn, err := upstream.prime(ctx)
		if err != nil {
			return e.quicHandshakeFailure(metadata, "Upstream QUIC TLS", err)
		}
		state := conn.ConnectionState().TLS
		upstreamState = &state
	}
	certificate, err := e.ca.leaf(metadata.Host)
	if err != nil {
		return e.quicHandshakeFailure(metadata, "Client QUIC TLS certificate", err)
	}
	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS13, NextProtos: []string{http3.NextProtoH3},
		Certificates: []tls.Certificate{*certificate}, SessionTicketsDisabled: true,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			if hello.ServerName != "" && normalizeHost(hello.ServerName) != normalizeHost(metadata.Host) ||
				hello.ServerName == "" && metadata.sniffedSNI {
				return nil, errors.New("server name changed after QUIC MITM selection")
			}
			return nil, nil
		},
	}
	acceptCtx, cancelAccept := context.WithCancel(ctx)
	defer cancelAccept()
	var accepted atomic.Bool
	var acceptFailure atomic.Pointer[error]
	transport := &quic.Transport{Conn: socket.PacketConn, DisableVersionNegotiationPackets: true,
		ConnContext: func(connCtx context.Context, _ *quic.ClientInfo) (context.Context, error) {
			context.AfterFunc(connCtx, func() {
				if !accepted.Load() {
					failure := context.Cause(connCtx)
					acceptFailure.Store(&failure)
					cancelAccept()
				}
			})
			return connCtx, nil
		},
	}
	if !socket.setTransport(transport) {
		return net.ErrClosed
	}
	defer socket.Close()
	listener, err := transport.Listen(tlsConfig, debugQUICConfig())
	if err != nil {
		return e.quicHandshakeFailure(metadata, "Client QUIC listener", err)
	}
	client, err := listener.Accept(acceptCtx)
	if err != nil {
		if ctx.Err() != nil || e.ctx.Err() != nil {
			return ctx.Err()
		}
		if failure := acceptFailure.Load(); failure != nil && *failure != nil {
			err = *failure
		}
		return e.quicHandshakeFailure(metadata, "Client QUIC TLS", err)
	}
	accepted.Store(true)
	if !socket.setConn(client) {
		_ = socket.Close()
		_ = client.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeNoError), "")
		return net.ErrClosed
	}
	context.AfterFunc(client.Context(), func() { _ = socket.Close() })
	state := client.ConnectionState().TLS
	metadata.TLS = &TLSMetadata{ServerName: metadata.Host, ClientVersion: tlsVersion(state.Version), ALPN: state.NegotiatedProtocol}
	if upstreamState != nil {
		applyQUICUpstreamTLS(metadata.TLS, *upstreamState)
	}
	e.plaintextConnections.Add(1)
	writeLog("info", fmt.Sprintf("MITM connected: %s; ALPN=%s; client=%s; upstream=%s", metadata.Host,
		state.NegotiatedProtocol, metadata.TLS.ClientVersion, metadata.TLS.UpstreamVersion))
	var mappedTransport *http.Transport
	if mapped != nil {
		mappedTransport = newRewriteTransport(ctx, mapped, roots)
		mappedTransport.ForceAttemptHTTP2 = e.mitm.H2
		mappedTransport.TLSClientConfig.NextProtos = e.mitm.httpProtocols()
		mappedTransport.TLSClientConfig.InsecureSkipVerify = e.mitm.SkipServerCertVerify
		defer mappedTransport.CloseIdleConnections()
	}
	target := &url.URL{Scheme: "https", Host: net.JoinHostPort(metadata.Host, strconv.Itoa(int(metadata.Port)))}
	process := e.handler(metadata, target, upstream, mappedTransport)
	var received atomic.Bool
	server := &http3.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			received.Store(true)
			process.ServeHTTP(w, request)
		}),
		MaxHeaderBytes: debugHTTP3HeaderLimit(),
		ConnContext: func(connCtx context.Context, _ *quic.Conn) context.Context {
			// Keep the QUIC request cancellation and HTTP/3 context values, and
			// also cancel every stream when the intercepted route is cancelled.
			requestCtx, cancel := context.WithCancel(connCtx)
			stop := context.AfterFunc(ctx, cancel)
			context.AfterFunc(requestCtx, func() { stop(); cancel() })
			return requestCtx
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	defer func() {
		_ = socket.Close()
		_ = server.Close()
	}()
	err = server.ServeQUICConn(client)
	if !received.Load() && ctx.Err() == nil && e.ctx.Err() == nil {
		e.tlsNoRequest.Add(1)
		reason := "QUIC TLS handshake completed but no HTTP/3 request was received; possible certificate pinning or client cancellation"
		writeLog("warn", reason+"; connection="+metadata.ConnectionID+" host="+metadata.Host)
		e.recordTLSDiagnostic(metadata, reason, nil)
	}
	if quicDebugClosed(err) || ctx.Err() != nil || e.ctx.Err() != nil {
		return nil
	}
	return err
}

func applyQUICUpstreamTLS(metadata *TLSMetadata, state tls.ConnectionState) {
	metadata.UpstreamVersion = tlsVersion(state.Version)
	protocol := state.NegotiatedProtocol
	metadata.UpstreamALPN, metadata.UpstreamServerName = &protocol, state.ServerName
	if len(state.PeerCertificates) > 0 {
		fingerprint := sha256.Sum256(state.PeerCertificates[0].Raw)
		metadata.UpstreamCertificate = hex.EncodeToString(fingerprint[:])
	}
}

func (e *Engine) quicHandshakeFailure(metadata Metadata, phase string, err error) error {
	if e.ctx.Err() != nil || errors.Is(err, context.Canceled) {
		return err
	}
	e.tlsFailed.Add(1)
	failure := fmt.Errorf("%s for %s: %w", phase, metadata.Host, err)
	e.reportError(failure.Error())
	e.recordTLSFailure(metadata, failure)
	return failure
}

// HTTP/3 uses the existing HTTP handler and ReverseProxy, including streaming
// bodies, trailers, HTTP rewrites, scripts and response.TLS capture. Every UDP
// transport is explicitly owned here; quic-go does not close supplied sockets.
type http3Upstream struct {
	ctx       context.Context
	cancel    context.CancelFunc
	dial      PacketDialFunc
	host      string
	transport *http3.Transport
	mu        sync.Mutex
	closed    bool
	primed    *quic.Conn
	sockets   []*http3Socket
	wg        sync.WaitGroup
	once      sync.Once
}

func newHTTP3Upstream(lifetime context.Context, dial PacketDialFunc, host string, roots *x509.CertPool, skipVerify bool) *http3Upstream {
	ctx, cancel := context.WithCancel(lifetime)
	result := &http3Upstream{ctx: ctx, cancel: cancel, dial: dial, host: host}
	result.transport = &http3.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, ServerName: normalizeHost(host), RootCAs: roots,
			NextProtos: []string{http3.NextProtoH3}, InsecureSkipVerify: skipVerify},
		QUICConfig: debugQUICConfig(), DisableCompression: true,
		MaxResponseHeaderBytes: debugHTTP3HeaderLimit(),
		Logger:                 slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	result.transport.Dial = func(callCtx context.Context, _ string, tlsConfig *tls.Config, config *quic.Config) (*quic.Conn, error) {
		result.mu.Lock()
		prime := result.primed
		result.primed = nil
		closed := result.closed
		result.mu.Unlock()
		if closed {
			return nil, net.ErrClosed
		}
		if prime != nil && prime.Context().Err() == nil {
			return prime, nil
		}
		return result.open(callCtx, tlsConfig, config)
	}
	return result
}

func (t *http3Upstream) prime(ctx context.Context) (*quic.Conn, error) {
	conn, err := t.open(ctx, t.transport.TLSClientConfig.Clone(), t.transport.QUICConfig.Clone())
	if err != nil {
		return nil, err
	}
	t.mu.Lock()
	if t.closed || t.ctx.Err() != nil {
		err := t.ctx.Err()
		t.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, net.ErrClosed
	}
	t.primed = conn
	t.mu.Unlock()
	return conn, nil
}

func (t *http3Upstream) open(handshake context.Context, tlsConfig *tls.Config, config *quic.Config) (*quic.Conn, error) {
	t.mu.Lock()
	if t.closed || t.ctx.Err() != nil {
		err := t.ctx.Err()
		t.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, net.ErrClosed
	}
	t.wg.Add(1)
	t.mu.Unlock()
	defer t.wg.Done()
	if t.dial == nil {
		return nil, errors.New("HTTP/3 requires a sing-box UDP route dialer")
	}
	// Keep route context values, but let the owned socket notify the peer before
	// service cancellation tears down the selected UDP handler. Cancellation
	// remains immediate while the route dial and QUIC handshake are pending.
	connCtx, cancel := context.WithCancel(context.WithoutCancel(t.ctx))
	stopRouteDial := context.AfterFunc(t.ctx, cancel)
	defer stopRouteDial()
	stopHandshake := context.AfterFunc(handshake, cancel)
	defer stopHandshake()
	raw, address, err := t.dial(connCtx)
	if err != nil || raw == nil || address == nil || connCtx.Err() != nil {
		stopHandshake()
		cancel()
		if raw != nil {
			_ = raw.Close()
		}
		if err == nil {
			err = connCtx.Err()
			if err == nil {
				err = errors.New("UDP route returned no packet connection or peer")
			}
		}
		return nil, err
	}
	socket := &http3Socket{PacketConn: raw, transport: &quic.Transport{Conn: raw}, cancel: cancel}
	closeSocket := func() { _ = socket.Close() }
	stopClose := context.AfterFunc(connCtx, closeSocket)
	defer stopClose()
	stopRouteDial()
	t.mu.Lock()
	if t.closed || t.ctx.Err() != nil || connCtx.Err() != nil {
		err := t.ctx.Err()
		if err == nil {
			err = connCtx.Err()
		}
		t.mu.Unlock()
		_ = socket.Close()
		if err != nil {
			return nil, err
		}
		return nil, net.ErrClosed
	}
	t.sockets = append(t.sockets, socket)
	t.mu.Unlock()
	context.AfterFunc(t.ctx, closeSocket)
	conn, err := socket.transport.Dial(connCtx, address, tlsConfig, config)
	if err != nil {
		_ = socket.Close()
		return nil, err
	}
	if !socket.setConn(conn) {
		_ = socket.Close()
		_ = conn.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeNoError), "")
		return nil, net.ErrClosed
	}
	if !stopHandshake() || handshake.Err() != nil || connCtx.Err() != nil {
		_ = socket.Close()
		if err = handshake.Err(); err == nil {
			err = connCtx.Err()
		}
		return nil, err
	}
	// Use the QUIC lifetime once the handshake succeeded. Cancelling the HTTP
	// request that opened a pooled connection must not terminate other streams.
	context.AfterFunc(conn.Context(), closeSocket)
	return conn, nil
}

func (t *http3Upstream) RoundTrip(request *http.Request) (*http.Response, error) {
	return t.transport.RoundTrip(request)
}

func (t *http3Upstream) Close() {
	t.once.Do(func() {
		t.mu.Lock()
		t.closed = true
		sockets := append([]*http3Socket(nil), t.sockets...)
		t.mu.Unlock()
		t.cancel()
		for _, socket := range sockets {
			_ = socket.Close()
		}
		_ = t.transport.Close()
		t.wg.Wait()
	})
}

type http3Socket struct {
	net.PacketConn
	transport *quic.Transport
	cancel    context.CancelFunc
	mu        sync.Mutex
	conn      *quic.Conn
	closed    bool
	once      sync.Once
}

func (s *http3Socket) setTransport(transport *quic.Transport) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.transport = transport
	return true
}

func (s *http3Socket) setConn(conn *quic.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conn = conn
	return true
}

// This bounds shutdown only; it never limits an active handshake or stream.
const http3CloseGrace = time.Second

func (s *http3Socket) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		conn := s.conn
		transport := s.transport
		s.mu.Unlock()
		// quic-go drains its send queue before writing CONNECTION_CLOSE. Allow
		// that write while the selected route is still alive, but interrupt a
		// stalled packet pipe even if a custom socket rejects write deadlines.
		deadline := time.Now().Add(http3CloseGrace)
		_ = s.PacketConn.SetWriteDeadline(deadline)
		teardown := sync.OnceFunc(func() {
			// A routed socket's Close joins its onClose callback. Cancel the
			// handler before joining, including one still dialing its outbound.
			s.cancel()
			_ = s.PacketConn.Close()
		})
		forceClose := time.AfterFunc(time.Until(deadline), teardown)
		if conn != nil {
			_ = conn.CloseWithError(quic.ApplicationErrorCode(http3.ErrCodeNoError), "")
			if routed, loaded := s.PacketConn.(interface{ CloseGracefully() error }); loaded {
				// An in-memory route write hands off to the selected handler.
				// Drain that final physical UDP write before cancelling its route.
				_ = routed.CloseGracefully()
			}
		}
		forceClose.Stop()
		teardown()
		if transport != nil {
			_ = transport.Close()
		}
	})
	return nil
}

func quicDebugClosed(err error) bool {
	if err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed) || errors.Is(err, context.Canceled) ||
		errors.Is(err, quic.ErrServerClosed) || errors.Is(err, quic.ErrTransportClosed) {
		return true
	}
	var app *quic.ApplicationError
	if errors.As(err, &app) {
		return app.ErrorCode == 0 || http3.ErrCode(app.ErrorCode) == http3.ErrCodeNoError
	}
	var h3 *http3.Error
	return errors.As(err, &h3) && (h3.ErrorCode == http3.ErrCodeNoError || h3.ErrorCode == http3.ErrCodeRequestCanceled)
}
