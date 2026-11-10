package route

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/sniff"
	"github.com/sagernet/sing-box/common/trafficcontrol"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/httpdebug"
	"github.com/sagernet/sing/common"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type httpRewriteRouteKey struct{}

func debugMetadata(m adapter.InboundContext) httpdebug.Metadata {
	host := m.Domain
	if host == "" {
		host = m.Destination.AddrString()
	}
	result := httpdebug.Metadata{Host: host, Port: m.Destination.Port, Protocol: m.Protocol,
		Source: m.Source.String(), Destination: m.Destination.String(), Inbound: m.Inbound,
		Outbound: m.RouteOutbound, Rule: m.RouteRule, UID: -1, Packages: []string{}}
	if m.OriginDestination.IsValid() {
		result.OriginalDestination = m.OriginDestination.String()
	}
	if m.RouteOriginalDestination.IsValid() {
		result.OriginalDestination = m.RouteOriginalDestination.String()
		result.Port = m.RouteOriginalDestination.Port
	}
	if m.ProcessInfo != nil {
		result.UID = m.ProcessInfo.UserId
		result.Packages = append(result.Packages, m.ProcessInfo.PackageNames...)
	}
	return result
}

// The route is already selected. Sniff a COPY solely for capture selection;
// never feed a recovered name or an HTTP Host back into routing or DNS.
func (r *Router) routeHTTPDebug(ctx context.Context, conn net.Conn, metadata adapter.InboundContext, outbound adapter.Outbound, onClose N.CloseHandlerFunc) (net.Conn, bool) {
	if ctx.Value(httpRewriteRouteKey{}) != nil || httpdebug.ProcessingDisabled(ctx) {
		return conn, false
	}
	engine := httpdebug.Current()
	if engine == nil || !engine.Candidate(debugMetadata(metadata)) {
		return conn, false
	}
	// A CONNECT/SOCKS client may wait for success before sending ClientHello.
	// This is the same early tunnel acknowledgment needed by route sniffing.
	if err := N.ReportConnHandshakeSuccess(conn, conn); err != nil {
		N.CloseOnHandshakeFailure(conn, onClose, err)
		return conn, true
	}
	captureMetadata := metadata
	if captureMetadata.Protocol == "" {
		var err error
		conn, err = peekHTTPDebug(ctx, &captureMetadata, conn)
		if err != nil {
			return conn, false
		}
	}
	info := debugMetadata(captureMetadata)
	// Routing registered the client connection before this hook. Unwrap sniffing
	// caches and other upstream-aware wrappers to read the same UUID as libbox.
	// Keep it on the original metadata even if an HTTP rewrite routes a new dial.
	if tracker, loaded := common.Cast[trafficcontrol.Tracker](conn); loaded {
		info.ConnectionID = tracker.Metadata().ID.String()
	}
	if info.Protocol == "tls" {
		var selected bool
		conn, info, selected = engine.SelectTLS(ctx, conn, info)
		if !selected {
			engine.Observe(info, "TLS")
			return conn, false
		}
	} else if !engine.Matches(info) {
		return conn, false
	}
	dial := func(dialCtx context.Context) (net.Conn, error) {
		if err := dialCtx.Err(); err != nil {
			return nil, err
		}
		local, routed := net.Pipe()
		// Reuse the exact selected outbound's ConnectionHandler, including route
		// address resolution, network strategy, TLS fragmentation and spoofing.
		go func() {
			closed := func(error) { routed.Close() }
			if handler, ok := outbound.(adapter.ConnectionHandler); ok {
				handler.NewConnection(dialCtx, routed, metadata, closed)
			} else {
				r.connection.NewConnection(dialCtx, outbound, routed, metadata, closed)
			}
		}()
		return local, nil
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
		// Re-evaluate the new destination through sing-box. Preserve source and
		// ownership, but never reuse the old resolved addresses, route overrides,
		// rule caches, outbound chain or sniffed domain. Avoid recursive rewriting.
		mappedMetadata := adapter.InboundContext{Inbound: metadata.Inbound, InboundType: metadata.InboundType,
			Source: metadata.Source, User: metadata.User, Network: N.NetworkTCP, Destination: destination,
			ProcessInfo: metadata.ProcessInfo, SourceMACAddress: metadata.SourceMACAddress, SourceHostname: metadata.SourceHostname}
		mappedCtx := adapter.WithContext(context.WithValue(dialCtx, httpRewriteRouteKey{}, true), &mappedMetadata)
		go r.RouteConnectionEx(mappedCtx, routed, mappedMetadata, func(error) { routed.Close() })
		return local, nil
	}
	return conn, engine.HandleRouted(ctx, conn, info, dial, mapped, adapter.RootPoolFromContext(r.ctx), onClose)
}

// Keep upstream's protocol-detection fallback and replay semantics, but grow
// its read-ahead buffer when it needs more bytes. Buffer capacity must not also
// act as a limit on valid HTTP headers or fragmented TLS records.
func peekHTTPDebug(ctx context.Context, metadata *adapter.InboundContext, conn net.Conn) (net.Conn, error) {
	buffer := buf.New()
	deadline := time.Now().Add(time.Second)
	var err error
	for {
		if err = ctx.Err(); err != nil {
			break
		}
		err = sniff.PeekStream(ctx, metadata, conn, nil, buffer, time.Until(deadline), sniff.TLSClientHello, sniff.HTTPHost)
		if !errors.Is(err, sniff.ErrNeedMoreData) || !buffer.IsFull() || !time.Now().Before(deadline) {
			break
		}
		if buffer.Cap() > int(^uint(0)>>1)/2 {
			break // Cannot represent a larger buffer length.
		}
		grown := buf.NewSize(buffer.Cap() * 2)
		_, _ = grown.Write(buffer.Bytes())
		buffer.Release()
		buffer = grown
	}
	if !buffer.IsEmpty() {
		return bufio.NewCachedConn(conn, buffer), err
	}
	buffer.Release()
	return conn, err
}

func debugNeedsStream(m adapter.InboundContext) bool {
	engine := httpdebug.Current()
	if engine == nil {
		return false
	}
	if m.Network == N.NetworkTCP {
		return engine.Candidate(debugMetadata(m))
	}
	return m.Network == N.NetworkUDP && engine.WantsQUIC(debugMetadata(m))
}

// Only a recovered QUIC ClientHello name can trigger the opt-in TCP fallback.
// Other UDP and all buffered datagrams of unmatched flows stay unchanged.
func debugObserveQUIC(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, buffers []*N.PacketBuffer) ([]*N.PacketBuffer, bool) {
	engine := httpdebug.Current()
	if engine == nil || !engine.WantsQUIC(debugMetadata(metadata)) {
		return buffers, false
	}
	if metadata.Protocol != "" && metadata.Protocol != C.ProtocolQUIC {
		return buffers, false
	}
	if metadata.Protocol == C.ProtocolQUIC && metadata.Domain != "" {
		return buffers, engine.RejectQUIC(debugMetadata(metadata))
	}
	metadata.Domain = ""
	for i := 0; i < 3; i++ {
		if i >= len(buffers) {
			packet := buf.NewPacket()
			if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
				packet.Release()
				break
			}
			destination, err := conn.ReadPacket(packet)
			_ = conn.SetReadDeadline(time.Time{})
			if err != nil {
				packet.Release()
				break
			}
			buffer := N.NewPacketBuffer()
			*buffer = N.PacketBuffer{Buffer: packet, Destination: destination}
			buffers = append(buffers, buffer)
		}
		err := sniff.QUICClientHello(ctx, &metadata, buffers[i].Buffer.Bytes())
		if err == nil && metadata.Domain != "" {
			return buffers, engine.RejectQUIC(debugMetadata(metadata))
		}
		if !errors.Is(err, sniff.ErrNeedMoreData) {
			break
		}
	}
	return buffers, false
}
