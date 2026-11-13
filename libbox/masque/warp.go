package masque

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"strings"
	"sync"
	"time"

	boxTLS "github.com/sagernet/sing-box/common/tls"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	transportHTTP "github.com/sagernet/sing-box/transport/http"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/ntp"
)

const warpServerName = "consumer-masque.cloudflareclient.com"

// Wire behavior is checked against warpscout 9e2ce957 and its pinned usque
// 6aa03fc97d12 / connect-ip-go 66cba32d7d33. WARP is not RFC 9484 CONNECT-IP:
// it uses cf-connect-ip, enrolled client keys and API-assigned addresses.
type WARPTransport struct {
	ctx       context.Context
	cancel    context.CancelFunc
	dialer    N.Dialer
	server    M.Socksaddr
	tlsConfig *tls.Config
	version   int
	mtu       uint32
	access    sync.Mutex
	peerCtx   context.Context
	peerClose context.CancelFunc
}

func NewWARPTransport(ctx context.Context, dialer N.Dialer, options option.MASQUEOutboundOptions) (*WARPTransport, error) {
	version := options.Version
	if version == 0 {
		version = 3
	}
	if version != 2 && version != 3 {
		return nil, E.New("WARP MASQUE version must be 2 or 3")
	}
	if version == 3 && !warpQUICIncluded {
		return nil, C.ErrQUICNotIncluded
	}
	tlsConfig, err := warpTLSConfig(ctx, options.PrivateKey, options.PeerPublicKey, options.ServerName)
	if err != nil {
		return nil, err
	}
	mtu := options.MTU
	if mtu == 0 {
		mtu = DefaultMTU
	}
	ctx, cancel := context.WithCancel(ctx)
	peerCtx, peerClose := context.WithCancel(ctx)
	return &WARPTransport{
		ctx: ctx, cancel: cancel, dialer: dialer, server: options.ServerOptions.Build(),
		tlsConfig: tlsConfig, version: version, mtu: mtu, peerCtx: peerCtx, peerClose: peerClose,
	}, nil
}

func (t *WARPTransport) OpenTunnel(ctx context.Context, _, _ string) (io.ReadWriteCloser, error) {
	t.access.Lock()
	peerCtx, cancel := context.WithCancel(t.peerCtx)
	t.access.Unlock()
	// The caller's timeout bounds the handshake only. The HTTP/2 request must
	// survive its cancellation after OpenTunnel returns successfully.
	stopDial := context.AfterFunc(ctx, cancel)
	var stream transportHTTP.DatagramStream
	var err error
	if t.version == 2 {
		stream, err = t.openHTTP2(peerCtx)
	} else {
		stream, err = t.openHTTP3(peerCtx)
	}
	if !stopDial() || ctx.Err() != nil {
		err = ctx.Err()
	}
	if err == nil {
		err = peerCtx.Err()
	}
	if err != nil {
		cancel()
		if stream != nil {
			stream.Close()
		}
		return nil, err
	}
	tunnel := &warpTunnel{DatagramStream: stream, ctx: peerCtx, cancel: cancel}
	// Closing the socket alone leaves HTTP/3 datagram readers waiting in the
	// HTTP layer. Reset and shutdown must close the whole authenticated tunnel.
	context.AfterFunc(peerCtx, func() { tunnel.Close() })
	return tunnel, nil
}

func (t *WARPTransport) ResetConnections() {
	t.access.Lock()
	defer t.access.Unlock()
	t.peerClose()
	t.peerCtx, t.peerClose = context.WithCancel(t.ctx)
}

func (t *WARPTransport) Close() error {
	t.access.Lock()
	defer t.access.Unlock()
	t.cancel()
	t.peerClose()
	return nil
}

type warpTunnel struct {
	transportHTTP.DatagramStream
	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
	err    error
}

func (t *warpTunnel) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	if err := t.ctx.Err(); err != nil {
		return nil, err
	}
	readCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(t.ctx, cancel)
	defer stop()
	defer cancel()
	return t.DatagramStream.ReceiveDatagram(readCtx)
}

func (t *warpTunnel) SendDatagram(payload []byte) error {
	if err := t.ctx.Err(); err != nil {
		return err
	}
	return t.DatagramStream.SendDatagram(payload)
}

func (t *warpTunnel) Close() error {
	t.once.Do(func() {
		t.cancel()
		t.err = t.DatagramStream.Close()
	})
	return t.err
}

func warpKeyDER(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if block, rest := pem.Decode([]byte(value)); block != nil {
		if len(strings.TrimSpace(string(rest))) != 0 {
			return nil, E.New("unexpected data after PEM key")
		}
		return block.Bytes, nil
	}
	return base64.StdEncoding.DecodeString(value)
}

func warpTLSConfig(ctx context.Context, privateKey, peerPublicKey, serverName string) (*tls.Config, error) {
	privateDER, err := warpKeyDER(privateKey)
	if err != nil {
		return nil, E.Cause(err, "decode WARP private_key")
	}
	private, err := x509.ParseECPrivateKey(privateDER)
	if err != nil {
		parsed, parseErr := x509.ParsePKCS8PrivateKey(privateDER)
		if parseErr != nil {
			return nil, E.New("WARP private_key must be an ECDSA P-256 SEC1 or PKCS#8 key")
		}
		private, _ = parsed.(*ecdsa.PrivateKey)
	}
	if private == nil || private.Curve != elliptic.P256() {
		return nil, E.New("WARP private_key must use ECDSA P-256")
	}
	publicDER, err := warpKeyDER(peerPublicKey)
	if err != nil {
		return nil, E.Cause(err, "decode WARP peer_public_key")
	}
	parsed, err := x509.ParsePKIXPublicKey(publicDER)
	if err != nil {
		return nil, E.Cause(err, "parse WARP peer_public_key")
	}
	public, ok := parsed.(*ecdsa.PublicKey)
	if !ok || public.Curve != elliptic.P256() {
		return nil, E.New("WARP peer_public_key must use ECDSA P-256")
	}
	publicDER, err = x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return nil, err
	}
	publicHash := sha256.Sum256(publicDER)
	if serverName == "" {
		serverName = warpServerName
	}
	if strings.ContainsAny(serverName, "\x00\r\n") {
		return nil, E.New("invalid WARP server_name")
	}
	now := ntp.TimeFuncFromContext(ctx)
	if now == nil {
		now = time.Now
	}
	return &tls.Config{
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
		Time:       now,
		// WARP's certificate does not authenticate the selectable SNI. Always
		// pin the leaf public key supplied by the account API instead.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return boxTLS.VerifyPinnedCertificate(nil, [][]byte{publicHash[:]}, rawCerts)
		},
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			// Renew only the certificate on each handshake; keep the enrolled key.
			certificate := &x509.Certificate{
				SerialNumber: big.NewInt(0),
				NotBefore:    now().Add(-time.Hour),
				NotAfter:     now().Add(24 * time.Hour),
				KeyUsage:     x509.KeyUsageDigitalSignature,
				ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			}
			der, signErr := x509.CreateCertificate(rand.Reader, certificate, certificate, &private.PublicKey, private)
			if signErr != nil {
				return nil, signErr
			}
			return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private}, nil
		},
	}, nil
}
