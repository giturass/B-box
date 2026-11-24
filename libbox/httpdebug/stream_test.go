package httpdebug

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRawTCPCapturesBothDirectionsAndPreservesHalfClose(t *testing.T) {
	e, store := testEngine(t, Config{RawTCP: true, BodyLimit: 7})
	client, inbound, err := mitmTCPPair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	origin, routed, err := mitmTCPPair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	origin.SetDeadline(time.Now().Add(5 * time.Second))
	finished := make(chan error, 1)
	metadata := Metadata{ConnectionID: "raw-client", Host: "origin.test", Port: 80, Protocol: "tcp", RawTCP: true,
		Outbound: "selected", Rule: "preserved"}
	if !e.Handle(context.Background(), inbound, metadata, func(context.Context) (net.Conn, error) { return routed, nil }, nil,
		func(err error) { finished <- err }) {
		t.Fatal("raw TCP was not intercepted")
	}
	greeting := []byte{0, 255, 8, 0}
	payload := bytes.Repeat([]byte{0, 255, 7, 2}, 10000)
	originResult := make(chan error, 1)
	go func() {
		if _, err := origin.Write(greeting); err != nil {
			originResult <- err
			return
		}
		data, err := io.ReadAll(origin)
		if err == nil && !bytes.Equal(data, payload) {
			err = io.ErrUnexpectedEOF
		}
		if err == nil {
			_, err = origin.Write(data)
		}
		if err == nil {
			err = origin.(interface{ CloseWrite() error }).CloseWrite()
		}
		originResult <- err
	}()
	got := make([]byte, len(greeting))
	if _, err := io.ReadFull(client, got); err != nil || !bytes.Equal(got, greeting) {
		t.Fatalf("server-first greeting changed: %v %v", got, err)
	}
	if _, err = client.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err = client.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
		t.Fatal(err)
	}
	got, err = io.ReadAll(client)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("raw TCP response changed: %d %v", len(got), err)
	}
	if err = <-originResult; err != nil {
		t.Fatal(err)
	}
	if err = <-finished; err != nil {
		t.Fatal(err)
	}
	page := store.Page(0, "")
	var parent *Detail
	children := 0
	for _, item := range page.Items {
		detail, err := store.Detail(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Metadata.ParentID == "" {
			parent = detail
		} else {
			children++
			if detail.Metadata.StreamID == "" || !streamDirection(detail.Metadata.Direction) || detail.Metadata.ConnectionID != "raw-client" {
				t.Fatalf("raw chunk lost its parent/route/direction: %+v", detail)
			}
		}
	}
	if parent == nil || children < 3 || parent.Metadata.TLS != nil || parent.Request.Protocol != "TCP" ||
		parent.Request.BodyBytes != int64(len(payload)) || parent.Response.BodyBytes != int64(len(payload)+len(greeting)) ||
		!parent.Request.BodyComplete || !parent.Response.BodyComplete || !parent.Request.BodyTruncated || parent.Metadata.Outbound != "selected" {
		t.Fatalf("wrong raw TCP capture: parent=%+v children=%d", parent, children)
	}
	if _, err := store.Curl(parent.ID); err == nil {
		t.Fatal("raw TCP exported as cURL")
	}
	var har strings.Builder
	if err := store.WriteHAR(&har, ""); err != nil || !strings.Contains(har.String(), `"entries":[]`) {
		t.Fatalf("raw TCP leaked into HAR: %s %v", har.String(), err)
	}
}

func TestTCPStreamScriptRewritesAndInjectsWithoutRecursion(t *testing.T) {
	e, store := testEngine(t, Config{RawTCP: true})
	e.scripts = scriptProgram(t, `{"stream":[{"type":"tcp","direction":"client","pattern":"^tcp://origin.test:80$","binary_body":true,"source":"$done({body:new Uint8Array([10,11]),inject:[{direction:'server',body:new Uint8Array([12])}]});"}]}`)
	client, inbound, err := mitmTCPPair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	origin, routed, err := mitmTCPPair(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer origin.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	origin.SetDeadline(time.Now().Add(5 * time.Second))
	if !e.Handle(context.Background(), inbound, Metadata{Host: "origin.test", Port: 80, Protocol: "tcp", RawTCP: true},
		func(context.Context) (net.Conn, error) { return routed, nil }, nil, nil) {
		t.Fatal("raw TCP was not intercepted")
	}
	if _, err = client.Write([]byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 2)
	if _, err := io.ReadFull(origin, got); err != nil || !bytes.Equal(got, []byte{10, 11}) {
		t.Fatalf("TCP replacement not delivered: %v %v", got, err)
	}
	got = got[:1]
	if _, err := io.ReadFull(client, got); err != nil || got[0] != 12 {
		t.Fatalf("opposite-direction injection not delivered: %v %v", got, err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		for _, item := range store.Page(0, "").Items {
			if item.Injected && item.Complete {
				detail, err := store.detailWithBodies(item.ID)
				if err != nil || !bytes.Equal(detail.ResponseBody, []byte{12}) || detail.Metadata.Direction != "server" {
					t.Fatalf("injection was reprocessed or not recorded: %+v %v", detail, err)
				}
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("injected TCP chunk was not recorded")
}

func TestStreamScriptsKeepMessageIdentityAcrossRules(t *testing.T) {
	e, _ := testEngine(t, Config{})
	e.scripts = scriptProgram(t, `{"stream":[
		{"type":"tcp","source":"if (!$message.id || $message.id === $message.parentId) throw new Error('message ID missing'); $done({body:$message.id});"},
		{"type":"tcp","source":"if ($message.body !== $message.id || $message.parentId !== 'parent') throw new Error('message identity changed'); $done();"}
	]}`)
	metadata := Metadata{Host: "origin.test", Port: 80, Protocol: "tcp", ParentID: "parent"}
	first, err := e.processStreamMessage(context.Background(), metadata, "tcp", "client", 0, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.processStreamMessage(context.Background(), metadata, "tcp", "client", 0, []byte("payload"))
	if err != nil || bytes.Equal(first.body, second.body) {
		t.Fatalf("different TCP chunks shared one message ID: %q %q %v", first.body, second.body, err)
	}
	metadata.messageID = "captured-message"
	captured, err := e.processStreamMessage(context.Background(), metadata, "tcp", "client", 0, []byte("payload"))
	if err != nil || string(captured.body) != metadata.messageID {
		t.Fatalf("script lost the capture message ID: %q %v", captured.body, err)
	}
}

func TestStreamScriptsValidateDropAbortAndCancellation(t *testing.T) {
	for _, invalid := range []string{
		`{"stream":[{"type":"udp","source":"$done();"}]}`,
		`{"stream":[{"type":"tcp","direction":"both","source":"$done();"}]}`,
		`{"stream":[{"type":"tcp","timeout_ms":-2,"source":"$done();"}]}`,
		`{"stream":[{"type":"tcp","max_body_bytes":-2,"source":"$done();"}]}`,
	} {
		var config ScriptsConfig
		if err := json.Unmarshal([]byte(invalid), &config); err == nil {
			t.Fatalf("invalid stream options accepted: %s", invalid)
		}
	}
	metadata := Metadata{Host: "origin.test", Port: 80, Protocol: "tcp", RawTCP: true}
	e, _ := testEngine(t, Config{})
	e.scripts = scriptProgram(t, `{"stream":[{"type":"tcp","source":"$done({drop:true});"}]}`)
	result, err := e.processStreamMessage(context.Background(), metadata, "tcp", "client", 0, []byte("payload"))
	if err != nil || !result.drop {
		t.Fatalf("drop failed: %+v %v", result, err)
	}
	e.scripts = scriptProgram(t, `{"stream":[{"type":"tcp","source":"$done({abort:true});"}]}`)
	if _, err = e.processStreamMessage(context.Background(), metadata, "tcp", "client", 0, []byte("payload")); !errors.Is(err, errScriptAbort) {
		t.Fatalf("abort did not terminate stream processing: %v", err)
	}
	e.scripts = scriptProgram(t, `{"stream":[{"type":"tcp","source":"$done({inject:[{body:'missing-direction'}]});"}]}`)
	if _, err = e.processStreamMessage(context.Background(), metadata, "tcp", "client", 0, nil); err == nil {
		t.Fatal("injection without direction accepted")
	}
	e.scripts = scriptProgram(t, `{"stream":[{"type":"tcp","timeout_ms":-1,"source":"while(true){}"}]}`)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, err := e.processStreamMessage(ctx, metadata, "tcp", "client", 0, []byte("payload"))
		finished <- err
	}()
	deadline := time.Now().Add(time.Second)
	for streamScriptsRunning.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("cancelled stream script succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("stream script ignored cancellation")
	}
}

type streamTestWriteConn struct {
	net.Conn
	started chan struct{}
	once    sync.Once
}

func (c *streamTestWriteConn) Write(data []byte) (int, error) {
	c.once.Do(func() { close(c.started) })
	return c.Conn.Write(data)
}

func TestStreamWriterCancelledInjectionRestoresWriteDeadline(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()
	conn := &streamTestWriteConn{Conn: client, started: make(chan struct{})}
	writer := newStreamWriter(conn)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := writer.write(ctx, []byte("blocked")); finished <- err }()
	<-conn.started
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled write returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked injection write ignored cancellation")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { _, err := writer.write(ctx, []byte("next")); finished <- err }()
	got := make([]byte, 4)
	if _, err := io.ReadFull(server, got); err != nil || string(got) != "next" {
		t.Fatalf("cancelled injection poisoned next write: %q %v", got, err)
	}
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
}

func TestStreamInjectionTargetsActiveEngineAndCancelsWrite(t *testing.T) {
	e, _ := testEngine(t, Config{})
	id := newStreamID()
	started := make(chan struct{})
	unregister := e.registerStream(id, "tcp", func(ctx context.Context, direction string, data []byte, opcode int, injected bool) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- e.InjectStream(ctx, id, "client", 0, []byte{0, 255}) }()
	<-started
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("injection ignored cancellation: %v", err)
	}
	unregister()
	if err := e.InjectStream(context.Background(), id, "server", 0, nil); err == nil {
		t.Fatal("closed stream was revived by injection")
	}
	e.Close()
	if err := e.InjectStream(context.Background(), newStreamID(), "server", 0, nil); err == nil {
		t.Fatal("stopped engine accepted injection")
	}
}
