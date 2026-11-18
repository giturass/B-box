package libbox

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"net"
	"net/url"
	"strings"

	"github.com/sagernet/sing-vmess"
	E "github.com/sagernet/sing/common/exceptions"
)

// ShareUIConfigNodes returns the selected nodes as sing-box JSON and offers
// a protocol URI for each node that can be represented in that format.
func ShareUIConfigNodes(config, tags string) (*StringBox, error) {
	names, err := uiNodeTextList(tags)
	if err != nil || len(names) == 0 {
		return nil, E.New("Select nodes to share")
	}
	root, err := uiNodeDecodeObject(config)
	if err != nil {
		return nil, E.New("Unable to read configuration for sharing")
	}
	ctx := baseContext(nil)
	byTag := make(map[string]*uiNodeObject)
	for _, section := range []string{"outbounds", "endpoints"} {
		for _, item := range root.list(section) {
			if node, ok := item.(*uiNodeObject); ok && node.text("tag") != "" {
				byTag[node.text("tag")] = node
			}
		}
	}
	seen := make(map[string]bool, len(names))
	nodes := make([]any, 0, len(names))
	shares := make([]any, 0, len(names))
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		node := byTag[name]
		proxy, endpoint := uiNodeKind(ctx, node.text("type"))
		if !proxy {
			return nil, E.New("Selected node is unavailable for sharing")
		}
		entry := uiNodeOf("tag", name)
		uiNodePut(entry, "link", uiNodeShareLink(ctx, node, endpoint))
		shares = append(shares, entry)
		nodes = append(nodes, node)
	}
	content, err := uiNodeEncode(nodes, false)
	if err != nil {
		return nil, E.New("Unable to encode selected nodes")
	}
	response, err := uiNodeEncode(uiNodeOf("nodes", shares, "json", content), false)
	if err != nil {
		return nil, E.New("Unable to encode selected nodes")
	}
	return wrapString(response), nil
}

// Compare the core's canonical options after parsing the URI. Fields without
// an equivalent in the URI, including references to local dialers, cause the
// standard link to be omitted instead of dropping those options.
func uiNodeShareCanonical(ctx context.Context, node *uiNodeObject, endpoint bool) *uiNodeObject {
	section := "outbounds"
	if endpoint {
		section = "endpoints"
	}
	content, err := uiNodeEncode(uiNodeOf(section, []any{node}), false)
	if err != nil {
		return nil
	}
	options, err := parseConfig(ctx, content)
	if err != nil {
		return nil
	}
	var data []byte
	if endpoint {
		data, err = options.Endpoints[0].MarshalJSONContext(ctx)
	} else {
		data, err = options.Outbounds[0].MarshalJSONContext(ctx)
	}
	if err != nil {
		return nil
	}
	result, err := uiNodeDecodeObject(string(data))
	if err != nil {
		return nil
	}
	switch result.text("type") {
	case "socks":
		if result.text("version") == "5" {
			result.remove("version")
		}
	case "vless":
		if result.text("packet_encoding") == "xudp" {
			result.remove("packet_encoding")
		}
	}
	return result
}

func uiNodeShareLink(ctx context.Context, node *uiNodeObject, endpoint bool) string {
	original := uiNodeShareCanonical(ctx, node, endpoint)
	if original == nil {
		return ""
	}
	link := uiNodeShareURI(original)
	if link == "" {
		return ""
	}
	decoded, err := uiNodeLinkParse(link)
	if err != nil {
		return ""
	}
	// A locally configured VMess auto cipher uses zero with TLS. Imported
	// links interpret auto as AEAD, so the export uses the effective cipher.
	if original.text("type") == "vmess" {
		original.set("security", uiNodeShareVMessCipher(original))
	}
	if encoded := uiNodeShareCanonical(ctx, decoded, endpoint); encoded == nil || !uiNodeEqual(original, encoded) {
		return ""
	}
	return link
}

func uiNodeShareEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}

func uiNodeShareFields(query url.Values, values *uiNodeObject, fields ...string) {
	for _, field := range fields {
		key, parameter, renamed := strings.Cut(field, "=")
		if !renamed {
			parameter = key
		}
		if value := values.scalar(key); value != "" {
			query.Set(parameter, value)
		}
	}
}

func uiNodeShareList(query url.Values, key string, values any) {
	if list := uiNodeClashTexts(values); len(list) > 0 {
		query.Set(key, strings.Join(list, ","))
	}
}

func uiNodeShareTLS(query url.Values, tls *uiNodeObject, quic bool) {
	if tls.get("enabled") != true {
		query.Set("security", "none")
		return
	}
	query.Set("security", "tls")
	uiNodeShareFields(query, tls, "server_name=sni", "insecure=allowInsecure", "disable_sni",
		"fragment=tls_fragment", "record_fragment=tls_record_fragment")
	if quic && tls.get("insecure") == true {
		query.Del("allowInsecure")
		query.Set("insecure", "1")
	}
	uiNodeShareList(query, "alpn", tls.get("alpn"))
	if utls := tls.object("utls"); utls.get("enabled") == true {
		uiNodeShareFields(query, utls, "fingerprint=fp")
	}
	if reality := tls.object("reality"); reality.get("enabled") == true {
		query.Set("security", "reality")
		uiNodeShareFields(query, reality, "public_key=pbk", "short_id=sid")
	}
	if ech := tls.object("ech"); ech.get("enabled") == true {
		query.Set("ech", "true")
		uiNodeShareFields(query, ech, "query_server_name=ech_server_name")
		for _, line := range uiNodeClashTexts(ech.get("config")) {
			if !strings.HasPrefix(line, "-----") {
				query.Set("ech_config", query.Get("ech_config")+line)
			}
		}
	}
	var pins []string
	for _, pin := range uiNodeClashTexts(tls.get("certificate_sha256")) {
		if hash, ok := uiNodeBase64(pin); ok && len(hash) == 32 {
			pins = append(pins, hex.EncodeToString(hash))
		}
	}
	if len(pins) > 0 {
		key := "pcs"
		if quic {
			key = "pinSHA256"
		}
		query.Set(key, strings.Join(pins, ","))
	}
}

func uiNodeShareTransport(query url.Values, transport *uiNodeObject, tls bool) {
	switch kind := transport.text("type"); kind {
	case "":
		query.Set("type", "tcp")
	case "http":
		if tls {
			query.Set("type", "http")
		} else {
			query.Set("type", "tcp")
			query.Set("headerType", "http")
		}
		uiNodeShareList(query, "host", transport.get("host"))
		uiNodeShareFields(query, transport, "path", "method")
	case "ws":
		query.Set("type", kind)
		if hosts := uiNodeClashHeader(transport.object("headers"), "Host"); len(hosts) == 1 {
			query.Set("host", hosts[0])
		}
		uiNodeShareFields(query, transport, "path")
		if size := transport.scalar("max_early_data"); size != "" &&
			transport.text("early_data_header_name") == "Sec-WebSocket-Protocol" {
			query.Set("path", query.Get("path")+"?ed="+size)
		} else {
			uiNodeShareFields(query, transport, "max_early_data", "early_data_header_name")
		}
	case "grpc":
		query.Set("type", kind)
		uiNodeShareFields(query, transport, "service_name=serviceName")
	case "httpupgrade", "xhttp":
		query.Set("type", kind)
		uiNodeShareList(query, "host", transport.get("host"))
		uiNodeShareFields(query, transport, "path", "mode")
	case "quic":
		query.Set("type", kind)
	}
}

func uiNodeShareVMessCipher(node *uiNodeObject) string {
	if cipher := node.text("security"); cipher != "" && cipher != "auto" {
		return cipher
	}
	if node.object("tls").get("enabled") == true {
		return "zero"
	}
	if vmess.AutoSecurityType() == vmess.SecurityTypeAes128Gcm {
		return "aes-128-gcm"
	}
	return "chacha20-poly1305"
}

func uiNodeShareVMess(node *uiNodeObject, query url.Values) string {
	network, header, path := query.Get("type"), "none", query.Get("path")
	switch network {
	case "tcp":
		if query.Get("headerType") == "http" {
			header = "http"
		}
	case "grpc":
		path = query.Get("serviceName")
	case "http":
		network = "h2"
	}
	tls := ""
	if query.Get("security") == "tls" {
		tls = "tls"
	}
	fields := uiNodeOf("v", "2", "ps", node.text("tag"), "add", node.text("server"),
		"port", node.scalar("server_port"), "id", node.text("uuid"), "aid", node.scalar("alter_id"),
		"scy", uiNodeShareVMessCipher(node), "net", network, "type", header,
		"host", query.Get("host"), "path", path, "tls", tls)
	if fields.text("aid") == "" {
		fields.set("aid", "0")
	}
	for _, key := range []string{"sni", "alpn", "fp", "allowInsecure", "mode"} {
		uiNodePut(fields, key, query.Get(key))
	}
	content, err := uiNodeEncode(fields, false)
	if err != nil {
		return ""
	}
	return "vmess://" + base64.StdEncoding.EncodeToString([]byte(content))
}

func uiNodeShareURI(node *uiNodeObject) string {
	scheme := node.text("type")
	host, port := node.text("server"), node.scalar("server_port")
	var credentials, path string
	query := make(url.Values)
	switch scheme {
	case "shadowsocks":
		scheme = "ss"
		method, password := node.text("method"), node.text("password")
		if strings.HasPrefix(method, "2022-") {
			credentials = uiNodeShareEscape(method) + ":" + uiNodeShareEscape(password)
		} else {
			credentials = base64.RawURLEncoding.EncodeToString([]byte(method + ":" + password))
		}
		if plugin := node.text("plugin"); plugin != "" {
			path = "/"
			if opts := node.text("plugin_opts"); opts != "" {
				plugin += ";" + opts
			}
			query.Set("plugin", plugin)
		}
	case "vless", "vmess", "trojan":
		tls := node.object("tls")
		uiNodeShareTLS(query, tls, false)
		transport := node.object("transport")
		uiNodeShareTransport(query, transport, tls.get("enabled") == true)
		switch scheme {
		case "vless":
			credentials = uiNodeShareEscape(node.text("uuid"))
			query.Set("encryption", "none")
			uiNodeShareFields(query, node, "encryption", "flow", "packet_encoding=packetEncoding")
			if node.get("packet_encoding") == "" {
				query.Set("packetEncoding", "none")
			}
		case "vmess":
			return uiNodeShareVMess(node, query)
		case "trojan":
			credentials = uiNodeShareEscape(node.text("password"))
		}
	case "hysteria", "hysteria2", "tuic", "anytls":
		uiNodeShareTLS(query, node.object("tls"), scheme != "anytls")
		if scheme == "tuic" {
			credentials = uiNodeShareEscape(node.text("uuid")) + ":" + uiNodeShareEscape(node.text("password"))
			uiNodeShareFields(query, node, "congestion_control", "udp_relay_mode", "udp_over_stream", "zero_rtt_handshake", "heartbeat")
		} else {
			credentials = uiNodeShareEscape(node.text("password"))
			uiNodeShareFields(query, node, "idle_session_check_interval", "idle_session_timeout", "min_idle_session")
		}
		if scheme == "hysteria" || scheme == "hysteria2" {
			uiNodeShareFields(query, node, "auth_str=auth", "up_mbps=upmbps", "down_mbps=downmbps", "hop_interval", "hop_interval_max",
				"obfs=obfsParam", "recv_window_conn", "recv_window", "disable_mtu_discovery", "bbr_profile", "disable_chrome_parrot")
			if ports := uiNodeClashTexts(node.get("server_ports")); len(ports) > 0 {
				port = strings.ReplaceAll(strings.Join(ports, ","), ":", "-")
			}
			if obfs := node.object("obfs"); obfs != nil {
				uiNodeShareFields(query, obfs, "type=obfs", "password=obfs-password", "min_packet_size", "max_packet_size")
			}
		}
	case "socks", "naive":
		user, password := node.text("username"), node.text("password")
		if user != "" || password != "" {
			credentials = uiNodeShareEscape(user) + ":" + uiNodeShareEscape(password)
		}
		if scheme == "socks" {
			version := node.text("version")
			if version == "" {
				version = "5"
			}
			scheme += version
		} else {
			scheme = "naive+https"
			if node.get("quic") == true {
				scheme = "naive+quic"
			}
			uiNodeShareFields(query, node, "insecure_concurrency=insecure-concurrency", "quic_congestion_control=congestion_control")
			uiNodeShareTLS(query, node.object("tls"), false)
		}
	case "wireguard":
		peers := node.list("peers")
		if len(peers) != 1 {
			return ""
		}
		peer, _ := peers[0].(*uiNodeObject)
		host, port = peer.text("address"), peer.scalar("port")
		scheme = "wg"
		uiNodeShareFields(query, node, "private_key", "mtu", "udp_timeout")
		uiNodeShareList(query, "local_address", node.get("address"))
		uiNodeShareFields(query, peer, "public_key", "pre_shared_key", "persistent_keepalive_interval")
		uiNodeShareList(query, "allowed_ips", peer.get("allowed_ips"))
		switch value := peer.get("reserved").(type) {
		case string:
			query.Set("reserved", value)
		case []any:
			uiNodeShareList(query, "reserved", value)
		}
	default:
		return ""
	}
	if host == "" || strings.ContainsAny(host, "/?#@[]% \t\r\n") || port == "" || port == "0" {
		return ""
	}
	if node.get("udp_over_tcp") == true {
		query.Set("uot", "1")
	}
	if mux := node.object("multiplex"); mux.get("enabled") == true {
		query.Set("mux", "true")
		uiNodeShareFields(query, mux, "protocol=mux_protocol", "padding=mux_padding")
	}
	uiNodeShareFields(query, node, "tcp_fast_open", "tcp_multi_path", "udp_fragment")
	if credentials != "" {
		credentials += "@"
	}
	if len(query) > 0 {
		path += "?" + strings.ReplaceAll(query.Encode(), "+", "%20")
	}
	return scheme + "://" + credentials + net.JoinHostPort(host, port) + path + "#" + uiNodeShareEscape(node.text("tag"))
}
