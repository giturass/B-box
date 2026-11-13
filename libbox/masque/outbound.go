package masque

import (
	"context"
	"net/netip"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/dialer"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/transport/masque"
	E "github.com/sagernet/sing/common/exceptions"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service"
)

func RegisterOutbound(registry *outbound.Registry) {
	outbound.Register[option.MASQUEOutboundOptions](registry, C.TypeMASQUE, NewWARPOutbound)
}

func NewWARPOutbound(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options option.MASQUEOutboundOptions) (adapter.Outbound, error) {
	if options.Server == "" || options.ServerPort == 0 {
		return nil, E.New("WARP MASQUE requires server and server_port")
	}
	if options.MTU == 0 {
		options.MTU = masque.DefaultMTU
	}
	if options.MTU < 1280 || options.MTU > 65535 {
		return nil, E.New("WARP MASQUE mtu must be between 1280 and 65535")
	}
	if err := validateWARPAddresses(options.Address); err != nil {
		return nil, err
	}
	outboundDialer, err := dialer.NewWithOptions(dialer.Options{
		Context:          ctx,
		Options:          options.DialerOptions,
		RemoteIsDomain:   options.ServerIsDomain(),
		ResolverOnDetour: true,
		NewDialer:        true,
	})
	if err != nil {
		return nil, err
	}
	transport, err := masque.NewWARPTransport(ctx, outboundDialer, options)
	if err != nil {
		return nil, err
	}
	clientEndpoint := &ClientEndpoint{
		endpointBase: endpointBase{
			Adapter: endpoint.NewAdapterWithDialerOptions(C.TypeMASQUE, tag, []string{N.NetworkTCP, N.NetworkUDP, N.NetworkICMP}, options.DialerOptions),
			router:  router,
			logger:  logger,
		},
		ctx:       ctx,
		dnsRouter: service.FromContext[adapter.DNSRouter](ctx),
		mtu:       options.MTU,
		// A dial waits for the authenticated tunnel; it must not report success
		// merely because the reconnect loop has been started.
		onDemand: true,
	}
	clientEndpoint.state.Store(&clientState{})
	clientEndpoint.deviceOptions = newDeviceOptions(ctx, logger, clientEndpoint,
		option.MASQUEEndpointOptions{MTU: options.MTU}, 0, nil)
	clientEndpoint.client, err = masque.NewClient(masque.ClientOptions{
		Context:      ctx,
		Logger:       logger,
		HTTPClient:   transport,
		Path:         "/",
		LocalAddress: options.Address,
		Handler:      clientEndpoint,
	})
	if err != nil {
		transport.Close()
		return nil, err
	}
	return clientEndpoint, nil
}

func validateWARPAddresses(addresses []netip.Prefix) error {
	if len(addresses) == 0 {
		return E.New("WARP MASQUE requires the account's address")
	}
	var hasIPv4, hasIPv6 bool
	for _, prefix := range addresses {
		address := prefix.Addr()
		if !prefix.IsValid() || !prefix.IsSingleIP() || address.Is4In6() ||
			address.IsUnspecified() || address.IsMulticast() || address.Zone() != "" {
			return E.New("WARP MASQUE address must contain unicast /32 or /128 host addresses")
		}
		if address.Is4() {
			if hasIPv4 {
				return E.New("WARP MASQUE accepts one IPv4 address")
			}
			hasIPv4 = true
		} else {
			if hasIPv6 {
				return E.New("WARP MASQUE accepts one IPv6 address")
			}
			hasIPv6 = true
		}
	}
	return nil
}
