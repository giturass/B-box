package libbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/sagernet/bbolt"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/service/filemanager"
)

// GetUIConfigGroups projects the configured groups while the Android service is
// stopped. It reads the same selector cache as Selector.Start; it does not start
// a service or invent URL-test results for a group that has not run.
func GetUIConfigGroups(config string) (OutboundGroupIterator, error) {
	ctx := baseContext(nil)
	options, err := parseConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	types := uiConfigOutboundTypes(options)
	var groups []*OutboundGroup
	for index, outbound := range options.Outbounds {
		g := &OutboundGroup{Tag: uiConfigOutboundTag(outbound.Tag, index), Type: outbound.Type}
		var members []string
		switch outbound.Type {
		case C.TypeSelector:
			selector := outbound.Options.(*option.SelectorOutboundOptions)
			members = selector.Outbounds
			g.Selectable = true
			g.Selected = selector.Default
			if g.Selected == "" && len(members) > 0 {
				g.Selected = members[0]
			}
		case C.TypeURLTest:
			members = outbound.Options.(*option.URLTestOutboundOptions).Outbounds
		default:
			continue
		}
		for _, tag := range members {
			if itemType, exists := types[tag]; exists {
				g.itemList = append(g.itemList, &OutboundGroupItem{Tag: tag, Type: itemType})
			}
		}
		// daemon.readGroups also excludes groups with no resolved members.
		if len(g.itemList) > 0 {
			groups = append(groups, g)
		}
	}
	if len(groups) == 0 {
		return newIterator(groups), nil
	}
	path, cacheID := uiConfigGroupCache(ctx, options)
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return newIterator(groups), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read selector cache: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("selector cache is not a regular file")
	}
	if info.Size() == 0 {
		return nil, fmt.Errorf("selector cache is empty")
	}
	db, err := bbolt.Open(path, 0o666, &bbolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return nil, fmt.Errorf("open selector cache: %w", err)
	}
	err = uiConfigGroupTransaction(db, true, func(tx *bbolt.Tx) error {
		selected := uiConfigGroupBucket(tx, cacheID, []byte("selected"))
		expand := uiConfigGroupBucket(tx, cacheID, []byte("group_expand"))
		for _, g := range groups {
			if g.Selectable && selected != nil {
				cached := string(selected.Get([]byte(g.Tag)))
				if slices.ContainsFunc(g.itemList, func(item *OutboundGroupItem) bool { return item.Tag == cached }) {
					g.Selected = cached
				}
			}
			if expand != nil {
				value := expand.Get([]byte(g.Tag))
				g.IsExpand = len(value) == 1 && value[0] == 1
			}
		}
		return nil
	})
	closeErr := db.Close()
	if err != nil {
		return nil, fmt.Errorf("read selector cache: %w", err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close selector cache: %w", closeErr)
	}
	return newIterator(groups), nil
}

// SelectUIConfigGroup persists a stopped local profile's selector choice for
// the next Selector.Start. Android must serialize this with profile revisions,
// session changes and service startup; running groups use the daemon API.
func SelectUIConfigGroup(config, group, tag string) error {
	if err := CheckConfig(config); err != nil {
		return err
	}
	ctx := baseContext(nil)
	options, err := parseConfig(ctx, config)
	if err != nil {
		return err
	}
	var selector *option.SelectorOutboundOptions
	for index, outbound := range options.Outbounds {
		if uiConfigOutboundTag(outbound.Tag, index) != group {
			continue
		}
		if outbound.Type != C.TypeSelector {
			return fmt.Errorf("outbound group is not selectable")
		}
		selector = outbound.Options.(*option.SelectorOutboundOptions)
		break
	}
	if selector == nil {
		return fmt.Errorf("outbound group not found")
	}
	if !slices.Contains(selector.Outbounds, tag) {
		return fmt.Errorf("outbound is not a member of the selector")
	}
	if _, exists := uiConfigOutboundTypes(options)[tag]; !exists {
		return fmt.Errorf("selector outbound not found")
	}
	// Validate before opening or creating anything. Opening cachefile.CacheFile's
	// lifecycle here would prune unknown buckets or reset a corrupt database.
	path, cacheID := uiConfigGroupCache(ctx, options)
	if info, statErr := os.Stat(path); statErr == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("selector cache is not a regular file")
		}
		if info.Size() == 0 {
			return fmt.Errorf("selector cache is empty")
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("read selector cache: %w", statErr)
	}
	db, err := bbolt.Open(path, 0o666, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		return fmt.Errorf("open selector cache: %w", err)
	}
	err = uiConfigGroupTransaction(db, false, func(tx *bbolt.Tx) error {
		var bucket *bbolt.Bucket
		var err error
		if len(cacheID) == 0 {
			bucket, err = tx.CreateBucketIfNotExists([]byte("selected"))
		} else {
			var namespace *bbolt.Bucket
			namespace, err = tx.CreateBucketIfNotExists(cacheID)
			if err == nil {
				bucket, err = namespace.CreateBucketIfNotExists([]byte("selected"))
			}
		}
		if err != nil {
			return err
		}
		return bucket.Put([]byte(group), []byte(tag))
	})
	closeErr := db.Close()
	if err != nil {
		return fmt.Errorf("store selector cache: %w", err)
	}
	if closeErr != nil {
		return fmt.Errorf("close selector cache: %w", closeErr)
	}
	return nil
}

func uiConfigOutboundTag(tag string, index int) string {
	if tag == "" {
		return strconv.Itoa(index)
	}
	return tag
}

func uiConfigOutboundTypes(options option.Options) map[string]string {
	types := make(map[string]string, len(options.Outbounds)+len(options.Endpoints))
	for index, endpoint := range options.Endpoints {
		types[uiConfigOutboundTag(endpoint.Tag, index)] = endpoint.Type
	}
	for index, outbound := range options.Outbounds {
		// OutboundManager.Outbound resolves configured outbounds before endpoints.
		types[uiConfigOutboundTag(outbound.Tag, index)] = outbound.Type
	}
	return types
}

func uiConfigGroupCache(ctx context.Context, options option.Options) (string, []byte) {
	var cache option.CacheFileOptions
	if options.Experimental != nil && options.Experimental.CacheFile != nil {
		cache = *options.Experimental.CacheFile
	}
	if cache.Path == "" {
		cache.Path = "cache.db"
	}
	var cacheID []byte
	if cache.CacheID != "" {
		cacheID = append([]byte{0}, []byte(cache.CacheID)...)
	}
	// Android supplies PlatformLogWriter, so box.New uses this cache even when
	// experimental.cache_file.enabled is false or omitted.
	return filemanager.BasePath(ctx, cache.Path), cacheID
}

func uiConfigGroupBucket(tx *bbolt.Tx, cacheID, name []byte) *bbolt.Bucket {
	if len(cacheID) == 0 {
		return tx.Bucket(name)
	}
	namespace := tx.Bucket(cacheID)
	if namespace == nil {
		return nil
	}
	return namespace.Bucket(name)
}

func uiConfigGroupTransaction(db *bbolt.DB, readOnly bool, action func(*bbolt.Tx) error) (err error) {
	defer func() {
		if reason := recover(); reason != nil {
			err = fmt.Errorf("selector cache is corrupted: %v", reason)
		}
	}()
	check := func(tx *bbolt.Tx) error {
		var firstErr error
		for checkErr := range tx.Check() {
			if firstErr == nil {
				firstErr = checkErr
			}
		}
		return firstErr
	}
	if readOnly {
		return db.View(func(tx *bbolt.Tx) error {
			if err := check(tx); err != nil {
				return err
			}
			return action(tx)
		})
	}
	if err := db.View(check); err != nil {
		return err
	}
	return db.Update(action)
}
