package main

import (
	"archive/zip"
	"bytes"
	"context"
	"debug/buildinfo"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/ulikunitz/xz"
)

// attachPath is a pinned /proc/self/fd path, valid only during walkLibraries'
// visitor. The kernel pins this inode when opening the perf event. Resolving a
// pathname again after analysis could instrument an updated, different library.
type library struct {
	path, entry, identity, attachPath, method string
	sha256                                    string
	size                                      int64
	base                                      uint64
	symbols                                   map[string]uint64
}

type libraryVisitError struct{ err error }

func (e *libraryVisitError) Error() string { return e.err.Error() }
func (e *libraryVisitError) Unwrap() error { return e.err }

var wantedSymbols = map[string]bool{
	"SSL_new": true, "SSL_free": true, "SSL_clear": true,
	"SSL_read": true, "SSL_write": true, "SSL_read_ex": true, "SSL_write_ex": true,
	"SSL_read_early_data": true, "SSL_write_early_data": true,
	"SSL_connect": true, "SSL_do_handshake": true,
}

var requiredSymbols = []string{"SSL_new", "SSL_free", "SSL_read", "SSL_write"}

func executableOffset(file *elf.File, value uint64) (uint64, bool) {
	for _, program := range file.Progs {
		if program.Type == elf.PT_LOAD && program.Flags&elf.PF_X != 0 &&
			value >= program.Vaddr && value-program.Vaddr < program.Filesz &&
			program.Off <= ^uint64(0)-(value-program.Vaddr) {
			return program.Off + value - program.Vaddr, true
		}
	}
	return 0, false
}

// Validate against the original ELF, even for mini-debugdata symbols whose
// section indices belong to the separate debug file. Executable PT_LOAD alone
// is insufficient: Android's --no-rosegment may put read-only data in it too.
func executableSymbolOffset(file *elf.File, symbol elf.Symbol) (uint64, bool) {
	offset, ok := executableOffset(file, symbol.Value)
	if !ok {
		return 0, false
	}
	for _, section := range file.Sections {
		if section.Type != elf.SHT_PROGBITS || section.Flags&(elf.SHF_ALLOC|elf.SHF_EXECINSTR) != elf.SHF_ALLOC|elf.SHF_EXECINSTR ||
			symbol.Value < section.Addr || symbol.Value-section.Addr >= section.Size {
			continue
		}
		delta := symbol.Value - section.Addr
		if section.Offset <= ^uint64(0)-delta && section.Offset+delta == offset && symbol.Size <= section.Size-delta {
			return offset, true
		}
	}
	return 0, false
}

func readSymbols(ctx context.Context, reader io.ReaderAt, name string) (map[string]uint64, error) {
	symbols, _, err := resolveSymbols(ctx, reader, name)
	return symbols, err
}

func resolveSymbols(ctx context.Context, reader io.ReaderAt, name string) (map[string]uint64, string, error) {
	return resolveSymbolsForMachine(ctx, reader, name, collectorMachine())
}

func collectorMachine() elf.Machine {
	machine := elf.EM_AARCH64
	if runtime.GOARCH == "amd64" {
		machine = elf.EM_X86_64
	}
	return machine
}

func resolveSymbolsForMachine(ctx context.Context, reader io.ReaderAt, name string, machine elf.Machine) (map[string]uint64, string, error) {
	file, err := elf.NewFile(contextReaderAt{ctx, reader})
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	if file.Class != elf.ELFCLASS64 || file.Data != elf.ELFDATA2LSB || file.Machine != machine ||
		(file.Type != elf.ET_DYN && file.Type != elf.ET_EXEC) {
		return nil, "", errors.New("TLS library does not match the collector's executable 64-bit ABI")
	}
	sslSymbols, sslMethod, sslErr := resolveSSLSymbols(ctx, file, name, machine)
	if info, err := buildinfo.Read(contextReaderAt{ctx, reader}); err == nil {
		goSymbols, goMethod, goErr := resolveGoTLS(ctx, file, info)
		if goErr != nil {
			if sslErr == nil {
				return sslSymbols, sslMethod + "; Go TLS unavailable: " + goErr.Error(), nil
			}
			return nil, "", &unsupportedGoTLSError{filepath.Base(name), goErr.Error()}
		}
		if sslErr == nil {
			// A c-shared executable can expose both Go TLS and public SSL
			// interfaces. Its build-info must not hide the SSL adapter.
			for name, offset := range sslSymbols {
				goSymbols[name] = offset
			}
			goMethod += " + " + sslMethod
		}
		return goSymbols, goMethod, nil
	}
	return sslSymbols, sslMethod, sslErr
}

func resolveSSLSymbols(ctx context.Context, file *elf.File, name string, machine elf.Machine) (map[string]uint64, string, error) {
	result := make(map[string]uint64)
	collect := func(symbols []elf.Symbol) error {
		for _, symbol := range symbols {
			if !wantedSymbols[symbol.Name] || symbol.Section == elf.SHN_UNDEF || elf.ST_TYPE(symbol.Info) != elf.STT_FUNC {
				continue
			}
			offset, ok := executableSymbolOffset(file, symbol)
			if !ok || offset == 0 || machine == elf.EM_AARCH64 && offset%4 != 0 {
				return fmt.Errorf("SSL symbol %s is outside executable file data", symbol.Name)
			}
			if prior, exists := result[symbol.Name]; exists && prior != offset {
				return fmt.Errorf("conflicting %s symbols", symbol.Name)
			}
			result[symbol.Name] = offset
		}
		return nil
	}
	for _, read := range []func() ([]elf.Symbol, error){file.DynamicSymbols, file.Symbols} {
		if symbols, err := read(); err == nil {
			if err = collect(symbols); err != nil {
				return nil, "", err
			}
		}
	}
	complete := func() bool {
		for _, name := range requiredSymbols {
			if result[name] == 0 {
				return false
			}
		}
		return true
	}
	method := "ELF symbols"
	// Conscrypt can store its local SSL symbols in bounded XZ mini-debugdata.
	if !complete() {
		if section := file.Section(".gnu_debugdata"); section != nil && section.Size <= 16*1024*1024 {
			decoder, err := (xz.ReaderConfig{DictCap: 16 * 1024 * 1024, SingleStream: true}).NewReader(section.Open())
			if err == nil {
				data, readErr := io.ReadAll(io.LimitReader(decoder, 32*1024*1024+1))
				if readErr == nil && len(data) <= 32*1024*1024 {
					if debug, err := elf.NewFile(bytes.NewReader(data)); err == nil {
						if debug.Class == file.Class && debug.Machine == file.Machine && debug.Data == file.Data {
							if symbols, err := debug.Symbols(); err == nil {
								err = collect(symbols)
								debug.Close()
								if err != nil {
									return nil, "", err
								}
								method = "ELF mini-debugdata"
							} else {
								debug.Close()
							}
						} else {
							debug.Close()
						}
					}
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if !complete() {
		// Detect embedded Chromium/BoringSSL by contents, not an application,
		// version or library-name allowlist. This includes renamed Cronet libs.
		anchors := hasBoringSSLSocketAnchors(file)
		if anchors && machine == elf.EM_AARCH64 {
			resolved, err := resolveChromium(ctx, file)
			if ctx.Err() != nil {
				return nil, "", ctx.Err()
			}
			if err != nil {
				return nil, "", &unsupportedChromiumError{filepath.Base(name), err.Error()}
			}
			for symbol, offset := range resolved {
				if known := result[symbol]; known != 0 && known != offset {
					return nil, "", &unsupportedChromiumError{filepath.Base(name), "symbols disagree with the verified BoringSSL functions"}
				}
			}
			result, method = resolved, "BoringSSL code analysis"
		} else if len(result) > 0 || anchors || chromiumLibraryName(name) {
			return nil, "", &unsupportedChromiumError{filepath.Base(name), "automatic SSL_new/free/read/write resolution is incomplete"}
		} else {
			return nil, "", errors.New("no supported SSL interface found")
		}
	}
	if _, err := planProbes(library{symbols: result}); err != nil {
		return nil, "", err
	}
	return result, method, nil
}

func hasBoringSSLSocketAnchors(file *elf.File) bool {
	section, err := chromiumSection(file, ".rodata", 32*1024*1024, false)
	if err != nil {
		return false
	}
	data, err := section.Data()
	return err == nil && bytes.Contains(data, []byte("boringssl/src/ssl/ssl_lib.cc\x00")) &&
		bytes.Contains(data, []byte("net/socket/ssl_client_socket_impl.cc\x00"))
}

type contextReaderAt struct {
	ctx context.Context
	io.ReaderAt
}

func (r contextReaderAt) ReadAt(data []byte, offset int64) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.ReaderAt.ReadAt(data, offset)
}

// The visitor runs while the original file is open. APK members are read in
// place and probes always target APK data offset + ELF function file offset.
func walkLibraries(ctx context.Context, path string, visit func(library) error) error {
	return walkLibraryOptions(ctx, path, libraryOptions{}, visit)
}

type libraryOptions struct {
	offsets  []manualTLSOffsets
	describe func(library, error) error
}

func walkLibraryOptions(ctx context.Context, path string, options libraryOptions, visit func(library) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateManualTLSOffsets(options.offsets); err != nil {
		return err
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024*1024 {
		return errors.New("TLS library file is too large or not regular")
	}
	stat := info.Sys().(*syscall.Stat_t)
	identity := fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
	reader := contextReaderAt{ctx, file}
	attachPath := fmt.Sprintf("/proc/self/fd/%d", file.Fd())
	isArchive := false
	matched := make(map[string]bool)
	inspect := func(entry string, base, size int64) error {
		section := io.NewSectionReader(reader, base, size)
		value := library{path: path, entry: entry, identity: fmt.Sprintf("%s:%d", identity, base),
			attachPath: attachPath, base: uint64(base), size: size}
		var override *manualTLSOffsets
		for index := range options.offsets {
			candidate := &options.offsets[index]
			if candidate.Path == path && (isArchive && candidate.Entry == entry || !isArchive && candidate.Entry == "") {
				override = candidate
				break
			}
		}
		var err error
		if override != nil {
			matched[override.Entry] = true
			value.symbols, value.method, err = resolveManualTLS(ctx, section, size, *override, collectorMachine())
			if err == nil {
				value.sha256 = override.SHA256
				if current, statErr := file.Stat(); statErr != nil || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) {
					err = &manualTLSError{"library changed during fingerprint verification"}
				}
			}
			if err != nil {
				err = fmt.Errorf("%s: %w", entry, err)
			}
		} else {
			value.symbols, value.method, err = resolveSymbols(ctx, section, entry)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if options.describe != nil {
			if value.sha256 == "" {
				hash, hashErr := librarySHA256(ctx, section, size)
				value.sha256 = hash
				if hashErr != nil {
					return hashErr
				}
			}
			if reportErr := options.describe(value, err); reportErr != nil {
				return &libraryVisitError{reportErr}
			}
		}
		if err != nil {
			return err
		}
		err = visit(value)
		if err != nil {
			return &libraryVisitError{err}
		}
		return nil
	}
	var magic [4]byte
	if _, err := reader.ReadAt(magic[:], 0); err != nil {
		return err
	}
	if bytes.Equal(magic[:], []byte{'P', 'K', 3, 4}) {
		isArchive = true
		for _, value := range options.offsets {
			if value.Path == path && value.Entry == "" {
				return &manualTLSError{"an APK path requires the exact lib/<ABI>/name.so entry"}
			}
		}
		archive, err := zip.NewReader(reader, info.Size())
		if err != nil {
			return err
		}
		if len(archive.File) > 32768 {
			return errors.New("APK contains too many entries")
		}
		requested := make(map[string]int)
		for _, value := range options.offsets {
			if value.Path == path {
				requested[value.Entry] = 0
			}
		}
		ordered := make([]*zip.File, 0, len(archive.File))
		for _, entry := range archive.File {
			if _, manual := requested[entry.Name]; manual {
				requested[entry.Name]++
				ordered = append(ordered, entry)
			}
		}
		for _, entry := range archive.File {
			if _, manual := requested[entry.Name]; !manual {
				ordered = append(ordered, entry)
			}
		}
		prefix := "lib/arm64-v8a/"
		if runtime.GOARCH == "amd64" {
			prefix = "lib/x86_64/"
		}
		var warnings []error
		for _, entry := range ordered {
			if err := ctx.Err(); err != nil {
				return err
			}
			if requested[entry.Name] > 1 || !strings.HasPrefix(entry.Name, prefix) || !strings.HasSuffix(entry.Name, ".so") ||
				entry.UncompressedSize64 > 256*1024*1024 {
				continue
			}
			// Compressed members are not executable in place. PackageManager's
			// nativeLibraryDir and process mappings cover their extracted copy.
			if entry.Method != zip.Store || entry.CompressedSize64 != entry.UncompressedSize64 {
				continue
			}
			offset, err := entry.DataOffset()
			if err != nil || offset < 0 || uint64(offset) > uint64(info.Size()) ||
				entry.UncompressedSize64 > uint64(info.Size())-uint64(offset) {
				continue
			}
			if err := inspect(entry.Name, offset, int64(entry.UncompressedSize64)); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				var unsupported *unsupportedChromiumError
				var unsupportedGo *unsupportedGoTLSError
				var manual *manualTLSError
				if errors.As(err, &manual) || (errors.As(err, &unsupported) || errors.As(err, &unsupportedGo)) && len(warnings) < 3 {
					warnings = append(warnings, err)
				}
				var visitorError *libraryVisitError
				if errors.As(err, &visitorError) {
					return err
				}
			}
		}
		for _, value := range options.offsets {
			if value.Path == path && !matched[value.Entry] {
				reason := "is missing, compressed, oversized or uses a different ABI; use its extracted library when compressed"
				if requested[value.Entry] > 1 {
					reason = "is ambiguous because the APK has duplicate entry names"
				}
				warnings = append(warnings, &manualTLSError{fmt.Sprintf("%.512q %s", value.Entry, reason)})
			}
		}
		return errors.Join(warnings...)
	}
	for _, value := range options.offsets {
		if value.Path == path && value.Entry != "" {
			return &manualTLSError{"APK entry must be empty for a standalone ELF library"}
		}
	}
	if info.Size() > 256*1024*1024 {
		return errors.New("ELF exceeds 256 MiB")
	}
	return inspect(filepath.Base(path), 0, info.Size())
}

// Read-only diagnostics never return a live attachment path to their caller.
func inspectLibraries(ctx context.Context, path string) ([]library, error) {
	var result []library
	err := walkLibraries(ctx, path, func(value library) error {
		value.attachPath = ""
		result = append(result, value)
		return nil
	})
	return result, err
}
