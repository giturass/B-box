package httpdebug

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
)

type DialFunc func(context.Context) (net.Conn, error)

type Engine struct {
	config      Config
	ca          *authority
	store       *Store
	mu          sync.Mutex
	wg          sync.WaitGroup
	closed      bool
	connections map[net.Conn]context.CancelFunc
	skipped     atomic.Int64
	blockedQUIC atomic.Int64
}

func newEngine(config Config, ca *authority, store *Store) *Engine {
	return &Engine{config: config, ca: ca, store: store, connections: make(map[net.Conn]context.CancelFunc)}
}

func (e *Engine) Candidate(metadata Metadata) bool { return e.config.candidate(metadata) }

func (e *Engine) Matches(metadata Metadata) bool { return e.config.matches(metadata) }

func (e *Engine) WantsQUIC(metadata Metadata) bool {
	metadata.Protocol = "tls"
	return e.config.BlockQUIC && e.config.HTTPS && e.config.candidate(metadata)
}

// QUIC is rejected only when explicitly requested and its recovered hostname
// matches the HTTPS allowlist. An unknown SNI never causes blanket UDP blocking.
func (e *Engine) RejectQUIC(metadata Metadata) bool {
	metadata.Protocol = "tls"
	if metadata.Host == "" || !e.WantsQUIC(metadata) || !e.config.matches(metadata) {
		return false
	}
	e.blockedQUIC.Add(1)
	return true
}

func (e *Engine) Handle(ctx context.Context, conn net.Conn, metadata Metadata, dial DialFunc, roots *x509.CertPool, onClose func(error)) bool {
	if !e.Matches(metadata) {
		return false
	}
	e.mu.Lock()
	if e.closed || len(e.connections) >= MaxConnections {
		e.skipped.Add(1)
		e.mu.Unlock()
		return false
	}
	ctx, cancel := context.WithCancel(ctx)
	e.connections[conn] = cancel
	e.wg.Add(1)
	e.mu.Unlock()
	go func() {
		defer e.wg.Done()
		stopClose := context.AfterFunc(ctx, func() { conn.Close() })
		err := e.serve(ctx, conn, metadata, dial, roots)
		stopClose()
		cancel()
		conn.Close()
		e.mu.Lock()
		delete(e.connections, conn)
		e.mu.Unlock()
		if onClose != nil {
			onClose(err)
		}
	}()
	return true
}

func (e *Engine) Close() {
	e.mu.Lock()
	e.closed = true
	for conn, cancel := range e.connections {
		cancel()
		conn.Close()
	}
	e.mu.Unlock()
	e.wg.Wait()
}

func (e *Engine) serve(ctx context.Context, conn net.Conn, metadata Metadata, dial DialFunc, roots *x509.CertPool) error {
	secure := metadata.Protocol == "tls"
	scheme := "http"
	if secure {
		scheme = "https"
		server := tls.Server(conn, &tls.Config{
			MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1"},
			GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
				// The router sniffs the same ClientHello before applying the domain
				// allowlist. Never sign for an unrelated name after that decision.
				if hello.ServerName != "" && normalizeHost(hello.ServerName) != normalizeHost(metadata.Host) {
					return nil, errors.New("TLS server name changed after capture selection")
				}
				return e.ca.certificate(metadata.Host)
			},
		})
		handshakeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := server.HandshakeContext(handshakeCtx)
		cancel()
		if err != nil {
			e.connectionError(metadata, fmt.Errorf("TLS handshake failed (trust, pinning or protocol): %w", err))
			return err
		}
		conn = server
	}
	transport := newTransport(dial, metadata.Host, roots, true)
	http1Transport := newTransport(dial, metadata.Host, roots, false)
	defer transport.CloseIdleConnections()
	defer http1Transport.CloseIdleConnections()
	target := &url.URL{Scheme: scheme, Host: net.JoinHostPort(metadata.Host, strconv.Itoa(int(metadata.Port)))}
	handler := e.handler(metadata, target, transport, http1Transport)
	base := &http.Server{
		Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: MaxHeaderBytes, ErrorLog: log.New(io.Discard, "", 0),
		BaseContext: func(net.Listener) context.Context { return ctx },
	}
	h2 := &http2.Server{
		MaxConcurrentStreams: 16, MaxReadFrameSize: 64 * 1024,
		MaxDecoderHeaderTableSize: 4096, MaxEncoderHeaderTableSize: 4096,
		IdleTimeout:                  60 * time.Second,
		MaxUploadBufferPerConnection: 256 * 1024, MaxUploadBufferPerStream: 64 * 1024,
	}
	if server, ok := conn.(*tls.Conn); ok && server.ConnectionState().NegotiatedProtocol == "h2" {
		h2.ServeConn(conn, &http2.ServeConnOpts{Context: ctx, BaseConfig: base, Handler: handler})
		return nil
	}
	if !secure {
		reader := bufio.NewReader(conn)
		conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		prefix, _ := reader.Peek(3)
		conn.SetReadDeadline(time.Time{})
		conn = &bufferedConn{Conn: conn, reader: reader}
		if string(prefix) == "PRI" {
			h2.ServeConn(conn, &http2.ServeConnOpts{Context: ctx, BaseConfig: base, Handler: handler})
			return nil
		}
	}
	listener := newSingleListener(conn)
	defer listener.Close()
	stop := context.AfterFunc(ctx, func() { base.Close() })
	defer stop()
	err := base.Serve(listener)
	if errors.Is(err, net.ErrClosed) || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func newTransport(dial DialFunc, serverName string, roots *x509.CertPool, h2 bool) *http.Transport {
	transport := &http.Transport{
		Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		TLSClientConfig:   &tls.Config{ServerName: serverName, RootCAs: roots, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2: h2, DisableCompression: true, MaxIdleConns: 4, MaxIdleConnsPerHost: 4,
		MaxConnsPerHost: 4, IdleConnTimeout: 60 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second, ExpectContinueTimeout: time.Second,
		MaxResponseHeaderBytes: MaxHeaderBytes,
	}
	if !h2 {
		transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	}
	return transport
}

func (e *Engine) handler(metadata Metadata, target *url.URL, transport, http1Transport http.RoundTripper) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			http.Error(w, "Capture stopped; reconnect", http.StatusServiceUnavailable)
			return
		}
		e.wg.Add(1)
		e.mu.Unlock()
		defer e.wg.Done()
		requestHost := request.Host
		if host, _, err := net.SplitHostPort(requestHost); err == nil {
			requestHost = host
		}
		requestHost = normalizeHost(requestHost)
		if metadata.Protocol == "tls" && requestHost != normalizeHost(metadata.Host) {
			// A coalesced HTTP/2 request must not escape the TLS domain selection.
			http.Error(w, "Use a separate connection for this origin", http.StatusMisdirectedRequest)
			return
		}
		display := request.Clone(request.Context())
		display.URL.Scheme = target.Scheme
		display.URL.Host = request.Host
		if display.URL.Host == "" {
			display.URL.Host = target.Host
		}
		requestMetadata := metadata
		requestMetadata.Host = requestHost
		id := ""
		if e.config.matches(requestMetadata) {
			id = e.store.begin(requestMetadata, display, e.config.BodyLimit)
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				e.store.setError(id, errors.New("HTTP response stream aborted"))
				e.store.finish(id)
				panic(recovered)
			}
			e.store.finish(id)
		}()
		if request.Method == http.MethodConnect {
			e.store.setError(id, errors.New("nested CONNECT is not supported; bypass this proxy's domain"))
			http.Error(w, "Nested CONNECT is not supported", http.StatusNotImplemented)
			return
		}
		if request.Body != nil && request.Body != http.NoBody {
			request.Body = &tapBody{ReadCloser: request.Body, store: e.store, id: id, expected: request.ContentLength}
			defer request.Body.Close()
		}
		upstream := transport
		if request.Header.Get("Upgrade") != "" {
			upstream = http1Transport // RFC 6455 upgrades require HTTP/1.1.
		}
		proxy := &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				r.Out.URL.Scheme = target.Scheme
				r.Out.URL.Host = target.Host
				r.Out.URL.User = nil
				// ReverseProxy otherwise cleans query parameters before Rewrite.
				// Debugging must preserve the client's original request target.
				r.Out.URL.RawQuery = r.In.URL.RawQuery
				r.Out.Host = r.In.Host
				// Preserve client-supplied forwarding headers without adding our own.
				for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
					if values := r.In.Header.Values(name); len(values) > 0 {
						r.Out.Header[name] = append([]string(nil), values...)
					}
				}
			},
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				response, err := upstream.RoundTrip(r)
				if err != nil {
					return nil, err
				}
				e.store.response(id, response)
				if response.StatusCode != http.StatusSwitchingProtocols && response.Body != nil && response.Body != http.NoBody {
					response.Body = &tapBody{ReadCloser: response.Body, store: e.store, id: id, response: true, expected: response.ContentLength}
				}
				return response, nil
			}),
			FlushInterval: -1, // SSE and other streaming responses are visible immediately.
			ErrorLog:      log.New(io.Discard, "", 0),
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				e.store.setError(id, err)
				http.Error(w, "B-box: upstream connection failed; see HTTP debugging details", http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(w, request)
	})
}

func (e *Engine) connectionError(metadata Metadata, err error) {
	request := &http.Request{Method: "TLS", Proto: "TLS", Header: make(http.Header),
		URL: &url.URL{Scheme: "https", Host: net.JoinHostPort(metadata.Host, strconv.Itoa(int(metadata.Port)))}}
	id := e.store.begin(metadata, request, e.config.BodyLimit)
	e.store.setError(id, err)
	e.store.finish(id)
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type tapBody struct {
	io.ReadCloser
	store    *Store
	id       string
	response bool
	expected int64
	read     int64
}

func (b *tapBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.read += int64(n)
		b.store.appendBody(b.id, b.response, p[:n])
	}
	if err == io.EOF {
		b.store.bodyComplete(b.id, b.response)
	} else if err != nil {
		b.store.setError(b.id, err)
	}
	return n, err
}

func (b *tapBody) Close() error {
	if b.expected >= 0 && b.read == b.expected {
		b.store.bodyComplete(b.id, b.response)
	}
	return b.ReadCloser.Close()
}

type bufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

type singleListener struct {
	conn     net.Conn
	accepted bool
	done     chan struct{}
	once     sync.Once
}

func newSingleListener(conn net.Conn) *singleListener {
	listener := &singleListener{done: make(chan struct{})}
	listener.conn = &closeNotifyConn{Conn: conn, notify: func() { listener.once.Do(func() { close(listener.done) }) }}
	return listener
}

func (l *singleListener) Accept() (net.Conn, error) {
	if !l.accepted {
		l.accepted = true
		return l.conn, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *singleListener) Close() error { return l.conn.Close() }

func (l *singleListener) Addr() net.Addr { return l.conn.LocalAddr() }

type closeNotifyConn struct {
	net.Conn
	notify func()
}

func (c *closeNotifyConn) Close() error {
	err := c.Conn.Close()
	c.notify()
	return err
}
