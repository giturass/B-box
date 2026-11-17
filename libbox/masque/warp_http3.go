//go:build with_quic

package masque

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/dialer"
	"github.com/sagernet/sing-box/common/httpclient"
	"github.com/sagernet/sing-box/log"
	transportHTTP "github.com/sagernet/sing-box/transport/http"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

const warpQUICIncluded = true

// An unconnected core-dialer socket keeps Android protection, detours and QUIC's
// native UDP handling. Domain lookup uses the same resolver options as the dialer.
func (t *WARPTransport) listenPacket(ctx context.Context) (net.PacketConn, M.Socksaddr, error) {
	ctx = adapter.ContextForMultiplexSession(ctx)
	if !t.server.IsDomain() {
		conn, err := t.dialer.ListenPacket(ctx, t.server)
		return conn, t.server, err
	}
	router := service.FromContext[adapter.DNSRouter](t.ctx)
	if router == nil {
		return nil, M.Socksaddr{}, E.New("missing WARP server resolver")
	}
	var query adapter.DNSQueryOptions
	if resolver, ok := t.dialer.(dialer.ResolveDialer); ok {
		query = resolver.QueryOptions()
	}
	addresses, err := router.Lookup(log.ContextWithOverrideLevel(ctx, log.LevelDebug), t.server.Fqdn, query)
	if err != nil {
		return nil, M.Socksaddr{}, err
	}
	conn, address, err := N.ListenSerial(ctx, t.dialer, t.server, addresses)
	if err != nil {
		return nil, M.Socksaddr{}, err
	}
	return conn, M.SocksaddrFrom(address, t.server.Port), nil
}

func (t *WARPTransport) openHTTP3(ctx, lifetime context.Context) (transportHTTP.DatagramStream, error) {
	config, err := t.tlsConfig(3)
	if err != nil {
		return nil, err
	}
	tlsConfig, err := config.STDConfig()
	if err != nil {
		return nil, err
	}
	raw, destination, err := t.listenPacket(ctx)
	if err != nil {
		return nil, err
	}
	stopClose := context.AfterFunc(ctx, func() { raw.Close() })
	qt := &quic.Transport{Conn: raw, ConnectionIDLength: 20}
	keep := false
	defer func() {
		stopClose()
		if !keep {
			raw.Close()
			qt.Close()
		}
	}()
	tlsConfig.NextProtos = []string{http3.NextProtoH3}
	quicOptions := t.options.QUICOptions
	if quicOptions.KeepAlivePeriod == 0 {
		quicOptions.KeepAlivePeriod = badoption.Duration(30 * time.Second)
	}
	if quicOptions.InitialPacketSize == 0 {
		quicOptions.InitialPacketSize = 1242
		if quicOptions.DisablePathMTUDiscovery {
			mtu := t.options.MTU
			if mtu == 0 {
				mtu = DefaultMTU
			}
			quicOptions.InitialPacketSize = int(min(mtu+46, 65535))
		}
	}
	quicConfig := httpclient.NewQUICConfig(quicOptions)
	quicConfig.EnableDatagrams = true
	conn, err := qt.Dial(ctx, destination.UDPAddr(), tlsConfig, quicConfig)
	if err != nil {
		return nil, err
	}
	h3 := &http3.Transport{EnableDatagrams: true, DisableCompression: true, AdditionalSettings: map[uint64]uint64{0x276: 1}}
	defer func() {
		if !keep {
			conn.CloseWithError(0, "")
			h3.Close()
		}
	}()
	client := h3.NewClientConn(conn)
	select {
	case <-client.ReceivedSettings():
	case <-client.Context().Done():
		return nil, context.Cause(client.Context())
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Cloudflare may omit SETTINGS_ENABLE_CONNECT_PROTOCOL for cf-connect-ip.
	if !client.Settings().EnableDatagrams {
		return nil, E.New("WARP endpoint did not enable HTTP/3 datagrams")
	}
	request, err := client.OpenRequestStream(ctx)
	if err != nil {
		return nil, err
	}
	err = request.SendRequestHeader(&http.Request{Method: http.MethodConnect, Proto: "cf-connect-ip", Host: "cloudflareaccess.com",
		URL:    &url.URL{Scheme: "https", Host: "cloudflareaccess.com", Path: "/"},
		Header: http.Header{"Capsule-Protocol": []string{"?1"}, "User-Agent": []string{""}}})
	if err != nil {
		return nil, err
	}
	response, err := request.ReadResponse()
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, E.New("WARP HTTP/3 CONNECT returned status ", response.StatusCode)
	}
	if !stopClose() || ctx.Err() != nil {
		return nil, ctx.Err()
	}
	streamCtx, cancel := context.WithCancel(lifetime)
	stream := &warpHTTP3Stream{ctx: streamCtx, cancel: cancel, stream: request, conn: conn, quic: qt, http: h3, raw: raw}
	stream.stopClose = context.AfterFunc(streamCtx, func() { raw.Close() })
	keep = true
	go stream.drainCapsules()
	return stream, nil
}

type warpHTTP3Stream struct {
	ctx       context.Context
	cancel    context.CancelFunc
	stream    *http3.RequestStream
	conn      *quic.Conn
	quic      *quic.Transport
	http      *http3.Transport
	raw       net.PacketConn
	stopClose func() bool
	closeOnce sync.Once
}

// WARP does not negotiate addresses in its control stream. Drain bounded
// capsules and treat stream closure as tunnel failure, without applying them
// as standard MASQUE address or route changes.
func (s *warpHTTP3Stream) drainCapsules() {
	reader := bufio.NewReader(s.stream)
	for {
		_, _, err := transportHTTP.ReadVarint(reader)
		if err != nil {
			s.Close()
			return
		}
		length, _, err := transportHTTP.ReadVarint(reader)
		if err != nil || length > transportHTTP.MaxCapsuleLength {
			s.Close()
			return
		}
		if _, err = io.CopyN(io.Discard, reader, int64(length)); err != nil {
			s.Close()
			return
		}
	}
}
func (s *warpHTTP3Stream) Read([]byte) (int, error) { <-s.ctx.Done(); return 0, s.ctx.Err() }
func (s *warpHTTP3Stream) Write([]byte) (int, error) {
	return 0, E.New("WARP does not support control capsules")
}
func (s *warpHTTP3Stream) Close() error {
	s.closeOnce.Do(func() {
		s.stopClose()
		s.cancel()
		s.stream.CancelRead(0)
		s.stream.CancelWrite(0)
		s.conn.CloseWithError(0, "")
		s.raw.Close()
		s.quic.Close()
		s.http.Close()
	})
	return nil
}
func (s *warpHTTP3Stream) SendDatagram(payload []byte) error {
	err := s.stream.SendDatagram(payload)
	var tooLarge *quic.DatagramTooLargeError
	if errors.As(err, &tooLarge) {
		// quic-go omits up to four packet-number bytes from this limit. The
		// session adds/removes the IP context ID itself when computing ICMP MTU.
		return &transportHTTP.DatagramTooLargeError{MaxPayloadSize: int(tooLarge.MaxDatagramPayloadSize) - transportHTTP.VarintLen(uint64(s.stream.StreamID()/4)) - 4}
	}
	return err
}
func (s *warpHTTP3Stream) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	return s.stream.ReceiveDatagram(ctx)
}
