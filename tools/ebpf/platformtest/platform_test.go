package platformtest

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/asm"
	"golang.org/x/sys/unix"
)

// Also run by the unprivileged Android emulator shell. This tests entry into
// the Linux syscall path, not permission to capture on a real Android device.
func TestBPFPlatform(t *testing.T) {
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
}

func checkKernelResult(t *testing.T, object io.Closer, err error) {
	t.Helper()
	if err == nil {
		if err := object.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}
	if strings.Contains(err.Error(), "not supported on android") || strings.Contains(err.Error(), "unsupported platform") {
		t.Fatalf("Android BPF was rejected before reaching the kernel: %v", err)
	}
	if errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) ||
		errors.Is(err, unix.ENOSYS) || errors.Is(err, ebpf.ErrNotSupported) {
		t.Logf("Kernel or permission limitation (not a capture support check): %v", err)
		return
	}
	t.Fatal(err)
}
