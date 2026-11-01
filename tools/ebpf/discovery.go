package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

type discoveryUpdate struct {
	stage     string
	libraries int
	err       error
}

// One worker owns discovery state and links. Only progress/counts cross to the
// event loop; closing the worker precedes closing the shared BPF collection.
type libraryDiscovery struct {
	collection    *ebpf.Collection
	attached      map[string][]link.Link
	seen          map[string]bool
	failures      []string
	inspect       func(context.Context, string) ([]library, error)
	attachLibrary func(context.Context, library) ([]link.Link, error)
}

func newLibraryDiscovery(collection *ebpf.Collection) *libraryDiscovery {
	d := &libraryDiscovery{
		collection: collection,
		attached:   make(map[string][]link.Link),
		seen:       make(map[string]bool),
		inspect:    inspectLibraries,
	}
	d.attachLibrary = d.attach
	return d
}

func (d *libraryDiscovery) watch(ctx context.Context, config collectorConfig, updates chan<- discoveryUpdate) error {
	report := func(update discoveryUpdate) error {
		select {
		case updates <- update:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// Explicit paths and system TLS libraries do not require a /proc sweep.
	if err := d.scan(ctx, initialLibraryPaths(config), report); err != nil {
		return err
	}
	for {
		if len(d.attached) == 0 {
			if err := report(discoveryUpdate{stage: "discovering process TLS libraries"}); err != nil {
				return err
			}
		}
		paths, err := discoverPaths(ctx, config)
		if err != nil {
			return err
		}
		if err = d.scan(ctx, paths, report); err != nil {
			return err
		}
		if len(d.attached) == 0 {
			reason := strings.Join(d.failures, "; ")
			if reason == "" {
				reason = "SSL_new/free/read/write symbols were not found. Open the target app or select its compatible TLS library."
			}
			return fmt.Errorf("no compatible TLS library could be attached: %s", reason)
		}
		timer := time.NewTimer(3 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (d *libraryDiscovery) scan(ctx context.Context, paths []string, report func(discoveryUpdate) error) error {
	stage := func(action, path string) error {
		if len(d.attached) > 0 {
			return nil
		}
		return report(discoveryUpdate{stage: fmt.Sprintf("%s %.512s", action, path)})
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(d.attached) >= 64 {
			break
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		stat := info.Sys().(*syscall.Stat_t)
		key := fmt.Sprintf("%d:%d:%d:%d", stat.Dev, stat.Ino, info.Size(), info.ModTime().UnixNano())
		if d.seen[key] {
			continue
		}
		if len(d.seen) >= 2048 {
			break
		}
		d.seen[key] = true
		if err = stage("inspecting TLS symbols in", path); err != nil {
			return err
		}
		libraries, err := d.inspect(ctx, path)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		for _, library := range libraries {
			if err = ctx.Err(); err != nil {
				return err
			}
			if len(d.attached) >= 64 {
				break
			}
			if _, exists := d.attached[library.identity]; exists {
				continue
			}
			if err = stage("attaching TLS probes to", path); err != nil {
				return err
			}
			links, err := d.attachLibrary(ctx, library)
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if len(d.failures) < 3 {
					d.failures = append(d.failures, filepath.Base(path)+": "+err.Error())
				}
				continue
			}
			d.attached[library.identity] = links
			if err = report(discoveryUpdate{libraries: len(d.attached)}); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}
