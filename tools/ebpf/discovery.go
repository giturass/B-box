package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
)

var errStopDiscovery = errors.New("TLS library attachment limit reached")

type discoveryUpdate struct {
	stage     string
	level     string
	libraries int
	warning   string
	err       error
	scanning  bool
}

// One worker owns all file inspection and perf handles. The event loop keeps
// draining plaintext while further APKs are analyzed. Shutdown waits for this
// worker before closing the BPF maps and programs.
type libraryDiscovery struct {
	collection    *ebpf.Collection
	attacher      *uprobeAttacher
	attached      map[string][]io.Closer
	seen          map[string]bool
	explicit      map[string]bool
	noted         map[string]bool
	failures      []string
	scanning      bool
	config        collectorConfig
	walk          func(context.Context, string, func(library) error) error
	attachLibrary func(context.Context, library) ([]io.Closer, error)
}

func newLibraryDiscovery(collection *ebpf.Collection, attacher *uprobeAttacher) *libraryDiscovery {
	d := &libraryDiscovery{
		collection: collection, attacher: attacher,
		attached: make(map[string][]io.Closer), seen: make(map[string]bool),
		explicit: make(map[string]bool), noted: make(map[string]bool), walk: walkLibraries,
		scanning: true,
	}
	d.attachLibrary = d.attach
	return d
}

func (d *libraryDiscovery) close() {
	for _, probes := range d.attached {
		closeProbes(probes)
	}
	clear(d.attached)
}

func (d *libraryDiscovery) watch(ctx context.Context, config collectorConfig, updates chan<- discoveryUpdate) error {
	d.config = config
	report := func(update discoveryUpdate) error {
		select {
		case updates <- update:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	for _, path := range config.Libraries {
		d.explicit[path] = true
	}
	if len(config.TLSOffsets) > 0 {
		for _, value := range config.TLSOffsets {
			d.explicit[value.Path] = true
		}
		d.walk = func(ctx context.Context, path string, visit func(library) error) error {
			return walkLibraryOptions(ctx, path, libraryOptions{offsets: config.TLSOffsets}, visit)
		}
	}
	if err := d.scan(ctx, initialLibraryPaths(config), report); err != nil {
		return err
	}
	for {
		if len(d.attached) == 0 {
			if err := report(discoveryUpdate{stage: "discovering installed application and process TLS libraries"}); err != nil {
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
				reason = "automatic SSL function resolution found no compatible library in the selected applications"
			}
			return fmt.Errorf("no TLS probes attached: %s", reason)
		}
		if d.scanning {
			d.scanning = false
			if err := report(discoveryUpdate{libraries: len(d.attached), warning: strings.Join(d.failures, "; ")}); err != nil {
				return err
			}
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

func (d *libraryDiscovery) warn(message string) bool {
	message = fmt.Sprintf("%.1024s", message)
	for _, previous := range d.failures {
		if previous == message {
			return false
		}
	}
	if len(d.failures) < 3 {
		d.failures = append(d.failures, message)
		return true
	}
	return false
}

func (d *libraryDiscovery) scan(ctx context.Context, paths []string, report func(discoveryUpdate) error) error {
	source := func(path string) string { return fmt.Sprintf("%s path=%.2048q", d.config.libraryPackages(path), path) }
	stage := func(action, path string) error {
		return report(discoveryUpdate{stage: fmt.Sprintf("%s %s", action, source(path)), level: "debug"})
	}
	log := func(level, message string) error { return report(discoveryUpdate{stage: message, level: level}) }
	status := func() error {
		return report(discoveryUpdate{libraries: len(d.attached), warning: strings.Join(d.failures, "; "), scanning: d.scanning})
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(d.attached) >= 64 {
			if !d.noted["library-limit"] {
				d.noted["library-limit"] = true
				d.warn("TLS discovery reached its 64-library limit; select fewer applications")
				if err := log("warn", "TLS discovery reached its 64-library limit; select fewer applications"); err != nil {
					return err
				}
				if err := status(); err != nil {
					return err
				}
			}
			break
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			if d.explicit[path] && !d.noted["unavailable:"+path] {
				d.noted["unavailable:"+path] = true
				d.warn(source(path) + ": library is unavailable")
				if err == nil {
					err = errors.New("not a regular file")
				}
				if err := report(discoveryUpdate{stage: fmt.Sprintf("TLS library unavailable: %s: %v", source(path), err), level: "warn",
					libraries: len(d.attached), warning: strings.Join(d.failures, "; "), scanning: d.scanning}); err != nil {
					return err
				}
			}
			continue
		}
		delete(d.noted, "unavailable:"+path)
		stat := info.Sys().(*syscall.Stat_t)
		key := fmt.Sprintf("%d:%d:%d:%d", stat.Dev, stat.Ino, info.Size(), info.ModTime().UnixNano())
		if d.seen[key] {
			continue
		}
		if len(d.seen) >= 2048 {
			d.warn("TLS discovery reached its 2048-file limit; select fewer applications")
			if !d.noted["file-limit"] {
				d.noted["file-limit"] = true
				if err := log("warn", "TLS discovery reached its 2048-file limit; select fewer applications"); err != nil {
					return err
				}
			}
			if len(d.attached) > 0 {
				return status()
			}
			break
		}
		d.seen[key] = true
		if !d.scanning {
			d.scanning = true
			if len(d.attached) > 0 {
				if err := status(); err != nil {
					return err
				}
			}
		}
		if err = stage("resolving TLS function offsets in", path); err != nil {
			return err
		}
		before := strings.Join(d.failures, "; ")
		resolved := false
		err = d.walk(ctx, path, func(value library) error {
			resolved = true
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, exists := d.attached[value.identity]; exists {
				return nil
			}
			if len(d.attached) >= 64 {
				return errStopDiscovery
			}
			if err := log("debug", fmt.Sprintf("attaching TLS probes to %s entry=%.512q", source(value.path), value.entry)); err != nil {
				return err
			}
			probes, err := d.attachLibrary(ctx, value)
			if err != nil || len(probes) == 0 {
				closeProbes(probes)
				if err == nil {
					err = errors.New("no probes attached")
				}
				d.warn(fmt.Sprintf("%s entry=%.512q: attach failed: %v", source(value.path), value.entry, err))
				if logErr := log("warn", fmt.Sprintf("TLS probe attachment failed: %s entry=%.512q: %v", source(value.path), value.entry, err)); logErr != nil {
					return logErr
				}
				return ctx.Err()
			}
			d.attached[value.identity] = probes
			return report(discoveryUpdate{stage: d.config.libraryPackages(value.path) + " " + describeAttachedLibrary(value, len(probes)), level: "info",
				libraries: len(d.attached), warning: strings.Join(d.failures, "; "), scanning: d.scanning})
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A package update may replace the pathname between Stat and Open.
		// Do not mark the earlier inode as inspected when a different file won.
		if after, statErr := os.Stat(path); statErr != nil || !os.SameFile(info, after) ||
			after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
			delete(d.seen, key)
		}
		var visitErr *libraryVisitError
		if errors.As(err, &visitErr) && !errors.Is(err, errStopDiscovery) {
			return visitErr.err
		}
		if err != nil && !errors.Is(err, errStopDiscovery) {
			diagnostics := []error{err}
			if joined, ok := err.(interface{ Unwrap() []error }); ok {
				diagnostics = joined.Unwrap()
			}
			for _, diagnostic := range diagnostics {
				var unsupported *unsupportedChromiumError
				var unsupportedGo *unsupportedGoTLSError
				var manual *manualTLSError
				level := "debug"
				if d.explicit[path] || errors.As(diagnostic, &unsupported) || errors.As(diagnostic, &unsupportedGo) || errors.As(diagnostic, &manual) {
					d.warn(source(path) + ": " + diagnostic.Error())
					level = "warn"
				}
				if logErr := log(level, fmt.Sprintf("TLS library inspection failed: %s: %v", source(path), diagnostic)); logErr != nil {
					return logErr
				}
			}
		} else if !resolved {
			level := "debug"
			if d.explicit[path] {
				d.warn(source(path) + ": no compatible TLS library found")
				level = "warn"
			}
			if err := log(level, fmt.Sprintf("No compatible TLS functions found in %s", source(path))); err != nil {
				return err
			}
		}
		if len(d.attached) > 0 && strings.Join(d.failures, "; ") != before {
			if err = status(); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

func describeAttachedLibrary(value library, probes int) string {
	fingerprint := ""
	if value.sha256 != "" {
		fingerprint = " sha256=" + value.sha256
	}
	names := make([]string, 0, len(value.symbols))
	for name := range value.symbols {
		names = append(names, name)
	}
	sort.Strings(names)
	offsets := make([]string, 0, len(names))
	for _, name := range names {
		offsets = append(offsets, fmt.Sprintf("%s=%#x", name, value.base+value.symbols[name]))
	}
	return fmt.Sprintf("TLS probes attached: path=%.2048q entry=%.512q method=%s identity=%s%s archiveOffset=%#x probes=%d; file offsets: %s",
		value.path, value.entry, value.method, value.identity, fingerprint, value.base, probes, strings.Join(offsets, ", "))
}
