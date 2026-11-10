package httpdebug

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testScriptStore(t *testing.T) *scriptStore {
	t.Helper()
	store, err := openScriptStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func storeScriptValue(t *testing.T, store *scriptStore, namespace, key, value string) {
	t.Helper()
	if err := store.write(context.Background(), namespace, key, &value); err != nil {
		t.Fatal(err)
	}
}

func expectScriptValue(t *testing.T, store *scriptStore, namespace, key, value string, found bool) {
	t.Helper()
	actual, exists, err := store.read(context.Background(), namespace, key)
	if err != nil || exists != found || actual != value {
		t.Fatalf("storage %s/%s: %q %v %v", namespace, key, actual, exists, err)
	}
}

func TestScriptStoragePersistsIsolatesAndDeletes(t *testing.T) {
	store := testScriptStore(t)
	storeScriptValue(t, store, "first", "key", "one")
	storeScriptValue(t, store, "second", "key", "two")
	storeScriptValue(t, store, "first", "../../outside", "a key is not a path")
	storeScriptValue(t, store, "first", "__proto__", "")
	reopened, err := openScriptStore(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	expectScriptValue(t, reopened, "first", "key", "one", true)
	expectScriptValue(t, reopened, "second", "key", "two", true)
	expectScriptValue(t, reopened, "first", "__proto__", "", true)
	expectScriptValue(t, reopened, "unknown", "key", "", false)
	if err = reopened.write(context.Background(), "second", "key", nil); err != nil {
		t.Fatal(err)
	}
	expectScriptValue(t, reopened, "second", "key", "", false)
	if _, err = os.Stat(filepath.Join(store.directory, "second.json")); !os.IsNotExist(err) {
		t.Fatal("empty namespace was not reclaimed", err)
	}
	info, err := os.Stat(filepath.Join(store.directory, "first.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("storage snapshot is not private", err)
	}
	if reopened.totalBytes != int(info.Size()) {
		t.Fatal("namespace deletion did not release its byte quota")
	}
}

func TestScriptStoragePersistsBeyondFormerQuotas(t *testing.T) {
	t.Run("values and bytes", func(t *testing.T) {
		store := testScriptStore(t)
		// Two snapshots cross the former 4 MiB value, 32 MiB namespace and
		// 64 MiB aggregate limits with only two atomic writes.
		value := strings.Repeat("v", 33*1024*1024)
		key := strings.Repeat("中", 100)
		for _, namespace := range []string{"first", strings.Repeat("n", 65)} {
			storeScriptValue(t, store, namespace, key, value)
		}
		reopened, err := openScriptStore(store.directory)
		if err != nil || reopened.totalBytes != store.totalBytes || reopened.totalBytes <= 64*1024*1024 {
			t.Fatal("large storage snapshots did not reopen", err)
		}
		for _, namespace := range []string{"first", strings.Repeat("n", 65)} {
			expectScriptValue(t, reopened, namespace, key, value, true)
		}
		before := reopened.totalBytes
		storeScriptValue(t, reopened, "first", key, "smaller")
		if reopened.totalBytes >= before {
			t.Fatal("overwrite retained the old byte count")
		}
	})
	t.Run("keys and namespaces", func(t *testing.T) {
		store := testScriptStore(t)
		for i := range 129 {
			storeScriptValue(t, store, "keys", fmt.Sprint(i), "value")
		}
		for i := range 257 {
			storeScriptValue(t, store, fmt.Sprint(i), "key", "value")
		}
		reopened, err := openScriptStore(store.directory)
		if err != nil {
			t.Fatal(err)
		}
		for i := range 129 {
			expectScriptValue(t, reopened, "keys", fmt.Sprint(i), "value", true)
		}
		for i := range 257 {
			expectScriptValue(t, reopened, fmt.Sprint(i), "key", "value", true)
		}
		for _, test := range []struct{ key, value string }{{"", "value"}, {"\xff", "value"}, {"key", "\xff"}} {
			if err := reopened.write(context.Background(), "keys", test.key, &test.value); err == nil {
				t.Fatal("invalid storage text accepted")
			}
		}
	})
}

func TestScriptStorageConcurrentWritesAndCancelledLockWait(t *testing.T) {
	store := testScriptStore(t)
	var wait sync.WaitGroup
	for i := 0; i < 32; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value := fmt.Sprint(i)
			if err := store.write(context.Background(), "shared", value, &value); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	reopened, err := openScriptStore(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 32; i++ {
		expectScriptValue(t, reopened, "shared", fmt.Sprint(i), fmt.Sprint(i), true)
	}
	store.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	value := "must not persist"
	err = store.write(ctx, "shared", "cancelled", &value)
	<-store.gate
	if err == nil {
		t.Fatal("cancelled storage wait committed a value")
	}
	expectScriptValue(t, store, "shared", "cancelled", "", false)
	if err = store.write(ctx, "shared", "cancelled", &value); err == nil {
		t.Fatal("already-cancelled invocation wrote data")
	}
}

func TestScriptStorageAtomicFailureAndInterruptedWriteRecovery(t *testing.T) {
	store := testScriptStore(t)
	storeScriptValue(t, store, "ns", "key", "old")
	path := filepath.Join(store.directory, "ns.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pending := filepath.Join(store.directory, scriptStorageTemporary)
	if err = os.Mkdir(pending, 0o700); err != nil {
		t.Fatal(err)
	}
	value := "new"
	if err = store.write(context.Background(), "ns", "key", &value); err == nil {
		t.Fatal("failed disk write was reported successful")
	}
	expectScriptValue(t, store, "ns", "key", "old", true)
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) || store.totalBytes != len(before) {
		t.Fatal("failed write changed the committed file or quota", err)
	}
	if err = os.Remove(pending); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(pending, []byte("interrupted snapshot"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := openScriptStore(store.directory)
	if err != nil {
		t.Fatal(err)
	}
	expectScriptValue(t, reopened, "ns", "key", "old", true)
	if _, err = os.Stat(pending); !os.IsNotExist(err) {
		t.Fatal("uncommitted snapshot survived recovery", err)
	}
	storeScriptValue(t, reopened, "ns", "key", "new")
}

func TestScriptStorageRejectsCorruptionAndSymlinksWithoutDataLoss(t *testing.T) {
	for _, data := range []string{
		`{"version":1,"values":`,
		`{"version":2,"values":{"key":"old"}}`,
		`{"version":1,"values":{"key":null}}`,
		`{"version":1,"values":{"key":"old"},"unknown":true}`,
		`{"version":1,"values":{"key":"old"}} {}`,
		" ",
	} {
		directory := t.TempDir()
		path := filepath.Join(directory, "ns.json")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := openScriptStore(directory); err == nil {
			t.Fatal("invalid snapshot was silently replaced")
		}
		if kept, err := os.ReadFile(path); err != nil || string(kept) != data {
			t.Fatal("invalid snapshot was destroyed", err)
		}
	}
	for _, name := range []string{"ns.json", scriptStorageTemporary} {
		directory, outside := t.TempDir(), t.TempDir()
		target := filepath.Join(outside, "keep")
		if err := os.WriteFile(target, []byte("private"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(directory, name)); err != nil {
			t.Fatal(err)
		}
		if _, err := openScriptStore(directory); err == nil {
			t.Fatal("followed a storage file symlink")
		}
		if kept, err := os.ReadFile(target); err != nil || string(kept) != "private" {
			t.Fatal("symlink target changed", err)
		}
	}
	directory, outside := t.TempDir(), t.TempDir()
	path := filepath.Join(directory, "linked")
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := openScriptStore(path); err == nil {
		t.Fatal("followed a storage directory symlink")
	}
}

func TestPersistentStoreAPIIsScopedSharedWithCronAndHasNoCallQuota(t *testing.T) {
	store := testScriptStore(t)
	run := func(namespace, source string) error {
		rule := httpAPIScript(t, HTTPScript{StorageNamespace: namespace, Source: source, TimeoutMS: 10000})
		request, _ := http.NewRequest("GET", "http://intercepted.test/", nil)
		_, err := runHTTPScript(request, nil, rule, "test", nil, scriptEnvironment{storage: store}, func(string) {})
		return err
	}
	if err := run("shared", `if($persistentStore.read('key')!==null) throw Error('missing');
		if(!$persistentStore.write('','empty') || $persistentStore.read('empty')!=='') throw Error('empty');
		if(!$persistentStore.write(null,'empty') || $persistentStore.read('empty')!==null) throw Error('delete');
		if(!$persistentStore.write('http','key')) throw Error('write'); $done();`); err != nil {
		t.Fatal(err)
	}
	content, _ := json.Marshal(ScriptsConfig{Cron: []CronScript{{
		Cron: "* * * * *", StorageNamespace: "shared",
		Source: `if($persistentStore.read('key')!=='http') throw Error('not shared'); if(!$persistentStore.write('cron','key')) throw Error('write'); $done();`,
	}}})
	program := scriptProgram(t, string(content))
	program.storage = store
	runner := &cronRunner{ctx: context.Background(), program: program}
	if err := runner.run(program.cron[0]); err != nil {
		t.Fatal(err)
	}
	if err := run("other", `if($persistentStore.read('key','shared')!==null) throw Error('escaped namespace'); $done();`); err != nil {
		t.Fatal(err)
	}
	if err := run("", `if(!$persistentStore.write('default','key') || $persistentStore.read('key')!=='default') throw Error('default storage');
		if(!$persistentStore.write('own') || $persistentStore.read()!=='own') throw Error('implicit key'); $done();`); err != nil {
		t.Fatal(err)
	}
	if err := run("shared", `if(!$persistentStore.write('x'.repeat(4194305),'large') || $persistentStore.read('large').length!==4194305) throw Error('large value'); $done();`); err != nil {
		t.Fatal(err)
	}
	if err := run("shared", `for(let i=0;i<129;i++) if($persistentStore.read('key')!=='cron') throw Error('read'); if(!$persistentStore.write('saved','after-many-reads')) throw Error('write'); $done();`); err != nil {
		t.Fatal(err)
	}
	expectScriptValue(t, store, "shared", "key", "cron", true)
	if err := run("shared", `$persistentStore.write('committed','key'); throw Error('after write');`); err == nil {
		t.Fatal("script exception disappeared")
	}
	if err := run("shared", `setTimeout(()=>{$persistentStore.write('late','key');},1); $done();`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	expectScriptValue(t, store, "shared", "key", "committed", true)
}

func TestScriptStorageSurvivesPreparationReloadAndCaptureClear(t *testing.T) {
	StopService()
	managerMu.Lock()
	previousStore, previousState := store, lastCaptureState
	store, lastCaptureState = nil, State{}
	managerMu.Unlock()
	t.Cleanup(func() {
		StopService()
		managerMu.Lock()
		store, lastCaptureState = previousStore, previousState
		managerMu.Unlock()
	})
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "routed") }))
	defer origin.Close()
	config := &ScriptsConfig{Enabled: true, HTTP: []HTTPScript{{
		RewriteMatch: RewriteMatch{Pattern: "."}, Phase: "request", TimeoutMS: 2000, StorageNamespace: "stable",
		HTTPClient: &ScriptHTTPOptions{AllowedOrigins: []string{"http://aux.test"}},
		Source: `setTimeout(()=>{$httpClient.get('http://aux.test/',(e,r,d)=>{
			if(e || !$persistentStore.write(d,'key')) throw Error(e || 'write');
			$done({response:{body:d}});
		});},1);`,
	}}}
	directory := t.TempDir()
	first, err := PrepareProcessing(directory, nil, nil, config, "", nil, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	dial := func(ctx context.Context, address string) (net.Conn, error) {
		if address != "aux.test:80" {
			t.Errorf("wrong service script route %s", address)
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
	}
	ActivateProcessing(first, dial, nil)
	exchange := func() string {
		client := debugClient(t, Current(), "127.0.0.1:1")
		response, err := client.Get("http://intercepted.test/")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("script processing failed: %s %v", body, err)
		}
		return string(body)
	}
	if body := exchange(); body != "routed" {
		t.Fatal("service did not supply script HTTP and storage", body)
	}
	config.HTTP[0].Source = "$done({response:{body:$persistentStore.read('key')}});"
	second, err := PrepareProcessing(directory, nil, nil, config, "", nil, "UTC")
	if err != nil || first.scripts.storage != second.scripts.storage {
		t.Fatal("preparation created a stale storage cache", err)
	}
	storeScriptValue(t, first.scripts.storage, "stable", "key", "written-after-prepare")
	ActivateProcessing(second, dial, nil)
	if err = Start(directory, "{}", "", nil); err != nil {
		t.Fatal(err)
	}
	store.Clear()
	Stop()
	if body := exchange(); body != "written-after-prepare" {
		t.Fatal("reload or recording cleared script data", body)
	}
	StopService()
	reopened, err := openScriptStore(filepath.Join(directory, "script-storage"))
	if err != nil {
		t.Fatal(err)
	}
	expectScriptValue(t, reopened, "stable", "key", "written-after-prepare", true)
}
