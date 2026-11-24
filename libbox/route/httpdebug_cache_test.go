package route

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-box/experimental/httpdebug"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func cacheTestBuffer(data []byte) *buf.Buffer {
	buffer := buf.NewSize(len(data))
	_, _ = buffer.Write(data)
	return buffer
}

type cacheTestObservedConn struct {
	net.Conn
	started chan struct{}
	once    sync.Once
}

func (c *cacheTestObservedConn) Read(p []byte) (int, error) {
	c.once.Do(func() { close(c.started) })
	return c.Conn.Read(p)
}

func (c *cacheTestObservedConn) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.started) })
	return c.Conn.Write(p)
}

func TestBboxHTTPDebugCacheCloseInterruptsNetworkReadAndWrite(t *testing.T) {
	for _, writing := range []bool{false, true} {
		name := "read"
		if writing {
			name = "write"
		}
		t.Run(name, func(t *testing.T) {
			local, peer := net.Pipe()
			defer local.Close()
			defer peer.Close()
			observed := &cacheTestObservedConn{Conn: local, started: make(chan struct{})}
			prefix := []byte("cached prefix")
			conn := newDebugCachedConn(observed, cacheTestBuffer(prefix))
			defer conn.Close()
			if !writing {
				got := make([]byte, len(prefix))
				if _, err := io.ReadFull(conn, got); err != nil || !bytes.Equal(got, prefix) {
					t.Fatal("cached prefix was not replayed", got, err)
				}
			}
			finished := make(chan error, 1)
			go func() {
				var err error
				if writing {
					_, err = conn.Write([]byte("blocked"))
				} else {
					_, err = conn.Read(make([]byte, 1))
				}
				finished <- err
			}()
			select {
			case <-observed.started:
			case <-time.After(time.Second):
				t.Fatal("cached connection did not reach its network operation")
			}
			closed := make(chan error, 1)
			go func() { closed <- conn.Close() }()
			select {
			case err := <-closed:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("cache lock made Close wait for a blocked network operation")
			}
			select {
			case err := <-finished:
				if !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, net.ErrClosed) {
					t.Fatal("Close did not interrupt network I/O", err)
				}
			case <-time.After(time.Second):
				t.Fatal("network operation survived cached connection closure")
			}
		})
	}
}

func TestBboxHTTPDebugCacheTransferSurvivesClose(t *testing.T) {
	local, peer := net.Pipe()
	defer local.Close()
	defer peer.Close()
	_ = local.SetDeadline(time.Now().Add(3 * time.Second))
	prefix := []byte("a cached prefix remains owned by its reader")
	conn := newDebugCachedConn(local, cacheTestBuffer(prefix))
	if conn.ReaderReplaceable() || !conn.WriterReplaceable() || conn.Upstream() != local {
		t.Fatal("pending cache lost native reader/writer wrapper semantics")
	}
	first := make([]byte, 2)
	if _, err := conn.Read(first); err != nil || !bytes.Equal(first, prefix[:2]) {
		t.Fatal("cached prefix read failed", first, err)
	}
	remaining := conn.ReadCached()
	if remaining == nil {
		t.Fatal("native copier could not take the remaining cache")
	}
	defer remaining.Release()
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(remaining.Bytes(), prefix[2:]) || !conn.ReaderReplaceable() || conn.ReadCached() != nil {
		t.Fatal("Close freed or duplicated a cache already transferred to its reader")
	}
}

type cacheTestHeldWriter struct {
	started chan struct{}
	release chan struct{}
	data    []byte
}

func (w *cacheTestHeldWriter) Write(p []byte) (int, error) {
	close(w.started)
	<-w.release
	w.data = append(w.data, p...)
	return len(p), nil
}

func TestBboxHTTPDebugCacheWriteToOwnsBytesWhileCloseReturns(t *testing.T) {
	local, peer := net.Pipe()
	defer local.Close()
	defer peer.Close()
	prefix := bytes.Repeat([]byte("still owned;"), 8192)
	conn := newDebugCachedConn(local, cacheTestBuffer(prefix))
	defer conn.Close()
	writer := &cacheTestHeldWriter{started: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(writer.release) })
	defer release()
	type result struct {
		n   int64
		err error
	}
	finished := make(chan result, 1)
	go func() { n, err := conn.WriteTo(writer); finished <- result{n, err} }()
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("WriteTo did not hand its cached bytes to the writer")
	}
	closed := make(chan error, 1)
	go func() { closed <- conn.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cached WriteTo held the cache lock while its destination blocked")
	}
	release()
	select {
	case got := <-finished:
		if got.n != int64(len(prefix)) || !bytes.Equal(writer.data, prefix) || got.err == nil {
			t.Fatal("Close freed in-flight bytes or WriteTo hid the closed network tail", got.n, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("WriteTo did not finish after its destination was released")
	}
}

type cacheTestResultWriter struct {
	written int
	err     error
	data    []byte
}

func (w *cacheTestResultWriter) Write(p []byte) (int, error) {
	w.data = append(w.data, p[:w.written]...)
	return w.written, w.err
}

func TestBboxHTTPDebugCacheWriteToPreservesUnwrittenSuffix(t *testing.T) {
	prefix := []byte("a cached prefix")
	writeFailure := errors.New("destination failed")
	for _, test := range []struct {
		name    string
		written int
		err     error
	}{
		{"failed", 0, writeFailure},
		{"partial-error", 3, writeFailure},
		{"short-write", 3, nil},
		{"full-write-error", len(prefix), writeFailure},
	} {
		t.Run(test.name, func(t *testing.T) {
			local, peer := net.Pipe()
			defer local.Close()
			defer peer.Close()
			_ = local.SetDeadline(time.Now().Add(3 * time.Second))
			_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
			conn := newDebugCachedConn(local, cacheTestBuffer(prefix))
			defer conn.Close()
			writer := &cacheTestResultWriter{written: test.written, err: test.err}
			n, err := conn.WriteTo(writer)
			wantErr := test.err
			if wantErr == nil {
				wantErr = io.ErrShortWrite
			}
			if n != int64(test.written) || !errors.Is(err, wantErr) || !bytes.Equal(writer.data, prefix[:test.written]) {
				t.Fatal("cached WriteTo misreported the destination's accepted bytes", n, err)
			}
			remaining := make([]byte, len(prefix)-test.written)
			if _, err = io.ReadFull(conn, remaining); err != nil || !bytes.Equal(remaining, prefix[test.written:]) {
				t.Fatal("failed cached WriteTo lost or duplicated its unwritten suffix", remaining, err)
			}
			tail := []byte("network tail")
			written := make(chan error, 1)
			go func() { _, err := peer.Write(tail); written <- err }()
			got := make([]byte, len(tail))
			if _, err = io.ReadFull(conn, got); err != nil || !bytes.Equal(got, tail) {
				t.Fatal("restored cache did not lead into the original network stream", got, err)
			}
			if err := <-written; err != nil {
				t.Fatal(err)
			}
		})
	}
}

type cacheTestPausedOutbound struct {
	adapter.Outbound
	started chan struct{}
	done    chan struct{}
}

func (o *cacheTestPausedOutbound) Tag() string  { return "cached-paused-outbound" }
func (o *cacheTestPausedOutbound) Type() string { return "direct" }

func (o *cacheTestPausedOutbound) NewConnection(ctx context.Context, conn net.Conn, _ adapter.InboundContext, onClose N.CloseHandlerFunc) {
	close(o.started)
	go func() {
		defer close(o.done)
		defer onClose(nil)
		defer conn.Close()
		<-ctx.Done()
	}()
}

func testBboxHTTPDebugPreSniffedTCPStopsWithPendingCache(t *testing.T, directory string) {
	t.Helper()
	defer httpdebug.StopService()
	for range 16 {
		if err := httpdebug.Start(directory, `{"ports":[9001],"rawTCP":true,"bodyLimit":-1}`, "", nil); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		local, client := net.Pipe()
		// The engine can read only its first chunk before the selected outbound
		// stalls. Stop overlaps that cache read while a buffered suffix remains.
		cached := newDebugCachedConn(local, cacheTestBuffer(bytes.Repeat([]byte{0, 255, 'c'}, 64*1024)))
		observed := &cacheTestObservedConn{Conn: cached, started: make(chan struct{})}
		outbound := &cacheTestPausedOutbound{started: make(chan struct{}), done: make(chan struct{})}
		metadata := adapter.InboundContext{Network: "tcp", Protocol: "tcp", Domain: "cached-pending.test",
			Source: M.ParseSocksaddr("192.0.2.2:4321"), Destination: M.ParseSocksaddr("198.51.100.9:9001"),
			RouteOutbound: outbound.Tag(), OutboundChain: []adapter.Outbound{outbound}}
		manager := trafficcontrol.NewManager()
		tracked := manager.RoutedConnection(ctx, observed, metadata, nil, outbound)
		router := &Router{ctx: context.Background()}
		if _, captured := router.routeHTTPDebug(ctx, tracked, metadata, outbound, func(error) {}); !captured {
			cancel()
			manager.CloseAllConnections()
			client.Close()
			t.Fatal("pre-sniffed pending TCP was not captured")
		}
		// Cache reads can start before the asynchronous outbound is admitted.
		// Stop must cancel an active handler, rather than skip a pending one.
		select {
		case <-outbound.started:
		case <-ctx.Done():
			cancel()
			manager.CloseAllConnections()
			client.Close()
			httpdebug.StopService()
			t.Fatal("pre-sniffed TCP never entered its selected outbound")
		}
		select {
		case <-observed.started:
		case <-ctx.Done():
			cancel()
			manager.CloseAllConnections()
			client.Close()
			httpdebug.StopService()
			t.Fatal("pre-sniffed TCP never started consuming its cache")
		}
		stopped := make(chan struct{})
		go func() { httpdebug.StopService(); close(stopped) }()
		select {
		case <-stopped:
		case <-ctx.Done():
			cancel()
			manager.CloseAllConnections()
			client.Close()
			t.Fatal("service stop waited for the cached TCP outbound to read")
		}
		select {
		case <-outbound.done:
		case <-ctx.Done():
			t.Error("service stop retained the pre-sniffed TCP route")
		}
		cancel()
		manager.CloseAllConnections()
		client.Close()
	}
}

func testBboxHTTPDebugPreSniffedTCPKeepsCacheCountersAndHalfClose(t *testing.T, directory string) {
	t.Helper()
	for _, capture := range []bool{false, true} {
		name, host, ports := "bypass", "cached-bypass.test", "[9002]"
		if capture {
			name, host, ports = "capture", "cached-capture.test", "[9001]"
		}
		t.Run(name, func(t *testing.T) {
			if err := httpdebug.Start(directory, `{"ports":`+ports+`,"rawTCP":true,"bodyLimit":-1}`, "", nil); err != nil {
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
			payload := bytes.Repeat([]byte{0, 255, 'c', 'a', 'c', 'h', 'e'}, 8192)
			reply := append([]byte("after cached request EOF:"), payload...)
			originDone := make(chan error, 1)
			go func() {
				conn, err := origin.Accept()
				if err != nil {
					originDone <- err
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(deadline)
				request, err := io.ReadAll(conn)
				if err == nil && !bytes.Equal(request, payload) {
					err = errors.New("cached request changed before origin EOF")
				}
				if err == nil {
					_, err = conn.Write(reply)
				}
				originDone <- err
			}()
			outbound := &captureHalfCloseOutbound{origin: origin.Addr().String(), metadata: make(chan adapter.InboundContext, 1), done: make(chan error, 1)}
			metadata := adapter.InboundContext{Network: "tcp", Protocol: "tcp", Inbound: "tun", Domain: host,
				Source: M.ParseSocksaddr("192.0.2.2:4321"), Destination: M.ParseSocksaddr("198.51.100.9:9101"),
				RouteOriginalDestination: M.ParseSocksaddr("198.51.100.9:9001"), RouteOutbound: outbound.Tag(),
				RouteRule: "unchanged-cached-route", TLSFragment: true, OutboundChain: []adapter.Outbound{outbound},
				ProcessInfo: &adapter.ConnectionOwner{UserId: 12345, PackageNames: []string{"com.test"}}}
			client, inbound := captureRealTCPPair(t, deadline)
			written := make(chan error, 1)
			go func() {
				_, err := client.Write(payload)
				if err == nil {
					err = client.CloseWrite()
				}
				written <- err
			}()
			// Match native route sniffing: cache read-ahead below the tracker and
			// set Protocol, so routeHTTPDebug must not add its own peek cache.
			prefix := buf.NewSize(256)
			if _, err = prefix.ReadFullFrom(inbound, 256); err != nil {
				prefix.Release()
				t.Fatal(err)
			}
			cached := newDebugCachedConn(inbound, prefix)
			manager := trafficcontrol.NewManager()
			defer manager.CloseAllConnections()
			tracked := manager.RoutedConnection(ctx, cached, metadata, nil, outbound)
			tracker, loaded := common.Cast[trafficcontrol.Tracker](tracked)
			if !loaded {
				t.Fatal("route cache hid the native connection tracker")
			}
			connectionID := tracker.Metadata().ID.String()
			router := &Router{ctx: context.Background()}
			replay, captured := router.routeHTTPDebug(ctx, tracked, metadata, outbound, func(error) {})
			if captured != capture {
				t.Fatal("pre-sniffed TCP cache changed capture selection", captured)
			}
			if !captured {
				outbound.NewConnection(ctx, replay, metadata, func(error) {})
			}
			response, err := io.ReadAll(client)
			if err != nil || !bytes.Equal(response, reply) {
				t.Fatal("pre-sniffed TCP lost its response after request half-close", len(response), err)
			}
			if err := <-written; err != nil {
				t.Fatal(err)
			}
			if err := <-outbound.done; err != nil {
				t.Fatal("native TCP copier lost cached replay or half-close", err)
			}
			if err := <-originDone; err != nil {
				t.Fatal(err)
			}
			if seen := <-outbound.metadata; !reflect.DeepEqual(seen, metadata) {
				t.Fatal("pre-sniffed TCP cache changed selected route metadata")
			}
			httpdebug.StopService()
			if tracker.Metadata().Upload.Load() != int64(len(payload)) || tracker.Metadata().Download.Load() != int64(len(reply)) {
				t.Fatal("cache replay lost or double-counted native tracker bytes", tracker.Metadata().Upload.Load(), tracker.Metadata().Download.Load())
			}
			if capture {
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
					if detail.Metadata.ParentID == "" {
						parents++
						if detail.Metadata.ConnectionID != connectionID || !detail.Request.BodyComplete || !detail.Response.BodyComplete ||
							detail.Request.BodyBytes != int64(len(payload)) || detail.Response.BodyBytes != int64(len(reply)) {
							t.Fatal("pre-sniffed capture lost tracker, cached bytes or EOF state")
						}
					}
				}
				if parents != 1 {
					t.Fatal("pre-sniffed TCP capture did not retain one connection record", parents)
				}
			}
		})
	}
}
