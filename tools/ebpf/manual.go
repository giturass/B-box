package main

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

type manualTLSOffsets struct {
	Path      string            `json:"path"`
	Entry     string            `json:"entry,omitempty"`
	SHA256    string            `json:"sha256"`
	Functions map[string]string `json:"functions"`
}

type manualTLSError struct{ reason string }

func (e *manualTLSError) Error() string { return "Manual TLS offsets rejected: " + e.reason }

func validateManualTLSOffsets(values []manualTLSOffsets) error {
	if len(values) > 16 {
		return errors.New("specify at most 16 manual TLS libraries")
	}
	seen := make(map[string]bool)
	for index := range values {
		v := &values[index]
		fail := func(reason string) error { return fmt.Errorf("manual TLS offsets at index %d: %s", index, reason) }
		if !filepath.IsAbs(v.Path) || len(v.Path) > 4096 || strings.ContainsAny(v.Path, "\x00\r\n") {
			return fail("path must be an absolute ELF or APK path")
		}
		if v.Entry != "" {
			parts := strings.Split(v.Entry, "/")
			if len(v.Entry) > 512 || len(parts) != 3 || parts[0] != "lib" ||
				(parts[1] != "arm64-v8a" && parts[1] != "x86_64") || len(parts[2]) <= 3 || !strings.HasSuffix(parts[2], ".so") ||
				strings.ContainsAny(v.Entry, "\x00\r\n\\") {
				return fail("APK entry must be lib/arm64-v8a/name.so or lib/x86_64/name.so")
			}
		}
		hash, err := hex.DecodeString(v.SHA256)
		if err != nil || len(hash) != 32 {
			return fail("SHA-256 must contain 64 hexadecimal digits for the ELF")
		}
		v.SHA256 = strings.ToLower(v.SHA256)
		key := v.Path + "\x00" + v.Entry
		if seen[key] {
			return fail("duplicate manual TLS library")
		}
		seen[key] = true
		offsets := make(map[uint64]bool)
		for name, text := range v.Functions {
			if !wantedSymbols[name] {
				return fail(fmt.Sprintf("unsupported manual TLS function %.128q; only the public SSL ABI is accepted", name))
			}
			offset, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(text), "0x"), 16, 64)
			if err != nil || len(text) > 18 || !strings.HasPrefix(strings.ToLower(text), "0x") || offset == 0 {
				return fail(name + " must be a nonzero hexadecimal ELF file offset (0x...)")
			}
			if offsets[offset] {
				return fail("manual TLS function offsets must be distinct")
			}
			offsets[offset] = true
			v.Functions[name] = fmt.Sprintf("%#x", offset)
		}
		for _, name := range requiredSymbols {
			if v.Functions[name] == "" {
				return fail("manual TLS offsets require " + name)
			}
		}
	}
	return nil
}

func librarySHA256(ctx context.Context, reader io.ReaderAt, size int64) (string, error) {
	if size <= 0 || size > 256*1024*1024 {
		return "", errors.New("ELF must contain between 1 byte and 256 MiB")
	}
	hash := sha256.New()
	if count, err := io.Copy(hash, io.NewSectionReader(contextReaderAt{ctx, reader}, 0, size)); err != nil {
		return "", err
	} else if count != size {
		return "", io.ErrUnexpectedEOF
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), ctx.Err()
}

func resolveManualTLS(ctx context.Context, reader io.ReaderAt, size int64, value manualTLSOffsets, machine elf.Machine) (map[string]uint64, string, error) {
	reject := func(reason string) (map[string]uint64, string, error) {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", &manualTLSError{reason}
	}
	if err := validateManualTLSOffsets([]manualTLSOffsets{value}); err != nil {
		return reject(err.Error())
	}
	file, err := elf.NewFile(contextReaderAt{ctx, reader})
	if err != nil {
		return reject(err.Error())
	}
	defer file.Close()
	if file.Class != elf.ELFCLASS64 || file.Data != elf.ELFDATA2LSB || file.Machine != machine ||
		(machine != elf.EM_AARCH64 && machine != elf.EM_X86_64) || (file.Type != elf.ET_DYN && file.Type != elf.ET_EXEC) {
		return reject("ELF does not match the collector's executable 64-bit ABI")
	}
	if _, err := buildinfo.Read(contextReaderAt{ctx, reader}); err == nil {
		return reject("Go binaries require automatic Go ABI and return-site verification; public SSL offsets cannot override it")
	}
	hash, err := librarySHA256(ctx, reader, size)
	if err != nil {
		return reject(err.Error())
	}
	if hash != strings.ToLower(value.SHA256) {
		return reject("ELF SHA-256 mismatch; actual " + hash + "; update the offsets for this exact library")
	}
	result := make(map[string]uint64)
	for name, text := range value.Functions {
		offset, _ := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(text), "0x"), 16, 64)
		width := uint64(1)
		if machine == elf.EM_AARCH64 {
			width = 4
		}
		valid := false
		if offset < uint64(size) && width <= uint64(size)-offset && (machine != elf.EM_AARCH64 || offset%4 == 0) {
			for _, section := range file.Sections {
				if offset < section.Offset || offset-section.Offset >= section.Size || section.Addr > ^uint64(0)-(offset-section.Offset) {
					continue
				}
				symbol := elf.Symbol{Value: section.Addr + offset - section.Offset, Size: width}
				if resolved, ok := executableSymbolOffset(file, symbol); ok && resolved == offset {
					for _, program := range file.Progs {
						if program.Type == elf.PT_LOAD && program.Flags&elf.PF_X != 0 &&
							offset >= program.Off && offset-program.Off < program.Filesz &&
							program.Vaddr <= ^uint64(0)-(offset-program.Off) && program.Vaddr+offset-program.Off == symbol.Value &&
							width <= program.Filesz-(offset-program.Off) {
							valid = true
							break
						}
					}
					if valid {
						break
					}
				}
			}
		}
		if !valid {
			return reject(fmt.Sprintf("%s=%#x is not an aligned executable ELF file offset", name, offset))
		}
		result[name] = offset
	}
	if _, err := planSSLProbes(library{symbols: result}); err != nil {
		return reject(err.Error())
	}
	return result, "manual SSL offsets (SHA-256 matched; ABI supplied by user)", ctx.Err()
}
