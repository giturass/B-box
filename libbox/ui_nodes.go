package libbox

// B-box node parsing and editing of sing-box configurations. Documents are
// edited as ordered values, so key order, fields unknown to this code and
// integers beyond float64 precision reach the core unchanged.
import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	E "github.com/sagernet/sing/common/exceptions"
	"github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/service"
)

const uiNodeMaxDepth = 512

// uiNodeNumber keeps a JSON number exactly as it was written.
type uiNodeNumber string

func uiNodeInt(value int) uiNodeNumber { return uiNodeNumber(strconv.Itoa(value)) }

type uiNodeObject struct {
	keys   []string
	values map[string]any
}

func uiNodeOf(pairs ...any) *uiNodeObject {
	object := &uiNodeObject{values: make(map[string]any, len(pairs)/2)}
	for index := 0; index+1 < len(pairs); index += 2 {
		object.set(pairs[index].(string), pairs[index+1])
	}
	return object
}

func (o *uiNodeObject) get(key string) any {
	if o == nil {
		return nil
	}
	return o.values[key]
}

func (o *uiNodeObject) set(key string, value any) {
	if _, exists := o.values[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

func (o *uiNodeObject) remove(key string) {
	if _, exists := o.values[key]; exists {
		delete(o.values, key)
		o.keys = slices.DeleteFunc(o.keys, func(item string) bool { return item == key })
	}
}

func (o *uiNodeObject) text(key string) string {
	value, _ := o.get(key).(string)
	return value
}

func (o *uiNodeObject) object(key string) *uiNodeObject {
	value, _ := o.get(key).(*uiNodeObject)
	return value
}

func (o *uiNodeObject) list(key string) []any {
	value, _ := o.get(key).([]any)
	return value
}

// optionalList fails only when the key holds something other than a list.
func (o *uiNodeObject) optionalList(key string) ([]any, bool) {
	value := o.get(key)
	if value == nil {
		return nil, true
	}
	list, isList := value.([]any)
	return list, isList
}

func uiNodeDecode(content string) (any, error) {
	decoder := json.NewDecoder(strings.NewReader(content))
	decoder.UseNumber()
	value, err := uiNodeDecodeValue(decoder, 0)
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, uiNodeSyntaxError(content, err)
	}
	if _, err = decoder.Token(); err != io.EOF {
		if err == nil {
			err = E.New("unexpected content after the JSON document")
		}
		return nil, uiNodeSyntaxError(content, err)
	}
	return value, nil
}

func uiNodeDecodeObject(content string) (*uiNodeObject, error) {
	value, err := uiNodeDecode(content)
	if err != nil {
		return nil, err
	}
	object, isObject := value.(*uiNodeObject)
	if !isObject {
		return nil, E.New("a JSON object is required")
	}
	return object, nil
}

func uiNodeSyntaxError(content string, err error) error {
	var syntaxError *json.SyntaxError
	if !errors.As(err, &syntaxError) {
		return err
	}
	prefix := content[:min(max(int(syntaxError.Offset), 0), len(content))]
	row := strings.Count(prefix, "\n") + 1
	column := len(prefix) - strings.LastIndex(prefix, "\n") - 1
	return E.Extend(syntaxError, "row ", row, ", column ", column)
}

// The decoder is the core's own, so comments and trailing commas are read the
// way the core reads them. Its number type is not exported; it is the only
// token left that prints itself.
func uiNodeDecodeValue(decoder *json.Decoder, depth int) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case json.Delim:
		if depth >= uiNodeMaxDepth {
			return nil, E.New("JSON document is nested too deeply")
		}
		switch value {
		case '{':
			object := uiNodeOf()
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, isKey := keyToken.(string)
				if !isKey {
					return nil, E.New("invalid JSON object key")
				}
				item, err := uiNodeDecodeValue(decoder, depth+1)
				if err != nil {
					return nil, err
				}
				object.set(key, item)
			}
			_, err = decoder.Token()
			return object, err
		case '[':
			list := make([]any, 0)
			for decoder.More() {
				item, err := uiNodeDecodeValue(decoder, depth+1)
				if err != nil {
					return nil, err
				}
				list = append(list, item)
			}
			_, err = decoder.Token()
			return list, err
		}
		return nil, E.New("unexpected JSON delimiter")
	case string, bool, nil:
		return value, nil
	case fmt.Stringer:
		return uiNodeNumber(value.String()), nil
	case float64:
		return uiNodeNumber(strconv.FormatFloat(value, 'f', -1, 64)), nil
	}
	return nil, E.New("unexpected JSON value")
}

type uiNodeWriter struct {
	buffer  bytes.Buffer
	encoder *json.Encoder
	indent  bool
}

func uiNodeEncode(value any, indent bool) (string, error) {
	writer := &uiNodeWriter{indent: indent}
	writer.encoder = json.NewEncoder(&writer.buffer)
	writer.encoder.SetEscapeHTML(false)
	if err := writer.write(value, 0); err != nil {
		return "", err
	}
	return writer.buffer.String(), nil
}

func (w *uiNodeWriter) line(depth int) {
	if w.indent {
		w.buffer.WriteByte('\n')
		for range depth {
			w.buffer.WriteString("  ")
		}
	}
}

func (w *uiNodeWriter) text(value string) error {
	if err := w.encoder.Encode(value); err != nil {
		return err
	}
	w.buffer.Truncate(w.buffer.Len() - 1) // Encode ends every value with a newline.
	return nil
}

func (w *uiNodeWriter) write(value any, depth int) error {
	switch value := value.(type) {
	case nil:
		w.buffer.WriteString("null")
	case *uiNodeObject:
		if len(value.keys) == 0 {
			w.buffer.WriteString("{}")
			return nil
		}
		w.buffer.WriteByte('{')
		for index, key := range value.keys {
			if index > 0 {
				w.buffer.WriteByte(',')
			}
			w.line(depth + 1)
			if err := w.text(key); err != nil {
				return err
			}
			w.buffer.WriteByte(':')
			if w.indent {
				w.buffer.WriteByte(' ')
			}
			if err := w.write(value.values[key], depth+1); err != nil {
				return err
			}
		}
		w.line(depth)
		w.buffer.WriteByte('}')
	case []any:
		if len(value) == 0 {
			w.buffer.WriteString("[]")
			return nil
		}
		w.buffer.WriteByte('[')
		for index, item := range value {
			if index > 0 {
				w.buffer.WriteByte(',')
			}
			w.line(depth + 1)
			if err := w.write(item, depth+1); err != nil {
				return err
			}
		}
		w.line(depth)
		w.buffer.WriteByte(']')
	case string:
		return w.text(value)
	case uiNodeNumber:
		w.buffer.WriteString(string(value))
	case bool:
		w.buffer.WriteString(strconv.FormatBool(value))
	default:
		return E.New("unsupported JSON value")
	}
	return nil
}

// uiNodeEqual compares two documents without regard to object key order.
func uiNodeEqual(left, right any) bool {
	switch left := left.(type) {
	case *uiNodeObject:
		other, isObject := right.(*uiNodeObject)
		if !isObject || len(left.keys) != len(other.keys) {
			return false
		}
		for key, item := range left.values {
			otherItem, exists := other.values[key]
			if !exists || !uiNodeEqual(item, otherItem) {
				return false
			}
		}
		return true
	case []any:
		other, isList := right.([]any)
		if !isList || len(left) != len(other) {
			return false
		}
		for index := range left {
			if !uiNodeEqual(left[index], other[index]) {
				return false
			}
		}
		return true
	}
	return left == right
}

// uiNodeSame reports whether two nodes differ in nothing but their name.
func uiNodeSame(left, right *uiNodeObject) bool {
	count := func(node *uiNodeObject) int {
		if _, named := node.values["tag"]; named {
			return len(node.keys) - 1
		}
		return len(node.keys)
	}
	if count(left) != count(right) {
		return false
	}
	for key, item := range left.values {
		if key == "tag" {
			continue
		}
		other, exists := right.values[key]
		if !exists || !uiNodeEqual(item, other) {
			return false
		}
	}
	return true
}

func uiNodeTextList(content string) ([]string, error) {
	value, err := uiNodeDecode(content)
	if err != nil {
		return nil, err
	}
	list, isList := value.([]any)
	if !isList {
		return nil, E.New("a JSON array of names is required")
	}
	result := make([]string, 0, len(list))
	for _, item := range list {
		text, isText := item.(string)
		if !isText {
			return nil, E.New("a JSON array of names is required")
		}
		result = append(result, text)
	}
	return result, nil
}

// uiNodeList reads an array of nodes, or the nodes of a ParseUINodes result.
func uiNodeList(content string) ([]*uiNodeObject, error) {
	value, err := uiNodeDecode(content)
	if err != nil {
		return nil, err
	}
	if result, isObject := value.(*uiNodeObject); isObject {
		value = result.get("nodes")
	}
	list, isList := value.([]any)
	if !isList {
		return nil, E.New("a JSON array of nodes is required")
	}
	nodes := make([]*uiNodeObject, 0, len(list))
	for _, item := range list {
		node, isObject := item.(*uiNodeObject)
		if !isObject {
			return nil, E.New("a node must be a JSON object")
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

// uiNodeKind reports whether a type is a proxy node and whether the core
// keeps it under endpoints. The removed WireGuard outbound is still
// registered as a stub, so endpoints are asked first.
func uiNodeKind(ctx context.Context, nodeType string) (bool, bool) {
	switch nodeType {
	case "", C.TypeDirect, C.TypeBlock, C.TypeDNS, C.TypeBridge, C.TypeSelector, C.TypeURLTest, C.TypeTailscale:
		return false, false
	}
	if strings.HasSuffix(nodeType, "-server") {
		return false, false
	}
	if registry := service.FromContext[option.EndpointOptionsRegistry](ctx); registry != nil {
		if _, loaded := registry.CreateOptions(nodeType); loaded {
			return true, true
		}
	}
	if registry := service.FromContext[option.OutboundOptionsRegistry](ctx); registry != nil {
		if _, loaded := registry.CreateOptions(nodeType); loaded {
			return true, false
		}
	}
	return false, false
}

// uiNodeProblem validates one node with the core's option decoder and
// reports whether it is an endpoint. The error does not name the node.
func uiNodeProblem(ctx context.Context, node *uiNodeObject) (bool, error) {
	if node.text("tag") == "" {
		return false, E.New("a node needs a name")
	}
	isNode, isEndpoint := uiNodeKind(ctx, node.text("type"))
	if !isNode {
		return false, E.New(node.text("type"), " is not a proxy node type")
	}
	return isEndpoint, uiNodeCheckOptions(ctx, node, isEndpoint)
}

// uiNodeCheck is uiNodeProblem with the node named in the error.
func uiNodeCheck(ctx context.Context, node *uiNodeObject) (bool, error) {
	isEndpoint, err := uiNodeProblem(ctx, node)
	if tag := node.text("tag"); err != nil && tag != "" {
		err = E.Cause(err, tag)
	}
	return isEndpoint, err
}

func uiNodeCheckOptions(ctx context.Context, node *uiNodeObject, isEndpoint bool) error {
	key := "outbounds"
	if isEndpoint {
		key = "endpoints"
	}
	content, err := uiNodeEncode(uiNodeOf(key, []any{node}), false)
	if err != nil {
		return err
	}
	if _, err = parseConfig(ctx, content); err != nil {
		return E.New(uiNodeRedact(node, err.Error()))
	}
	return nil
}

func (o *uiNodeObject) retagged(tag string) *uiNodeObject {
	copied := uiNodeOf()
	for _, key := range o.keys {
		copied.set(key, o.values[key])
	}
	copied.set("tag", tag)
	return copied
}

// The options whose text is a credential.
var uiNodeSecretKeys = []string{
	"password", "uuid", "private_key", "private_key_passphrase", "pre_shared_key",
	"psk", "auth", "auth_str", "obfs", "username", "user", "encryption", "decryption",
}

// uiNodeRedact takes the credentials of a node out of a message of the core,
// which quotes a malformed one.
func uiNodeRedact(value any, message string) string {
	switch typed := value.(type) {
	case *uiNodeObject:
		for _, key := range typed.keys {
			if text, isText := typed.values[key].(string); !isText {
				message = uiNodeRedact(typed.values[key], message)
			} else if len(text) > 3 && slices.Contains(uiNodeSecretKeys, key) {
				message = strings.ReplaceAll(message, text, "…")
				if key == "encryption" || key == "decryption" {
					// The handshake parser may report only a bad RTT, padding or
					// key segment rather than repeating the complete setting.
					for _, part := range strings.Split(text, ".") {
						if len(part) > 3 {
							message = strings.ReplaceAll(message, part, "…")
						}
					}
				}
			}
		}
	case []any:
		for _, item := range typed {
			message = uiNodeRedact(item, message)
		}
	}
	return message
}

// uiNodeRefusal is a node the core would not construct.
type uiNodeRefusal struct {
	index  int
	reason string
}

// uiNodeConstruct builds nodes the way a starting service does and returns
// the ones the core refuses, in order. Options that decode can still fail
// there: an unknown cipher, a malformed key, a protocol left out of the
// build. One such node would keep its configuration from starting. The nodes
// must have passed uiNodeCheck.
func uiNodeConstruct(ctx context.Context, nodes []*uiNodeObject) []uiNodeRefusal {
	var refusals []uiNodeRefusal
	for _, key := range []string{"outbounds", "endpoints"} {
		var batch []int
		for index, node := range nodes {
			if _, isEndpoint := uiNodeKind(ctx, node.text("type")); isEndpoint == (key == "endpoints") {
				batch = append(batch, index)
			}
		}
		label := "initialize " + strings.TrimSuffix(key, "s") + "["
		for len(batch) > 0 {
			list := make([]any, len(batch))
			for position, index := range batch {
				list[position] = nodes[index].retagged("node-" + strconv.Itoa(position))
			}
			content, err := uiNodeEncode(uiNodeOf("log", uiNodeOf("disabled", true), key, list), false)
			if err != nil {
				break
			}
			if err = CheckConfig(content); err == nil {
				break
			}
			// The core stops at the first node it cannot build and names its
			// position. Any other failure is not one of a node.
			rest, labelled := strings.CutPrefix(err.Error(), label)
			number, reason, closed := strings.Cut(rest, "]")
			position, err := strconv.Atoi(number)
			if !labelled || !closed || err != nil || position < 0 || position >= len(batch) {
				break
			}
			reason = uiNodeRedact(nodes[batch[position]], strings.TrimPrefix(reason, ": "))
			refusals = append(refusals, uiNodeRefusal{batch[position], reason})
			batch = batch[position+1:]
		}
	}
	slices.SortFunc(refusals, func(left, right uiNodeRefusal) int { return left.index - right.index })
	return refusals
}

// A reference is a place in a configuration that names an outbound.
type uiNodeReference struct {
	path   string
	tag    string
	holder *uiNodeObject
	key    string
	index  int // position in a member list, -1 for a single name
}

var (
	uiNodeReferenceKeys = []string{"detour", "outbound", "outbounds", "default", "final", "download_detour", "external_ui_download_detour"}
	uiNodeDetourKeys    = []string{"detour"}
)

// uiNodeReferences lists every place that names an outbound. The scan is by
// key rather than by schema so that options added by a later core are still
// seen. An inbound's own detour names another inbound, while the dial options
// nested in it name outbounds, and under dns only a detour is an outbound.
func uiNodeReferences(root *uiNodeObject) []uiNodeReference {
	var references []uiNodeReference
	for _, key := range root.keys {
		switch key {
		case "inbounds":
			for index, item := range root.list(key) {
				inbound, _ := item.(*uiNodeObject)
				if inbound == nil {
					continue
				}
				for _, name := range inbound.keys {
					if name != "detour" {
						uiNodeCollectReferences(inbound.values[name], key+"["+strconv.Itoa(index)+"]."+name, uiNodeDetourKeys, &references)
					}
				}
			}
		case "dns":
			uiNodeCollectReferences(root.values[key], key, uiNodeDetourKeys, &references)
		default:
			uiNodeCollectReferences(root.values[key], key, uiNodeReferenceKeys, &references)
		}
	}
	return references
}

func uiNodeCollectReferences(value any, path string, keys []string, references *[]uiNodeReference) {
	switch value := value.(type) {
	case *uiNodeObject:
		for _, key := range value.keys {
			// HTTP header names and Tor option names are caller data, not
			// sing-box option keys, even when named detour/default/outbounds.
			if key == "headers" || key == "torrc" {
				continue
			}
			item, itemPath := value.values[key], path+"."+key
			if slices.Contains(keys, key) {
				if tag, isText := item.(string); isText {
					*references = append(*references, uiNodeReference{itemPath, tag, value, key, -1})
				}
				if list, isList := item.([]any); isList {
					for index, member := range list {
						if tag, isText := member.(string); isText {
							*references = append(*references, uiNodeReference{itemPath + "[" + strconv.Itoa(index) + "]", tag, value, key, index})
						}
					}
				}
			}
			uiNodeCollectReferences(item, itemPath, keys, references)
		}
	case []any:
		for index, item := range value {
			uiNodeCollectReferences(item, path+"["+strconv.Itoa(index)+"]", keys, references)
		}
	}
}

func uiNodeIsGroup(outbound *uiNodeObject) bool {
	nodeType := outbound.text("type")
	return nodeType == C.TypeSelector || nodeType == C.TypeURLTest
}

// These are the outbound dialers a node actually uses. An XHTTP download may
// have its own detour independently of the upload; a disabled download does
// not participate in the dialing graph.
func uiNodeDialDetours(node *uiNodeObject) []string {
	var detours []string
	if detour := node.text("detour"); detour != "" {
		detours = append(detours, detour)
	}
	if transport := node.object("transport"); transport.text("type") == "xhttp" {
		if download := transport.object("download_settings"); download != nil && download.get("enabled") != false {
			if detour := download.text("detour"); detour != "" && !slices.Contains(detours, detour) {
				detours = append(detours, detour)
			}
		}
	}
	return detours
}

// uiNodeDefined returns the names a configuration defines itself.
func uiNodeDefined(root *uiNodeObject) map[string]bool {
	defined := make(map[string]bool)
	for _, key := range []string{"outbounds", "endpoints"} {
		for _, item := range root.list(key) {
			node, _ := item.(*uiNodeObject)
			if tag := node.text("tag"); tag != "" {
				defined[tag] = true
			}
		}
	}
	return defined
}

// uiNodeFind locates a node the configuration defines.
func uiNodeFind(root *uiNodeObject, tag string) (string, int) {
	for _, key := range []string{"outbounds", "endpoints"} {
		for index, item := range root.list(key) {
			if node, _ := item.(*uiNodeObject); tag != "" && node.text("tag") == tag {
				return key, index
			}
		}
	}
	return "", -1
}

// GetUINode returns a configuration node for editing as {"endpoint","content"}.
func GetUINode(config, tag string) (*StringBox, error) {
	root, err := uiNodeDecodeObject(config)
	if err != nil {
		return nil, err
	}
	key, index := uiNodeFind(root, tag)
	if index < 0 {
		return nil, E.New("node not found: ", tag)
	}
	content, err := uiNodeEncode(root.list(key)[index], true)
	if err != nil {
		return nil, err
	}
	encoded, err := uiNodeEncode(uiNodeOf("endpoint", key == "endpoints", "content", content), false)
	if err != nil {
		return nil, err
	}
	return wrapString(encoded), nil
}

// SetUIConfigNode replaces an outbound or endpoint a configuration defines
// and carries a new name to every place that used the old one.
func SetUIConfigNode(config, tag, node string) (*StringBox, error) {
	local, _, err := uiNodeLocalTags(config)
	if err != nil {
		return nil, err
	}
	root, err := uiNodeDecodeObject(config)
	if err != nil {
		return nil, err
	}
	key, index := uiNodeFind(root, tag)
	if index < 0 {
		return nil, E.New("node not found: ", tag)
	}
	edited, err := uiNodeDecodeObject(node)
	if err != nil {
		return nil, err
	}
	name := edited.text("tag")
	if name == "" {
		return nil, E.New("a node needs a name")
	}
	if name != tag && uiNodeDefined(root)[name] {
		return nil, E.New("a node named ", name, " already exists")
	}
	if err = uiNodeCheckOptions(baseContext(nil), edited, key == "endpoints"); err != nil {
		return nil, E.Cause(err, name)
	}
	root.list(key)[index] = edited
	if name != tag {
		for index, localTag := range local {
			if localTag == tag {
				local[index] = name
			}
		}
		for _, reference := range uiNodeReferences(root) {
			if reference.tag != tag {
				continue
			} else if reference.index < 0 {
				reference.holder.set(reference.key, name)
			} else {
				reference.holder.list(reference.key)[reference.index] = name
			}
		}
	}
	content, err := uiNodeEncodeConfig(root, local, true)
	if err != nil {
		return nil, err
	}
	return wrapString(content), nil
}

// RemoveUIConfigNodes removes outbounds or endpoints a configuration defines.
// They leave the groups listing them; an automatic group can fall back to an
// existing direct outbound. Other references or groups without a member refuse
// the removal.
func RemoveUIConfigNodes(config, tags string, defaultProfile bool) (*StringBox, error) {
	local, _, err := uiNodeLocalTags(config)
	if err != nil {
		return nil, err
	}
	root, err := uiNodeDecodeObject(config)
	if err != nil {
		return nil, err
	}
	names, err := uiNodeTextList(tags)
	if err != nil {
		return nil, err
	}
	removed := make(map[string]bool, len(names))
	for _, name := range names {
		if _, index := uiNodeFind(root, name); index < 0 {
			return nil, E.New("node not found: ", name)
		}
		removed[name] = true
	}
	for _, key := range []string{"outbounds", "endpoints"} {
		if list := root.list(key); len(list) > 0 {
			root.set(key, slices.DeleteFunc(list, func(item any) bool {
				node, _ := item.(*uiNodeObject)
				return removed[node.text("tag")]
			}))
		}
	}
	for _, reference := range uiNodeReferences(root) {
		if !removed[reference.tag] {
			continue
		}
		member := uiNodeIsGroup(reference.holder) && reference.key == "outbounds" && reference.index >= 0
		if !member && !(uiNodeIsGroup(reference.holder) && reference.key == "default") {
			return nil, E.New(reference.tag, " is still used at ", reference.path)
		}
	}
	fallback := ""
	for _, item := range root.list("outbounds") {
		node, _ := item.(*uiNodeObject)
		if node.text("type") == C.TypeDirect && node.text("tag") != "" {
			fallback = node.text("tag")
			break
		}
	}
	for _, item := range root.list("outbounds") {
		group, _ := item.(*uiNodeObject)
		if !uiNodeIsGroup(group) {
			continue
		}
		list := group.list("outbounds")
		members := slices.DeleteFunc(slices.Clone(list), func(member any) bool {
			name, isText := member.(string)
			return isText && removed[name]
		})
		if len(members) == 0 && len(list) > 0 {
			if defaultProfile && group.text("tag") == "auto" && group.text("type") == C.TypeURLTest && fallback != "" {
				members = []any{fallback}
			} else {
				return nil, E.New("group ", group.text("tag"), " would be left without a member")
			}
		}
		if !slices.Equal(members, list) {
			group.set("outbounds", members)
		}
		if removed[group.text("default")] {
			group.remove("default")
		}
	}
	content, err := uiNodeEncodeConfig(root, local, true)
	if err != nil {
		return nil, err
	}
	return wrapString(content), nil
}

// GetUIDefaultConfig returns the account-free default configuration. The
// direct member keeps the automatic group valid until nodes are appended.
func GetUIDefaultConfig() *StringBox { return wrapString(uiNodeDefaultConfig) }

const uiNodeDefaultConfig = `{
  "log": {
    "level": "panic",
    "timestamp": true
  },
  "certificate": {
    "store": "mozilla"
  },
  "dns": {
    "servers": [
      {
        "type": "https",
        "tag": "dnspod",
        "server": "120.53.53.53",
        "server_port": 443,
        "tls": {
          "enabled": true,
          "server_name": "doh.pub"
        },
        "path": "/dns-query"
      },
      {
        "type": "https",
        "tag": "cloudflare",
        "detour": "proxy",
        "server": "1.1.1.1",
        "server_port": 443,
        "tls": {
          "enabled": true,
          "server_name": "cloudflare-dns.com"
        },
        "path": "/dns-query"
      }
    ],
    "rules": [
      {
        "rule_set": "geosite-category-ads-all",
        "action": "reject",
        "method": "drop"
      },
      {
        "rule_set": "geosite-cn",
        "server": "dnspod"
      },
      {
        "rule_set": "geosite-geolocation-!cn",
        "server": "cloudflare"
      },
      {
        "clash_mode": "Direct",
        "server": "dnspod"
      },
      {
        "clash_mode": "Global",
        "server": "cloudflare"
      }
    ],
    "final": "cloudflare",
    "reverse_mapping": true,
    "strategy": "prefer_ipv4"
  },
  "inbounds": [
    {
      "type": "tun",
      "tag": "tun-in",
      "mtu": 9000,
      "address": [
        "172.19.0.1/30",
        "fdfe:dcba:9876::1/126"
      ],
      "auto_route": true,
      "strict_route": true,
      "exclude_package": [
        "com.google.android.captiveportallogin"
      ],
      "platform": {
        "http_proxy": {
          "enabled": true,
          "server": "127.0.0.1",
          "server_port": 6080
        }
      }
    },
    {
      "type": "mixed",
      "tag": "mixed-in",
      "listen": "127.0.0.1",
      "listen_port": 6080
    }
  ],
  "outbounds": [
    {
      "type": "direct",
      "tag": "direct"
    },
    {
      "type": "selector",
      "tag": "proxy",
      "outbounds": [
        "auto"
      ],
      "interrupt_exist_connections": true
    },
    {
      "type": "urltest",
      "tag": "auto",
      "outbounds": [
        "direct"
      ],
      "interrupt_exist_connections": true
    }
  ],
  "route": {
    "rules": [
      {
        "inbound": [
          "mixed-in",
          "tun-in"
        ],
        "action": "sniff"
      },
      {
        "type": "logical",
        "mode": "or",
        "rules": [
          {
            "port": 53
          },
          {
            "protocol": "dns"
          }
        ],
        "action": "hijack-dns"
      },
      {
        "type": "logical",
        "mode": "or",
        "rules": [
          {
            "port": 853
          },
          {
            "network": "udp",
            "port": 443
          },
          {
            "protocol": "stun"
          }
        ],
        "action": "reject"
      },
      {
        "rule_set": "geosite-category-ads-all",
        "action": "reject",
        "method": "drop"
      },
      {
        "ip_is_private": true,
        "outbound": "direct"
      },
      {
        "clash_mode": "Direct",
        "outbound": "direct"
      },
      {
        "clash_mode": "Global",
        "outbound": "proxy"
      },
      {
        "rule_set": [
          "geosite-cn",
          "geoip-cn"
        ],
        "outbound": "direct"
      },
      {
        "rule_set": "geosite-geolocation-!cn",
        "outbound": "proxy"
      }
    ],
    "rule_set": [
      {
        "type": "remote",
        "tag": "geosite-category-ads-all",
        "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-category-ads-all.srs",
        "http_client": {
          "detour": "proxy"
        },
        "update_interval": "24h0m0s"
      },
      {
        "type": "remote",
        "tag": "geosite-cn",
        "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-cn.srs",
        "http_client": {
          "detour": "proxy"
        },
        "update_interval": "24h0m0s"
      },
      {
        "type": "remote",
        "tag": "geosite-geolocation-!cn",
        "url": "https://raw.githubusercontent.com/SagerNet/sing-geosite/rule-set/geosite-geolocation-!cn.srs",
        "http_client": {
          "detour": "proxy"
        },
        "update_interval": "24h0m0s"
      },
      {
        "type": "remote",
        "tag": "geoip-cn",
        "url": "https://raw.githubusercontent.com/SagerNet/sing-geoip/rule-set/geoip-cn.srs",
        "http_client": {
          "detour": "proxy"
        },
        "update_interval": "24h0m0s"
      }
    ],
    "final": "proxy",
    "auto_detect_interface": true,
    "default_domain_resolver": "dnspod"
  },
  "experimental": {
    "cache_file": {
      "enabled": true
    }
  }
}
`
