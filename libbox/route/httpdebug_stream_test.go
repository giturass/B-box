package route

import (
	"bytes"
	"context"
	"io"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-box/experimental/httpdebug"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// Use sing's original bidirectional copier and a real TCP origin. A byte-count
// echo would miss the response that an origin sends only after request EOF.
type captureHalfCloseOutbound struct {
	adapter.Outbound
	origin   string
	metadata chan adapter.InboundContext
	done     chan error
}

func (o *captureHalfCloseOutbound) Tag() string  { return "selected-half-close-group" }
func (o *captureHalfCloseOutbound) Type() string { return "direct" }

func (o *captureHalfCloseOutbound) NewConnection(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, onClose N.CloseHandlerFunc) {
	o.metadata <- metadata
	go func() {
		var err error
		defer func() {
			_ = conn.Close()
			onClose(err)
			o.done <- err
		}()
		remote, dialErr := (&net.Dialer{}).DialContext(ctx, "tcp", o.origin)
		if dialErr != nil {
			err = dialErr
			return
		}
		defer remote.Close()
		err = bufio.CopyConn(ctx, conn, remote)
	}()
}

func captureRealTCPPair(t *testing.T, deadline time.Time) (*net.TCPConn, *net.TCPConn) {
	t.Helper()
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	client, err := net.DialTCP("tcp", nil, listener.Addr().(*net.TCPAddr))
	if err != nil {
		t.Fatal(err)
	}
	inbound, err := listener.AcceptTCP()
	if err != nil {
		_ = client.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close(); _ = inbound.Close() })
	_ = client.SetDeadline(deadline)
	_ = inbound.SetDeadline(deadline)
	return client, inbound
}

func testBboxHTTPDebugRawTCPHalfClose(t *testing.T, directory string) {
	for _, stopAfterEOF := range []bool{false, true} {
		name, host := "response-after-EOF", "raw-eof.test"
		if stopAfterEOF {
			name, host = "cancel-after-EOF", "raw-cancel.test"
		}
		t.Run(name, func(t *testing.T) {
			if err := httpdebug.Start(directory, `{"ports":[9001],"rawTCP":true,"bodyLimit":-1}`, "", nil); err != nil {
				t.Fatal(err)
			}
			defer httpdebug.StopService()
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			defer cancel()
			deadline, _ := ctx.Deadline()
			origin, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer origin.Close()
			payload := bytes.Repeat([]byte{0x00, 0xff, 'b', 'i', 'n', '\r', '\n'}, 16*1024)
			reply := append([]byte("response after request EOF:"), payload...)
			requestEOF := make(chan []byte, 1)
			originDone := make(chan error, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			go func() {
				conn, err := origin.Accept()
				if err != nil {
					originDone <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(deadline)
				request, err := io.ReadAll(conn)
				if err != nil {
					originDone <- err
					return
				}
				requestEOF <- request
				if stopAfterEOF {
					<-release
				} else {
					_, err = conn.Write(reply)
				}
				originDone <- err
			}()
			outbound := &captureHalfCloseOutbound{origin: origin.Addr().String(), metadata: make(chan adapter.InboundContext, 1), done: make(chan error, 1)}
			metadata := adapter.InboundContext{Network: "tcp", Inbound: "tun", Domain: host, Source: M.ParseSocksaddr("192.0.2.2:4321"),
				Destination: M.ParseSocksaddr("198.51.100.9:9101"), RouteOriginalDestination: M.ParseSocksaddr("198.51.100.9:9001"),
				RouteOutbound: outbound.Tag(), RouteRule: "unchanged-half-close-rule", TLSFragment: true,
				ProcessInfo: &adapter.ConnectionOwner{UserId: 12345, PackageNames: []string{"com.test"}}, OutboundChain: []adapter.Outbound{outbound}}
			client, inbound := captureRealTCPPair(t, deadline)
			manager := trafficcontrol.NewManager()
			defer manager.CloseAllConnections()
			tracked := manager.RoutedConnection(ctx, inbound, metadata, nil, outbound)
			connectionID := manager.Connections()[0].ID.String()
			written := make(chan error, 1)
			go func() {
				_, err := client.Write(payload)
				if err == nil {
					err = client.CloseWrite()
				}
				written <- err
			}()
			router := &Router{ctx: context.Background()}
			if _, captured := router.routeHTTPDebug(ctx, tracked, metadata, outbound, nil); !captured {
				t.Fatal("raw TCP with a real TCP half-close was not selected")
			}
			if err := <-written; err != nil {
				t.Fatal("client could not half-close after its binary payload", err)
			}
			select {
			case request := <-requestEOF:
				if !bytes.Equal(request, payload) {
					t.Fatal("raw TCP route lost sniffed bytes before origin EOF", len(request))
				}
			case <-ctx.Done():
				t.Fatal("origin did not receive the client's EOF", ctx.Err())
			}
			if seen := <-outbound.metadata; !reflect.DeepEqual(seen, metadata) {
				t.Fatalf("half-close routing changed the exact selected outbound metadata: %+v", seen)
			}
			if stopAfterEOF {
				stopped := make(chan struct{})
				go func() { httpdebug.StopService(); close(stopped) }()
				select {
				case <-stopped:
				case <-time.After(3 * time.Second):
					t.Fatal("service stop waited for a server response after client EOF")
				}
			} else {
				response, err := io.ReadAll(client)
				if err != nil || !bytes.Equal(response, reply) {
					t.Fatal("raw TCP lost the origin's response after client EOF", len(response), err)
				}
				httpdebug.StopService()
			}
			select {
			case err := <-outbound.done:
				if !stopAfterEOF && err != nil {
					t.Fatal("selected raw TCP handler did not finish its two directions", err)
				}
			case <-ctx.Done():
				t.Fatal("selected raw TCP route remained open after capture finished", ctx.Err())
			}
			releaseOnce.Do(func() { close(release) })
			if err := <-originDone; err != nil {
				t.Fatal("real TCP origin failed", err)
			}
			if !stopAfterEOF {
				store, err := httpdebug.Saved()
				if err != nil {
					t.Fatal(err)
				}
				parents := 0
				for _, item := range store.Page(0, "tcp://"+host+":9001").Items {
					detail, err := store.Detail(item.ID)
					if err != nil {
						t.Fatal(err)
					}
					if detail.Metadata.ParentID != "" {
						continue
					}
					parents++
					if detail.Metadata.ConnectionID != connectionID || detail.Metadata.Host != host || detail.Metadata.Port != 9001 ||
						!detail.Metadata.RawTCP || detail.Metadata.TLS != nil || detail.Metadata.Outbound != outbound.Tag() ||
						!detail.Request.BodyComplete || !detail.Response.BodyComplete || detail.Request.BodyBytes != int64(len(payload)) ||
						detail.Response.BodyBytes != int64(len(reply)) {
						t.Fatalf("raw TCP half-close lost tracker, route, EOF or response state: %+v", detail)
					}
				}
				if parents != 1 {
					t.Fatal("raw TCP half-close did not retain exactly one connection", parents)
				}
			}
		})
	}
}
