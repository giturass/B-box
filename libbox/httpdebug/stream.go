package httpdebug

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	N "github.com/sagernet/sing/common/network"
)

type streamInjection struct {
	Direction string
	Body      []byte
	Opcode    int
}

type streamResult struct {
	body   []byte
	drop   bool
	inject []streamInjection
}

type streamSendFunc func(context.Context, string, []byte, int, bool) error

type activeStream struct {
	kind string
	send streamSendFunc
}

func newStreamID() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(value[:])
}

func (e *Engine) registerStream(id, kind string, send streamSendFunc) func() {
	stream := &activeStream{kind: kind, send: send}
	e.streamMu.Lock()
	if e.streams == nil {
		e.streams = make(map[string]*activeStream)
	}
	if id != "" && e.ctx.Err() == nil {
		e.streams[id] = stream
	}
	e.streamMu.Unlock()
	return func() {
		e.streamMu.Lock()
		if e.streams[id] == stream {
			delete(e.streams, id)
		}
		e.streamMu.Unlock()
	}
}

func streamIsActive(id string) bool {
	e := Current()
	if e == nil || id == "" || e.ctx.Err() != nil {
		return false
	}
	e.streamMu.Lock()
	_, exists := e.streams[id]
	e.streamMu.Unlock()
	return exists
}

// Injection is admitted against this particular service engine and active flow.
// Recording may be stopped; injection does not start it or revive a closed flow.
func (e *Engine) InjectStream(ctx context.Context, id, direction string, opcode int, data []byte) error {
	if !validID(id) || !streamDirection(direction) {
		return errors.New("invalid stream ID or direction")
	}
	if e == nil || e.ctx.Err() != nil {
		return errors.New("local service has stopped")
	}
	e.streamMu.Lock()
	stream := e.streams[id]
	e.streamMu.Unlock()
	if stream == nil {
		return errors.New("stream is no longer active")
	}
	if err := validateStreamPayload(stream.kind, opcode, data); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(e.ctx, cancel)
	defer func() { stop(); cancel() }()
	return stream.send(ctx, direction, data, opcode, true)
}

func streamDirection(direction string) bool { return direction == "client" || direction == "server" }

func validateStreamPayload(kind string, opcode int, data []byte) error {
	if kind == "tcp" {
		if opcode != 0 {
			return errors.New("TCP injection does not have a WebSocket opcode")
		}
		return nil
	}
	if kind != "websocket" || opcode != 1 && opcode != 2 {
		return errors.New("WebSocket data opcode must be 1 (text) or 2 (binary)")
	}
	if opcode == 1 && !utf8.Valid(data) {
		return errors.New("WebSocket text message must be valid UTF-8")
	}
	return nil
}

func streamAddress(metadata Metadata, kind string) string {
	if metadata.StreamURL != "" {
		return metadata.StreamURL
	}
	scheme := "tcp"
	if metadata.TLS != nil || metadata.Protocol == "tls" {
		scheme = "tls"
	}
	if kind == "websocket" {
		scheme = "ws"
		if metadata.TLS != nil || metadata.Protocol == "tls" {
			scheme = "wss"
		}
	}
	return (&url.URL{Scheme: scheme, Host: net.JoinHostPort(metadata.Host, strconv.Itoa(int(metadata.Port)))}).String()
}

// Only cleartext non-HTTP TCP can enter the generic stream path. TLS still
// requires the profile's MITM allowlist and successful certificate handshake.
func (e *Engine) WantsRawTCP(metadata Metadata) bool {
	if metadata.Protocol == "tls" || metadata.Protocol == "http" {
		return false
	}
	config := e.recording.Load()
	if config != nil && config.RawTCP && config.candidate(metadata) {
		return true
	}
	if e.scripts == nil || len(e.scripts.stream) == 0 {
		return false
	}
	scope := e.scripts.scope
	scope.RawTCP = true
	if !scope.candidate(metadata) {
		return false
	}
	for _, script := range e.scripts.stream {
		if script.options.Type == "tcp" {
			return true
		}
	}
	return false
}

func (e *Engine) wantsWebSocket(metadata Metadata) bool {
	if metadata.Protocol != "" && metadata.Protocol != "http" || e.scripts == nil || !e.scripts.scope.candidate(metadata) {
		return false
	}
	for _, script := range e.scripts.stream {
		if script.options.Type == "websocket" {
			return true
		}
	}
	return false
}

func (e *Engine) beginStreamMessage(metadata Metadata, kind, direction string, opcode int, injected, dropped bool) string {
	metadata.Direction, metadata.Opcode = direction, opcode
	metadata.Injected, metadata.Dropped = injected, dropped
	protocol, method := "WebSocket", "WS"
	if kind == "tcp" {
		protocol, method, metadata.RawTCP = "TCP", "TCP", true
		if metadata.TLS != nil || metadata.Protocol == "tls" {
			protocol = "TLS/TCP"
		}
	}
	request := &http.Request{Method: method, Proto: protocol, Header: make(http.Header),
		URL: &url.URL{}, Body: io.NopCloser(bytes.NewReader(nil))}
	request.URL, _ = url.Parse(streamAddress(metadata, kind))
	if request.URL == nil {
		return ""
	}
	if opcode == 1 {
		request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	} else {
		request.Header.Set("Content-Type", "application/octet-stream")
	}
	id := e.beginRecording(metadata, request)
	if id != "" {
		e.store.response(id, &http.Response{Proto: protocol, Header: request.Header.Clone(), ContentLength: -1,
			Body: io.NopCloser(bytes.NewReader(nil))})
		e.store.bodyComplete(id, direction == "client") // The opposite direction is empty.
		e.recordedRequests.Add(1)
		if dropped {
			e.store.addNote(id, "Message dropped by stream script; saved payload was not forwarded")
		}
		if injected {
			e.store.addNote(id, "Injected stream message")
		}
	}
	return id
}

func (e *Engine) recordStreamMessage(metadata Metadata, kind, direction string, opcode int, payload []byte, injected, dropped bool) string {
	id := e.beginStreamMessage(metadata, kind, direction, opcode, injected, dropped)
	if id != "" {
		e.store.appendBody(id, direction == "server", payload)
		e.store.bodyComplete(id, direction == "server")
		e.store.finish(id)
	}
	return id
}

// Serialize every writer to a direction, including manual and script injection.
// A cancelled injection can interrupt its own write without leaving a deadline
// on the subsequent network write or cancelling the opposite direction.
type streamWriter struct {
	conn   net.Conn
	gate   chan struct{}
	closed bool // protected by gate
}

func newStreamWriter(conn net.Conn) *streamWriter {
	writer := &streamWriter{conn: conn, gate: make(chan struct{}, 1)}
	writer.gate <- struct{}{}
	return writer
}

func (w *streamWriter) write(ctx context.Context, data []byte) (written int, err error) {
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-w.gate:
	}
	defer func() { w.gate <- struct{}{} }()
	if w.closed {
		return 0, io.ErrClosedPipe
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = w.conn.SetWriteDeadline(time.Now())
		close(done)
	})
	defer func() {
		if !stop() {
			<-done
		}
		_ = w.conn.SetWriteDeadline(time.Time{})
		if ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	for written < len(data) {
		n, writeErr := w.conn.Write(data[written:])
		written += n
		if writeErr != nil {
			return written, writeErr
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

func (w *streamWriter) closeWrite(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-w.gate:
	}
	defer func() { w.gate <- struct{}{} }()
	w.closed = true
	if half, ok := streamCloseWriter(w.conn); ok {
		return half.CloseWrite()
	}
	return w.conn.Close()
}

// Follow the same replaceable writer and counter wrappers as sing-box's
// connection manager. A read cache or tracker must not hide TCP half-close.
func streamCloseWriter(conn net.Conn) (N.WriteCloser, bool) {
	writer, _ := N.UnwrapCountWriter(conn, nil)
	half, ok := N.UnwrapWriter(writer).(N.WriteCloser)
	return half, ok
}

func streamNote(metadata Metadata, kind string) func(string) {
	return func(message string) {
		writeLog("debug", fmt.Sprintf("%s processing; connection=%s stream=%s: %s", strings.ToUpper(kind), metadata.ConnectionID, metadata.StreamID, message))
	}
}
