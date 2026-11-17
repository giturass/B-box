package libbox

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/sagernet/bbolt"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	N "github.com/sagernet/sing/common/network"
	"github.com/sagernet/sing/service/filemanager"
)

func uiGroupTestConfig(t *testing.T, path, cacheID, fallback string) string {
	t.Helper()
	content, err := json.Marshal(map[string]any{
		"log": map[string]any{"disabled": true},
		"outbounds": []any{
			map[string]any{"type": "selector", "tag": "proxy", "outbounds": []string{"direct-a", "direct-b"}, "default": fallback},
			map[string]any{"type": "direct", "tag": "direct-a"},
			map[string]any{"type": "direct", "tag": "direct-b"},
		},
		"route":        map[string]any{"final": "proxy"},
		"experimental": map[string]any{"cache_file": map[string]any{"enabled": false, "path": path, "cache_id": cacheID}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func uiGroupTestRead(t *testing.T, config string) []*OutboundGroup {
	t.Helper()
	iterator, err := GetUIConfigGroups(config)
	if err != nil {
		t.Fatal(err)
	}
	var groups []*OutboundGroup
	for iterator.HasNext() {
		groups = append(groups, iterator.Next())
	}
	return groups
}

func uiGroupTestDB(t *testing.T, path string, update func(*bbolt.Tx) error) {
	t.Helper()
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	err = db.Update(update)
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
}

func uiGroupTestSnapshot(t *testing.T, path string) map[string]string {
	t.Helper()
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[string]string)
	err = db.View(func(tx *bbolt.Tx) error {
		var walk func(string, *bbolt.Bucket) error
		walk = func(prefix string, bucket *bbolt.Bucket) error {
			return bucket.ForEach(func(key, value []byte) error {
				name := prefix + "/" + string(key)
				if value == nil {
					return walk(name, bucket.Bucket(key))
				}
				result[name] = string(value)
				return nil
			})
		}
		return tx.ForEach(func(name []byte, bucket *bbolt.Bucket) error { return walk(string(name), bucket) })
	})
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	return result
}

func TestUIConfigGroupsUsesNativeMembersAndOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	encodedPath, _ := json.Marshal(path)
	config := `{
		"endpoints":[{"type":"wireguard","tag":"wg","address":["10.0.0.2/32"],"private_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}],
		"outbounds":[
			{"type":"direct","tag":"direct"},
			{"type":"selector","tag":"proxy","outbounds":["auto","direct","wg","node"],"default":"node"},
			{"type":"socks","tag":"node","server":"127.0.0.1","server_port":1080},
			{"type":"urltest","tag":"auto","outbounds":["wg","node","direct"]},
			{"type":"socks","tag":"ungrouped","server":"127.0.0.1","server_port":1081}
		],
		"experimental":{"cache_file":{"path":` + string(encodedPath) + `}}
	}`
	groups := uiGroupTestRead(t, config)
	if len(groups) != 2 || groups[0].Tag != "proxy" || groups[0].Type != "selector" || !groups[0].Selectable || groups[0].Selected != "node" ||
		groups[1].Tag != "auto" || groups[1].Type != "urltest" || groups[1].Selectable || groups[1].Selected != "" {
		t.Fatalf("configured native groups: %+v", groups)
	}
	for index, expected := range [][]*OutboundGroupItem{
		{{Tag: "auto", Type: "urltest"}, {Tag: "direct", Type: "direct"}, {Tag: "wg", Type: "wireguard"}, {Tag: "node", Type: "socks"}},
		{{Tag: "wg", Type: "wireguard"}, {Tag: "node", Type: "socks"}, {Tag: "direct", Type: "direct"}},
	} {
		var items []*OutboundGroupItem
		iterator := groups[index].GetItems()
		for iterator.HasNext() {
			items = append(items, iterator.Next())
		}
		if !reflect.DeepEqual(items, expected) {
			t.Fatalf("group %d members or invented latency: %+v", index, items)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("reading groups created a cache database", err)
	}
	if groups := uiGroupTestRead(t, `{"outbounds":[{"type":"direct","tag":"direct"}]}`); len(groups) != 0 {
		t.Fatal("ungrouped outbounds became a synthetic group")
	}
	if groups, err := GetUIConfigGroups(`{"outbounds":[{"type":"unsupported-protocol","tag":"fake"}]}`); err == nil || groups != nil {
		t.Fatal("unknown native outbound was accepted")
	}
}

func TestUIConfigGroupsCachedDefaultAndFirstSelection(t *testing.T) {
	for _, test := range []struct {
		name, cached, fallback, expected string
	}{
		{"first member", "", "", "direct-a"},
		{"configured default", "", "direct-b", "direct-b"},
		{"cached overrides default", "direct-a", "direct-b", "direct-a"},
		{"stale cache falls back to default", "removed", "direct-b", "direct-b"},
		{"stale cache falls back to first", "removed", "", "direct-a"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.db")
			uiGroupTestDB(t, path, func(tx *bbolt.Tx) error {
				selected, err := tx.CreateBucket([]byte("selected"))
				if err != nil {
					return err
				}
				if err = selected.Put([]byte("proxy"), []byte(test.cached)); err != nil {
					return err
				}
				expand, err := tx.CreateBucket([]byte("group_expand"))
				if err != nil {
					return err
				}
				return expand.Put([]byte("proxy"), []byte{1})
			})
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			groups := uiGroupTestRead(t, uiGroupTestConfig(t, path, "", test.fallback))
			if len(groups) != 1 || groups[0].Selected != test.expected || !groups[0].IsExpand {
				t.Fatalf("selection: %+v, expected %s", groups, test.expected)
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("reading groups changed existing cache bytes", err)
			}
		})
	}
}

func TestUIConfigGroupsUsesEffectiveUnnamedTags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	encodedPath, _ := json.Marshal(path)
	config := `{
		"endpoints":[{"type":"wireguard","address":["10.0.0.2/32"],"private_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}],
		"outbounds":[{"type":"direct","tag":"direct"},{"type":"selector","outbounds":["0"]}],
		"experimental":{"cache_file":{"path":` + string(encodedPath) + `}}
	}`
	groups := uiGroupTestRead(t, config)
	if len(groups) != 1 || groups[0].Tag != "1" || groups[0].Selected != "0" || len(groups[0].itemList) != 1 ||
		groups[0].itemList[0].Tag != "0" || groups[0].itemList[0].Type != "wireguard" {
		t.Fatalf("effective unnamed core group or endpoint tags: %+v", groups)
	}

	path = filepath.Join(t.TempDir(), "unnamed.db")
	encodedPath, _ = json.Marshal(path)
	config = `{
		"outbounds":[{"type":"selector","outbounds":["1","2"]},{"type":"direct"},{"type":"direct"}],
		"experimental":{"cache_file":{"path":` + string(encodedPath) + `}}
	}`
	if err := SelectUIConfigGroup(config, "0", "2"); err != nil {
		t.Fatal("selection did not accept the core's effective unnamed tags", err)
	}
	if actual := uiGroupTestSnapshot(t, path)["selected/0"]; actual != "2" {
		t.Fatal("selection was not stored using the effective core group tag", actual)
	}
	if actual := uiGroupTestRead(t, config)[0].Selected; actual != "2" {
		t.Fatal("unnamed group did not read its cached selection", actual)
	}
}

func TestUIConfigGroupsRejectsDuplicateOutboundEndpointTags(t *testing.T) {
	for _, tag := range []string{"", ",\"tag\":\"shared\""} {
		path := filepath.Join(t.TempDir(), "cache.db")
		encodedPath, _ := json.Marshal(path)
		member := "shared"
		if tag == "" {
			member = "0"
		}
		config := `{
			"endpoints":[{"type":"wireguard"` + tag + `,"address":["10.0.0.2/32"],"private_key":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}],
			"outbounds":[{"type":"direct"` + tag + `},{"type":"selector","tag":"proxy","outbounds":["` + member + `"]}],
			"experimental":{"cache_file":{"path":` + string(encodedPath) + `}}
		}`
		if groups, err := GetUIConfigGroups(config); groups != nil || err == nil || !strings.Contains(err.Error(), "duplicate outbound/endpoint tag") {
			t.Fatal("group projection accepted ambiguous outbound/endpoint tags", groups, err)
		}
		if err := SelectUIConfigGroup(config, "proxy", member); err == nil || !strings.Contains(err.Error(), "duplicate outbound/endpoint tag") {
			t.Fatal("group selection accepted ambiguous outbound/endpoint tags", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("invalid group configuration created a cache database", err)
		}
	}
}

func TestUIConfigGroupSelectionPreservesCacheNamespacesAndOtherData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.db")
	uiGroupTestDB(t, path, func(tx *bbolt.Tx) error {
		for _, name := range []string{"selected", "dns_cache", "fakeip_fixture", "unrelated-feature"} {
			bucket, err := tx.CreateBucket([]byte(name))
			if err != nil {
				return err
			}
			if err = bucket.Put([]byte("proxy"), []byte("direct-a")); err != nil {
				return err
			}
		}
		for _, id := range []string{"profile-a", "profile-b"} {
			namespace, err := tx.CreateBucket(append([]byte{0}, []byte(id)...))
			if err != nil {
				return err
			}
			for _, name := range []string{"selected", "dns_cache", "custom-extension"} {
				bucket, err := namespace.CreateBucket([]byte(name))
				if err != nil {
					return err
				}
				if err = bucket.Put([]byte("proxy"), []byte("direct-a")); err != nil {
					return err
				}
			}
		}
		return nil
	})
	expected := uiGroupTestSnapshot(t, path)
	config := uiGroupTestConfig(t, path, "profile-b", "direct-a")
	if err := SelectUIConfigGroup(config, "proxy", "direct-b"); err != nil {
		t.Fatal(err)
	}
	expected["\x00profile-b/selected/proxy"] = "direct-b"
	if actual := uiGroupTestSnapshot(t, path); !reflect.DeepEqual(actual, expected) {
		t.Fatalf("selection modified unrelated cache data: actual=%v expected=%v", actual, expected)
	}
	if got := uiGroupTestRead(t, config)[0].Selected; got != "direct-b" {
		t.Fatal("updated namespace was not read", got)
	}
	if got := uiGroupTestRead(t, uiGroupTestConfig(t, path, "profile-a", "direct-b"))[0].Selected; got != "direct-a" {
		t.Fatal("other profile selection changed", got)
	}
	if err := SelectUIConfigGroup(uiGroupTestConfig(t, path, "", "direct-b"), "proxy", "direct-b"); err != nil {
		t.Fatal(err)
	}
	expected["selected/proxy"] = "direct-b"
	if actual := uiGroupTestSnapshot(t, path); !reflect.DeepEqual(actual, expected) {
		t.Fatal("default namespace write changed other cache data", actual)
	}
}

func TestUIConfigGroupSelectionRejectsBeforeCreatingCache(t *testing.T) {
	for _, test := range []struct {
		name, group, tag string
		auto             bool
		invalid          bool
	}{
		{"unknown group", "missing", "direct-a", false, false},
		{"unknown member", "proxy", "missing", false, false},
		{"non-group outbound", "direct-a", "direct-b", false, false},
		{"automatic group", "proxy", "direct-a", true, false},
		{"invalid config", "proxy", "direct-a", false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.db")
			config := uiGroupTestConfig(t, path, "", "")
			if test.auto || test.invalid {
				var fields map[string]any
				if err := json.Unmarshal([]byte(config), &fields); err != nil {
					t.Fatal(err)
				}
				outbound := fields["outbounds"].([]any)[0].(map[string]any)
				if test.auto {
					outbound["type"] = "urltest"
					delete(outbound, "default")
				} else {
					outbound["outbounds"] = []string{}
				}
				encoded, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				config = string(encoded)
			}
			if err := SelectUIConfigGroup(config, test.group, test.tag); err == nil {
				t.Fatal("invalid stopped selection succeeded")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("rejected selection created a cache database", err)
			}
		})
	}
}

func TestUIConfigGroupsCacheFailuresDoNotResetOrSucceed(t *testing.T) {
	for _, failure := range []string{"empty", "corrupt", "locked"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.db")
			var holder *bbolt.DB
			if failure == "locked" {
				var err error
				holder, err = bbolt.Open(path, 0o600, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Close()
			} else {
				var fixture []byte
				if failure == "corrupt" {
					fixture = bytes.Repeat([]byte("corrupted cache fixture"), 1024)
				}
				if err := os.WriteFile(path, fixture, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			config := uiGroupTestConfig(t, path, "", "direct-a")
			start := time.Now()
			if groups, err := GetUIConfigGroups(config); err == nil || groups != nil {
				t.Fatal("unreadable cache gave a successful or fallback selection")
			}
			if err := SelectUIConfigGroup(config, "proxy", "direct-b"); err == nil {
				t.Fatal("unwritable cache reported selection success")
			}
			if time.Since(start) > 4*time.Second {
				t.Fatal("cache lock wait was not bounded")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("cache failure reset or modified existing data", err)
			}
		})
	}
}

type uiGroupTestLogWriter struct{}

func (uiGroupTestLogWriter) WriteMessage(log.Level, string) {}

func TestUIConfigGroupSelectionIsConsumedByCoreStartup(t *testing.T) {
	for _, cacheID := range []string{"", "startup-profile"} {
		t.Run(cacheID, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "cache.db")
			// The app supplies PlatformLogWriter and uses cache_file even with
			// enabled=false. The core must consume the same choice as the UI.
			config := uiGroupTestConfig(t, path, cacheID, "direct-a")
			if err := SelectUIConfigGroup(config, "proxy", "direct-b"); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(baseContext(nil))
			defer cancel()
			// The host app calls Setup before startup. This standalone fixture
			// supplies the same process ownership without changing global setup.
			ctx = filemanager.WithDefault(ctx, sWorkingPath, sTempPath, os.Getuid(), os.Getgid())
			options, err := parseConfig(ctx, config)
			if err != nil {
				t.Fatal(err)
			}
			instance, err := box.New(box.Options{Context: ctx, Options: options, PlatformLogWriter: uiGroupTestLogWriter{}})
			if err != nil {
				t.Fatal(err)
			}
			defer instance.Close()
			if err = instance.Start(); err != nil {
				t.Fatal(err)
			}
			outbound, found := instance.Outbound().Outbound("proxy")
			if !found {
				t.Fatal("selector missing after startup")
			}
			selected := outbound.(adapter.OutboundGroup).Selected(N.NetworkTCP)
			if selected == nil || selected.Tag() != "direct-b" {
				t.Fatal("core startup ignored the stopped selection", selected)
			}
		})
	}
}

func TestUIConfigGroupCacheUsesWorkingDirectory(t *testing.T) {
	workingPath := sWorkingPath
	sWorkingPath = t.TempDir()
	defer func() { sWorkingPath = workingPath }()
	for _, relative := range []string{"", "custom.db"} {
		config := uiGroupTestConfig(t, relative, "", "direct-a")
		if err := SelectUIConfigGroup(config, "proxy", "direct-b"); err != nil {
			t.Fatal(err)
		}
		name := relative
		if name == "" {
			name = "cache.db"
		}
		if value := uiGroupTestSnapshot(t, filepath.Join(sWorkingPath, name))["selected/proxy"]; value != "direct-b" {
			t.Fatal("cache was not stored relative to the core working directory", value)
		}
		if got := uiGroupTestRead(t, config)[0].Selected; got != "direct-b" {
			t.Fatal("working-directory selection was not read", got)
		}
	}
}
