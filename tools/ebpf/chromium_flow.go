package main

import (
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"

	"golang.org/x/arch/arm64/arm64asm"
)

var errChromiumCodeRange = errors.New("no verified ARM64 function covers the source reference")

// Cronet can use -fno-unwind-tables while linked assembly still supplies a
// sparse .eh_frame_hdr. A gap is not a corrupt FDE. For these gaps, a direct
// BL supplies an entry and a bounded control-flow walk supplies the extent.
// Never infer an entry from a prologue, source line, neighboring RET or version.
// https://chromium.googlesource.com/chromium/src/+/48db307645dcbaa0bb5ccee0cd096cf22971bb84/build/config/compiler/BUILD.gn#495
func (c *chromiumImage) loadCodeEntries() error {
	if c.codeEntries != nil {
		return nil
	}
	if err := c.loadPLTImports(); err != nil {
		return err
	}
	entries := make(map[uint64]bool)
	if err := c.scan(func(pc uint64, word uint32) error {
		target, call, branch := armBranch(word, pc)
		if branch && call && target >= c.text.Addr && target-c.text.Addr < c.text.Size {
			entries[target] = true
			if len(entries) > maxChromiumFunctions {
				return errors.New("too many ARM64 call targets")
			}
		}
		return nil
	}); err != nil {
		return err
	}
	c.codeEntries = make([]uint64, 0, len(entries))
	for address := range entries {
		c.codeEntries = append(c.codeEntries, address)
	}
	sort.Slice(c.codeEntries, func(i, j int) bool { return c.codeEntries[i] < c.codeEntries[j] })
	c.codeFailures = make(map[uint64]error)
	return nil
}

func (c *chromiumImage) codeFunction(pc uint64) (*chromiumFunction, error) {
	if err := c.loadCodeEntries(); err != nil {
		return nil, err
	}
	index := sort.Search(len(c.codeEntries), func(i int) bool { return c.codeEntries[i] > pc }) - 1
	if index < 0 {
		return nil, errChromiumCodeRange
	}
	start := c.codeEntries[index]
	if f := c.functions[start]; f != nil {
		if pc < f.end() {
			return f, nil
		}
		return nil, errChromiumCodeRange
	}
	if err := c.codeFailures[start]; err != nil {
		return nil, err
	}
	if len(c.functions)+len(c.codeFailures) >= maxChromiumCandidates {
		return nil, errors.New("too many Chromium function candidates")
	}
	// An entry inside an existing FDE cannot extend that FDE to cover a gap.
	if _, err := c.unwindFunction(start); !errors.Is(err, errChromiumUnwindGap) {
		if err != nil {
			return nil, err
		}
		return nil, errChromiumCodeRange
	}
	limit := start + min(c.text.Size-(start-c.text.Addr), uint64(maxChromiumFunctionSize))
	if index+1 < len(c.codeEntries) {
		limit = min(limit, c.codeEntries[index+1])
	}
	if next := sort.Search(len(c.table)/8, func(i int) bool { address, _ := c.entry(i); return address > start }); next < len(c.table)/8 {
		address, _ := c.entry(next)
		limit = min(limit, address)
	}
	if pc >= limit {
		return nil, errChromiumCodeRange
	}
	code := make([]byte, limit-start)
	if _, err := c.text.ReadAt(code, int64(start-c.text.Addr)); err != nil {
		return nil, err
	}
	f, err := c.walkCode(start, code)
	if err != nil {
		c.codeFailures[start] = err
		return nil, err
	}
	c.functions[start] = f
	if pc >= f.end() {
		return nil, errChromiumCodeRange
	}
	return f, nil
}

func (c *chromiumImage) walkCode(start uint64, code []byte) (*chromiumFunction, error) {
	visited := make([]bool, len(code)/4)
	queue := []uint64{start}
	end, exits := start, 0
	reject := func(pc uint64, reason string) (*chromiumFunction, error) {
		return nil, fmt.Errorf("%w: %#x at %#x: %s", errChromiumCodeRange, start, pc, reason)
	}
	for len(queue) > 0 {
		if err := c.ctx.Err(); err != nil {
			return nil, err
		}
		pc := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if pc < start || pc-start >= uint64(len(code)) || pc%4 != 0 {
			return reject(pc, "control flow leaves the bounded range")
		}
		index := (pc - start) / 4
		if visited[index] {
			continue
		}
		visited[index] = true
		end = max(end, pc+4)
		word := binary.LittleEndian.Uint32(code[pc-start:])
		if armReturn(word) || armTrap(word) {
			exits++
			continue
		}
		if target, call, branch := armBranch(word, pc); branch {
			if call {
				if _, ok := executableOffset(c.file, target); !ok {
					return reject(pc, "call target is outside executable data")
				}
				if c.noReturnCall(target) {
					exits++
					continue
				}
			} else if target < start || target-start >= uint64(len(code)) {
				i := sort.Search(len(c.codeEntries), func(i int) bool { return c.codeEntries[i] >= target })
				if i == len(c.codeEntries) || c.codeEntries[i] != target {
					return reject(pc, "tail call has no independently observed entry")
				}
				exits++
				continue
			} else {
				queue = append(queue, target)
				continue
			}
		} else if target, conditional := armConditional(word, pc); conditional {
			queue = append(queue, target)
		} else if word&0xfffffc1f == 0xd63f0000 { // BLR returns to the next instruction.
		} else if word&0xfe000000 == 0xd6000000 || word&0xff000000 == 0xd4000000 ||
			word&0xff000000 == 0x54000000 || word&0xffff0000 == 0 {
			return reject(pc, "unresolved indirect branch or exception")
		} else if !armHint(word) {
			if _, err := arm64asm.Decode(code[pc-start : pc-start+4]); err != nil {
				return reject(pc, "undecodable instruction")
			}
		}
		queue = append(queue, pc+4)
	}
	if exits == 0 {
		return reject(start, "no bounded exit")
	}
	// Linear ABI checks must never see instructions from an unreachable literal
	// island or an adjacent function. Only architectural padding is allowed.
	for pc := start; pc < end; pc += 4 {
		word := binary.LittleEndian.Uint32(code[pc-start:])
		// Chromium's ImmediateCrash uses BRK followed by HLT as a fallback.
		if !visited[(pc-start)/4] && word != 0xd503201f && !armTrap(word) {
			return reject(pc, "unreachable bytes inside the function")
		}
	}
	return &chromiumFunction{start, code[:end-start]}, nil
}

// A BL to abort at the end of a function has no fallthrough. Verify that it
// really names a non-returning libc import using the ELF relocation and PLT
// instructions; a last BL by itself is not evidence of a function boundary.
func (c *chromiumImage) loadPLTImports() error {
	if c.pltImports != nil {
		return nil
	}
	imports := make(map[uint64]string)
	relocations := c.file.Section(".rela.plt")
	if relocations == nil {
		c.pltImports = imports
		return nil
	}
	if relocations.Type != elf.SHT_RELA || relocations.Entsize != 24 || relocations.Size > 1024*1024 || relocations.Size%24 != 0 {
		return errors.New("unsupported ARM64 PLT relocation table")
	}
	if uint64(relocations.Link) >= uint64(len(c.file.Sections)) || c.file.Sections[relocations.Link] != c.file.Section(".dynsym") {
		return errors.New("PLT relocation symbols do not match .dynsym")
	}
	symbols, err := c.file.DynamicSymbols()
	if err != nil {
		return err
	}
	data, err := relocations.Data()
	if err != nil {
		return err
	}
	seen := make(map[uint64]bool)
	for at := 0; at < len(data); at += 24 {
		info := binary.LittleEndian.Uint64(data[at+8:])
		index := uint32(info >> 32)
		if uint32(info) != uint32(elf.R_AARCH64_JUMP_SLOT) || index == 0 || uint64(index) > uint64(len(symbols)) {
			continue
		}
		address := binary.LittleEndian.Uint64(data[at:])
		if seen[address] || binary.LittleEndian.Uint64(data[at+16:]) != 0 {
			return errors.New("conflicting or unsupported PLT import relocation")
		}
		seen[address] = true
		symbol := symbols[index-1]
		if symbol.Section == elf.SHN_UNDEF && (symbol.Name == "abort" || symbol.Name == "__stack_chk_fail" || symbol.Name == "__assert2" || symbol.Name == "memcpy") {
			imports[address] = symbol.Name
		}
	}
	c.pltImports = imports
	return c.ctx.Err()
}

func (c *chromiumImage) noReturnCall(address uint64) bool {
	switch c.pltImport(address) {
	case "abort", "__stack_chk_fail", "__assert2":
		return true
	default:
		return false
	}
}

func (c *chromiumImage) pltImport(address uint64) string {
	if len(c.pltImports) == 0 {
		return ""
	}
	plt, err := chromiumSection(c.file, ".plt", 1024*1024, true)
	if err != nil || address < plt.Addr || address-plt.Addr >= plt.Size {
		return ""
	}
	var code [32]byte
	size := min(uint64(len(code)), plt.Size-(address-plt.Addr))
	if _, err := plt.ReadAt(code[:size], int64(address-plt.Addr)); err != nil {
		return ""
	}
	var r armRegisters
	var imported [32]string
	for at := uint64(0); at+4 <= size; at += 4 {
		word := binary.LittleEndian.Uint32(code[at:])
		if word&0xfffffc1f == 0xd61f0000 { // BR Xn.
			return imported[(word>>5)&31]
		}
		if m, ok := armLoadStore(word); ok && m.load && !m.pair && !m.update && m.width == 8 {
			base := r[m.base]
			imported[m.first] = ""
			if base.kind == armConstant {
				imported[m.first] = c.pltImports[addSigned(base.id, m.offset)]
			}
			r[m.first] = armValue{}
		} else if !r.move(word, address+at) {
			return ""
		} else if !armHint(word) {
			imported[word&31] = ""
		}
	}
	return ""
}
