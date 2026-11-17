package libbox

import (
	"context"
	"slices"
	"strings"

	E "github.com/sagernet/sing/common/exceptions"

	"gopkg.in/yaml.v3"
)

func uiSubscriptionCheckEntries(ctx context.Context, parsed *uiNodeParsed, entries []any, kind string) {
	if parsed == nil {
		return
	}
	for _, item := range entries {
		// The general importer can skip unrelated entries. A source refresh
		// must instead refuse an unreadable item before replacing old nodes.
		probe := &uiNodeParsed{}
		if probe.entry(ctx, item) == "" {
			entry, _ := item.(*uiNodeObject)
			parsed.skip(entry.text("tag"), E.New("an entry of the subscription ", kind, " list is not a proxy node"))
		}
	}
}

func uiSubscriptionParse(ctx context.Context, content string) *uiNodeParsed {
	content = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(content), "\ufeff"))
	candidates := []string{content}
	if decoded, valid := uiNodeBase64Text(content); valid {
		candidates = append(candidates, strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(decoded), "\ufeff")))
	}
	for _, candidate := range candidates {
		value, err := uiNodeDecode(candidate)
		if err != nil {
			continue
		}
		if entries, isList := value.([]any); isList {
			parsed := uiNodeParseJSON(ctx, value)
			uiSubscriptionCheckEntries(ctx, parsed, entries, "node")
			return parsed
		}
		root, isObject := value.(*uiNodeObject)
		if !isObject {
			continue
		}
		if root.get("outbounds") == nil && root.get("endpoints") == nil {
			if entries, isList := root.get("servers").([]any); isList {
				parsed := uiNodeParseJSON(ctx, root)
				if parsed != nil && parsed.format == uiNodeFormatSIP008 {
					uiSubscriptionCheckEntries(ctx, parsed, entries, "SIP008 server")
				}
				return parsed
			}
			continue
		}
		// Only the node lists participate in format detection. Provider,
		// Clash proxy and SIP008 server metadata cannot hide these lists.
		nodes := uiNodeOf()
		for _, key := range []string{"outbounds", "endpoints"} {
			if root.get(key) != nil {
				nodes.set(key, root.get(key))
			}
		}
		parsed := uiNodeParseJSON(ctx, nodes)
		for _, key := range []string{"outbounds", "endpoints"} {
			if _, isList := root.optionalList(key); !isList {
				parsed.skip("", E.New(key, " must be a JSON array"))
			}
		}
		return parsed
	}
	return uiNodeParseText(ctx, content, false)
}

func uiSubscriptionProblems(parsed *uiNodeParsed) error {
	if len(parsed.skipped) == 0 {
		return nil
	}
	reasons := make([]string, 0, len(parsed.skipped))
	for _, item := range parsed.skipped {
		skipped, _ := item.(*uiNodeObject)
		reason := skipped.text("reason")
		if name := skipped.text("name"); name != "" {
			reason = name + ": " + reason
		}
		reasons = append(reasons, reason)
	}
	return E.New("subscription contains invalid proxy nodes: ", strings.Join(reasons, "; "))
}

// Read only the names of discarded groups. A subscription node that names
// such a group must not silently bind to a different local group of that name.
func uiSubscriptionGroupNames(content string) map[string]bool {
	content = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(content), "\ufeff"))
	if decoded, valid := uiNodeBase64Text(content); valid {
		content = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(decoded), "\ufeff"))
	}
	groups := make(map[string]bool)
	if value, err := uiNodeDecode(content); err == nil {
		list, _ := value.([]any)
		if root, isObject := value.(*uiNodeObject); isObject {
			list = append(slices.Clone(root.list("outbounds")), root.list("endpoints")...)
			for _, item := range root.list("proxy-groups") {
				group, _ := item.(*uiNodeObject)
				if tag := group.text("name"); tag != "" {
					groups[tag] = true
				}
			}
		}
		for _, item := range list {
			group, _ := item.(*uiNodeObject)
			if uiNodeIsGroup(group) && group.text("tag") != "" {
				groups[group.text("tag")] = true
			}
		}
	} else if strings.Contains(content, "proxy-groups") {
		var document struct {
			Groups []struct {
				Name string `yaml:"name"`
			} `yaml:"proxy-groups"`
		}
		// YAML decoder errors can quote subscription credentials; the node
		// parser already validated the document, so do not expose those errors.
		if yaml.Unmarshal([]byte(content), &document) == nil {
			for _, group := range document.Groups {
				if group.Name != "" {
					groups[group.Name] = true
				}
			}
		}
	}
	return groups
}

func uiSubscriptionDetourCycle(root *uiNodeObject, tag string) bool {
	seen := make(map[string]bool)
	for tag != "" {
		if seen[tag] {
			return true
		}
		seen[tag] = true
		key, index := uiNodeFind(root, tag)
		if index < 0 {
			return false // The complete reference scan reports the missing name.
		}
		node, _ := root.list(key)[index].(*uiNodeObject)
		tag = node.text("detour")
	}
	return false
}

// UISubscriptionNodeUpdate identifies the nodes owned by one source after its
// content is applied. Tags is a JSON name array for the next refresh.
type UISubscriptionNodeUpdate struct {
	Config string
	Tags   string
	Added  int32
}

// ApplyUISubscriptionNodes appends a new source to an existing configuration or
// replaces only the nodes named in previousTags. Local ownership and nodes of
// other sources survive. Callers persist Config and Tags together after checking
// and successfully applying the complete configuration.
func ApplyUISubscriptionNodes(current, subscription, previousTags string, defaultProfile bool) (*UISubscriptionNodeUpdate, error) {
	if strings.TrimSpace(current) == "" {
		return nil, E.New("a current configuration is required")
	}
	previous, err := uiNodeTextList(previousTags)
	if err != nil {
		return nil, E.Cause(err, "decode subscription node names")
	}
	return uiApplySubscriptionNodes(current, subscription, previous, false, defaultProfile)
}

// RetainUISubscriptionNodes releases a source's surviving nodes to local
// ownership when its URL is removed. It changes only the ownership comment.
func RetainUISubscriptionNodes(current, tags string) (*StringBox, error) {
	retained, err := uiNodeTextList(tags)
	if err != nil {
		return nil, E.Cause(err, "decode subscription node names")
	}
	local, _, err := uiNodeLocalTags(current)
	if err != nil {
		return nil, err
	}
	root, err := uiNodeDecodeObject(current)
	if err != nil {
		return nil, err
	}
	content, err := uiNodeMarkConfig(current, root, append(local, retained...), true)
	if err != nil {
		return nil, err
	}
	return wrapString(content), nil
}

// UpdateUISubscriptionNodes retains the single-URL remote-profile behavior:
// replace all non-local proxies, preserving the configuration's settings.
// An empty current document starts from the app's default configuration.
func UpdateUISubscriptionNodes(current, subscription string) (*StringBox, error) {
	newProfile := strings.TrimSpace(current) == ""
	if newProfile {
		current = uiNodeDefaultConfig
	}
	update, err := uiApplySubscriptionNodes(current, subscription, nil, true, newProfile)
	if err != nil {
		return nil, err
	}
	return wrapString(update.Config), nil
}

func uiApplySubscriptionNodes(current, subscription string, previous []string, replaceAll, defaultProfile bool) (*UISubscriptionNodeUpdate, error) {
	ctx := baseContext(nil)
	parsed := uiSubscriptionParse(ctx, subscription)
	if parsed == nil {
		return nil, E.New("the subscription is not in a known form of proxy nodes")
	}
	// ParseUINodes checks detours within the isolated incoming batch. Here a
	// detour may legitimately name a retained local node or direct outbound,
	// so check node options now and references after placement instead.
	for _, node := range parsed.nodes {
		if _, err := uiNodeProblem(ctx, node); err != nil {
			parsed.skip(node.text("tag"), err)
		}
	}
	if err := uiSubscriptionProblems(parsed); err != nil {
		return nil, err
	}
	for _, refusal := range uiNodeConstruct(ctx, parsed.nodes) {
		parsed.skip(parsed.nodes[refusal.index].text("tag"), E.New(refusal.reason))
	}
	if err := uiSubscriptionProblems(parsed); err != nil {
		return nil, err
	}
	if len(parsed.nodes) == 0 {
		if parsed.hint == uiNodeHintProviders {
			return nil, E.New("subscription contains no usable proxy nodes; providers are not imported")
		}
		return nil, E.New("subscription contains no usable proxy nodes")
	}
	groups := uiSubscriptionGroupNames(subscription)
	for _, node := range parsed.nodes {
		if groups[node.text("detour")] {
			return nil, E.New("subscription node ", node.text("tag"), " refers to discarded subscription group ", node.text("detour"))
		}
	}
	local, _, err := uiNodeLocalTags(current)
	if err != nil {
		return nil, err
	}
	root, err := uiNodeDecodeObject(current)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"outbounds", "endpoints"} {
		if _, isList := root.optionalList(key); !isList {
			return nil, E.New(key, " must be a JSON array")
		}
	}
	local = uiNodeExistingLocal(root, local)
	for _, key := range []string{"outbounds", "endpoints"} {
		list := root.list(key)
		if len(list) == 0 {
			continue
		}
		root.set(key, slices.DeleteFunc(list, func(item any) bool {
			node, _ := item.(*uiNodeObject)
			isNode, _ := uiNodeKind(ctx, node.text("type"))
			return isNode && !slices.Contains(local, node.text("tag")) && (replaceAll || slices.Contains(previous, node.text("tag")))
		}))
	}
	// Reserve local and non-proxy names first. The placement helper rewrites
	// incoming detours and keeps collisions distinct, even for identical nodes.
	added, _ := uiNodePlaceConfig(root, parsed.nodes, false)
	for _, tag := range added {
		if uiSubscriptionDetourCycle(root, tag) {
			return nil, E.New("subscription node ", tag, " has a cyclic detour")
		}
	}
	defined := uiNodeDefined(root)
	for _, item := range root.list("outbounds") {
		group, _ := item.(*uiNodeObject)
		if !uiNodeIsGroup(group) {
			continue
		}
		members := slices.DeleteFunc(slices.Clone(group.list("outbounds")), func(item any) bool {
			tag, isText := item.(string)
			if !isText {
				return false
			}
			if !defined[tag] {
				return true
			}
			key, index := uiNodeFind(root, tag)
			node, _ := root.list(key)[index].(*uiNodeObject)
			isNode, _ := uiNodeKind(ctx, node.text("type"))
			// A replacement can make other sources or local nodes indirectly
			// dial through this group. Remove unsafe proxy memberships while
			// retaining direct access and nested groups such as proxy -> auto.
			return isNode && !uiNodeGroupMember(root, tag)
		})
		group.set("outbounds", members)
	}
	uiNodeAppendGroups(root, added, defaultProfile)
	for _, item := range root.list("outbounds") {
		group, _ := item.(*uiNodeObject)
		if !uiNodeIsGroup(group) {
			continue
		}
		if len(group.list("outbounds")) == 0 {
			return nil, E.New("group ", group.text("tag"), " would be left without a member")
		}
		if selected := group.text("default"); selected != "" && !slices.Contains(group.list("outbounds"), any(selected)) {
			group.remove("default")
		}
	}
	// Group members may follow subscription additions and removals. A route,
	// resolver or local node that still names a removed proxy cannot safely be
	// rewritten to another node, so the whole update fails with its exact path.
	for _, reference := range uiNodeReferences(root) {
		if reference.tag != "" && !defined[reference.tag] {
			return nil, E.New("missing outbound ", reference.tag, " is still used at ", reference.path)
		}
	}
	content, err := uiNodeEncodeConfig(root, local, true)
	if err != nil {
		return nil, err
	}
	names := make([]any, len(added))
	for index, tag := range added {
		names[index] = tag
	}
	tags, err := uiNodeEncode(names, false)
	if err != nil {
		return nil, err
	}
	return &UISubscriptionNodeUpdate{Config: content, Tags: tags, Added: int32(len(added))}, nil
}
