package option

import (
	"net/netip"

	"github.com/sagernet/sing/common/json/badoption"
)

// MASQUEOutboundOptions describes Cloudflare WARP's CONNECT-IP dialect.
// The standard masque-client and masque-server endpoints remain independent.
type MASQUEOutboundOptions struct {
	DialerOptions
	ServerOptions
	PrivateKey    string                           `json:"private_key"`
	PeerPublicKey string                           `json:"peer_public_key"`
	Address       badoption.Listable[netip.Prefix] `json:"address"`
	ServerName    string                           `json:"server_name,omitempty"`
	Version       int                              `json:"version,omitempty" enum:"2,3"`
	MTU           uint32                           `json:"mtu,omitempty"`
}
