package option

import (
	"net/netip"

	"github.com/sagernet/sing/common/json/badoption"
)

// MASQUEOutboundOptions keeps B-box's outbound layout while using Throne's
// WARP carrier and TLS fields. RFC 9484 masque-client endpoints are independent.
type MASQUEOutboundOptions struct {
	DialerOptions
	ServerOptions
	OutboundTLSOptionsContainer
	QUICOptions
	PrivateKey             string                           `json:"private_key"`
	PeerPublicKey          string                           `json:"peer_public_key"`
	Address                badoption.Listable[netip.Prefix] `json:"address"`
	HTTPVersion            int                              `json:"http_version,omitempty" enum:"0,2,3"`
	DisableVersionFallback bool                             `json:"disable_version_fallback,omitempty"`
	MTU                    uint32                           `json:"mtu,omitempty"`
	// Compatibility with configurations written by the earlier B-box adapter.
	Version    int    `json:"version,omitempty" enum:"2,3"`
	ServerName string `json:"server_name,omitempty"`
}
