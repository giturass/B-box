package main

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"strings"
	"testing"
)

// Inert 64-bit ELF metadata, using the layout written by the Go linker. The
// Go text starts after C code and pcHeader is folded into a larger section.
// The CI smoke separately builds and executes real Go binaries with both
// linkers; this fixture exercises rejection of inconsistent metadata.
func goAddressFixture(machine elf.Machine, relocation elf.SectionType, module bool) []byte {
	b := binary.LittleEndian
	data := make([]byte, 4096)
	copy(data, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	b.PutUint16(data[16:], uint16(elf.ET_DYN))
	b.PutUint16(data[18:], uint16(machine))
	b.PutUint32(data[20:], 1)
	b.PutUint64(data[40:], 0xc00)
	b.PutUint16(data[52:], 64)
	b.PutUint16(data[58:], 64)
	b.PutUint16(data[60:], 6)
	b.PutUint16(data[62:], 5)
	names := "\x00.text\x00.gopclntab\x00.noptrdata\x00.rela.dyn\x00.shstrtab\x00"
	copy(data[0x100:], names)
	section := func(index int, name string, typ elf.SectionType, flags elf.SectionFlag, addr, offset, size, entry uint64) {
		s := data[0xc00+index*64:]
		b.PutUint32(s, uint32(strings.Index(names, name)))
		b.PutUint32(s[4:], uint32(typ))
		b.PutUint64(s[8:], uint64(flags))
		b.PutUint64(s[16:], addr)
		b.PutUint64(s[24:], offset)
		b.PutUint64(s[32:], size)
		b.PutUint64(s[48:], 8)
		b.PutUint64(s[56:], entry)
	}
	section(1, ".text", elf.SHT_PROGBITS, elf.SHF_ALLOC|elf.SHF_EXECINSTR, 0x5000, 0x200, 0x100, 0)
	section(2, ".gopclntab", elf.SHT_PROGBITS, elf.SHF_ALLOC, 0x7000, 0x400, 0x300, 0)
	section(3, ".noptrdata", elf.SHT_PROGBITS, elf.SHF_ALLOC|elf.SHF_WRITE, 0x9000, 0x800, 0x200, 0)
	section(5, ".shstrtab", elf.SHT_STRTAB, 0, 0, 0x100, uint64(len(names)), 0)
	header := data[0x420:]
	copy(header, []byte{0xf1, 0xff, 0xff, 0xff, 0, 0, 1, 8})
	if machine == elf.EM_AARCH64 {
		header[6] = 4
	}
	b.PutUint64(header[8:], 2)
	b.PutUint64(header[16:], 1)
	tables := []uint64{72, 96, 104, 120, 144}
	lengths := []uint64{16, 2, 16, 16, 128}
	for i, table := range tables {
		b.PutUint64(header[32+i*8:], table)
	}
	b.PutUint32(header[144:], 0x10)
	b.PutUint32(header[152:], 0x30)
	b.PutUint32(header[160:], 0x60)
	prefix := data[0x818:]
	for i, length := range lengths {
		b.PutUint64(prefix[16+i*24:], length)
		b.PutUint64(prefix[24+i*24:], length)
	}
	b.PutUint64(prefix[136:], 3)
	b.PutUint64(prefix[144:], 3)
	pointers := [][2]uint64{
		{0, 0x7020}, {8, 0x7068}, {32, 0x7080}, {56, 0x7088},
		{80, 0x7098}, {104, 0x70b0}, {128, 0x70b0},
		{160, 0x5030}, {168, 0x5080}, {176, 0x5020}, {184, 0x50c0},
	}
	entrySize := uint64(16)
	if relocation == elf.SHT_RELA {
		entrySize = 24
	}
	count := uint64(0)
	writePointer := func(address, offset, value uint64) {
		if relocation != elf.SHT_RELA {
			b.PutUint64(data[offset:], value)
		}
		if relocation == elf.SHT_NULL {
			return
		}
		entry := data[0xa00+count*entrySize:]
		b.PutUint64(entry, address)
		kind := uint64(elf.R_X86_64_RELATIVE)
		if machine == elf.EM_AARCH64 {
			kind = uint64(elf.R_AARCH64_RELATIVE)
		}
		b.PutUint64(entry[8:], kind)
		if relocation == elf.SHT_RELA {
			b.PutUint64(entry[16:], value)
		}
		count++
	}
	for _, pointer := range pointers {
		writePointer(0x9018+pointer[0], 0x818+pointer[0], pointer[1])
	}
	if !module {
		writePointer(0x7038, 0x438, 0x5020)
	}
	if count != 0 {
		section(4, ".rela.dyn", relocation, elf.SHF_ALLOC, 0xa000, 0xa00, count*entrySize, entrySize)
	}
	return data
}

func readFixtureText(ctx context.Context, data []byte, module bool) (uint64, error) {
	file, err := elf.NewFile(bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	defer file.Close()
	section := file.Section(".gopclntab")
	pcln, err := section.Data()
	if err != nil {
		return 0, err
	}
	if module {
		return goModuleTextAddress(ctx, file, section, pcln, 32)
	}
	return goTextAddress(ctx, file, section, pcln, 32)
}

func TestGoTextAddressesIncludeCStartupAndELFRelocations(t *testing.T) {
	for _, machine := range []elf.Machine{elf.EM_X86_64, elf.EM_AARCH64} {
		for _, relocation := range []elf.SectionType{elf.SHT_NULL, elf.SHT_REL, elf.SHT_RELA} {
			for _, module := range []bool{false, true} {
				value, err := readFixtureText(context.Background(), goAddressFixture(machine, relocation, module), module)
				if err != nil || value != 0x5020 {
					t.Fatalf("%s %s module=%v: text=%#x, error=%v", machine, relocation, module, value, err)
				}
			}
		}
	}
}

func TestGoModuleTextRequiresMatchingTablesAndBounds(t *testing.T) {
	b := binary.LittleEndian
	for name, mutate := range map[string]func([]byte){
		"missing module":           func(data []byte) { b.PutUint64(data[0x818+144:], 0) },
		"wrong header":             func(data []byte) { b.PutUint64(data[0xa00+16:], 0x7028) },
		"wrong slice":              func(data []byte) { b.PutUint64(data[0xa00+24+16:], 0x7060) },
		"unbounded slice":          func(data []byte) { b.PutUint64(data[0x818+16:], 0xffffffffffffffff) },
		"wrong minpc":              func(data []byte) { b.PutUint64(data[0xa00+7*24+16:], 0x5038) },
		"wrong text":               func(data []byte) { b.PutUint64(data[0xa00+9*24+16:], 0x5030) },
		"outside text":             func(data []byte) { b.PutUint64(data[0xa00+10*24+16:], 0x6000) },
		"unknown relocation":       func(data []byte) { b.PutUint64(data[0xa00+8:], 0) },
		"duplicate relocation":     func(data []byte) { b.PutUint64(data[0xa00+24:], 0x9018) },
		"truncated function index": func(data []byte) { b.PutUint64(data[0x420+64:], 0x2d0) },
	} {
		t.Run(name, func(t *testing.T) {
			data := goAddressFixture(elf.EM_X86_64, elf.SHT_RELA, true)
			mutate(data)
			if value, err := readFixtureText(context.Background(), data, true); err == nil {
				t.Fatalf("unverified Go text accepted: %#x", value)
			}
		})
	}
	data := goAddressFixture(elf.EM_X86_64, elf.SHT_NULL, true)
	copy(data[0x8d8:0x998], data[0x818:0x8d8])
	b.PutUint64(data[0x8d8+160:], 0x5040)
	b.PutUint64(data[0x8d8+168:], 0x5090)
	b.PutUint64(data[0x8d8+176:], 0x5030)
	if _, err := readFixtureText(context.Background(), data, true); err == nil {
		t.Fatal("conflicting Go modules accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readFixtureText(ctx, data, true); err != context.Canceled {
		t.Fatal("cancelled Go module scan continued", err)
	}
}
