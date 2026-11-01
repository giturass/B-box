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
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/perf"
	"github.com/cilium/ebpf/rlimit"
	"golang.org/x/sys/unix"
)

// Built by build.sh for the same register ABI as this executable.
//
//go:embed bpf/capture.o
var program []byte

type collectorConfig struct {
	ExcludeUID uint32              `json:"excludeUid"`
	UserID     uint32              `json:"userId"`
	UIDs       []uint32            `json:"uids"`
	Packages   map[string][]string `json:"packages"`
	Libraries  []string            `json:"libraries"`
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
	if len(config.UIDs) > 256 || len(config.Libraries) > 16 || len(config.Packages) > 8192 {
		return config, errors.New("too many eBPF capture filters")
	}
	for _, path := range config.Libraries {
		if !filepath.IsAbs(path) || len(path) > 4096 || strings.ContainsAny(path, "\x00\r\n") {
			return config, errors.New("invalid TLS library path")
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
	return config, nil
}

func main() {
	debug.SetMemoryLimit(256 * 1024 * 1024)
	writer := json.NewEncoder(os.Stdout)
	if err := run(writer.Encode); err != nil {
		_ = writer.Encode(pipeEvent{Type: "error", Error: fmt.Sprintf("%.4096s", err.Error())})
		os.Exit(1)
	}
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
	_ = rlimit.RemoveMemlock()
	progress := func(stage string) error {
		return encode(pipeEvent{Type: "progress", Version: 1, Stage: stage})
	}
	if err = progress("reading the capture BPF object"); err != nil {
		return err
	}
	spec, err := ebpf.LoadCollectionSpecFromReader(bytes.NewReader(program))
	if err != nil {
		return fmt.Errorf("load capture BPF object: %w", err)
	}
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
	if err = progress("opening the eBPF perf event buffers"); err != nil {
		return err
	}
	events, err := perf.NewReader(collection.Maps["events"], 64*os.Getpagesize())
	if err != nil {
		return fmt.Errorf("open eBPF perf buffer: %w", err)
	}
	defer events.Close()
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
	discovery := newLibraryDiscovery(collection)
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
			if update.stage != "" && activeResults == nil {
				if err = progress(update.stage); err != nil {
					return err
				}
			}
			if update.libraries > 0 {
				kind := "status"
				if activeResults == nil {
					kind = "ready"
				}
				if err = emit(pipeEvent{Type: kind, Version: 1, Libraries: update.libraries}); err != nil {
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
			event, err := decodeEvent(result.record.RawSample)
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

func (d *libraryDiscovery) attach(ctx context.Context, library library) (_ []link.Link, err error) {
	executable, err := link.OpenExecutable(library.path)
	if err != nil {
		return nil, err
	}
	type hook struct {
		symbol, program string
		ret, optional   bool
	}
	hooks := []hook{
		{"SSL_write", "ssl_write_enter", false, false}, {"SSL_write", "ssl_write_leave", true, false},
		{"SSL_read", "ssl_read_enter", false, false}, {"SSL_read", "ssl_read_leave", true, false},
		{"SSL_write_ex", "ssl_write_ex_enter", false, true}, {"SSL_write_ex", "ssl_write_leave", true, true},
		{"SSL_read_ex", "ssl_read_ex_enter", false, true}, {"SSL_read_ex", "ssl_read_leave", true, true},
		{"SSL_write_early_data", "ssl_write_ex_enter", false, true}, {"SSL_write_early_data", "ssl_write_leave", true, true},
		{"SSL_read_early_data", "ssl_read_ex_enter", false, true}, {"SSL_read_early_data", "ssl_read_leave", true, true},
		{"SSL_clear", "ssl_clear", false, true}, {"SSL_free", "ssl_free", false, false},
		// Register allocations last, after every data/lifetime hook is ready.
		{"SSL_new", "ssl_new", true, false},
	}
	var attached []link.Link
	defer func() {
		if err != nil {
			for _, probe := range attached {
				probe.Close()
			}
		}
	}()
	seen := make(map[string]bool)
	for _, hook := range hooks {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		offset := library.symbols[hook.symbol]
		if offset == 0 && hook.optional {
			continue
		}
		options := &link.UprobeOptions{Address: library.base + offset}
		key := fmt.Sprintf("%d:%t", options.Address, hook.ret)
		if seen[key] {
			continue
		}
		seen[key] = true
		var probe link.Link
		if hook.ret {
			probe, err = executable.Uretprobe(hook.symbol, d.collection.Programs[hook.program], options)
		} else {
			probe, err = executable.Uprobe(hook.symbol, d.collection.Programs[hook.program], options)
		}
		if err != nil {
			return nil, fmt.Errorf("attach %s: %w", hook.symbol, err)
		}
		attached = append(attached, probe)
	}
	return attached, nil
}
func (d *libraryDiscovery) close() {
	for _, probes := range d.attached {
		for _, probe := range probes {
			probe.Close()
		}
	}
}

type connectionKey struct {
	SSL      uint64
	PID, Pad uint32
}
type connectionValue struct{ Generation, WriteSequence, ReadSequence uint64 }
type ownerKey struct {
	Connection     connectionKey
	Direction, Pad uint32
}

type callKey struct {
	Thread         uint64
	Direction, Pad uint32
}
type callValue struct {
	SSL, Buffer, Requested, CountPointer, Stack uint64
	Depth, Extended                             uint32
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
	var key connectionKey
	var value connectionValue
	iterator := collection.Maps["connections"].Iterate()
	for iterator.Next(&key, &value) {
		if exists(key.PID) {
			continue
		}
		id := rawEvent{ssl: key.SSL, pid: key.PID, generation: value.Generation}.id()
		if flow := assembler.flows[id]; flow != nil && len(flow.pending) == 0 && flow.open && flow.next[0] == value.WriteSequence+1 && flow.next[1] == value.ReadSequence+1 {
			if err := assembler.emit(pipeEvent{Type: "close", ID: id}); err != nil {
				return err
			}
			delete(assembler.flows, id)
			_ = collection.Maps["connections"].Delete(key)
		}
	}
	if err := iterator.Err(); err != nil {
		return err
	}
	var call callKey
	var state callValue
	iterator = collection.Maps["calls"].Iterate()
	for iterator.Next(&call, &state) {
		pid, tid := uint32(call.Thread>>32), uint32(call.Thread)
		_, threadErr := os.Stat(fmt.Sprintf("/proc/%d/task/%d", pid, tid))
		if !exists(pid) || errors.Is(threadErr, os.ErrNotExist) {
			_ = collection.Maps["calls"].Delete(call)
			_ = collection.Maps["owners"].Delete(ownerKey{Connection: connectionKey{SSL: state.SSL, PID: uint32(call.Thread >> 32)}, Direction: call.Direction})
		}
	}
	return iterator.Err()
}
