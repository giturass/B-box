package main

import (
	"debug/elf"
	"encoding/binary"
	"reflect"
	"testing"
)

func TestGoTLSVersionGateAndInstructionBoundaries(t *testing.T) {
	for _, version := range []string{"go1.22.0", "go1.22.6", "go1.23.0", "go1.23.9", "go1.24.0", "go1.25.3", "go1.26.10", "go1.27.0", "go1.27.3"} {
		if !supportedGoVersion(version) {
			t.Fatal(version)
		}
	}
	for _, version := range []string{"go1.21.9", "go1.28.0", "devel go1.27", "go1.27rc1", "go1.27.3 X:experiment", "go1.27.-1", "go1.27.+1", "go1.027.0", "go1.27", "go1.27.03"} {
		if supportedGoVersion(version) {
			t.Fatal("accepted unverified ABI", version)
		}
	}
	// 0xc3 inside MOV's immediate is not a return instruction.
	returns, err := goReturnOffsets([]byte{0xb8, 0xc3, 0, 0, 0, 0x90, 0xc3}, elf.EM_X86_64)
	if err != nil || !reflect.DeepEqual(returns, []uint64{6}) {
		t.Fatal(returns, err)
	}
	for _, code := range [][]byte{{0xc2, 8, 0}, {0x0f}, {0x90, 0x90}} {
		if _, err := goReturnOffsets(code, elf.EM_X86_64); err == nil {
			t.Fatal("accepted missing, broken or non-Go return", code)
		}
	}
	arm := make([]byte, 8)
	binary.LittleEndian.PutUint32(arm, 0xd503201f)
	binary.LittleEndian.PutUint32(arm[4:], 0xd65f03c0)
	returns, err = goReturnOffsets(arm, elf.EM_AARCH64)
	if err != nil || !reflect.DeepEqual(returns, []uint64{4}) {
		t.Fatal(returns, err)
	}
}

func TestGoProbePlanNeverUsesKernelReturnProbes(t *testing.T) {
	library := library{base: 0x4000, symbols: make(map[string]uint64)}
	for i, name := range []string{goRead, goWrite, goHandshake, goClose, goKeyLog} {
		library.symbols[name] = uint64(0x100 + i*0x100)
		if name != goClose && name != goKeyLog {
			library.symbols[name+"/return/0"] = library.symbols[name] + 0x40
		}
	}
	probes, err := planProbes(library)
	if err != nil {
		t.Fatal(err)
	}
	if probes[len(probes)-1].program != "go_handshake_enter" {
		t.Fatal("handshakes enabled before complete Go I/O probes")
	}
	for _, probe := range probes {
		if probe.ret || probe.offset != library.base+library.symbols[probe.symbol] {
			t.Fatalf("unsafe Go return/file offset: %+v", probe)
		}
	}
	for i, name := range requiredSymbols {
		library.symbols[name] = uint64(0x1000 + i*0x100)
	}
	combined, err := planProbes(library)
	if err != nil || len(combined) <= len(probes) {
		t.Fatal("Go build info hid a coexisting SSL adapter", err)
	}
	var sslNew, handshake int
	for _, probe := range combined {
		if probe.program == "ssl_new" {
			sslNew++
		}
		if probe.program == "go_handshake_enter" {
			handshake++
		}
	}
	if sslNew != 1 || handshake != 1 {
		t.Fatal("combined library omitted or duplicated a TLS adapter")
	}
	delete(library.symbols, goRead+"/return/0")
	if _, err := planProbes(library); err == nil {
		t.Fatal("missing Go return probe accepted")
	}
}
