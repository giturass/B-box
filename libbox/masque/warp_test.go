package masque

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	stdlog "log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	transportHTTP "github.com/sagernet/sing-box/transport/http"
	"github.com/sagernet/sing/common/buf"
	N "github.com/sagernet/sing/common/network"
)

func warpTestCredentials(t *testing.T) (option.MASQUEOutboundOptions, *tls.Config) {
	t.Helper()
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalECPrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&serverKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"different-from-the-selected-sni.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &serverKey.PublicKey, serverKey)
	if err != nil {
		t.Fatal(err)
	}
	options := option.MASQUEOutboundOptions{
		PrivateKey:    base64.StdEncoding.EncodeToString(privateDER),
		PeerPublicKey: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER})),
		ServerName:    "www.apple.com",
		Address:       []netip.Prefix{netip.MustParsePrefix("172.16.0.2/32")},
		Version:       2,
	}
	serverTLS := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: serverKey}},
		ClientAuth:   tls.RequireAnyClientCert,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) != 1 {
				return errors.New("missing enrolled client certificate")
			}
			cert, parseErr := x509.ParseCertificate(raw[0])
			if parseErr != nil {
				return parseErr
			}
			public, ok := cert.PublicKey.(*ecdsa.PublicKey)
			if !ok || !public.Equal(&clientKey.PublicKey) {
				return errors.New("wrong enrolled client key")
			}
			return nil
		},
	}
	return options, serverTLS
}

func warpTestHTTP2Server(t *testing.T, handler http.HandlerFunc) option.MASQUEOutboundOptions {
	t.Helper()
	options, tlsConfig := warpTestCredentials(t)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 || r.Method != http.MethodConnect || r.Host != "cloudflareaccess.com:443" ||
			r.Header.Get("Cf-Connect-Proto") != "cf-connect-ip" || r.Header.Get("Pq-Enabled") != "false" ||
			r.Header.Get(":protocol") != "" || r.TLS.ServerName != options.ServerName {
			t.Errorf("incorrect WARP HTTP/2 handshake: %s %s host=%s headers=%v SNI=%s", r.Proto, r.Method, r.Host, r.Header, r.TLS.ServerName)
			http.Error(w, "bad WARP handshake", 400)
			return
		}
		handler(w, r)
	}))
	server.EnableHTTP2 = true
	server.TLS = tlsConfig
	server.Config.ErrorLog = stdlog.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	options.Server = "127.0.0.1"
	options.ServerPort = uint16(server.Listener.Addr().(*net.TCPAddr).Port)
	return options
}

func warpTestTransport(t *testing.T, options option.MASQUEOutboundOptions) *WARPTransport {
	t.Helper()
	transport, err := NewWARPTransport(context.Background(), N.SystemDialer, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { transport.Close() })
	return transport
}

func warpTestEchoHTTP2(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	w.WriteHeader(200)
	w.(http.Flusher).Flush()
	reader := bufio.NewReader(r.Body)
	for {
		capsuleType, _, err := transportHTTP.ReadVarint(reader)
		if err != nil {
			return
		}
		length, _, err := transportHTTP.ReadVarint(reader)
		if err != nil {
			return
		}
		if capsuleType != 0 || length > 65535 {
			t.Errorf("unexpected WARP capsule: type=%d length=%d", capsuleType, length)
			return
		}
		packet := make([]byte, length)
		if _, err = io.ReadFull(reader, packet); err != nil {
			return
		}
		if len(packet) < 20 || packet[0] != 0x45 {
			t.Errorf("HTTP/2 capsule must begin with the IP header, not a context ID: %x", packet)
			return
		}
		// A response travels back to the API-assigned client address.
		source := append([]byte(nil), packet[12:16]...)
		copy(packet[12:16], packet[16:20])
		copy(packet[16:20], source)
		frame := make([]byte, 1+transportHTTP.VarintLen(length)+len(packet))
		n := transportHTTP.PutVarint(frame[1:], length)
		copy(frame[1+n:], packet)
		// Exercise unknown capsules and fragmented reads as well.
		if _, err = w.Write([]byte{37, 2, 1, 2}); err != nil {
			return
		}
		for _, part := range [][]byte{frame[:1], frame[1:]} {
			if _, err = w.Write(part); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}
}

func warpTestPacket() []byte {
	packet := make([]byte, 28)
	packet[0], packet[3], packet[8], packet[9] = 0x45, 28, 64, 17
	copy(packet[12:16], []byte{172, 16, 0, 2})
	copy(packet[16:20], []byte{198, 51, 100, 1})
	packet[21], packet[23], packet[25] = 53, 53, 8
	return packet
}

func TestWARPHTTP2HandshakeDatagramsAndReconnect(t *testing.T) {
	options := warpTestHTTP2Server(t, func(w http.ResponseWriter, r *http.Request) { warpTestEchoHTTP2(t, w, r) })
	transport := warpTestTransport(t, options)
	for i := range 2 {
		dialCtx, cancelDial := context.WithTimeout(context.Background(), 5*time.Second)
		stream, err := transport.OpenTunnel(dialCtx, "connect-ip", "/")
		cancelDial()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { stream.Close() })
		datagrams := stream.(transportHTTP.DatagramStream)
		packet := warpTestPacket()
		if err = datagrams.SendDatagram(append([]byte{0}, packet...)); err != nil {
			t.Fatal("dial cancellation killed the established tunnel", err)
		}
		readCtx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
		response, err := datagrams.ReceiveDatagram(readCtx)
		cancelRead()
		if err != nil || len(response) != len(packet)+1 || response[0] != 0 ||
			!bytes.Equal(response[17:21], packet[12:16]) || !bytes.Equal(response[13:17], packet[16:20]) {
			t.Fatalf("bad WARP reply: %x, %v", response, err)
		}
		if i == 0 {
			transport.ResetConnections()
			result := make(chan error, 1)
			go func() { _, err := datagrams.ReceiveDatagram(context.Background()); result <- err }()
			select {
			case err = <-result:
				if err == nil {
					t.Fatal("network reset left old tunnel alive")
				}
			case <-time.After(time.Second):
				t.Fatal("network reset did not interrupt the old read")
			}
		}
		stream.Close()
	}
}

func TestWARPHTTP2StalledHandshakeCancellation(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller", true: "network-reset"}[reset], func(t *testing.T) {
			arrived := make(chan struct{}, 1)
			options := warpTestHTTP2Server(t, func(w http.ResponseWriter, r *http.Request) {
				arrived <- struct{}{}
				<-r.Context().Done()
			})
			transport := warpTestTransport(t, options)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := transport.OpenTunnel(ctx, "connect-ip", "/"); result <- err }()
			select {
			case <-arrived:
			case <-time.After(5 * time.Second):
				t.Fatal("handshake never reached the server")
			}
			if reset {
				transport.ResetConnections()
			} else {
				cancel()
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("stalled handshake reported success")
				}
			case <-time.After(time.Second):
				t.Fatal("stalled CONNECT did not observe cancellation")
			}
		})
	}
}

func TestWARPRejectsWrongPeerKeyAndHTTPFailure(t *testing.T) {
	for _, wrongKey := range []bool{false, true} {
		options := warpTestHTTP2Server(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "denied", 403) })
		if wrongKey {
			other, _ := warpTestCredentials(t)
			options.PeerPublicKey = other.PeerPublicKey
		}
		transport := warpTestTransport(t, options)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		stream, err := transport.OpenTunnel(ctx, "connect-ip", "/")
		cancel()
		if err == nil || stream != nil {
			t.Fatal("untrusted peer or failed CONNECT was accepted")
		}
	}
}

func TestWARPKeyFormatsAndValidation(t *testing.T) {
	options, _ := warpTestCredentials(t)
	der, _ := base64.StdEncoding.DecodeString(options.PrivateKey)
	key, _ := x509.ParseECPrivateKey(der)
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	public, _ := pem.Decode([]byte(options.PeerPublicKey))
	for _, private := range []string{options.PrivateKey, string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})), base64.StdEncoding.EncodeToString(pkcs8)} {
		cfg, err := warpTLSConfig(context.Background(), private, base64.StdEncoding.EncodeToString(public.Bytes), options.ServerName)
		if err != nil {
			t.Fatal(err)
		}
		certificate, err := cfg.GetClientCertificate(&tls.CertificateRequestInfo{})
		if err != nil || !certificate.PrivateKey.(*ecdsa.PrivateKey).Equal(key) {
			t.Fatal("client certificate replaced the enrolled key", err)
		}
		if cfg.VerifyPeerCertificate(nil, nil) == nil {
			t.Fatal("missing peer certificate bypassed pinning")
		}
	}
	for _, pair := range [][2]string{{"", options.PeerPublicKey}, {options.PrivateKey, ""}, {"invalid", options.PeerPublicKey}, {options.PrivateKey, "invalid"}} {
		if _, err := warpTLSConfig(context.Background(), pair[0], pair[1], ""); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
}

type warpTestHandler struct {
	configuration chan Configuration
	packets       chan []byte
}

func (h *warpTestHandler) UpdateConfiguration(c Configuration) error {
	h.configuration <- c
	return nil
}
func (h *warpTestHandler) FrontHeadroom() int { return PacketHeadroom }
func (h *warpTestHandler) WriteInboundBuffers(buffers []*buf.Buffer) error {
	for _, buffer := range buffers {
		h.packets <- append([]byte(nil), buffer.Bytes()...)
		buffer.Release()
	}
	return nil
}
func (h *warpTestHandler) WaitWriteInboundBuffers(buffers []*buf.Buffer) error {
	return h.WriteInboundBuffers(buffers)
}

func TestWARPStaticAddressesAndClientLifecycle(t *testing.T) {
	options := warpTestHTTP2Server(t, func(w http.ResponseWriter, r *http.Request) { warpTestEchoHTTP2(t, w, r) })
	transport := warpTestTransport(t, options)
	handler := &warpTestHandler{configuration: make(chan Configuration, 8), packets: make(chan []byte, 8)}
	client, err := NewClient(ClientOptions{
		Context: context.Background(), Logger: log.NewNOPFactory().Logger(),
		HTTPClient: transport, Path: "/", LocalAddress: options.Address, Handler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	client.Start()
	t.Cleanup(func() { client.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = client.WaitReady(ctx); err != nil {
		t.Fatal("WARP must not wait for an ADDRESS_ASSIGN capsule", err)
	}
	configuration := <-handler.configuration
	if !reflect.DeepEqual(configuration.Address, []netip.Prefix(options.Address)) {
		t.Fatal("account addresses changed")
	}
	packet := warpTestPacket()
	buffer := buf.NewSize(PacketHeadroom + len(packet))
	buffer.Resize(PacketHeadroom, 0)
	buffer.Write(packet)
	if err = client.WritePacketBuffers([]*buf.Buffer{buffer}, false); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-handler.packets:
		if !bytes.Equal(response[16:20], packet[12:16]) {
			t.Fatal("response did not reach account address")
		}
	case <-ctx.Done():
		t.Fatal("no IP packet returned through WARP", ctx.Err())
	}
	closed := make(chan struct{})
	go func() { client.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("client shutdown did not unblock both tunnel loops")
	}
}

// warpGatedTestTransport opens one tunnel per permit, so a test decides when a reconnect completes.
type warpGatedTestTransport struct {
	permits chan struct{}
}

func (t *warpGatedTestTransport) OpenTunnel(ctx context.Context, _, _ string) (io.ReadWriteCloser, error) {
	select {
	case <-t.permits:
		tunnel, _ := net.Pipe()
		return tunnel, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (t *warpGatedTestTransport) ResetConnections() {}
func (t *warpGatedTestTransport) Close() error      { return nil }

func TestWARPNetworkResetRevokesReadiness(t *testing.T) {
	transport := &warpGatedTestTransport{permits: make(chan struct{}, 1)}
	handler := &warpTestHandler{configuration: make(chan Configuration, 8), packets: make(chan []byte, 8)}
	client, err := NewClient(ClientOptions{
		Context: context.Background(), Logger: log.NewNOPFactory().Logger(),
		HTTPClient: transport, Path: "/", LocalAddress: []netip.Prefix{netip.MustParsePrefix("172.16.0.2/32")}, Handler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	client.Start()
	t.Cleanup(func() { client.Close() })
	waitReady := func(timeout time.Duration) error {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		return client.WaitReady(ctx)
	}
	transport.permits <- struct{}{}
	if err = waitReady(5 * time.Second); err != nil {
		t.Fatal(err)
	}
	client.RestartSession()
	// The replacement tunnel cannot open before the next permit. A dial that
	// found the cancelled session ready would send its first packets nowhere.
	if client.Ready() {
		t.Fatal("network reset left the cancelled session ready")
	}
	if err = waitReady(100 * time.Millisecond); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("dial did not wait for the replacement tunnel", err)
	}
	transport.permits <- struct{}{}
	if err = waitReady(5 * time.Second); err != nil {
		t.Fatal("replacement tunnel did not become ready", err)
	}
}

type warpStandardTestTransport struct {
	client net.Conn
	peer   net.Conn
}

func (t *warpStandardTestTransport) OpenTunnel(_ context.Context, protocol, path string) (io.ReadWriteCloser, error) {
	if protocol != "connect-ip" || path != "/" {
		return nil, errors.New("standard MASQUE request was changed to the WARP dialect")
	}
	return t.client, nil
}
func (t *warpStandardTestTransport) ResetConnections() { t.client.Close() }
func (t *warpStandardTestTransport) Close() error {
	t.client.Close()
	return t.peer.Close()
}

func TestWARPKeepsStandardMASQUEAddressNegotiation(t *testing.T) {
	local, peer := net.Pipe()
	transport := &warpStandardTestTransport{client: local, peer: peer}
	handler := &warpTestHandler{configuration: make(chan Configuration, 1), packets: make(chan []byte, 1)}
	client, err := NewClient(ClientOptions{
		Context: context.Background(), Logger: log.NewNOPFactory().Logger(),
		HTTPClient: transport, Path: "/", Handler: handler,
	})
	if err != nil {
		t.Fatal(err)
	}
	client.Start()
	t.Cleanup(func() { client.Close() })
	negotiated := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(peer)
		kind, _, err := transportHTTP.ReadVarint(reader)
		if err != nil {
			negotiated <- err
			return
		}
		length, _, err := transportHTTP.ReadVarint(reader)
		if err != nil || kind != capsuleTypeAddressRequest || length > 4096 {
			negotiated <- errors.New("missing standard ADDRESS_REQUEST")
			return
		}
		payload := make([]byte, length)
		if _, err = io.ReadFull(reader, payload); err != nil {
			negotiated <- err
			return
		}
		addresses, err := parseAddresses(payload)
		if err != nil || len(addresses) != 2 || addresses[0].RequestID != 1 || addresses[1].RequestID != 2 ||
			addresses[0].Prefix.String() != "0.0.0.0/32" || addresses[1].Prefix.String() != "::/128" {
			negotiated <- errors.New("standard address requests changed")
			return
		}
		assigned := newAddressCapsule(capsuleTypeAddressAssign, []AssignedAddress{
			{RequestID: 1, Prefix: netip.MustParsePrefix("10.8.0.2/32")},
		})
		_, err = peer.Write(assigned.Bytes())
		assigned.Release()
		negotiated <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case err := <-negotiated:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("standard MASQUE address negotiation stalled")
	}
	if err = client.WaitReady(ctx); err != nil {
		t.Fatal(err)
	}
	configuration := <-handler.configuration
	if !reflect.DeepEqual(configuration.Address, []netip.Prefix{netip.MustParsePrefix("10.8.0.2/32")}) {
		t.Fatal("standard MASQUE no longer uses the server-assigned address")
	}
}
