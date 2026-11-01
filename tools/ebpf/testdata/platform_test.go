// Copied into the patched cilium/ebpf module by prepare.py for CI regression tests.
package bboxtest

import (
	"errors"
	"io"
	"testing"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"github.com/cilium/ebpf/btf"
	"github.com/cilium/ebpf/internal"
	"github.com/cilium/ebpf/internal/platform"
	"golang.org/x/sys/unix"
)

// Runs on Linux and on Android as the unprivileged emulator shell. Kernel or
// permission failures are allowed here; an OS rejection before the syscall is
// not. Real privileged loading and attachment remain covered by the TLS smoke.
func TestBPFPlatform(t *testing.T) {
	t.Run("kernel_versions", func(t *testing.T) {
		for _, versions := range [][]string{{"3.19"}, {"windows:0.1", "linux:3.19"}} {
			version, err := platform.SelectVersion(versions)
			if err != nil || version != "3.19" {
				t.Fatalf("Linux kernel version %v: got %q, %v", versions, version, err)
			}
		}
	})
	t.Run("map", func(t *testing.T) {
		m, err := ebpf.NewMap(&ebpf.MapSpec{
			Type: ebpf.PerfEventArray, KeySize: 4, ValueSize: 4, MaxEntries: 1,
		})
		checkKernelResult(t, m, err)
	})
	t.Run("program", func(t *testing.T) {
		p, err := ebpf.NewProgram(&ebpf.ProgramSpec{
			Type: ebpf.Kprobe, License: "GPL",
			Instructions: asm.Instructions{asm.Mov.Imm(asm.R0, 0), asm.Return()},
		})
		checkKernelResult(t, p, err)
	})
	t.Run("btf", func(t *testing.T) {
		var builder btf.Builder
		if _, err := builder.Add(&btf.Int{Name: "uint32", Size: 4}); err != nil {
			t.Fatal(err)
		}
		handle, err := btf.NewHandle(&builder)
		checkKernelResult(t, handle, err)
	})
}

func checkKernelResult(t *testing.T, object io.Closer, err error) {
	t.Helper()
	if err == nil {
		if err := object.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	if errors.Is(err, internal.ErrNotSupportedOnOS) {
		t.Fatalf("Linux BPF rejected before reaching the kernel: %v", err)
	}
	if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) ||
		errors.Is(err, unix.ENOSYS) || errors.Is(err, ebpf.ErrNotSupported) {
		t.Logf("Kernel or permission limitation (not a capture support check): %v", err)
		return
	}
	t.Fatal(err)
}
