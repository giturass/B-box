//go:build !with_quic

package httpdebug

import (
	"context"
	"crypto/x509"
	"net"
)

const http3Included = false

func (e *Engine) HandleQUIC(context.Context, net.PacketConn, Metadata, PacketDialFunc, RewriteDialFunc, *x509.CertPool, func(error)) bool {
	return false
}
