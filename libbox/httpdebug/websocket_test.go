package httpdebug

import (
	"bytes"
	"compress/flate"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func webSocketTestHeaders(extensions string) (*http.Request, *http.Response) {
	request, _ := http.NewRequest(http.MethodGet, "http://example.test/socket", nil)
	request.Header.Set("Connection", "keep-alive, Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Sec-WebSocket-Version", "13")
	request.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	response := &http.Response{StatusCode: 101, Header: http.Header{
		"Connection": {"Upgrade"}, "Upgrade": {"websocket"},
		"Sec-Websocket-Accept": {"s3pPLMBiTxaQ9kYGzzhZRbK+xOo="},
	}}
	if extensions != "" {
		request.Header.Set("Sec-WebSocket-Extensions", extensions)
		response.Header.Set("Sec-WebSocket-Extensions", extensions)
	}
	return request, response
}

func webSocketTestSession(t *testing.T, engine *Engine, extensions string) (net.Conn, net.Conn, <-chan error) {
	t.Helper()
	client, inbound := net.Pipe()
	upstream, server := net.Pipe()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	server.SetDeadline(time.Now().Add(5 * time.Second))
	request, response := webSocketTestHeaders(extensions)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- engine.serveWebSocket(ctx, inbound, inbound, upstream, request, response,
			Metadata{Host: "example.test", Port: 80, Protocol: "http", ConnectionID: "client-connection",
				StreamID: "00112233445566778899aabbccddeeff"}, "11223344556677889900aabbccddeeff")
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		client.Close()
		server.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("WebSocket session did not finish after cancellation")
		}
	})
	return client, server, done
}

func webSocketTestRead(reader io.Reader, client, compression bool) (webSocketFrame, []byte, error) {
	frame, err := readWebSocketFrame(reader, client, compression)
	if err != nil {
		return frame, nil, err
	}
	var payload bytes.Buffer
	err = readWebSocketPayload(context.Background(), reader, frame, nil, &payload)
	return frame, payload.Bytes(), err
}

func webSocketTestWrite(writer io.Writer, opcode int, final, compressed, client bool, payload []byte) error {
	return writeWebSocketFrame(context.Background(), writer, opcode, final, compressed, client,
		bytes.NewReader(payload), int64(len(payload)))
}

func finishWebSocketTestSession(t *testing.T, client, server net.Conn, done <-chan error) {
	t.Helper()
	serverDone := make(chan error, 1)
	go func() {
		frame, payload, err := webSocketTestRead(server, true, true)
		if err == nil && frame.opcode != webSocketClose {
			err = errors.New("expected client close frame")
		}
		if err == nil {
			err = webSocketTestWrite(server, webSocketClose, true, false, false, payload)
		}
		serverDone <- err
	}()
	if err := webSocketTestWrite(client, webSocketClose, true, false, true, []byte{3, 232}); err != nil {
		t.Fatal(err)
	}
	frame, payload, err := webSocketTestRead(client, false, true)
	if err != nil || frame.opcode != webSocketClose || !bytes.Equal(payload, []byte{3, 232}) {
		t.Fatalf("close handshake changed: %+v %x %v", frame, payload, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WebSocket close handshake did not finish the session")
	}
}

func waitWebSocketMessages(t *testing.T, store *Store, count int) []Summary {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		var messages []Summary
		complete := true
		for _, item := range store.Page(0, "").Items {
			if item.Protocol == "WebSocket" {
				messages = append(messages, item)
				complete = complete && item.Complete
			}
		}
		if len(messages) == count && complete {
			return messages
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("WebSocket messages did not complete: %+v", store.Page(0, ""))
	return nil
}

func TestWebSocketUpgradeSnapshotsHandshakeBeforeProxyMutation(t *testing.T) {
	engine, store := testEngine(t, Config{})
	request, response := webSocketTestHeaders("permessage-deflate")
	upstream, origin := net.Pipe()
	defer origin.Close()
	origin.SetDeadline(time.Now().Add(5 * time.Second))
	response.Body = upstream
	stream, reason, err := engine.wrapWebSocket(context.Background(), request, response,
		Metadata{Host: "example.test", Port: 80, Protocol: "http"}, "11223344556677889900aabbccddeeff")
	if err != nil || reason != "" || stream == nil {
		upstream.Close()
		t.Fatalf("valid WebSocket handshake did not open a stream: %s %v", reason, err)
	}
	t.Cleanup(func() { stream.Close() })
	// The HTTP proxy owns and may replace the original response headers after
	// wrapWebSocket returns. Later message parsing must use the opening snapshot.
	response.Header = http.Header{"Sec-Websocket-Extensions": {"unsupported"}}
	response.Body = nil
	request.Header.Set("Sec-WebSocket-Extensions", "unsupported")
	request.URL.Path = "/changed-after-upgrade"
	payload := []byte("original negotiated handshake")
	encoded := newWebSocketTestDeflater(t).message(t, payload)
	writeDone := make(chan error, 1)
	go func() { writeDone <- webSocketTestWrite(stream, webSocketText, true, true, true, encoded) }()
	frame, wirePayload, err := webSocketTestRead(origin, true, true)
	if err != nil || !frame.compressed || !bytes.Equal(wirePayload, encoded) {
		t.Fatalf("proxy mutation changed the negotiated compressed transport: %+v %x %v", frame, wirePayload, err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	items := waitWebSocketMessages(t, store, 1)
	detail, err := store.detailWithBodies(items[0].ID)
	if err != nil || !bytes.Equal(detail.RequestBody, payload) || detail.Request.URL != "ws://example.test/socket" {
		t.Fatalf("message did not use the original opening handshake: %+v %v", detail, err)
	}
}

func TestWebSocketFrameLengthsMaskingAndValidation(t *testing.T) {
	for _, size := range []int{0, 125, 126, 65535, 65536} {
		for _, client := range []bool{false, true} {
			var wire bytes.Buffer
			payload := bytes.Repeat([]byte{0, 255, 17, 42}, (size+3)/4)[:size]
			if err := webSocketTestWrite(&wire, webSocketBinary, true, false, client, payload); err != nil {
				t.Fatal(err)
			}
			frame, decoded, err := webSocketTestRead(&wire, client, false)
			if err != nil || frame.length != int64(size) || !bytes.Equal(decoded, payload) || wire.Len() != 0 {
				t.Fatalf("length=%d client=%v: %+v %v", size, client, frame, err)
			}
		}
	}
	for _, test := range []struct {
		name   string
		header []byte
		client bool
	}{
		{"client unmasked", []byte{0x81, 0}, true},
		{"server masked", []byte{0x81, 0x80}, false},
		{"reserved bit", []byte{0xa1, 0}, false},
		{"unknown opcode", []byte{0x83, 0}, false},
		{"fragmented ping", []byte{0x09, 0}, false},
		{"compressed ping", []byte{0xc9, 0}, false},
		{"compressed continuation", []byte{0xc0, 0}, false},
		{"nonminimal length", []byte{0x82, 126, 0, 1}, false},
		{"oversized control", []byte{0x89, 126, 0, 126}, false},
		{"negative length", []byte{0x82, 127, 128, 0, 0, 0, 0, 0, 0, 0}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := readWebSocketFrame(bytes.NewReader(test.header), test.client, true)
			var protocol *webSocketProtocolError
			if !errors.As(err, &protocol) || protocol.code != 1002 {
				t.Fatalf("invalid frame accepted: %v", err)
			}
		})
	}
	var text webSocketUTF8
	encoded := []byte("你好🙂")
	for _, value := range encoded {
		if err := text.write([]byte{value}); err != nil {
			t.Fatal(err)
		}
	}
	if err := text.finish(); err != nil {
		t.Fatal(err)
	}
	for _, payload := range [][]byte{{255}, {0xe4, 0xbd}, {0xc0, 0x80}} {
		var text webSocketUTF8
		err := text.write(payload)
		if err == nil {
			err = text.finish()
		}
		if err == nil {
			t.Fatalf("invalid UTF-8 accepted: %x", payload)
		}
	}
	for _, payload := range [][]byte{{1}, {3, 237}, {3, 232, 255}} {
		if validateWebSocketClose(payload) == nil {
			t.Fatalf("invalid close payload accepted: %x", payload)
		}
	}
}

type webSocketTestDeflater struct {
	buffer bytes.Buffer
	writer *flate.Writer
	offset int
}

func newWebSocketTestDeflater(t *testing.T) *webSocketTestDeflater {
	t.Helper()
	compressor := &webSocketTestDeflater{}
	var err error
	compressor.writer, err = flate.NewWriter(&compressor.buffer, flate.DefaultCompression)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { compressor.writer.Close() })
	return compressor
}

func (c *webSocketTestDeflater) message(t *testing.T, payload []byte) []byte {
	t.Helper()
	if _, err := c.writer.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := c.writer.Flush(); err != nil {
		t.Fatal(err)
	}
	data := c.buffer.Bytes()
	if len(data)-c.offset < 4 || !bytes.Equal(data[len(data)-4:], []byte{0, 0, 255, 255}) {
		t.Fatal("DEFLATE message did not end in sync-flush")
	}
	message := append([]byte(nil), data[c.offset:len(data)-4]...)
	c.offset = len(data)
	return message
}

func TestWebSocketDeflateNegotiationAndContextTakeover(t *testing.T) {
	for _, test := range []struct {
		offer, selected string
		accepted        bool
	}{
		{"", "", true},
		{"permessage-deflate", "permessage-deflate", true},
		{"permessage-deflate; client_max_window_bits", "permessage-deflate; client_max_window_bits=12", true},
		{"permessage-deflate; client_max_window_bits=8", "permessage-deflate; client_max_window_bits=12", true},
		{"permessage-deflate; server_no_context_takeover", "permessage-deflate", false},
		{"permessage-deflate; server_max_window_bits=12", "permessage-deflate; server_max_window_bits=13", false},
		{"permessage-deflate; server_max_window_bits=15", "permessage-deflate", false},
		{"permessage-deflate", "permessage-deflate; client_max_window_bits=12", false},
		{"", "permessage-deflate", false},
		{"x-private", "x-private", false},
		{"permessage-deflate", "permessage-deflate; x-private=1", false},
		{"permessage-deflate", "permessage-deflate; server_max_window_bits=7", false},
		{"permessage-deflate", "permessage-deflate; server_max_window_bits=012", false},
		{"permessage-deflate", "permessage-deflate; server_no_context_takeover=1", false},
		{"permessage-deflate", "permessage-deflate; server_max_window_bits=12; server_max_window_bits=12", false},
	} {
		request, response := make(http.Header), make(http.Header)
		request.Set("Sec-WebSocket-Extensions", test.offer)
		response.Set("Sec-WebSocket-Extensions", test.selected)
		_, reason := negotiateWebSocketCompression(request, response)
		if (reason == "") != test.accepted {
			t.Fatalf("offer=%q selected=%q: %s", test.offer, test.selected, reason)
		}
	}
	compressor := newWebSocketTestDeflater(t)
	first := []byte(strings.Repeat("history:0123456789abcdef;", 1000))
	second := append([]byte("second:"), first[len(first)-16000:]...)
	var dictionary []byte
	for _, payload := range [][]byte{first, second} {
		encoded := compressor.message(t, payload)
		var output bytes.Buffer
		var err error
		dictionary, err = inflateWebSocketMessage(context.Background(), bytes.NewReader(encoded), &output,
			webSocketCompressionDirection{window: 15}, dictionary)
		if err != nil || !bytes.Equal(output.Bytes(), payload) || len(dictionary) > 32768 {
			t.Fatalf("context-takeover decode failed: %d %v", output.Len(), err)
		}
	}
	var output bytes.Buffer
	_, err := inflateWebSocketMessage(context.Background(), bytes.NewReader([]byte{1, 0, 0, 255, 255}), &output,
		webSocketCompressionDirection{window: 15}, nil)
	if err == nil {
		t.Fatal("premature BFINAL accepted before message boundary")
	}
}

func TestWebSocketCompressedFragmentsPreserveWireAndDecodeRecording(t *testing.T) {
	engine, store := testEngine(t, Config{BodyLimit: -1})
	client, server, done := webSocketTestSession(t, engine,
		"permessage-deflate; client_no_context_takeover; server_no_context_takeover")
	compressor := newWebSocketTestDeflater(t)
	payload := []byte(strings.Repeat("分片🙂compressed;", 2000))
	encoded := compressor.message(t, payload)
	split := len(encoded) / 2
	writeDone := make(chan error, 1)
	go func() {
		err := webSocketTestWrite(client, webSocketText, false, true, true, encoded[:split])
		if err == nil {
			err = webSocketTestWrite(client, webSocketPing, true, false, true, []byte("ping"))
		}
		if err == nil {
			err = webSocketTestWrite(client, webSocketContinuation, true, false, true, encoded[split:])
		}
		writeDone <- err
	}()
	var forwarded bytes.Buffer
	for index, opcode := range []int{webSocketText, webSocketPing, webSocketContinuation} {
		frame, data, err := webSocketTestRead(server, true, true)
		if err != nil || frame.opcode != opcode || frame.compressed != (index == 0) {
			t.Fatalf("compressed fragment changed: %+v %v", frame, err)
		}
		if opcode != webSocketPing {
			forwarded.Write(data)
		}
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(forwarded.Bytes(), encoded) {
		t.Fatal("inspection changed the original compressed payload")
	}
	items := waitWebSocketMessages(t, store, 1)
	detail, err := store.detailWithBodies(items[0].ID)
	if err != nil || !bytes.Equal(detail.RequestBody, payload) || !detail.Request.BodyComplete {
		t.Fatalf("compressed message was not recorded as complete decoded text: %+v %v", detail, err)
	}
	finishWebSocketTestSession(t, client, server, done)
}

func TestWebSocketFragmentControlAndRecordingBoundaries(t *testing.T) {
	engine, store := testEngine(t, Config{})
	client, server, done := webSocketTestSession(t, engine, "")
	text := []byte("你好🙂")
	writeDone := make(chan error, 1)
	go func() {
		err := webSocketTestWrite(client, webSocketText, false, false, true, text[:2])
		if err == nil {
			err = webSocketTestWrite(client, webSocketPing, true, false, true, []byte("ping"))
		}
		if err == nil {
			err = webSocketTestWrite(client, webSocketContinuation, true, false, true, text[2:])
		}
		writeDone <- err
	}()
	for index, expected := range []int{webSocketText, webSocketPing, webSocketContinuation} {
		frame, payload, err := webSocketTestRead(server, true, false)
		if err != nil || frame.opcode != expected {
			t.Fatalf("fragment %d changed: %+v %v", index, frame, err)
		}
		if expected == webSocketPing && string(payload) != "ping" {
			t.Fatal("ping payload changed")
		}
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	items := waitWebSocketMessages(t, store, 1)
	detail, err := store.detailWithBodies(items[0].ID)
	if err != nil || !bytes.Equal(detail.RequestBody, text) || detail.Metadata.Opcode != 1 ||
		detail.Metadata.Direction != "client" || detail.Metadata.ParentID == "" || detail.Metadata.ConnectionID != "client-connection" {
		t.Fatalf("fragments did not form one associated text message: %+v %v", detail, err)
	}
	// Stop halfway through one fragmented message, then restart. Its remaining
	// fragment must not create a new record; the next data message is admitted.
	go func() { writeDone <- webSocketTestWrite(client, webSocketBinary, false, false, true, []byte{0, 255}) }()
	if _, _, err := webSocketTestRead(server, true, false); err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	// Receiving the forwarded fragment does not synchronize with the store's
	// later append. Stop only after the prefix under test has been captured.
	partialID := ""
	deadline := time.Now().Add(2 * time.Second)
	for partialID == "" && time.Now().Before(deadline) {
		for _, item := range store.Page(0, "").Items {
			if item.Direction != "client" || item.Opcode != webSocketBinary {
				continue
			}
			detail, err := store.detailWithBodies(item.ID)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(detail.RequestBody, []byte{0, 255}) && !detail.Request.BodyComplete {
				partialID = item.ID
				break
			}
		}
		if partialID == "" {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if partialID == "" {
		t.Fatal("initial fragment was not captured before stopping recording")
	}
	engine.stopRecording()
	config := Config{Version: 3, BodyLimit: 64, Ports: []uint16{80}}
	engine.recording.Store(&config)
	go func() {
		writeDone <- webSocketTestWrite(client, webSocketContinuation, true, false, true, []byte{1, 2})
	}()
	if _, _, err := webSocketTestRead(server, true, false); err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	go func() {
		writeDone <- webSocketTestWrite(server, webSocketBinary, true, false, false, []byte{4, 0, 255})
	}()
	frame, payload, err := webSocketTestRead(client, false, false)
	if err != nil || frame.opcode != webSocketBinary || !bytes.Equal(payload, []byte{4, 0, 255}) {
		t.Fatal("server binary frame changed", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	waitWebSocketMessages(t, store, 3)
	detail, err = store.detailWithBodies(partialID)
	if err != nil || !bytes.Equal(detail.RequestBody, []byte{0, 255}) || detail.Request.BodyComplete {
		t.Fatalf("restarted recording changed a stopped fragmented message: %+v %v", detail, err)
	}
	finishWebSocketTestSession(t, client, server, done)
}

func TestWebSocketCompressedScriptRewriteDropAndInjection(t *testing.T) {
	engine, store := testEngine(t, Config{BodyLimit: -1})
	engine.scripts = scriptProgram(t, `{"stream":[{"type":"websocket","direction":"client","pattern":"^ws://example[.]test/socket$","source":"if ($message.body.indexOf('first:') === 0) $done({body:'changed', inject:[{direction:'server',body:'notice',opcode:1}]}); else if ($message.body === 'drop') $done({drop:true,inject:[{direction:'client',body:'injected',opcode:1}]}); else if ($message.body === 'injected') $done({body:'recursive'}); else $done();"}]}`)
	client, server, done := webSocketTestSession(t, engine, "permessage-deflate")
	compressor := newWebSocketTestDeflater(t)
	first := []byte("first:" + strings.Repeat("original-compressor-history;", 700))
	second := append([]byte("second:"), first[len(first)-15000:]...)
	writeDone := make(chan error, 1)
	encoded := compressor.message(t, first)
	go func() { writeDone <- webSocketTestWrite(client, webSocketText, true, true, true, encoded) }()
	frame, payload, err := webSocketTestRead(server, true, true)
	if err != nil || frame.compressed || string(payload) != "changed" {
		t.Fatalf("compressed rewrite failed: %+v %s %v", frame, payload, err)
	}
	frame, payload, err = webSocketTestRead(client, false, true)
	if err != nil || frame.opcode != webSocketText || string(payload) != "notice" {
		t.Fatal("script server injection failed", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	encoded = compressor.message(t, second)
	go func() { writeDone <- webSocketTestWrite(client, webSocketText, true, true, true, encoded) }()
	frame, payload, err = webSocketTestRead(server, true, true)
	if err != nil || frame.compressed || !bytes.Equal(payload, second) {
		t.Fatalf("rewrite broke the original compression history: %+v %d %v", frame, len(payload), err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	encoded = compressor.message(t, []byte("drop"))
	go func() { writeDone <- webSocketTestWrite(client, webSocketText, true, true, true, encoded) }()
	frame, payload, err = webSocketTestRead(server, true, true)
	if err != nil || string(payload) != "injected" || frame.opcode != 1 {
		t.Fatalf("drop or nonrecursive client injection failed: %+v %s %v", frame, payload, err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	items := waitWebSocketMessages(t, store, 5)
	var dropped, injected int
	for _, item := range items {
		detail, err := store.detailWithBodies(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Metadata.Dropped {
			dropped++
			if string(detail.RequestBody) != "drop" {
				t.Fatal("dropped message payload was not retained")
			}
		}
		if detail.Metadata.Injected {
			injected++
		}
	}
	if dropped != 1 || injected != 2 {
		t.Fatalf("drop/injection metadata lost: dropped=%d injected=%d", dropped, injected)
	}
	finishWebSocketTestSession(t, client, server, done)
}

func TestWebSocketScriptEmptyMessageRewrite(t *testing.T) {
	for _, message := range []struct {
		name   string
		opcode int
		body   []byte
		result string
	}{
		{"text", webSocketText, []byte("replace with an empty message"), "''"},
		{"binary", webSocketBinary, []byte{0, 255, 1, 2}, "new Uint8Array(0)"},
	} {
		for _, transport := range []struct {
			name       string
			extensions string
		}{
			{"plain", ""},
			{"deflate", "permessage-deflate"},
		} {
			t.Run(message.name+"/"+transport.name, func(t *testing.T) {
				engine, store := testEngine(t, Config{BodyLimit: -1})
				engine.scripts = scriptProgram(t, `{"stream":[{"type":"websocket","direction":"client","pattern":"^ws://example[.]test/socket$","binary_body":true,"source":"$done({body:`+message.result+`});"}]}`)
				client, server, done := webSocketTestSession(t, engine, transport.extensions)
				payload := message.body
				compressed := transport.extensions != ""
				if compressed {
					payload = newWebSocketTestDeflater(t).message(t, payload)
				}
				writeDone := make(chan error, 1)
				go func() {
					writeDone <- webSocketTestWrite(client, message.opcode, true, compressed, true, payload)
				}()
				frame, rewritten, err := webSocketTestRead(server, true, compressed)
				if err != nil || !frame.final || frame.opcode != message.opcode || frame.compressed || len(rewritten) != 0 {
					t.Fatalf("nonempty message was not rewritten to an empty data frame: %+v %x %v", frame, rewritten, err)
				}
				if err := <-writeDone; err != nil {
					t.Fatal(err)
				}
				items := waitWebSocketMessages(t, store, 1)
				detail, err := store.detailWithBodies(items[0].ID)
				if err != nil || len(detail.RequestBody) != 0 || !detail.Request.BodyComplete || detail.Metadata.Dropped {
					t.Fatalf("empty rewritten message was not recorded as a forwarded complete message: %+v %v", detail, err)
				}
				finishWebSocketTestSession(t, client, server, done)
			})
		}
	}
}

func TestWebSocketScriptLimitsFollowShrinkingChain(t *testing.T) {
	for _, limits := range []struct {
		name     string
		section  int64
		first    int64
		second   int64
		expected string
	}{
		{"bounded", 0, 8, 2, "xy"},
		{"unlimited_zero", 0, 0, 2, "xy"},
		{"unlimited_override", 2, -1, 2, "xy"},
		{"inherited", 8, 0, 2, "xy"},
		{"first_limit_preserves_original", 0, 7, 2, "original"},
		{"later_limit_preserves_original", 0, 8, 1, "original"},
	} {
		for _, transport := range []struct {
			name       string
			extensions string
		}{
			{"plain", ""},
			{"deflate", "permessage-deflate"},
		} {
			t.Run(limits.name+"/"+transport.name, func(t *testing.T) {
				engine, store := testEngine(t, Config{BodyLimit: -1})
				// Disabled and nonmatching narrow rules cannot constrain loading.
				engine.scripts = scriptProgram(t, fmt.Sprintf(`{"max_body_bytes":%d,"stream":[
{"type":"websocket","direction":"client","max_body_bytes":1,"disabled":true,"source":"throw Error('disabled rule executed');"},
{"type":"websocket","direction":"client","max_body_bytes":1,"pattern":"^ws://different[.]test/","source":"throw Error('unmatched rule executed');"},
{"type":"websocket","direction":"client","max_body_bytes":%d,"source":"if ($message.body !== 'original') throw Error('wrong original body'); $done({body:'ok'});"},
{"type":"websocket","direction":"client","max_body_bytes":%d,"source":"if ($message.body !== 'ok') throw Error('wrong preceding result'); $done({body:'xy'});"}
]}`, limits.section, limits.first, limits.second))
				client, server, done := webSocketTestSession(t, engine, transport.extensions)
				payload := []byte("original")
				compressed := transport.extensions != ""
				if compressed {
					payload = newWebSocketTestDeflater(t).message(t, payload)
				}
				expectedWire := []byte(limits.expected)
				preserved := limits.expected == "original"
				if compressed && preserved {
					expectedWire = payload
				}
				writeDone := make(chan error, 1)
				go func() {
					writeDone <- webSocketTestWrite(client, webSocketText, true, compressed, true, payload)
				}()
				frame, result, err := webSocketTestRead(server, true, compressed)
				if err != nil || !frame.final || frame.opcode != webSocketText ||
					frame.compressed != (compressed && preserved) || !bytes.Equal(result, expectedWire) {
					t.Fatalf("stream rule limits changed chain behavior: %+v %x %v", frame, result, err)
				}
				if err := <-writeDone; err != nil {
					t.Fatal(err)
				}
				items := waitWebSocketMessages(t, store, 1)
				detail, err := store.detailWithBodies(items[0].ID)
				if err != nil || string(detail.RequestBody) != limits.expected || !detail.Request.BodyComplete || detail.Metadata.Dropped {
					t.Fatalf("script chain result was not recorded completely: %+v %v", detail, err)
				}
				if preserved && !strings.Contains(detail.Note, "exceeds") {
					t.Fatal("configured limit fallback did not explain why the original was preserved")
				}
				finishWebSocketTestSession(t, client, server, done)
			})
		}
	}
}

func TestWebSocketManualInjectionWaitsForFragmentBoundary(t *testing.T) {
	engine, store := testEngine(t, Config{})
	client, server, done := webSocketTestSession(t, engine, "")
	writeDone := make(chan error, 1)
	go func() { writeDone <- webSocketTestWrite(client, webSocketText, false, false, true, []byte("start")) }()
	if _, _, err := webSocketTestRead(server, true, false); err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := engine.InjectStream(ctx, "00112233445566778899aabbccddeeff", "client", 1, []byte("cancelled"))
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued injection did not cancel at the fragment boundary: %v", err)
	}
	injected := make(chan error, 1)
	go func() {
		injected <- engine.InjectStream(context.Background(), "00112233445566778899aabbccddeeff", "client", 1, []byte("injected"))
	}()
	go func() {
		writeDone <- webSocketTestWrite(client, webSocketContinuation, true, false, true, []byte("end"))
	}()
	frame, payload, err := webSocketTestRead(server, true, false)
	if err != nil || frame.opcode != webSocketContinuation || string(payload) != "end" {
		t.Fatal("manual injection split a fragmented message", err)
	}
	frame, payload, err = webSocketTestRead(server, true, false)
	if err != nil || frame.opcode != webSocketText || string(payload) != "injected" {
		t.Fatal("manual injection was not sent after FIN", err)
	}
	if err := <-injected; err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	waitWebSocketMessages(t, store, 2)
	finishWebSocketTestSession(t, client, server, done)
}

func TestWebSocketInvalidFrameClosesWithProtocolError(t *testing.T) {
	engine, _ := testEngine(t, Config{})
	client, server, done := webSocketTestSession(t, engine, "")
	writeDone := make(chan error, 1)
	go func() {
		_, err := client.Write([]byte{0x81, 0}) // Client data frames must be masked.
		writeDone <- err
	}()
	readDone := make(chan error, 1)
	go func() {
		frame, payload, err := webSocketTestRead(server, true, false)
		if err == nil && (frame.opcode != webSocketClose || len(payload) != 2 || binary.BigEndian.Uint16(payload) != 1002) {
			err = errors.New("upstream did not receive protocol-error close")
		}
		readDone <- err
	}()
	frame, payload, err := webSocketTestRead(client, false, false)
	if err != nil || frame.opcode != webSocketClose || len(payload) != 2 || binary.BigEndian.Uint16(payload) != 1002 {
		t.Fatal("client did not receive protocol-error close", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-readDone; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("invalid frame completed as a successful session")
	}
}

func TestWebSocketStalledFrameInjectionCancelsWithoutClosingStream(t *testing.T) {
	engine, store := testEngine(t, Config{})
	client, server, done := webSocketTestSession(t, engine, "")
	// Declare a real masked binary frame, but withhold its four payload bytes.
	// The forwarding pump holds that direction's write admission while waiting
	// for the missing bytes, so cancellation must also cover acquiring it.
	writeDone := make(chan error, 1)
	go func() {
		_, err := client.Write([]byte{0x82, 0x84, 1, 2, 3, 4})
		writeDone <- err
	}()
	frame, err := readWebSocketFrame(server, true, false)
	if err != nil || frame.length != 4 || frame.opcode != webSocketBinary {
		t.Fatal("stalled frame header did not reach the original route", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	injected := make(chan error, 1)
	go func() {
		injected <- engine.InjectStream(ctx, "00112233445566778899aabbccddeeff", "client", 1, []byte("cancelled"))
	}()
	select {
	case err := <-injected:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stalled-frame injection cancellation returned %v", err)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("injection cancellation waited for the source frame payload")
	}
	cancel()
	// The opposite direction stays usable while the first frame is still held.
	go func() { writeDone <- webSocketTestWrite(server, webSocketText, true, false, false, []byte("opposite")) }()
	opposite, payload, err := webSocketTestRead(client, false, false)
	if err != nil || opposite.opcode != webSocketText || string(payload) != "opposite" {
		t.Fatal("queued injection cancellation closed the opposite direction", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	go func() {
		_, err := client.Write([]byte{0 ^ 1, 255 ^ 2, 1 ^ 3, 2 ^ 4})
		writeDone <- err
	}()
	var decoded bytes.Buffer
	if err := readWebSocketPayload(context.Background(), server, frame, nil, &decoded); err != nil ||
		!bytes.Equal(decoded.Bytes(), []byte{0, 255, 1, 2}) {
		t.Fatal("cancelled injection corrupted the original stalled frame", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	waitWebSocketMessages(t, store, 2)
	finishWebSocketTestSession(t, client, server, done)
}
