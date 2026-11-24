package route

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/experimental/httpdebug"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type captureRawTCPOutbound struct {
	adapter.Outbound
	metadata chan adapter.InboundContext
	length   int
}

func (o *captureRawTCPOutbound) Tag() string  { return "selected-raw-group" }
func (o *captureRawTCPOutbound) Type() string { return "direct" }

func (o *captureRawTCPOutbound) NewConnection(_ context.Context, raw net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	o.metadata <- metadata
	go func() {
		defer onClose(nil)
		defer raw.Close()
		data := make([]byte, o.length)
		if _, err := io.ReadFull(raw, data); err == nil {
			_, _ = raw.Write(data)
		}
	}()
}

func testBboxHTTPDebugRawTCP(t *testing.T, directory string) {
	t.Helper()
	if err := httpdebug.Start(directory, `{"ports":[9001],"rawTCP":true,"bodyLimit":-1}`, "", nil); err != nil {
		t.Fatal(err)
	}
	defer httpdebug.StopService()
	metadata := adapter.InboundContext{Network: "tcp", Inbound: "tun", Domain: "raw.test", Source: M.ParseSocksaddr("192.0.2.2:4321"),
		Destination: M.ParseSocksaddr("198.51.100.9:9101"), RouteOriginalDestination: M.ParseSocksaddr("198.51.100.9:9001"),
		RouteOutbound: "selected-raw-group", RouteRule: "unchanged-raw-rule", TLSFragment: true,
		ProcessInfo: &adapter.ConnectionOwner{UserId: 12345, PackageNames: []string{"com.test"}}}
	payload := bytes.Repeat([]byte{0x00, 0xff, 'b', 'i', 'n', '\r', '\n'}, 4096)
	outbound := &captureRawTCPOutbound{metadata: make(chan adapter.InboundContext, 1), length: len(payload)}
	client, inbound := net.Pipe()
	defer client.Close()
	defer inbound.Close()
	_ = client.SetDeadline(time.Now().Add(4 * time.Second))
	go func() { _, _ = client.Write(payload) }()
	router := &Router{ctx: context.Background()}
	_, captured := router.routeHTTPDebug(context.Background(), inbound, metadata, outbound, nil)
	if !captured {
		t.Fatal("opt-in raw TCP did not survive the HTTP sniff fallback")
	}
	actual := make([]byte, len(payload))
	if _, err := io.ReadFull(client, actual); err != nil || !bytes.Equal(actual, payload) {
		t.Fatal("raw TCP lost bytes read ahead by HTTP sniffing", err)
	}
	seen := <-outbound.metadata
	if seen.Domain != metadata.Domain || seen.Destination != metadata.Destination || seen.RouteOriginalDestination != metadata.RouteOriginalDestination ||
		seen.RouteOutbound != metadata.RouteOutbound || seen.RouteRule != metadata.RouteRule || !seen.TLSFragment || seen.ProcessInfo.UserId != 12345 {
		t.Fatalf("raw TCP capture replaced the original outbound metadata: %+v", seen)
	}
	_ = client.Close()
	httpdebug.StopService()
	store, err := httpdebug.Saved()
	if err != nil {
		t.Fatal(err)
	}
	items := store.Page(0, "tcp://raw.test:9001").Items
	parents := 0
	for _, item := range items {
		detail, err := store.Detail(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Metadata.ParentID != "" {
			continue
		}
		parents++
		if !detail.Metadata.RawTCP || detail.Metadata.TLS != nil || detail.Metadata.Port != 9001 ||
			detail.Request.Protocol != "TCP" || detail.Request.BodyBytes != int64(len(payload)) || detail.Response.BodyBytes != int64(len(payload)) {
			t.Fatalf("raw TCP capture lost the original port or fabricated TLS: %+v", detail)
		}
	}
	if parents != 1 {
		t.Fatal("raw TCP did not save exactly one connection record", parents)
	}
	if err := httpdebug.Start(directory, `{"ports":[9001]}`, "", nil); err != nil {
		t.Fatal(err)
	}
	plain, unselected := net.Pipe()
	defer plain.Close()
	defer unselected.Close()
	go func() { _, _ = plain.Write(payload) }()
	replay, captured := router.routeHTTPDebug(context.Background(), unselected, metadata, outbound, nil)
	if captured {
		t.Fatal("raw TCP interception ignored its opt-in switch")
	}
	if _, err := io.ReadFull(replay, actual); err != nil || !bytes.Equal(actual, payload) {
		t.Fatal("unselected raw TCP lost read-ahead bytes", err)
	}
}
