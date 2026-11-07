package httpdebug

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"maps"
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
	recording            atomic.Pointer[Config]
	rewrite              *RewriteProgram
	mitm                 *MITMConfig
	scripts              *ScriptProgram
	scriptEnvironment    scriptEnvironment
	ca                   *authority
	store                *Store
	ctx                  context.Context
	cancel               context.CancelFunc
	mu                   sync.Mutex
	wg                   sync.WaitGroup
	closed               bool
	connections          map[net.Conn]context.CancelFunc
	preparing            map[*tlsSelection]struct{}
	skipped              atomic.Int64
	warning              atomic.Pointer[string]
	tlsOpened            atomic.Int64
	tlsFailed            atomic.Int64
	tlsNoRequest         atomic.Int64
	blockedQUIC          atomic.Int64
	plaintextConnections atomic.Int64
	plaintextBytes       atomic.Int64
	parsedRequests       atomic.Int64
	filteredRequests     atomic.Int64
	recordedRequests     atomic.Int64
}

func newEngine(config Config, store *Store) *Engine {
	e := &Engine{store: store, connections: make(map[net.Conn]context.CancelFunc), preparing: make(map[*tlsSelection]struct{})}
	e.recording.Store(&config)
	e.ctx, e.cancel = context.WithCancel(context.Background())
	return e
}

func (e *Engine) Candidate(metadata Metadata) bool {
	config := e.recording.Load()
	if e.mitm.candidate(metadata) {
		return true
	}
	if metadata.Protocol == "tls" {
		return config != nil && config.ExtractHosts && config.candidate(metadata)
	}
	return config != nil && config.candidate(metadata) || e.httpProgram(metadata) != nil
}

func (e *Engine) Matches(metadata Metadata) bool {
	if metadata.Protocol == "tls" {
		return e.mitm.matches(metadata)
	}
	config := e.recording.Load()
	return config != nil && config.matches(metadata) || e.httpProgram(metadata) != nil
}

func (e *Engine) WantsQUIC(metadata Metadata) bool {
	metadata.Protocol = "tls"
	config := e.recording.Load()
	return config != nil && config.ExtractHosts && config.candidate(metadata) ||
		e.mitm.candidate(metadata) && e.mitm.BlockQUIC
}

// The caller must have observed a QUIC ClientHello, never just an IP/UDP port.
func (e *Engine) RejectQUIC(metadata Metadata) bool {
	metadata.Protocol = "tls"
	metadata.sniffedSNI = true
	e.Observe(metadata, "QUIC")
	if !e.mitm.matches(metadata) || !e.mitm.BlockQUIC {
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
	return e.HandleRouted(ctx, conn, metadata, dial, nil, roots, onClose)
}

func (e *Engine) HandleRouted(ctx context.Context, conn net.Conn, metadata Metadata, dial DialFunc, mapped RewriteDialFunc, roots *x509.CertPool, onClose func(error)) bool {
	selected, hasSelection := conn.(*selectedTLSConn)
	if hasSelection {
		defer selected.selection.release(false)
		if selected.selection.engine != e {
			return false
		}
	}
	if !e.Matches(metadata) || metadata.Protocol == "tls" && e.ca == nil {
		return false
	}
	e.mu.Lock()
	if e.closed || hasSelection && !selected.selection.held {
		e.mu.Unlock()
		e.connectionSkipped(metadata, "HTTP processing unavailable: service stopped or selection cancelled")
		return false
	}
	ctx, cancel := context.WithCancel(ctx)
	e.connections[conn] = cancel
	if hasSelection {
		selected.selection.detachLocked()
	} else {
		e.wg.Add(1)
	}
	e.mu.Unlock()
	go func() {
		defer e.wg.Done()
		stopClose := context.AfterFunc(ctx, func() { conn.Close() })
		err := e.serve(ctx, conn, metadata, dial, mapped, roots)
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
	connections := maps.Clone(e.connections)
	e.mu.Unlock()
	e.cancel()
	// Selected TLS wrappers release lifecycle tracking in Close, which takes
	// e.mu too. Close transports after releasing the engine lock.
	for conn, cancel := range connections {
		cancel()
		conn.Close()
	}
	e.wg.Wait()
}

func (e *Engine) serve(ctx context.Context, conn net.Conn, metadata Metadata, dial DialFunc, mapped RewriteDialFunc, roots *x509.CertPool) error {
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
		if metadata.RawTCP {
			defer transport.CloseIdleConnections()
			upstream, err := transport.DialTLSContext(ctx, "tcp", "")
			if err != nil {
				return err
			}
			defer upstream.Close()
			return e.serveTCP(ctx, conn, upstream, metadata)
		}
		scheme = "https"
	} else {
		metadata.CaptureSource = "HTTP"
		e.plaintextConnections.Add(1)
	}
	defer transport.CloseIdleConnections()
	var mappedTransport *http.Transport
	if mapped != nil {
		mappedTransport = newRewriteTransport(ctx, mapped, roots)
		if metadata.Protocol == "tls" {
			mappedTransport.ForceAttemptHTTP2 = e.mitm.H2
			mappedTransport.TLSClientConfig.NextProtos = e.mitm.httpProtocols()
			mappedTransport.TLSClientConfig.InsecureSkipVerify = e.mitm.SkipServerCertVerify
		}
		defer mappedTransport.CloseIdleConnections()
	}
	target := &url.URL{Scheme: scheme, Host: net.JoinHostPort(metadata.Host, strconv.Itoa(int(metadata.Port)))}
	process := e.handler(metadata, target, transport, mappedTransport)
	var received atomic.Bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		received.Store(true)
		process.ServeHTTP(w, request)
	})
	defer func() {
		if metadata.Protocol == "tls" && !received.Load() && ctx.Err() == nil && e.ctx.Err() == nil {
			e.tlsNoRequest.Add(1)
			reason := "TLS handshake completed but no HTTP request was received; possible certificate pinning or client cancellation"
			writeLog("warn", reason+"; connection="+metadata.ConnectionID+" host="+metadata.Host)
			e.recordTLSDiagnostic(metadata, reason, nil)
		}
	}()
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
	if metadata.Protocol == "tls" {
		if metadata.TLS != nil && metadata.TLS.ALPN == "h2" {
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

func (e *Engine) handler(metadata Metadata, target *url.URL, transport http.RoundTripper, mapped *http.Transport) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			http.Error(w, "Local service stopped; reconnect", http.StatusServiceUnavailable)
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
		if display.URL.Scheme == "" {
			display.URL.Scheme = target.Scheme
		}
		if display.URL.Host == "" {
			display.URL.Host = request.Host
		}
		if display.URL.Host == "" {
			display.URL.Host = target.Host
		}
		requestMetadata := metadata
		requestMetadata.Host = requestHost
		id := ""
		e.parsedRequests.Add(1)
		id = e.beginRecording(requestMetadata, display)
		if id != "" {
			e.recordedRequests.Add(1)
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
		defer func() {
			if request.Body != nil {
				request.Body.Close()
			}
		}()
		originalURL := *request.URL
		program := e.httpProgram(metadata)
		scriptID := id
		if scriptID == "" {
			scriptID = rand.Text()
		}
		note := func(message string) {
			e.store.addNote(id, message)
			writeLog("debug", "HTTP processing; connection="+metadata.ConnectionID+" request="+scriptID+": "+message)
		}
		var local *http.Response
		if program != nil {
			// Limit reads of a selected request body, including slow/chunked uploads.
			controller := http.NewResponseController(w)
			_ = controller.SetReadDeadline(time.Now().Add(15 * time.Second))
			local, err = program.request(request, scriptID, originalURL.String(), note)
			if err != nil && request.ProtoMajor == 1 {
				// HTTP/1 Body.Close may drain unread data before the response is
				// flushed. Stop those reads and never reuse a rejected upload's
				// connection, even when the client leaves its body unfinished.
				_ = controller.SetReadDeadline(time.Now())
				w.Header().Set("Connection", "close")
			} else {
				_ = controller.SetReadDeadline(time.Time{})
			}
			if err != nil {
				e.store.setError(id, err)
				if errors.Is(err, errScriptAbort) {
					panic(http.ErrAbortHandler)
				}
				e.reportError("HTTP request rewrite: " + err.Error())
				status := http.StatusBadGateway
				if errors.Is(err, errRewriteSize) {
					status = http.StatusRequestEntityTooLarge
				}
				http.Error(w, "B-box: request processing failed; see request details or service logs", status)
				return
			}
			e.store.rewrittenRequest(id, request, originalURL.String())
			if !sameRewriteOrigin(request.URL, &originalURL) {
				note("Mapped destination uses sing-box routing; connection metadata describes the original client route")
			}
		}
		if request.Body != nil && request.Body != http.NoBody {
			request.Body = &tapBody{ReadCloser: request.Body, store: e.store, id: id, expected: request.ContentLength, trailers: request.Trailer, engine: e}
		}
		proxy := &httputil.ReverseProxy{
			Rewrite: func(r *httputil.ProxyRequest) {
				if sameRewriteOrigin(r.In.URL, &originalURL) {
					r.Out.URL.Scheme = target.Scheme
					r.Out.URL.Host = target.Host
				}
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
				var response *http.Response
				var err error
				if local != nil {
					response = local
				} else if sameRewriteOrigin(request.URL, &originalURL) {
					response, err = transport.RoundTrip(r)
				} else if mapped != nil {
					response, err = mapped.RoundTrip(r)
				} else {
					err = errors.New("mapped destination requires a sing-box route dialer")
				}
				if err != nil {
					return nil, err
				}
				if program != nil {
					if err = program.response(request, response, scriptID, originalURL.String(), note); err != nil {
						response.Body.Close()
						return nil, err
					}
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
				if errors.Is(err, errScriptAbort) {
					panic(http.ErrAbortHandler)
				}
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
	if !validHost(normalizeHost(metadata.Host)) {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return
	}
	config := e.recording.Load()
	if config == nil || !config.ExtractHosts || !config.candidate(metadata) {
		return
	}
	request := &http.Request{Method: "TLS", Proto: protocol, Header: make(http.Header),
		URL: &url.URL{Scheme: "https", Host: net.JoinHostPort(metadata.Host, strconv.Itoa(int(metadata.Port)))}}
	id := e.store.begin(metadata, request, config.BodyLimit)
	e.store.domainOnly(id)
	if metadata.ProcessingReason != "" {
		e.store.addNote(id, metadata.ProcessingReason)
	}
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

func (e *Engine) reportError(message string) {
	message = strings.ToValidUTF8(message[:min(len(message), 4096)], "")
	e.warning.Store(&message)
	writeLog("error", message)
}
