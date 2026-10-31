package main

import (
	"archive/zip"
	"bufio"
	"bytes"
	"debug/elf"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/ulikunitz/xz"
)

type library struct {
	path, identity string
	base           uint64
	symbols        map[string]uint64
}

var wantedSymbols = map[string]bool{
	"SSL_new": true, "SSL_free": true, "SSL_clear": true,
	"SSL_read": true, "SSL_write": true, "SSL_read_ex": true, "SSL_write_ex": true,
	"SSL_read_early_data": true, "SSL_write_early_data": true,
}

func executableOffset(file *elf.File, value uint64) (uint64, bool) {
	for _, program := range file.Progs {
		if program.Type == elf.PT_LOAD && program.Flags&elf.PF_X != 0 &&
			value >= program.Vaddr && value-program.Vaddr < program.Filesz {
			return program.Off + value - program.Vaddr, true
		}
	}
	return 0, false
}

func readSymbols(reader io.ReaderAt) (map[string]uint64, error) {
	file, err := elf.NewFile(reader)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	machine := elf.EM_AARCH64
	if runtime.GOARCH == "amd64" {
		machine = elf.EM_X86_64
	}
	if file.Class != elf.ELFCLASS64 || file.Data != elf.ELFDATA2LSB || file.Machine != machine {
		return nil, errors.New("TLS library does not match the collector's 64-bit architecture")
	}
	result := make(map[string]uint64)
	collect := func(symbols []elf.Symbol) {
		for _, symbol := range symbols {
			if !wantedSymbols[symbol.Name] || symbol.Section == elf.SHN_UNDEF || elf.ST_TYPE(symbol.Info) != elf.STT_FUNC {
				continue
			}
			if offset, ok := executableOffset(file, symbol.Value); ok {
				result[symbol.Name] = offset
			}
		}
	}
	if symbols, err := file.DynamicSymbols(); err == nil {
		collect(symbols)
	}
	if symbols, err := file.Symbols(); err == nil {
		collect(symbols)
	}
	// Conscrypt may retain local symbols in XZ mini-debug information.
	if section := file.Section(".gnu_debugdata"); section != nil && section.Size <= 16*1024*1024 {
		decoder, err := (xz.ReaderConfig{DictCap: 16 * 1024 * 1024, SingleStream: true}).NewReader(section.Open())
		if err == nil {
			data, err := io.ReadAll(io.LimitReader(decoder, 32*1024*1024+1))
			if err == nil && len(data) <= 32*1024*1024 {
				if debug, err := elf.NewFile(bytes.NewReader(data)); err == nil {
					if symbols, err := debug.Symbols(); err == nil {
						collect(symbols)
					}
					debug.Close()
				}
			}
		}
	}
	if result["SSL_new"] == 0 || result["SSL_free"] == 0 || result["SSL_read"] == 0 || result["SSL_write"] == 0 {
		return nil, errors.New("SSL_new/free/read/write symbols are unavailable")
	}
	return result, nil
}

func inspectLibraries(path string) ([]library, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024*1024 {
		return nil, errors.New("TLS library file is too large or not regular")
	}
	stat := info.Sys().(*syscall.Stat_t)
	identity := fmt.Sprintf("%d:%d", stat.Dev, stat.Ino)
	if strings.HasSuffix(path, ".apk") {
		archive, err := zip.NewReader(file, info.Size())
		if err != nil {
			return nil, err
		}
		if len(archive.File) > 32768 {
			return nil, errors.New("APK contains too many entries")
		}
		prefix := "lib/arm64-v8a/"
		if runtime.GOARCH == "amd64" {
			prefix = "lib/x86_64/"
		}
		var result []library
		for _, entry := range archive.File {
			if entry.Method != zip.Store || !strings.HasPrefix(entry.Name, prefix) ||
				!strings.HasSuffix(entry.Name, ".so") || entry.UncompressedSize64 > 256*1024*1024 {
				continue
			}
			offset, err := entry.DataOffset()
			if err != nil || offset < 0 || uint64(offset)+entry.UncompressedSize64 > uint64(info.Size()) {
				continue
			}
			symbols, err := readSymbols(io.NewSectionReader(file, offset, int64(entry.UncompressedSize64)))
			if err != nil {
				continue
			}
			result = append(result, library{path, fmt.Sprintf("%s:%d", identity, offset), uint64(offset), symbols})
		}
		return result, nil
	}
	if info.Size() > 256*1024*1024 {
		return nil, errors.New("ELF exceeds 256 MiB")
	}
	symbols, err := readSymbols(file)
	if err != nil {
		return nil, err
	}
	return []library{{path, identity + ":0", 0, symbols}}, nil
}

func (c collectorConfig) selected(uid uint32) bool {
	if uid == c.ExcludeUID || uid/100000 != c.UserID {
		return false
	}
	if len(c.UIDs) == 0 {
		return true
	}
	for _, selected := range c.UIDs {
		if uid == selected {
			return true
		}
	}
	return false
}

func discoverPaths(config collectorConfig) []string {
	paths := map[string]bool{
		"/apex/com.android.conscrypt/lib64/libssl.so":        true,
		"/apex/com.android.conscrypt/lib64/libjavacrypto.so": true,
		"/system/lib64/libssl.so":                            true, "/system/lib64/libjavacrypto.so": true,
	}
	for _, path := range config.Libraries {
		paths[path] = true
	}
	processes, _ := os.ReadDir("/proc")
	for _, process := range processes {
		if _, err := strconv.ParseUint(process.Name(), 10, 32); err != nil {
			continue
		}
		directory := filepath.Join("/proc", process.Name())
		info, err := os.Stat(directory)
		if err != nil || !config.selected(info.Sys().(*syscall.Stat_t).Uid) {
			continue
		}
		file, err := os.Open(filepath.Join(directory, "maps"))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(io.LimitReader(file, 8*1024*1024))
		scanner.Buffer(make([]byte, 8192), 64*1024)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			if len(fields) < 6 || !strings.Contains(fields[1], "x") {
				continue
			}
			path := strings.Join(fields[5:], " ")
			if !strings.HasPrefix(path, "/") || !(strings.HasSuffix(path, ".so") || strings.HasSuffix(path, ".apk")) {
				continue
			}
			// Resolve the actual inode visible in this process's mount namespace.
			path = directory + "/root" + strings.ReplaceAll(path, `\040`, " ")
			if len(paths) < 4096 {
				paths[path] = true
			}
		}
		file.Close()
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	return result
}
