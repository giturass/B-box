package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/perf"
	"golang.org/x/sys/unix"
)

// Built by build.sh for the same register ABI as this executable.
//
//go:embed bpf/capture.o
var program []byte

type collectorConfig struct {
	ExcludeUID     uint32              `json:"excludeUid"`
	UserID         uint32              `json:"userId"`
	UIDs           []uint32            `json:"uids"`
	Packages       map[string][]string `json:"packages"`
	Libraries      []string            `json:"libraries"`
	Targets        []string            `json:"targets"`
	TargetPackages map[string][]string `json:"targetPackages"`
	TLSOffsets     []manualTLSOffsets  `json:"tlsOffsets"`
}

func readConfig(reader *bufio.Reader) (collectorConfig, error) {
	var config collectorConfig
	line, err := reader.ReadSlice('\n')
	if err != nil {
		return config, errors.New("missing or oversized eBPF startup settings")
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&config); err != nil {
		return config, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return config, errors.New("unexpected data after eBPF startup settings")
	}
	if len(config.UIDs) > 256 || len(config.Libraries) > 16 || len(config.Packages) > 8192 || len(config.Targets)+len(config.TargetPackages) > 4096 {
		return config, errors.New("too many eBPF capture filters")
	}
	for _, group := range []struct {
		name  string
		paths []string
	}{
		{"TLS library", config.Libraries},
		{"installed capture target", config.Targets},
	} {
		for index, path := range group.paths {
			if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
				return config, fmt.Errorf("invalid %s path at index %d: %.256q", group.name, index, path)
			}
		}
	}
	for _, names := range config.Packages {
		if len(names) > 256 {
			return config, errors.New("too many packages sharing a UID")
		}
		for _, name := range names {
			if len(name) > 255 {
				return config, errors.New("invalid package name")
			}
		}
	}
	for path, names := range config.TargetPackages {
		if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") || len(names) > 256 {
			return config, errors.New("invalid installed capture target ownership")
		}
		for _, name := range names {
			if name == "" || len(name) > 255 || strings.ContainsAny(name, "\x00\r\n") {
				return config, errors.New("invalid capture target package name")
			}
		}
	}
	if err := validateManualTLSOffsets(config.TLSOffsets); err != nil {
		return config, err
	}
	return config, nil
}

func main() {
	debug.SetMemoryLimit(256 * 1024 * 1024)
	writer := json.NewEncoder(os.Stdout)
	var err error
	if len(os.Args) > 1 {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		err = inspectCommand(ctx, os.Args[1:], writer.Encode)
	} else {
		err = run(writer.Encode)
	}
	if err != nil {
		_ = writer.Encode(pipeEvent{Type: "error", Error: fmt.Sprintf("%.4096s", err.Error())})
		os.Exit(1)
	}
}

// Read-only diagnostics use exactly the production library resolver. This can
// inspect an installed APK without Root, loading BPF, or observing a process.
func inspectCommand(ctx context.Context, args []string, encode func(any) error) error {
	if (len(args) != 2 && len(args) != 4) || args[0] != "--inspect-library" || len(args) == 4 && args[2] != "--offsets" {
		return errors.New("usage: --inspect-library <ELF or APK path> [--offsets <JSON array file>]")
	}
	var options libraryOptions
	if len(args) == 4 {
		file, err := os.Open(args[3])
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
		file.Close()
		if err != nil {
			return err
		}
		data = bytes.TrimSpace(data)
		if len(data) == 0 || len(data) > 64*1024 || data[0] != '[' {
			return errors.New("manual offsets must be a JSON array of at most 64 KiB")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&options.offsets); err != nil {
			return err
		}
		if decoder.Decode(new(any)) != io.EOF {
			return errors.New("unexpected data after manual TLS offsets")
		}
	}
	options.describe = func(library library, failure error) error {
		reason := ""
		if failure != nil {
			reason = failure.Error()
		}
		return encode(struct {
			Path          string            `json:"path"`
			Entry         string            `json:"entry"`
			Method        string            `json:"method"`
			SHA256        string            `json:"sha256"`
			ArchiveOffset uint64            `json:"archiveOffset"`
			Symbols       map[string]uint64 `json:"symbols"`
			Error         string            `json:"error,omitempty"`
		}{library.path, library.entry, library.method, library.sha256, library.base, library.symbols, reason})
	}
	count := 0
	err := walkLibraryOptions(ctx, args[1], options, func(library) error { count++; return nil })
	if err == nil && count == 0 {
		err = errors.New("no compatible TLS library found")
	}
	return err
}

func run(encode func(any) error) error {
	if os.Geteuid() != 0 {
		return errors.New("HTTPS capture requires Root")
	}
	var uname unix.Utsname
	if err := unix.Uname(&uname); err != nil {
		return err
	}
	version := strings.Split(strings.TrimRight(string(uname.Release[:]), "\x00"), ".")
	if len(version) < 2 {
		return errors.New("unable to determine kernel version")
	}
	major, _ := strconv.Atoi(version[0])
	minor, _ := strconv.Atoi(version[1])
	if major < 5 || major == 5 && minor < 10 {
		return errors.New("eBPF capture requires Linux 5.10 or newer with uprobes, perf events and BPF enabled")
	}
	reader := bufio.NewReaderSize(os.Stdin, 1024*1024)
	config, err := readConfig(reader)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// The owner keeps stdin open; process death closes it and removes all probes.
	go func() { _, _ = io.Copy(io.Discard, reader); cancel() }()
	progress := func(stage string) error {
		return encode(diagnosticEvent("info", stage, true))
	}
	if err = progress(fmt.Sprintf("collector environment: kernel=%s ABI=%s PID=%d; Android user=%d excluded UID=%d; selected UIDs=%v; installed targets=%d additional libraries=%d",
		strings.TrimRight(string(uname.Release[:]), "\x00"), runtime.GOARCH, os.Getpid(), config.UserID, config.ExcludeUID, config.UIDs, len(config.Targets), len(config.Libraries))); err != nil {
		return err
	}
	if err = progress("preparing the eBPF memory limit"); err != nil {
		return err
	}
	// rlimit's package initializer probes BPF before run can check Root. Android
	// app seccomp can kill that probe with SIGSYS. Raise only this collector's
	// limit here instead; kernels with memcg accounting need no limit change.
	// Best effort: the actual BPF load below still reports allocation failures.
	limit := unix.Rlimit{Cur: unix.RLIM_INFINITY, Max: unix.RLIM_INFINITY}
	if limitErr := unix.Prlimit(0, unix.RLIMIT_MEMLOCK, &limit, nil); limitErr != nil {
		if err = encode(diagnosticEvent("warn", fmt.Sprintf("unable to raise RLIMIT_MEMLOCK; continuing with the actual BPF load: %v", limitErr), true)); err != nil {
			return err
		}
	}
	if err = progress("reading the capture BPF object"); err != nil {
		return err
	}
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(program))
	if err != nil {
		return fmt.Errorf("load capture BPF object: %w", err)
	}
	socketSpec := spec.Copy()
	for name := range socketSpec.Programs {
		if name != "socket_enter" {
			delete(socketSpec.Programs, name)
		}
	}
	delete(spec.Programs, "socket_enter")
	if err = progress("loading BPF programs and maps into the kernel"); err != nil {
		return err
	}
	collection, err := ebpf.NewCollection(spec)
	if err != nil {
		return fmt.Errorf("load eBPF probes (check kernel and SELinux permission): %w", err)
	}
	defer collection.Close()
	if err = progress("configuring capture UID filters"); err != nil {
		return err
	}
	zero, one := uint32(0), uint32(1)
	if err = collection.Maps["config"].Put(zero, config.ExcludeUID); err != nil {
		return err
	}
	filtered := uint32(0)
	if len(config.UIDs) > 0 {
		filtered = 1
	}
	if err = collection.Maps["config"].Put(one, filtered); err != nil {
		return err
	}
	if err = collection.Maps["config"].Put(uint32(2), config.UserID); err != nil {
		return err
	}
	for _, uid := range config.UIDs {
		if err = collection.Maps["uids"].Put(uid, uint8(1)); err != nil {
			return err
		}
	}
	if err = progress(fmt.Sprintf("opening the eBPF perf event buffers: %d bytes per CPU", 64*os.Getpagesize())); err != nil {
		return err
	}
	events, err := perf.NewReader(collection.Maps["events"], 64*os.Getpagesize())
	if err != nil {
		return fmt.Errorf("open eBPF perf buffer: %w", err)
	}
	defer events.Close()
	// Kernel BTF is optional. The ordinary SSL/Go probes above never depend
	// on guessed task/file/socket offsets or this device's CO-RE support.
	socketCollection, socketErr := ebpf.NewCollectionWithOptions(socketSpec, ebpf.CollectionOptions{MapReplacements: collection.Maps})
	if socketErr == nil {
		defer socketCollection.Close()
		socketLink, attachErr := link.AttachRawTracepoint(link.RawTracepointOptions{Name: "sys_enter", Program: socketCollection.Programs["socket_enter"]})
		socketErr = attachErr
		if socketErr == nil {
			defer socketLink.Close()
			if err = progress("socket correlation attached using kernel BTF and TLS syscall context"); err != nil {
				return err
			}
		}
	}
	if socketErr != nil {
		if err = encode(diagnosticEvent("warn", fmt.Sprintf("socket correlation unavailable; endpoints remain unknown: %v", socketErr), true)); err != nil {
			return err
		}
	}
	type readResult struct {
		record perf.Record
		err    error
	}
	results := make(chan readResult)
	go func() {
		for {
			record, err := events.Read()
			select {
			case results <- readResult{record, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	go func() { <-ctx.Done(); events.Close() }()
	emit := func(event pipeEvent) error { return encode(event) }
	assembler := newAssembler(config, emit)

	// Discovery owns its links and can inspect slow APK/debug-data files without
	// blocking perf reads or delaying ready after the first successful attach.
	attacher, err := newUprobeAttacher()
	if err != nil {
		return err
	}
	discovery := newLibraryDiscovery(collection, attacher)
	updates := make(chan discoveryUpdate)
	discoveryDone := make(chan struct{})
	go func() {
		defer close(discoveryDone)
		defer discovery.close()
		if err := discovery.watch(ctx, config, updates); err != nil && ctx.Err() == nil {
			select {
			case updates <- discoveryUpdate{err: err}:
			case <-ctx.Done():
			}
		}
	}()
	defer func() {
		cancel()
		// Never close programs/maps while discovery is still attaching probes.
		<-discoveryDone
	}()
	var activeResults <-chan readResult
	cleanup := time.NewTicker(3 * time.Second)
	defer cleanup.Stop()
	health := time.NewTicker(250 * time.Millisecond)
	defer health.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case update := <-updates:
			if update.err != nil {
				return update.err
			}
			if update.stage != "" {
				level := update.level
				if level == "" {
					level = "debug"
				}
				if err = emit(diagnosticEvent(level, update.stage, activeResults == nil)); err != nil {
					return err
				}
			}
			if update.libraries > 0 {
				kind := "status"
				if activeResults == nil {
					kind = "ready"
				}
				if err = emit(pipeEvent{Type: kind, Version: 1, Libraries: update.libraries, Scanning: update.scanning, Error: update.warning}); err != nil {
					return err
				}
				// A probe may already have produced data. Keep it buffered until
				// ready has been written as the first capture event.
				activeResults = results
			}
		case result := <-activeResults:
			if result.err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return fmt.Errorf("read eBPF events: %w", result.err)
			}
			if result.record.LostSamples > 0 {
				return fmt.Errorf("eBPF lost %d events; capture is incomplete", result.record.LostSamples)
			}
			event, err := decodePerfRecord(result.record)
			if err != nil {
				return err
			}
			if err = assembler.accept(event); err != nil {
				return err
			}
		case now := <-health.C:
			if activeResults == nil {
				continue
			}
			var losses []uint64
			if err = collection.Maps["lost"].Lookup(zero, &losses); err != nil {
				return err
			}
			for _, lost := range losses {
				if lost > 0 {
					return errors.New("eBPF event order or data was lost; capture is incomplete")
				}
			}
			if err = assembler.checkLoss(now); err != nil {
				return err
			}
		case <-cleanup.C:
			if activeResults == nil {
				continue
			}
			if err = cleanupExited(collection, assembler); err != nil {
				return err
			}
		}
	}
}

type connectionKey struct {
	SSL      uint64
	PID, Pad uint32
}
type connectionValue struct {
	Generation, WriteSequence, ReadSequence uint64
	Adapter, Pad                            uint32
}
type ownerKey struct {
	SSL, Generation uint64
	PID, Direction  uint32
}
type sessionKey struct {
	SSL, Generation uint64
	PID, Pad        uint32
}

type callKey struct {
	Thread         uint64
	Direction, Pad uint32
}
type callValue struct {
	SSL, Generation, Buffer, Requested, CountPointer, Stack uint64
	Depth, Extended                                         uint32
}
type goCallKey struct {
	G              uint64
	PID, Direction uint32
}
type goCallValue struct {
	Call                callValue
	StackLow, StackHigh uint64
}
type goHandshakeValue struct{ SSL, Generation, Stack, StackLow, StackHigh uint64 }
type socketIdentity struct {
	Inode, Socket   uint64
	NetNS, Conflict uint32
}

func cleanupExited(collection *ebpf.Collection, assembler *eventAssembler) error {
	alive := make(map[uint32]bool)
	exists := func(pid uint32) bool {
		if value, ok := alive[pid]; ok {
			return value
		}
		_, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
		alive[pid] = !errors.Is(err, os.ErrNotExist)
		return alive[pid]
	}
	var call callKey
	var state callValue
	iterator := collection.Maps["calls"].Iterate()
	for iterator.Next(&call, &state) {
		pid, tid := uint32(call.Thread>>32), uint32(call.Thread)
		_, threadErr := os.Stat(fmt.Sprintf("/proc/%d/task/%d", pid, tid))
		if !exists(pid) || errors.Is(threadErr, os.ErrNotExist) {
			// A thread dying inside SSL I/O has no return count. Poison the
			// generation before releasing its owner; subsequent calls cannot
			// silently resume a stream whose bytes may be missing.
			var connection connectionValue
			err := collection.Maps["connections"].Lookup(connectionKey{SSL: state.SSL, PID: pid}, &connection)
			if err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
				return err
			}
			if err == nil && connection.Generation == state.Generation {
				session := sessionKey{SSL: state.SSL, Generation: state.Generation, PID: pid}
				if err := collection.Maps["broken"].Update(session, uint32(4), ebpf.UpdateNoExist); err != nil && !errors.Is(err, ebpf.ErrKeyExist) {
					return fmt.Errorf("retire interrupted TLS call: %w", err)
				}
				if err := assembler.accept(rawEvent{ssl: state.SSL, generation: state.Generation, pid: pid, kind: 5, flags: 4}); err != nil {
					return err
				}
			}
			_ = collection.Maps["calls"].Delete(call)
			_ = collection.Maps["owners"].Delete(ownerKey{SSL: state.SSL, Generation: state.Generation, PID: uint32(call.Thread >> 32), Direction: call.Direction})
		}
	}
	if err := iterator.Err(); err != nil {
		return err
	}
	var key connectionKey
	var value connectionValue
	var goKey goCallKey
	var goCall goCallValue
	iterator = collection.Maps["go_calls"].Iterate()
	for iterator.Next(&goKey, &goCall) {
		err := collection.Maps["connections"].Lookup(connectionKey{SSL: goCall.Call.SSL, PID: goKey.PID}, &value)
		if err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
			return err
		}
		if !exists(goKey.PID) {
			if err == nil && value.Generation == goCall.Call.Generation {
				if err := assembler.accept(rawEvent{ssl: goCall.Call.SSL, generation: goCall.Call.Generation, pid: goKey.PID, kind: 5, flags: 4}); err != nil {
					return err
				}
			}
		} else if goCall.Call.Generation == 0 || err == nil && value.Generation == goCall.Call.Generation {
			continue
		}
		_ = collection.Maps["go_calls"].Delete(goKey)
		_ = collection.Maps["owners"].Delete(ownerKey{SSL: goCall.Call.SSL, Generation: goCall.Call.Generation, PID: goKey.PID, Direction: goKey.Direction})
	}
	if err := iterator.Err(); err != nil {
		return err
	}
	var handshake goHandshakeValue
	iterator = collection.Maps["go_handshakes"].Iterate()
	for iterator.Next(&goKey, &handshake) {
		err := collection.Maps["connections"].Lookup(connectionKey{SSL: handshake.SSL, PID: goKey.PID}, &value)
		if err != nil && !errors.Is(err, ebpf.ErrKeyNotExist) {
			return err
		}
		if !exists(goKey.PID) || err != nil || value.Generation != handshake.Generation {
			_ = collection.Maps["go_handshakes"].Delete(goKey)
			_ = collection.Maps["owners"].Delete(ownerKey{SSL: handshake.SSL, Generation: handshake.Generation, PID: goKey.PID})
		}
	}
	if err := iterator.Err(); err != nil {
		return err
	}
	iterator = collection.Maps["connections"].Iterate()
	for iterator.Next(&key, &value) {
		if exists(key.PID) {
			continue
		}
		id := rawEvent{ssl: key.SSL, pid: key.PID, generation: value.Generation}.id()
		if flow := assembler.flows[id]; flow != nil && len(flow.pending) == 0 && flow.open &&
			(flow.broken != nil || flow.next[0] == value.WriteSequence+1 && flow.next[1] == value.ReadSequence+1) {
			if err := assembler.closeFlow(id, key.PID, 0); err != nil {
				return err
			}
			_ = collection.Maps["connections"].Delete(key)
		}
	}
	if err := iterator.Err(); err != nil {
		return err
	}
	// SSL_free may race the interrupted-call update above. Retain failure
	// markers only while the corresponding generation still exists.
	var session sessionKey
	var socket socketIdentity
	iterator = collection.Maps["socket_seen"].Iterate()
	for iterator.Next(&session, &socket) {
		err := collection.Maps["connections"].Lookup(connectionKey{SSL: session.SSL, PID: session.PID}, &value)
		if errors.Is(err, ebpf.ErrKeyNotExist) || err == nil && value.Generation != session.Generation {
			_ = collection.Maps["socket_seen"].Delete(session)
		} else if err != nil {
			return err
		}
	}
	if err := iterator.Err(); err != nil {
		return err
	}
	var reason uint32
	iterator = collection.Maps["broken"].Iterate()
	for iterator.Next(&session, &reason) {
		err := collection.Maps["connections"].Lookup(connectionKey{SSL: session.SSL, PID: session.PID}, &value)
		if errors.Is(err, ebpf.ErrKeyNotExist) || err == nil && value.Generation != session.Generation {
			_ = collection.Maps["broken"].Delete(session)
		} else if err != nil {
			return err
		}
	}
	if err := iterator.Err(); err != nil {
		return err
	}
	var thread, ssl uint64
	iterator = collection.Maps["clears"].Iterate()
	for iterator.Next(&thread, &ssl) {
		pid, tid := uint32(thread>>32), uint32(thread)
		_, err := os.Stat(fmt.Sprintf("/proc/%d/task/%d", pid, tid))
		if !exists(pid) || errors.Is(err, os.ErrNotExist) {
			_ = collection.Maps["clears"].Delete(thread)
		}
	}
	return iterator.Err()
}
