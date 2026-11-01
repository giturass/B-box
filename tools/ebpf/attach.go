package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"unsafe"

	"github.com/cilium/ebpf"
	"golang.org/x/sys/unix"
)

// stackplz separates RealFilePath, NonElfOffset and UAddress. In particular an APK
// is the inode to instrument, not an ELF that link.OpenExecutable can open.
// We use the same PMU + ioctl attachment mechanism, without extracting a second
// copy of the library or depending on a BPF link / attach cookie (Linux 5.15).
// Reference: tools/ebpf/README.md. No debugger signals or memory writes are used.
type uprobeAttacher struct {
	eventType uint32
	retBit    uint64
}

func newUprobeAttacher() (*uprobeAttacher, error) {
	read := func(path string) (string, error) {
		data, err := os.ReadFile("/sys/bus/event_source/devices/uprobe/" + path)
		return strings.TrimSpace(string(data)), err
	}
	value, err := read("type")
	if err != nil {
		return nil, fmt.Errorf("read uprobe PMU type (CONFIG_UPROBE_EVENTS is required): %w", err)
	}
	eventType, err := strconv.ParseUint(value, 10, 32)
	if err != nil || eventType == 0 {
		return nil, errors.New("invalid uprobe PMU type")
	}
	value, err = read("format/retprobe")
	if err != nil {
		return nil, fmt.Errorf("read uretprobe PMU format: %w", err)
	}
	if !strings.HasPrefix(value, "config:") {
		return nil, errors.New("unsupported uretprobe PMU format")
	}
	bit, err := strconv.ParseUint(strings.TrimPrefix(value, "config:"), 10, 6)
	if err != nil {
		return nil, errors.New("invalid uretprobe PMU bit")
	}
	return &uprobeAttacher{uint32(eventType), uint64(1) << bit}, nil
}

func (a *uprobeAttacher) attach(path string, offset uint64, ret bool, program *ebpf.Program) (io.Closer, error) {
	if program == nil || program.FD() < 0 || program.Type() != ebpf.Kprobe {
		return nil, errors.New("missing TLS probe program")
	}
	name, err := unix.BytePtrFromString(path)
	if err != nil {
		return nil, err
	}
	attr := unix.PerfEventAttr{
		Type: a.eventType, Size: unix.PERF_ATTR_SIZE_VER1,
		Ext1: uint64(uintptr(unsafe.Pointer(name))), Ext2: offset,
	}
	if ret {
		attr.Config = a.retBit
	}
	// As in SeeFlowerX/ebpf's link/kprobe.go, a system-wide uprobe's BPF
	// program is attached to the trace event, not sampled once per CPU.
	// Registering the same program on every CPU would duplicate invocations.
	fd, err := unix.PerfEventOpen(&attr, -1, 0, -1, unix.PERF_FLAG_FD_CLOEXEC)
	runtime.KeepAlive(name)
	if err != nil {
		return nil, fmt.Errorf("perf_event_open at %#x (check Root, kernel and SELinux): %w", offset, err)
	}
	handle := os.NewFile(uintptr(fd), "tls-uprobe")
	if err = unix.IoctlSetInt(fd, unix.PERF_EVENT_IOC_SET_BPF, program.FD()); err == nil {
		err = unix.IoctlSetInt(fd, unix.PERF_EVENT_IOC_ENABLE, 0)
	}
	runtime.KeepAlive(program)
	if err != nil {
		handle.Close()
		return nil, fmt.Errorf("enable TLS uprobe at %#x: %w", offset, err)
	}
	return handle, nil
}

type probeSpec struct {
	symbol, program string
	offset          uint64
	ret             bool
}

func planProbes(library library) ([]probeSpec, error) {
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
		{"SSL_read_early_data", "ssl_read_early_enter", false, true}, {"SSL_read_early_data", "ssl_read_leave", true, true},
		{"SSL_clear", "ssl_clear_enter", false, true}, {"SSL_clear", "ssl_clear_leave", true, true},
		{"SSL_free", "ssl_free", false, false},
		// Allocations are observed only after the complete I/O/lifetime set is
		// attached. Unwinding this list also disables allocations first.
		{"SSL_new", "ssl_new", true, false},
	}
	type key struct {
		offset uint64
		ret    bool
	}
	seen := make(map[key]string)
	var probes []probeSpec
	for _, hook := range hooks {
		offset, exists := library.symbols[hook.symbol]
		if !exists || offset == 0 {
			if hook.optional {
				continue
			}
			return nil, fmt.Errorf("missing %s probe offset", hook.symbol)
		}
		if offset > ^uint64(0)-library.base {
			return nil, errors.New("TLS probe offset overflows the backing file")
		}
		location := key{library.base + offset, hook.ret}
		if previous, found := seen[location]; found {
			if previous != hook.program {
				return nil, fmt.Errorf("incompatible SSL ABIs share offset %#x", offset)
			}
			continue
		}
		seen[location] = hook.program
		probes = append(probes, probeSpec{hook.symbol, hook.program, location.offset, hook.ret})
	}
	return probes, nil
}

func closeProbes(probes []io.Closer) {
	for i := len(probes) - 1; i >= 0; i-- {
		_ = probes[i].Close()
	}
}

func (d *libraryDiscovery) attach(ctx context.Context, library library) (_ []io.Closer, err error) {
	return attachProbes(ctx, library, func(probe probeSpec) (io.Closer, error) {
		return d.attacher.attach(library.attachPath, probe.offset, probe.ret, d.collection.Programs[probe.program])
	})
}

func attachProbes(ctx context.Context, library library, open func(probeSpec) (io.Closer, error)) (_ []io.Closer, err error) {
	probes, err := planProbes(library)
	if err != nil {
		return nil, err
	}
	var attached []io.Closer
	defer func() {
		if err != nil {
			closeProbes(attached)
		}
	}()
	for _, probe := range probes {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		handle, attachErr := open(probe)
		if attachErr != nil {
			return nil, fmt.Errorf("%s: %w", probe.symbol, attachErr)
		}
		if handle == nil {
			return nil, fmt.Errorf("%s: missing probe handle", probe.symbol)
		}
		attached = append(attached, handle)
	}
	return attached, nil
}
