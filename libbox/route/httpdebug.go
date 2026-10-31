package route

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/sniff"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/experimental/httpdebug"
	"github.com/sagernet/sing/common/buf"
	"github.com/sagernet/sing/common/bufio"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

type debugRewriteContextKey struct{}

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
	if ctx.Value(debugRewriteContextKey{}) != nil {
		return conn, false
	}
	engine := httpdebug.Current()
	if engine == nil || !engine.Candidate(debugMetadata(metadata)) {
		return conn, false
	}
	captureMetadata := metadata
	if captureMetadata.Protocol == "" {
		buffer := buf.NewSize(httpdebug.MaxHeaderBytes)
		err := sniff.PeekStream(ctx, &captureMetadata, conn, nil, buffer, time.Second, sniff.TLSClientHello, sniff.HTTPHost)
		if !buffer.IsEmpty() {
			conn = bufio.NewCachedConn(conn, buffer)
		} else {
			buffer.Release()
		}
		if err != nil {
			return conn, false
		}
	}
	info := debugMetadata(captureMetadata)
	if !engine.Matches(info) {
		if info.Protocol == "tls" {
			engine.Observe(info, "TLS")
		}
		return conn, false
	}
	// Unblock delayed SOCKS/HTTP inbound handshakes before acting as the server.
	if err := N.ReportConnHandshakeSuccess(conn, conn); err != nil {
		N.CloseOnHandshakeFailure(conn, onClose, err)
		return conn, true
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
	rewriteDial := func(dialCtx context.Context, address string) (net.Conn, error) {
		if err := dialCtx.Err(); err != nil {
			return nil, err
		}
		rewritten := debugRewriteMetadata(metadata, address)
		if !rewritten.Destination.IsValid() {
			return nil, errors.New("invalid rewritten destination")
		}
		local, routed := net.Pipe()
		dialCtx = context.WithValue(dialCtx, debugRewriteContextKey{}, true)
		dialCtx = adapter.WithContext(dialCtx, &rewritten)
		go r.RouteConnectionEx(dialCtx, routed, rewritten, func(error) { routed.Close() })
		return local, nil
	}
	return conn, engine.Handle(ctx, conn, info, dial, adapter.RootPoolFromContext(r.ctx), onClose, rewriteDial)
}

// A changed URL destination gets a fresh rule evaluation while retaining the
// original client identity. Do not carry DNS answers or prior rule actions over.
func debugRewriteMetadata(original adapter.InboundContext, address string) adapter.InboundContext {
	destination := M.ParseSocksaddr(address)
	return adapter.InboundContext{
		Inbound: original.Inbound, InboundType: original.InboundType, Network: N.NetworkTCP,
		Source: original.Source, User: original.User, Outbound: original.Outbound,
		Destination: destination, Domain: destination.Fqdn, ProcessInfo: original.ProcessInfo,
		SourceMACAddress: original.SourceMACAddress, SourceHostname: original.SourceHostname,
	}
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

// Only an observed QUIC ClientHello is eligible for rejection. Unknown SNI,
// and incomplete hellos are replayed unmodified to the original outbound.
func debugRejectQUIC(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, buffers []*N.PacketBuffer) ([]*N.PacketBuffer, bool) {
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
