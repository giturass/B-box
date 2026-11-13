package masque

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	transportHTTP "github.com/sagernet/sing-box/transport/http"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"

	"golang.org/x/net/http2"
)

func (t *WARPTransport) openHTTP2(ctx context.Context) (transportHTTP.DatagramStream, error) {
	raw, err := t.dialer.DialContext(adapter.ContextForMultiplexSession(ctx), N.NetworkTCP, t.server)
	if err != nil {
		return nil, err
	}
	stopClose := context.AfterFunc(ctx, func() { raw.Close() })
	keep := false
	defer func() {
		if !keep {
			stopClose()
			raw.Close()
		}
	}()
	tlsConfig := t.tlsConfig.Clone()
	tlsConfig.NextProtos = []string{http2.NextProtoTLS}
	tlsConn := tls.Client(raw, tlsConfig)
	if err = tlsConn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	if tlsConn.ConnectionState().NegotiatedProtocol != http2.NextProtoTLS {
		return nil, E.New("WARP endpoint did not negotiate HTTP/2")
	}
	h2 := &http2.Transport{DisableCompression: true, ReadIdleTimeout: 30 * time.Second, PingTimeout: 15 * time.Second}
	client, err := h2.NewClientConn(tlsConn)
	if err != nil {
		return nil, err
	}
	reader, writer := io.Pipe()
	defer func() {
		if !keep {
			reader.Close()
			writer.Close()
			client.Close()
		}
	}()
	request, err := http.NewRequestWithContext(ctx, http.MethodConnect, "https://cloudflareaccess.com/", reader)
	if err != nil {
		return nil, err
	}
	// WARP's HTTP/2 fallback is ordinary CONNECT, not extended CONNECT.
	request.Host = "cloudflareaccess.com:443"
	request.ContentLength = -1
	request.Header = http.Header{
		"User-Agent":       []string{""},
		"Cf-Connect-Proto": []string{"cf-connect-ip"},
		"Pq-Enabled":       []string{"false"},
	}
	response, err := client.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, E.New("WARP HTTP/2 CONNECT returned status ", response.StatusCode)
	}
	keep = true
	return &warpHTTP2Stream{
		ctx: ctx, raw: raw, client: client, request: writer, response: response.Body,
		reader: bufio.NewReader(response.Body), stopClose: stopClose,
	}, nil
}

type warpHTTP2Stream struct {
	ctx         context.Context
	raw         net.Conn
	client      *http2.ClientConn
	request     *io.PipeWriter
	response    io.ReadCloser
	reader      *bufio.Reader
	stopClose   func() bool
	writeAccess sync.Mutex
	closeOnce   sync.Once
}

func (s *warpHTTP2Stream) Read([]byte) (int, error) {
	// There is no control stream in WARP HTTP/2. The session's datagram loop
	// reads capsules; keep its control loop alive until the tunnel is closed.
	<-s.ctx.Done()
	return 0, s.ctx.Err()
}

func (s *warpHTTP2Stream) Write([]byte) (int, error) {
	return 0, E.New("WARP HTTP/2 does not support control capsules")
}

func (s *warpHTTP2Stream) Close() error {
	s.closeOnce.Do(func() {
		s.stopClose()
		s.request.Close()
		s.response.Close()
		s.client.Close()
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
	// Unlike RFC 9297, Cloudflare's HTTP/2 capsule contains the IP packet
	// directly, without the context ID used in its HTTP/3 datagrams.
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
