package libbox

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const (
	uiWARPAPI               = "https://api.cloudflareclient.com/v0a4471"
	uiWARPWireGuardEndpoint = "engage.cloudflareclient.com:2408"
	uiWARPTimeout           = 45 * time.Second
	uiWARPMaxResponse       = 1 << 20
)

// UIWARPRegistrationTask registers one WARP device and returns only the
// sing-box proxy node. Device ID, API token and license never cross the binding.
// The Android caller admits this direct variant only while its service is stopped.
type UIWARPRegistrationTask struct {
	probe *UIEgressProbe
	used  atomic.Bool
}

func NewUIWARPRegistrationTask() *UIWARPRegistrationTask {
	return newUIWARPRegistrationTask(NewUIDirectEgressProbe())
}

// The running-service variant shares route admission, cancellation and teardown
// with egress probes, and never falls back to the device network after failure.
func (s *CommandServer) NewUIWARPRegistrationTask() (*UIWARPRegistrationTask, error) {
	probe, err := s.NewUIEgressProbe()
	if err != nil {
		return nil, err
	}
	return newUIWARPRegistrationTask(probe), nil
}
func newUIWARPRegistrationTask(probe *UIEgressProbe) *UIWARPRegistrationTask {
	// Registration is non-idempotent and its authorization token belongs to
	// this API origin. Return every redirect as a failed status without replay.
	probe.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &UIWARPRegistrationTask{probe: probe}
}
func (t *UIWARPRegistrationTask) Close() { t.probe.Close() }
func (t *UIWARPRegistrationTask) Register(name string, httpVersion int32) (string, error) {
	return t.register(name, httpVersion, false)
}

// RegisterWireGuard returns a native sing-box WireGuard endpoint without
// replacing its Curve25519 registration key with a MASQUE key.
func (t *UIWARPRegistrationTask) RegisterWireGuard(name string) (string, error) {
	return t.register(name, 0, true)
}

func (t *UIWARPRegistrationTask) register(name string, httpVersion int32, wireGuard bool) (string, error) {
	if !t.used.CompareAndSwap(false, true) {
		return "", fmt.Errorf("WARP registration task was already used")
	}
	defer t.Close()
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "\x00\r\n") {
		return "", fmt.Errorf("invalid WARP node name")
	}
	if httpVersion != 0 && httpVersion != 2 && httpVersion != 3 {
		return "", fmt.Errorf("WARP HTTP version must be 0, 2 or 3")
	}
	ctx, cancel := context.WithTimeout(t.probe.ctx, uiWARPTimeout)
	defer cancel()
	if wireGuard {
		return uiWARPRegisterWireGuard(ctx, t.probe, uiWARPAPI, name)
	}
	return uiWARPRegister(ctx, t.probe, uiWARPAPI, name, httpVersion)
}

type uiWARPDevice struct {
	ID     string `json:"id"`
	Token  string `json:"token"`
	Config struct {
		ClientID  string `json:"client_id"`
		Interface struct {
			Addresses struct {
				V4 string `json:"v4"`
				V6 string `json:"v6"`
			} `json:"addresses"`
		} `json:"interface"`
		Peers []struct {
			PublicKey string `json:"public_key"`
			Endpoint  struct {
				Host string `json:"host"`
				V4   string `json:"v4"`
				V6   string `json:"v6"`
			} `json:"endpoint"`
		} `json:"peers"`
	} `json:"config"`
}

// Behavior reference: Throne 48273be2 core/internal/warp. Both tunnels first
// acquire a Curve25519 device identity. Retrying POST after an ambiguous
// failure would create another device.
func uiWARPRegisterDevice(ctx context.Context, probe *UIEgressProbe, api string) (*uiWARPDevice, *ecdh.PrivateKey, error) {
	wgKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("WARP: could not generate registration key")
	}
	// crypto/ecdh clamps only while multiplying; store the same clamped
	// Curve25519 scalar that WireGuard's key generator exports.
	privateKey := wgKey.Bytes()
	privateKey[0] &= 248
	privateKey[31] &= 127
	privateKey[31] |= 64
	wgKey, err = ecdh.X25519().NewPrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("WARP: could not generate registration key")
	}
	serial := make([]byte, 8)
	if _, err = rand.Read(serial); err != nil {
		return nil, nil, fmt.Errorf("WARP: could not generate device identity")
	}
	registered, err := uiWARPCall(ctx, probe, api+"/reg", http.MethodPost, "", map[string]string{
		"key": base64.StdEncoding.EncodeToString(wgKey.PublicKey().Bytes()), "install_id": "", "fcm_token": "",
		"tos": time.Now().UTC().Format("2006-01-02T15:04:05.000-07:00"), "model": "PC", "serial_number": hex.EncodeToString(serial),
		"key_type": "curve25519", "tunnel_type": "wireguard", "locale": "en_US",
	}, "register device")
	if err != nil {
		return nil, nil, err
	}
	if !uiWARPDeviceID(registered.ID) || registered.Token == "" || strings.ContainsAny(registered.Token, "\x00\r\n") {
		return nil, nil, fmt.Errorf("WARP register device: invalid device credentials in response")
	}
	return registered, wgKey, nil
}

// MASQUE enrolls a P-256 key on the device acquired by the first API call.
// RegisterWireGuard keeps that device's original Curve25519 key instead.
func uiWARPRegister(ctx context.Context, probe *UIEgressProbe, api, name string, httpVersion int32) (string, error) {
	registered, _, err := uiWARPRegisterDevice(ctx, probe, api)
	if err != nil {
		return "", err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", fmt.Errorf("WARP: could not generate MASQUE key")
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return "", fmt.Errorf("WARP: could not encode MASQUE key")
	}
	privateDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", fmt.Errorf("WARP: could not encode MASQUE key")
	}
	enrolled, err := uiWARPCall(ctx, probe, api+"/reg/"+registered.ID, http.MethodPatch, registered.Token,
		map[string]string{"key": base64.StdEncoding.EncodeToString(publicDER), "key_type": "secp256r1", "tunnel_type": "masque"}, "enroll MASQUE key")
	if err != nil {
		return "", err
	}
	addresses, err := uiWARPAddresses(enrolled)
	if err != nil {
		return "", err
	}
	if len(enrolled.Config.Peers) == 0 {
		return "", fmt.Errorf("WARP enrollment response: missing peer public key")
	}
	peer, err := uiWARPPeerKey(enrolled.Config.Peers[0].PublicKey)
	if err != nil {
		return "", err
	}
	// A complete valid enrollment response owns a real registered key. Return
	// it even if cancellation wins immediately afterward, so Android can retain
	// the pending node while independently refusing a cancelled config commit.
	node := map[string]any{
		"type": "masque", "tag": name, "server": "162.159.198.2", "server_port": 443,
		"private_key": base64.StdEncoding.EncodeToString(privateDER), "peer_public_key": peer, "address": addresses,
		"http_version": httpVersion, "mtu": 1280,
		"tls": map[string]any{"enabled": true, "server_name": "consumer-masque.cloudflareclient.com"},
	}
	// .1 returned by the API is a CDN edge on TCP; .2 supports both carriers.
	content, err := json.Marshal(node)
	if err != nil {
		return "", fmt.Errorf("WARP: could not serialize node")
	}
	return string(content), nil
}

func uiWARPRegisterWireGuard(ctx context.Context, probe *UIEgressProbe, api, name string) (string, error) {
	registered, key, err := uiWARPRegisterDevice(ctx, probe, api)
	if err != nil {
		return "", err
	}
	addresses, err := uiWARPAddresses(registered)
	if err != nil {
		return "", err
	}
	if len(registered.Config.Peers) == 0 {
		return "", fmt.Errorf("WARP registration response: missing WireGuard peer")
	}
	peer := registered.Config.Peers[0]
	publicKey, err := base64.StdEncoding.Strict().DecodeString(peer.PublicKey)
	if err != nil || len(publicKey) != 32 || bytes.Equal(publicKey, make([]byte, 32)) || strings.ContainsAny(peer.PublicKey, "\x00\r\n") {
		return "", fmt.Errorf("WARP registration response: invalid WireGuard peer public key")
	}
	reserved, err := base64.StdEncoding.Strict().DecodeString(registered.Config.ClientID)
	if err != nil || len(reserved) != 3 || strings.ContainsAny(registered.Config.ClientID, "\x00\r\n") {
		return "", fmt.Errorf("WARP registration response: invalid WireGuard client_id")
	}
	endpoint, family := peer.Endpoint.Host, 0
	if endpoint == "" {
		endpoint, family = peer.Endpoint.V4, 4
	}
	if endpoint == "" {
		endpoint, family = peer.Endpoint.V6, 6
	}
	if endpoint == "" {
		endpoint, family = uiWARPWireGuardEndpoint, 0
	}
	host, port, err := uiWARPParseWireGuardEndpoint(endpoint, family)
	if err != nil {
		return "", err
	}
	// []byte is encoded as a Base64 string by encoding/json. The endpoint
	// schema expects the three reserved bytes as a JSON array of integers.
	reservedValues := []int{int(reserved[0]), int(reserved[1]), int(reserved[2])}
	// Keep a completed registration at the same cancellation boundary as
	// MASQUE, so Android can retain its node without committing a cancelled save.
	node := map[string]any{
		"type": "wireguard", "tag": name, "address": addresses,
		"private_key": base64.StdEncoding.EncodeToString(key.Bytes()), "mtu": 1280,
		"peers": []any{map[string]any{
			"address": host, "port": port, "public_key": base64.StdEncoding.EncodeToString(publicKey),
			"allowed_ips": []string{"0.0.0.0/0", "::/0"}, "reserved": reservedValues,
		}},
	}
	content, err := json.Marshal(node)
	if err != nil {
		return "", fmt.Errorf("WARP: could not serialize node")
	}
	return string(content), nil
}

func uiWARPParseWireGuardEndpoint(endpoint string, family int) (string, uint16, error) {
	invalid := fmt.Errorf("WARP registration response: invalid WireGuard endpoint")
	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil || host == "" || portText == "" || strings.IndexFunc(portText, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return "", 0, invalid
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return "", 0, invalid
	}
	if address, err := netip.ParseAddr(host); err == nil {
		if address.IsUnspecified() || address.IsMulticast() || address.Is4In6() || address.Zone() != "" ||
			family == 4 && !address.Is4() || family == 6 && !address.Is6() {
			return "", 0, invalid
		}
		return address.String(), uint16(port), nil
	}
	if family != 0 || len(host) > 253 {
		return "", 0, invalid
	}
	for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", 0, invalid
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
				return "", 0, invalid
			}
		}
	}
	return strings.ToLower(host), uint16(port), nil
}

func uiWARPDeviceID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}
func uiWARPCall(ctx context.Context, probe *UIEgressProbe, link, method, token string, body any, phase string) (*uiWARPDevice, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !probe.current() {
		return nil, fmt.Errorf("WARP: local service changed during registration")
	}
	content, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("WARP %s: could not encode request", phase)
	}
	// Egress's route dialer needs the original request context even when the
	// HTTP transport detaches the dial context for connection pooling.
	requestCtx := context.WithValue(ctx, uiEgressRequestContextKey{}, ctx)
	request, err := http.NewRequestWithContext(requestCtx, method, link, bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("WARP %s: invalid API request", phase)
	}
	request.Header.Set("User-Agent", "WARP for Android")
	request.Header.Set("CF-Client-Version", "a-6.35-4471")
	request.Header.Set("Content-Type", "application/json; charset=UTF-8")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := probe.client.Do(request)
	if err != nil {
		return nil, uiWARPNetworkError(ctx, phase, err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusTooManyRequests {
			return nil, fmt.Errorf("WARP %s: Cloudflare rate limited registration; try again later", phase)
		}
		// Never return the API body, status text or URL: any can echo credentials.
		return nil, fmt.Errorf("WARP %s: HTTP %d", phase, response.StatusCode)
	}
	content, err = io.ReadAll(io.LimitReader(response.Body, uiWARPMaxResponse+1))
	if err != nil {
		return nil, uiWARPNetworkError(ctx, phase, err)
	}
	if len(content) > uiWARPMaxResponse {
		return nil, fmt.Errorf("WARP %s: API response too large", phase)
	}
	var result uiWARPDevice
	if json.Unmarshal(content, &result) != nil {
		return nil, fmt.Errorf("WARP %s: invalid API response", phase)
	}
	return &result, nil
}
func uiWARPNetworkError(ctx context.Context, phase string, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return fmt.Errorf("WARP %s: network timeout", phase)
	}
	return fmt.Errorf("WARP %s: network request failed", phase)
}
func uiWARPAddresses(device *uiWARPDevice) ([]string, error) {
	var addresses []string
	for index, text := range []string{device.Config.Interface.Addresses.V4, device.Config.Interface.Addresses.V6} {
		if text == "" {
			continue
		}
		address, err := netip.ParseAddr(text)
		if err != nil || address.Is4In6() || address.IsUnspecified() || address.IsMulticast() || address.Zone() != "" || address.Is4() != (index == 0) {
			return nil, fmt.Errorf("WARP response: invalid interface address")
		}
		addresses = append(addresses, netip.PrefixFrom(address, address.BitLen()).String())
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("WARP response: missing interface addresses")
	}
	return addresses, nil
}
func uiWARPPeerKey(text string) (string, error) {
	var der []byte
	if block, rest := pem.Decode([]byte(text)); block != nil {
		if strings.TrimSpace(string(rest)) != "" || block.Type != "PUBLIC KEY" {
			return "", fmt.Errorf("WARP enrollment response: invalid peer public key")
		}
		der = block.Bytes
	} else {
		var err error
		der, err = base64.StdEncoding.DecodeString(text)
		if err != nil {
			return "", fmt.Errorf("WARP enrollment response: invalid peer public key")
		}
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return "", fmt.Errorf("WARP enrollment response: invalid peer public key")
	}
	key, ok := parsed.(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() {
		return "", fmt.Errorf("WARP enrollment response: peer public key must use P-256")
	}
	der, err = x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return "", fmt.Errorf("WARP enrollment response: invalid peer public key")
	}
	return base64.StdEncoding.EncodeToString(der), nil
}
