package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

type chromiumSample struct {
	name                string
	old, address, frame uint64
	code                []byte
}

type chromiumFixture struct {
	data                     []byte
	functions                map[string]*chromiumSample
	imports                  map[string]*chromiumSample
	header, frames, sections uint64
}

func fixtureAddress(t *testing.T, text string) uint64 {
	t.Helper()
	value, err := strconv.ParseUint(text, 0, 64)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// Build an inert ELF from licensed, real compiler output. All functions,
// source strings and external call targets are relocated. External helpers
// are RET stubs: this is static analysis input and can never perform TLS.
func makeChromiumFixture(t *testing.T, seed int64, duplicateWrite bool) chromiumFixture {
	return makeChromiumFixtureSource(t, "instructions.json", seed, duplicateWrite, false)
}

func makeChromiumFixtureSource(t *testing.T, source string, seed int64, duplicateWrite, entryCalls bool) chromiumFixture {
	t.Helper()
	var input struct {
		Sources   map[string]string `json:"sources"`
		Imports   map[string]string `json:"imports"`
		Functions []struct {
			Name, Address, SHA256 string
			Code                  []string
		}
	}
	data, err := os.ReadFile("testdata/chromium/" + source)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &input); err != nil {
		t.Fatal(err)
	}
	var samples []*chromiumSample
	for _, source := range input.Functions {
		code, err := hex.DecodeString(strings.Join(source.Code, ""))
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(code)) != source.SHA256 {
			t.Fatalf("corrupt instruction fixture %s: %v", source.Name, err)
		}
		samples = append(samples, &chromiumSample{name: source.Name, old: fixtureAddress(t, source.Address), code: code})
	}
	inside := func(target uint64) bool {
		for _, sample := range samples {
			if target >= sample.old && target-sample.old < uint64(len(sample.code)) {
				return true
			}
		}
		return false
	}
	external := make(map[uint64]bool)
	for _, sample := range samples {
		for index := 0; index < len(sample.code); index += 4 {
			word := binary.LittleEndian.Uint32(sample.code[index:])
			if word&0x7c000000 == 0x14000000 {
				delta := int64(int32(word<<6)>>6) * 4
				target := uint64(int64(sample.old) + int64(index) + delta)
				if !inside(target) {
					external[target] = true
				}
			}
		}
	}
	var targets []uint64
	for target := range external {
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i] < targets[j] })
	for _, target := range targets {
		code := []byte{0xc0, 0x03, 0x5f, 0xd6}
		if input.Imports[fmt.Sprintf("%#x", target)] != "" {
			code = make([]byte, 16) // Filled below with an inert, relocated PLT stub.
		}
		samples = append(samples, &chromiumSample{name: fmt.Sprintf("helper_%x", target), old: target, code: code})
	}
	if entryCalls {
		// Stand in for callers outside the nine extracted functions. A direct
		// BL is required even when the callee has no unwind record.
		samples = append(samples, &chromiumSample{name: "fixture_entry_calls", code: make([]byte, 4*(len(input.Functions)+1))})
	}
	if duplicateWrite {
		for _, sample := range samples {
			if sample.name == "SSL_write" {
				samples = append(samples, &chromiumSample{name: "duplicate_write", old: sample.old, code: bytes.Clone(sample.code)})
				break
			}
		}
	}
	random := rand.New(rand.NewSource(seed))
	random.Shuffle(len(samples), func(i, j int) { samples[i], samples[j] = samples[j], samples[i] })
	base := uint64(0x10000000) + uint64(seed)*0x1000000
	align := func(value, multiple uint64) uint64 { return (value + multiple - 1) & ^(multiple - 1) }
	header := uint64(0x4000)
	headerSize := uint64(12 + len(samples)*8)
	frames := align(header+headerSize, 8)
	framesSize := uint64(20 + len(samples)*20)
	textOffset := align(frames+framesSize, 4096)
	next := textOffset
	for _, sample := range samples {
		next += uint64(random.Intn(32)) * 4
		sample.address = base + next
		next += uint64(len(sample.code))
	}
	textSize := next - textOffset
	stringsOffset := align(next, 8)
	sectionNames := "\x00.rodata\x00.eh_frame_hdr\x00.eh_frame\x00.text\x00.shstrtab\x00"
	sectionOffset := align(stringsOffset+uint64(len(sectionNames)), 8)
	output := make([]byte, sectionOffset+6*64)
	put16 := func(offset uint64, value uint16) { binary.LittleEndian.PutUint16(output[offset:], value) }
	put32 := func(offset uint64, value uint32) { binary.LittleEndian.PutUint32(output[offset:], value) }
	put64 := func(offset, value uint64) { binary.LittleEndian.PutUint64(output[offset:], value) }
	copy(output, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	put16(16, uint16(elf.ET_DYN))
	put16(18, uint16(elf.EM_AARCH64))
	put32(20, 1)
	put64(32, 64)
	put64(40, sectionOffset)
	put16(52, 64)
	put16(54, 56)
	put16(56, 2)
	put16(58, 64)
	put16(60, 6)
	put16(62, 5)
	for index, region := range [][3]uint64{{0, textOffset, 4}, {textOffset, textSize, 5}} {
		offset := uint64(64 + index*56)
		put32(offset, uint32(elf.PT_LOAD))
		put32(offset+4, uint32(region[2]))
		put64(offset+8, region[0])
		put64(offset+16, base+region[0])
		put64(offset+24, base+region[0])
		put64(offset+32, region[1])
		put64(offset+40, region[1])
		put64(offset+48, 4096)
	}
	pages := make(map[uint64]uint64)
	var sourceAddresses []uint64
	for address := range input.Sources {
		sourceAddresses = append(sourceAddresses, fixtureAddress(t, address))
	}
	sort.Slice(sourceAddresses, func(i, j int) bool { return sourceAddresses[i] < sourceAddresses[j] })
	for index, old := range sourceAddresses {
		page := uint64(0x1000 + index*4096)
		pages[old&^uint64(4095)] = base + page
		copy(output[page+(old&4095):], input.Sources[fmt.Sprintf("0x%x", old)]+"\x00")
	}
	relocate := func(from *chromiumSample, target uint64) uint64 {
		if target >= from.old && target-from.old < uint64(len(from.code)) {
			return from.address + target - from.old
		}
		for _, other := range samples {
			if other.name != "duplicate_write" && target >= other.old && target-other.old < uint64(len(other.code)) {
				return other.address + target - other.old
			}
		}
		t.Fatalf("unmapped fixture call target %#x", target)
		return 0
	}
	fixture := chromiumFixture{data: output, functions: make(map[string]*chromiumSample), imports: make(map[string]*chromiumSample), header: header, frames: frames, sections: sectionOffset}
	for _, sample := range samples {
		for index := 0; index < len(sample.code); index += 4 {
			word := binary.LittleEndian.Uint32(sample.code[index:])
			oldPC, newPC := sample.old+uint64(index), sample.address+uint64(index)
			switch {
			case word&0x7c000000 == 0x14000000:
				oldTarget := uint64(int64(oldPC) + int64(int32(word<<6)>>6)*4)
				delta := int64(relocate(sample, oldTarget)) - int64(newPC)
				word = word&0xfc000000 | uint32(delta/4)&0x3ffffff
			case word&0x9f000000 == 0x90000000:
				encoded := (word>>29)&3 | ((word>>5)&0x7ffff)<<2
				oldPage := uint64(int64(oldPC&^uint64(4095)) + (int64(int32(encoded<<11)>>11) << 12))
				page, known := pages[oldPage]
				if !known {
					page = base + 0x3000 // An inert page for irrelevant literals/vtables.
				}
				delta := uint32((int64(page)-int64(newPC&^uint64(4095)))/4096) & 0x1fffff
				word = word&0x9f00001f | (delta&3)<<29 | (delta>>2)<<5
			case word&0x9f000000 == 0x10000000:
				_, oldTarget, _ := armAddress(word, oldPC)
				target := base + 0x3000 + (oldTarget & 4095)
				if page, found := pages[oldTarget&^uint64(4095)]; found {
					target = page + (oldTarget & 4095)
				} else if inside(oldTarget) {
					target = relocate(sample, oldTarget)
				}
				delta := uint32(int64(target)-int64(newPC)) & 0x1fffff
				word = word&0x9f00001f | (delta&3)<<29 | (delta>>2)<<5
			case word&0xffe0001f == 0x52800004:
				// Deliberately change error source lines on every relocation.
				line := ((word>>5)&0xffff + uint32(seed)*97) & 0xffff
				word = word&^uint32(0x1fffe0) | line<<5
			}
			put32(newPC-base, word)
		}
		sample.code = output[sample.address-base : sample.address-base+uint64(len(sample.code))]
		fixture.functions[sample.name] = sample
		if name := input.Imports[fmt.Sprintf("%#x", sample.old)]; name != "" {
			fixture.imports[name] = sample
		}
	}
	if driver := fixture.functions["fixture_entry_calls"]; driver != nil {
		for index, source := range input.Functions {
			target := fixture.functions[source.Name].address
			pc := driver.address + uint64(index)*4
			put32(pc-base, 0x94000000|uint32((int64(target)-int64(pc))/4)&0x3ffffff)
		}
		put32(driver.address-base+uint64(len(driver.code))-4, 0xd65f03c0)
	}
	copy(output[header:], []byte{1, 0x1b, 3, 0x3b})
	put32(header+4, uint32(frames-(header+4)))
	put32(header+8, uint32(len(samples)))
	copy(output[frames:], []byte{16, 0, 0, 0, 0, 0, 0, 0, 1, 'z', 'R', 0, 1, 0x7c, 30, 1, 0x1b, 0x0c, 31, 0})
	for index, sample := range samples {
		fde := frames + 20 + uint64(index)*20
		sample.frame = fde
		put32(header+12+uint64(index)*8, uint32(sample.address-(base+header)))
		put32(header+16+uint64(index)*8, uint32(fde-header))
		put32(fde, 16)
		put32(fde+4, uint32(fde+4-frames))
		put32(fde+8, uint32(sample.address-(base+fde+8)))
		put32(fde+12, uint32(len(sample.code)))
	}
	copy(output[stringsOffset:], sectionNames)
	for index, section := range []struct {
		name                string
		offset, size, flags uint64
	}{
		{".rodata", 0x1000, 0x3000, 2}, {".eh_frame_hdr", header, headerSize, 2},
		{".eh_frame", frames, framesSize, 2}, {".text", textOffset, textSize, 6},
		{".shstrtab", stringsOffset, uint64(len(sectionNames)), 0},
	} {
		offset := sectionOffset + uint64(index+1)*64
		put32(offset, uint32(strings.Index(sectionNames, section.name+"\x00")))
		kind := elf.SHT_PROGBITS
		if section.name == ".shstrtab" {
			kind = elf.SHT_STRTAB
		} else {
			put64(offset+16, base+section.offset)
		}
		put32(offset+4, uint32(kind))
		put64(offset+8, section.flags)
		put64(offset+24, section.offset)
		put64(offset+32, section.size)
		put64(offset+48, 4)
	}
	if len(fixture.imports) != 0 {
		fixture.addImportSections(base)
	}
	return fixture
}

// Inert dynamic symbols, relocations and PLT instruction stubs. No imported
// implementation is included or executed. The .plt view covers the shuffled
// stubs inside the executable fixture segment, independently of the FDE table.
func (f *chromiumFixture) addImportSections(base uint64) {
	b := binary.LittleEndian
	var names []string
	for name := range f.imports {
		names = append(names, name)
	}
	sort.Strings(names)
	strtab := []byte{0}
	symbols := make([]byte, (len(names)+1)*24)
	relocations := make([]byte, len(names)*24)
	pltStart, pltEnd := ^uint64(0), uint64(0)
	for index, name := range names {
		sample := f.imports[name]
		pltStart, pltEnd = min(pltStart, sample.address), max(pltEnd, sample.address+16)
		got := base + 0x3000 + uint64(index)*8
		pageDelta := uint32((int64(got&^uint64(4095))-int64(sample.address&^uint64(4095)))/4096) & 0x1fffff
		b.PutUint32(sample.code, 0x90000010|(pageDelta&3)<<29|(pageDelta>>2)<<5)
		b.PutUint32(sample.code[4:], 0xf9400211|uint32(got&4095)/8<<10)
		b.PutUint32(sample.code[8:], 0x91000210|uint32(got&4095)<<10)
		b.PutUint32(sample.code[12:], 0xd61f0220)
		symbol := symbols[(index+1)*24:]
		b.PutUint32(symbol, uint32(len(strtab)))
		symbol[4] = byte(elf.STB_GLOBAL)<<4 | byte(elf.STT_FUNC)
		strtab = append(strtab, []byte(name)...)
		strtab = append(strtab, 0)
		relocation := relocations[index*24:]
		b.PutUint64(relocation, got)
		b.PutUint64(relocation[8:], uint64(index+1)<<32|uint64(elf.R_AARCH64_JUMP_SLOT))
	}
	oldCount := int(b.Uint16(f.data[60:]))
	oldSections := bytes.Clone(f.data[f.sections : f.sections+uint64(oldCount)*64])
	stringsSection := oldSections[5*64 : 6*64]
	stringsStart, stringsSize := b.Uint64(stringsSection[24:]), b.Uint64(stringsSection[32:])
	sectionNames := bytes.Clone(f.data[stringsStart : stringsStart+stringsSize])
	sectionNames = append(sectionNames, []byte(".plt\x00.dynsym\x00.dynstr\x00.rela.plt\x00")...)
	appendData := func(data []byte) uint64 {
		for len(f.data)%8 != 0 {
			f.data = append(f.data, 0)
		}
		offset := uint64(len(f.data))
		f.data = append(f.data, data...)
		return offset
	}
	symbolsOffset := appendData(symbols)
	stringsOffset := appendData(strtab)
	relocationsOffset := appendData(relocations)
	namesOffset := appendData(sectionNames)
	b.PutUint64(stringsSection[24:], namesOffset)
	b.PutUint64(stringsSection[32:], uint64(len(sectionNames)))
	f.sections = appendData(oldSections)
	f.data = append(f.data, make([]byte, 4*64)...)
	b.PutUint64(f.data[40:], f.sections)
	b.PutUint16(f.data[60:], uint16(oldCount+4))
	for index, info := range []struct {
		name         string
		kind         elf.SectionType
		offset, size uint64
		link, entry  uint32
	}{
		{".plt", elf.SHT_PROGBITS, pltStart - base, pltEnd - pltStart, 0, 0},
		{".dynsym", elf.SHT_DYNSYM, symbolsOffset, uint64(len(symbols)), uint32(oldCount + 2), 24},
		{".dynstr", elf.SHT_STRTAB, stringsOffset, uint64(len(strtab)), 0, 0},
		{".rela.plt", elf.SHT_RELA, relocationsOffset, uint64(len(relocations)), uint32(oldCount + 1), 24},
	} {
		section := f.data[f.sections+uint64(oldCount+index)*64:]
		b.PutUint32(section, uint32(bytes.Index(sectionNames, []byte(info.name+"\x00"))))
		b.PutUint32(section[4:], uint32(info.kind))
		b.PutUint64(section[24:], info.offset)
		b.PutUint64(section[32:], info.size)
		b.PutUint32(section[40:], info.link)
		b.PutUint64(section[48:], 8)
		b.PutUint64(section[56:], uint64(info.entry))
		if index == 0 {
			b.PutUint64(section[8:], uint64(elf.SHF_ALLOC|elf.SHF_EXECINSTR))
			b.PutUint64(section[16:], base+info.offset)
		}
	}
	for _, sample := range f.functions {
		sample.code = f.data[sample.address-base : sample.address-base+uint64(len(sample.code))]
	}
}

func (f chromiumFixture) sparseUnwind() {
	// Keep just the first helper's valid FDE, as in a library whose C/C++
	// functions omit unwind tables but linked assembly still supplies them.
	var selected *chromiumSample
	for name, sample := range f.functions {
		if strings.HasPrefix(name, "helper_") && (selected == nil || sample.address < selected.address) {
			selected = sample
		}
	}
	b := binary.LittleEndian
	base := b.Uint64(f.data[64+16:])
	b.PutUint32(f.data[f.header+8:], 1)
	b.PutUint32(f.data[f.header+12:], uint32(selected.address-(base+f.header)))
	b.PutUint32(f.data[f.header+16:], uint32(selected.frame-f.header))
	b.PutUint64(f.data[f.sections+2*64+32:], 20)
}

func (f chromiumFixture) mergeLoadSegments() {
	// Match --no-rosegment: .rodata, unwind data and .text share one RX load.
	data, code := f.data[64:64+56], f.data[64+56:64+2*56]
	size := binary.LittleEndian.Uint64(code[8:]) + binary.LittleEndian.Uint64(code[32:])
	binary.LittleEndian.PutUint16(f.data[56:], 1)
	binary.LittleEndian.PutUint32(data[4:], uint32(elf.PF_R|elf.PF_X))
	binary.LittleEndian.PutUint64(data[32:], size)
	binary.LittleEndian.PutUint64(data[40:], size)
}

func resolveFixture(ctx context.Context, fixture chromiumFixture) (map[string]uint64, error) {
	file, err := elf.NewFile(contextReaderAt{ctx, bytes.NewReader(fixture.data)})
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return resolveChromium(ctx, file)
}

func TestChromiumResolvesRelocatedStrippedLibrary(t *testing.T) {
	for _, seed := range []int64{1, 7, 23, 71} {
		for _, sharedLoad := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/shared-load=%t", seed, sharedLoad), func(t *testing.T) {
				fixture := makeChromiumFixture(t, seed, false)
				if sharedLoad {
					fixture.mergeLoadSegments()
				}
				resolved, err := resolveFixture(context.Background(), fixture)
				if err != nil {
					t.Fatal(err)
				}
				want := make(map[string]uint64)
				base := uint64(0x10000000) + uint64(seed)*0x1000000
				for _, name := range []string{"SSL_new", "SSL_free", "SSL_read", "SSL_write"} {
					want[name] = fixture.functions[name].address - base
				}
				if !reflect.DeepEqual(resolved, want) {
					t.Fatalf("resolved %v; expected %v", resolved, want)
				}
			})
		}
	}
}

func TestChromiumReadDoesNotDependOnSavedRegistersOrFieldOffsets(t *testing.T) {
	fixture := makeChromiumFixture(t, 11, false)
	code := fixture.functions["SSL_read"].code
	rename := func(word uint32, shift uint) uint32 {
		register := (word >> shift) & 31
		if register == 19 || register == 20 {
			word = word&^(uint32(31)<<shift) | (register+8)<<shift
		}
		return word
	}
	for offset := 0; offset < len(code); offset += 4 {
		word := binary.LittleEndian.Uint32(code[offset:])
		if memory, ok := armLoadStore(word); ok {
			word = rename(rename(word, 0), 5)
			if memory.pair {
				word = rename(word, 10)
			}
			if memory.base != 31 && memory.base != 29 {
				// Shift the object's fields together; no layout offset is fixed.
				if memory.pair {
					word += 8 << 15
				} else {
					word += 8 << 10
				}
			}
		} else if word&0x7fe0ffe0 == 0x2a0003e0 {
			word = rename(rename(word, 0), 16)
		} else if word&0x1f000000 == 0x0b000000 {
			word = rename(rename(rename(word, 0), 5), 16)
		} else if word&0x1f000000 == 0x11000000 {
			word = rename(rename(word, 0), 5)
		}
		binary.LittleEndian.PutUint32(code[offset:], word)
	}
	if _, err := resolveFixture(context.Background(), fixture); err != nil {
		t.Fatalf("register or private field relocation broke recognition: %v", err)
	}
}

func TestChromiumARM64Decoding(t *testing.T) {
	if target, call, ok := armBranch(0x94000018, 0x4cd3270); !ok || !call || target != 0x4cd32d0 {
		t.Fatal("forward BL decoding failed")
	}
	if target, call, ok := armBranch(0x17ffffc3, 0x4cd3734); !ok || call || target != 0x4cd3640 {
		t.Fatal("backward B decoding failed")
	}
	if memory, ok := armLoadStore(0xf9401808); !ok || !memory.load || memory.width != 8 || memory.base != 0 || memory.first != 8 || memory.offset != 48 {
		t.Fatalf("LDR decoding failed: %+v", memory)
	}
	if memory, ok := armLoadStore(0xa908250a); !ok || memory.load || !memory.pair || memory.base != 8 || memory.first != 10 || memory.second != 9 || memory.offset != 128 {
		t.Fatalf("STP decoding failed: %+v", memory)
	}
	r := armArguments()
	if !r.move(0x2a1f03e1, 0) || r[1].kind != armConstant || r[1].id != 0 || !r[1].narrow {
		t.Fatal("MOV W1, WZR decoding failed")
	}
}

func TestChromiumRejectsUnverifiedFunctions(t *testing.T) {
	mutateWord := func(f chromiumFixture, name string, offset uint64, word uint32) {
		binary.LittleEndian.PutUint32(f.functions[name].code[offset:], word)
	}
	cases := map[string]func(chromiumFixture){
		"non-consuming peek": func(f chromiumFixture) { mutateWord(f, "SSL_read", 0x44, 0xd503201f) },
		"Boolean return":     func(f chromiumFixture) { mutateWord(f, "SSL_read", 0x5c, 0x52800020) },
		"unsigned read result": func(f chromiumFixture) {
			code := f.functions["SSL_read"].code
			binary.LittleEndian.PutUint32(code[0x20:], binary.LittleEndian.Uint32(code[0x20:])&^uint32(15)|3)
		},
		"different SSL pointer":      func(f chromiumFixture) { mutateWord(f, "SSL_read", 0x10, 0xaa0103f4) },
		"no allocation guard":        func(f chromiumFixture) { mutateWord(f, "SSL_new", 0x14, 0xd503201f) },
		"wrong error argument":       func(f chromiumFixture) { mutateWord(f, "SSL_new", 0x54, 0x52800022) },
		"missing destructor":         func(f chromiumFixture) { mutateWord(f, "SSL_free", 0x20, 0xd503201f) },
		"free another pointer":       func(f chromiumFixture) { mutateWord(f, "SSL_free", 0x24, 0xaa1403e0) },
		"overwritten integer length": func(f chromiumFixture) { mutateWord(f, "SSL_write", 0x38, 0x52800033) },
		"overwritten socket pointer": func(f chromiumFixture) { mutateWord(f, "Chrome_Write", 0x38, 0xaa1403f3) },
		"different socket slot": func(f chromiumFixture) {
			code := f.functions["Chrome_Write"].code
			binary.LittleEndian.PutUint32(code[0x48:], binary.LittleEndian.Uint32(code[0x48:])+(1<<10))
		},
		"unwind count overflow":       func(f chromiumFixture) { binary.LittleEndian.PutUint32(f.data[f.header+8:], ^uint32(0)) },
		"unwind range mismatch":       func(f chromiumFixture) { binary.LittleEndian.PutUint32(f.data[f.functions["SSL_read"].frame+8:], 0) },
		"unsupported unwind encoding": func(f chromiumFixture) { f.data[f.frames+16] = 0x1c },
		"non-executable text":         func(f chromiumFixture) { binary.LittleEndian.PutUint32(f.data[64+56+4:], uint32(elf.PF_R)) },
		"unreadable text load":        func(f chromiumFixture) { binary.LittleEndian.PutUint32(f.data[64+56+4:], uint32(elf.PF_X)) },
		"unreadable data load":        func(f chromiumFixture) { binary.LittleEndian.PutUint32(f.data[64+4:], uint32(elf.PF_X)) },
		"rodata exceeds file-backed load": func(f chromiumFixture) {
			binary.LittleEndian.PutUint64(f.data[64+32:], 0x3fff) // Still within p_memsz.
		},
		"rodata file/virtual mismatch": func(f chromiumFixture) {
			binary.LittleEndian.PutUint64(f.data[f.sections+64+24:], 0x1004)
		},
		"executable rodata section": func(f chromiumFixture) {
			binary.LittleEndian.PutUint64(f.data[f.sections+64+8:], uint64(elf.SHF_ALLOC|elf.SHF_EXECINSTR))
		},
		"writable rodata section": func(f chromiumFixture) {
			binary.LittleEndian.PutUint64(f.data[f.sections+64+8:], uint64(elf.SHF_ALLOC|elf.SHF_WRITE))
		},
		"text section marked as data": func(f chromiumFixture) {
			binary.LittleEndian.PutUint64(f.data[f.sections+4*64+8:], uint64(elf.SHF_ALLOC))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := makeChromiumFixture(t, 5, false)
			mutate(fixture)
			if resolved, err := resolveFixture(context.Background(), fixture); err == nil || len(resolved) != 0 {
				t.Fatalf("unverified functions were accepted: %v, %v", resolved, err)
			}
		})
	}
	fixture := makeChromiumFixture(t, 5, true)
	if _, err := resolveFixture(context.Background(), fixture); err == nil || !strings.Contains(err.Error(), "write") {
		t.Fatalf("duplicate SSL_write was not rejected: %v", err)
	}
}

func TestChromiumAPKAddressesAreFileOffsets(t *testing.T) {
	fixture := makeChromiumFixture(t, 19, false)
	fixture.mergeLoadSegments()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	padding, _ := writer.CreateHeader(&zip.FileHeader{Name: "assets/padding", Method: zip.Store})
	_, _ = padding.Write(bytes.Repeat([]byte{0x51}, 16421))
	entry, _ := writer.CreateHeader(&zip.FileHeader{Name: "lib/arm64-v8a/libchrome.so", Method: zip.Store})
	_, _ = entry.Write(fixture.data)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatal(err)
	}
	start, err := reader.File[1].DataOffset()
	if err != nil {
		t.Fatal(err)
	}
	file, err := elf.NewFile(io.NewSectionReader(bytes.NewReader(archive.Bytes()), start, int64(reader.File[1].UncompressedSize64)))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	resolved, err := resolveChromium(context.Background(), file)
	if err != nil {
		t.Fatal(err)
	}
	for name, offset := range resolved {
		address := uint64(start) + offset
		if !bytes.Equal(archive.Bytes()[address:address+4], fixture.functions[name].code[:4]) {
			t.Fatalf("%s does not point to code inside the stored APK entry", name)
		}
	}
}

func TestChromiumCancellation(t *testing.T) {
	fixture := makeChromiumFixture(t, 3, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := resolveFixture(ctx, fixture); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled library resolution continued: %v", err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	file, err := elf.NewFile(bytes.NewReader(fixture.data))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	image, err := openChromiumImage(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	visited := 0
	err = image.scan(func(uint64, uint32) error {
		visited++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || visited > 256*1024/4 {
		t.Fatalf("text scan ignored cancellation: visited=%d error=%v", visited, err)
	}
}
