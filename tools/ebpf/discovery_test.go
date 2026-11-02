package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type countedLink struct{ closed *atomic.Int32 }

func (l countedLink) Close() error { l.closed.Add(1); return nil }

func discoveryFiles(t *testing.T, count int) []string {
	t.Helper()
	directory := t.TempDir()
	var paths []string
	for i := 0; i < count; i++ {
		path := filepath.Join(directory, fmt.Sprintf("library-%d.so", i))
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

func TestDiscoveryPublishesFirstAttachBeforeSlowLibraryAndCancels(t *testing.T) {
	paths := discoveryFiles(t, 2)
	d := newLibraryDiscovery(nil, nil)
	var closed atomic.Int32
	d.attachLibrary = func(context.Context, library) ([]io.Closer, error) {
		return []io.Closer{countedLink{&closed}}, nil
	}
	slowStarted := make(chan struct{})
	d.walk = func(ctx context.Context, path string, visit func(library) error) error {
		if path == paths[1] {
			close(slowStarted)
			<-ctx.Done()
			return ctx.Err()
		}
		return visit(library{path: path, identity: path})
	}
	ctx, cancel := context.WithCancel(context.Background())
	updates := make(chan discoveryUpdate)
	done := make(chan struct{})
	var workerErr error
	go func() {
		defer close(done)
		defer d.close()
		workerErr = d.watch(ctx, collectorConfig{Libraries: paths}, updates)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("discovery did not stop after cancellation")
		}
	})
	deadline := time.After(5 * time.Second)
ready:
	for {
		select {
		case update := <-updates:
			if update.libraries == 1 {
				if !update.scanning {
					t.Fatal("reported discovery complete before inspecting the slow library")
				}
				break ready
			}
		case <-deadline:
			t.Fatal("first attach waited for an unrelated slow library")
		}
	}
	// Continue consuming diagnostics after ready, as the event loop does, while
	// the next library is being inspected independently of plaintext reads.
waiting:
	for {
		select {
		case <-slowStarted:
			break waiting
		case <-updates:
		case <-deadline:
			t.Fatal("discovery stopped after its first attach")
		}
	}
	cancel()
	select {
	case <-done:
	case <-deadline:
		t.Fatal("slow discovery ignored cancellation")
	}
	d.close()
	if !errors.Is(workerErr, context.Canceled) || closed.Load() != 1 {
		t.Fatalf("shutdown: error=%v, handles closed=%d", workerErr, closed.Load())
	}
}

func TestUnavailableExplicitPathPublishesDiagnosticWithoutNewAttachment(t *testing.T) {
	d := newLibraryDiscovery(nil, nil)
	d.scanning = false
	d.attached["already-attached"] = nil
	path := filepath.Join(t.TempDir(), "missing.so")
	d.explicit[path] = true
	var updates []discoveryUpdate
	for i := 0; i < 2; i++ {
		if err := d.scan(context.Background(), []string{path}, func(value discoveryUpdate) error {
			updates = append(updates, value)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(updates) != 1 || updates[0].libraries != 1 || updates[0].scanning || !strings.Contains(updates[0].warning, "missing.so") {
		t.Fatalf("missing or repeated diagnostic: %+v", updates)
	}
}

func TestDiscoveryDeduplicatesInodesAndUnwindsFailedProbes(t *testing.T) {
	paths := discoveryFiles(t, 2)
	alias := paths[0] + ".alias.so"
	if err := os.Link(paths[0], alias); err != nil {
		t.Fatal(err)
	}
	d := newLibraryDiscovery(nil, nil)
	var inspections, attachments int
	var closed atomic.Int32
	d.walk = func(_ context.Context, path string, visit func(library) error) error {
		inspections++
		for i := 0; i < 2; i++ {
			if err := visit(library{path: path, identity: path}); err != nil {
				return err
			}
		}
		return nil
	}
	d.attachLibrary = func(context.Context, library) ([]io.Closer, error) {
		attachments++
		return []io.Closer{countedLink{&closed}}, nil
	}
	var counts []int
	report := func(update discoveryUpdate) error {
		if update.libraries > 0 {
			counts = append(counts, update.libraries)
		}
		return nil
	}
	for i := 0; i < 2; i++ {
		if err := d.scan(context.Background(), append(paths, alias), report); err != nil {
			t.Fatal(err)
		}
	}
	if inspections != 2 || attachments != 2 || fmt.Sprint(counts) != "[1 2]" {
		t.Fatalf("inspections=%d attachments=%d counts=%v", inspections, attachments, counts)
	}
	d.close()
	if closed.Load() != 2 {
		t.Fatal("not all perf handles closed")
	}
}

func TestDiscoveryNeverReportsAnEmptyOrFailedAttachAsReady(t *testing.T) {
	for _, failed := range []bool{false, true} {
		paths := discoveryFiles(t, 4)
		d := newLibraryDiscovery(nil, nil)
		d.walk = func(_ context.Context, path string, visit func(library) error) error {
			return visit(library{path: path, entry: path, identity: path})
		}
		d.attachLibrary = func(context.Context, library) ([]io.Closer, error) {
			if failed {
				return nil, errors.New("kernel denied the probe")
			}
			return nil, nil
		}
		var diagnostics []string
		if err := d.scan(context.Background(), paths, func(update discoveryUpdate) error {
			if update.libraries != 0 {
				t.Fatal("failed probe reported as attached")
			}
			if update.level == "warn" {
				diagnostics = append(diagnostics, update.stage)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if len(d.attached) != 0 || len(d.failures) != 3 {
			t.Fatalf("attached=%d failures=%v", len(d.attached), d.failures)
		}
		if len(diagnostics) != len(paths) || !strings.Contains(diagnostics[3], paths[3]) {
			t.Fatalf("detailed log lost failures beyond the three-warning status summary: %v", diagnostics)
		}
	}
}

func TestDiscoveryLogsResolvedOffsetsAndPreparationAfterReady(t *testing.T) {
	paths := discoveryFiles(t, 2)
	d := newLibraryDiscovery(nil, nil)
	d.walk = func(_ context.Context, path string, visit func(library) error) error {
		return visit(library{path: path, entry: "lib/arm64-v8a/libssl.so", identity: path, method: "symbols", base: 4096,
			symbols: map[string]uint64{"SSL_new": 16, "SSL_read": 32}})
	}
	var closed atomic.Int32
	d.attachLibrary = func(context.Context, library) ([]io.Closer, error) { return []io.Closer{countedLink{&closed}}, nil }
	defer d.close()
	var logs []string
	if err := d.scan(context.Background(), paths, func(update discoveryUpdate) error {
		logs = append(logs, update.stage)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(logs, "\n")
	for _, detail := range []string{paths[1], "method=symbols", "archiveOffset=0x1000", "SSL_new=0x1010", "SSL_read=0x1020", "probes=1"} {
		if !strings.Contains(joined, detail) {
			t.Fatalf("missing diagnostic %q: %s", detail, joined)
		}
	}
	if !strings.Contains(joined, "resolving TLS function offsets in packages=unknown path=\""+paths[1]+"\"") {
		t.Fatal("preparation diagnostics stopped after the first attached library")
	}
}

func TestLibraryInspectionHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := inspectLibraries(ctx, "does-not-exist.so"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := discoverPaths(ctx, collectorConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestDiscoveryKeepsCompatibleAPKMembersWithDiagnostics(t *testing.T) {
	path := discoveryFiles(t, 1)[0]
	d := newLibraryDiscovery(nil, nil)
	d.walk = func(_ context.Context, path string, visit func(library) error) error {
		if err := visit(library{path: path, identity: path}); err != nil {
			return err
		}
		return &unsupportedChromiumError{"libcronet.so", "unverified ABI"}
	}
	var closed atomic.Int32
	d.attachLibrary = func(context.Context, library) ([]io.Closer, error) {
		return []io.Closer{countedLink{&closed}}, nil
	}
	var updates []discoveryUpdate
	if err := d.scan(context.Background(), []string{path}, func(update discoveryUpdate) error {
		if update.libraries > 0 {
			updates = append(updates, update)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	defer d.close()
	if len(updates) != 2 || updates[0].libraries != 1 || updates[1].libraries != 1 ||
		!strings.Contains(updates[1].warning, "libcronet.so") {
		t.Fatalf("updates=%+v", updates)
	}
}

func TestInstalledNativeDirectoryDiscoveredBeforeProcessLaunch(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "libprivate.so")
	if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	paths, err := discoverPaths(context.Background(), collectorConfig{TargetPackages: map[string][]string{directory: {"com.example"}}, ExcludeUID: uint32(os.Getuid())})
	if err != nil {
		t.Fatal(err)
	}
	for _, found := range paths {
		if found == path {
			return
		}
	}
	t.Fatal("installed native library was not discovered")
}

func TestDiscoveryFailuresKeepPackagePathAndEntryInLogsAndSummary(t *testing.T) {
	paths := discoveryFiles(t, 2)
	d := newLibraryDiscovery(nil, nil)
	d.config.TargetPackages = map[string][]string{paths[0]: {"com.example.first"}, paths[1]: {"com.example.second"}}
	d.walk = func(_ context.Context, _ string, _ func(library) error) error {
		return &unsupportedChromiumError{"lib/arm64-v8a/libcronet.so", "unverified ABI"}
	}
	var logs []string
	if err := d.scan(context.Background(), paths, func(update discoveryUpdate) error {
		if update.level == "warn" {
			logs = append(logs, update.stage)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for i, path := range paths {
		for _, text := range []string{logs[i], d.failures[i]} {
			if !strings.Contains(text, path) || !strings.Contains(text, d.config.TargetPackages[path][0]) ||
				!strings.Contains(text, "lib/arm64-v8a/libcronet.so") {
				t.Fatal("library diagnostic lost application provenance", text)
			}
		}
	}
}

func TestRejectedManualLibraryCannotFallBackThroughAnInodeAlias(t *testing.T) {
	paths := discoveryFiles(t, 2)
	for _, path := range paths {
		if err := os.WriteFile(path, symbolFixture(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	alias := paths[0] + ".alias"
	if err := os.Link(paths[0], alias); err != nil {
		t.Fatal(err)
	}
	value := manualFixture(symbolFixture(), paths[0], "")
	value.SHA256 = strings.Repeat("0", 64)
	config := collectorConfig{TLSOffsets: []manualTLSOffsets{value}, Libraries: []string{alias, paths[1]}}
	d := newLibraryDiscovery(nil, nil)
	d.walk = func(ctx context.Context, path string, visit func(library) error) error {
		return walkLibraryOptions(ctx, path, libraryOptions{offsets: config.TLSOffsets}, visit)
	}
	var attached []string
	var closed atomic.Int32
	d.attachLibrary = func(_ context.Context, value library) ([]io.Closer, error) {
		attached = append(attached, value.path)
		return []io.Closer{countedLink{&closed}}, nil
	}
	defer d.close()
	if err := d.scan(context.Background(), initialLibraryPaths(config), func(discoveryUpdate) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(attached) != 1 || attached[0] != paths[1] || !strings.Contains(strings.Join(d.failures, "\n"), "SHA-256 mismatch") {
		t.Fatal("manual error bypassed through alias or prevented an unrelated library", attached, d.failures)
	}
}

func TestAPKManualDiagnosticsAreNotTruncatedToTheStatusSummary(t *testing.T) {
	path := discoveryFiles(t, 1)[0]
	d := newLibraryDiscovery(nil, nil)
	d.walk = func(context.Context, string, func(library) error) error {
		var problems []error
		for i := 0; i < 5; i++ {
			problems = append(problems, &manualTLSError{fmt.Sprintf("library-%d.so: SHA-256 mismatch", i)})
		}
		return errors.Join(problems...)
	}
	var logs []string
	if err := d.scan(context.Background(), []string{path}, func(update discoveryUpdate) error {
		if update.level == "warn" {
			logs = append(logs, update.stage)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(d.failures) != 3 || len(logs) != 5 || !strings.Contains(logs[4], "library-4.so") {
		t.Fatal("APK diagnostics were lost beyond the three-warning summary", d.failures, logs)
	}
}
