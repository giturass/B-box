package libbox

// Readers of the forms in which proxy nodes are passed around: lists of share
// links, plain or in base64, Clash and ClashMeta documents, SIP008, wg-quick
// files and the outbounds of sing-box. Only nodes are read; whatever else a
// document holds is left behind.
import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/service"
)

// Unsupported features are distinct from damaged input. Subscription sources
// can omit these nodes with a visible report; unreadable nodes still abort a
// refresh before any existing configuration is changed.
type uiNodeUnsupportedError struct {
	reason string
}

func (e *uiNodeUnsupportedError) Error() string { return e.reason }

func uiNodeUnsupported(reason string) error { return &uiNodeUnsupportedError{reason: reason} }

// uiNodeParsed is what a text was read as.
type uiNodeParsed struct {
	format  string
	nodes   []*uiNodeObject
	skipped []any
	hint    string
}

// The hints a result can carry.
const (
	// The document is a whole configuration, of which only the nodes were read.
	uiNodeHintConfig = "config"
	// The nodes of the document are kept by providers, which are not fetched.
	uiNodeHintProviders = "providers"
)

// skip records a node that is left out. The reason never quotes the node.
func (p *uiNodeParsed) skip(name string, reason error) {
	entry := uiNodeOf("name", name, "reason", reason.Error())
	var unsupported *uiNodeUnsupportedError
	if errors.As(reason, &unsupported) {
		entry.set("unsupported", true)
	}
	p.skipped = append(p.skipped, entry)
}

// uiNodeParseLinks reads share links, one to a line. A line may be a message
// that quotes a link, and a line without one is passed over. Nothing is
// returned for a text without any link.
func uiNodeParseLinks(content string) *uiNodeParsed {
	var parsed *uiNodeParsed
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		start := uiNodeLinkStart(line)
		if start < 0 {
			continue
		}
		text := line[start:]
		// A name may hold spaces; a link without a name ends at the first one.
		if !strings.Contains(text, "#") {
			text, _, _ = strings.Cut(text, " ")
			text, _, _ = strings.Cut(text, "\t")
		}
		if parsed == nil {
			parsed = &uiNodeParsed{format: "links"}
		}
		node, err := uiNodeLinkParse(text)
		if err != nil {
			name := ""
			if link := uiNodeSplitLink(text); link != nil {
				name = link.name
				// v2rayN VMess names come from the inner JSON, even if an
				// outer fragment exists. A blank ps remains an unnamed refusal
				// so subscription detours cannot guess a retained default tag.
				if link.scheme == "vmess" {
					if decoded, valid := uiNodeBase64Text(link.body); valid && strings.HasPrefix(strings.TrimSpace(decoded), "{") {
						name = ""
						if fields, decodeErr := uiNodeDecodeObject(decoded); decodeErr == nil {
							name = fields.scalar("ps")
						}
					}
				}
			}
			parsed.skip(name, err)
			continue
		}
		parsed.nodes = append(parsed.nodes, node)
	}
	return parsed
}

// uiNodeServer reads a Shadowsocks server as SIP008 and the configuration
// file of the Shadowsocks clients write it.
func uiNodeServer(server *uiNodeObject) (*uiNodeObject, error) {
	link := &uiNodeLink{name: server.scalar("remarks"), host: server.scalar("server"), port: server.scalar("server_port")}
	if link.name == "" {
		link.name = server.scalar("name")
	}
	node, err := uiNodeLinkNode(link, "shadowsocks", 0)
	if err != nil {
		return nil, err
	}
	node.set("method", strings.ToLower(server.scalar("method")))
	node.set("password", server.scalar("password"))
	if err = uiNodeShadowsocksPlugin(node, server.scalar("plugin"), server.scalar("plugin_opts")); err != nil {
		return nil, err
	}
	return node, nil
}

// uiNodeRegistered reports a type the core knows, whether a node or not.
func uiNodeRegistered(ctx context.Context, nodeType string) bool {
	if registry := service.FromContext[option.EndpointOptionsRegistry](ctx); registry != nil {
		if _, loaded := registry.CreateOptions(nodeType); loaded {
			return true
		}
	}
	if registry := service.FromContext[option.OutboundOptionsRegistry](ctx); registry != nil {
		if _, loaded := registry.CreateOptions(nodeType); loaded {
			return true
		}
	}
	return false
}

// The forms entry tells apart.
const (
	uiNodeFormatBox    = "sing-box"
	uiNodeFormatSIP008 = "sip008"
)

// entry reads an object that stands for one node, an outbound or endpoint of
// sing-box or a Shadowsocks server, and returns which of the two it was, or
// nothing for an object that is neither.
func (p *uiNodeParsed) entry(ctx context.Context, item any) string {
	entry, isObject := item.(*uiNodeObject)
	if !isObject {
		return ""
	}
	nodeType, typed := entry.get("type").(string)
	if !typed {
		if entry.get("server") == nil || entry.get("method") == nil {
			return ""
		}
		node, err := uiNodeServer(entry)
		if err != nil {
			p.skip(entry.scalar("remarks"), err)
		} else {
			p.nodes = append(p.nodes, node)
		}
		return uiNodeFormatSIP008
	}
	if isNode, _ := uiNodeKind(ctx, nodeType); !isNode {
		// A group or a way out that is not a server is not a node, and is
		// passed over without a word.
		if !uiNodeRegistered(ctx, nodeType) {
			p.skip(entry.text("tag"), E.New("this protocol is not supported by this core"))
		}
		return uiNodeFormatBox
	}
	node := entry.retagged(entry.text("tag"))
	if node.text("tag") == "" {
		name := node.text("type")
		if server := node.scalar("server"); server != "" {
			name = server
			if port := node.scalar("server_port"); port != "" {
				name = net.JoinHostPort(server, port)
			}
		}
		node.set("tag", name)
	}
	// The resolver is a DNS server of the document the node came from. The
	// configuration that uses the node names its own.
	node.remove("domain_resolver")
	if transport := node.object("transport"); transport.text("type") == "xhttp" {
		if download := transport.object("download_settings"); download != nil {
			download.remove("domain_resolver")
		}
	}
	p.nodes = append(p.nodes, node)
	return uiNodeFormatBox
}

// The settings by which AmneziaWG alters what WireGuard sends.
var uiNodeAmneziaKeys = []string{"jc", "jmin", "jmax", "s1", "s2", "s3", "s4", "h1", "h2", "h3", "h4", "i1", "i2", "i3", "i4", "i5"}

// uiNodeParseWireGuard reads a wg-quick file, an interface with its peers.
// Nothing is returned for a text that is not one.
func uiNodeParseWireGuard(content string) *uiNodeParsed {
	var device, section map[string]string
	var peers []map[string]string
	for _, line := range strings.Split(content, "\n") {
		line, _, _ = strings.Cut(line, "#")
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = nil
			switch strings.ToLower(strings.TrimSpace(line[1 : len(line)-1])) {
			case "interface":
				if device != nil {
					return nil
				}
				device = make(map[string]string)
				section = device
			case "peer":
				section = make(map[string]string)
				peers = append(peers, section)
			}
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found || section == nil {
			continue
		}
		// A setting that takes a list may be written more than once.
		key, value = strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)
		if earlier := section[key]; earlier != "" {
			value = earlier + "," + value
		}
		section[key] = value
	}
	if device["privatekey"] == "" || len(peers) == 0 {
		return nil
	}
	parsed := &uiNodeParsed{format: "wireguard"}
	if node, err := uiNodeWireGuardFile(device, peers); err != nil {
		parsed.skip("", err)
	} else {
		parsed.nodes = append(parsed.nodes, node)
	}
	return parsed
}

func uiNodeWireGuardFile(device map[string]string, peers []map[string]string) (*uiNodeObject, error) {
	for _, key := range uiNodeAmneziaKeys {
		if device[key] != "" {
			return nil, uiNodeUnsupported("AmneziaWG is not supported by this core")
		}
	}
	node := uiNodeOf("type", "wireguard")
	var list []any
	for _, values := range peers {
		// A peer without an address is one that connects to this device.
		if values["endpoint"] == "" {
			return nil, E.New("a peer of this WireGuard file has no endpoint")
		}
		address := &uiNodeLink{}
		address.setAddress(values["endpoint"])
		server, err := uiNodeLinkNode(address, "wireguard", 51820)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			node.set("tag", server.get("tag"))
		}
		peer := uiNodeOf("address", server.get("server"), "port", server.get("server_port"))
		err = uiNodeWireGuardPeer(peer, values["publickey"], values["presharedkey"],
			values["allowedips"], values["persistentkeepalive"], values["reserved"])
		if err != nil {
			return nil, err
		}
		list = append(list, peer)
	}
	uiNodePutDigits(node, "mtu", device["mtu"])
	if addresses := uiNodePrefixes(device["address"]); len(addresses) > 0 {
		node.set("address", addresses)
	}
	node.set("private_key", uiNodeWireGuardKey(device["privatekey"]))
	node.set("peers", list)
	return node, nil
}

// uiNodeParseJSON reads a JSON document. Nothing is returned for one that
// holds no nodes in a known form.
func uiNodeParseJSON(ctx context.Context, value any) *uiNodeParsed {
	parsed := &uiNodeParsed{}
	// each reads a list of entries and notes the form of the first it knows.
	each := func(list []any, unknown string) {
		for _, item := range list {
			form := parsed.entry(ctx, item)
			if parsed.format == "" {
				parsed.format = form
			}
			if entry, _ := item.(*uiNodeObject); form == "" && unknown != "" {
				parsed.skip(entry.scalar("tag"), E.New(unknown))
			}
		}
	}
	root, isObject := value.(*uiNodeObject)
	if !isObject {
		list, _ := value.([]any)
		each(list, "")
	} else if proxies, isList := root.get("proxies").([]any); isList || root.object("proxy-providers") != nil {
		parsed.format = "clash"
		uiNodeClashProxies(proxies, parsed)
		if providers := root.object("proxy-providers"); providers != nil && len(providers.keys) > 0 {
			parsed.hint = uiNodeHintProviders
		}
	} else if servers, isList := root.get("servers").([]any); isList && root.get("outbounds") == nil {
		// SIP008. The servers of another kind of document are not nodes.
		each(servers, "")
		if parsed.format != uiNodeFormatSIP008 {
			return nil
		}
	} else if root.get("outbounds") != nil || root.get("endpoints") != nil {
		parsed.format = uiNodeFormatBox
		each(root.list("outbounds"), "an entry of the document is not an outbound of sing-box")
		each(root.list("endpoints"), "an entry of the document is not an endpoint of sing-box")
		for _, key := range []string{"inbounds", "route", "dns"} {
			if root.get(key) != nil {
				parsed.hint = uiNodeHintConfig
			}
		}
	} else {
		each([]any{root}, "")
	}
	if parsed.format == "" {
		return nil
	}
	return parsed
}

// uiNodeParseText tells the form of a text and reads it. Nothing is returned
// for a text in none of the forms.
func uiNodeParseText(ctx context.Context, content string, decoded bool) *uiNodeParsed {
	content = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(content), "\ufeff"))
	if content == "" {
		return nil
	}
	// A wg-quick file begins with a bracket too, and YAML may be written in
	// braces, so a text that is not JSON is tried as the other forms.
	if strings.ContainsRune("{[/", rune(content[0])) {
		if value, err := uiNodeDecode(content); err == nil {
			return uiNodeParseJSON(ctx, value)
		}
	}
	if strings.Contains(content, "proxies") || strings.Contains(content, "proxy-providers") {
		if proxies, providers, isClash := uiNodeClashYAML(content); isClash {
			parsed := &uiNodeParsed{format: "clash"}
			uiNodeClashProxies(proxies, parsed)
			if providers {
				parsed.hint = uiNodeHintProviders
			}
			return parsed
		}
	}
	if parsed := uiNodeParseLinks(content); parsed != nil {
		return parsed
	}
	if parsed := uiNodeParseWireGuard(content); parsed != nil {
		return parsed
	}
	// A subscription is often the base64 of what it would otherwise be.
	if !decoded {
		if text, valid := uiNodeBase64Text(content); valid {
			return uiNodeParseText(ctx, text, true)
		}
	}
	return nil
}

// check leaves out what the core would not take: a node with options it
// does not read, a node it cannot construct, and a node that connects through
// one that is not among the others.
func (p *uiNodeParsed) check(ctx context.Context) {
	kept := p.nodes[:0]
	for _, node := range p.nodes {
		if _, err := uiNodeProblem(ctx, node); err != nil {
			p.skip(node.text("tag"), err)
		} else {
			kept = append(kept, node)
		}
	}
	p.nodes = kept
	refused := make(map[int]string)
	for _, refusal := range uiNodeConstruct(ctx, p.nodes) {
		refused[refusal.index] = refusal.reason
	}
	kept = p.nodes[:0]
	for index, node := range p.nodes {
		if reason, isRefused := refused[index]; isRefused {
			p.skip(node.text("tag"), E.New(reason))
		} else {
			kept = append(kept, node)
		}
	}
	p.nodes = kept
	graph := make([]any, len(p.nodes))
	for index, node := range p.nodes {
		graph[index] = node
	}
	root := uiNodeOf("outbounds", graph)
	kept = p.nodes[:0]
	for _, node := range p.nodes {
		if uiSubscriptionDetourCycle(root, node.text("tag")) {
			p.skip(node.text("tag"), E.New("this node has a cyclic detour"))
		} else {
			kept = append(kept, node)
		}
	}
	p.nodes = kept
	// Leaving one node out may leave another without its detour.
	for changed := true; changed; {
		changed = false
		names := make(map[string]bool, len(p.nodes))
		for _, node := range p.nodes {
			names[node.text("tag")] = true
		}
		kept = p.nodes[:0]
		for _, node := range p.nodes {
			missing := false
			for _, detour := range uiNodeDialDetours(node) {
				if !names[detour] || detour == node.text("tag") {
					missing = true
					break
				}
			}
			if missing {
				p.skip(node.text("tag"), E.New("this node connects through another that is not among the nodes read"))
				changed = true
			} else {
				kept = append(kept, node)
			}
		}
		p.nodes = kept
	}
}

func (p *uiNodeParsed) result() (*StringBox, error) {
	nodes := make([]any, len(p.nodes))
	for index, node := range p.nodes {
		nodes[index] = node
	}
	skipped := p.skipped
	if skipped == nil {
		skipped = []any{}
	}
	content, err := uiNodeEncode(uiNodeOf("format", p.format, "nodes", nodes, "skipped", skipped, "hint", p.hint), false)
	if err != nil {
		return nil, err
	}
	return wrapString(content), nil
}

// ParseUINodes reads proxy nodes from a text in any of the forms they are
// passed around in: share links, a subscription in base64, a Clash document,
// SIP008, a wg-quick file or outbounds of sing-box. The result holds the
// nodes, each node that was left out with the reason, and a hint for a
// document that holds more than nodes. Only a text in none of the forms is an
// error, so a text without a usable node is still a result.
func ParseUINodes(content string) (*StringBox, error) {
	ctx := baseContext(nil)
	parsed := uiNodeParseText(ctx, content, false)
	if parsed == nil {
		return nil, E.New("the text is not in a known form of proxy nodes")
	}
	parsed.check(ctx)
	return parsed.result()
}
