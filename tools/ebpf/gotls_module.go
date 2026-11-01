package main

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"errors"
)

// Go 1.26/1.27 no longer store runtime.text in pcHeader. Their moduledata prefix is
// one pointer, six slices, findfunctab, minpc/maxpc, then text/etext (192 bytes
// on our two supported 64-bit ABIs). The layout and its linker initialization:
// https://github.com/golang/go/blob/go1.26.10/src/runtime/symtab.go
// https://github.com/golang/go/blob/go1.26.10/src/cmd/link/internal/ld/symtab.go
// https://github.com/golang/go/blob/go1.27.3/src/runtime/symtab.go
// https://github.com/golang/go/blob/go1.27.3/src/cmd/link/internal/ld/symtab.go
// Go 1.27 changes type metadata after this prefix, not these address fields.
//
// Locate the prefix by ftab's length/capacity, which do not need relocation.
// Then verify all six slices, pcHeader, and both PC bounds. This works in
// stripped ELF and external PIE without guessing that runtime.text == .text.
func goModuleTextAddress(ctx context.Context, file *elf.File, pclnSection *elf.Section, pcln []byte, at int) (uint64, error) {
	if at < 0 || at > len(pcln)-72 || pclnSection.Addr > ^uint64(0)-uint64(len(pcln)) {
		return 0, errors.New("invalid Go module function table")
	}
	header := pcln[at:]
	headerAddress := pclnSection.Addr + uint64(at)
	read := binary.LittleEndian.Uint64
	nfunc := read(header[8:])
	if nfunc == 0 || nfunc > 200000 {
		return 0, errors.New("invalid Go module function count")
	}
	var tables [5]uint64
	for i := range tables {
		tables[i] = read(header[32+i*8:])
		if tables[i] < 72 || tables[i] > uint64(len(header)) {
			return 0, errors.New("invalid Go module table offset")
		}
	}
	if uint64(len(header))-tables[4] < nfunc*8+4 {
		return 0, errors.New("truncated Go module function index")
	}
	firstPC := uint64(binary.LittleEndian.Uint32(header[tables[4]:]))
	lastPC := uint64(binary.LittleEndian.Uint32(header[tables[4]+nfunc*8:]))
	text := file.Section(".text")
	if text == nil || lastPC <= firstPC {
		return 0, errors.New("invalid Go module executable bounds")
	}
	var found, scanned uint64
	var candidates int
	for _, section := range file.Sections {
		if section.Type != elf.SHT_PROGBITS || section.Flags&(elf.SHF_ALLOC|elf.SHF_WRITE|elf.SHF_EXECINSTR) != elf.SHF_ALLOC|elf.SHF_WRITE || section.Size < 192 {
			continue
		}
		if section.Size > 32*1024*1024 || section.Size > 64*1024*1024-scanned || section.Addr > ^uint64(0)-section.Size {
			return 0, errors.New("Go module data scan limit exceeded")
		}
		scanned += section.Size
		data, err := section.Data()
		if err != nil {
			return 0, err
		}
		for offset := int((8 - section.Addr%8) % 8); offset <= len(data)-192; offset += 8 {
			if offset%65536 < 8 {
				if err := ctx.Err(); err != nil {
					return 0, err
				}
			}
			prefix := data[offset : offset+192]
			if read(prefix[136:]) != nfunc+1 || read(prefix[144:]) != nfunc+1 {
				continue
			}
			candidates++
			if candidates > 16 {
				return 0, errors.New("too many Go module data candidates")
			}
			base := section.Addr + uint64(offset)
			values := make(map[uint64]uint64)
			for _, field := range []uint64{0, 8, 32, 56, 80, 104, 128, 160, 168, 176, 184} {
				values[base+field] = read(prefix[field:])
			}
			if err := relocateGoAddresses(ctx, file, values); err != nil {
				if ctx.Err() != nil {
					return 0, ctx.Err()
				}
				continue
			}
			if values[base] != headerAddress || values[base+128] != headerAddress+tables[4] {
				continue
			}
			valid := true
			for i, tableOffset := range tables {
				field := uint64(8 + i*24)
				length, capacity := read(prefix[field+8:]), read(prefix[field+16:])
				unit := uint64(1)
				if i == 1 { // cutab is []uint32; the other tables are []byte.
					unit = 4
				}
				if values[base+field] != headerAddress+tableOffset || length != capacity || length > (uint64(len(header))-tableOffset)/unit {
					valid = false
					break
				}
			}
			start, end := values[base+176], values[base+184]
			if !valid || start == 0 || start < text.Addr || end <= start || end < text.Addr || end-text.Addr > text.Size || lastPC > end-start {
				continue
			}
			if values[base+160] != start+firstPC || values[base+168] != start+lastPC {
				continue
			}
			if found != 0 && found != start {
				return 0, errors.New("conflicting Go module text addresses")
			}
			found = start
		}
	}
	if found == 0 {
		return 0, errors.New("Go moduledata is missing or does not match pclntab")
	}
	return found, ctx.Err()
}
