package libbox

import (
	"slices"
	"strconv"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	E "github.com/sagernet/sing/common/exceptions"
)

// The marker is a JSON comment, accepted and ignored by the core's decoder.
// It travels with the configuration, including shares and backups, without
// introducing a core option or a separate runtime node document.
const uiNodeLocalPrefix = "// b-box-local-nodes: "

func uiNodeLocalTags(config string) ([]string, bool, error) {
	header := strings.TrimLeft(config, " \t\r\n")
	if !strings.HasPrefix(header, uiNodeLocalPrefix) {
		return nil, false, nil
	}
	header, _, _ = strings.Cut(header, "\n")
	tags, err := uiNodeTextList(strings.TrimSpace(strings.TrimPrefix(header, uiNodeLocalPrefix)))
	if err != nil {
		return nil, true, E.Cause(err, "decode local node names")
	}
	return tags, true, nil
}

func uiNodeExistingLocal(root *uiNodeObject, tags []string) []string {
	ctx := baseContext(nil)
	kept := make([]string, 0, len(tags))
	for _, tag := range tags {
		key, index := uiNodeFind(root, tag)
		if index < 0 || slices.Contains(kept, tag) {
			continue
		}
		node, _ := root.list(key)[index].(*uiNodeObject)
		if isNode, _ := uiNodeKind(ctx, node.text("type")); isNode {
			kept = append(kept, tag)
		}
	}
	return kept
}

func uiNodeMarkConfig(config string, root *uiNodeObject, tags []string, markEmpty bool) (string, error) {
	kept := uiNodeExistingLocal(root, tags)
	if len(kept) == 0 && !markEmpty {
		return config, nil
	}
	list := make([]any, len(kept))
	for index, tag := range kept {
		list[index] = tag
	}
	header, err := uiNodeEncode(list, false)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimLeft(config, " \t\r\n")
	if strings.HasPrefix(trimmed, uiNodeLocalPrefix) {
		_, config, _ = strings.Cut(trimmed, "\n")
	}
	return uiNodeLocalPrefix + header + "\n" + config, nil
}

func uiNodeEncodeConfig(root *uiNodeObject, tags []string, markEmpty bool) (string, error) {
	config, err := uiNodeEncode(root, true)
	if err != nil {
		return "", err
	}
	return uiNodeMarkConfig(config, root, tags, markEmpty)
}

// PreserveUIConfigNodes retains local node names after a platform JSON edit
// or core formatting. An explicit marker in edited is authoritative, even
// when empty; a merged download therefore never inherits old upstream names.
func PreserveUIConfigNodes(previous, edited string) (*StringBox, error) {
	tags, marked, err := uiNodeLocalTags(edited)
	if err != nil {
		return nil, err
	}
	if !marked {
		tags, marked, err = uiNodeLocalTags(previous)
		if err != nil {
			return nil, err
		}
	}
	if !marked {
		return wrapString(edited), nil
	}
	root, err := uiNodeDecodeObject(edited)
	if err != nil {
		return nil, err
	}
	config, err := uiNodeMarkConfig(edited, root, tags, true)
	if err != nil {
		return nil, err
	}
	return wrapString(config), nil
}

// UIConfigNodeUpdate is the selected configuration after a node import.
type UIConfigNodeUpdate struct {
	Config  string
	Added   int32
	Skipped int32
}

// uiNodePlaceConfig reserves names against both outbounds and endpoints and
// rewrites detours inside the incoming batch to their final names. Repeated
// imports reuse identical nodes; remote merges keep every local node distinct
// from upstream nodes, with upstream retaining its original names.
func uiNodePlaceConfig(root *uiNodeObject, incoming []*uiNodeObject, deduplicate bool) ([]string, int) {
	nodeReferences := make([][]uiNodeReference, len(incoming))
	var references []uiNodeReference
	for index, node := range incoming {
		nodeReferences[index] = uiNodeReferences(uiNodeOf("outbounds", []any{node}))
		references = append(references, nodeReferences[index]...)
	}
	existing := make(map[string]*uiNodeObject)
	for _, key := range []string{"outbounds", "endpoints"} {
		for _, item := range root.list(key) {
			node, _ := item.(*uiNodeObject)
			if tag := node.text("tag"); tag != "" {
				existing[tag] = node
			}
		}
	}
	first := make(map[string]int, len(incoming))
	for index, node := range incoming {
		if _, exists := first[node.text("tag")]; !exists {
			first[node.text("tag")] = index
		}
	}
	const visiting, done = 1, 2
	state := make([]int, len(incoming))
	final := make([]string, len(incoming))
	keep := make([]bool, len(incoming))
	placed := make(map[string]*uiNodeObject, len(incoming))
	var resolve func(int)
	resolve = func(index int) {
		if state[index] != 0 {
			return
		}
		state[index] = visiting
		node := incoming[index]
		// Resolve every outbound reference before comparing node content.
		// Nested dialers (such as XHTTP's download connection) affect identity
		// just as the outer detour does.
		for _, reference := range nodeReferences[index] {
			if target, exists := first[reference.tag]; exists && target != index {
				resolve(target)
				if state[target] == done {
					if reference.index < 0 {
						reference.holder.set(reference.key, final[target])
					} else {
						reference.holder.list(reference.key)[reference.index] = final[target]
					}
				}
			}
		}
		base, candidate := node.text("tag"), node.text("tag")
		for number := 2; ; number++ {
			if other, exists := placed[candidate]; exists {
				if deduplicate && uiNodeSame(other, node) {
					break
				}
			} else if other, exists := existing[candidate]; exists {
				if deduplicate && uiNodeSame(other, node) {
					break
				}
			} else {
				node.set("tag", candidate)
				placed[candidate], keep[index] = node, true
				break
			}
			candidate = base + " " + strconv.Itoa(number)
		}
		final[index], state[index] = candidate, done
	}
	for index := range incoming {
		resolve(index)
	}
	for _, reference := range references {
		if index, exists := first[reference.tag]; exists {
			if reference.index < 0 {
				reference.holder.set(reference.key, final[index])
			} else {
				reference.holder.list(reference.key)[reference.index] = final[index]
			}
		}
	}
	ctx := baseContext(nil)
	added := make([]string, 0, len(incoming))
	for index, node := range incoming {
		if !keep[index] {
			continue
		}
		key := "outbounds"
		if _, endpoint := uiNodeKind(ctx, node.text("type")); endpoint {
			key = "endpoints"
		}
		root.set(key, append(root.list(key), node))
		added = append(added, final[index])
	}
	return added, len(incoming) - len(added)
}

// uiNodeGroupMember excludes unresolved or cyclic detours and detours through
// any group. Putting such a node into a group could create a routing loop.
func uiNodeGroupMember(root *uiNodeObject, tag string) bool {
	state := make(map[string]int)
	var visit func(string) bool
	visit = func(tag string) bool {
		if state[tag] != 0 {
			return state[tag] == 2
		}
		key, index := uiNodeFind(root, tag)
		if index < 0 {
			return false
		}
		node, _ := root.list(key)[index].(*uiNodeObject)
		if uiNodeIsGroup(node) {
			return false
		}
		state[tag] = 1
		for _, detour := range uiNodeDialDetours(node) {
			if !visit(detour) {
				return false
			}
		}
		state[tag] = 2
		return true
	}
	return tag == "" || visit(tag)
}

func uiNodeAppendGroups(root *uiNodeObject, tags []string, defaultProfile bool) {
	eligible := make([]any, 0, len(tags))
	for _, tag := range tags {
		if uiNodeGroupMember(root, tag) {
			eligible = append(eligible, tag)
		}
	}
	if len(eligible) == 0 && !defaultProfile {
		return
	}
	for _, item := range root.list("outbounds") {
		group, _ := item.(*uiNodeObject)
		if !uiNodeIsGroup(group) {
			continue
		}
		previous := group.list("outbounds")
		members := slices.Clone(previous)
		for _, tag := range eligible {
			if !slices.Contains(members, tag) {
				members = append(members, tag)
			}
		}
		// Direct is only the empty default automatic group's fallback.
		// Repair mixed members from earlier imports, including duplicates
		// that do not append any new nodes, once a usable proxy exists.
		if defaultProfile && group.text("tag") == "auto" && group.text("type") == C.TypeURLTest {
			ctx := baseContext(nil)
			direct := make(map[string]bool)
			hasProxy := false
			for _, member := range members {
				tag, isText := member.(string)
				if !isText {
					continue
				}
				key, index := uiNodeFind(root, tag)
				if index < 0 {
					continue
				}
				node, _ := root.list(key)[index].(*uiNodeObject)
				if node.text("type") == C.TypeDirect {
					direct[tag] = true
				} else if isNode, _ := uiNodeKind(ctx, node.text("type")); isNode && !uiSubscriptionGroupMemberCycle(root, tag, group.text("tag")) {
					hasProxy = true
				}
			}
			if hasProxy {
				members = slices.DeleteFunc(members, func(member any) bool {
					tag, _ := member.(string)
					return direct[tag]
				})
			}
		}
		if !uiNodeEqual(members, previous) {
			group.set("outbounds", members)
		}
	}
}

// AddUIConfigNodes appends parsed nodes directly to the selected sing-box
// document. Local ownership is recorded in an ignored JSON comment, so a
// subsequent remote refresh can merge the nodes without runtime expansion.
func AddUIConfigNodes(config, nodes string, defaultProfile bool) (*UIConfigNodeUpdate, error) {
	local, _, err := uiNodeLocalTags(config)
	if err != nil {
		return nil, err
	}
	root, err := uiNodeDecodeObject(config)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"outbounds", "endpoints"} {
		if _, isList := root.optionalList(key); !isList {
			return nil, E.New(key, " must be a JSON array")
		}
	}
	incoming, err := uiNodeList(nodes)
	if err != nil {
		return nil, err
	}
	ctx := baseContext(nil)
	for _, node := range incoming {
		if _, err = uiNodeCheck(ctx, node); err != nil {
			return nil, err
		}
	}
	if refusals := uiNodeConstruct(ctx, incoming); len(refusals) > 0 {
		return nil, E.New(incoming[refusals[0].index].text("tag"), ": ", refusals[0].reason)
	}
	added, skipped := uiNodePlaceConfig(root, incoming, true)
	for _, tag := range added {
		if uiSubscriptionDetourCycle(root, tag) {
			return nil, E.New("node ", tag, " has a cyclic detour")
		}
	}
	uiNodeAppendGroups(root, added, defaultProfile)
	content, err := uiNodeEncodeConfig(root, append(local, added...), true)
	if err != nil {
		return nil, err
	}
	return &UIConfigNodeUpdate{Config: content, Added: int32(len(added)), Skipped: int32(skipped)}, nil
}

// MergeUIConfigNodes starts from the new upstream configuration and appends
// only locally added nodes from the current file. Upstream deletions remain
// deletions; upstream name collisions rename the local node and its detours.
// The caller validates the full result before atomically saving the refresh.
func MergeUIConfigNodes(current, incoming string) (*StringBox, error) {
	local, marked, err := uiNodeLocalTags(current)
	if err != nil {
		return nil, err
	}
	if !marked && !strings.HasPrefix(strings.TrimLeft(incoming, " \t\r\n"), uiNodeLocalPrefix) {
		return wrapString(incoming), nil
	}
	root, err := uiNodeDecodeObject(incoming)
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"outbounds", "endpoints"} {
		if _, isList := root.optionalList(key); !isList {
			return nil, E.New(key, " must be a JSON array")
		}
	}
	var nodes []*uiNodeObject
	if len(local) > 0 {
		previous, err := uiNodeDecodeObject(current)
		if err != nil {
			return nil, err
		}
		ctx := baseContext(nil)
		for _, tag := range uiNodeExistingLocal(previous, local) {
			key, index := uiNodeFind(previous, tag)
			node, _ := previous.list(key)[index].(*uiNodeObject)
			if _, err = uiNodeCheck(ctx, node); err != nil {
				return nil, err
			}
			nodes = append(nodes, node)
		}
	}
	if len(nodes) == 0 {
		content, err := uiNodeMarkConfig(incoming, root, nil, true)
		if err != nil {
			return nil, err
		}
		return wrapString(content), nil
	}
	added, _ := uiNodePlaceConfig(root, nodes, false)
	defined := uiNodeDefined(root)
	for _, node := range nodes {
		for _, reference := range uiNodeReferences(uiNodeOf("outbounds", []any{node})) {
			if reference.tag != "" && !defined[reference.tag] {
				return nil, E.New("local node ", node.text("tag"), " refers to missing outbound ", reference.tag)
			}
		}
	}
	uiNodeAppendGroups(root, added, false)
	content, err := uiNodeEncodeConfig(root, added, true)
	if err != nil {
		return nil, err
	}
	return wrapString(content), nil
}
