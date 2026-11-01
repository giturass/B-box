package main

import (
	"archive/zip"
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

// Small ELF with distinct virtual and file addresses. These are inert symbols,
// not evidence of TLS capture; the CI smoke also executes mapped fixture code.
func symbolFixture() []byte {
	b := binary.LittleEndian
	data := make([]byte, 4096)
	copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	b.PutUint16(data[16:], uint16(elf.ET_DYN))
	machine := elf.EM_X86_64
	if runtime.GOARCH == "arm64" {
		machine = elf.EM_AARCH64
	}
	b.PutUint16(data[18:], uint16(machine))
	b.PutUint32(data[20:], 1)
	b.PutUint64(data[32:], 64)
	b.PutUint64(data[40:], 0x800)
	b.PutUint16(data[52:], 64)
	b.PutUint16(data[54:], 56)
	b.PutUint16(data[56:], 1)
	b.PutUint16(data[58:], 64)
	b.PutUint16(data[60:], 5)
	b.PutUint16(data[62:], 4)
	b.PutUint32(data[64:], uint32(elf.PT_LOAD))
	b.PutUint32(data[68:], uint32(elf.PF_R|elf.PF_X))
	b.PutUint64(data[80:], 0x5000)
	b.PutUint64(data[96:], uint64(len(data)))
	b.PutUint64(data[104:], uint64(len(data)))
	b.PutUint64(data[112:], 4096)
	names := "\x00SSL_new\x00SSL_free\x00SSL_read\x00SSL_write\x00"
	copy(data[0x400:], names)
	for i, name := range requiredSymbols {
		symbol := data[0x500+24*(i+1):]
		b.PutUint32(symbol, uint32(strings.Index(names, name)))
		symbol[4] = byte(elf.STB_GLOBAL)<<4 | byte(elf.STT_FUNC)
		b.PutUint16(symbol[6:], 1)
		b.PutUint64(symbol[8:], 0x5200+uint64(i)*32)
		b.PutUint64(symbol[16:], 16)
	}
	sections := "\x00.text\x00.symtab\x00.strtab\x00.shstrtab\x00"
	copy(data[0x600:], sections)
	section := func(index int, name string, typ elf.SectionType, flags elf.SectionFlag, addr, off, size, align, entry uint64, link uint32) {
		s := data[0x800+index*64:]
		b.PutUint32(s, uint32(strings.Index(sections, name)))
		b.PutUint32(s[4:], uint32(typ))
		b.PutUint64(s[8:], uint64(flags))
		b.PutUint64(s[16:], addr)
		b.PutUint64(s[24:], off)
		b.PutUint64(s[32:], size)
		b.PutUint32(s[40:], link)
		b.PutUint64(s[48:], align)
		b.PutUint64(s[56:], entry)
	}
	section(1, ".text", elf.SHT_PROGBITS, elf.SHF_ALLOC|elf.SHF_EXECINSTR, 0x5200, 0x200, 128, 16, 0, 0)
	section(2, ".symtab", elf.SHT_SYMTAB, 0, 0, 0x500, 24*5, 8, 24, 3)
	b.PutUint32(data[0x800+2*64+44:], 1)
	section(3, ".strtab", elf.SHT_STRTAB, 0, 0, 0x400, uint64(len(names)), 1, 0, 0)
	section(4, ".shstrtab", elf.SHT_STRTAB, 0, 0, 0x600, uint64(len(sections)), 1, 0, 0)
	return data
}

func TestAutomaticOffsetsAreFileOffsets(t *testing.T) {
	symbols, method, err := resolveSymbols(context.Background(), bytes.NewReader(symbolFixture()), "renamed.so")
	if err != nil || method != "ELF symbols" {
		t.Fatal(symbols, method, err)
	}
	for i, name := range requiredSymbols {
		if symbols[name] != 0x200+uint64(i)*32 {
			t.Fatalf("%s: used virtual address %#x", name, symbols[name])
		}
	}
}

func TestSymbolsInExecutableDataOrOutsideCodeAreRejected(t *testing.T) {
	for _, mutate := range []func([]byte){
		func(data []byte) { binary.LittleEndian.PutUint64(data[0x500+24+8:], 0x5100) },
		func(data []byte) { binary.LittleEndian.PutUint64(data[0x800+64+8:], uint64(elf.SHF_ALLOC)) },
		func(data []byte) { binary.LittleEndian.PutUint64(data[0x500+24+16:], 4096) },
		func(data []byte) { binary.LittleEndian.PutUint16(data[18:], uint16(elf.EM_ARM)) },
	} {
		data := symbolFixture()
		mutate(data)
		if _, _, err := resolveSymbols(context.Background(), bytes.NewReader(data), "libssl.so"); err == nil {
			t.Fatal("accepted an incompatible SSL symbol")
		}
	}
}

func TestMiniDebugdataResolvesAgainstTheOriginalLoadMapping(t *testing.T) {
	var compressed bytes.Buffer
	writer, err := xz.NewWriter(&compressed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(symbolFixture()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data := symbolFixture()
	const names = "\x00.text\x00.gnu_debugdata\x00.strtab\x00.shstrtab\x00"
	copy(data[0x600:], names)
	b := binary.LittleEndian
	for index, name := range []string{"", ".text", ".gnu_debugdata", ".strtab", ".shstrtab"} {
		b.PutUint32(data[0x800+index*64:], uint32(strings.Index(names, name)))
	}
	b.PutUint64(data[0x800+4*64+32:], uint64(len(names)))
	section := data[0x800+2*64:]
	b.PutUint32(section[4:], uint32(elf.SHT_PROGBITS))
	b.PutUint64(section[24:], 0xa00)
	b.PutUint64(section[32:], uint64(compressed.Len()))
	b.PutUint64(section[40:], 0)
	b.PutUint64(section[56:], 0)
	if compressed.Len() > len(data)-0xa00 {
		t.Fatal("mini-debug fixture is too large")
	}
	copy(data[0xa00:], compressed.Bytes())
	symbols, method, err := resolveSymbols(context.Background(), bytes.NewReader(data), "libjavacrypto.so")
	if err != nil || method != "ELF mini-debugdata" || symbols["SSL_new"] != 0x200 {
		t.Fatal(symbols, method, err)
	}
}

func TestRenamedCronetIsRecognizedByContents(t *testing.T) {
	fixture := makeChromiumFixture(t, 42, false)
	symbols, method, err := resolveSymbolsForMachine(context.Background(), bytes.NewReader(fixture.data),
		"libnetwork-obfuscated-name.so", elf.EM_AARCH64)
	if err != nil || len(symbols) != 4 || method != "BoringSSL code analysis" {
		t.Fatalf("automatic stripped library resolution: %v, %s, %v", symbols, method, err)
	}
}

func writeAPK(t *testing.T, path string, method uint16) string {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	abi := "x86_64"
	if runtime.GOARCH == "arm64" {
		abi = "arm64-v8a"
	}
	name := "lib/" + abi + "/libprivate.so"
	entry, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: method})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(symbolFixture()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestAPKVisitorPinsTheAnalyzedInodeAndPropagatesErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.apk")
	name := writeAPK(t, path, zip.Store)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("stop visitor")
	called := false
	err = walkLibraries(context.Background(), path, func(value library) error {
		called = true
		if value.entry != name || value.base == 0 {
			t.Fatalf("missing APK identity: %+v", value)
		}
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("replacement inode"), 0600); err != nil {
			t.Fatal(err)
		}
		pinned, err := os.Stat(value.attachPath)
		if err != nil || !os.SameFile(before, pinned) {
			t.Fatalf("analysis/attach inode changed: %v", err)
		}
		file, err := os.Open(value.attachPath)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		header := make([]byte, 4)
		if _, err := file.ReadAt(header, int64(value.base)); err != nil || string(header) != "\x7fELF" {
			t.Fatalf("APK offset is not ELF data: %x, %v", header, err)
		}
		plan, err := planProbes(value)
		if err != nil || plan[0].offset != value.base+value.symbols["SSL_write"] {
			t.Fatal(plan, err)
		}
		return sentinel
	})
	if !called || !errors.Is(err, sentinel) {
		t.Fatal("APK visitor error was swallowed", err)
	}
}

func TestCompressedAPKMembersAreNotProbedAtCompressedOffsets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "base.apk")
	writeAPK(t, path, zip.Deflate)
	err := walkLibraries(context.Background(), path, func(library) error {
		t.Fatal("compressed bytes were treated as executable code")
		return io.EOF
	})
	if err != nil {
		t.Fatal(err)
	}
}
