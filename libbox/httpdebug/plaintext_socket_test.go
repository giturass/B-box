package httpdebug

import (
	"strings"
	"testing"
)

func TestPlaintextSocketGenerationsAndLateMetadataNeverGuessRoutes(t *testing.T) {
	engine, store := testEngine(t, Config{HTTPS: true})
	for _, id := range []string{"first", "second"} {
		if err := engine.passive.accept(plaintextEvent{Type: "open", ID: id, PID: 42, UID: 10001, Adapter: "Go TLS"}); err != nil {
			t.Fatal(err)
		}
		feedPlaintext(t, engine, id, "write", []byte("GET /"+id+" HTTP/1.1\r\nHost: api.test:8443\r\n\r\n"), 3)
		feedPlaintext(t, engine, id, "read", []byte("HTTP/1.1 204 No Content\r\n\r\n"), 4)
		closePlaintext(t, engine, id)
	}
	items := waitComplete(t, store, 2)
	for index, id := range []string{"first", "second"} {
		socket := &SocketMetadata{FD: 0, Inode: string(rune('a' + index)), NetNS: 1, Source: "127.0.0.1:1234", Destination: "127.0.0.1:443"}
		if index == 1 {
			socket.Source = "[::1]:5678"
			socket.Destination = "[::1]:9443"
		}
		if err := engine.passive.accept(plaintextEvent{Type: "socket", ID: id, Socket: socket}); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range items {
		detail, _ := store.Detail(item.ID)
		metadata := detail.Metadata
		if metadata.Outbound != "" || metadata.Rule != "" || metadata.Port != 8443 || metadata.Socket == nil || metadata.Socket.FD != 0 || metadata.TLSAdapter != "Go TLS" {
			t.Fatalf("socket replaced HTTP authority or invented route: %+v", metadata)
		}
		if metadata.CaptureID == "first" && metadata.Source != "127.0.0.1:1234" || metadata.CaptureID == "second" && metadata.Source != "[::1]:5678" {
			t.Fatalf("FD reuse crossed TLS generations: %+v", metadata)
		}
	}
	if err := engine.passive.accept(plaintextEvent{Type: "socket", ID: "first", SocketConflict: true}); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		detail, _ := store.Detail(item.ID)
		if detail.Metadata.CaptureID == "first" && (detail.Metadata.Socket != nil || detail.Metadata.Source != "" || !strings.Contains(detail.Note, "socket endpoints and sing-box route are unavailable")) {
			t.Fatal("ambiguous association remained visible")
		}
	}
	if engine.parsedRequests.Load() != 2 || engine.recordedRequests.Load() != 2 || engine.goOpened.Load() != 2 || engine.plaintextConnections.Load() != 2 {
		t.Fatal("diagnostics confused observed, plaintext and parsed stages")
	}
}
