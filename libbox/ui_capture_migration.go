package libbox

import (
	"context"
	"strings"

	"github.com/sagernet/sing/common/json"
)

// RemoveUIRetiredCaptureOptions is a narrow upgrade sanitizer. It drops the
// retired extension without interpreting it; no capture option or schema exists.
// Raw messages preserve unrelated numeric values and fields.
func RemoveUIRetiredCaptureOptions(content string) (string, error) {
	if !strings.Contains(content, "http_debug") {
		return content, nil
	}
	root, err := json.UnmarshalExtendedContext[map[string]json.RawMessage](context.Background(), []byte(content))
	if err != nil {
		return "", err
	}
	raw, exists := root["experimental"]
	if !exists {
		return content, nil
	}
	var experimental map[string]json.RawMessage
	if err = json.Unmarshal(raw, &experimental); err != nil {
		return "", err
	}
	if _, exists = experimental["http_debug"]; !exists {
		return content, nil
	}
	delete(experimental, "http_debug")
	root["experimental"], err = json.Marshal(experimental)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(root)
	return string(data), err
}
