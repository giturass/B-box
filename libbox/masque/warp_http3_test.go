//go:build with_quic

package masque

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/quic-go/qpack"
	"github.com/sagernet/quic-go"
	"github.com/sagernet/quic-go/http3"
	"github.com/sagernet/sing-box/option"
	transportHTTP "github.com/sagernet/sing-box/transport/http"
)

type warpH3Handshake struct {
	headers  map[string]string
	settings map[uint64]uint64
}

// Use a raw QUIC peer: http3.Server always advertises extended CONNECT, while
// Cloudflare can omit it. No enrolled account or external network is involved.
func warpTestHTTP3Server(t *testing.T, datagrams bool, status int) (option.MASQUEOutboundOptions, <-chan warpH3Handshake) {
	t.Helper()
	options, serverTLS := warpTestCredentials(t)
	options.Version = 3
	serverTLS.NextProtos = []string{http3.NextProtoH3}
	packetConn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	transport := &quic.Transport{Conn: packetConn}
	listener, err := transport.Listen(serverTLS, &quic.Config{EnableDatagrams: true})
	if err != nil {
		packetConn.Close()
		t.Fatal(err)
	}
	options.Server = "127.0.0.1"
	options.ServerPort = uint16(packetConn.LocalAddr().(*net.UDPAddr).Port)
	ctx, cancel := context.WithCancel(context.Background())
	observed := make(chan warpH3Handshake, 4)
	var workers sync.WaitGroup
	workers.Go(func() {
		for {
			conn, acceptErr := listener.Accept(ctx)
			if acceptErr != nil {
				return
			}
			workers.Go(func() {
				defer conn.CloseWithError(0, "")
				stop := context.AfterFunc(ctx, func() { conn.CloseWithError(0, "") })
				defer stop()
				if conn.ConnectionState().TLS.ServerName != options.ServerName {
					t.Error("HTTP/3 did not send the selected WARP SNI")
					return
				}
				control, openErr := conn.OpenUniStreamSync(ctx)
				if openErr != nil {
					return
				}
				settings := []byte{0, 4, 0} // control stream, empty SETTINGS
				if datagrams {
					settings = []byte{0, 4, 2, 0x33, 1} // H3_DATAGRAM only, no setting 0x8
				}
				if _, writeErr := control.Write(settings); writeErr != nil {
					return
				}
				clientSettings, readErr := warpReadH3Settings(conn)
				if readErr != nil {
					return
				}
				request, readErr := conn.AcceptStream(ctx)
				if readErr != nil {
					return
				}
				block, readErr := warpReadH3Frame(bufio.NewReader(request), 1)
				if readErr != nil {
					return
				}
				headers := make(map[string]string)
				next := qpack.NewDecoder().Decode(block)
				for {
					field, decodeErr := next()
					if decodeErr == io.EOF {
						break
					}
					if decodeErr != nil {
						t.Error(decodeErr)
						return
					}
					headers[field.Name] = field.Value
				}
				observed <- warpH3Handshake{headers, clientSettings}
				if status == 0 {
					<-conn.Context().Done() // stalled CONNECT response
					return
				}
				var response bytes.Buffer
				encoder := qpack.NewEncoder(&response)
				if encodeErr := encoder.WriteField(qpack.HeaderField{Name: ":status", Value: strconv.Itoa(status)}); encodeErr != nil {
					t.Error(encodeErr)
					return
				}
				if encodeErr := encoder.Close(); encodeErr != nil {
					t.Error(encodeErr)
					return
				}
				frame := make([]byte, 1+transportHTTP.VarintLen(uint64(response.Len())))
				frame[0] = 1 // HEADERS
				transportHTTP.PutVarint(frame[1:], uint64(response.Len()))
				if _, writeErr := request.Write(append(frame, response.Bytes()...)); writeErr != nil {
					return
				}
				for {
					data, receiveErr := conn.ReceiveDatagram(ctx)
					if receiveErr != nil {
						return
					}
					id, offset, valid := transportHTTP.DecodeVarint(data)
					if !valid || id != uint64(request.StreamID()/4) || len(data) <= offset+1 || data[offset] != 0 || data[offset+1] != 0x45 {
						t.Errorf("incorrect WARP HTTP/3 datagram: %x", data)
						return
					}
					if sendErr := conn.SendDatagram(data); sendErr != nil {
						return
					}
				}
			})
		}
	})
	t.Cleanup(func() {
		cancel()
		listener.Close()
		transport.Close()
		packetConn.Close()
		workers.Wait()
	})
	return options, observed
}

func warpReadH3Frame(reader *bufio.Reader, expected uint64) ([]byte, error) {
	frameType, _, err := transportHTTP.ReadVarint(reader)
	if err != nil {
		return nil, err
	}
	length, _, err := transportHTTP.ReadVarint(reader)
	if err != nil {
		return nil, err
	}
	if frameType != expected || length > 65535 {
		return nil, fmt.Errorf("unexpected HTTP/3 frame: type=%d length=%d", frameType, length)
	}
	data := make([]byte, int(length))
	_, err = io.ReadFull(reader, data)
	return data, err
}

func warpReadH3Settings(conn *quic.Conn) (map[uint64]uint64, error) {
	for {
		stream, err := conn.AcceptUniStream(conn.Context())
		if err != nil {
			return nil, err
		}
		reader := bufio.NewReader(stream)
		streamType, _, err := transportHTTP.ReadVarint(reader)
		if err != nil {
			return nil, err
		}
		if streamType != 0 {
			continue
		}
		data, err := warpReadH3Frame(reader, 4)
		if err != nil {
			return nil, err
		}
		settings := make(map[uint64]uint64)
		for len(data) > 0 {
			id, n, valid := transportHTTP.DecodeVarint(data)
			if !valid {
				return nil, errors.New("invalid setting ID")
			}
			data = data[n:]
			value, n, valid := transportHTTP.DecodeVarint(data)
			if !valid {
				return nil, errors.New("invalid setting value")
			}
			data = data[n:]
			settings[id] = value
		}
		return settings, nil
	}
}

func TestWARPHTTP3WithoutExtendedConnectSetting(t *testing.T) {
	options, observed := warpTestHTTP3Server(t, true, 200)
	transport := warpTestTransport(t, options)
	for range 2 {
		dialCtx, cancelDial := context.WithTimeout(context.Background(), 5*time.Second)
		stream, err := transport.OpenTunnel(dialCtx, "connect-ip", "/")
		cancelDial()
		if err != nil {
			t.Fatal("Cloudflare's missing extended CONNECT setting must be accepted", err)
		}
		t.Cleanup(func() { stream.Close() })
		handshake := <-observed
		for name, value := range map[string]string{
			":method": "CONNECT", ":protocol": "cf-connect-ip", ":scheme": "https",
			":authority": "cloudflareaccess.com", ":path": "/", "capsule-protocol": "?1", "user-agent": "",
		} {
			if handshake.headers[name] != value {
				t.Errorf("incorrect %s: %q", name, handshake.headers[name])
			}
		}
		if handshake.settings[0x276] != 1 || handshake.settings[0x33] != 1 {
			t.Fatalf("missing WARP datagram settings: %v", handshake.settings)
		}
		datagrams := stream.(transportHTTP.DatagramStream)
		payload := append([]byte{0}, warpTestPacket()...)
		if err = datagrams.SendDatagram(payload); err != nil {
			t.Fatal("dial cancellation killed the established tunnel", err)
		}
		readCtx, cancelRead := context.WithTimeout(context.Background(), 5*time.Second)
		response, err := datagrams.ReceiveDatagram(readCtx)
		cancelRead()
		if err != nil || !bytes.Equal(response, payload) {
			t.Fatalf("HTTP/3 datagram round trip failed: %x, %v", response, err)
		}
		var tooLarge *transportHTTP.DatagramTooLargeError
		if err = datagrams.SendDatagram(make([]byte, 65535)); !errors.As(err, &tooLarge) || tooLarge.MaxPayloadSize <= 0 {
			t.Fatal("oversized datagram did not report the usable tunnel MTU", err)
		}
		transport.ResetConnections()
		readCtx, cancelRead = context.WithTimeout(context.Background(), time.Second)
		_, err = datagrams.ReceiveDatagram(readCtx)
		cancelRead()
		if err == nil || errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("network reset did not interrupt the old HTTP/3 tunnel", err)
		}
		if err = datagrams.SendDatagram(payload); err == nil {
			t.Fatal("network reset left the old HTTP/3 tunnel writable")
		}
		stream.Close()
	}
}

func TestWARPHTTP3StalledHandshakeCancellation(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller", true: "network-reset"}[reset], func(t *testing.T) {
			options, observed := warpTestHTTP3Server(t, true, 0)
			transport := warpTestTransport(t, options)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { _, err := transport.OpenTunnel(ctx, "connect-ip", "/"); result <- err }()
			select {
			case <-observed:
			case <-time.After(5 * time.Second):
				t.Fatal("CONNECT never reached the HTTP/3 peer")
			}
			if reset {
				transport.ResetConnections()
			} else {
				cancel()
			}
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("stalled CONNECT reported success")
				}
			case <-time.After(time.Second):
				t.Fatal("stalled HTTP/3 CONNECT ignored cancellation")
			}
		})
	}
}

func TestWARPHTTP3RejectsUnusablePeers(t *testing.T) {
	for _, test := range []struct {
		name      string
		datagrams bool
		status    int
		wrongKey  bool
	}{
		{"no-datagrams", false, 200, false},
		{"forbidden", true, 403, false},
		{"wrong-key", true, 200, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, _ := warpTestHTTP3Server(t, test.datagrams, test.status)
			if test.wrongKey {
				other, _ := warpTestCredentials(t)
				options.PeerPublicKey = other.PeerPublicKey
			}
			transport := warpTestTransport(t, options)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			stream, err := transport.OpenTunnel(ctx, "connect-ip", "/")
			if err == nil || stream != nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("unusable WARP peer was not rejected promptly", err)
			}
		})
	}
}
