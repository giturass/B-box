//go:build !with_quic

package masque

import (
	"context"

	C "github.com/sagernet/sing-box/constant"
	transportHTTP "github.com/sagernet/sing-box/transport/http"
)

const warpQUICIncluded = false

func (*WARPTransport) openHTTP3(context.Context) (transportHTTP.DatagramStream, error) {
	return nil, C.ErrQUICNotIncluded
}
