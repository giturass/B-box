package libbox

// Share links of proxy nodes. Links come from panels and clipboards, so the
// reader is tolerant: unknown parameters are ignored, and a link is refused
// only when a parameter this core cannot honour would change what the node
// does. A refusal never quotes the link or anything taken from it.
import (
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	E "github.com/sagernet/sing/common/exceptions"
)

// uiNodeLink is a link split by hand. net/url rejects much of what panels
// write, and its errors repeat the input together with the credentials.
type uiNodeLink struct {
	scheme      string
	name        string
	body        string // everything between the scheme and the query
	user        string
	password    string
	hasPassword bool
	host        string
	port        string
	path        string
	query       map[string]string
	xhttp       *uiNodeObject // already converted Clash XHTTP settings
}

// uiNodeUnescape leaves a plus sign alone: keys and passwords are written
// into links unescaped far more often than a space is written as a plus.
func uiNodeUnescape(text string) string {
	if decoded, err := url.PathUnescape(text); err == nil {
		return decoded
	}
	return text
}

func uiNodeSplitLink(text string) *uiNodeLink {
	scheme, rest, found := strings.Cut(strings.TrimSpace(text), "://")
	if !found {
		return nil
	}
	link := &uiNodeLink{scheme: strings.ToLower(scheme), query: make(map[string]string)}
	rest, name, _ := strings.Cut(rest, "#")
	link.name = strings.TrimSpace(uiNodeUnescape(name))
	rest, query, _ := strings.Cut(rest, "?")
	for _, pair := range strings.Split(query, "&") {
		key, value, _ := strings.Cut(pair, "=")
		key = uiNodeUnescape(key)
		if _, exists := link.query[key]; !exists && key != "" {
			link.query[key] = uiNodeUnescape(value)
		}
	}
	link.body = rest
	// A slash may belong to the credentials (standard base64, a raw password),
	// so the address is what follows the last at sign, up to the next slash.
	address := rest
	if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		user, password, hasPassword := strings.Cut(rest[:at], ":")
		link.user, link.password, link.hasPassword = uiNodeUnescape(user), uiNodeUnescape(password), hasPassword
		address = rest[at+1:]
	}
	address, link.path, _ = strings.Cut(address, "/")
	link.setAddress(address)
	return link
}

// setAddress splits server:port. An IPv6 server is bracketed when a port
// follows, and the port part of a Hysteria link may be a list.
func (l *uiNodeLink) setAddress(address string) {
	l.host, l.port = "", ""
	if strings.HasPrefix(address, "[") {
		if host, port, closed := strings.Cut(address[1:], "]"); closed {
			l.host, l.port = host, strings.TrimPrefix(port, ":")
		}
	} else if _, err := netip.ParseAddr(address); err == nil || !strings.Contains(address, ":") {
		l.host = address
	} else {
		index := strings.LastIndexByte(address, ':')
		l.host, l.port = address[:index], address[index+1:]
	}
	l.host = strings.TrimSpace(l.host)
}

// secret is the user information read as one credential: a password may
// contain a colon that was left unescaped.
func (l *uiNodeLink) secret() string {
	if l.hasPassword {
		return l.user + ":" + l.password
	}
	return l.user
}

// portNumber reads a single port; a link without one takes the fallback.
func (l *uiNodeLink) portNumber(fallback int) (int, bool) {
	if l.port == "" {
		return fallback, fallback > 0
	}
	port, err := strconv.Atoi(l.port)
	return port, err == nil && port >= 1 && port <= 65535
}

// value returns the first of the parameters that is present and not blank.
func (l *uiNodeLink) value(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(l.query[key]); value != "" {
			return value
		}
	}
	return ""
}

func (l *uiNodeLink) flag(keys ...string) bool {
	return uiNodeTruthy(l.value(keys...))
}

func uiNodeTruthy(text string) bool {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "1", "true":
		return true
	}
	return false
}

func uiNodePut(object *uiNodeObject, key, value string) {
	if value != "" {
		object.set(key, value)
	}
}

func uiNodeCommaList(text string) []any {
	var list []any
	for _, item := range strings.Split(text, ",") {
		if item = strings.TrimSpace(item); item != "" {
			list = append(list, item)
		}
	}
	return list
}

// uiNodeDigits turns decimal text into a JSON number without passing it
// through a fixed-width integer.
func uiNodeDigits(text string) (uiNodeNumber, bool) {
	text = strings.TrimSpace(text)
	if text == "" || len(text) > 20 {
		return "", false
	}
	for _, digit := range text {
		if digit < '0' || digit > '9' {
			return "", false
		}
	}
	if text = strings.TrimLeft(text, "0"); text == "" {
		text = "0"
	}
	return uiNodeNumber(text), true
}

func uiNodePutDigits(object *uiNodeObject, key, text string) {
	if number, valid := uiNodeDigits(text); valid {
		object.set(key, number)
	}
}

// uiNodeBase64 accepts both alphabets, padded or not.
func uiNodeBase64(text string) ([]byte, bool) {
	text = strings.TrimRight(strings.Join(strings.Fields(text), ""), "=")
	if text == "" {
		return nil, false
	}
	decoded, err := base64.RawStdEncoding.DecodeString(strings.NewReplacer("-", "+", "_", "/").Replace(text))
	return decoded, err == nil
}

func uiNodeBase64Text(text string) (string, bool) {
	decoded, valid := uiNodeBase64(text)
	if !valid || !utf8.Valid(decoded) {
		return "", false
	}
	return string(decoded), true
}

// uiNodeLinkNode starts a node from the parts every link has.
func uiNodeLinkNode(link *uiNodeLink, nodeType string, fallbackPort int) (*uiNodeObject, error) {
	if link.host == "" {
		return nil, E.New("the node has no server address")
	}
	port, valid := link.portNumber(fallbackPort)
	if !valid {
		return nil, E.New("the server port must be a number between 1 and 65535")
	}
	name := link.name
	if name == "" {
		name = link.value("remarks", "remark")
	}
	if name == "" {
		name = net.JoinHostPort(link.host, strconv.Itoa(port))
	}
	return uiNodeOf("type", nodeType, "tag", name, "server", link.host, "server_port", uiNodeInt(port)), nil
}

// uiNodePins converts certificate pins, written as hexadecimal SHA-256 with
// optional colons, to the base64 of certificate_sha256. The Hysteria clients
// also write dashes and base64, which loose admits.
func uiNodePins(text string, loose bool) ([]any, error) {
	var pins []any
	for _, item := range uiNodeCommaList(text) {
		pin := item.(string)
		compact := strings.ReplaceAll(pin, ":", "")
		if loose {
			compact = strings.ReplaceAll(compact, "-", "")
		}
		hash, err := hex.DecodeString(compact)
		if err != nil || len(hash) != 32 {
			decoded, valid := uiNodeBase64(pin)
			if !loose || !valid || len(decoded) != 32 {
				return nil, E.New("a certificate pin of this node is not a SHA-256 hash")
			}
			hash = decoded
		}
		pins = append(pins, base64.StdEncoding.EncodeToString(hash))
	}
	return pins, nil
}

// uiNodeLinkECH reads the ech parameter, which is a switch, a base64 config
// list, or a name to query followed by a resolver this core does not take.
func uiNodeLinkECH(link *uiNodeLink) *uiNodeObject {
	value := link.value("ech")
	enabled := value != "" && value != "0" && !strings.EqualFold(value, "false")
	config, queryName := link.value("ech_config"), link.value("ech_server_name")
	if enabled && value != "1" && !strings.EqualFold(value, "true") {
		name, _, hasResolver := strings.Cut(value, "+")
		switch {
		case !strings.Contains(value, "://") && !strings.Contains(value, "."):
			config = value
		case queryName != "":
		case hasResolver && !strings.Contains(name, "://"):
			queryName = strings.TrimSpace(name)
		case !strings.Contains(value, "://"):
			queryName = value
		}
	}
	if switched := link.value("ech_enabled"); switched != "" {
		enabled = uiNodeTruthy(switched)
	}
	if !enabled {
		return nil
	}
	options := uiNodeOf("enabled", true)
	var body strings.Builder
	for _, line := range uiNodeCommaList(strings.ReplaceAll(config, "\n", ",")) {
		if text := line.(string); !strings.Contains(text, "-----") {
			body.WriteString(text)
		}
	}
	if decoded, valid := uiNodeBase64(body.String()); valid {
		options.set("config", []any{
			"-----BEGIN ECH CONFIGS-----",
			base64.StdEncoding.EncodeToString(decoded),
			"-----END ECH CONFIGS-----",
		})
	}
	uiNodePut(options, "query_server_name", queryName)
	return options
}

type uiNodeTLSMode int

const (
	uiNodeTLSOptional uiNodeTLSMode = iota // on when the link asks for it
	uiNodeTLSDefault                       // on unless the link turns it off
	uiNodeTLSAlways                        // the protocol cannot run without it
	uiNodeTLSQUIC                          // always on; uTLS and REALITY do not apply
)

// The names the core's uTLS client knows.
var uiNodeFingerprints = []string{"chrome", "firefox", "edge", "safari", "360", "qq", "ios", "android", "random", "randomized"}

func uiNodeLinkTLS(link *uiNodeLink, mode uiNodeTLSMode) (*uiNodeObject, error) {
	security := strings.ToLower(link.value("security"))
	serverName, publicKey := link.value("sni", "peer"), link.value("pbk")
	switch security {
	case "tls", "xtls", "reality", "true", "1":
	case "none", "false", "0":
		if mode < uiNodeTLSAlways {
			return nil, nil
		}
	default:
		// A link that names a server expects TLS even when it does not say so.
		if mode == uiNodeTLSOptional && serverName == "" && publicKey == "" {
			return nil, nil
		}
	}
	options := uiNodeOf("enabled", true)
	if link.flag("disable_sni") {
		options.set("disable_sni", true)
	}
	uiNodePut(options, "server_name", serverName)
	insecure := false
	for _, key := range []string{"allowInsecure", "allow_insecure", "insecure"} {
		if value := link.value(key); value != "" {
			insecure = uiNodeTruthy(value)
		}
	}
	if insecure {
		options.set("insecure", true)
	}
	if alpn := uiNodeCommaList(link.value("alpn")); len(alpn) > 0 {
		options.set("alpn", alpn)
	}
	pins, err := uiNodePins(link.value("pcs"), false)
	if err == nil && len(pins) == 0 {
		pins, err = uiNodePins(link.value("pinSHA256"), mode == uiNodeTLSQUIC)
	}
	if err != nil {
		return nil, err
	}
	if len(pins) > 0 {
		options.set("certificate_sha256", pins)
	}
	if ech := uiNodeLinkECH(link); ech != nil {
		options.set("ech", ech)
	}
	if mode == uiNodeTLSQUIC {
		return options, nil
	}
	if link.value("tls_fragment") == "true" {
		options.set("fragment", true)
	}
	if link.value("tls_record_fragment") == "true" {
		options.set("record_fragment", true)
	}
	fingerprint := strings.ToLower(link.value("fp"))
	if !slices.Contains(uiNodeFingerprints, fingerprint) {
		fingerprint = ""
	}
	if security != "reality" && publicKey == "" {
		if fingerprint != "" {
			options.set("utls", uiNodeOf("enabled", true, "fingerprint", fingerprint))
		}
		return options, nil
	}
	// Dropping the ML-DSA-65 key would weaken the check of the server.
	if link.value("pqv") != "" {
		return nil, uiNodeUnsupported("REALITY with an ML-DSA-65 verification key is not supported by this core")
	}
	key, valid := uiNodeBase64(publicKey)
	if !valid || len(key) != 32 {
		return nil, E.New("REALITY needs the public key of the server")
	}
	shortID := link.value("sid")
	if _, err = hex.DecodeString(shortID); err != nil || len(shortID) > 16 {
		return nil, E.New("the REALITY short ID must be at most 16 hexadecimal digits")
	}
	if fingerprint == "" {
		fingerprint = "chrome"
	}
	reality := uiNodeOf("enabled", true, "public_key", base64.RawURLEncoding.EncodeToString(key))
	uiNodePut(reality, "short_id", shortID)
	options.set("utls", uiNodeOf("enabled", true, "fingerprint", fingerprint))
	options.set("reality", reality)
	return options, nil
}

// uiNodeLinkTransport builds the V2Ray transport of a link; nil is plain TCP.
func uiNodeLinkTransport(link *uiNodeLink) (*uiNodeObject, error) {
	host, path := link.value("host"), link.value("path")
	var transport *uiNodeObject
	switch strings.ToLower(link.value("type")) {
	case "", "tcp", "raw":
		if !strings.EqualFold(link.value("headerType"), "http") {
			return nil, nil
		}
		// The HTTP camouflage of a raw connection. Its server accepts any of
		// the listed paths and this core sends one. The core would send PUT.
		transport = uiNodeOf("type", "http", "method", "GET")
		if paths := uiNodeCommaList(path); len(paths) > 0 {
			path = paths[0].(string)
		}
	case "h2":
		transport = uiNodeOf("type", "http", "method", "GET")
	case "http":
		transport = uiNodeOf("type", "http")
	case "ws", "websocket":
		transport = uiNodeOf("type", "ws")
	case "grpc":
		transport = uiNodeOf("type", "grpc")
		uiNodePut(transport, "service_name", link.value("serviceName", "service_name"))
	case "httpupgrade":
		transport = uiNodeOf("type", "httpupgrade")
		uiNodePut(transport, "host", host)
		uiNodePut(transport, "path", path)
	case "quic":
		for _, key := range []string{"quicSecurity", "headerType"} {
			if value := link.value(key); value != "" && !strings.EqualFold(value, "none") {
				return nil, uiNodeUnsupported("the QUIC transport of this core takes neither encryption nor a header")
			}
		}
		transport = uiNodeOf("type", "quic")
	case "xhttp", "splithttp":
		return uiNodeXHTTPLink(link)
	default:
		return nil, uiNodeUnsupported("the transport of this node is not supported by this core")
	}
	switch transport.text("type") {
	case "http":
		if hosts := uiNodeCommaList(host); len(hosts) > 0 {
			transport.set("host", hosts)
		}
		uiNodePut(transport, "path", path)
		method := strings.ToUpper(link.value("method"))
		if method != "" && strings.Trim(method, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") == "" {
			transport.set("method", method)
		}
	case "ws":
		// An early data size is conventionally appended to the path.
		route, early, _ := strings.Cut(path, "?ed=")
		uiNodePut(transport, "path", route)
		if host != "" {
			transport.set("headers", uiNodeOf("Host", host))
		}
		if size, valid := uiNodeDigits(early); valid && size != "0" {
			transport.set("max_early_data", size)
			transport.set("early_data_header_name", "Sec-WebSocket-Protocol")
		}
		uiNodePutDigits(transport, "max_early_data", link.value("max_early_data"))
		uiNodePut(transport, "early_data_header_name", link.value("early_data_header_name"))
	}
	return transport, nil
}

// uiNodeLinkMultiplex reads the sing-mux switch. Only the exact form its own
// exporters write counts: elsewhere mux=1 means Mux.Cool, which is another
// protocol.
func uiNodeLinkMultiplex(link *uiNodeLink) *uiNodeObject {
	if link.value("mux") != "true" {
		return nil
	}
	options := uiNodeOf("enabled", true)
	switch protocol := strings.ToLower(link.value("mux_protocol")); protocol {
	case "smux", "yamux", "h2mux":
		options.set("protocol", protocol)
	}
	if link.value("mux_padding") == "true" {
		options.set("padding", true)
	}
	return options
}

func uiNodeLinkDial(link *uiNodeLink, node *uiNodeObject) {
	for _, key := range []string{"tcp_fast_open", "tcp_multi_path", "udp_fragment"} {
		if link.value(key) == "true" {
			node.set(key, true)
		}
	}
}

// uiNodeLinkStream adds what the V2Ray family shares: TLS, a transport and
// multiplexing.
func uiNodeLinkStream(link *uiNodeLink, node *uiNodeObject, mode uiNodeTLSMode) error {
	tlsOptions, err := uiNodeLinkTLS(link, mode)
	if err != nil {
		return err
	}
	transport, err := uiNodeLinkTransport(link)
	if err != nil {
		return err
	}
	if tlsOptions != nil {
		// v2rayN names the server after the Host of an HTTP-like transport when
		// a link has no SNI, which is how a link for a CDN address is written.
		hosts := uiNodeCommaList(link.value("host"))
		if len(hosts) > 0 && tlsOptions.text("server_name") == "" && tlsOptions.get("disable_sni") == nil {
			switch transport.text("type") {
			case "http", "ws", "httpupgrade", "xhttp":
				if _, err = netip.ParseAddr(hosts[0].(string)); err != nil {
					tlsOptions.set("server_name", hosts[0])
				}
			}
		}
		node.set("tls", tlsOptions)
	}
	if transport != nil {
		node.set("transport", transport)
	}
	if multiplex := uiNodeLinkMultiplex(link); multiplex != nil {
		node.set("multiplex", multiplex)
	}
	uiNodeLinkDial(link, node)
	return nil
}
