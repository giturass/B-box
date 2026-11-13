//go:build with_quic

package masque

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/adapter"
	transportHTTP "github.com/sagernet/sing-box/transport/http"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
)

const warpQUICIncluded = true

func (t *WARPTransport) openHTTP3(ctx context.Context) (transportHTTP.DatagramStream, error) {
	raw, err := t.dialer.DialContext(adapter.ContextForMultiplexSession(ctx), N.NetworkUDP, t.server)
	if err != nil {
		return nil, err
	}
	stopClose := context.AfterFunc(ctx, func() { raw.Close() })
	// Wrap the connected core dialer, including detours and Android socket
	// protection. quic.Transport must not WriteTo an already-connected UDP fd.
	qt := &quic.Transport{Conn: &warpConnectedPacketConn{Conn: raw}, ConnectionIDLength: 20}
	keep := false
	defer func() {
		if !keep {
			stopClose()
			raw.Close()
			qt.Close()
		}
	}()
	tlsConfig := t.tlsConfig.Clone()
	tlsConfig.NextProtos = []string{http3.NextProtoH3}
	quicConn, err := qt.Dial(ctx, raw.RemoteAddr(), tlsConfig, &quic.Config{
		EnableDatagrams:   true,
		KeepAlivePeriod:   30 * time.Second,
		InitialPacketSize: uint16(min(t.mtu+QUICPacketOverhead, 1452)),
	})
	if err != nil {
		return nil, err
	}
	h3 := &http3.Transport{
		EnableDatagrams:    true,
		DisableCompression: true,
		AdditionalSettings: map[uint64]uint64{0x276: 1}, // WARP's legacy H3_DATAGRAM setting.
	}
	defer func() {
		if !keep {
			quicConn.CloseWithError(0, "")
			h3.Close()
		}
	}()
	client := h3.NewClientConn(quicConn)
	select {
	case <-client.ReceivedSettings():
	case <-client.Context().Done():
		return nil, context.Cause(client.Context())
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Cloudflare omits SETTINGS_ENABLE_CONNECT_PROTOCOL. Check datagrams, but
	// do not apply the standard MASQUE extended-CONNECT capability check here.
	if !client.Settings().EnableDatagrams {
		return nil, E.New("WARP endpoint did not enable HTTP/3 datagrams")
	}
	stream, err := client.OpenRequestStream(ctx)
	if err != nil {
		return nil, err
	}
	err = stream.SendRequestHeader(&http.Request{
		Method: http.MethodConnect,
		Proto:  "cf-connect-ip",
		Host:   "cloudflareaccess.com",
		URL:    &url.URL{Scheme: "https", Host: "cloudflareaccess.com", Path: "/"},
		Header: http.Header{"Capsule-Protocol": []string{"?1"}, "User-Agent": []string{""}},
	})
	if err != nil {
		return nil, err
	}
	response, err := stream.ReadResponse()
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, E.New("WARP HTTP/3 CONNECT returned status ", response.StatusCode)
	}
	keep = true
	return &warpHTTP3Stream{stream: stream, conn: quicConn, quic: qt, http: h3, raw: raw, stopClose: stopClose}, nil
}

type warpConnectedPacketConn struct{ net.Conn }

func (c *warpConnectedPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	n, err := c.Read(p)
	return n, c.RemoteAddr(), err
}

func (c *warpConnectedPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	return c.Write(p)
}

type warpHTTP3Stream struct {
	stream    *http3.RequestStream
	conn      *quic.Conn
	quic      *quic.Transport
	http      *http3.Transport
	raw       net.Conn
	stopClose func() bool
	closeOnce sync.Once
}

func (s *warpHTTP3Stream) Read(p []byte) (int, error)  { return s.stream.Read(p) }
func (s *warpHTTP3Stream) Write(p []byte) (int, error) { return s.stream.Write(p) }

func (s *warpHTTP3Stream) Close() error {
	s.closeOnce.Do(func() {
		s.stopClose()
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
		return &transportHTTP.DatagramTooLargeError{
			MaxPayloadSize: int(tooLarge.MaxDatagramPayloadSize) - transportHTTP.VarintLen(uint64(s.stream.StreamID()/4)),
		}
	}
	return err
}

func (s *warpHTTP3Stream) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	return s.stream.ReceiveDatagram(ctx)
}
