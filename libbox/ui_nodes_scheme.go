package libbox

// The share link notation of each protocol. The parameters are the ones the
// clients in use write, which no single specification covers.
import (
	"encoding/base64"
	"encoding/hex"
	"math"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/sagernet/sing-vmess"
	E "github.com/sagernet/sing/common/exceptions"
)

// scalar reads a value that writers give as text, a number or a switch.
func (o *uiNodeObject) scalar(key string) string {
	switch value := o.get(key).(type) {
	case string:
		return strings.TrimSpace(value)
	case uiNodeNumber:
		return string(value)
	case bool:
		return strconv.FormatBool(value)
	}
	return ""
}

// uiNodePutDuration writes a duration; a bare number is taken as seconds.
func uiNodePutDuration(object *uiNodeObject, key, text string) {
	if _, bare := uiNodeDigits(text); bare {
		text += "s"
	}
	if _, err := time.ParseDuration(text); err == nil {
		object.set(key, text)
	}
}

func uiNodeLinkUoT(link *uiNodeLink, node *uiNodeObject) {
	value := link.value("uot")
	if number, counted := uiNodeDigits(value); value == "true" || counted && number != "0" {
		node.set("udp_over_tcp", true)
	}
}

// uiNodeShadowsocksPlugin names a SIP003 plugin the way the core does. The
// core carries obfs-local and v2ray-plugin and can run nothing else.
func uiNodeShadowsocksPlugin(node *uiNodeObject, name, options string) error {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "":
		return nil
	case "obfs-local", "simple-obfs", "obfs":
		name = "obfs-local"
	case "v2ray-plugin":
		name = "v2ray-plugin"
	default:
		return uiNodeUnsupported("the Shadowsocks plugin of this node is not supported by this core")
	}
	node.set("plugin", name)
	uiNodePut(node, "plugin_opts", options)
	return nil
}

// uiNodeShadowsocksLink reads SIP002, whose user information is
// method:password in plain text or in base64, and the older form whose whole
// body is base64.
func uiNodeShadowsocksLink(link *uiNodeLink) (*uiNodeObject, error) {
	method, password, valid := link.user, link.password, link.hasPassword
	if !strings.Contains(link.body, "@") {
		decoded, _ := uiNodeBase64Text(link.body)
		at := strings.LastIndexByte(decoded, '@')
		if at < 0 {
			return nil, E.New("the Shadowsocks link is not readable")
		}
		method, password, valid = strings.Cut(decoded[:at], ":")
		link.setAddress(decoded[at+1:])
	} else if !valid {
		decoded, _ := uiNodeBase64Text(link.user)
		method, password, valid = strings.Cut(decoded, ":")
	}
	if !valid || method == "" {
		return nil, E.New("the Shadowsocks link has no method and password")
	}
	node, err := uiNodeLinkNode(link, "shadowsocks", 0)
	if err != nil {
		return nil, err
	}
	node.set("method", strings.ToLower(method))
	node.set("password", password)
	plugin, options, _ := strings.Cut(link.value("plugin"), ";")
	if separate := link.value("plugin-opts", "plugin_opts"); separate != "" {
		options = separate
	}
	if err = uiNodeShadowsocksPlugin(node, plugin, options); err != nil {
		return nil, err
	}
	uiNodeLinkUoT(link, node)
	if multiplex := uiNodeLinkMultiplex(link); multiplex != nil {
		node.set("multiplex", multiplex)
	}
	uiNodeLinkDial(link, node)
	return node, nil
}

// The ciphers of sing-vmess. Anything else a link names becomes auto.
var uiNodeVMessCiphers = []string{"auto", "none", "zero", "aes-128-gcm", "chacha20-poly1305", "aes-128-cfb"}

// uiNodeVMessLink reads the v2rayN notation, base64 of a JSON object, and
// the notation that follows the VLESS one.
func uiNodeVMessLink(link *uiNodeLink) (*uiNodeObject, error) {
	if decoded, valid := uiNodeBase64Text(link.body); valid && strings.HasPrefix(strings.TrimSpace(decoded), "{") {
		return uiNodeVMessJSON(decoded)
	}
	return uiNodeVMessNode(link)
}

// uiNodeVMessJSON turns the fields of v2rayN, which are text or numbers
// depending on the writer, into the parameters of the other notation.
func uiNodeVMessJSON(content string) (*uiNodeObject, error) {
	fields, err := uiNodeDecodeObject(content)
	if err != nil {
		return nil, E.New("the VMess link is not readable")
	}
	link := &uiNodeLink{
		scheme: "vmess",
		name:   fields.scalar("ps"),
		user:   fields.scalar("id"),
		host:   fields.scalar("add"),
		port:   fields.scalar("port"),
		query:  make(map[string]string),
	}
	network, header := strings.ToLower(fields.scalar("net")), fields.scalar("type")
	host, path := fields.scalar("host"), fields.scalar("path")
	switch network {
	case "grpc":
		link.query["serviceName"], path = path, ""
	case "quic":
		link.query["quicSecurity"], link.query["headerType"] = host, header
		host, path = "", ""
	case "", "tcp", "raw":
		link.query["headerType"] = header
	}
	link.query["type"], link.query["host"], link.query["path"] = network, host, path
	if network == "xhttp" || network == "splithttp" {
		link.query["mode"] = fields.scalar("mode")
		if extra := fields.get("extra"); extra != nil {
			if text, isText := extra.(string); isText {
				link.query["extra"] = text
			} else if object, isObject := extra.(*uiNodeObject); isObject {
				link.query["extra"], err = uiNodeEncode(object, false)
				if err != nil {
					return nil, E.New("the XHTTP extra settings must be a JSON object")
				}
			} else {
				return nil, E.New("the XHTTP extra settings must be a JSON object")
			}
		}
	}
	// Without the tls field there is no TLS, whatever sni still holds.
	switch strings.ToLower(fields.scalar("tls")) {
	case "tls":
		link.query["security"] = "tls"
	case "", "none":
		link.query["security"] = "none"
	default:
		return nil, uiNodeUnsupported("the TLS setting of this VMess link is not supported by this core")
	}
	for _, key := range []string{"sni", "alpn", "fp", "insecure", "allowInsecure"} {
		link.query[key] = fields.scalar(key)
	}
	link.query["alterId"], link.query["encryption"] = fields.scalar("aid"), fields.scalar("scy")
	return uiNodeVMessNode(link)
}

func uiNodeVMessNode(link *uiNodeLink) (*uiNodeObject, error) {
	if link.user == "" {
		return nil, E.New("the VMess node has no user ID")
	}
	node, err := uiNodeLinkNode(link, "vmess", 443)
	if err != nil {
		return nil, err
	}
	node.set("uuid", link.user)
	cipher := strings.ToLower(link.value("encryption"))
	if !slices.Contains(uiNodeVMessCiphers, cipher) {
		cipher = "auto"
	}
	// Foreign nodes use auto for AEAD even with TLS. Resolve it before the
	// sing-box outbound can turn auto into zero when TLS is enabled.
	if cipher == "auto" {
		if vmess.AutoSecurityType() == vmess.SecurityTypeAes128Gcm {
			cipher = "aes-128-gcm"
		} else {
			cipher = "chacha20-poly1305"
		}
	}
	node.set("security", cipher)
	if alterID, valid := uiNodeDigits(link.value("alterId", "aid")); valid && alterID != "0" {
		node.set("alter_id", alterID)
	}
	if link.flag("globalPadding") {
		node.set("global_padding", true)
	}
	if link.flag("authenticatedLength") {
		node.set("authenticated_length", true)
	}
	switch encoding := strings.ToLower(link.value("packetEncoding")); encoding {
	case "packetaddr", "xudp":
		node.set("packet_encoding", encoding)
	}
	if err = uiNodeLinkStream(link, node, uiNodeTLSOptional); err != nil {
		return nil, err
	}
	return node, nil
}

func uiNodeVLESSLink(link *uiNodeLink) (*uiNodeObject, error) {
	if link.user == "" {
		return nil, E.New("the VLESS node has no user ID")
	}
	node, err := uiNodeLinkNode(link, "vless", 443)
	if err != nil {
		return nil, err
	}
	node.set("uuid", link.user)
	// Encryption includes case-sensitive public keys; only the none switch is
	// compared without case. The core validates the complete handshake value.
	if encryption := link.value("encryption"); encryption != "" && !strings.EqualFold(encryption, "none") {
		node.set("encryption", encryption)
	}
	switch flow := strings.ToLower(link.value("flow")); flow {
	case "", "none":
	case "xtls-rprx-vision-udp443":
		// Xray sends the plain Vision flow for this name; the suffix lifts a
		// block on UDP port 443 that this core does not apply.
		node.set("flow", "xtls-rprx-vision")
	default:
		node.set("flow", flow)
	}
	// The core takes a missing value as xudp and an empty one as none.
	switch encoding := strings.ToLower(link.value("packetEncoding", "packet_encoding")); encoding {
	case "", "xudp":
	case "packetaddr":
		node.set("packet_encoding", encoding)
	default:
		node.set("packet_encoding", "")
	}
	if err = uiNodeLinkStream(link, node, uiNodeTLSOptional); err != nil {
		return nil, err
	}
	return node, nil
}

func uiNodeTrojanLink(link *uiNodeLink) (*uiNodeObject, error) {
	password := link.secret()
	if password == "" {
		return nil, E.New("the Trojan node has no password")
	}
	node, err := uiNodeLinkNode(link, "trojan", 443)
	if err != nil {
		return nil, err
	}
	node.set("password", password)
	if err = uiNodeLinkStream(link, node, uiNodeTLSDefault); err != nil {
		return nil, err
	}
	return node, nil
}

// uiNodeMbps reads a rate. A bare number is Mbps; with a unit only the last
// letter tells bits from bytes, so 100 Mbps is 100 and 50 MB/s is 400.
func uiNodeMbps(text string) (uiNodeNumber, bool) {
	text = strings.TrimSpace(text)
	end := strings.IndexFunc(text, func(char rune) bool { return (char < '0' || char > '9') && char != '.' })
	if end < 0 {
		end = len(text)
	}
	value, err := strconv.ParseFloat(text[:end], 64)
	if err != nil || value <= 0 {
		return "", false
	}
	unit := strings.TrimSpace(text[end:])
	if rate, found := strings.CutSuffix(unit, "/s"); found {
		unit = rate
	} else {
		unit = strings.TrimSuffix(unit, "ps")
	}
	scale := 1.0
	if unit != "" {
		scale = 1e-6
		switch unit[len(unit)-1] {
		case 'B':
			scale = 8e-6
			fallthrough
		case 'b':
			unit = unit[:len(unit)-1]
		}
		switch strings.ToUpper(unit) {
		case "":
		case "K":
			scale *= 1e3
		case "M":
			scale *= 1e6
		case "G":
			scale *= 1e9
		case "T":
			scale *= 1e12
		default:
			return "", false
		}
	}
	return uiNodeInt(max(1, int(math.Round(min(value*scale, math.MaxInt32))))), true
}

// uiNodePortRanges turns a port list such as 443,1000-2000 into the ranges
// of server_ports and returns its first port.
func uiNodePortRanges(text string) ([]any, int, bool) {
	var ranges []any
	first := 0
	for _, item := range strings.FieldsFunc(text, func(char rune) bool { return char == ',' || char == '/' }) {
		low, high := item, item
		if index := strings.IndexAny(item, ":-"); index >= 0 {
			low, high = item[:index], item[index+1:]
		}
		start, startErr := strconv.Atoi(strings.TrimSpace(low))
		end, endErr := strconv.Atoi(strings.TrimSpace(high))
		if startErr != nil || endErr != nil || start < 1 || end > 65535 || start > end {
			return nil, 0, false
		}
		if first == 0 {
			first = start
		}
		ranges = append(ranges, strconv.Itoa(start)+":"+strconv.Itoa(end))
	}
	return ranges, first, len(ranges) > 0
}

// uiNodeHysteriaNode starts a Hysteria node. A port list, in the address or
// in mport, makes the client hop between the ports.
func uiNodeHysteriaNode(link *uiNodeLink, nodeType string) (*uiNodeObject, error) {
	ports := link.value("mport")
	if _, single := uiNodeDigits(link.port); link.port != "" && !single {
		if ports == "" {
			ports = link.port
		}
		link.port = ""
	}
	var ranges []any
	if ports != "" {
		list, first, valid := uiNodePortRanges(ports)
		if !valid {
			return nil, E.New("the port list of this node is not readable")
		}
		ranges = list
		if link.port == "" {
			link.port = strconv.Itoa(first)
		}
	}
	node, err := uiNodeLinkNode(link, nodeType, 443)
	if err != nil {
		return nil, err
	}
	if ranges != nil {
		node.set("server_ports", ranges)
	}
	return node, nil
}

// uiNodeLinkQUIC adds the TLS a QUIC protocol always has.
func uiNodeLinkQUIC(link *uiNodeLink, node *uiNodeObject) (*uiNodeObject, error) {
	tlsOptions, err := uiNodeLinkTLS(link, uiNodeTLSQUIC)
	if err != nil {
		return nil, err
	}
	node.set("tls", tlsOptions)
	uiNodeLinkDial(link, node)
	return node, nil
}

func uiNodeHysteriaLink(link *uiNodeLink) (*uiNodeObject, error) {
	if protocol := strings.ToLower(link.value("protocol")); protocol != "" && protocol != "udp" {
		return nil, uiNodeUnsupported("Hysteria over this transport is not supported by this core")
	}
	node, err := uiNodeHysteriaNode(link, "hysteria")
	if err != nil {
		return nil, err
	}
	// The core does not build the protocol without both rates, and a link
	// often leaves them to the client.
	for _, rate := range [][3]string{{"up_mbps", "upmbps", "10"}, {"down_mbps", "downmbps", "50"}} {
		mbps, valid := uiNodeMbps(link.value(rate[1]))
		if !valid {
			mbps = uiNodeNumber(rate[2])
		}
		node.set(rate[0], mbps)
	}
	uiNodePut(node, "obfs", link.value("obfsParam"))
	uiNodePut(node, "auth_str", link.value("auth"))
	uiNodePutDigits(node, "recv_window_conn", link.value("recv_window_conn"))
	uiNodePutDigits(node, "recv_window", link.value("recv_window"))
	if link.flag("disable_mtu_discovery") {
		node.set("disable_mtu_discovery", true)
	}
	uiNodePutDuration(node, "hop_interval", link.value("hop_interval"))
	return uiNodeLinkQUIC(link, node)
}

func uiNodeHysteria2Link(link *uiNodeLink) (*uiNodeObject, error) {
	node, err := uiNodeHysteriaNode(link, "hysteria2")
	if err != nil {
		return nil, err
	}
	uiNodePut(node, "password", link.secret())
	for _, rate := range [][2]string{{"up_mbps", "upmbps"}, {"down_mbps", "downmbps"}} {
		if mbps, valid := uiNodeMbps(link.value(rate[1])); valid {
			node.set(rate[0], mbps)
		}
	}
	// The official client reads plain as no obfuscation, and a link that
	// carries only the password means salamander.
	kind, declared := link.query["obfs"]
	kind = strings.ToLower(strings.TrimSpace(kind))
	password := link.value("obfs-password", "obfs_password")
	minSize, maxSize := link.value("min_packet_size"), link.value("max_packet_size")
	switch {
	case kind == "none" || kind == "plain":
		kind = ""
	case declared || password == "":
	case minSize != "" || maxSize != "":
		kind = "gecko"
	default:
		kind = "salamander"
	}
	if kind != "" {
		obfs := uiNodeOf("type", kind)
		uiNodePut(obfs, "password", password)
		if kind == "gecko" {
			uiNodePutDigits(obfs, "min_packet_size", minSize)
			uiNodePutDigits(obfs, "max_packet_size", maxSize)
		}
		node.set("obfs", obfs)
	}
	uiNodePutDuration(node, "hop_interval", link.value("hop_interval"))
	uiNodePutDuration(node, "hop_interval_max", link.value("hop_interval_max"))
	uiNodePut(node, "bbr_profile", strings.ToLower(link.value("bbr_profile")))
	if link.value("disable_chrome_parrot") == "true" {
		node.set("disable_chrome_parrot", true)
	}
	return uiNodeLinkQUIC(link, node)
}

func uiNodeTUICLink(link *uiNodeLink) (*uiNodeObject, error) {
	user, password, valid := link.user, link.password, link.hasPassword
	if !valid {
		user, password, valid = strings.Cut(user, ":")
	}
	// Version 4 knew a client by a token alone.
	if !valid {
		return nil, uiNodeUnsupported("TUIC with a token in place of a user ID and password is not supported by this core")
	}
	node, err := uiNodeLinkNode(link, "tuic", 443)
	if err != nil {
		return nil, err
	}
	node.set("uuid", user)
	node.set("password", password)
	switch control := strings.ToLower(link.value("congestion_control", "congestion-controller")); control {
	case "cubic", "new_reno", "bbr":
		node.set("congestion_control", control)
	}
	// The core takes one of the two ways to carry UDP.
	if link.flag("udp_over_stream") {
		node.set("udp_over_stream", true)
	} else {
		switch mode := strings.ToLower(link.value("udp_relay_mode", "udp-relay-mode")); mode {
		case "native", "quic":
			node.set("udp_relay_mode", mode)
		}
	}
	if link.flag("zero_rtt_handshake", "reduce_rtt", "reduce-rtt") {
		node.set("zero_rtt_handshake", true)
	}
	uiNodePutDuration(node, "heartbeat", link.value("heartbeat"))
	return uiNodeLinkQUIC(link, node)
}

func uiNodeAnyTLSLink(link *uiNodeLink) (*uiNodeObject, error) {
	node, err := uiNodeLinkNode(link, "anytls", 443)
	if err != nil {
		return nil, err
	}
	node.set("password", link.secret())
	uiNodePutDuration(node, "idle_session_check_interval", link.value("idle_session_check_interval"))
	uiNodePutDuration(node, "idle_session_timeout", link.value("idle_session_timeout"))
	uiNodePutDigits(node, "min_idle_session", link.value("min_idle_session"))
	tlsOptions, err := uiNodeLinkTLS(link, uiNodeTLSAlways)
	if err != nil {
		return nil, err
	}
	node.set("tls", tlsOptions)
	return node, nil
}

// uiNodeLinkLogin reads a user name and password, which some writers put
// into the user information as base64 of name:password.
func uiNodeLinkLogin(link *uiNodeLink) (string, string) {
	if !link.hasPassword {
		if decoded, valid := uiNodeBase64Text(link.user); valid {
			if user, password, found := strings.Cut(decoded, ":"); found {
				return user, password
			}
		}
	}
	return link.user, link.password
}

func uiNodeSOCKSLink(link *uiNodeLink) (*uiNodeObject, error) {
	node, err := uiNodeLinkNode(link, "socks", 1080)
	if err != nil {
		return nil, err
	}
	version := link.value("version")
	if version == "" {
		version = strings.TrimPrefix(link.scheme, "socks")
	}
	switch version {
	case "4", "4a":
		node.set("version", version)
	}
	user, password := uiNodeLinkLogin(link)
	uiNodePut(node, "username", user)
	uiNodePut(node, "password", password)
	uiNodeLinkUoT(link, node)
	uiNodeLinkDial(link, node)
	return node, nil
}

// uiNodeWireGuardKey writes a key as the padded base64 the core reads. Tools
// also write keys unpadded, in the URL alphabet or in hexadecimal.
func uiNodeWireGuardKey(text string) string {
	text = strings.TrimSpace(text)
	if key, err := hex.DecodeString(text); err == nil && len(key) == 32 {
		return base64.StdEncoding.EncodeToString(key)
	}
	if key, valid := uiNodeBase64(text); valid && len(key) == 32 {
		return base64.StdEncoding.EncodeToString(key)
	}
	return text
}

// uiNodePrefixes reads addresses as the prefixes the core wants; an address
// alone is a single host.
func uiNodePrefixes(text string) []any {
	var prefixes []any
	for _, item := range uiNodeCommaList(text) {
		address := item.(string)
		if parsed, err := netip.ParseAddr(address); err == nil {
			address = netip.PrefixFrom(parsed, parsed.BitLen()).String()
		}
		prefixes = append(prefixes, address)
	}
	return prefixes
}

// uiNodeReserved reads the reserved bytes of a WireGuard peer, written as
// three numbers or as their base64.
func uiNodeReserved(text string) ([]any, bool) {
	if items := uiNodeCommaList(strings.Trim(strings.TrimSpace(text), "[]")); len(items) == 3 {
		for index, item := range items {
			number, err := strconv.ParseUint(item.(string), 10, 8)
			if err != nil {
				return nil, false
			}
			items[index] = uiNodeInt(int(number))
		}
		return items, true
	}
	decoded, valid := uiNodeBase64(text)
	if !valid || len(decoded) != 3 {
		return nil, false
	}
	return []any{uiNodeInt(int(decoded[0])), uiNodeInt(int(decoded[1])), uiNodeInt(int(decoded[2]))}, true
}

// uiNodeWireGuardPeer fills what every notation says about a peer. Without a
// list of allowed addresses the peer carries everything.
func uiNodeWireGuardPeer(peer *uiNodeObject, publicKey, sharedKey, allowed, keepalive, reserved string) error {
	uiNodePut(peer, "public_key", uiNodeWireGuardKey(publicKey))
	uiNodePut(peer, "pre_shared_key", uiNodeWireGuardKey(sharedKey))
	prefixes := uiNodePrefixes(allowed)
	if len(prefixes) == 0 {
		prefixes = []any{"0.0.0.0/0", "::/0"}
	}
	peer.set("allowed_ips", prefixes)
	if seconds, valid := uiNodeDigits(keepalive); valid && seconds != "0" {
		peer.set("persistent_keepalive_interval", seconds)
	}
	if strings.TrimSpace(reserved) != "" {
		list, valid := uiNodeReserved(reserved)
		if !valid {
			return E.New("the reserved bytes of this WireGuard peer are not readable")
		}
		peer.set("reserved", list)
	}
	return nil
}

func uiNodeWireGuardLink(link *uiNodeLink) (*uiNodeObject, error) {
	server, err := uiNodeLinkNode(link, "wireguard", 51820)
	if err != nil {
		return nil, err
	}
	privateKey := link.value("private_key", "privatekey", "privateKey")
	if privateKey == "" {
		privateKey = link.secret()
	}
	peer := uiNodeOf("address", server.get("server"), "port", server.get("server_port"))
	err = uiNodeWireGuardPeer(peer,
		link.value("public_key", "publickey", "publicKey", "peer_public_key"),
		link.value("pre_shared_key", "preshared_key", "presharedkey", "presharedKey", "psk"),
		link.value("allowed_ips", "allowedips", "allowedIPs"),
		link.value("persistent_keepalive_interval", "persistent_keepalive", "keepalive"),
		link.value("reserved"))
	if err != nil {
		return nil, err
	}
	node := uiNodeOf("type", "wireguard", "tag", server.get("tag"))
	uiNodePutDigits(node, "mtu", link.value("mtu"))
	if addresses := uiNodePrefixes(link.value("local_address", "address", "ip")); len(addresses) > 0 {
		node.set("address", addresses)
	}
	node.set("private_key", uiNodeWireGuardKey(privateKey))
	node.set("peers", []any{peer})
	uiNodePutDuration(node, "udp_timeout", link.value("udp_timeout"))
	return node, nil
}

func uiNodeNaiveLink(link *uiNodeLink) (*uiNodeObject, error) {
	node, err := uiNodeLinkNode(link, "naive", 443)
	if err != nil {
		return nil, err
	}
	uiNodePut(node, "username", link.user)
	uiNodePut(node, "password", link.password)
	uiNodePutDigits(node, "insecure_concurrency", link.value("insecure-concurrency", "insecure_concurrency"))
	if link.scheme == "naive+quic" {
		node.set("quic", true)
		uiNodePut(node, "quic_congestion_control", strings.ToLower(link.value("congestion_control")))
	}
	uiNodeLinkUoT(link, node)
	// Cronet verifies the server itself and takes only a name and ECH.
	tlsOptions := uiNodeOf("enabled", true)
	uiNodePut(tlsOptions, "server_name", link.value("sni"))
	if ech := uiNodeLinkECH(link); ech != nil {
		tlsOptions.set("ech", ech)
	}
	node.set("tls", tlsOptions)
	return node, nil
}

// The schemes read as nodes. Protocols known by name that this core does not
// carry are listed too, so that a list of them is reported as nodes left out
// and not as text of no known form. A web address is never a node: it is
// where a subscription is fetched from.
var uiNodeSchemes = map[string]func(link *uiNodeLink) (*uiNodeObject, error){
	"ss":          uiNodeShadowsocksLink,
	"shadowsocks": uiNodeShadowsocksLink,
	"vmess":       uiNodeVMessLink,
	"vless":       uiNodeVLESSLink,
	"trojan":      uiNodeTrojanLink,
	"hysteria":    uiNodeHysteriaLink,
	"hysteria2":   uiNodeHysteria2Link,
	"hy2":         uiNodeHysteria2Link,
	"tuic":        uiNodeTUICLink,
	"anytls":      uiNodeAnyTLSLink,
	"socks":       uiNodeSOCKSLink,
	"socks4":      uiNodeSOCKSLink,
	"socks4a":     uiNodeSOCKSLink,
	"socks5":      uiNodeSOCKSLink,
	"socks5h":     uiNodeSOCKSLink,
	"wireguard":   uiNodeWireGuardLink,
	"wg":          uiNodeWireGuardLink,
	"naive+https": uiNodeNaiveLink,
	"naive+quic":  uiNodeNaiveLink,
	"ssr":         nil,
	"juicity":     nil,
	"mieru":       nil,
	"mierus":      nil,
	"brook":       nil,
	"awg":         nil,
	"tt":          nil,
}

// uiNodeLinkStart finds where a share link begins in a line of text, which
// may be a message that quotes one.
func uiNodeLinkStart(line string) int {
	for offset := 0; ; {
		index := strings.Index(line[offset:], "://")
		if index < 0 {
			return -1
		}
		end := offset + index
		start := end
		for start > 0 && strings.IndexByte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789+", line[start-1]) >= 0 {
			start--
		}
		if _, known := uiNodeSchemes[strings.ToLower(line[start:end])]; known {
			return start
		}
		offset = end + 3
	}
}

// uiNodeLinkParse reads one share link. The error is the reason the node is
// left out, and it holds nothing of the link.
func uiNodeLinkParse(text string) (*uiNodeObject, error) {
	link := uiNodeSplitLink(text)
	if link == nil {
		return nil, E.New("this is not a share link")
	}
	parse, known := uiNodeSchemes[link.scheme]
	switch {
	case link.scheme == "ssr":
		return nil, uiNodeUnsupported("ShadowsocksR was removed from the core")
	case !known || parse == nil:
		return nil, uiNodeUnsupported("this protocol is not supported by this core")
	}
	return parse(link)
}
