package libbox

// LAN sharing changes sing-box's native proxy inbound listen addresses.
// There is no Mihomo allow-lan flag or single global mixed-port.
import (
	"bytes"
	stdjson "encoding/json"
	"fmt"
	"net"
	"net/netip"
	"slices"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"
)

type uiLANAddress struct {
	Address   string `json:"address"`
	Interface string `json:"interface"`
}

type uiLANListener struct {
	Type           string   `json:"type"`
	Tag            string   `json:"tag"`
	Listen         string   `json:"listen"`
	Port           uint16   `json:"port"`
	BindInterface  string   `json:"bindInterface"`
	LAN            bool     `json:"lan"`
	TLS            bool     `json:"tls"`
	Authentication bool     `json:"authentication"`
	Endpoints      []string `json:"endpoints"`
}

func uiProxyInbound(inboundType string) bool {
	return inboundType == C.TypeMixed || inboundType == C.TypeHTTP || inboundType == C.TypeSOCKS
}

// ReadUILANProxyListeners expands wildcard binds to device addresses, while
// respecting explicit addresses, address families and bind_interface.
// Port zero is assigned by the OS and cannot be advertised from configuration.
func ReadUILANProxyListeners(configContent, interfaceAddresses string) (*StringBox, error) {
	options, err := parseConfig(baseContext(nil), configContent)
	if err != nil {
		return nil, err
	}
	var addresses []uiLANAddress
	if err = stdjson.Unmarshal([]byte(interfaceAddresses), &addresses); err != nil {
		return nil, err
	}
	listeners := make([]uiLANListener, 0)
	for _, inbound := range options.Inbounds {
		if !uiProxyInbound(inbound.Type) {
			continue
		}
		wrapper, ok := inbound.Options.(option.ListenOptionsWrapper)
		if !ok {
			continue
		}
		listenOptions := wrapper.TakeListenOptions()
		// common/listener/listener_tcp.go defaults omitted listen to loopback.
		listen := listenOptions.Listen.Build(netip.MustParseAddr("127.0.0.1")).Unmap()
		item := uiLANListener{
			Type: inbound.Type, Tag: inbound.Tag, Listen: listen.String(),
			Port: listenOptions.ListenPort, BindInterface: listenOptions.BindInterface,
			LAN: !listen.IsLoopback() && (listen.IsUnspecified() || listen.IsGlobalUnicast() || listen.IsLinkLocalUnicast()),
			Endpoints: make([]string, 0),
		}
		switch typed := inbound.Options.(type) {
		case *option.HTTPMixedInboundOptions:
			item.TLS = typed.TLS != nil && typed.TLS.Enabled
			item.Authentication = len(typed.Users) > 0
		case *option.HTTPInboundOptions:
			item.TLS = typed.TLS != nil && typed.TLS.Enabled
			item.Authentication = len(typed.Users) > 0
		case *option.SocksInboundOptions:
			item.Authentication = len(typed.Users) > 0
		}
		if item.LAN && item.Port != 0 && listenOptions.NetNs == "" {
			for _, device := range addresses {
				if item.BindInterface != "" && item.BindInterface != device.Interface {
					continue
				}
				address, parseErr := netip.ParseAddr(device.Address)
				if parseErr != nil {
					continue
				}
				address = address.Unmap()
				if !address.IsGlobalUnicast() {
					continue
				}
				if listen.IsUnspecified() {
					// sing-box uses net.Listen("tcp"); :: accepts IPv4 on Android's
					// dual-stack socket, whereas 0.0.0.0 accepts only IPv4.
					if listen.Is4() && !address.Is4() {
						continue
					}
				} else if listen != address {
					continue
				}
				endpoint := net.JoinHostPort(address.String(), fmt.Sprint(item.Port))
				if !slices.Contains(item.Endpoints, endpoint) {
					item.Endpoints = append(item.Endpoints, endpoint)
				}
			}
		}
		listeners = append(listeners, item)
	}
	encoded, err := stdjson.Marshal(listeners)
	if err != nil {
		return nil, err
	}
	return wrapString(string(encoded)), nil
}

// SetUILANProxyEnabled preserves ports, tags, authentication, TLS and routing.
// An explicit LAN bind is retained when enabling; disabling restricts proxy
// inbounds to loopback. initialPort is used only when no proxy inbound exists.
func SetUILANProxyEnabled(configContent string, enabled bool, initialPort int32) (*StringBox, error) {
	options, err := parseConfig(baseContext(nil), configContent)
	if err != nil {
		return nil, err
	}
	found := false
	for _, inbound := range options.Inbounds {
		if !uiProxyInbound(inbound.Type) {
			continue
		}
		wrapper, ok := inbound.Options.(option.ListenOptionsWrapper)
		if !ok {
			continue
		}
		found = true
		listenOptions := wrapper.TakeListenOptions()
		address := listenOptions.Listen.Build(netip.MustParseAddr("127.0.0.1")).Unmap()
		if enabled && !address.IsLoopback() || !enabled && address.IsLoopback() {
			continue
		}
		var next netip.Addr
		if enabled {
			if address.Is4() {
				next = netip.IPv4Unspecified()
			} else {
				next = netip.IPv6Unspecified()
			}
		} else if address.Is4() {
			next = netip.MustParseAddr("127.0.0.1")
		} else {
			next = netip.IPv6Loopback()
		}
		converted := badoption.Addr(next)
		listenOptions.Listen = &converted
		wrapper.ReplaceListenOptions(listenOptions)
	}
	if enabled && !found {
		if initialPort < 1 || initialPort > 65535 {
			return nil, fmt.Errorf("choose a proxy port between 1 and 65535")
		}
		tags := make(map[string]bool)
		for _, inbound := range options.Inbounds {
			tags[inbound.Tag] = true
			if wrapper, ok := inbound.Options.(option.ListenOptionsWrapper); ok && wrapper.TakeListenOptions().ListenPort == uint16(initialPort) {
				return nil, fmt.Errorf("port %d is already used by this profile", initialPort)
			}
		}
		tag := "b-box-lan"
		for suffix := 2; tags[tag]; suffix++ {
			tag = fmt.Sprintf("b-box-lan-%d", suffix)
		}
		listen := badoption.Addr(netip.IPv4Unspecified())
		options.Inbounds = append(options.Inbounds, option.Inbound{
			Type: C.TypeMixed, Tag: tag,
			Options: &option.HTTPMixedInboundOptions{
				ListenOptions: option.ListenOptions{Listen: &listen, ListenPort: uint16(initialPort)},
			},
		})
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(options); err != nil {
		return nil, err
	}
	return wrapString(buffer.String()), nil
}
