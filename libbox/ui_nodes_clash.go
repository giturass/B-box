package libbox

// The proxies of a Clash or ClashMeta document. Nothing else of the document
// is read: its groups, rules and providers belong to another core. A proxy is
// first written in the parameters of the share links, so that both notations
// build a node the same way.
import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	E "github.com/sagernet/sing/common/exceptions"

	"gopkg.in/yaml.v3"
)

// uiNodeClashYAML reads the proxies of a YAML document and reports whether
// it also names providers of proxies, and whether it is a Clash document at
// all. The decoder resolves anchors and merge keys and bounds what they
// expand to. Its errors are not passed on, because they quote the document.
func uiNodeClashYAML(content string) ([]any, bool, bool) {
	var document struct {
		Proxies   []any `yaml:"proxies"`
		Providers any   `yaml:"proxy-providers"`
	}
	if err := yaml.Unmarshal([]byte(content), &document); err != nil {
		return nil, false, false
	}
	providers, _ := uiNodeFromYAML(document.Providers, 0).(*uiNodeObject)
	hasProviders := providers != nil && len(providers.keys) > 0
	if document.Proxies == nil && !hasProviders {
		return nil, false, false
	}
	list := make([]any, len(document.Proxies))
	for index, proxy := range document.Proxies {
		list[index] = uiNodeFromYAML(proxy, 0)
	}
	return list, hasProviders, true
}

// uiNodeFromYAML turns decoded YAML into the values of the JSON reader. YAML
// keeps no key order worth having here, so keys are sorted.
func uiNodeFromYAML(value any, depth int) any {
	if depth > uiNodeMaxDepth {
		return nil
	}
	switch typed := value.(type) {
	case map[string]any:
		object := uiNodeOf()
		for _, key := range slices.Sorted(maps.Keys(typed)) {
			object.set(key, uiNodeFromYAML(typed[key], depth+1))
		}
		return object
	case map[any]any:
		named := make(map[string]any, len(typed))
		for key, item := range typed {
			named[fmt.Sprint(key)] = item
		}
		return uiNodeFromYAML(named, depth)
	case []any:
		list := make([]any, len(typed))
		for index, item := range typed {
			list[index] = uiNodeFromYAML(item, depth+1)
		}
		return list
	case string, bool:
		return typed
	case int:
		return uiNodeInt(typed)
	case int64:
		return uiNodeNumber(strconv.FormatInt(typed, 10))
	case uint64:
		return uiNodeNumber(strconv.FormatUint(typed, 10))
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil
		}
		return uiNodeNumber(strconv.FormatFloat(typed, 'f', -1, 64))
	case time.Time:
		return typed.Format(time.RFC3339)
	}
	return nil
}

// uiNodeClashKeys lets a key be written with underscores, as the Clash
// reader does. Header names are left as they are.
func uiNodeClashKeys(value any, depth int) {
	switch typed := value.(type) {
	case *uiNodeObject:
		for _, key := range slices.Clone(typed.keys) {
			item := typed.values[key]
			if dashed := strings.ReplaceAll(key, "_", "-"); dashed != key && typed.get(dashed) == nil {
				typed.set(dashed, item)
			}
			if depth < 8 && !strings.HasSuffix(key, "headers") {
				uiNodeClashKeys(item, depth+1)
			}
		}
	case []any:
		for _, item := range typed {
			uiNodeClashKeys(item, depth)
		}
	}
}

// uiNodeClashTexts reads a value written as one text or as a list.
func uiNodeClashTexts(value any) []string {
	list, isList := value.([]any)
	if !isList {
		list = []any{value}
	}
	var texts []string
	for _, item := range list {
		if text := uiNodeOf("item", item).scalar("item"); text != "" {
			texts = append(texts, text)
		}
	}
	return texts
}

func uiNodeClashHeader(headers *uiNodeObject, name string) []string {
	if headers == nil {
		return nil
	}
	for _, key := range headers.keys {
		if strings.EqualFold(key, name) {
			return uiNodeClashTexts(headers.values[key])
		}
	}
	return nil
}

func (o *uiNodeObject) truthy(key string) bool {
	return uiNodeTruthy(o.scalar(key))
}

// uiNodeClashLink starts the link notation of a proxy with what every proxy
// has: a name, a server and the dial options.
func uiNodeClashLink(proxy *uiNodeObject) *uiNodeLink {
	link := &uiNodeLink{
		name:  proxy.scalar("name"),
		host:  proxy.scalar("server"),
		port:  proxy.scalar("port"),
		query: make(map[string]string),
	}
	if proxy.truthy("tfo") {
		link.query["tcp_fast_open"] = "true"
	}
	if proxy.truthy("mptcp") {
		link.query["tcp_multi_path"] = "true"
	}
	return link
}

// uiNodeClashTLS writes the TLS settings of a proxy as link parameters.
func uiNodeClashTLS(proxy *uiNodeObject, link *uiNodeLink, enabled bool) error {
	if !enabled {
		link.query["security"] = "none"
		return nil
	}
	// Each of these wraps the connection in a protocol of its own.
	for _, key := range []string{"shadow-tls-opts", "restls-opts", "jls-opts", "tlsmirror-opts"} {
		if wrapper := proxy.object(key); wrapper != nil && len(wrapper.keys) > 0 {
			return uiNodeUnsupported("the TLS camouflage of this node is not supported by this core")
		}
	}
	// Without its certificate the client would be turned away by the server.
	if proxy.scalar("private-key") != "" {
		return E.New("the TLS client certificate of this node is not read")
	}
	link.query["security"] = "tls"
	link.query["sni"] = proxy.scalar("servername")
	if link.query["sni"] == "" {
		link.query["sni"] = proxy.scalar("sni")
	}
	if proxy.truthy("skip-cert-verify") {
		link.query["insecure"] = "true"
	}
	if proxy.truthy("disable-sni") {
		link.query["disable_sni"] = "true"
	}
	link.query["alpn"] = strings.Join(uiNodeClashTexts(proxy.get("alpn")), ",")
	// fingerprint pins the certificate; client-fingerprint is the hello to imitate.
	link.query["pcs"] = proxy.scalar("fingerprint")
	link.query["fp"] = proxy.scalar("client-fingerprint")
	reality := proxy.object("reality-opts")
	link.query["pbk"], link.query["sid"] = reality.scalar("public-key"), reality.scalar("short-id")
	if ech := proxy.object("ech-opts"); ech.truthy("enable") {
		link.query["ech_enabled"] = "true"
		link.query["ech_config"] = ech.scalar("config")
		link.query["ech_server_name"] = ech.scalar("query-server-name")
	}
	return nil
}

func uiNodeClashFirst(texts []string) string {
	if len(texts) == 0 {
		return ""
	}
	return texts[0]
}

// uiNodeClashStream writes the TLS and the transport of a proxy of the V2Ray
// family as link parameters.
func uiNodeClashStream(proxy *uiNodeObject, link *uiNodeLink, secure bool) error {
	if err := uiNodeClashTLS(proxy, link, secure); err != nil {
		return err
	}
	network := strings.ToLower(proxy.scalar("network"))
	switch network {
	case "", "tcp":
	case "ws":
		options := proxy.object("ws-opts")
		headers := options.object("headers")
		if headers == nil {
			headers = proxy.object("ws-headers")
		}
		link.query["type"] = "ws"
		if options.truthy("v2ray-http-upgrade") {
			link.query["type"] = "httpupgrade"
		}
		link.query["path"] = options.scalar("path")
		link.query["host"] = uiNodeClashFirst(uiNodeClashHeader(headers, "Host"))
		if size := options.scalar("max-early-data"); size != "0" {
			link.query["max_early_data"] = size
			link.query["early_data_header_name"] = options.scalar("early-data-header-name")
		}
	case "http":
		// The HTTP camouflage of a plain connection.
		options := proxy.object("http-opts")
		link.query["type"], link.query["headerType"] = "tcp", "http"
		link.query["path"] = uiNodeClashFirst(uiNodeClashTexts(options.get("path")))
		link.query["host"] = strings.Join(uiNodeClashHeader(options.object("headers"), "Host"), ",")
		link.query["method"] = options.scalar("method")
	case "h2":
		options := proxy.object("h2-opts")
		link.query["type"] = "h2"
		link.query["path"] = options.scalar("path")
		link.query["host"] = strings.Join(uiNodeClashTexts(options.get("host")), ",")
	case "grpc":
		link.query["type"] = "grpc"
		link.query["serviceName"] = proxy.object("grpc-opts").scalar("grpc-service-name")
	case "xhttp", "splithttp":
		link.query["type"] = "xhttp"
		transport, err := uiNodeXHTTPClash(proxy, link)
		if err != nil {
			return err
		}
		link.xhttp = transport
		link.query["host"] = strings.Join(uiNodeClashTexts(transport.get("host")), ",")
	default:
		return uiNodeUnsupported("the transport of this node is not supported by this core")
	}
	// Clash names the server after the Host header for a WebSocket only.
	if secure && network != "ws" && link.query["sni"] == "" && link.query["host"] != "" {
		link.query["sni"] = link.host
	}
	return nil
}

// uiNodeClashMultiplex reads the sing-mux settings of a proxy.
func uiNodeClashMultiplex(proxy *uiNodeObject) *uiNodeObject {
	smux := proxy.object("smux")
	if !smux.truthy("enabled") {
		return nil
	}
	options := uiNodeOf("enabled", true)
	switch protocol := strings.ToLower(smux.scalar("protocol")); protocol {
	case "smux", "yamux", "h2mux":
		options.set("protocol", protocol)
	}
	// A stream limit takes the place of the other two.
	if streams, valid := uiNodeDigits(smux.scalar("max-streams")); valid && streams != "0" {
		options.set("max_streams", streams)
	} else {
		for _, key := range []string{"max-connections", "min-streams"} {
			if count, valid := uiNodeDigits(smux.scalar(key)); valid && count != "0" {
				options.set(strings.ReplaceAll(key, "-", "_"), count)
			}
		}
	}
	if smux.truthy("padding") {
		options.set("padding", true)
	}
	if brutal := smux.object("brutal-opts"); brutal.truthy("enabled") {
		up, upValid := uiNodeMbps(brutal.scalar("up"))
		down, downValid := uiNodeMbps(brutal.scalar("down"))
		if upValid && downValid {
			options.set("brutal", uiNodeOf("enabled", true, "up_mbps", up, "down_mbps", down))
		}
	}
	return options
}

// uiNodeClashFinish adds what the link notation does not carry: the other
// headers of a WebSocket, multiplexing and the proxy to connect through.
func uiNodeClashFinish(proxy, node *uiNodeObject) {
	headers := proxy.object("ws-opts").object("headers")
	if headers == nil {
		headers = proxy.object("ws-headers")
	}
	transport := node.object("transport")
	if kind := transport.text("type"); headers != nil && (kind == "ws" || kind == "httpupgrade") {
		target := transport.object("headers")
		for _, key := range headers.keys {
			text := headers.scalar(key)
			if strings.EqualFold(key, "Host") || text == "" {
				continue
			}
			if target == nil {
				target = uiNodeOf()
				transport.set("headers", target)
			}
			target.set(key, text)
		}
	}
	switch node.text("type") {
	case "vmess", "vless", "trojan", "shadowsocks":
		if multiplex := uiNodeClashMultiplex(proxy); multiplex != nil {
			node.set("multiplex", multiplex)
		}
	}
	uiNodePut(node, "detour", proxy.scalar("dialer-proxy"))
}

// The characters a SIP003 option value must escape.
var uiNodePluginEscape = strings.NewReplacer(`\`, `\\`, ";", `\;`, "=", `\=`)

// uiNodeClashPlugin writes the plugin of a Shadowsocks proxy in the SIP003
// notation the core reads.
func uiNodeClashPlugin(proxy, node *uiNodeObject) error {
	options := proxy.object("plugin-opts")
	var arguments []string
	add := func(key, value string) {
		if value != "" {
			arguments = append(arguments, key+"="+uiNodePluginEscape.Replace(value))
		}
	}
	plugin := strings.ToLower(proxy.scalar("plugin"))
	switch plugin {
	case "":
		return nil
	case "obfs":
		add("obfs", strings.ToLower(options.scalar("mode")))
		add("obfs-host", options.scalar("host"))
	case "v2ray-plugin":
		mode := strings.ToLower(options.scalar("mode"))
		// The plugin of the core has no way to carry either of these.
		if mode != "" && mode != "websocket" || options.truthy("v2ray-http-upgrade") ||
			options.truthy("tls") && options.truthy("skip-cert-verify") {
			return uiNodeUnsupported("the Shadowsocks plugin of this node is not supported by this core")
		}
		arguments = append(arguments, "mode=websocket")
		if options.truthy("tls") {
			arguments = append(arguments, "tls")
		}
		add("host", options.scalar("host"))
		add("path", options.scalar("path"))
		// Both sides multiplex unless told otherwise.
		if mux := options.scalar("mux"); mux != "" && !uiNodeTruthy(mux) {
			arguments = append(arguments, "mux=0")
		}
	}
	return uiNodeShadowsocksPlugin(node, plugin, strings.Join(arguments, ";"))
}

// uiNodeClashUoT writes UDP over TCP. Clash speaks the first version of it
// unless the second is asked for, and the core the other way around.
func uiNodeClashUoT(proxy, node *uiNodeObject, key string) {
	if !proxy.truthy(key) {
		return
	}
	if proxy.scalar(key+"-version") == "2" {
		node.set("udp_over_tcp", true)
	} else {
		node.set("udp_over_tcp", uiNodeOf("enabled", true, "version", uiNodeInt(1)))
	}
}

func uiNodeClashShadowsocks(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	node, err := uiNodeLinkNode(link, "shadowsocks", 0)
	if err != nil {
		return nil, err
	}
	node.set("method", strings.ToLower(proxy.scalar("cipher")))
	node.set("password", proxy.scalar("password"))
	if err = uiNodeClashPlugin(proxy, node); err != nil {
		return nil, err
	}
	uiNodeClashUoT(proxy, node, "udp-over-tcp")
	uiNodeLinkDial(link, node)
	return node, nil
}

// uiNodeClashPacketEncoding reads the three ways a proxy names the encoding
// of UDP packets; xudp wins over packetaddr.
func uiNodeClashPacketEncoding(proxy *uiNodeObject) string {
	encoding := strings.ToLower(proxy.scalar("packet-encoding"))
	switch {
	case encoding == "xudp" || proxy.truthy("xudp"):
		return "xudp"
	case encoding == "packetaddr" || encoding == "packet" || proxy.truthy("packet-addr"):
		return "packetaddr"
	}
	return ""
}

func uiNodeClashVMess(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	link.user = proxy.scalar("uuid")
	link.query["encryption"] = proxy.scalar("cipher")
	link.query["alterId"] = proxy.scalar("alterId")
	link.query["packetEncoding"] = uiNodeClashPacketEncoding(proxy)
	link.query["globalPadding"] = proxy.scalar("global-padding")
	link.query["authenticatedLength"] = proxy.scalar("authenticated-length")
	if err := uiNodeClashStream(proxy, link, proxy.truthy("tls")); err != nil {
		return nil, err
	}
	return uiNodeVMessNode(link)
}

func uiNodeClashVLESS(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	link.user = proxy.scalar("uuid")
	link.query["flow"] = proxy.scalar("flow")
	link.query["encryption"] = proxy.scalar("encryption")
	// Clash sends xudp unless packetaddr is asked for, as the core does.
	if uiNodeClashPacketEncoding(proxy) == "packetaddr" {
		link.query["packetEncoding"] = "packetaddr"
	}
	if err := uiNodeClashStream(proxy, link, proxy.truthy("tls")); err != nil {
		return nil, err
	}
	return uiNodeVLESSLink(link)
}

func uiNodeClashTrojan(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	// trojan-go encrypts the stream once more with a Shadowsocks cipher.
	if proxy.object("ss-opts").truthy("enabled") {
		return nil, uiNodeUnsupported("Trojan with a Shadowsocks layer is not supported by this core")
	}
	link.user = proxy.scalar("password")
	if err := uiNodeClashStream(proxy, link, true); err != nil {
		return nil, err
	}
	return uiNodeTrojanLink(link)
}

func uiNodeClashHysteria(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	link.query["protocol"] = proxy.scalar("protocol")
	link.query["mport"] = proxy.scalar("ports")
	// A rate is text with a unit or, in the fields Stash added, a number of Mbps.
	link.query["upmbps"], link.query["downmbps"] = proxy.scalar("up"), proxy.scalar("down")
	if link.query["upmbps"] == "" {
		link.query["upmbps"] = proxy.scalar("up-speed")
	}
	if link.query["downmbps"] == "" {
		link.query["downmbps"] = proxy.scalar("down-speed")
	}
	link.query["obfsParam"] = proxy.scalar("obfs")
	link.query["auth"] = proxy.scalar("auth-str")
	link.query["recv_window_conn"] = proxy.scalar("recv-window-conn")
	link.query["recv_window"] = proxy.scalar("recv-window")
	link.query["disable_mtu_discovery"] = proxy.scalar("disable-mtu-discovery")
	link.query["hop_interval"] = proxy.scalar("hop-interval")
	if err := uiNodeClashTLS(proxy, link, true); err != nil {
		return nil, err
	}
	node, err := uiNodeHysteriaLink(link)
	if err != nil {
		return nil, err
	}
	// A credential in base64 takes the place of the one in text, as it does
	// in Clash. The core reads it as it is.
	if auth := proxy.scalar("auth"); auth != "" {
		node.remove("auth_str")
		node.set("auth", auth)
	}
	return node, nil
}

func uiNodeClashHysteria2(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	// A rendezvous server would have to tell where the node is.
	if proxy.object("realm-opts").truthy("enable") {
		return nil, E.New("Hysteria 2 through a realm is not read from a Clash document")
	}
	link.user = proxy.scalar("password")
	link.query["mport"] = proxy.scalar("ports")
	link.query["upmbps"], link.query["downmbps"] = proxy.scalar("up"), proxy.scalar("down")
	// Clash obfuscates only when the kind is named, so the parameter is set
	// even when it is empty: a link with the password alone means salamander.
	link.query["obfs"] = proxy.scalar("obfs")
	link.query["obfs-password"] = proxy.scalar("obfs-password")
	link.query["min_packet_size"] = proxy.scalar("obfs-min-packet-size")
	link.query["max_packet_size"] = proxy.scalar("obfs-max-packet-size")
	// The interval is a number of seconds or a range of them.
	low, high, ranged := strings.Cut(proxy.scalar("hop-interval"), "-")
	link.query["hop_interval"] = strings.TrimSpace(low)
	if ranged {
		link.query["hop_interval_max"] = strings.TrimSpace(high)
	}
	link.query["bbr_profile"] = proxy.scalar("bbr-profile")
	if err := uiNodeClashTLS(proxy, link, true); err != nil {
		return nil, err
	}
	return uiNodeHysteria2Link(link)
}

func uiNodeClashTUIC(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	link.user, link.password = proxy.scalar("uuid"), proxy.scalar("password")
	link.hasPassword = link.user != ""
	link.query["congestion_control"] = proxy.scalar("congestion-controller")
	link.query["udp_relay_mode"] = proxy.scalar("udp-relay-mode")
	link.query["udp_over_stream"] = proxy.scalar("udp-over-stream")
	link.query["reduce_rtt"] = proxy.scalar("reduce-rtt")
	// The heartbeat is a number of milliseconds.
	if interval, valid := uiNodeDigits(proxy.scalar("heartbeat-interval")); valid && interval != "0" {
		link.query["heartbeat"] = string(interval) + "ms"
	}
	if err := uiNodeClashTLS(proxy, link, true); err != nil {
		return nil, err
	}
	// ip is the address to connect to; the server then only names the certificate.
	if address := proxy.scalar("ip"); address != "" {
		if link.query["sni"] == "" {
			link.query["sni"] = link.host
		}
		link.host = address
	}
	return uiNodeTUICLink(link)
}

func uiNodeClashAnyTLS(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	link.user = proxy.scalar("password")
	// The three are numbers of seconds, which is how a bare number is read.
	link.query["idle_session_check_interval"] = proxy.scalar("idle-session-check-interval")
	link.query["idle_session_timeout"] = proxy.scalar("idle-session-timeout")
	link.query["min_idle_session"] = proxy.scalar("min-idle-session")
	if err := uiNodeClashTLS(proxy, link, true); err != nil {
		return nil, err
	}
	return uiNodeAnyTLSLink(link)
}

func uiNodeClashSOCKS(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	if proxy.truthy("tls") {
		return nil, uiNodeUnsupported("SOCKS over TLS is not supported by this core")
	}
	link.scheme = "socks5"
	link.user, link.password, link.hasPassword = proxy.scalar("username"), proxy.scalar("password"), true
	return uiNodeSOCKSLink(link)
}

func uiNodeClashHTTP(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	node, err := uiNodeLinkNode(link, "http", 0)
	if err != nil {
		return nil, err
	}
	uiNodePut(node, "username", proxy.scalar("username"))
	uiNodePut(node, "password", proxy.scalar("password"))
	if source := proxy.object("headers"); source != nil {
		headers := uiNodeOf()
		for _, key := range source.keys {
			uiNodePut(headers, key, source.scalar(key))
		}
		if len(headers.keys) > 0 {
			node.set("headers", headers)
		}
	}
	if err = uiNodeClashTLS(proxy, link, proxy.truthy("tls")); err != nil {
		return nil, err
	}
	tlsOptions, err := uiNodeLinkTLS(link, uiNodeTLSOptional)
	if err != nil {
		return nil, err
	}
	if tlsOptions != nil {
		node.set("tls", tlsOptions)
	}
	uiNodeLinkDial(link, node)
	return node, nil
}

func uiNodeClashWireGuard(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	// AmneziaWG changes what is sent to the server.
	if amnezia := proxy.object("amnezia-wg-option"); amnezia != nil && len(amnezia.keys) > 0 {
		return nil, uiNodeUnsupported("AmneziaWG is not supported by this core")
	}
	// A list of peers takes the place of the one written into the proxy.
	sources := []any{proxy}
	if list := proxy.list("peers"); len(list) > 0 {
		sources = list
	}
	node := uiNodeOf("type", "wireguard")
	var peers []any
	for _, item := range sources {
		source, isObject := item.(*uiNodeObject)
		if !isObject {
			return nil, E.New("a peer of this WireGuard node is not readable")
		}
		address := &uiNodeLink{name: link.name, host: source.scalar("server"), port: source.scalar("port")}
		server, err := uiNodeLinkNode(address, "wireguard", 51820)
		if err != nil {
			return nil, err
		}
		if len(peers) == 0 {
			node.set("tag", server.get("tag"))
		}
		// The reserved bytes of the proxy are those of a peer that has none.
		reserved := uiNodeClashTexts(source.get("reserved"))
		if len(reserved) == 0 {
			reserved = uiNodeClashTexts(proxy.get("reserved"))
		}
		peer := uiNodeOf("address", server.get("server"), "port", server.get("server_port"))
		err = uiNodeWireGuardPeer(peer, source.scalar("public-key"), source.scalar("pre-shared-key"),
			strings.Join(uiNodeClashTexts(source.get("allowed-ips")), ","),
			proxy.scalar("persistent-keepalive"), strings.Join(reserved, ","))
		if err != nil {
			return nil, err
		}
		peers = append(peers, peer)
	}
	uiNodePutDigits(node, "mtu", proxy.scalar("mtu"))
	if addresses := uiNodePrefixes(proxy.scalar("ip") + "," + proxy.scalar("ipv6")); len(addresses) > 0 {
		node.set("address", addresses)
	}
	node.set("private_key", uiNodeWireGuardKey(proxy.scalar("private-key")))
	node.set("peers", peers)
	if workers, valid := uiNodeDigits(proxy.scalar("workers")); valid && workers != "0" {
		node.set("workers", workers)
	}
	return node, nil
}

func uiNodeClashSSH(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	node, err := uiNodeLinkNode(link, "ssh", 22)
	if err != nil {
		return nil, err
	}
	uiNodePut(node, "user", proxy.scalar("username"))
	uiNodePut(node, "password", proxy.scalar("password"))
	if key := proxy.text("private-key"); strings.TrimSpace(key) != "" {
		// Anything else is the path of a file, which is not part of the document.
		if !strings.Contains(key, "PRIVATE KEY") {
			return nil, E.New("the SSH private key of this node is kept in a file that was not imported")
		}
		node.set("private_key", key)
		uiNodePut(node, "private_key_passphrase", proxy.text("private-key-passphrase"))
	}
	for _, key := range []string{"host-key", "host-key-algorithms"} {
		var list []any
		for _, text := range uiNodeClashTexts(proxy.get(key)) {
			list = append(list, text)
		}
		if len(list) > 0 {
			node.set(strings.ReplaceAll(key, "-", "_"), list)
		}
	}
	uiNodeLinkDial(link, node)
	return node, nil
}

func uiNodeClashSnell(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	node, err := uiNodeLinkNode(link, "snell", 0)
	if err != nil {
		return nil, err
	}
	// Clash reads a proxy without a version as the first one. A server of the
	// fifth version accepts a client of the fourth.
	version := proxy.scalar("version")
	switch version {
	case "4", "5":
		version = "4"
	case "6":
	default:
		return nil, uiNodeUnsupported("this version of Snell is not supported by this core")
	}
	node.set("version", uiNodeNumber(version))
	node.set("psk", proxy.scalar("psk"))
	if proxy.truthy("reuse") {
		node.set("reuse", true)
	}
	obfs := proxy.object("obfs-opts")
	switch mode := strings.ToLower(obfs.scalar("mode")); {
	case mode == "" || mode == "none":
	case version == "4" && (mode == "http" || mode == "tls"):
		host := obfs.scalar("host")
		if host == "" {
			// The name Clash sends when none is given.
			host = "bing.com"
		}
		node.set("obfs_mode", mode)
		node.set("obfs_host", host)
	default:
		return nil, uiNodeUnsupported("the obfuscation of this Snell node is not supported by this core")
	}
	// The sixth version shapes the traffic itself.
	if version == "6" {
		uiNodePut(node, "mode", strings.ToLower(proxy.scalar("mode")))
	}
	uiNodeLinkDial(link, node)
	return node, nil
}

// uiNodeClashProxy converts one entry of the proxy list. An entry that is not
// a proxy server, such as a built-in policy, gives neither a node nor an error.
func uiNodeClashProxy(proxy *uiNodeObject) (*uiNodeObject, error) {
	protocol, typed := proxy.get("type").(string)
	if !typed || strings.TrimSpace(protocol) == "" {
		return nil, E.New("the proxy protocol is missing or not a non-empty string")
	}
	uiNodeClashKeys(proxy, 0)
	var convert func(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error)
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "ss", "shadowsocks":
		convert = uiNodeClashShadowsocks
	case "vmess":
		convert = uiNodeClashVMess
	case "vless":
		convert = uiNodeClashVLESS
	case "trojan":
		convert = uiNodeClashTrojan
	case "hysteria":
		convert = uiNodeClashHysteria
	case "hysteria2", "hy2":
		convert = uiNodeClashHysteria2
	case "tuic":
		convert = uiNodeClashTUIC
	case "anytls":
		convert = uiNodeClashAnyTLS
	case "socks5", "socks":
		convert = uiNodeClashSOCKS
	case "http":
		convert = uiNodeClashHTTP
	case "wireguard", "wg":
		convert = uiNodeClashWireGuard
	case "ssh":
		convert = uiNodeClashSSH
	case "snell":
		convert = uiNodeClashSnell
	case "direct", "reject", "reject-drop", "dns", "rematch", "pass", "compatible":
		return nil, nil
	case "ssr":
		return nil, uiNodeUnsupported("ShadowsocksR was removed from the core")
	case "masque", "openvpn", "tailscale":
		return nil, uiNodeUnsupported("this protocol is not read from a Clash document")
	default:
		return nil, uiNodeUnsupported("this protocol is not supported by this core")
	}
	node, err := convert(proxy, uiNodeClashLink(proxy))
	if err != nil {
		return nil, err
	}
	uiNodeClashFinish(proxy, node)
	return node, nil
}

// uiNodeClashProxies converts the proxy list of a Clash document.
func uiNodeClashProxies(list []any, parsed *uiNodeParsed) {
	for _, item := range list {
		proxy, isObject := item.(*uiNodeObject)
		if !isObject {
			parsed.skip("", E.New("an entry of the proxy list is not readable"))
			continue
		}
		node, err := uiNodeClashProxy(proxy)
		if err != nil {
			parsed.skip(proxy.scalar("name"), err)
		} else if node != nil {
			parsed.nodes = append(parsed.nodes, node)
		}
	}
}
