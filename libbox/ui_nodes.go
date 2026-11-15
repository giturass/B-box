package libbox

// B-box shared node library and its expansion into sing-box configurations.
// A profile stays sing-box JSON: an empty string in a selector or urltest
// member list stands for every shared node, and a node named explicitly is
// supplied from the library when the profile does not define it. Documents are
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
	"psk", "auth", "auth_str", "obfs", "username", "user",
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
// build. One such node would keep every profile that uses the shared nodes
// from starting. The nodes must have passed uiNodeCheck.
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

type uiNodeEntry struct {
	source string
	node   *uiNodeObject
}

// The library is one document: subscription sources with metadata this code
// does not interpret, and the nodes in display order, each owned by a source
// or, with an empty source, entered by hand. Names are unique across it.
type uiNodeLibrary struct {
	sources []*uiNodeObject
	nodes   []uiNodeEntry
}

func uiNodeLibraryDecode(content string) (*uiNodeLibrary, error) {
	library := &uiNodeLibrary{}
	if strings.TrimSpace(content) == "" {
		return library, nil
	}
	root, err := uiNodeDecodeObject(content)
	if err != nil {
		return nil, E.Cause(err, "decode node library")
	}
	if version, _ := root.get("version").(uiNodeNumber); version != "1" {
		return nil, E.New("unsupported node library version")
	}
	sources := make(map[string]bool)
	for _, item := range root.list("sources") {
		source, _ := item.(*uiNodeObject)
		id := source.text("id")
		if id == "" || sources[id] {
			return nil, E.New("invalid node library source")
		}
		sources[id] = true
		library.sources = append(library.sources, source)
	}
	tags := make(map[string]bool)
	for _, item := range root.list("nodes") {
		entry, _ := item.(*uiNodeObject)
		node, source := entry.object("node"), entry.text("source")
		tag := node.text("tag")
		if tag == "" || tags[tag] || node.text("type") == "" || source != "" && !sources[source] {
			return nil, E.New("invalid node library entry")
		}
		tags[tag] = true
		library.nodes = append(library.nodes, uiNodeEntry{source, node})
	}
	return library, nil
}

func (l *uiNodeLibrary) encode() (string, error) {
	sources := make([]any, len(l.sources))
	for index, source := range l.sources {
		sources[index] = source
	}
	nodes := make([]any, len(l.nodes))
	for index, entry := range l.nodes {
		nodes[index] = uiNodeOf("source", entry.source, "node", entry.node)
	}
	return uiNodeEncode(uiNodeOf("version", uiNodeInt(1), "sources", sources, "nodes", nodes), false)
}

func (l *uiNodeLibrary) result() (*StringBox, error) {
	content, err := l.encode()
	if err != nil {
		return nil, err
	}
	return wrapString(content), nil
}

func (l *uiNodeLibrary) index(tag string) int {
	return slices.IndexFunc(l.nodes, func(entry uiNodeEntry) bool { return entry.node.text("tag") == tag })
}

func (l *uiNodeLibrary) sourceIndex(id string) int {
	return slices.IndexFunc(l.sources, func(source *uiNodeObject) bool { return source.text("id") == id })
}

// place inserts nodes for a source at position. A node the source already
// holds under its name, or under a numbered variant of that name, is not
// added again; any other collision takes the next free number. A detour
// inside the batch follows the name its target ended up with, which keeps a
// repeated import from growing the library.
func (l *uiNodeLibrary) place(sourceID string, position int, incoming []*uiNodeObject) (int, int) {
	existing := make(map[string]uiNodeEntry, len(l.nodes))
	for _, entry := range l.nodes {
		existing[entry.node.text("tag")] = entry
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
	var resolve func(index int)
	resolve = func(index int) {
		if state[index] != 0 {
			return
		}
		state[index] = visiting
		node := incoming[index]
		if target, exists := first[node.text("detour")]; exists && target != index {
			resolve(target)
			if state[target] == done {
				node.set("detour", final[target])
			}
		}
		base := node.text("tag")
		candidate := base
		for number := 2; ; number++ {
			if other, exists := placed[candidate]; exists {
				if uiNodeSame(other, node) {
					break
				}
			} else if entry, exists := existing[candidate]; exists {
				if entry.source == sourceID && uiNodeSame(entry.node, node) {
					break
				}
			} else {
				node.set("tag", candidate)
				placed[candidate] = node
				keep[index] = true
				break
			}
			candidate = base + " " + strconv.Itoa(number)
		}
		final[index] = candidate
		state[index] = done
	}
	for index := range incoming {
		resolve(index)
	}
	entries := make([]uiNodeEntry, 0, len(incoming))
	for index, node := range incoming {
		if keep[index] {
			entries = append(entries, uiNodeEntry{sourceID, node})
		}
	}
	l.nodes = slices.Insert(l.nodes, position, entries...)
	return len(entries), len(incoming) - len(entries)
}

// dependent names a remaining node whose detour is one of the removed names.
func (l *uiNodeLibrary) dependent(removed map[string]bool) error {
	for _, entry := range l.nodes {
		if detour := entry.node.text("detour"); detour != "" && removed[detour] {
			return E.New(detour, " is the detour of ", entry.node.text("tag"))
		}
	}
	return nil
}

// UINodeUpdate is a library after nodes were added to it.
type UINodeUpdate struct {
	Library string
	Added   int32
	Skipped int32
}

func uiNodeAdd(libraryContent, sourceID, nodesContent string, replace bool) (*UINodeUpdate, error) {
	library, err := uiNodeLibraryDecode(libraryContent)
	if err != nil {
		return nil, err
	}
	if (replace || sourceID != "") && library.sourceIndex(sourceID) < 0 {
		return nil, E.New("unknown node source: ", sourceID)
	}
	incoming, err := uiNodeList(nodesContent)
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
	position := len(library.nodes)
	if replace {
		// An update that finds nothing is a failed download or a portal page,
		// not a subscription that lost every node.
		if len(incoming) == 0 {
			return nil, E.New("the subscription contains no usable node")
		}
		kept := make([]uiNodeEntry, 0, len(library.nodes))
		position = -1
		for _, entry := range library.nodes {
			if entry.source != sourceID {
				kept = append(kept, entry)
			} else if position < 0 {
				position = len(kept)
			}
		}
		if position < 0 {
			position = len(kept)
		}
		library.nodes = kept
	} else {
		for index, entry := range library.nodes {
			if entry.source == sourceID {
				position = index + 1
			}
		}
	}
	added, skipped := library.place(sourceID, position, incoming)
	content, err := library.encode()
	if err != nil {
		return nil, err
	}
	return &UINodeUpdate{Library: content, Added: int32(added), Skipped: int32(skipped)}, nil
}

// AddUINodes adds parsed nodes to a subscription source, or with an empty
// source to the nodes entered by hand.
func AddUINodes(library, sourceID, nodes string) (*UINodeUpdate, error) {
	return uiNodeAdd(library, sourceID, nodes, false)
}

// ReplaceUINodeSource gives a subscription source its updated nodes in the
// place the previous ones had.
func ReplaceUINodeSource(library, sourceID, nodes string) (*UINodeUpdate, error) {
	return uiNodeAdd(library, sourceID, nodes, true)
}

// SetUINode replaces the node named tag, or with an empty tag adds a node
// entered by hand. A new name is carried into the detours of other shared
// nodes; profiles naming the old one are the caller's to check.
func SetUINode(library, tag, node string) (*StringBox, error) {
	lib, err := uiNodeLibraryDecode(library)
	if err != nil {
		return nil, err
	}
	edited, err := uiNodeDecodeObject(node)
	if err != nil {
		return nil, err
	}
	ctx := baseContext(nil)
	if _, err = uiNodeCheck(ctx, edited); err != nil {
		return nil, err
	}
	if refusals := uiNodeConstruct(ctx, []*uiNodeObject{edited}); len(refusals) > 0 {
		return nil, E.New(edited.text("tag"), ": ", refusals[0].reason)
	}
	name := edited.text("tag")
	if name != tag && lib.index(name) >= 0 {
		return nil, E.New("a node named ", name, " already exists")
	}
	if tag == "" {
		position := len(lib.nodes)
		for index, entry := range lib.nodes {
			if entry.source == "" {
				position = index + 1
			}
		}
		lib.nodes = slices.Insert(lib.nodes, position, uiNodeEntry{node: edited})
		return lib.result()
	}
	index := lib.index(tag)
	if index < 0 {
		return nil, E.New("node not found: ", tag)
	}
	lib.nodes[index].node = edited
	if name != tag {
		for _, entry := range lib.nodes {
			if entry.node.text("detour") == tag {
				entry.node.set("detour", name)
			}
		}
	}
	return lib.result()
}

// RemoveUINodes removes shared nodes. Profiles are never edited here, so the
// caller first asks FindUINodeReferences which of them name the nodes.
func RemoveUINodes(library, tags string) (*StringBox, error) {
	lib, err := uiNodeLibraryDecode(library)
	if err != nil {
		return nil, err
	}
	names, err := uiNodeTextList(tags)
	if err != nil {
		return nil, err
	}
	removed := make(map[string]bool, len(names))
	for _, name := range names {
		removed[name] = true
	}
	lib.nodes = slices.DeleteFunc(lib.nodes, func(entry uiNodeEntry) bool { return removed[entry.node.text("tag")] })
	if err = lib.dependent(removed); err != nil {
		return nil, err
	}
	return lib.result()
}

// SetUINodeSource adds a subscription source or replaces its metadata.
func SetUINodeSource(library, source string) (*StringBox, error) {
	lib, err := uiNodeLibraryDecode(library)
	if err != nil {
		return nil, err
	}
	edited, err := uiNodeDecodeObject(source)
	if err != nil {
		return nil, err
	}
	id := edited.text("id")
	if id == "" {
		return nil, E.New("a node source needs an id")
	}
	if index := lib.sourceIndex(id); index >= 0 {
		lib.sources[index] = edited
	} else {
		lib.sources = append(lib.sources, edited)
	}
	return lib.result()
}

// RemoveUINodeSource removes a subscription source with its nodes.
func RemoveUINodeSource(library, sourceID string) (*StringBox, error) {
	lib, err := uiNodeLibraryDecode(library)
	if err != nil {
		return nil, err
	}
	index := lib.sourceIndex(sourceID)
	if index < 0 {
		return lib.result()
	}
	removed := make(map[string]bool)
	for _, entry := range lib.nodes {
		if entry.source == sourceID {
			removed[entry.node.text("tag")] = true
		}
	}
	lib.sources = slices.Delete(lib.sources, index, index+1)
	lib.nodes = slices.DeleteFunc(lib.nodes, func(entry uiNodeEntry) bool { return entry.source == sourceID })
	if err = lib.dependent(removed); err != nil {
		return nil, err
	}
	return lib.result()
}

// MergeUINodes returns a library with the sources and nodes of another one,
// as a restored backup brings them. A source or node of the other library
// replaces the one held under its id or name and everything else stays in
// its place, so an empty library becomes the other one. The nodes that come
// in pass the checks of an import.
func MergeUINodes(library, incoming string) (*StringBox, error) {
	lib, err := uiNodeLibraryDecode(library)
	if err != nil {
		return nil, err
	}
	other, err := uiNodeLibraryDecode(incoming)
	if err != nil {
		return nil, err
	}
	ctx := baseContext(nil)
	nodes := make([]*uiNodeObject, len(other.nodes))
	position := make(map[string]int, len(other.nodes))
	for index, entry := range other.nodes {
		if _, err = uiNodeCheck(ctx, entry.node); err != nil {
			return nil, err
		}
		nodes[index] = entry.node
		position[entry.node.text("tag")] = index
	}
	if refusals := uiNodeConstruct(ctx, nodes); len(refusals) > 0 {
		return nil, E.New(nodes[refusals[0].index].text("tag"), ": ", refusals[0].reason)
	}
	for _, source := range other.sources {
		if index := lib.sourceIndex(source.text("id")); index >= 0 {
			lib.sources[index] = source
		} else {
			lib.sources = append(lib.sources, source)
		}
	}
	replaced := make([]bool, len(other.nodes))
	for index, entry := range lib.nodes {
		if at, exists := position[entry.node.text("tag")]; exists {
			lib.nodes[index] = other.nodes[at]
			replaced[at] = true
		}
	}
	for index, entry := range other.nodes {
		if !replaced[index] {
			lib.nodes = append(lib.nodes, entry)
		}
	}
	return lib.result()
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

// uiNodeDetourResolves follows a shared node's detours to an outbound the
// configuration can dial. A chain that ends at a missing name would stop the
// whole configuration from starting, and one that ends at a group of the
// configuration would loop once the node became a member of that group.
func uiNodeDetourResolves(tag string, shared map[string]*uiNodeObject, defined, groups map[string]bool) bool {
	for range len(shared) + 1 {
		detour := shared[tag].text("detour")
		if detour == "" {
			return true
		}
		if defined[detour] {
			return !groups[detour]
		}
		if shared[detour] == nil {
			return false
		}
		tag = detour
	}
	return false
}

func uiNodeExpand(ctx context.Context, config, libraryContent string) (string, error) {
	library, err := uiNodeLibraryDecode(libraryContent)
	if err != nil {
		return "", err
	}
	if len(library.nodes) == 0 && !strings.Contains(config, `""`) {
		return config, nil
	}
	// A document that cannot be read here is left for the core to reject in
	// its own words.
	root, err := uiNodeDecodeObject(config)
	if err != nil {
		return config, nil
	}
	outbounds, isList := root.optionalList("outbounds")
	if !isList {
		return config, nil
	}
	endpoints, isList := root.optionalList("endpoints")
	if !isList {
		return config, nil
	}
	defined := uiNodeDefined(root)
	var groups []*uiNodeObject
	groupTags := make(map[string]bool)
	fallback := ""
	for _, item := range outbounds {
		outbound, _ := item.(*uiNodeObject)
		if fallback == "" && outbound.text("type") == C.TypeDirect {
			fallback = outbound.text("tag")
		}
		if !uiNodeIsGroup(outbound) {
			continue
		}
		groupTags[outbound.text("tag")] = true
		if slices.Contains(outbound.list("outbounds"), any("")) {
			groups = append(groups, outbound)
		}
	}
	// A name the configuration defines itself hides the shared node.
	shared := make(map[string]*uiNodeObject, len(library.nodes))
	isEndpoint := make(map[string]bool)
	for _, entry := range library.nodes {
		tag := entry.node.text("tag")
		if isNode, endpoint := uiNodeKind(ctx, entry.node.text("type")); isNode && !defined[tag] {
			shared[tag], isEndpoint[tag] = entry.node, endpoint
		}
	}
	included := make(map[string]bool)
	var include func(tag string)
	include = func(tag string) {
		if node := shared[tag]; node != nil && !included[tag] {
			included[tag] = true
			include(node.text("detour"))
		}
	}
	var members []string
	if len(groups) > 0 {
		for _, entry := range library.nodes {
			tag := entry.node.text("tag")
			if shared[tag] != nil && uiNodeDetourResolves(tag, shared, defined, groupTags) {
				include(tag)
				members = append(members, tag)
			}
		}
	}
	for _, reference := range uiNodeReferences(root) {
		include(reference.tag)
	}
	if len(groups) == 0 && len(included) == 0 {
		return config, nil
	}
	for _, group := range groups {
		list := group.list("outbounds")
		listed := make(map[string]bool, len(list))
		for _, item := range list {
			if tag, isText := item.(string); isText {
				listed[tag] = true
			}
		}
		expanded := make([]any, 0, len(list)+len(members))
		for _, item := range list {
			if tag, isText := item.(string); !isText || tag != "" {
				expanded = append(expanded, item)
			} else if listed[""] {
				listed[""] = false
				for _, member := range members {
					if !listed[member] {
						expanded = append(expanded, member)
					}
				}
			}
		}
		// The core refuses an empty group; without shared nodes it connects
		// directly.
		if len(expanded) == 0 {
			if fallback == "" {
				fallback = C.TypeDirect
				for number := 2; defined[fallback] || shared[fallback] != nil; number++ {
					fallback = C.TypeDirect + " " + strconv.Itoa(number)
				}
				outbounds = append(outbounds, uiNodeOf("type", C.TypeDirect, "tag", fallback))
			}
			expanded = append(expanded, fallback)
		}
		group.set("outbounds", expanded)
	}
	for _, entry := range library.nodes {
		if tag := entry.node.text("tag"); !included[tag] {
			continue
		} else if isEndpoint[tag] {
			endpoints = append(endpoints, entry.node)
		} else {
			outbounds = append(outbounds, entry.node)
		}
	}
	if len(outbounds) > 0 {
		root.set("outbounds", outbounds)
	}
	if len(endpoints) > 0 {
		root.set("endpoints", endpoints)
	}
	return uiNodeEncode(root, true)
}

// ExpandUINodes returns the configuration the core runs: shared nodes take
// the place of each group's empty member, and nodes the configuration names
// without defining are added with the nodes their detours need.
func ExpandUINodes(config, library string) (*StringBox, error) {
	content, err := uiNodeExpand(baseContext(nil), config, library)
	if err != nil {
		return nil, err
	}
	return wrapString(content), nil
}

// FindUINodeReferences lists where a configuration names any of the given
// nodes without defining them, as [{"tag","path"}].
func FindUINodeReferences(config, tags string) (*StringBox, error) {
	names, err := uiNodeTextList(tags)
	if err != nil {
		return nil, err
	}
	found := make([]any, 0)
	if root, err := uiNodeDecodeObject(config); err == nil {
		defined := uiNodeDefined(root)
		for _, reference := range uiNodeReferences(root) {
			if slices.Contains(names, reference.tag) && !defined[reference.tag] {
				found = append(found, uiNodeOf("tag", reference.tag, "path", reference.path))
			}
		}
	}
	content, err := uiNodeEncode(found, false)
	if err != nil {
		return nil, err
	}
	return wrapString(content), nil
}

// GetUINode returns a node for editing as {"origin","source","endpoint",
// "content"}. A node the configuration defines hides a shared one.
func GetUINode(config, library, tag string) (*StringBox, error) {
	var node *uiNodeObject
	result := uiNodeOf()
	if root, err := uiNodeDecodeObject(config); err == nil {
		if key, index := uiNodeFind(root, tag); index >= 0 {
			node, _ = root.list(key)[index].(*uiNodeObject)
			result.set("origin", "profile")
			result.set("source", "")
			result.set("endpoint", key == "endpoints")
		}
	}
	if node == nil {
		lib, err := uiNodeLibraryDecode(library)
		if err != nil {
			return nil, err
		}
		index := lib.index(tag)
		if index < 0 {
			return nil, E.New("node not found: ", tag)
		}
		node = lib.nodes[index].node
		_, isEndpoint := uiNodeKind(baseContext(nil), node.text("type"))
		result.set("origin", "library")
		result.set("source", lib.nodes[index].source)
		result.set("endpoint", isEndpoint)
	}
	content, err := uiNodeEncode(node, true)
	if err != nil {
		return nil, err
	}
	result.set("content", content)
	encoded, err := uiNodeEncode(result, false)
	if err != nil {
		return nil, err
	}
	return wrapString(encoded), nil
}

// SetUIConfigNode replaces an outbound or endpoint a configuration defines
// and carries a new name to every place that used the old one.
func SetUIConfigNode(config, tag, node string) (*StringBox, error) {
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
	content, err := uiNodeEncode(root, true)
	if err != nil {
		return nil, err
	}
	return wrapString(content), nil
}

// RemoveUIConfigNodes removes outbounds or endpoints a configuration defines.
// They leave the groups listing them; any other use, or a group left without
// a member, refuses the removal.
func RemoveUIConfigNodes(config, tags string) (*StringBox, error) {
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
			return nil, E.New("group ", group.text("tag"), " would be left without a member")
		}
		if len(members) < len(list) {
			group.set("outbounds", members)
		}
		if removed[group.text("default")] {
			group.remove("default")
		}
	}
	content, err := uiNodeEncode(root, true)
	if err != nil {
		return nil, err
	}
	return wrapString(content), nil
}

// GetUIDefaultConfig returns the configuration offered when none exists. It
// holds no account: the proxy group lists the automatic group and every
// shared node, and the automatic group tests every shared node.
func GetUIDefaultConfig() *StringBox { return wrapString(uiNodeDefaultConfig) }

const uiNodeDefaultConfig = `{
  "log": {
    "level": "panic",
    "timestamp": true
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
        "auto",
        ""
      ],
      "interrupt_exist_connections": true
    },
    {
      "type": "urltest",
      "tag": "auto",
      "outbounds": [
        ""
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
