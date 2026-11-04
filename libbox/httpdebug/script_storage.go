package httpdebug

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/dop251/goja"
)

const (
	maxScriptStorageNamespaces = 256
	maxScriptStorageKeys       = 128
	maxScriptStorageKeyBytes   = 256
	maxScriptStorageValueBytes = 16 * 1024
	maxScriptStorageBytes      = 64 * 1024
	maxScriptStorageTotalBytes = 4 * 1024 * 1024
	maxScriptStorageCalls      = 64
	scriptStorageTemporary     = ".pending"
)

type scriptNamespace struct {
	values map[string]string
	bytes  int
}

type scriptStore struct {
	directory  string
	gate       chan struct{}
	namespaces map[string]scriptNamespace
	totalBytes int
}

type scriptStorageDocument struct {
	Version int               `json:"version"`
	Values  map[string]string `json:"values"`
}

func validateStorageNamespace(namespace string) error {
	if namespace == "" { // Storage is opt-in, independently for each rule.
		return nil
	}
	if len(namespace) > 64 {
		return errors.New("storage_namespace must contain 1 to 64 ASCII letters, digits, dots, underscores or hyphens, starting with a letter or digit")
	}
	for i, c := range namespace {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			i > 0 && (c == '.' || c == '_' || c == '-') {
			continue
		}
		return errors.New("invalid storage_namespace: use ASCII letters, digits, dots, underscores or hyphens, starting with a letter or digit")
	}
	return nil
}

func (p *ScriptProgram) needsStorage() bool {
	for _, rule := range p.http {
		if rule.options.StorageNamespace != "" {
			return true
		}
	}
	for _, rule := range p.cron {
		if rule.options.StorageNamespace != "" {
			return true
		}
	}
	return false
}

// Only preparation opens storage. Config validation never reads or writes
// device data. The Store owns one shared instance across service reloads,
// including preparation while old scripts are still finishing their writes.
func openScriptStore(directory string) (*scriptStore, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("script storage directory must not be a symlink")
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	store := &scriptStore{directory: directory, gate: make(chan struct{}, 1), namespaces: make(map[string]scriptNamespace)}
	for _, entry := range entries {
		name := entry.Name()
		// A crash before rename leaves an uncommitted snapshot. Never promote
		// it to stored data or follow a symlink during recovery.
		if name == scriptStorageTemporary {
			if !entry.Type().IsRegular() {
				return nil, errors.New("invalid pending script storage file")
			}
			if err = os.Remove(filepath.Join(directory, name)); err != nil {
				return nil, err
			}
			continue
		}
		namespace := strings.TrimSuffix(name, ".json")
		if namespace == "" || !strings.HasSuffix(name, ".json") || validateStorageNamespace(namespace) != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > maxScriptStorageBytes || len(store.namespaces) >= maxScriptStorageNamespaces {
			return nil, fmt.Errorf("invalid or oversized script storage namespace %q", namespace)
		}
		file, err := os.Open(filepath.Join(directory, name))
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(file, maxScriptStorageBytes+1))
		file.Close()
		if err != nil {
			return nil, err
		}
		if len(data) > maxScriptStorageBytes || !utf8.Valid(data) {
			return nil, fmt.Errorf("invalid script storage namespace %q", namespace)
		}
		var document struct {
			Version int                `json:"version"`
			Values  map[string]*string `json:"values"`
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err = decoder.Decode(&document); err != nil {
			return nil, fmt.Errorf("read script storage namespace %q: %w", namespace, err)
		}
		if decoder.Decode(new(any)) != io.EOF || document.Version != 1 || len(document.Values) == 0 || len(document.Values) > maxScriptStorageKeys {
			return nil, fmt.Errorf("invalid script storage namespace %q", namespace)
		}
		values := make(map[string]string, len(document.Values))
		for key, value := range document.Values {
			if value == nil || validScriptStorageKey(key) != nil || !utf8.ValidString(*value) || len(*value) > maxScriptStorageValueBytes {
				return nil, fmt.Errorf("invalid script storage entry in %q", namespace)
			}
			values[key] = *value
		}
		store.totalBytes += len(data)
		if store.totalBytes > maxScriptStorageTotalBytes {
			return nil, errors.New("script storage exceeds its global 4 MiB quota")
		}
		store.namespaces[namespace] = scriptNamespace{values, len(data)}
	}
	return store, nil
}

func validScriptStorageKey(key string) error {
	if key == "" || len(key) > maxScriptStorageKeyBytes || !utf8.ValidString(key) {
		return errors.New("storage key must contain 1 to 256 UTF-8 bytes")
	}
	return nil
}

func (s *scriptStore) lock(ctx context.Context) error {
	select {
	case s.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.gate
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *scriptStore) read(ctx context.Context, namespace, key string) (string, bool, error) {
	if err := s.lock(ctx); err != nil {
		return "", false, err
	}
	defer func() { <-s.gate }()
	value, found := s.namespaces[namespace].values[key]
	return value, found, nil
}

// Each write commits a complete bounded namespace before changing the cache
// or quota counters. Concurrent writes to different keys never overwrite a
// stale snapshot. A read followed by a write is not a multi-operation transaction.
func (s *scriptStore) write(ctx context.Context, namespace, key string, value *string) error {
	if namespace == "" {
		return errors.New("storage requires storage_namespace")
	}
	if err := validateStorageNamespace(namespace); err != nil {
		return err
	}
	if err := validScriptStorageKey(key); err != nil {
		return err
	}
	if value != nil && (!utf8.ValidString(*value) || len(*value) > maxScriptStorageValueBytes) {
		return errors.New("storage value exceeds 16 KiB or is not UTF-8")
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer func() { <-s.gate }()
	old, exists := s.namespaces[namespace]
	current, found := old.values[key]
	if value == nil && !found || value != nil && found && current == *value {
		return nil
	}
	values := maps.Clone(old.values)
	if values == nil {
		values = make(map[string]string)
	}
	if value == nil {
		delete(values, key)
	} else {
		values[key] = *value
	}
	if len(values) > maxScriptStorageKeys || !exists && len(s.namespaces) >= maxScriptStorageNamespaces {
		return errors.New("script storage key or namespace quota exceeded")
	}
	if len(values) == 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(s.directory, namespace+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		delete(s.namespaces, namespace)
		s.totalBytes -= old.bytes
		return nil
	}
	data, err := json.Marshal(scriptStorageDocument{1, values})
	if err != nil {
		return err
	}
	nextTotal := s.totalBytes - old.bytes + len(data)
	if len(data) > maxScriptStorageBytes || nextTotal > maxScriptStorageTotalBytes {
		return errors.New("script storage byte quota exceeded (64 KiB per namespace, 4 MiB total)")
	}
	if err = s.commit(ctx, namespace+".json", data); err != nil {
		return err
	}
	s.namespaces[namespace] = scriptNamespace{values, len(data)}
	s.totalBytes = nextTotal
	return nil
}

func (s *scriptStore) commit(ctx context.Context, name string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	temporary := filepath.Join(s.directory, scriptStorageTemporary)
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		file.Close()
		os.Remove(temporary)
	}()
	if written, err := file.Write(data); err != nil {
		return err
	} else if written != len(data) {
		return io.ErrShortWrite
	}
	if err = file.Sync(); err != nil {
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return os.Rename(temporary, filepath.Join(s.directory, name))
}

func installScriptStorage(vm *goja.Runtime, ctx context.Context, store *scriptStore, namespace, label string) {
	calls := 0
	warned := false
	check := func() error {
		calls++
		if namespace == "" {
			return errors.New("persistent storage requires storage_namespace on this rule")
		}
		if store == nil {
			return errors.New("persistent storage requires the running local service")
		}
		if calls > maxScriptStorageCalls {
			return errors.New("script storage operation limit exceeded (64)")
		}
		return ctx.Err()
	}
	keyValue := func(value goja.Value) string {
		if value.ExportType() != reflect.TypeFor[string]() {
			panic(vm.NewTypeError("storage key must be a string"))
		}
		key := value.String()
		if err := validScriptStorageKey(key); err != nil {
			panic(vm.NewTypeError("%s", err))
		}
		return key
	}
	_ = vm.Set("$persistentStore", map[string]any{
		"read": func(call goja.FunctionCall) goja.Value {
			key := keyValue(call.Argument(0))
			if err := check(); err != nil {
				panic(vm.NewGoError(err))
			}
			value, found, err := store.read(ctx, namespace, key)
			if err != nil {
				panic(vm.NewGoError(err))
			}
			if !found {
				return goja.Null()
			}
			return vm.ToValue(value)
		},
		"write": func(call goja.FunctionCall) goja.Value {
			key := keyValue(call.Argument(1))
			var data *string
			if value := call.Argument(0); !goja.IsNull(value) {
				if value.ExportType() != reflect.TypeFor[string]() {
					panic(vm.NewTypeError("storage value must be a string or null (delete)"))
				}
				text := value.String()
				data = &text
			}
			err := check()
			if err == nil {
				err = store.write(ctx, namespace, key, data)
			}
			if err != nil && !warned {
				warned = true
				writeLog("warn", label+": storage write failed: "+err.Error())
			}
			return vm.ToValue(err == nil)
		},
	})
}
