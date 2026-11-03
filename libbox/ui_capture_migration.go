package libbox

import (
	"context"
	"errors"
	"strings"

	"github.com/sagernet/sing/common/json"
)

// Keep this exported name for existing Android profile/backup callers. Drop the
// retired passive-capture extension without executing it, and relocate the
// previous nested MITM/scripts extensions without widening their enabled scope.
// Raw messages preserve unrelated numeric values and fields.
func RemoveUIRetiredCaptureOptions(content string) (string, error) {
	if !strings.Contains(content, "http_debug") && !strings.Contains(content, "http_rewrite") {
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
	_, retired := experimental["http_debug"]
	changed, err := migrateUIProcessing(experimental)
	if err != nil {
		return "", err
	}
	if !retired && !changed {
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

func migrateUIProcessing(experimental map[string]json.RawMessage) (bool, error) {
	raw, exists := experimental["http_rewrite"]
	if !exists {
		return false, nil
	}
	var rewrite map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rewrite); err != nil {
		return false, err
	}
	_, hasMITM := rewrite["mitm"]
	_, hasScripts := rewrite["scripts"]
	if !hasMITM && !hasScripts {
		return false, nil
	}
	var enabled bool
	if raw, ok := rewrite["enabled"]; ok {
		if err := json.Unmarshal(raw, &enabled); err != nil {
			return false, err
		}
	}
	copyScope := func(target map[string]json.RawMessage) {
		for _, key := range []string{"ports", "packages"} {
			if value, exists := rewrite[key]; exists {
				target[key] = value
			}
		}
	}
	if hasMITM {
		if _, exists := experimental["mitm"]; exists {
			return false, errors.New("both experimental.mitm and http_rewrite.mitm exist; merge MITM settings before saving")
		}
		var mitm map[string]json.RawMessage
		if err := json.Unmarshal(rewrite["mitm"], &mitm); err != nil {
			return false, err
		}
		if mitm == nil {
			mitm = make(map[string]json.RawMessage)
		}
		var exclusions []string
		if raw, ok := mitm["excluded_hosts"]; ok {
			if err := json.Unmarshal(raw, &exclusions); err != nil {
				return false, err
			}
		}
		delete(mitm, "excluded_hosts")
		// Subtracting a hostname from a wildcard cannot be represented as an
		// allowlist. Disable interception until the user reviews the new list.
		if !enabled || len(exclusions) > 0 {
			mitm["enabled"] = json.RawMessage("false")
		}
		copyScope(mitm)
		var ports []uint16
		if raw, exists := mitm["ports"]; exists {
			if err := json.Unmarshal(raw, &ports); err != nil {
				return false, err
			}
		}
		if len(ports) == 0 {
			mitm["ports"] = json.RawMessage("[80,443,8080,8443]")
		}
		var err error
		experimental["mitm"], err = json.Marshal(mitm)
		if err != nil {
			return false, err
		}
		delete(rewrite, "mitm")
	}
	if hasScripts {
		if _, exists := experimental["scripts"]; exists {
			return false, errors.New("both experimental.scripts and http_rewrite.scripts exist; merge scripts before saving")
		}
		scripts := map[string]json.RawMessage{"http": rewrite["scripts"]}
		scripts["enabled"], _ = json.Marshal(enabled)
		copyScope(scripts)
		if value, exists := rewrite["max_body_bytes"]; exists {
			scripts["max_body_bytes"] = value
		}
		// Copy only referenced script resources. Large static response bodies
		// remain in http_rewrite and do not become executable script resources.
		var rules []map[string]json.RawMessage
		if err := json.Unmarshal(rewrite["scripts"], &rules); err != nil {
			return false, err
		}
		var resources map[string]json.RawMessage
		if raw, exists := rewrite["resources"]; exists {
			if err := json.Unmarshal(raw, &resources); err != nil {
				return false, err
			}
		}
		selected := make(map[string]json.RawMessage)
		for _, rule := range rules {
			var name string
			if raw, ok := rule["resource"]; ok {
				if err := json.Unmarshal(raw, &name); err != nil {
					return false, err
				}
				if value, exists := resources[name]; exists {
					selected[name] = value
				}
			}
		}
		if len(selected) > 0 {
			scripts["resources"], _ = json.Marshal(selected)
		}
		var err error
		experimental["scripts"], err = json.Marshal(scripts)
		if err != nil {
			return false, err
		}
		delete(rewrite, "scripts")
	}
	var err error
	experimental["http_rewrite"], err = json.Marshal(rewrite)
	return true, err
}
