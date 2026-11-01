package main

import (
	"bytes"
	"context"
	"debug/elf"
	"debug/gosym"
	"encoding/binary"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/arch/x86/x86asm"
)

const (
	goRead      = "crypto/tls.(*Conn).Read"
	goWrite     = "crypto/tls.(*Conn).Write"
	goHandshake = "crypto/tls.(*Conn).clientHandshake"
	goClose     = "crypto/tls.(*Conn).Close"
	goKeyLog    = "crypto/tls.(*Config).writeKeyLog"
)

type unsupportedGoTLSError struct{ name, reason string }

func (e *unsupportedGoTLSError) Error() string {
	return e.name + ": Go TLS capture unavailable: " + e.reason
}

func supportedGoVersion(version string) bool {
	parts := strings.Split(strings.TrimPrefix(version, "go1."), ".")
	if !strings.HasPrefix(version, "go1.") || len(parts) != 2 {
		return false
	}
	minor, err := strconv.Atoi(parts[0])
	patch, patchErr := strconv.Atoi(parts[1])
	return err == nil && patchErr == nil && patch >= 0 && minor >= 24 && minor <= 26 &&
		strconv.Itoa(minor) == parts[0] && strconv.Itoa(patch) == parts[1]
}

func resolveGoTLS(ctx context.Context, file *elf.File, info *debug.BuildInfo) (result map[string]uint64, method string, err error) {
	// gosym is a standard-library parser, but malformed tables can still panic.
	// An untrusted installed file must not take down discovery for other apps.
	defer func() {
		if recover() != nil {
			result, method, err = nil, "", errors.New("invalid Go function table")
		}
	}()
	if !supportedGoVersion(info.GoVersion) {
		return nil, "", fmt.Errorf("Go TLS ABI is unverified for %s; supported: Go 1.24-1.26", info.GoVersion)
	}
	for _, setting := range info.Settings {
		if setting.Key == "-compiler" && setting.Value != "gc" || setting.Key == "GOEXPERIMENT" && setting.Value != "" {
			return nil, "", errors.New("Go TLS compiler/experiment ABI is unverified")
		}
	}
	text := file.Section(".text")
	if text == nil || text.Size == 0 || text.Flags&elf.SHF_EXECINSTR == 0 {
		return nil, "", errors.New("Go TLS executable text is missing")
	}
	var table *gosym.Table
	for _, name := range []string{".gopclntab", ".data.rel.ro.gopclntab", ".data.rel.ro"} {
		section := file.Section(name)
		if section == nil || section.Size > 32*1024*1024 {
			continue
		}
		data, readErr := section.Data()
		if readErr != nil {
			continue
		}
		// External PIE links may fold pclntab into .data.rel.ro. Its Go 1.20+
		// header identifies pointer size and instruction quantum, not a build ID.
		magic := []byte{0xf1, 0xff, 0xff, 0xff, 0, 0, 1, 8}
		if file.Machine == elf.EM_AARCH64 {
			magic[6] = 4
		}
		for at, tries := 0, 0; at < len(data) && tries < 16; tries++ {
			index := bytes.Index(data[at:], magic)
			if index < 0 {
				break
			}
			at += index
			if len(data)-at >= 72 && binary.LittleEndian.Uint64(data[at+8:]) <= 200000 {
				candidate, parseErr := gosym.NewTable(nil, gosym.NewLineTable(data[at:], text.Addr))
				if parseErr == nil && candidate.LookupFunc(goRead) != nil {
					table = candidate
					break
				}
			}
			at += 8
		}
		if table != nil {
			break
		}
	}
	if table == nil {
		return nil, "", errors.New("Go TLS function table is missing or unsupported")
	}
	result = make(map[string]uint64)
	for _, name := range []string{goRead, goWrite, goClose, goKeyLog, goHandshake} {
		if err := ctx.Err(); err != nil {
			return nil, "", err
		}
		function := table.LookupFunc(name)
		if function == nil || function.End <= function.Entry || function.End-function.Entry > 128*1024 {
			return nil, "", fmt.Errorf("Go TLS function %s is missing or unbounded", name)
		}
		symbol := elf.Symbol{Value: function.Entry, Size: function.End - function.Entry}
		offset, ok := executableSymbolOffset(file, symbol)
		if !ok || offset == 0 || function.Entry < text.Addr || function.End-text.Addr > text.Size {
			return nil, "", fmt.Errorf("Go TLS function %s is outside executable file data", name)
		}
		result[name] = offset
		if name != goRead && name != goWrite && name != goHandshake {
			continue
		}
		code := make([]byte, int(symbol.Size))
		if _, err := text.ReadAt(code, int64(function.Entry-text.Addr)); err != nil {
			return nil, "", err
		}
		returns, err := goReturnOffsets(code, file.Machine)
		if err != nil {
			return nil, "", fmt.Errorf("%s: %w", name, err)
		}
		for i, relative := range returns {
			result[fmt.Sprintf("%s/return/%d", name, i)] = offset + relative
		}
	}
	if _, err := planGoProbes(library{symbols: result}); err != nil {
		return nil, "", err
	}
	return result, "Go TLS " + info.GoVersion + " ABIInternal / pclntab", nil
}

func goReturnOffsets(code []byte, machine elf.Machine) ([]uint64, error) {
	var result []uint64
	if machine == elf.EM_AARCH64 {
		if len(code)%4 != 0 {
			return nil, errors.New("unaligned Go ARM64 function")
		}
		for i := 0; i < len(code); i += 4 {
			if binary.LittleEndian.Uint32(code[i:]) == 0xd65f03c0 {
				result = append(result, uint64(i))
			}
		}
	} else if machine == elf.EM_X86_64 {
		for i := 0; i < len(code); {
			instruction, err := x86asm.Decode(code[i:], 64)
			if err != nil || instruction.Len == 0 || instruction.Op == 0 {
				return nil, errors.New("undecodable Go x86_64 instruction")
			}
			if instruction.Op == x86asm.RET {
				if instruction.Args[0] != nil {
					return nil, errors.New("unverified Go return ABI")
				}
				result = append(result, uint64(i))
			}
			i += instruction.Len
		}
	} else {
		return nil, errors.New("unsupported Go register ABI")
	}
	if len(result) == 0 || len(result) > 128 {
		return nil, errors.New("Go function must have 1-128 verified returns")
	}
	return result, nil
}

func planGoProbes(library library) ([]probeSpec, error) {
	var result []probeSpec
	add := func(name, program string) error {
		offset := library.symbols[name]
		if offset == 0 || offset > ^uint64(0)-library.base {
			return fmt.Errorf("missing or invalid Go probe: %s", name)
		}
		result = append(result, probeSpec{symbol: name, program: program, offset: library.base + offset})
		return nil
	}
	for _, pair := range [][2]string{{goRead, "go_read"}, {goWrite, "go_write"}, {goHandshake, "go_handshake"}} {
		var names []string
		for name := range library.symbols {
			if strings.HasPrefix(name, pair[0]+"/return/") {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		if len(names) == 0 || len(names) > 128 {
			return nil, fmt.Errorf("missing verified Go returns: %s", pair[0])
		}
		for _, name := range names {
			if err := add(name, pair[1]+"_leave"); err != nil {
				return nil, err
			}
		}
		if pair[0] != goHandshake {
			if err := add(pair[0], pair[1]+"_enter"); err != nil {
				return nil, err
			}
		}
	}
	if err := add(goKeyLog, "go_keylog"); err != nil {
		return nil, err
	}
	if err := add(goClose, "go_close"); err != nil {
		return nil, err
	}
	// Enable new generations only after every I/O, key and lifetime probe.
	if err := add(goHandshake, "go_handshake_enter"); err != nil {
		return nil, err
	}
	return result, nil
}
