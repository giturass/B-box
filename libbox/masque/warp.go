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
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	transportHTTP "github.com/sagernet/sing-box/transport/http"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json/badoption"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/common/ntp"
)

const (
	warpServerName           = "consumer-masque.cloudflareclient.com"
	warpHTTP3FallbackTimeout = 5 * time.Second
)

// Independently implements the WARP behavior checked against Throne 48273be2
// and its sing-box fork 183cf759ed90. WARP uses cf-connect-ip, a registered
// P-256 client key and API-assigned IP addresses, rather than RFC 9484 enrollment.
type WARPTransport struct {
	ctx        context.Context
	cancel     context.CancelFunc
	logger     log.ContextLogger
	dialer     N.Dialer
	server     M.Socksaddr
	options    option.MASQUEOutboundOptions
	tlsOptions option.OutboundTLSOptions
	privateKey *ecdsa.PrivateKey
	version    int
	fallback   bool
	access     sync.Mutex
	peerCtx    context.Context
	peerClose  context.CancelFunc
}

func NewWARPTransport(ctx context.Context, dialer N.Dialer, options option.MASQUEOutboundOptions) (*WARPTransport, error) {
	return NewWARPTransportWithLogger(ctx, log.NewNOPFactory().Logger(), dialer, options)
}

func NewWARPTransportWithLogger(ctx context.Context, logger log.ContextLogger, dialer N.Dialer, options option.MASQUEOutboundOptions) (*WARPTransport, error) {
	version := options.HTTPVersion
	fallback := !options.DisableVersionFallback
	if options.Version != 0 {
		if version != 0 && version != options.Version {
			return nil, E.New("conflicting WARP version and http_version")
		}
		version = options.Version
		// Preserve the explicit carrier choice made by older B-box configs.
		fallback = false
	}
	if version != 0 && version != 2 && version != 3 {
		return nil, E.New("WARP http_version must be 0, 2 or 3")
	}
	if version == 0 {
		version = 3
	}
	if version == 2 {
		fallback = false
	}
	if version == 3 && !warpQUICIncluded {
		if !fallback {
			return nil, C.ErrQUICNotIncluded
		}
		version, fallback = 2, false
	}
	if options.InitialPacketSize != 0 && (options.InitialPacketSize < 1200 || options.InitialPacketSize > 65535) {
		return nil, E.New("WARP initial_packet_size must be between 1200 and 65535")
	}
	if options.IdleTimeout < 0 || options.KeepAlivePeriod < 0 || options.MaxConcurrentStreams < 0 {
		return nil, E.New("invalid WARP carrier timeout or stream limit")
	}
	privateKey, err := warpPrivateKey(options.PrivateKey)
	if err != nil {
		return nil, err
	}
	peerHash, err := warpPeerHash(options.PeerPublicKey)
	if err != nil {
		return nil, err
	}
	tlsOptions := option.OutboundTLSOptions{Enabled: true, ServerName: warpServerName}
	if options.TLS != nil {
		tlsOptions = *options.TLS
		if !tlsOptions.Enabled {
			return nil, C.ErrTLSRequired
		}
	}
	if options.ServerName != "" {
		if tlsOptions.ServerName != "" && options.TLS != nil && tlsOptions.ServerName != options.ServerName {
			return nil, E.New("conflicting WARP server_name and tls.server_name")
		}
		tlsOptions.ServerName = options.ServerName
	}
	if strings.ContainsAny(tlsOptions.ServerName, "\x00\r\n") {
		return nil, E.New("invalid WARP server_name")
	}
	if tlsOptions.Reality != nil && tlsOptions.Reality.Enabled {
		return nil, E.New("WARP MASQUE does not support REALITY")
	}
	if len(tlsOptions.ClientCertificate) > 0 || tlsOptions.ClientCertificatePath != "" || len(tlsOptions.ClientKey) > 0 || tlsOptions.ClientKeyPath != "" {
		return nil, E.New("WARP client certificate and key are derived from private_key")
	}
	if len(tlsOptions.Certificate) > 0 || tlsOptions.CertificatePath != "" || len(tlsOptions.CertificateSHA256) > 0 || len(tlsOptions.CertificatePublicKeySHA256) > 0 {
		return nil, E.New("WARP peer_public_key conflicts with TLS certificate settings")
	}
	tlsOptions.CertificatePublicKeySHA256 = badoption.Listable[[]byte]{peerHash}
	if options.ServerPort == 0 {
		options.ServerPort = 443
	}
	ctx, cancel := context.WithCancel(ctx)
	peerCtx, peerClose := context.WithCancel(ctx)
	t := &WARPTransport{ctx: ctx, cancel: cancel, logger: logger, dialer: dialer, server: options.ServerOptions.Build(), options: options,
		tlsOptions: tlsOptions, privateKey: privateKey, version: version, fallback: fallback, peerCtx: peerCtx, peerClose: peerClose}
	// Validate the actual core TLS options while checking configuration, without
	// opening a socket. The client certificate is renewed for each handshake.
	_, err = t.tlsConfig(version)
	if err == nil && fallback {
		// Both enabled carriers must accept the configured TLS engine/options.
		_, err = t.tlsConfig(2)
	}
	if err != nil {
		t.Close()
		return nil, err
	}
	return t, nil
}

func (t *WARPTransport) clientTLSOptions() (option.OutboundTLSOptions, error) {
	now := ntp.TimeFuncFromContext(t.ctx)
	if now == nil {
		now = time.Now
	}
	instant := now()
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), SignatureAlgorithm: x509.ECDSAWithSHA256,
		NotBefore: instant.Add(-time.Minute), NotAfter: instant.Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &t.privateKey.PublicKey, t.privateKey)
	if err != nil {
		return option.OutboundTLSOptions{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(t.privateKey)
	if err != nil {
		return option.OutboundTLSOptions{}, err
	}
	options := t.tlsOptions
	options.ClientCertificate = badoption.Listable[string]{string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))}
	options.ClientKey = badoption.Listable[string]{string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}))}
	return options, nil
}

func (t *WARPTransport) tlsConfig(version int) (boxTLS.Config, error) {
	options, err := t.clientTLSOptions()
	if err != nil {
		return nil, err
	}
	if version == 3 {
		return boxTLS.NewSTDClient(t.ctx, t.logger, t.options.Server, options)
	}
	return boxTLS.NewClient(t.ctx, t.logger, t.options.Server, options)
}

func (t *WARPTransport) OpenTunnel(ctx context.Context, _, _ string) (io.ReadWriteCloser, error) {
	t.access.Lock()
	peerCtx, cancel := context.WithCancel(t.peerCtx)
	version := t.version
	t.access.Unlock()
	// A handshake deadline does not become the established stream's lifetime.
	stopDial := context.AfterFunc(ctx, cancel)
	stream, err := t.openCarrier(ctx, peerCtx, version)
	if err != nil && t.fallback && ctx.Err() == nil && peerCtx.Err() == nil {
		alternate := 5 - version
		t.logger.Warn("WARP HTTP/", version, " unavailable, trying HTTP/", alternate)
		stream, err = t.openCarrier(ctx, peerCtx, alternate)
		if err == nil {
			t.access.Lock()
			t.version = alternate
			t.access.Unlock()
		}
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
	context.AfterFunc(peerCtx, func() { tunnel.Close() })
	return tunnel, nil
}

func (t *WARPTransport) openCarrier(parent, lifetime context.Context, version int) (transportHTTP.DatagramStream, error) {
	timeout := C.TCPTimeout
	if version == 3 && t.fallback {
		timeout = warpHTTP3FallbackTimeout
	}
	base, cancelLifetime := context.WithCancel(parent)
	stopLifetime := context.AfterFunc(lifetime, cancelLifetime)
	defer stopLifetime()
	defer cancelLifetime()
	ctx, cancel := context.WithTimeout(base, timeout)
	defer cancel()
	if version == 2 {
		return t.openHTTP2(ctx, lifetime)
	}
	return t.openHTTP3(ctx, lifetime)
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
	t.once.Do(func() { t.cancel(); t.err = t.DatagramStream.Close() })
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
func warpPrivateKey(content string) (*ecdsa.PrivateKey, error) {
	der, err := warpKeyDER(content)
	if err != nil {
		return nil, E.New("invalid WARP private_key encoding")
	}
	private, err := x509.ParseECPrivateKey(der)
	if err != nil {
		parsed, parseErr := x509.ParsePKCS8PrivateKey(der)
		if parseErr != nil {
			return nil, E.New("WARP private_key must be an ECDSA P-256 SEC1 or PKCS#8 key")
		}
		private, _ = parsed.(*ecdsa.PrivateKey)
	}
	if private == nil || private.Curve != elliptic.P256() {
		return nil, E.New("WARP private_key must use ECDSA P-256")
	}
	return private, nil
}
func warpPeerHash(content string) ([]byte, error) {
	der, err := warpKeyDER(content)
	if err != nil {
		return nil, E.New("invalid WARP peer_public_key encoding")
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, E.New("invalid WARP peer_public_key")
	}
	public, ok := parsed.(*ecdsa.PublicKey)
	if !ok || public.Curve != elliptic.P256() {
		return nil, E.New("WARP peer_public_key must use ECDSA P-256")
	}
	der, err = x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(der)
	return hash[:], nil
}

// Kept as a small credential fixture for the transport regression tests.
func warpTLSConfig(ctx context.Context, privateKey, peerPublicKey, serverName string) (*tls.Config, error) {
	t, err := NewWARPTransport(ctx, N.SystemDialer, option.MASQUEOutboundOptions{PrivateKey: privateKey, PeerPublicKey: peerPublicKey, ServerName: serverName, Version: 2})
	if err != nil {
		return nil, err
	}
	defer t.Close()
	config, err := t.tlsConfig(3)
	if err != nil {
		return nil, err
	}
	return config.STDConfig()
}
