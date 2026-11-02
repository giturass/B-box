package main

import (
	"bytes"
	"context"
	"debug/buildinfo"
	"debug/elf"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

// Inert Go 1.22 metadata with independently named functions. The Go linker
// writes pcHeader, funcnametab and the function index in this layout:
// https://github.com/golang/go/blob/go1.22.6/src/cmd/link/internal/ld/pcln.go
// Real stripped PIE/c-shared binaries remain covered by the CI smoke.
func goTableFixture(names []string, sectionName string, sslCount int) []byte {
	b := binary.LittleEndian
	data := append(symbolFixture(), make([]byte, 4096)...)
	b.PutUint64(data[96:], uint64(len(data)))
	b.PutUint64(data[104:], uint64(len(data)))
	b.PutUint64(data[0x800+64+32:], 0x200)
	for i := sslCount; i < len(requiredSymbols); i++ {
		b.PutUint16(data[0x500+24*(i+1)+6:], uint16(elf.SHN_UNDEF))
	}
	sections := "\x00.text\x00.symtab\x00.strtab\x00.shstrtab\x00" + sectionName + "\x00.go.buildinfo\x00"
	copy(data[0x600:], sections)
	b.PutUint64(data[0x800+4*64+32:], uint64(len(sections)))
	b.PutUint16(data[60:], 7)
	section := func(index int, name string, offset, size uint64) {
		s := data[0x800+index*64:]
		b.PutUint32(s, uint32(strings.Index(sections, name)))
		b.PutUint32(s[4:], uint32(elf.SHT_PROGBITS))
		b.PutUint64(s[8:], uint64(elf.SHF_ALLOC))
		b.PutUint64(s[16:], 0x5000+offset)
		b.PutUint64(s[24:], offset)
		b.PutUint64(s[32:], size)
		b.PutUint64(s[48:], 8)
	}
	info := append(make([]byte, 32), []byte("\x08go1.22.6\x00")...)
	copy(info, "\xff Go buildinf:\x08\x02")
	copy(data[0xa00:], info)
	section(6, ".go.buildinfo", 0xa00, uint64(len(info)))

	nameTable := []byte(strings.Join(names, "\x00") + "\x00")
	align := func(value int) int { return (value + 7) &^ 7 }
	cutab := align(72 + len(nameTable))
	filetab := cutab + 4
	pctab := filetab + len("fixture.go\x00")
	funcdata := align(pctab + 1)
	records := align(len(names)*8 + 4)
	pcln := make([]byte, funcdata+records+len(names)*48)
	copy(pcln, []byte{0xf1, 0xff, 0xff, 0xff, 0, 0, 1, 8})
	machine := elf.Machine(b.Uint16(data[18:]))
	if machine == elf.EM_AARCH64 {
		pcln[6] = 4
	}
	b.PutUint64(pcln[8:], uint64(len(names)))
	b.PutUint64(pcln[16:], 1)
	b.PutUint64(pcln[24:], 0x5280) // Go text follows the synthetic C entry points.
	for i, offset := range []int{72, cutab, filetab, pctab, funcdata} {
		b.PutUint64(pcln[32+i*8:], uint64(offset))
	}
	copy(pcln[72:], nameTable)
	copy(pcln[filetab:], "fixture.go\x00")
	nameOffset := 0
	for i, name := range names {
		entry, record := i*16, records+i*48
		b.PutUint32(pcln[funcdata+i*8:], uint32(entry))
		b.PutUint32(pcln[funcdata+i*8+4:], uint32(record))
		b.PutUint32(pcln[funcdata+record:], uint32(entry))
		b.PutUint32(pcln[funcdata+record+4:], uint32(nameOffset))
		nameOffset += len(name) + 1
		code := data[0x280+entry : 0x280+entry+16]
		if machine == elf.EM_AARCH64 {
			for at := 0; at < len(code); at += 4 {
				b.PutUint32(code[at:], 0xd503201f)
			}
			b.PutUint32(code[12:], 0xd65f03c0)
		} else {
			for at := range code {
				code[at] = 0x90
			}
			code[15] = 0xc3
		}
	}
	b.PutUint32(pcln[funcdata+len(names)*8:], uint32(len(names)*16))
	// Also exercise a header folded into .data.rel.ro after unrelated data.
	copy(data[0x1020:], pcln)
	section(5, sectionName, 0x1000, uint64(32+len(pcln)))
	return data
}

func TestGoFunctionTableWithoutTLSIsNotAnUnsupportedTLSLibrary(t *testing.T) {
	for _, section := range []string{".gopclntab", ".data.rel.ro"} {
		data := goTableFixture([]string{"runtime.main", "runtime.goexit", "crypto/sha256.Sum256"}, section, 0)
		file, err := elf.NewFile(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		info, err := buildinfo.Read(bytes.NewReader(data))
		if err != nil || info.GoVersion != "go1.22.6" {
			t.Fatal("invalid Go build-info fixture", info, err)
		}
		if _, _, err := resolveGoTLS(context.Background(), file, info); !errors.Is(err, errNoGoTLS) {
			t.Fatalf("valid non-TLS function table was called missing: %v", err)
		}
		_, _, err = resolveSymbols(context.Background(), bytes.NewReader(data), "libgojni.so")
		var unsupported *unsupportedGoTLSError
		if err == nil || errors.As(err, &unsupported) {
			t.Fatalf("non-TLS Go library must be skipped without a capture warning: %v", err)
		}
	}
	data := goTableFixture([]string{"runtime.main", "runtime.goexit"}, ".data.rel.ro", 4)
	symbols, method, err := resolveSymbols(context.Background(), bytes.NewReader(data), "combined.so")
	if err != nil || method != "ELF symbols" || len(symbols) != 4 {
		t.Fatalf("non-TLS Go code must not hide or warn about a valid SSL adapter: %v, %s, %v", symbols, method, err)
	}
	data = goTableFixture([]string{"runtime.main", "runtime.goexit"}, ".data.rel.ro", 1)
	_, _, err = resolveSymbols(context.Background(), bytes.NewReader(data), "partial-ssl.so")
	var unsupported *unsupportedChromiumError
	if !errors.As(err, &unsupported) {
		t.Fatalf("non-TLS Go code hid an incomplete SSL adapter: %v", err)
	}
}

func TestGoTLSMissingFunctionsAndCorruptTablesRemainDiagnostics(t *testing.T) {
	names := []string{goRead, goWrite, goClose, goHandshake}
	data := goTableFixture(names, ".data.rel.ro", 0)
	symbols, method, err := resolveSymbols(context.Background(), bytes.NewReader(data), "tls.so")
	if err != nil || symbols[goRead] != 0x280 || !strings.HasPrefix(method, "Go TLS go1.22.6 ") {
		t.Fatal(symbols, method, err)
	}
	for name, mutate := range map[string]func([]byte){
		"missing read": func(data []byte) {
			at := bytes.Index(data, []byte(goRead+"\x00"))
			data[at] = 'X'
		},
		"missing header": func(data []byte) { data[0x1020] = 0 },
		"bad function names": func(data []byte) {
			binary.LittleEndian.PutUint64(data[0x1020+32:], uint64(len(data)))
		},
	} {
		t.Run(name, func(t *testing.T) {
			data := bytes.Clone(data)
			mutate(data)
			_, _, err := resolveSymbols(context.Background(), bytes.NewReader(data), "tls.so")
			var unsupported *unsupportedGoTLSError
			if !errors.As(err, &unsupported) || errors.Is(err, errNoGoTLS) {
				t.Fatalf("incomplete or malformed Go TLS was silently skipped: %v", err)
			}
			if name == "missing read" && !strings.Contains(err.Error(), goRead+" is missing") {
				t.Fatalf("missing Read was misreported as a missing table: %v", err)
			}
		})
	}
}
