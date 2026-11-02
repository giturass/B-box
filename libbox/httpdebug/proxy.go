package httpdebug

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"
)

type DialFunc func(context.Context) (net.Conn, error)

type Engine struct {
	config               Config
	ca                   *authority
	store                *Store
	ctx                  context.Context
	cancel               context.CancelFunc
	mu                   sync.Mutex
	wg                   sync.WaitGroup
	closed               bool
	connections          map[net.Conn]context.CancelFunc
	preparing            int
	skipped              atomic.Int64
	warning              atomic.Pointer[string]
	tlsOpened            atomic.Int64
	tlsFailed            atomic.Int64
	blockedQUIC          atomic.Int64
	plaintextConnections atomic.Int64
	plaintextBytes       atomic.Int64
	parsedRequests       atomic.Int64
	filteredRequests     atomic.Int64
	recordedRequests     atomic.Int64
}

func newEngine(config Config, store *Store) *Engine {
	e := &Engine{config: config, store: store, connections: make(map[net.Conn]context.CancelFunc)}
	e.ctx, e.cancel = context.WithCancel(context.Background())
	return e
}

func (e *Engine) Candidate(metadata Metadata) bool {
	return e.config.candidate(metadata)
}

func (e *Engine) Matches(metadata Metadata) bool { return e.config.matches(metadata) }

func (e *Engine) WantsQUIC(metadata Metadata) bool {
	metadata.Protocol = "tls"
	return (e.config.ExtractHosts || e.config.HTTPS && e.config.BlockQUIC) && e.config.candidate(metadata)
}

// The caller must have observed a QUIC ClientHello, never just an IP/UDP port.
func (e *Engine) RejectQUIC(metadata Metadata) bool {
	metadata.Protocol = "tls"
	e.Observe(metadata, "QUIC")
	if !e.config.BlockQUIC || !e.Matches(metadata) {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return false
	}
	e.blockedQUIC.Add(1)
	return true
}

func (e *Engine) Handle(ctx context.Context, conn net.Conn, metadata Metadata, dial DialFunc, roots *x509.CertPool, onClose func(error)) bool {
	if !e.Matches(metadata) || metadata.Protocol == "tls" && e.ca == nil {
		return false
	}
	e.mu.Lock()
	if e.closed || len(e.connections)+e.preparing >= MaxConnections {
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
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	e.cancel()
	for conn, cancel := range e.connections {
		cancel()
		conn.Close()
	}
	e.mu.Unlock()
	e.wg.Wait()
}

func (e *Engine) serve(ctx context.Context, conn net.Conn, metadata Metadata, dial DialFunc, roots *x509.CertPool) error {
	dial = bindDialLifetime(ctx, dial)
	transport := newTransport(dial)
	scheme := "http"
	if metadata.Protocol == "tls" {
		var cleanup func()
		var err error
		conn, transport, metadata, cleanup, err = e.openMITM(ctx, conn, metadata, dial, roots)
		if err != nil {
			return err
		}
		defer cleanup()
		scheme = "https"
	} else {
		metadata.CaptureSource = "HTTP"
		e.plaintextConnections.Add(1)
	}
	defer transport.CloseIdleConnections()
	target := &url.URL{Scheme: scheme, Host: net.JoinHostPort(metadata.Host, strconv.Itoa(int(metadata.Port)))}
	handler := e.handler(metadata, target, transport)
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
	if secure, ok := conn.(*tls.Conn); ok {
		if secure.ConnectionState().NegotiatedProtocol == "h2" {
			h2.ServeConn(conn, &http2.ServeConnOpts{Context: ctx, BaseConfig: base, Handler: handler})
			return nil
		}
	} else {
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

// net/http may detach a dial from a request's cancellation to reuse it. The
// selected route must still end when this intercepted client connection ends.
func bindDialLifetime(lifetime context.Context, dial DialFunc) DialFunc {
	return func(callCtx context.Context) (net.Conn, error) {
		ctx, cancel := context.WithCancel(callCtx)
		stop := context.AfterFunc(lifetime, cancel)
		conn, err := dial(ctx)
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			stop()
			cancel()
			if conn != nil {
				conn.Close()
			}
			return nil, err
		}
		return &cancelConn{Conn: conn, cancel: func() { stop(); cancel() }}, nil
	}
}

func newTransport(dial DialFunc) *http.Transport {
	return &http.Transport{
		Proxy: nil, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return dial(ctx) },
		DisableCompression: true, MaxIdleConns: 4, MaxIdleConnsPerHost: 4,
		MaxConnsPerHost: 4, IdleConnTimeout: 60 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second, ExpectContinueTimeout: time.Second,
		MaxResponseHeaderBytes: MaxHeaderBytes,
	}
}

func (e *Engine) handler(metadata Metadata, target *url.URL, transport http.RoundTripper) http.Handler {
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
		requestURL, err := url.Parse(target.Scheme + "://" + request.Host)
		if err != nil || requestURL.Hostname() == "" || requestURL.User != nil {
			http.Error(w, "Invalid request authority", http.StatusBadRequest)
			return
		}
		requestHost := normalizeHost(requestURL.Hostname())
		if metadata.Protocol == "tls" {
			port := requestURL.Port()
			if port == "" {
				port = "443"
			}
			if requestHost != normalizeHost(metadata.Host) || port != strconv.Itoa(int(metadata.Port)) {
				// HTTP/2 clients can retry this origin on its own TLS connection.
				e.filteredRequests.Add(1)
				http.Error(w, "Reconnect using this origin's own TLS connection", http.StatusMisdirectedRequest)
				return
			}
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
		selected := e.config.matches(requestMetadata)
		e.parsedRequests.Add(1)
		if selected {
			id = e.store.begin(requestMetadata, display, e.config.BodyLimit)
			if id != "" {
				e.recordedRequests.Add(1)
			}
		} else {
			e.filteredRequests.Add(1)
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
			e.store.setError(id, errors.New("nested CONNECT is not supported"))
			http.Error(w, "Nested CONNECT is not supported", http.StatusNotImplemented)
			return
		}
		request.URL = display.URL
		if request.Body != nil && request.Body != http.NoBody {
			request.Body = &tapBody{ReadCloser: request.Body, store: e.store, id: id, expected: request.ContentLength, trailers: request.Trailer, engine: e}
			defer request.Body.Close()
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
				// Trailer values arrive while reading the incoming body. Keep that
				// map shared instead of ReverseProxy's early clone with empty values.
				r.Out.Trailer = r.In.Trailer
				for _, name := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto"} {
					if values := r.In.Header.Values(name); len(values) > 0 {
						r.Out.Header[name] = append([]string(nil), values...)
					}
				}
			},
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				response, err := transport.RoundTrip(r)
				if err != nil {
					return nil, err
				}
				e.store.response(id, response)
				if response.StatusCode != http.StatusSwitchingProtocols && response.Body != nil && response.Body != http.NoBody {
					response.Body = &tapBody{ReadCloser: response.Body, store: e.store, id: id, response: true, expected: response.ContentLength, trailers: response.Trailer, engine: e}
				}
				return response, nil
			}),
			FlushInterval: -1, // SSE and other streaming responses are visible immediately.
			ErrorLog:      log.New(io.Discard, "", 0),
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
				e.store.setError(id, err)
				e.reportError("Upstream HTTP " + metadata.Host + ": " + err.Error())
				http.Error(w, "B-box: upstream connection failed; see HTTP debugging details", http.StatusBadGateway)
			},
		}
		proxy.ServeHTTP(w, request)
	})
}

// Observe records only the visible name, never a fabricated decrypted request.
// Domain observations do not imply that TLS plaintext was captured.
func (e *Engine) Observe(metadata Metadata, protocol string) {
	metadata.Protocol = "tls"
	if !e.config.ExtractHosts || !e.config.candidate(metadata) || !validHost(normalizeHost(metadata.Host)) {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	request := &http.Request{Method: "TLS", Proto: protocol, Header: make(http.Header),
		URL: &url.URL{Scheme: "https", Host: net.JoinHostPort(metadata.Host, strconv.Itoa(int(metadata.Port)))}}
	id := e.store.begin(metadata, request, e.config.BodyLimit)
	e.store.domainOnly(id)
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
	read     atomic.Int64
	trailers http.Header
	engine   *Engine
}

func (b *tapBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.read.Add(int64(n))
		b.engine.plaintextBytes.Add(int64(n))
		b.store.appendBody(b.id, b.response, p[:n])
	}
	if err == io.EOF {
		b.store.bodyComplete(b.id, b.response)
		b.store.trailers(b.id, b.response, b.trailers)
	} else if err != nil {
		b.store.setError(b.id, err)
	}
	return n, err
}

func (b *tapBody) Close() error {
	if b.expected >= 0 && b.read.Load() == b.expected {
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

func (e *Engine) skippedFlow(err error) {
	e.skipped.Add(1)
	e.reportError(err.Error())
}

func (e *Engine) reportError(message string) {
	message = strings.ToValidUTF8(message[:min(len(message), 4096)], "")
	e.warning.Store(&message)
	AppendCaptureLog("error", message)
}
