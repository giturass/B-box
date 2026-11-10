package route

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-box/experimental/httpdebug"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

type captureTestOutbound struct {
	adapter.Outbound
	metadata chan adapter.InboundContext
}

func TestBboxHTTPDebugSniffGrowsAndReplaysLargeHeaders(t *testing.T) {
	client, inbound := net.Pipe()
	defer client.Close()
	defer inbound.Close()
	metadata := adapter.InboundContext{}
	payload := "GET /large HTTP/1.1\r\nX-Large: " + strings.Repeat("x", 128*1024) + "\r\nHost: large.test\r\n\r\n"
	go func() { _, _ = io.WriteString(client, payload) }()
	replay, err := peekHTTPDebug(context.Background(), &metadata, inbound)
	if err != nil || metadata.Protocol != "http" || metadata.Domain != "large.test" {
		t.Fatal("sniff buffer still limits valid request headers", metadata, err)
	}
	got := make([]byte, len(payload))
	if _, err = io.ReadFull(replay, got); err != nil || string(got) != payload {
		t.Fatal("growing the sniff buffer lost bytes", err)
	}
}

func (o *captureTestOutbound) Tag() string  { return "selected-group" }
func (o *captureTestOutbound) Type() string { return "direct" }

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
	if err := httpdebug.Start(directory, `{}`, "", nil); err != nil {
		t.Fatal(err)
	}
	defer httpdebug.StopService()
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
	saved, err := httpdebug.Saved()
	if err != nil {
		t.Fatal(err)
	}
	items := saved.Page(10, "observed.example").Items
	if len(items) != 1 {
		t.Fatalf("untracked HTTP was not retained: %+v", items)
	}
	detail, err := saved.Detail(items[0].ID)
	if err != nil || detail.Metadata.ConnectionID != "" {
		t.Fatalf("untracked HTTP gained a connection ID: %+v %v", detail, err)
	}
	// All capture sessions share one private store, including across restarts.
	t.Run("connection-ID", func(t *testing.T) {
		testBboxHTTPDebugConnectionID(t, directory)
	})
	for _, bypass := range []bool{false, true} {
		t.Run(map[bool]string{false: "MITM", true: "unselected-TLS"}[bypass], func(t *testing.T) {
			testBboxTLSRoute(t, directory, bypass)
		})
	}
}

func testBboxHTTPDebugConnectionID(t *testing.T, directory string) {
	t.Helper()
	if err := httpdebug.Start(directory, `{}`, "", nil); err != nil {
		t.Fatal(err)
	}
	defer httpdebug.Stop()
	manager := trafficcontrol.NewManager()
	defer manager.CloseAllConnections()
	outbound := &captureTestOutbound{metadata: make(chan adapter.InboundContext, 2)}
	metadata := adapter.InboundContext{Network: "tcp", Inbound: "tun",
		Source: M.ParseSocksaddr("192.0.2.2:4321"), Destination: M.ParseSocksaddr("198.51.100.9:80"),
		RouteOutbound: outbound.Tag(), OutboundChain: []adapter.Outbound{outbound}}
	client, inbound := net.Pipe()
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	tracked := manager.RoutedConnection(context.Background(), inbound, metadata, nil, outbound)
	connections := manager.Connections()
	if len(connections) != 1 {
		t.Fatal("original connection was not tracked")
	}
	connectionID := connections[0].ID
	accepted := make(chan bool, 1)
	go func() {
		// No protocol hint: routeHTTPDebug adds a sniff cache above the tracker.
		router := &Router{ctx: context.Background()}
		_, captured := router.routeHTTPDebug(context.Background(), tracked, metadata, outbound, nil)
		accepted <- captured
		if !captured {
			tracked.Close()
		}
	}()
	reader := bufio.NewReader(client)
	for _, path := range []string{"first", "second"} {
		request, err := http.NewRequest("GET", "http://tracked.test/"+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err = request.Write(client); err != nil {
			t.Fatal(err)
		}
		response, err := http.ReadResponse(reader, request)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || string(body) != "ok" {
			t.Fatalf("HTTP/1.1 connection reuse failed: %q %v", body, err)
		}
	}
	if !<-accepted {
		t.Fatal("tracked connection was not captured")
	}
	manager.CloseAllConnections()
	httpdebug.StopService()
	manager.Clear()
	if manager.Connection(connectionID) != nil || len(manager.ClosedConnections()) != 0 {
		t.Fatal("original connection was not removed from tracking")
	}
	// Capture history must outlive both connection tracking and the engine.
	saved, err := httpdebug.OpenStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	items := saved.Page(10, "tracked.test").Items
	if len(items) != 2 || items[0].ID == items[1].ID {
		t.Fatalf("requests on one connection need separate IDs: %+v", items)
	}
	for _, item := range items {
		detail, err := saved.Detail(item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Metadata.ConnectionID != connectionID.String() || detail.ID == connectionID.String() ||
			!detail.Complete || detail.ResponsePreview.Encoding != "text" || detail.ResponsePreview.Text != "ok" {
			t.Fatalf("original connection association or retained request lost: %+v", detail)
		}
	}
}

type captureTestRoots struct {
	adapter.CertificateStore
	roots *x509.CertPool
}

func (s *captureTestRoots) Pool() *x509.CertPool { return s.roots }

type captureTunnelConn struct{ net.Conn }

func (c *captureTunnelConn) ConnHandshakeSuccess(net.Conn) error {
	_, err := io.WriteString(c.Conn, "READY")
	return err
}

type captureTLSOutbound struct {
	adapter.Outbound
	certificate tls.Certificate
	metadata    chan adapter.InboundContext
}

func (o *captureTLSOutbound) Tag() string  { return "selected-group" }
func (o *captureTLSOutbound) Type() string { return "direct" }

func (o *captureTLSOutbound) NewConnection(ctx context.Context, raw net.Conn, m adapter.InboundContext, onClose N.CloseHandlerFunc) {
	o.metadata <- m
	go func() {
		defer onClose(nil)
		defer raw.Close()
		conn := tls.Server(raw, &tls.Config{Certificates: []tls.Certificate{o.certificate}, NextProtos: []string{"http/1.1"}})
		if err := conn.HandshakeContext(ctx); err != nil {
			return
		}
		request, err := http.ReadRequest(bufio.NewReader(conn))
		if err != nil {
			return
		}
		request.Body.Close()
		if request.Host != "origin.test" || request.URL.RequestURI() != "/routed?raw=1;2" {
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Length: 6\r\nConnection: close\r\n\r\nrouted")
	}()
}

func testBboxTLSRoute(t *testing.T, directory string, bypass bool) {
	t.Helper()
	// Upstream trust and MITM trust use separate keys and chains.
	originKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	originPEM, err := httpdebug.GenerateCA(originKey)
	if err != nil {
		t.Fatal(err)
	}
	originCA, err := httpdebug.ParseCA(originPEM)
	if err != nil {
		t.Fatal(err)
	}
	originRoots := x509.NewCertPool()
	originRoots.AddCert(originCA)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"origin.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, originCA, &originKey.PublicKey, originKey)
	if err != nil {
		t.Fatal(err)
	}
	outbound := &captureTLSOutbound{metadata: make(chan adapter.InboundContext, 2),
		certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: originKey}}
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	pem, err := httpdebug.GenerateCA(key)
	if err != nil {
		t.Fatal(err)
	}
	clientRoots := x509.NewCertPool()
	clientRoots.AppendCertsFromPEM([]byte(pem))
	mitm := &httpdebug.MITMConfig{Enabled: true, Hosts: []string{"origin.test"}, Ports: []uint16{443}}
	if bypass {
		mitm.Hosts = []string{"other.test"}
		clientRoots = originRoots
	}
	prepared, err := httpdebug.PrepareProcessing(directory, mitm, nil, nil, pem, key, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	httpdebug.ActivateProcessing(prepared, nil, nil)
	defer httpdebug.StopService()
	if err = httpdebug.Start(directory, `{"ports":[443]}`, "", nil); err != nil {
		t.Fatal(err)
	}
	defer httpdebug.Stop()
	router := &Router{ctx: service.ContextWith[adapter.CertificateStore](context.Background(), &captureTestRoots{roots: originRoots})}
	metadata := adapter.InboundContext{Network: "tcp", Inbound: "mixed", Domain: "dns-mapping.test",
		Source: M.ParseSocksaddr("192.0.2.2:4321"), Destination: M.ParseSocksaddr("198.51.100.9:9443"),
		RouteOriginalDestination: M.ParseSocksaddr("198.51.100.9:443"),
		RouteOutbound:            "selected-group", RouteRule: "unchanged-rule", TLSFragment: true,
		ProcessInfo: &adapter.ConnectionOwner{UserId: 12345, PackageNames: []string{"com.test"}}}
	client, inbound := net.Pipe()
	defer client.Close()
	defer inbound.Close()
	manager := trafficcontrol.NewManager()
	defer manager.CloseAllConnections()
	metadata.OutboundChain = []adapter.Outbound{outbound}
	tracked := manager.RoutedConnection(context.Background(), &captureTunnelConn{inbound}, metadata, nil, outbound)
	connectionID := manager.Connections()[0].ID.String()
	client.SetDeadline(time.Now().Add(5 * time.Second))
	finished := make(chan error, 1)
	go func() {
		ack := make([]byte, 5)
		if _, err := io.ReadFull(client, ack); err != nil {
			finished <- err
			return
		}
		if string(ack) != "READY" {
			finished <- io.ErrUnexpectedEOF
			return
		}
		conn := tls.Client(client, &tls.Config{ServerName: "origin.test", RootCAs: clientRoots, NextProtos: []string{"http/1.1"}})
		if err := conn.Handshake(); err != nil {
			finished <- err
			return
		}
		if _, err := io.WriteString(conn, "GET /routed?raw=1;2 HTTP/1.1\r\nHost: origin.test\r\nConnection: close\r\n\r\n"); err != nil {
			finished <- err
			return
		}
		response, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			finished <- err
			return
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err == nil && string(body) != "routed" {
			err = io.ErrUnexpectedEOF
		}
		finished <- err
	}()
	remaining, captured := router.routeHTTPDebug(context.Background(), tracked, metadata, outbound, nil)
	if captured == bypass {
		t.Fatalf("TLS capture selection: captured=%v bypass=%v", captured, bypass)
	}
	if !captured {
		outbound.NewConnection(context.Background(), remaining, metadata, func(error) {})
	}
	if err = <-finished; err != nil {
		t.Fatal("CONNECT acknowledgment, TLS or original route failed:", err)
	}
	seen := <-outbound.metadata
	if seen.Destination != metadata.Destination || seen.Domain != metadata.Domain || seen.RouteOriginalDestination != metadata.RouteOriginalDestination ||
		seen.RouteOutbound != metadata.RouteOutbound || seen.RouteRule != metadata.RouteRule || !seen.TLSFragment || seen.ProcessInfo.UserId != 12345 {
		t.Fatalf("TLS capture changed routing metadata: %+v", seen)
	}
	if len(outbound.metadata) != 0 {
		t.Fatal("upstream verification discarded its connection")
	}
	httpdebug.Stop()
	if !bypass {
		saved, err := httpdebug.Saved()
		if err != nil {
			t.Fatal(err)
		}
		items := saved.Page(10, "origin.test").Items
		if len(items) != 1 {
			t.Fatalf("wrong MITM records: %+v", items)
		}
		detail, err := saved.Detail(items[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Metadata.TLS == nil || detail.Metadata.TLS.ServerName != "origin.test" ||
			detail.Metadata.ConnectionID != connectionID ||
			detail.Metadata.OriginalDestination != "198.51.100.9:443" || !strings.Contains(detail.Request.URL, "origin.test/routed") {
			t.Fatalf("record lost original destination or actual SNI: %+v", detail)
		}
	}
}
