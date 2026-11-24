package route

import (
	"context"
	"encoding/binary"
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
			if !engine.WantsRawTCP(debugMetadata(metadata)) {
				return conn, false
			}
			captureMetadata.Protocol = "tcp"
		}
	}
	info := debugMetadata(captureMetadata)
	if info.Protocol != "http" && info.Protocol != "tls" && engine.WantsRawTCP(info) {
		info.Protocol, info.RawTCP = "tcp", true
	}
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
		var local, routed net.Conn
		if info.RawTCP {
			local, routed = newDebugStreamPipe()
		} else {
			local, routed = net.Pipe()
		}
		var stopCancel func() bool
		if info.RawTCP {
			stopCancel = context.AfterFunc(dialCtx, func() { _ = local.Close(); _ = routed.Close() })
		}
		// Reuse the exact selected outbound's ConnectionHandler, including route
		// address resolution, network strategy, TLS fragmentation and spoofing.
		go func() {
			closed := func(error) {
				if stopCancel != nil {
					stopCancel()
				}
				_ = routed.Close()
			}
			if info.RawTCP && dialCtx.Err() != nil {
				closed(dialCtx.Err())
				return
			}
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
		return newDebugCachedConn(conn, buffer), err
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

type debugQUICSelection struct {
	engine   *httpdebug.Engine
	metadata httpdebug.Metadata
}

// Observe a copy after routing, then replay every datagram through the normal
// tracking/NAT wrappers. Only a genuine HTTP/3 offer may be intercepted. Other
// QUIC protocols, unknown names and unmatched flows retain their selected route.
func debugObserveQUIC(ctx context.Context, conn N.PacketConn, metadata adapter.InboundContext, buffers []*N.PacketBuffer) ([]*N.PacketBuffer, *debugQUICSelection, bool) {
	if ctx.Value(httpRewriteRouteKey{}) != nil || httpdebug.ProcessingDisabled(ctx) {
		return buffers, nil, false
	}
	engine := httpdebug.Current()
	if engine == nil || !engine.WantsQUIC(debugMetadata(metadata)) {
		return buffers, nil, false
	}
	if metadata.Protocol != "" && metadata.Protocol != C.ProtocolQUIC {
		return buffers, nil, false
	}
	// The actual ClientHello must establish SNI and ALPN even when a routing
	// rule already sniffed a name. Do not treat a DNS mapping as inspected SNI.
	metadata.Domain, metadata.Protocol, metadata.SniffContext = "", "", nil
	deadline := time.Now().Add(time.Second)
	for i := 0; ; i++ {
		if i >= len(buffers) {
			if ctx.Err() != nil || !time.Now().Before(deadline) {
				break
			}
			packet := buf.NewPacket()
			if err := conn.SetReadDeadline(deadline); err != nil {
				packet.Release()
				break
			}
			deadlineDone := make(chan struct{})
			stop := context.AfterFunc(ctx, func() {
				_ = conn.SetReadDeadline(time.Now())
				close(deadlineDone)
			})
			destination, err := conn.ReadPacket(packet)
			if !stop() {
				<-deadlineDone
			}
			_ = conn.SetReadDeadline(time.Time{})
			if err != nil {
				packet.Release()
				break
			}
			buffer := N.NewPacketBuffer()
			*buffer = N.PacketBuffer{Buffer: packet, Destination: destination}
			buffers = append(buffers, buffer)
		}
		var clientHello []byte
		err := sniff.QUICClientHelloDebug(ctx, &metadata, buffers[i].Buffer.Bytes(), func(hello []byte) {
			clientHello = append([]byte(nil), hello...)
		})
		if err == nil {
			info := debugMetadata(metadata)
			packet := buffers[i].Buffer.Bytes()
			version := binary.BigEndian.Uint32(packet[1:5])
			if selected, matched := engine.SelectQUIC(info, clientHello); matched && (version == 1 || version == 0x6b3343cf) {
				return buffers, &debugQUICSelection{engine: engine, metadata: selected}, false
			}
			if metadata.Domain != "" {
				return buffers, nil, engine.RejectQUIC(info)
			}
			break
		}
		if !errors.Is(err, sniff.ErrNeedMoreData) {
			break
		}
	}
	return buffers, nil, false
}
