package libbox

// The field correspondence follows MiChongs/sing-box f88982450a13a818f680de884663fa914798e84a
// option/v2rayxhttp.go and provider/parser/xhttp.go. This reader is implemented
// here: malformed settings fail instead of falling back to transport defaults.
import (
	"strings"

	E "github.com/sagernet/sing/common/exceptions"
)

const uiNodeXHTTPUnsupported = "an XHTTP setting is not supported by this core"

func uiNodeXHTTPKey(key string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
}

// Separators and case are aliases only for known option names. Arbitrary
// fields and conflicting aliases are never discarded or resolved by order.
func uiNodeXHTTPFields(source *uiNodeObject, names string, aliases map[string]string) (*uiNodeObject, error) {
	known := make(map[string]string)
	for _, name := range strings.Fields(names) {
		known[uiNodeXHTTPKey(name)] = name
	}
	for alias, name := range aliases {
		known[uiNodeXHTTPKey(alias)] = name
	}
	target := uiNodeOf()
	for _, key := range source.keys {
		name, supported := known[uiNodeXHTTPKey(key)]
		if !supported {
			return nil, uiNodeUnsupported(uiNodeXHTTPUnsupported)
		}
		value := source.get(key)
		if previous, exists := target.values[name]; exists && !uiNodeEqual(previous, value) {
			return nil, E.New("the XHTTP settings contain conflicting aliases")
		}
		target.set(name, value)
	}
	return target, nil
}

const uiNodeXHTTPNames = `host path mode headers x_padding_bytes x_padding_obfs_mode
	x_padding_key x_padding_header x_padding_placement x_padding_method uplink_http_method
	session_placement session_key session_id_table session_id_length seq_placement seq_key
	uplink_data_placement uplink_data_key uplink_chunk_size no_grpc_header no_sse_header
	sc_max_each_post_bytes sc_min_posts_interval_ms sc_max_buffered_posts sc_stream_up_server_secs
	server_max_header_bytes xmux download_settings user_agent quic_congestion quic_up`

var uiNodeXHTTPAliases = map[string]string{
	"sessionIDPlacement": "session_placement", "sessionIDKey": "session_key",
	"sessionTable": "session_id_table", "sessionLength": "session_id_length",
	"reuseSettings": "xmux",
}

const uiNodeXHTTPXmuxNames = `max_concurrency max_connections c_max_reuse_times
	h_max_request_times h_max_reusable_secs h_keep_alive_period`

func uiNodeXHTTPOptions(source *uiNodeObject) (*uiNodeObject, error) {
	options, err := uiNodeXHTTPFields(source, uiNodeXHTTPNames, uiNodeXHTTPAliases)
	if err != nil {
		return nil, err
	}
	if value := options.get("xmux"); value != nil {
		object, valid := value.(*uiNodeObject)
		if !valid {
			return nil, E.New("the XHTTP reuse settings must be an object")
		}
		mux, err := uiNodeXHTTPFields(object, uiNodeXHTTPXmuxNames, nil)
		if err != nil {
			return nil, err
		}
		options.set("xmux", mux)
	}
	if value := options.get("download_settings"); value != nil {
		if _, valid := value.(*uiNodeObject); !valid {
			return nil, E.New("the XHTTP download settings must be an object")
		}
	}
	return options, nil
}

// XHTTP disallows Host in headers. Panels conventionally put it there when
// host is omitted, so move it to the host option and preserve other headers.
func uiNodeXHTTPHeaders(options *uiNodeObject) error {
	return uiNodeXHTTPHeadersWithHost(options, true)
}

func uiNodeXHTTPHeadersWithHost(options *uiNodeObject, fallback bool) error {
	if host := options.get("host"); host != nil {
		switch typed := host.(type) {
		case string:
			options.set("host", uiNodeCommaList(typed))
		case []any:
			for _, item := range typed {
				if _, valid := item.(string); !valid {
					return E.New("the XHTTP host must be a string or a list of strings")
				}
			}
		default:
			return E.New("the XHTTP host must be a string or a list of strings")
		}
	}
	if raw := options.get("headers"); raw != nil {
		headers, valid := raw.(*uiNodeObject)
		if !valid {
			return E.New("the XHTTP headers must be an object")
		}
		copied := uiNodeOf()
		for _, key := range headers.keys {
			value := headers.get(key)
			switch typed := value.(type) {
			case string:
			case []any:
				for _, item := range typed {
					if _, valid := item.(string); !valid {
						return E.New("the XHTTP header values must be strings")
					}
				}
			default:
				return E.New("the XHTTP header values must be strings")
			}
			if strings.EqualFold(key, "Host") {
				if fallback && len(uiNodeClashTexts(options.get("host"))) == 0 {
					options.set("host", uiNodeCommaList(strings.Join(uiNodeClashTexts(value), ",")))
				}
			} else {
				copied.set(key, value)
			}
		}
		options.set("headers", copied)
	}
	return nil
}

func uiNodeXHTTPLink(link *uiNodeLink) (*uiNodeObject, error) {
	if link.xhttp != nil {
		return link.xhttp, nil
	}
	options := uiNodeOf()
	if extra := link.value("extra"); extra != "" {
		object, err := uiNodeDecodeObject(extra)
		if err != nil {
			return nil, E.New("the XHTTP extra settings must be a JSON object")
		}
		options, err = uiNodeXHTTPOptions(object)
		if err != nil {
			return nil, err
		}
	}
	// Xray share links take these three fields from the outer link, even when
	// they are absent there. A Host header in extra remains a valid fallback.
	for _, key := range []string{"host", "path", "mode"} {
		options.remove(key)
		uiNodePut(options, key, link.value(key))
	}
	if err := uiNodeXHTTPHeaders(options); err != nil {
		return nil, err
	}
	if source := options.object("download_settings"); source != nil {
		download, err := uiNodeXHTTPShareDownload(source)
		if err != nil {
			return nil, err
		}
		options.set("download_settings", download)
	}
	options.set("type", "xhttp")
	return options, nil
}

func uiNodeXHTTPClash(proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	if first, second := proxy.get("xhttp-opts"), proxy.get("splithttp-opts"); first != nil && second != nil && !uiNodeEqual(first, second) {
		return nil, E.New("the XHTTP settings contain conflicting aliases")
	}
	source := proxy.object("xhttp-opts")
	if source == nil {
		source = proxy.object("splithttp-opts")
	}
	if source == nil {
		if proxy.get("xhttp-opts") != nil || proxy.get("splithttp-opts") != nil {
			return nil, E.New("the XHTTP settings must be an object")
		}
		source = uiNodeOf()
	}
	options, err := uiNodeXHTTPOptions(source)
	if err != nil {
		return nil, err
	}
	if err = uiNodeXHTTPHeaders(options); err != nil {
		return nil, err
	}
	if source := options.object("download_settings"); source != nil {
		download, err := uiNodeXHTTPClashDownload(source, options, proxy, link)
		if err != nil {
			return nil, err
		}
		options.set("download_settings", download)
	}
	options.set("type", "xhttp")
	return options, nil
}

// Native snake_case download settings retain the core's dialer/TLS options.
// Xray streamSettings are converted explicitly, without upload inheritance.
func uiNodeXHTTPShareDownload(source *uiNodeObject) (*uiNodeObject, error) {
	xray := false
	for _, key := range source.keys {
		switch uiNodeXHTTPKey(key) {
		case "address", "port", "network", "security", "tlssettings", "realitysettings", "xhttpsettings", "splithttpsettings":
			xray = true
		}
	}
	if !xray {
		return uiNodeXHTTPNativeDownload(source)
	}
	stream, err := uiNodeXHTTPFields(source,
		"address port network security alpn tls_settings reality_settings xhttp_settings splithttp_settings", nil)
	if err != nil {
		return nil, err
	}
	if err = uiNodeXHTTPTextFields(stream, "address network security"); err != nil {
		return nil, err
	}
	if err = uiNodeXHTTPListFields(stream, "alpn"); err != nil {
		return nil, err
	}
	switch strings.ToLower(stream.scalar("network")) {
	case "", "xhttp", "splithttp":
	default:
		return nil, uiNodeUnsupported(uiNodeXHTTPUnsupported)
	}
	options := uiNodeOf()
	settings := stream.object("xhttp_settings")
	if first, second := stream.get("xhttp_settings"), stream.get("splithttp_settings"); first != nil && second != nil && !uiNodeEqual(first, second) {
		return nil, E.New("the XHTTP settings contain conflicting aliases")
	}
	if settings == nil {
		settings = stream.object("splithttp_settings")
	}
	for _, key := range []string{"xhttp_settings", "splithttp_settings"} {
		if stream.get(key) != nil && stream.object(key) == nil {
			return nil, E.New("the XHTTP download settings must be an object")
		}
	}
	if settings != nil {
		options, err = uiNodeXHTTPOptions(settings)
		if err != nil {
			return nil, err
		}
		if options.get("download_settings") != nil {
			return nil, uiNodeUnsupported(uiNodeXHTTPUnsupported)
		}
	}
	if value := stream.get("address"); value != nil {
		options.set("server", value)
	}
	if value := stream.get("port"); value != nil {
		options.set("server_port", value)
	}
	security := strings.ToLower(stream.scalar("security"))
	if stream.get("tls_settings") != nil && security != "tls" || stream.get("reality_settings") != nil && security != "reality" {
		return nil, uiNodeUnsupported(uiNodeXHTTPUnsupported)
	}
	if security == "tls" || security == "reality" {
		link := &uiNodeLink{query: map[string]string{"security": security}}
		name, fields := "tls_settings", "server_name allow_insecure alpn fingerprint"
		if security == "reality" {
			name, fields = "reality_settings", "server_name fingerprint public_key password short_id"
		}
		if value := stream.get(name); value != nil {
			object, valid := value.(*uiNodeObject)
			if !valid {
				return nil, E.New("the XHTTP download TLS settings must be an object")
			}
			tls, err := uiNodeXHTTPFields(object, fields, nil)
			if err != nil {
				return nil, err
			}
			if err = uiNodeXHTTPTextFields(tls, "server_name fingerprint public_key password short_id"); err != nil {
				return nil, err
			}
			if err = uiNodeXHTTPListFields(tls, "alpn"); err != nil {
				return nil, err
			}
			if value := tls.get("allow_insecure"); value != nil {
				if _, valid := value.(bool); !valid {
					return nil, E.New("the XHTTP settings contain an invalid field type")
				}
			}
			link.query["sni"] = tls.scalar("server_name")
			link.query["fp"] = tls.scalar("fingerprint")
			link.query["allowInsecure"] = tls.scalar("allow_insecure")
			link.query["pbk"] = tls.scalar("public_key")
			if link.query["pbk"] == "" {
				link.query["pbk"] = tls.scalar("password")
			}
			link.query["sid"] = tls.scalar("short_id")
			link.query["alpn"] = strings.Join(uiNodeClashTexts(tls.get("alpn")), ",")
		}
		if link.query["alpn"] == "" {
			link.query["alpn"] = strings.Join(uiNodeClashTexts(stream.get("alpn")), ",")
		}
		tls, err := uiNodeLinkTLS(link, uiNodeTLSOptional)
		if err != nil {
			return nil, err
		}
		options.set("tls", tls)
	} else if security != "" && security != "none" {
		return nil, uiNodeUnsupported(uiNodeXHTTPUnsupported)
	}
	if err = uiNodeXHTTPHeaders(options); err != nil {
		return nil, err
	}
	return options, nil
}

func uiNodeXHTTPTextFields(options *uiNodeObject, names string) error {
	for _, name := range strings.Fields(names) {
		if value := options.get(name); value != nil {
			if _, valid := value.(string); !valid {
				return E.New("the XHTTP settings contain an invalid field type")
			}
		}
	}
	return nil
}

func uiNodeXHTTPListFields(options *uiNodeObject, names string) error {
	for _, name := range strings.Fields(names) {
		if value := options.get(name); value != nil {
			switch typed := value.(type) {
			case string:
			case []any:
				for _, item := range typed {
					if _, valid := item.(string); !valid {
						return E.New("the XHTTP settings contain an invalid field type")
					}
				}
			default:
				return E.New("the XHTTP settings contain an invalid field type")
			}
		}
	}
	return nil
}

const uiNodeXHTTPDownloadNames = `enabled server server_port tls detour bind_interface
	inet4_bind_address inet6_bind_address bind_address_no_port protect_path routing_mark reuse_addr netns
	connect_timeout tcp_fast_open tcp_multi_path disable_tcp_keep_alive tcp_keep_alive tcp_keep_alive_interval
	tcp_keep_alive_count udp_gso udp_fragment domain_resolver network_strategy network_type
	fallback_network_type fallback_delay resolve_on_detour tcp_concurrent domain_strategy`

func uiNodeXHTTPNativeDownload(source *uiNodeObject) (*uiNodeObject, error) {
	options, err := uiNodeXHTTPFields(source, uiNodeXHTTPNames+" "+uiNodeXHTTPDownloadNames, uiNodeXHTTPAliases)
	if err != nil {
		return nil, err
	}
	transport := uiNodeOf()
	for _, name := range strings.Fields(uiNodeXHTTPDownloadNames) {
		if value := options.get(name); value != nil {
			transport.set(name, value)
			options.remove(name)
		}
	}
	options, err = uiNodeXHTTPOptions(options)
	if err != nil {
		return nil, err
	}
	if options.get("download_settings") != nil {
		return nil, uiNodeUnsupported(uiNodeXHTTPUnsupported)
	}
	if err = uiNodeXHTTPHeaders(options); err != nil {
		return nil, err
	}
	for _, key := range transport.keys {
		options.set(key, transport.get(key))
	}
	return options, nil
}

// Mihomo's download-settings inherit upload fields and TLS; materialize that
// behavior in the self-contained native download option before storing it.
func uiNodeXHTTPClashDownload(source, upload, proxy *uiNodeObject, link *uiNodeLink) (*uiNodeObject, error) {
	settings, err := uiNodeXHTTPFields(source, uiNodeXHTTPNames+` enabled server port tls alpn
		ech_opts reality_opts skip_cert_verify fingerprint servername sni client_fingerprint disable_sni`, uiNodeXHTTPAliases)
	if err != nil {
		return nil, err
	}
	if settings.get("download_settings") != nil {
		return nil, uiNodeUnsupported(uiNodeXHTTPUnsupported)
	}
	if err = uiNodeXHTTPTextFields(settings, "server servername sni fingerprint client_fingerprint"); err != nil {
		return nil, err
	}
	if err = uiNodeXHTTPListFields(settings, "alpn"); err != nil {
		return nil, err
	}
	for _, key := range []string{"enabled", "tls", "skip_cert_verify", "disable_sni"} {
		if value := settings.get(key); value != nil {
			if _, valid := value.(bool); !valid {
				return nil, E.New("the XHTTP settings contain an invalid field type")
			}
		}
	}
	download := upload.retagged("")
	download.remove("tag")
	download.remove("download_settings")
	download.set("server", link.host)
	port, valid := link.portNumber(443)
	if !valid {
		return nil, E.New("the server port must be a number between 1 and 65535")
	}
	download.set("server_port", uiNodeInt(port))
	tlsProxy := proxy.retagged("")
	tlsProxy.remove("tag")
	if first, second := settings.get("servername"), settings.get("sni"); first != nil && second != nil && !uiNodeEqual(first, second) {
		return nil, E.New("the XHTTP settings contain conflicting aliases")
	}
	if settings.get("servername") != nil || settings.get("sni") != nil {
		// Either explicit spelling replaces both inherited SNI spellings.
		tlsProxy.remove("servername")
		tlsProxy.remove("sni")
	}
	for _, key := range settings.keys {
		value := settings.get(key)
		switch key {
		case "port":
			download.set("server_port", value)
		case "tls", "alpn", "ech_opts", "reality_opts", "skip_cert_verify", "fingerprint", "servername", "sni", "client_fingerprint", "disable_sni":
			tlsProxy.set(strings.ReplaceAll(key, "_", "-"), value)
		default:
			download.set(key, value)
		}
	}
	if settings.get("headers") != nil && settings.get("host") == nil {
		if hosts := uiNodeClashHeader(settings.object("headers"), "Host"); len(hosts) > 0 {
			download.set("host", uiNodeCommaList(strings.Join(hosts, ",")))
		}
	}
	tlsLink := &uiNodeLink{query: make(map[string]string)}
	if err = uiNodeClashTLS(tlsProxy, tlsLink, tlsProxy.truthy("tls")); err != nil {
		return nil, err
	}
	if tlsLink.value("sni") == "" {
		tlsLink.query["sni"] = download.scalar("server")
	}
	tls, err := uiNodeLinkTLS(tlsLink, uiNodeTLSOptional)
	if err != nil {
		return nil, err
	}
	if tls != nil {
		download.set("tls", tls)
	}
	if err = uiNodeXHTTPHeadersWithHost(download, settings.get("host") == nil); err != nil {
		return nil, err
	}
	return uiNodeXHTTPNativeDownload(download)
}
