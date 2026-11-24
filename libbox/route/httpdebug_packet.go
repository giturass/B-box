package route

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	"github.com/sagernet/sing-box/experimental/httpdebug"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func (r *Router) routeQUICDebug(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, outbound adapter.Outbound, selection *debugQUICSelection, onClose N.CloseHandlerFunc) bool {
	if selection == nil || ctx.Value(httpRewriteRouteKey{}) != nil || httpdebug.ProcessingDisabled(ctx) {
		return false
	}
	info := debugMetadata(metadata)
	info.Host, info.Protocol = selection.metadata.Host, "tls"
	// Preserve inspected SNI provenance while filling route/app identity after
	// tracking. The QUIC TLS server checks SNI again during its real handshake.
	info = selection.metadataWithRoute(info)
	if tracker, loaded := common.Cast[trafficcontrol.Tracker](conn); loaded {
		info.ConnectionID = tracker.Metadata().ID.String()
	}
	client := &debugClientPacketConn{PacketConn: conn, source: metadata.Source, reply: metadata.Destination}
	dial := func(dialCtx context.Context) (net.PacketConn, net.Addr, error) {
		if err := dialCtx.Err(); err != nil {
			return nil, nil, err
		}
		local, routed := newDebugPacketPipe(metadata.Source, metadata.Destination)
		routeDone := make(chan struct{})
		closed := N.OnceClose(func(error) {
			_ = routed.Close()
			close(routeDone)
		})
		go func() {
			if handler, ok := outbound.(adapter.PacketConnectionHandler); ok {
				handler.NewPacketConnection(dialCtx, routed, metadata, closed)
			} else {
				r.connection.NewPacketConnection(dialCtx, outbound, routed, metadata, closed)
			}
		}()
		owned := &debugRoutedPacketConn{PacketConn: local, done: routeDone}
		return &debugFixedPacketPeer{PacketConn: owned, peer: metadata.Destination}, metadata.Destination, nil
	}
	mapped := func(dialCtx context.Context, address string) (net.Conn, error) {
		if err := dialCtx.Err(); err != nil {
			return nil, err
		}
		destination := M.ParseSocksaddr(address)
		if !destination.IsValid() || destination.Port == 0 {
			return nil, errors.New("invalid HTTP rewrite destination")
		}
		local, routed := net.Pipe()
		mappedMetadata := adapter.InboundContext{Inbound: metadata.Inbound, InboundType: metadata.InboundType,
			Source: metadata.Source, User: metadata.User, Network: N.NetworkTCP, Destination: destination,
			ProcessInfo: metadata.ProcessInfo, SourceMACAddress: metadata.SourceMACAddress, SourceHostname: metadata.SourceHostname}
		mappedCtx := adapter.WithContext(context.WithValue(dialCtx, httpRewriteRouteKey{}, true), &mappedMetadata)
		go r.RouteConnectionEx(mappedCtx, routed, mappedMetadata, func(error) { _ = routed.Close() })
		return local, nil
	}
	return selection.engine.HandleQUIC(ctx, client, info, dial, mapped, adapter.RootPoolFromContext(r.ctx), onClose)
}

func (s *debugQUICSelection) metadataWithRoute(route httpdebug.Metadata) httpdebug.Metadata {
	// Preserve private SNI provenance from SelectQUIC, and replace only the
	// public fields that normal route tracking fills after ClientHello sniffing.
	result := s.metadata
	result.ConnectionID, result.Source, result.Destination = route.ConnectionID, route.Source, route.Destination
	result.OriginalDestination, result.Inbound = route.OriginalDestination, route.Inbound
	result.Outbound, result.Rule, result.UID, result.Packages = route.Outbound, route.Rule, route.UID, route.Packages
	result.Port = route.Port
	return result
}

// sing's inbound packet interface reports the original destination on reads
// and expects that destination on replies. QUIC instead expects the client's
// address. Keep both identities distinct, including fake-IP and route overrides.
type debugClientPacketConn struct {
	N.PacketConn
	source M.Socksaddr
	mu     sync.Mutex
	reply  M.Socksaddr
	read   bool
}

func (c *debugClientPacketConn) ReadFrom(p []byte) (int, net.Addr, error) {
	buffer := buf.NewPacket()
	defer buffer.Release()
	destination, err := c.PacketConn.ReadPacket(buffer)
	if err != nil {
		return 0, nil, err
	}
	c.mu.Lock()
	if !c.read {
		c.reply, c.read = destination, true
	}
	c.mu.Unlock()
	n := copy(p, buffer.Bytes())
	return n, c.source.UDPAddr(), nil
}

func (c *debugClientPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) {
	c.mu.Lock()
	reply := c.reply
	c.mu.Unlock()
	writer := bufio.NewNetPacketWriter(c.PacketConn)
	return writer.WriteTo(p, reply)
}

// The UDP handler may report the resolved server IP. QUIC still identifies its
// peer by the route's original address; normalizing it does not resolve DNS.
type debugFixedPacketPeer struct {
	net.PacketConn
	peer M.Socksaddr
}

type debugRoutedPacketConn struct {
	net.PacketConn
	done <-chan struct{}
}

func (c *debugRoutedPacketConn) Close() error {
	err := c.PacketConn.Close()
	<-c.done
	return err
}

func (c *debugRoutedPacketConn) CloseGracefully() error {
	writer, loaded := common.Cast[interface{ CloseWrite() error }](c.PacketConn)
	if !loaded {
		return errors.ErrUnsupported
	}
	if err := writer.CloseWrite(); err != nil {
		return err
	}
	// The selected handler receives EOF on its next read, after forwarding the
	// current datagram to its physical UDP socket. Keep its route context alive
	// until onClose; the owner's shutdown watchdog can still force full close.
	<-c.done
	return nil
}

func (c *debugFixedPacketPeer) CloseGracefully() error {
	if closer, loaded := c.PacketConn.(interface{ CloseGracefully() error }); loaded {
		return closer.CloseGracefully()
	}
	return errors.ErrUnsupported
}

func (c *debugFixedPacketPeer) ReadFrom(p []byte) (int, net.Addr, error) {
	n, _, err := c.PacketConn.ReadFrom(p)
	return n, c.peer, err
}

func (c *debugFixedPacketPeer) WriteTo(p []byte, _ net.Addr) (int, error) {
	return c.PacketConn.WriteTo(p, c.peer)
}

// A packet pipe hands the original UDP metadata to the selected outbound's
// PacketConnectionHandler, preserving DNS strategy, detours and Android VPN
// protection. It cannot fall back to a direct socket or a different outbound.
type debugPacketPipe struct {
	read, write    chan debugPipeDatagram
	readDone       <-chan struct{}
	writeDone      chan struct{}
	writeCloseOnce sync.Once
	done           chan struct{}
	closeOnce      *sync.Once
	local          net.Addr
	readLimit      *debugPacketDeadline
	writeLimit     *debugPacketDeadline
}

type debugPipeDatagram struct {
	data []byte
	peer net.Addr
}

func newDebugPacketPipe(source, destination M.Socksaddr) (N.NetPacketConn, N.NetPacketConn) {
	forward, reverse := make(chan debugPipeDatagram), make(chan debugPipeDatagram)
	forwardDone, reverseDone := make(chan struct{}), make(chan struct{})
	done, once := make(chan struct{}), new(sync.Once)
	left := &debugPacketPipe{read: reverse, write: forward, readDone: reverseDone, writeDone: forwardDone, done: done, closeOnce: once, local: source,
		readLimit: newDebugPacketDeadline(), writeLimit: newDebugPacketDeadline()}
	right := &debugPacketPipe{read: forward, write: reverse, readDone: forwardDone, writeDone: reverseDone, done: done, closeOnce: once, local: destination,
		readLimit: newDebugPacketDeadline(), writeLimit: newDebugPacketDeadline()}
	return bufio.NewPacketConn(left), bufio.NewPacketConn(right)
}

func (p *debugPacketPipe) ReadFrom(buffer []byte) (int, net.Addr, error) {
	for {
		changed, expired := p.readLimit.channels()
		select {
		case <-p.done:
			return 0, nil, net.ErrClosed
		case <-p.readDone:
			return 0, nil, io.EOF
		case <-expired:
			return 0, nil, os.ErrDeadlineExceeded
		default:
		}
		select {
		case <-p.done:
			return 0, nil, net.ErrClosed
		case <-p.readDone:
			return 0, nil, io.EOF
		case <-expired:
			return 0, nil, os.ErrDeadlineExceeded
		case <-changed:
			continue
		case packet := <-p.read:
			return copy(buffer, packet.data), packet.peer, nil
		}
	}
}

func (p *debugPacketPipe) WriteTo(buffer []byte, address net.Addr) (int, error) {
	packet := debugPipeDatagram{data: append([]byte(nil), buffer...), peer: address}
	for {
		changed, expired := p.writeLimit.channels()
		select {
		case <-p.done:
			return 0, net.ErrClosed
		case <-p.writeDone:
			return 0, net.ErrClosed
		case <-expired:
			return 0, os.ErrDeadlineExceeded
		default:
		}
		select {
		case <-p.done:
			return 0, net.ErrClosed
		case <-p.writeDone:
			return 0, net.ErrClosed
		case <-expired:
			return 0, os.ErrDeadlineExceeded
		case <-changed:
			continue
		case p.write <- packet:
			return len(buffer), nil
		}
	}
}

func (p *debugPacketPipe) CloseWrite() error {
	p.writeCloseOnce.Do(func() { close(p.writeDone) })
	return nil
}

func (p *debugPacketPipe) Close() error {
	p.closeOnce.Do(func() { close(p.done) })
	p.readLimit.set(time.Time{})
	p.writeLimit.set(time.Time{})
	return nil
}

func (p *debugPacketPipe) LocalAddr() net.Addr { return p.local }
func (p *debugPacketPipe) SetDeadline(deadline time.Time) error {
	p.readLimit.set(deadline)
	p.writeLimit.set(deadline)
	return nil
}
func (p *debugPacketPipe) SetReadDeadline(deadline time.Time) error {
	p.readLimit.set(deadline)
	return nil
}
func (p *debugPacketPipe) SetWriteDeadline(deadline time.Time) error {
	p.writeLimit.set(deadline)
	return nil
}

type debugPacketDeadline struct {
	mu      sync.Mutex
	changed chan struct{}
	expired chan struct{}
	timer   *time.Timer
}

func newDebugPacketDeadline() *debugPacketDeadline {
	return &debugPacketDeadline{changed: make(chan struct{}), expired: make(chan struct{})}
}

func (d *debugPacketDeadline) channels() (<-chan struct{}, <-chan struct{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.changed, d.expired
}

func (d *debugPacketDeadline) set(deadline time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.timer != nil {
		d.timer.Stop()
		d.timer = nil
	}
	close(d.changed)
	d.changed, d.expired = make(chan struct{}), make(chan struct{})
	if deadline.IsZero() {
		return
	}
	expired := d.expired
	if !time.Now().Before(deadline) {
		close(expired)
		return
	}
	d.timer = time.AfterFunc(time.Until(deadline), func() {
		d.mu.Lock()
		defer d.mu.Unlock()
		if expired == d.expired {
			close(expired)
		}
	})
}
