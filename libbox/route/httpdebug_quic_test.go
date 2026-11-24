//go:build with_quic

package route

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-box/experimental/httpdebug"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

type captureQUICOutbound struct {
	adapter.Outbound
	origin   *net.UDPAddr
	metadata chan adapter.InboundContext
	closed   chan struct{}
}

func (o *captureQUICOutbound) Tag() string  { return "selected-udp-group" }
func (o *captureQUICOutbound) Type() string { return "direct" }

func (o *captureQUICOutbound) NewPacketConnection(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	o.metadata <- metadata
	go func() {
		defer onClose(nil)
		defer close(o.closed)
		defer conn.Close()
		raw, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			return
		}
		defer raw.Close()
		stop := context.AfterFunc(ctx, func() { _ = raw.Close(); _ = conn.Close() })
		defer stop()
		packets := bufio.NewNetPacketConn(conn)
		finished := make(chan struct{}, 2)
		go func() {
			defer func() { finished <- struct{}{} }()
			data := make([]byte, 65535)
			for {
				n, _, err := packets.ReadFrom(data)
				if err != nil {
					return
				}
				if _, err = raw.WriteTo(data[:n], o.origin); err != nil {
					return
				}
			}
		}()
		go func() {
			defer func() { finished <- struct{}{} }()
			data := make([]byte, 65535)
			for {
				n, address, err := raw.ReadFrom(data)
				if err != nil {
					return
				}
				if _, err = packets.WriteTo(data[:n], address); err != nil {
					return
				}
			}
		}()
		<-finished
		_ = raw.Close()
		_ = conn.Close()
		<-finished
	}()
}

func captureQUICCertificates(t *testing.T) (tls.Certificate, *x509.CertPool, string, *ecdsa.PrivateKey, *x509.CertPool) {
	t.Helper()
	originKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	originPEM, err := httpdebug.GenerateCA(originKey)
	if err != nil {
		t.Fatal(err)
	}
	originCA, err := httpdebug.ParseCA(originPEM)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{SerialNumber: big.NewInt(2), DNSNames: []string{"origin.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, leaf, originCA, &originKey.PublicKey, originKey)
	if err != nil {
		t.Fatal(err)
	}
	originRoots := x509.NewCertPool()
	originRoots.AddCert(originCA)
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pem, err := httpdebug.GenerateCA(key)
	if err != nil {
		t.Fatal(err)
	}
	clientRoots := x509.NewCertPool()
	if !clientRoots.AppendCertsFromPEM([]byte(pem)) {
		t.Fatal("invalid MITM root")
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: originKey}, originRoots, pem, key, clientRoots
}

func prepareCaptureQUIC(t *testing.T, directory string, pem string, signer *ecdsa.PrivateKey) {
	t.Helper()
	prepared, err := httpdebug.PrepareProcessing(directory, &httpdebug.MITMConfig{Enabled: true, H3: true, BlockQUIC: true,
		Hosts: []string{"origin.test"}, Ports: []uint16{443}, Packages: []string{"com.test"}}, nil, nil, pem, signer, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	httpdebug.ActivateProcessing(prepared, nil, nil)
	t.Cleanup(httpdebug.StopService)
	if err = httpdebug.Start(directory, `{"ports":[443],"bodyLimit":-1}`, "", nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(httpdebug.Stop)
}

func testBboxHTTPDebugQUIC(t *testing.T, directory string) {
	t.Run("HTTP3-route-and-connection", func(t *testing.T) {
		testBboxHTTPDebugQUICRoute(t, directory)
	})
	t.Run("QUIC-unmatched-replay", func(t *testing.T) {
		testBboxHTTPDebugQUICReplay(t, directory)
	})
}

func testBboxHTTPDebugQUICRoute(t *testing.T, directory string) {
	certificate, originRoots, pem, signer, clientRoots := captureQUICCertificates(t)
	rawOrigin, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer rawOrigin.Close()
	origin := &http3.Server{TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}, NextProtos: []string{"h3"}},
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor != 3 || r.Host != "origin.test" {
				t.Errorf("UDP route did not reach a real HTTP/3 origin: %s %s", r.Proto, r.Host)
			}
			_, _ = io.Copy(w, r.Body)
		})}
	defer origin.Close()
	go func() { _ = origin.Serve(rawOrigin) }()
	prepareCaptureQUIC(t, directory, pem, signer)
	metadata := adapter.InboundContext{Network: "udp", Inbound: "tun", Domain: "dns-mapping.test",
		Source: M.ParseSocksaddr("192.0.2.2:4321"), Destination: M.ParseSocksaddr("198.51.100.9:9443"),
		RouteOriginalDestination: M.ParseSocksaddr("198.51.100.9:443"), RouteOutbound: "selected-udp-group", RouteRule: "unchanged-udp-rule",
		ProcessInfo: &adapter.ConnectionOwner{UserId: 12345, PackageNames: []string{"com.test"}}}
	outbound := &captureQUICOutbound{origin: rawOrigin.LocalAddr().(*net.UDPAddr), metadata: make(chan adapter.InboundContext, 2), closed: make(chan struct{})}
	router := &Router{ctx: service.ContextWith[adapter.CertificateStore](context.Background(), &captureTestRoots{roots: originRoots})}
	manager := trafficcontrol.NewManager()
	defer manager.CloseAllConnections()
	clientPackets, inbound := newDebugPacketPipe(metadata.Source, metadata.RouteOriginalDestination)
	defer clientPackets.Close()
	defer inbound.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	selected := make(chan error, 1)
	go func() {
		buffers, selection, rejected := debugObserveQUIC(ctx, inbound, metadata, nil)
		if rejected || selection == nil {
			N.ReleaseMultiPacketBuffer(buffers)
			selected <- errors.New("real QUIC HTTP/3 ClientHello was blocked or not selected")
			_ = inbound.Close()
			return
		}
		// This is the same ordered replay performed by the patched route.go.
		for index := len(buffers) - 1; index >= 0; index-- {
			buffer := buffers[index]
			inbound = bufio.NewCachedPacketConn(inbound, buffer.Buffer, buffer.Destination)
			N.PutPacketBuffer(buffer)
		}
		metadata.OutboundChain = []adapter.Outbound{outbound}
		inbound = manager.RoutedPacketConnection(ctx, inbound, metadata, nil, outbound)
		if !router.routeQUICDebug(ctx, inbound, metadata, outbound, selection, nil) {
			selected <- errors.New("tracked HTTP/3 packet connection was not handled")
			_ = inbound.Close()
			return
		}
		selected <- nil
	}()
	qt := &quic.Transport{Conn: clientPackets}
	defer func() { _ = clientPackets.Close(); _ = qt.Close() }()
	conn, err := qt.Dial(ctx, metadata.RouteOriginalDestination, &tls.Config{RootCAs: clientRoots, ServerName: "origin.test", MinVersion: tls.VersionTLS13,
		NextProtos: []string{http3.NextProtoH3}}, &quic.Config{HandshakeIdleTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal("HTTP/3 handshake failed after replaying the observed Initial packets", err)
	}
	defer func() { _ = clientPackets.Close(); _ = conn.CloseWithError(0, "") }()
	if err = <-selected; err != nil {
		t.Fatal(err)
	}
	h3 := &http3.Transport{DisableCompression: true}
	defer h3.Close()
	client := &http.Client{Transport: h3.NewClientConn(conn), Timeout: 4 * time.Second}
	for _, path := range []string{"first", "second"} {
		payload := strings.Repeat(path, 8192)
		response, err := client.Post("https://origin.test/quic-routed/"+path, "application/octet-stream", strings.NewReader(payload))
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err != nil || string(body) != payload || response.ProtoMajor != 3 {
			t.Fatal("HTTP/3 routed body lost bytes", response.Proto, len(body), err)
		}
	}
	seen := <-outbound.metadata
	if seen.Destination != metadata.Destination || seen.RouteOriginalDestination != metadata.RouteOriginalDestination || seen.Domain != metadata.Domain ||
		seen.RouteOutbound != metadata.RouteOutbound || seen.RouteRule != metadata.RouteRule || seen.Network != "udp" || seen.ProcessInfo.UserId != 12345 ||
		len(seen.ProcessInfo.PackageNames) != 1 || seen.ProcessInfo.PackageNames[0] != "com.test" {
		t.Fatalf("HTTP/3 capture changed the exact UDP route: %+v", seen)
	}
	connections := manager.Connections()
	if len(connections) != 1 {
		t.Fatal("original UDP flow was not tracked", len(connections))
	}
	connectionID := connections[0].ID
	httpdebug.StopService()
	// Closing the engine must wait for the selected route's onClose callback.
	select {
	case <-outbound.closed:
	default:
		t.Fatal("HTTP/3 service close returned before its UDP route stopped")
	}
	store, err := httpdebug.Saved()
	if err != nil {
		t.Fatal(err)
	}
	items := store.Page(10, "/quic-routed/").Items
	if len(items) != 2 || items[0].ID == items[1].ID {
		t.Fatal("HTTP/3 did not retain separate requests on the same connection", items)
	}
	for _, item := range items {
		detail, err := store.Detail(item.ID)
		if err != nil || detail.Metadata.ConnectionID != connectionID.String() || detail.Metadata.Port != 443 || detail.Metadata.Host != "origin.test" ||
			detail.Metadata.Outbound != "selected-udp-group" || detail.Metadata.Rule != "unchanged-udp-rule" ||
			detail.Metadata.TLS == nil || detail.Metadata.TLS.ALPN != "h3" || detail.Metadata.TLS.UpstreamALPN == nil || *detail.Metadata.TLS.UpstreamALPN != "h3" {
			t.Fatalf("HTTP/3 did not retain the original tracker/port and two TLS legs: %+v %v", detail, err)
		}
	}
}

func testBboxHTTPDebugQUICReplay(t *testing.T, directory string) {
	_, _, pem, signer, _ := captureQUICCertificates(t)
	prepareCaptureQUIC(t, directory, pem, signer)
	for _, test := range []struct{ name, sni, protocol string }{
		{"unmatched-name", "outside.test", "h3"},
		{"non-HTTP-QUIC", "origin.test", "raw-quic"},
	} {
		t.Run(test.name, func(t *testing.T) {
			metadata := adapter.InboundContext{Network: "udp", Domain: "dns-mapping.test", Source: M.ParseSocksaddr("192.0.2.2:4321"),
				Destination: M.ParseSocksaddr("198.51.100.9:443"), ProcessInfo: &adapter.ConnectionOwner{UserId: 12345, PackageNames: []string{"com.test"}}}
			client, inbound := newDebugPacketPipe(metadata.Source, metadata.Destination)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			qt := &quic.Transport{Conn: client}
			dialed := make(chan struct{})
			go func() {
				_, _ = qt.Dial(ctx, metadata.Destination, &tls.Config{ServerName: test.sni, InsecureSkipVerify: true,
					NextProtos: []string{test.protocol}}, &quic.Config{})
				close(dialed)
			}()
			defer func() { cancel(); _ = client.Close(); _ = inbound.Close(); _ = qt.Close(); <-dialed }()
			buffers, selection, rejected := debugObserveQUIC(ctx, inbound, metadata, nil)
			if selection != nil || rejected || len(buffers) == 0 {
				N.ReleaseMultiPacketBuffer(buffers)
				t.Fatal("unmatched QUIC was intercepted, blocked or consumed")
			}
			original := make([][]byte, len(buffers))
			for index, packet := range buffers {
				original[index] = append([]byte(nil), packet.Buffer.Bytes()...)
				if packet.Destination != metadata.Destination {
					t.Fatal("QUIC sniffing changed a buffered destination")
				}
			}
			for index := len(buffers) - 1; index >= 0; index-- {
				packet := buffers[index]
				inbound = bufio.NewCachedPacketConn(inbound, packet.Buffer, packet.Destination)
				N.PutPacketBuffer(packet)
			}
			for _, expected := range original {
				actual := make([]byte, 65535)
				n, destination, err := bufio.NewNetPacketConn(inbound).ReadFrom(actual)
				if err != nil || !bytes.Equal(actual[:n], expected) || M.SocksaddrFromNet(destination) != metadata.Destination {
					t.Fatal("unmatched QUIC changed datagram bytes, order or destination", err)
				}
			}
		})
	}
}
