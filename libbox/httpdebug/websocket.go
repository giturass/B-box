package httpdebug

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const webSocketStreamKind = "websocket"

// ReverseProxy owns the HTTP handshake and its buffered client bytes. Its
// upgraded io.ReadWriteCloser is replaced by a pipe whose other endpoint parses
// both WebSocket directions; no HTTP hijack or synthetic error is needed.
func (e *Engine) wrapWebSocket(ctx context.Context, request *http.Request, response *http.Response, metadata Metadata, parentID string) (io.ReadWriteCloser, string, error) {
	if response.StatusCode != http.StatusSwitchingProtocols || !strings.EqualFold(strings.TrimSpace(response.Header.Get("Upgrade")), "websocket") {
		return nil, "", nil
	}
	if !validWebSocketHandshake(request, response) {
		return nil, "WebSocket messages not inspected: unsupported or invalid opening handshake", nil
	}
	_, reason := negotiateWebSocketCompression(request.Header, response.Header)
	if reason != "" {
		return nil, reason, nil
	}
	upstream, ok := response.Body.(io.ReadWriteCloser)
	if !ok {
		return nil, "", errors.New("WebSocket upgrade has no bidirectional transport")
	}
	if metadata.StreamID == "" {
		metadata.StreamID = newStreamID()
	}
	metadata.ParentID = parentID
	metadata.StreamURL = webSocketAddress(request)
	proxy, client := net.Pipe()
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.ctx, cancel)
	// ReverseProxy replaces response.Header and response.Body while emitting
	// the 101 response. Keep the stream's handshake inputs independent of it.
	handshakeRequest := request.Clone(ctx)
	handshakeResponse := &http.Response{Header: response.Header.Clone()}
	stream := &webSocketUpgrade{Conn: proxy, upstream: upstream, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(stream.done)
		defer stop()
		defer cancel()
		defer client.Close()
		defer upstream.Close()
		err := e.serveWebSocket(ctx, client, client, upstream, handshakeRequest, handshakeResponse, metadata, parentID)
		if err != nil && ctx.Err() == nil {
			e.reportError("WebSocket stream for " + metadata.Host + ": " + err.Error())
		}
	}()
	return stream, "", nil
}

type webSocketUpgrade struct {
	net.Conn
	upstream io.ReadWriteCloser
	cancel   context.CancelFunc
	done     chan struct{}
	once     sync.Once
}

func (s *webSocketUpgrade) Close() error {
	s.once.Do(func() {
		s.cancel()
		s.Conn.Close()
		s.upstream.Close()
	})
	<-s.done
	return nil
}

func validWebSocketHandshake(request *http.Request, response *http.Response) bool {
	if request.Method != http.MethodGet || request.ProtoMajor != 1 ||
		!strings.EqualFold(strings.TrimSpace(request.Header.Get("Upgrade")), "websocket") ||
		!webSocketHeaderToken(request.Header, "Connection", "upgrade") ||
		!webSocketHeaderToken(response.Header, "Connection", "upgrade") ||
		request.Header.Get("Sec-WebSocket-Version") != "13" {
		return false
	}
	key := strings.TrimSpace(request.Header.Get("Sec-WebSocket-Key"))
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 16 {
		return false
	}
	accepted := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return strings.TrimSpace(response.Header.Get("Sec-WebSocket-Accept")) == base64.StdEncoding.EncodeToString(accepted[:])
}

func webSocketHeaderToken(header http.Header, name, token string) bool {
	for _, value := range header.Values(name) {
		for _, candidate := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(candidate), token) {
				return true
			}
		}
	}
	return false
}

type webSocketSession struct {
	engine   *Engine
	ctx      context.Context
	cancel   context.CancelFunc
	metadata Metadata
	request  *http.Request
	writers  map[string]*webSocketWriter
}

// All messages sent to one endpoint share a writer. An injection waits for a
// fragmented message's FIN; control frames can still pass between its fragments.
type webSocketWriter struct {
	gate     chan struct{}
	writer   io.Writer
	ctx      context.Context
	cancel   context.CancelFunc
	masked   bool
	message  bool
	boundary chan struct{}
	closed   bool
}

func newWebSocketWriter(writer io.Writer, ctx context.Context, cancel context.CancelFunc, masked bool) *webSocketWriter {
	w := &webSocketWriter{writer: writer, ctx: ctx, cancel: cancel, masked: masked, gate: make(chan struct{}, 1)}
	w.gate <- struct{}{}
	return w
}

func (w *webSocketWriter) lock(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.ctx.Done():
		return w.ctx.Err()
	case <-w.gate:
		return nil
	}
}

func (w *webSocketWriter) unlock() { w.gate <- struct{}{} }

func (w *webSocketWriter) beginRaw() error {
	if err := w.lock(w.ctx); err != nil {
		return err
	}
	defer w.unlock()
	if w.closed || w.ctx.Err() != nil {
		return net.ErrClosed
	}
	if w.message {
		return errors.New("WebSocket output already has a fragmented message")
	}
	w.message = true
	w.boundary = make(chan struct{})
	return nil
}

func (w *webSocketWriter) endRaw() {
	<-w.gate
	defer w.unlock()
	if w.message {
		w.message = false
		close(w.boundary)
	}
}

func (w *webSocketWriter) rawFrame(ctx context.Context, reader io.Reader, frame webSocketFrame, payload io.Writer) error {
	if err := w.lock(ctx); err != nil {
		return err
	}
	defer w.unlock()
	if w.closed {
		return net.ErrClosed
	}
	if err := writeWebSocketBytes(w.writer, frame.header); err != nil {
		return err
	}
	return readWebSocketPayload(ctx, reader, frame, w.writer, payload)
}

func (w *webSocketWriter) control(ctx context.Context, opcode int, payload []byte) error {
	if err := w.lock(ctx); err != nil {
		return err
	}
	defer w.unlock()
	if w.closed {
		return net.ErrClosed
	}
	err := writeWebSocketFrame(ctx, w.writer, opcode, true, false, w.masked, bytes.NewReader(payload), int64(len(payload)))
	if err == nil && opcode == webSocketClose {
		w.closed = true
	}
	return err
}

func (w *webSocketWriter) lockMessage(ctx context.Context) error {
	for {
		if err := w.lock(ctx); err != nil {
			return err
		}
		if w.closed || w.ctx.Err() != nil {
			w.unlock()
			return net.ErrClosed
		}
		if err := ctx.Err(); err != nil {
			w.unlock()
			return err
		}
		if !w.message {
			return nil
		}
		boundary := w.boundary
		w.unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.ctx.Done():
			return w.ctx.Err()
		case <-boundary:
		}
	}
}

func (w *webSocketWriter) send(ctx context.Context, opcode int, payload io.Reader, length int64) error {
	if err := w.lockMessage(ctx); err != nil {
		return err
	}
	defer w.unlock()
	// Cancellation after any header byte was emitted must end the connection;
	// continuing it with a partial injected frame would corrupt its framing.
	stop := context.AfterFunc(ctx, w.cancel)
	defer stop()
	return writeWebSocketFrame(ctx, w.writer, opcode, true, false, w.masked, payload, length)
}

func (w *webSocketWriter) sendOriginal(ctx context.Context, wire *os.File) error {
	if err := w.lockMessage(ctx); err != nil {
		return err
	}
	defer w.unlock()
	stop := context.AfterFunc(ctx, w.cancel)
	defer stop()
	if _, err := wire.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err := io.CopyBuffer(w.writer, &webSocketContextReader{ctx: ctx, reader: wire}, make([]byte, 32*1024))
	return err
}

type webSocketContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *webSocketContextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

type webSocketPumpResult struct {
	err      error
	graceful bool
}

func (e *Engine) serveWebSocket(parent context.Context, client net.Conn, clientReader io.Reader, upstream io.ReadWriteCloser, request *http.Request, response *http.Response, metadata Metadata, parentID string) error {
	compression, reason := negotiateWebSocketCompression(request.Header, response.Header)
	if reason != "" {
		return errors.New(reason)
	}
	if metadata.StreamID == "" {
		metadata.StreamID = newStreamID()
	}
	metadata.ParentID = parentID
	metadata.StreamURL = webSocketAddress(request)
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	stopService := context.AfterFunc(e.ctx, cancel)
	defer stopService()
	stop := context.AfterFunc(ctx, func() { client.Close(); upstream.Close() })
	defer stop()
	session := &webSocketSession{engine: e, ctx: ctx, cancel: cancel, metadata: metadata, request: request,
		writers: map[string]*webSocketWriter{
			"client": newWebSocketWriter(upstream, ctx, cancel, true),
			"server": newWebSocketWriter(client, ctx, cancel, false),
		}}
	unregister := e.registerStream(metadata.StreamID, webSocketStreamKind, session.inject)
	defer unregister()
	results := make(chan webSocketPumpResult, 2)
	go func() { results <- session.pump(clientReader, "client", compression.enabled, compression.client) }()
	go func() { results <- session.pump(upstream, "server", compression.enabled, compression.server) }()
	first := <-results
	if !first.graceful {
		var protocol *webSocketProtocolError
		if errors.As(first.err, &protocol) {
			session.failProtocol(protocol)
		}
		cancel()
		client.Close()
		upstream.Close()
	}
	second := <-results
	cancel()
	client.Close()
	upstream.Close()
	if parent.Err() != nil {
		return parent.Err()
	}
	return errors.Join(webSocketPumpError(first.err), webSocketPumpError(second.err))
}

func webSocketPumpError(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func (s *webSocketSession) failProtocol(protocol *webSocketProtocolError) {
	ctx, cancel := context.WithTimeout(s.ctx, time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, func() { s.cancel() })
	defer stop()
	payload := make([]byte, 2)
	binary.BigEndian.PutUint16(payload, protocol.code)
	var wait sync.WaitGroup
	for _, writer := range s.writers {
		wait.Add(1)
		go func(writer *webSocketWriter) {
			defer wait.Done()
			_ = writer.control(ctx, webSocketClose, payload)
		}(writer)
	}
	wait.Wait()
}

func (s *webSocketSession) inject(ctx context.Context, direction string, body []byte, opcode int, injected bool) error {
	writer := s.writers[direction]
	if writer == nil || opcode != webSocketText && opcode != webSocketBinary {
		return errors.New("WebSocket injection requires client/server direction and text/binary opcode")
	}
	if opcode == webSocketText && !utf8.Valid(body) {
		return errors.New("WebSocket text injection must be valid UTF-8")
	}
	if err := writer.send(ctx, opcode, bytes.NewReader(body), int64(len(body))); err != nil {
		return err
	}
	s.engine.recordStreamMessage(s.metadata, webSocketStreamKind, direction, opcode, body, injected, false)
	return nil
}

type webSocketMessage struct {
	ctx        context.Context
	id         string
	opcode     int
	compressed bool
	processing bool
	runScript  bool
	wire       *os.File
	payload    *os.File
	sink       *webSocketPayloadWriter
	input      *io.PipeWriter
	inflated   chan webSocketInflated
}

type webSocketInflated struct {
	dictionary []byte
	err        error
}

type webSocketPayloadWriter struct {
	ctx       context.Context
	writer    io.Writer
	text      bool
	validator webSocketUTF8
	size      int64
}

func (w *webSocketPayloadWriter) Write(data []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(data)) > math.MaxInt64-w.size {
		return 0, webSocketError(1009, "message length cannot be represented")
	}
	if w.text {
		if err := w.validator.write(data); err != nil {
			return 0, err
		}
	}
	n, err := w.writer.Write(data)
	w.size += int64(n)
	return n, err
}

type webSocketRecordingWriter struct {
	store    *Store
	id       string
	response bool
}

func (w *webSocketRecordingWriter) Write(data []byte) (int, error) {
	w.store.appendBody(w.id, w.response, data)
	return len(data), nil
}

func (s *webSocketSession) beginMessage(direction string, frame webSocketFrame, config webSocketCompressionDirection, dictionary []byte, forceProcessing bool) (*webSocketMessage, error) {
	e := s.engine
	metadata := s.metadata
	metadata.Direction, metadata.Opcode = direction, frame.opcode
	message := &webSocketMessage{ctx: s.ctx, opcode: frame.opcode, compressed: frame.compressed,
		runScript: e.streamMessageNeedsBody(s.ctx, metadata, webSocketStreamKind, direction)}
	message.processing = message.runScript || forceProcessing
	message.id = e.beginWebSocketRecord(metadata, s.request)
	var output io.Writer = &webSocketRecordingWriter{store: e.store, id: message.id, response: direction == "server"}
	if message.processing {
		var err error
		message.wire, err = os.CreateTemp(e.store.directory, "websocket-wire-*.tmp")
		if err != nil {
			e.finishWebSocketRecord(message.id, direction, err)
			return nil, err
		}
		message.payload, err = os.CreateTemp(e.store.directory, "websocket-payload-*.tmp")
		if err != nil {
			message.cleanup(err)
			e.finishWebSocketRecord(message.id, direction, err)
			return nil, err
		}
		output = message.payload
	} else if err := s.writers[direction].beginRaw(); err != nil {
		e.finishWebSocketRecord(message.id, direction, err)
		return nil, err
	}
	message.sink = &webSocketPayloadWriter{ctx: s.ctx, writer: output, text: frame.opcode == webSocketText}
	if message.compressed {
		input, writer := io.Pipe()
		message.input = writer
		message.inflated = make(chan webSocketInflated, 1)
		go func() {
			defer input.Close()
			dictionary, err := inflateWebSocketMessage(s.ctx, input, message.sink, config, dictionary)
			message.inflated <- webSocketInflated{dictionary: dictionary, err: err}
		}()
	}
	return message, nil
}

func (m *webSocketMessage) destination() io.Writer {
	if m.input != nil {
		return m.input
	}
	return m.sink
}

func (m *webSocketMessage) complete() ([]byte, error) {
	var dictionary []byte
	if m.input != nil {
		m.input.Close()
		result := <-m.inflated
		m.input, m.inflated = nil, nil
		if result.err != nil {
			return nil, result.err
		}
		dictionary = result.dictionary
	}
	if m.sink.text {
		if err := m.sink.validator.finish(); err != nil {
			return nil, err
		}
	}
	return dictionary, nil
}

func (m *webSocketMessage) cleanup(err error) {
	if m.input != nil {
		m.input.CloseWithError(err)
		<-m.inflated
		m.input, m.inflated = nil, nil
	}
	for _, file := range []*os.File{m.wire, m.payload} {
		if file != nil {
			file.Close()
			os.Remove(file.Name())
		}
	}
}

func (s *webSocketSession) pump(reader io.Reader, direction string, compression bool, config webSocketCompressionDirection) (result webSocketPumpResult) {
	writer := s.writers[direction]
	var message *webSocketMessage
	var dictionary []byte
	// A rewritten or dropped compressed message changes the peer's history.
	// Thereafter send decoded, uncompressed messages, which RFC 7692 permits,
	// instead of relaying references to the original sender's private history.
	diverged := false
	defer func() {
		if message != nil {
			s.engine.finishWebSocketRecord(message.id, direction, result.err)
			message.cleanup(result.err)
			writer.endRaw()
		}
	}()
	for {
		frame, err := readWebSocketFrame(reader, direction == "client", compression)
		if err != nil {
			if errors.Is(err, io.EOF) && message != nil {
				err = io.ErrUnexpectedEOF
			}
			result.err = err
			return
		}
		if frame.opcode >= webSocketClose {
			var payload bytes.Buffer
			if err = readWebSocketPayload(s.ctx, reader, frame, nil, &payload); err != nil {
				result.err = err
				return
			}
			if frame.opcode == webSocketClose {
				if err = validateWebSocketClose(payload.Bytes()); err != nil {
					result.err = err
					return
				}
			}
			if err = writer.control(s.ctx, frame.opcode, payload.Bytes()); err != nil {
				result.err = err
				return
			}
			if frame.opcode == webSocketClose {
				if message != nil {
					s.engine.finishWebSocketRecord(message.id, direction, io.ErrUnexpectedEOF)
					message.cleanup(io.ErrUnexpectedEOF)
					message = nil
					writer.endRaw()
				}
				result.graceful = true
				return
			}
			continue
		}
		if frame.opcode == webSocketContinuation {
			if message == nil {
				result.err = webSocketError(1002, "continuation without a data message")
				return
			}
		} else {
			if message != nil {
				result.err = webSocketError(1002, "new data message before the previous FIN")
				return
			}
			message, err = s.beginMessage(direction, frame, config, dictionary, diverged && frame.compressed)
			if err != nil {
				result.err = err
				return
			}
		}
		if message.processing {
			if err = writeWebSocketBytes(message.wire, frame.header); err == nil {
				err = readWebSocketPayload(s.ctx, reader, frame, message.wire, message.destination())
			}
		} else {
			err = writer.rawFrame(s.ctx, reader, frame, message.destination())
		}
		if err != nil {
			result.err = err
			return
		}
		if !frame.final {
			continue
		}
		nextDictionary, err := message.complete()
		if err != nil {
			result.err = err
			return
		}
		if message.compressed {
			dictionary = nextDictionary
		}
		if message.processing {
			diverged, err = s.processMessage(direction, message, diverged)
		}
		s.engine.finishWebSocketRecord(message.id, direction, err)
		message.cleanup(err)
		message = nil
		writer.endRaw()
		if err != nil {
			result.err = err
			return
		}
	}
}

func (s *webSocketSession) processMessage(direction string, message *webSocketMessage, diverged bool) (bool, error) {
	e := s.engine
	metadata := s.metadata
	metadata.Direction, metadata.Opcode = direction, message.opcode
	metadata.messageID = message.id
	limit := e.streamMessageBodyLimit(s.ctx, metadata, webSocketStreamKind, direction)
	var processed streamResult
	changed := false
	if !message.runScript {
		// This message only needs reframing after a previous compression rewrite.
	} else if limit > 0 && message.sink.size > limit {
		e.store.addNote(message.id, "WebSocket script skipped: message exceeds its configured body limit")
	} else {
		if _, err := message.payload.Seek(0, io.SeekStart); err != nil {
			return diverged, err
		}
		data, err := io.ReadAll(&webSocketContextReader{ctx: s.ctx, reader: message.payload})
		if err != nil {
			return diverged, err
		}
		processed, err = e.processStreamMessage(s.ctx, metadata, webSocketStreamKind, direction, message.opcode, data)
		if err != nil {
			if s.ctx.Err() != nil || errors.Is(err, errScriptAbort) {
				return diverged, err
			}
			e.store.addNote(message.id, "WebSocket script failed; original message preserved: "+err.Error())
			writeLog("warn", "WebSocket script; stream="+metadata.StreamID+": "+err.Error())
			processed = streamResult{}
		} else {
			changed = !bytes.Equal(data, processed.body)
			if message.opcode == webSocketText && !utf8.Valid(processed.body) {
				return diverged, webSocketError(1007, "script returned invalid UTF-8 text")
			}
		}
	}
	if message.compressed && (changed || processed.drop) {
		diverged = true
	}
	var source io.Reader = io.NewSectionReader(message.payload, 0, message.sink.size)
	length := message.sink.size
	if changed {
		source, length = bytes.NewReader(processed.body), int64(len(processed.body))
	}
	if processed.drop {
		e.store.markStreamResult(message.id, false, true)
	} else {
		var err error
		if changed || message.compressed && diverged {
			err = s.writers[direction].send(s.ctx, message.opcode, source, length)
		} else {
			err = s.writers[direction].sendOriginal(s.ctx, message.wire)
		}
		if err != nil {
			return diverged, err
		}
		if changed {
			e.store.addNote(message.id, "WebSocket payload rewritten by script")
			source = bytes.NewReader(processed.body)
		} else {
			source = io.NewSectionReader(message.payload, 0, message.sink.size)
		}
	}
	if processed.drop {
		source = io.NewSectionReader(message.payload, 0, message.sink.size)
	}
	if _, err := io.CopyBuffer(&webSocketRecordingWriter{store: e.store, id: message.id, response: direction == "server"},
		&webSocketContextReader{ctx: s.ctx, reader: source}, make([]byte, 32*1024)); err != nil {
		return diverged, err
	}
	for _, injection := range processed.inject {
		if err := s.inject(s.ctx, injection.Direction, injection.Body, injection.Opcode, true); err != nil {
			return diverged, err
		}
	}
	return diverged, nil
}

func (e *Engine) beginWebSocketRecord(metadata Metadata, request *http.Request) string {
	metadata.StreamURL = webSocketAddress(request)
	return e.beginStreamMessage(metadata, webSocketStreamKind, metadata.Direction, metadata.Opcode, false, false)
}

func webSocketAddress(request *http.Request) string {
	target := &url.URL{}
	if request.URL != nil {
		*target = *request.URL
	}
	switch target.Scheme {
	case "https":
		target.Scheme = "wss"
	case "", "http":
		target.Scheme = "ws"
	}
	if target.Host == "" {
		target.Host = request.Host
	}
	return target.String()
}

func (e *Engine) finishWebSocketRecord(id, direction string, err error) {
	if err != nil {
		e.store.setError(id, fmt.Errorf("WebSocket message incomplete: %w", err))
	} else {
		e.store.bodyComplete(id, direction == "server")
	}
	e.store.finish(id)
}
