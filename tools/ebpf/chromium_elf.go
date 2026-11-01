package main

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

// Chromium's Android libraries normally retain unwind information and source
// filenames, even when all of BoringSSL's public symbols have been stripped.
// These limits are independent of the APK version and keep discovery bounded.
const (
	maxChromiumFunctions    = 1024 * 1024
	maxChromiumCandidates   = 1024
	maxChromiumFunctionSize = 64 * 1024
)

type chromiumFunction struct {
	address uint64
	code    []byte
}

func (f *chromiumFunction) end() uint64 { return f.address + uint64(len(f.code)) }
func (f *chromiumFunction) word(pc uint64) uint32 {
	return binary.LittleEndian.Uint32(f.code[pc-f.address:])
}

type chromiumImage struct {
	ctx          context.Context
	file         *elf.File
	text, frames *elf.Section
	header       *elf.Section
	table        []byte
	functions    map[uint64]*chromiumFunction
	sslFiles     map[uint64]bool
	socketFiles  map[uint64]bool
	codeEntries  []uint64
	codeFailures map[uint64]error
	pltImports   map[uint64]string
}

var errChromiumUnwindGap = errors.New("probe site is not covered by unwind information")

func chromiumSection(file *elf.File, name string, limit uint64, executable bool) (*elf.Section, error) {
	s := file.Section(name)
	if s == nil || s.Type != elf.SHT_PROGBITS || s.Size == 0 || s.Size > limit ||
		s.Addr > ^uint64(0)-s.Size || s.Offset > ^uint64(0)-s.Size ||
		s.Flags&elf.SHF_ALLOC == 0 || s.Flags&(elf.SHF_COMPRESSED|elf.SHF_WRITE) != 0 ||
		(s.Flags&elf.SHF_EXECINSTR != 0) != executable {
		return nil, fmt.Errorf("missing or unsupported %s", name)
	}
	// Android Chromium uses --no-rosegment, so read-only sections may share
	// an executable PT_LOAD with .text. Section flags still distinguish data
	// from code; a section table alone must never create an executable probe site.
	for _, p := range file.Progs {
		if p.Type == elf.PT_LOAD && p.Flags&elf.PF_R != 0 &&
			(!executable || p.Flags&elf.PF_X != 0) && s.Addr >= p.Vaddr && s.Offset >= p.Off &&
			s.Addr-p.Vaddr == s.Offset-p.Off && s.Addr-p.Vaddr <= p.Filesz &&
			s.Size <= p.Filesz-(s.Addr-p.Vaddr) {
			return s, nil
		}
	}
	return nil, fmt.Errorf("%s is outside its load segment", name)
}

func sourceFilenames(data []byte, base uint64, suffix string) (map[uint64]bool, error) {
	result := make(map[uint64]bool)
	needle := []byte(suffix + "\x00")
	for offset := 0; offset < len(data); {
		index := bytes.Index(data[offset:], needle)
		if index < 0 {
			break
		}
		index += offset
		start := index
		for start > 0 && data[start-1] != 0 && index-start < 512 {
			start--
		}
		if index-start >= 512 || (start > 0 && data[start-1] != 0) {
			return nil, errors.New("oversized source filename")
		}
		result[base+uint64(start)] = true
		if len(result) > 8 {
			return nil, errors.New("ambiguous source filenames")
		}
		offset = index + len(needle)
	}
	return result, nil
}

func openChromiumImage(ctx context.Context, file *elf.File) (*chromiumImage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if file.Class != elf.ELFCLASS64 || file.Data != elf.ELFDATA2LSB || file.Machine != elf.EM_AARCH64 {
		return nil, errors.New("stripped Chromium recognition requires ARM64")
	}
	rodata, err := chromiumSection(file, ".rodata", 32*1024*1024, false)
	if err != nil {
		return nil, err
	}
	data, err := rodata.Data()
	if err != nil {
		return nil, err
	}
	sslFiles, err := sourceFilenames(data, rodata.Addr, "boringssl/src/ssl/ssl_lib.cc")
	if err != nil {
		return nil, err
	}
	socketFiles, err := sourceFilenames(data, rodata.Addr, "net/socket/ssl_client_socket_impl.cc")
	if err != nil {
		return nil, err
	}
	if len(sslFiles) == 0 || len(socketFiles) == 0 {
		return nil, errors.New("BoringSSL and Chromium socket source anchors are unavailable")
	}
	text, err := chromiumSection(file, ".text", 224*1024*1024, true)
	if err != nil {
		return nil, err
	}
	if text.Addr%4 != 0 || text.Size%4 != 0 {
		return nil, errors.New("unaligned ARM64 text")
	}
	header, err := chromiumSection(file, ".eh_frame_hdr", 12+8*maxChromiumFunctions, false)
	if err != nil {
		return nil, err
	}
	frames, err := chromiumSection(file, ".eh_frame", 48*1024*1024, false)
	if err != nil {
		return nil, err
	}
	table, err := header.Data()
	if err != nil {
		return nil, err
	}
	// DWARF v1 header: pcrel/sdata4 frame pointer, udata4 count and
	// datarel/sdata4 search table. Do not guess unfamiliar unwind encodings.
	if len(table) < 12 || !bytes.Equal(table[:4], []byte{1, 0x1b, 3, 0x3b}) ||
		addSigned(header.Addr+4, int64(int32(binary.LittleEndian.Uint32(table[4:])))) != frames.Addr {
		return nil, errors.New("unsupported unwind header encoding")
	}
	count := uint64(binary.LittleEndian.Uint32(table[8:]))
	if count == 0 || count > maxChromiumFunctions || 12+count*8 != uint64(len(table)) {
		return nil, errors.New("invalid unwind table size")
	}
	image := &chromiumImage{ctx: ctx, file: file, text: text, frames: frames, header: header,
		table: table[12:], functions: make(map[uint64]*chromiumFunction), sslFiles: sslFiles, socketFiles: socketFiles}
	var previous uint64
	for index := 0; index < int(count); index++ {
		if index%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		start, frame := image.entry(index)
		if start%4 != 0 || start <= previous || frame < frames.Addr || frame-frames.Addr >= frames.Size {
			return nil, errors.New("invalid or unsorted unwind table")
		}
		previous = start
	}
	return image, nil
}

func addSigned(base uint64, delta int64) uint64 { return base + uint64(delta) }

func (c *chromiumImage) entry(index int) (uint64, uint64) {
	data := c.table[index*8:]
	return addSigned(c.header.Addr, int64(int32(binary.LittleEndian.Uint32(data)))),
		addSigned(c.header.Addr, int64(int32(binary.LittleEndian.Uint32(data[4:]))))
}

func (c *chromiumImage) frameRecord(address uint64) ([]byte, error) {
	if c.frames.Size < 4 || address < c.frames.Addr || address-c.frames.Addr > c.frames.Size-4 {
		return nil, errors.New("unwind record is outside .eh_frame")
	}
	var length [4]byte
	if _, err := c.frames.ReadAt(length[:], int64(address-c.frames.Addr)); err != nil {
		return nil, err
	}
	size := uint64(binary.LittleEndian.Uint32(length[:]))
	if size < 12 || size > 4096 || size+4 > c.frames.Size-(address-c.frames.Addr) {
		return nil, errors.New("invalid unwind record size")
	}
	data := make([]byte, size)
	_, err := c.frames.ReadAt(data, int64(address-c.frames.Addr+4))
	return data, err
}

func (c *chromiumImage) function(pc uint64) (*chromiumFunction, error) {
	f, err := c.unwindFunction(pc)
	if errors.Is(err, errChromiumUnwindGap) {
		return c.codeFunction(pc)
	}
	return f, err
}

func (c *chromiumImage) unwindFunction(pc uint64) (*chromiumFunction, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	if pc%4 != 0 || pc < c.text.Addr || pc-c.text.Addr >= c.text.Size {
		return nil, errors.New("probe site is outside ARM64 text")
	}
	index := sort.Search(len(c.table)/8, func(i int) bool { start, _ := c.entry(i); return start > pc }) - 1
	if index < 0 {
		return nil, errChromiumUnwindGap
	}
	start, frame := c.entry(index)
	if f := c.functions[start]; f != nil {
		if pc < f.end() {
			return f, nil
		}
		return nil, errChromiumUnwindGap
	}
	fde, err := c.frameRecord(frame)
	if err != nil {
		return nil, err
	}
	cieAddress := frame + 4 - uint64(binary.LittleEndian.Uint32(fde))
	if cieAddress >= frame {
		return nil, errors.New("invalid unwind CIE reference")
	}
	cie, err := c.frameRecord(cieAddress)
	if err != nil {
		return nil, err
	}
	// The Android clang layout uses zR, code alignment 1, data alignment -4
	// or -8, x30 return address and pcrel/sdata4 FDE addresses. CFI instructions
	// after the augmentation are deliberately not used as byte signatures.
	if len(cie) < 13 || !bytes.Equal(cie[:9], []byte{0, 0, 0, 0, 1, 'z', 'R', 0, 1}) ||
		(cie[9] != 0x7c && cie[9] != 0x78) || !bytes.Equal(cie[10:13], []byte{30, 1, 0x1b}) {
		return nil, errors.New("unsupported unwind CIE encoding")
	}
	actualStart := addSigned(frame+8, int64(int32(binary.LittleEndian.Uint32(fde[4:]))))
	size := uint64(binary.LittleEndian.Uint32(fde[8:]))
	if actualStart != start || size == 0 || size%4 != 0 || size > maxChromiumFunctionSize ||
		start < c.text.Addr || start-c.text.Addr > c.text.Size || size > c.text.Size-(start-c.text.Addr) {
		return nil, fmt.Errorf("invalid unwind function range at %#x (FDE %#x, start %#x, size %#x)", pc, frame, actualStart, size)
	}
	if index+1 < len(c.table)/8 {
		next, _ := c.entry(index + 1)
		if size > next-start {
			return nil, errors.New("overlapping unwind functions")
		}
	}
	if pc-start >= size {
		return nil, errChromiumUnwindGap
	}
	if len(c.functions) >= maxChromiumCandidates {
		return nil, errors.New("too many Chromium function candidates")
	}
	f := &chromiumFunction{start, make([]byte, size)}
	if _, err := c.text.ReadAt(f.code, int64(start-c.text.Addr)); err != nil {
		return nil, err
	}
	c.functions[start] = f
	return f, nil
}

func (c *chromiumImage) scan(visit func(uint64, uint32) error) error {
	// Stream .text: a current Chrome library is about 200 MiB and must not be
	// retained alongside the collector's perf buffers and assembly queues.
	buffer := make([]byte, 256*1024)
	reader := c.text.Open()
	for offset := uint64(0); offset < c.text.Size; {
		if err := c.ctx.Err(); err != nil {
			return err
		}
		size := min(uint64(len(buffer)), c.text.Size-offset)
		if _, err := io.ReadFull(reader, buffer[:size]); err != nil {
			return err
		}
		for i := uint64(0); i < size; i += 4 {
			if err := visit(c.text.Addr+offset+i, binary.LittleEndian.Uint32(buffer[i:])); err != nil {
				return err
			}
		}
		offset += size
	}
	return c.ctx.Err()
}
