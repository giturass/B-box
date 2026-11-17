package masque

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/httpclient"
	boxTLS "github.com/sagernet/sing-box/common/tls"
	transportHTTP "github.com/sagernet/sing-box/transport/http"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"

	"golang.org/x/net/http2"
)

func (t *WARPTransport) openHTTP2(dial, lifetime context.Context) (transportHTTP.DatagramStream, error) {
	ctx, cancel := context.WithCancel(lifetime)
	stopDial := context.AfterFunc(dial, cancel)
	raw, err := t.dialer.DialContext(adapter.ContextForMultiplexSession(dial), N.NetworkTCP, t.server)
	if err != nil {
		stopDial()
		cancel()
		return nil, err
	}
	stream := &warpHTTP2Stream{ctx: ctx, cancel: cancel, raw: raw}
	stream.stopClose = context.AfterFunc(ctx, func() { raw.Close() })
	keep := false
	defer func() {
		stopDial()
		if !keep {
			stream.Close()
		}
	}()
	config, err := t.tlsConfig(2)
	if err != nil {
		return nil, err
	}
	config.SetNextProtos([]string{http2.NextProtoTLS})
	tlsConn, err := boxTLS.ClientHandshake(dial, raw, config)
	if err != nil {
		return nil, err
	}
	// The real Cloudflare TCP edge can omit ALPN despite accepting HTTP/2.
	if alpn := tlsConn.ConnectionState().NegotiatedProtocol; alpn != "" && alpn != http2.NextProtoTLS {
		return nil, E.New("WARP endpoint negotiated an incompatible HTTP/2 ALPN")
	}
	h2, err := httpclient.ConfigureHTTP2Transport(t.options.HTTP2Options)
	if err != nil {
		return nil, err
	}
	h2.DisableCompression = true
	if h2.ReadIdleTimeout == 0 {
		h2.ReadIdleTimeout = 30 * time.Second
	}
	if h2.PingTimeout == 0 {
		h2.PingTimeout = 15 * time.Second
	}
	stream.client, err = h2.NewClientConn(tlsConn)
	if err != nil {
		return nil, err
	}
	reader, writer := io.Pipe()
	stream.request = writer
	defer func() {
		if !keep {
			reader.Close()
		}
	}()
	authority := "cloudflareaccess.com:443"
	request := &http.Request{Method: http.MethodConnect, Host: authority,
		URL:    &url.URL{Scheme: "https", Host: authority},
		Header: http.Header{"User-Agent": []string{""}, "Cf-Connect-Proto": []string{"cf-connect-ip"}, "Pq-Enabled": []string{"false"}},
		Body:   reader, ContentLength: -1}
	response, err := stream.client.RoundTrip(request.WithContext(ctx))
	if err != nil {
		return nil, err
	}
	stream.response = response.Body
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, E.New("WARP HTTP/2 CONNECT returned status ", response.StatusCode)
	}
	if !stopDial() || dial.Err() != nil {
		return nil, dial.Err()
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	stream.reader = bufio.NewReader(response.Body)
	// The request owns the pipe reader once CONNECT succeeds.
	keep = true
	return stream, nil
}

type warpHTTP2Stream struct {
	ctx         context.Context
	cancel      context.CancelFunc
	raw         net.Conn
	client      *http2.ClientConn
	request     *io.PipeWriter
	response    io.ReadCloser
	reader      *bufio.Reader
	stopClose   func() bool
	writeAccess sync.Mutex
	closeOnce   sync.Once
}

func (s *warpHTTP2Stream) Read([]byte) (int, error) { <-s.ctx.Done(); return 0, s.ctx.Err() }
func (s *warpHTTP2Stream) Write([]byte) (int, error) {
	return 0, E.New("WARP does not support control capsules")
}
func (s *warpHTTP2Stream) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		s.stopClose()
		if s.request != nil {
			s.request.CloseWithError(net.ErrClosed)
		}
		if s.response != nil {
			s.response.Close()
		}
		if s.client != nil {
			s.client.Close()
		}
		s.raw.Close()
	})
	return nil
}
func (s *warpHTTP2Stream) SendDatagram(data []byte) error {
	contextID, offset, valid := transportHTTP.DecodeVarint(data)
	if !valid || contextID != 0 || offset == len(data) {
		return E.New("invalid WARP datagram context")
	}
	payload := data[offset:]
	if len(payload) > maxPacketSize {
		return &transportHTTP.DatagramTooLargeError{MaxPayloadSize: maxPacketSize + 1}
	}
	// WARP's H2 capsule carries the IP packet directly, without a context ID.
	frame := make([]byte, 1+transportHTTP.VarintLen(uint64(len(payload)))+len(payload))
	n := transportHTTP.PutVarint(frame[1:], uint64(len(payload)))
	copy(frame[1+n:], payload)
	s.writeAccess.Lock()
	defer s.writeAccess.Unlock()
	written, err := s.request.Write(frame)
	if err == nil && written != len(frame) {
		err = io.ErrShortWrite
	}
	return err
}
func (s *warpHTTP2Stream) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	stop := context.AfterFunc(ctx, func() { s.raw.Close() })
	defer stop()
	for {
		capsuleType, _, err := transportHTTP.ReadVarint(s.reader)
		if err != nil {
			return nil, err
		}
		length, _, err := transportHTTP.ReadVarint(s.reader)
		if err != nil {
			return nil, err
		}
		if length > transportHTTP.MaxCapsuleLength {
			return nil, E.New("WARP capsule too large")
		}
		if capsuleType != transportHTTP.CapsuleTypeDatagram || length == 0 || length > maxPacketSize {
			if _, err = io.CopyN(io.Discard, s.reader, int64(length)); err != nil {
				return nil, err
			}
			continue
		}
		packet := make([]byte, 1+int(length))
		if _, err = io.ReadFull(s.reader, packet[1:]); err != nil {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return packet, nil
	}
}
