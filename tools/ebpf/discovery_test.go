package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cilium/ebpf/link"
)

// Link's other operations are unused by discovery; no kernel privileges are
// needed to verify ownership and shutdown of an attached probe.
type countedLink struct {
	link.Link
	closed *atomic.Int32
}

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
	d := newLibraryDiscovery(nil)
	var closed atomic.Int32
	d.attachLibrary = func(context.Context, library) ([]link.Link, error) {
		return []link.Link{countedLink{closed: &closed}}, nil
	}
	slowStarted := make(chan struct{})
	d.inspect = func(ctx context.Context, path string) ([]library, error) {
		if path == paths[1] {
			close(slowStarted)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []library{{path: path, identity: path}}, nil
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
			t.Error("discovery did not stop after owner cancellation")
		}
	})
	deadline := time.After(5 * time.Second)
	for {
		select {
		case update := <-updates:
			if update.libraries == 0 {
				continue
			}
			if update.libraries != 1 {
				t.Fatalf("first attach count = %d", update.libraries)
			}
		case <-deadline:
			t.Fatal("first attach waited for an unrelated slow library")
		}
		break
	}
	select {
	case <-slowStarted:
	case <-deadline:
		t.Fatal("discovery stopped after its first attach")
	}
	cancel()
	select {
	case <-done:
	case <-deadline:
		t.Fatal("slow discovery ignored cancellation")
	}
	if !errors.Is(workerErr, context.Canceled) || closed.Load() != 1 {
		t.Fatalf("shutdown: error=%v, links closed=%d", workerErr, closed.Load())
	}
}

func TestDiscoveryReportsLaterLibrariesAndDeduplicatesInodes(t *testing.T) {
	paths := discoveryFiles(t, 2)
	alias := paths[0] + ".alias.so"
	if err := os.Link(paths[0], alias); err != nil {
		t.Fatal(err)
	}
	d := newLibraryDiscovery(nil)
	var inspections, attachments int
	d.inspect = func(_ context.Context, path string) ([]library, error) {
		inspections++
		return []library{{path: path, identity: path}, {path: path, identity: path}}, nil
	}
	d.attachLibrary = func(context.Context, library) ([]link.Link, error) {
		attachments++
		return nil, nil
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
}

func TestDiscoveryDoesNotReportFailedAttachmentsAsReady(t *testing.T) {
	paths := discoveryFiles(t, 4)
	d := newLibraryDiscovery(nil)
	d.inspect = func(_ context.Context, path string) ([]library, error) {
		return []library{{path: path, identity: path}}, nil
	}
	d.attachLibrary = func(context.Context, library) ([]link.Link, error) {
		return nil, errors.New("kernel denied the probe")
	}
	if err := d.scan(context.Background(), paths, func(update discoveryUpdate) error {
		if update.libraries != 0 {
			t.Fatal("failed probe was reported as attached")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(d.attached) != 0 || len(d.failures) != 3 {
		t.Fatalf("attached=%d failures=%v", len(d.attached), d.failures)
	}
}

func TestLibraryInspectionHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := inspectLibraries(ctx, "does-not-exist.so"); !errors.Is(err, context.Canceled) {
		t.Fatalf("inspection continued after cancellation: %v", err)
	}
	if _, err := discoverPaths(ctx, collectorConfig{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("process scan continued after cancellation: %v", err)
	}
}
