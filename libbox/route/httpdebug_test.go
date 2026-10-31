package route

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/experimental/httpdebug"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type captureTestOutbound struct {
	adapter.Outbound
	metadata chan adapter.InboundContext
}

func (o *captureTestOutbound) NewConnection(ctx context.Context, conn net.Conn, m adapter.InboundContext, onClose N.CloseHandlerFunc) {
	o.metadata <- m
	go func() {
		defer conn.Close()
		defer onClose(nil)
		request, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return
		}
		request.Body.Close()
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok")
	}()
}

func TestBboxHTTPDebugKeepsSelectedRouteAndReplaysUnmatchedBytes(t *testing.T) {
	directory := t.TempDir()
	if err := httpdebug.Start(directory, `{}`, "", ""); err != nil {
		t.Fatal(err)
	}
	defer httpdebug.Stop()
	router := &Router{ctx: context.Background()}
	metadata := adapter.InboundContext{Network: "tcp", Inbound: "tun", Domain: "dns-mapping.example",
		Source: M.ParseSocksaddr("127.0.0.1:4321"), Destination: M.ParseSocksaddr("198.51.100.9:80"),
		RouteOutbound: "selected-group", RouteRule: "unchanged-rule", TLSFragment: true,
		ProcessInfo: &adapter.ConnectionOwner{UserId: 12345, PackageNames: []string{"com.test"}}}
	outbound := &captureTestOutbound{metadata: make(chan adapter.InboundContext, 1)}
	client, inbound := net.Pipe()
	defer client.Close()
	go func() {
		_, _ = io.WriteString(client, "GET /path HTTP/1.1\r\nHost: observed.example\r\nConnection: close\r\n\r\n")
	}()
	_, captured := router.routeHTTPDebug(context.Background(), inbound, metadata, outbound, nil)
	if !captured {
		t.Fatal("HTTP stream was not captured")
	}
	response, err := http.ReadResponse(bufio.NewReader(client), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "ok" {
		t.Fatal("original outbound did not carry the response")
	}
	seen := <-outbound.metadata
	if seen.Destination != metadata.Destination || seen.Domain != metadata.Domain || seen.RouteOutbound != metadata.RouteOutbound ||
		seen.RouteRule != metadata.RouteRule || !seen.TLSFragment || seen.ProcessInfo.UserId != 12345 {
		t.Fatalf("capture changed the selected route: %+v", seen)
	}
	if !debugNeedsStream(metadata) {
		t.Fatal("selected TCP still used the fast flow path")
	}

	// Non-HTTP traffic on a selected port must be byte-for-byte transparent.
	rawClient, rawInbound := net.Pipe()
	defer rawClient.Close()
	defer rawInbound.Close()
	payload := "SSH-2.0-byte-preservation\r\n"
	go func() { _, _ = io.WriteString(rawClient, payload) }()
	raw, captured := router.routeHTTPDebug(context.Background(), rawInbound, metadata, outbound, nil)
	if captured {
		t.Fatal("non-HTTP traffic was intercepted")
	}
	buffer := make([]byte, len(payload))
	if _, err := io.ReadFull(raw, buffer); err != nil || string(buffer) != payload {
		t.Fatalf("sniff lost bytes: %q %v", buffer, err)
	}
	httpdebug.Stop()
	if debugNeedsStream(metadata) {
		t.Fatal("stopped capture changed fast routing")
	}
}
