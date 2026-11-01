package main

import (
	"context"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// Public BoringSSL error reasons, not source line numbers or private layouts.
const (
	sslErrorLibrary      = 16
	sslShouldNotBeCalled = 66
	sslBadLength         = 111
	sslNullContext       = 185
	sslHandshakeFailure  = 215
	sslUninitialized     = 226
)

type unsupportedChromiumError struct{ name, reason string }

func (e *unsupportedChromiumError) Error() string {
	return fmt.Sprintf("Automatic TLS function resolution is unavailable for %s: %s; TLS/QUIC domain observations may still be available", e.name, e.reason)
}

func chromiumLibraryName(name string) bool {
	name = filepath.Base(name)
	return name == "libchrome.so" || name == "libwebviewchromium.so" || name == "libtrichrome.so" ||
		strings.HasPrefix(name, "libmonochrome") && strings.HasSuffix(name, ".so")
}

type chromiumErrorSite struct {
	reason uint32
	target uint64
}

// Only static ELF metadata and instruction relationships are consulted here.
// No Chrome version, ELF build ID, code hash or per-release address table is
// accepted as evidence that a function implements the required public ABI.
func resolveChromium(ctx context.Context, file *elf.File) (map[string]uint64, error) {
	c, err := openChromiumImage(ctx, file)
	if err != nil {
		return nil, err
	}
	errorsByFunction := make(map[uint64]map[uint32]uint64)
	socketFunctions := make(map[uint64]*chromiumFunction)
	pages := make(map[uint64]bool)
	for address := range c.sslFiles {
		pages[address&^uint64(4095)] = true
	}
	for address := range c.socketFiles {
		pages[address&^uint64(4095)] = true
	}
	err = c.scan(func(pc uint64, word uint32) error {
		_, address, ok := armAddress(word, pc)
		if !ok || !pages[address&^uint64(4095)] {
			return nil
		}
		filename, referenceEnd, err := c.sourceReference(pc)
		if err != nil || filename == 0 {
			return err
		}
		f, err := c.function(pc)
		if err != nil {
			return err
		}
		if referenceEnd > f.end() {
			return errors.New("source reference crosses an unwind function boundary")
		}
		if c.socketFiles[filename] {
			socketFunctions[f.address] = f
			return nil
		}
		// Constants may precede the source address. Reset at intervening control
		// flow, then follow bounded branches into shared ERR_put_error blocks.
		start := max(f.address, pc-min(pc-f.address, 32))
		if site, ok := c.errorCall(f, start, pc); ok {
			reasons := errorsByFunction[f.address]
			if reasons == nil {
				reasons = make(map[uint32]uint64)
				errorsByFunction[f.address] = reasons
			}
			if prior := reasons[site.reason]; prior != 0 && prior != site.target {
				return errors.New("ambiguous BoringSSL error call targets")
			}
			reasons[site.reason] = site.target
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	selectFunction := func(reasons ...uint32) (*chromiumFunction, uint64, error) {
		var selected *chromiumFunction
		var helper uint64
		for address, found := range errorsByFunction {
			if len(found) != len(reasons) {
				continue
			}
			target := found[reasons[0]]
			match := target != 0
			for _, reason := range reasons {
				match = match && found[reason] == target
			}
			if !match {
				continue
			}
			if selected != nil {
				return nil, 0, errors.New("ambiguous BoringSSL error signatures")
			}
			selected, helper = c.functions[address], target
		}
		if selected == nil {
			return nil, 0, fmt.Errorf("BoringSSL error signature %v was not found", reasons)
		}
		return selected, helper, nil
	}
	allocation, errorHelper, err := selectFunction(sslNullContext)
	if err != nil {
		return nil, err
	}
	write, writeHelper, err := selectFunction(sslShouldNotBeCalled, sslUninitialized, sslHandshakeFailure, sslBadLength)
	if err != nil || writeHelper != errorHelper {
		return nil, errors.New("SSL_write error paths could not be verified")
	}
	readImpl, readHelper, err := selectFunction(sslUninitialized, sslHandshakeFailure)
	if err != nil || readHelper != errorHelper {
		return nil, errors.New("BoringSSL read implementation could not be verified")
	}
	if _, err = c.entryFunction(errorHelper); err != nil {
		return nil, fmt.Errorf("BoringSSL error helper: %w", err)
	}
	var peek *chromiumFunction
	for address, reasons := range errorsByFunction {
		if len(reasons) != 1 || reasons[sslShouldNotBeCalled] != errorHelper {
			continue
		}
		f := c.functions[address]
		if armForwardsArguments(f, readImpl.address) {
			if peek != nil {
				return nil, errors.New("ambiguous SSL_peek wrappers")
			}
			peek = f
		}
	}
	if peek == nil {
		return nil, errors.New("SSL_peek/read call relationship is unavailable")
	}
	allocatedRegister, ok := c.validateAllocation(allocation, errorHelper)
	if !ok || !c.validateWrite(write, errorHelper) {
		return nil, errors.New("SSL_new/SSL_write public argument ABI could not be verified")
	}
	var free *chromiumFunction
	var slot int64
	for _, caller := range socketFunctions {
		for _, call := range armCalls(caller) {
			if call.target != allocation.address {
				continue
			}
			target, member, ok := chromiumReset(caller, call.pc)
			if !ok {
				continue
			}
			candidate, err := c.entryFunction(target)
			if err != nil {
				return nil, err
			}
			destructor, deallocate, ok := chromiumFree(candidate)
			if !ok || !chromiumAllocationCleanup(allocation, allocatedRegister, destructor, deallocate) {
				continue
			}
			if free != nil && (free.address != candidate.address || slot != member) {
				return nil, errors.New("ambiguous Chromium SSL lifetime ownership")
			}
			free, slot = candidate, member
		}
	}
	if free == nil {
		return nil, errors.New("SSL_free and SSL_new lifetime pairing could not be verified")
	}
	var read *chromiumFunction
	writeUsed := false
	checked := make(map[uint64]bool)
	for _, caller := range socketFunctions {
		for _, call := range armCalls(caller) {
			member, ok := chromiumSocketSlot(caller, call.pc)
			if !ok || member != slot {
				continue
			}
			if call.target == write.address {
				writeUsed = true
			}
			if checked[call.target] {
				continue
			}
			checked[call.target] = true
			candidate, err := c.entryFunction(call.target)
			if err != nil {
				return nil, err
			}
			if !chromiumRead(candidate, peek.address) {
				continue
			}
			if read != nil && read.address != candidate.address {
				return nil, errors.New("ambiguous consuming SSL_read wrappers")
			}
			read = candidate
		}
	}
	if read == nil || !writeUsed {
		return nil, errors.New("Chromium socket read/write ownership or consuming read ABI is unsupported")
	}
	result := make(map[string]uint64)
	seen := make(map[uint64]bool)
	for name, f := range map[string]*chromiumFunction{"SSL_new": allocation, "SSL_free": free, "SSL_read": read, "SSL_write": write} {
		offset, ok := executableOffset(file, f.address)
		if !ok || offset == 0 || offset%4 != 0 || seen[offset] {
			return nil, errors.New("invalid or overlapping Chromium probe sites")
		}
		seen[offset], result[name] = true, offset
	}
	return result, ctx.Err()
}

func (c *chromiumImage) entryFunction(address uint64) (*chromiumFunction, error) {
	f, err := c.function(address)
	if err == nil && f.address != address {
		err = errors.New("call target is not an unwind function entry")
	}
	return f, err
}

func (c *chromiumImage) sourceReference(pc uint64) (uint64, uint64, error) {
	var buffer [32]byte
	size := min(uint64(len(buffer)), c.text.Addr+c.text.Size-pc)
	if _, err := c.text.ReadAt(buffer[:size], int64(pc-c.text.Addr)); err != nil {
		return 0, 0, err
	}
	var registers armRegisters
	registers[31] = armValue{kind: armConstant}
	for offset := uint64(0); offset < size; offset += 4 {
		if !registers.move(binary.LittleEndian.Uint32(buffer[offset:]), pc+offset) {
			return 0, 0, nil
		}
		for _, value := range registers[:31] {
			if value.kind == armConstant && !value.narrow && (c.sslFiles[value.id] || c.socketFiles[value.id]) {
				return value.id, pc + offset + 4, nil
			}
		}
	}
	return 0, 0, nil
}

func (c *chromiumImage) errorCall(f *chromiumFunction, start, anchor uint64) (chromiumErrorSite, bool) {
	var r armRegisters
	r[31] = armValue{kind: armConstant}
	anchored := false
	for pc, steps := start, 0; pc >= f.address && pc < f.end() && steps < 40; steps++ {
		anchored = anchored || pc >= anchor
		word := f.word(pc)
		if target, call, branch := armBranch(word, pc); branch {
			if anchored {
				if call {
					if r[0].kind == armConstant && r[0].id == sslErrorLibrary && r[1].kind == armConstant && r[1].id == 0 &&
						r[2].kind == armConstant && r[2].id > 0 && r[2].id <= 4095 && r[3].kind == armConstant &&
						!r[3].narrow && c.sslFiles[r[3].id] {
						return chromiumErrorSite{uint32(r[2].id), target}, true
					}
					return chromiumErrorSite{}, false
				}
				pc = target
				continue
			}
			r = armRegisters{}
		} else if _, branch := armConditional(word, pc); branch || armReturn(word) {
			if anchored {
				return chromiumErrorSite{}, false
			}
			r = armRegisters{}
		} else if !r.simple(word, pc) {
			// In particular, never carry stale constants through an unknown
			// instruction which could have overwritten an error argument.
			r = armRegisters{}
		}
		r[31] = armValue{kind: armConstant}
		pc += 4
	}
	return chromiumErrorSite{}, false
}

func armForwardsArguments(f *chromiumFunction, target uint64) bool {
	type state struct {
		pc uint64
		r  armRegisters
	}
	queue := []state{{f.address, armArguments()}}
	seen := make(map[state]bool)
	for len(queue) > 0 && len(seen) < 512 {
		s := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if s.pc < f.address || s.pc >= f.end() || seen[s] {
			continue
		}
		seen[s] = true
		word := f.word(s.pc)
		if address, call, branch := armBranch(word, s.pc); branch {
			if call {
				if address == target && s.r[0].argument(0, false) && s.r[1].argument(1, false) && s.r[2].argument(2, true) {
					return true
				}
				continue
			}
			s.pc = address
		} else {
			if address, branch := armConditional(word, s.pc); branch {
				queue = append(queue, state{address, s.r})
			} else if !s.r.simple(word, s.pc) {
				continue
			}
			s.pc += 4
		}
		queue = append(queue, s)
	}
	return false
}

func (c *chromiumImage) validateAllocation(f *chromiumFunction, helper uint64) (uint32, bool) {
	r := armArguments()
	guarded := false
	for pc := f.address; pc < f.end() && pc-f.address < 512; pc += 4 {
		word := f.word(pc)
		if target, branch := armConditional(word, pc); branch {
			if word&0xff000000 != 0xb4000000 || !r[word&31].argument(0, false) {
				return 0, false
			}
			site, ok := c.errorCall(f, target, target)
			guarded = ok && site.reason == sslNullContext && site.target == helper
		} else if _, call, branch := armBranch(word, pc); branch {
			if !call || !guarded || r[0].kind != armConstant || r[0].id == 0 {
				return 0, false
			}
			r.clobberCall()
			r[0] = armValue{kind: armResult, id: pc}
			for next := pc + 4; next < f.end() && next-pc <= 16; next += 4 {
				if !r.move(f.word(next), next) {
					break
				}
				for register := uint32(19); register <= 28; register++ {
					if r[register] == r[0] {
						return register, true
					}
				}
			}
			return 0, false
		} else if !r.simple(word, pc) {
			return 0, false
		}
	}
	return 0, false
}

func (c *chromiumImage) validateWrite(f *chromiumFunction, helper uint64) bool {
	r, firstCall, ok := armPrelude(f)
	if !ok {
		return false
	}
	hasSSL, hasBuffer, hasLength := false, false, false
	for register := 19; register <= 28; register++ {
		if r[register].kind == armArgument && !armSavedStable(f, firstCall+4, uint32(register)) {
			return false
		}
		hasSSL = hasSSL || r[register].argument(0, false)
		hasBuffer = hasBuffer || r[register].argument(1, false)
		hasLength = hasLength || r[register].argument(2, true) && r[register].narrow
	}
	if !hasSSL || !hasBuffer || !hasLength {
		return false
	}
	for pc := f.address; pc < f.end(); pc += 4 {
		word := f.word(pc)
		// An int length's sign bit must lead to SSL_R_BAD_LENGTH. This rejects
		// size_t/out-count variants whose entry arguments need different probes.
		if word&0xfff80000 == 0x37f80000 && r[word&31].argument(2, true) && r[word&31].narrow {
			target, _ := armConditional(word, pc)
			site, ok := c.errorCall(f, target, target)
			if ok && site.reason == sslBadLength && site.target == helper {
				return true
			}
		}
	}
	return false
}

func chromiumReset(f *chromiumFunction, call uint64) (uint64, int64, bool) {
	r, firstCall, ok := armPrelude(f)
	if !ok {
		return 0, 0, false
	}
	for register := 19; register <= 28; register++ {
		if r[register].argument(0, false) && !armSavedStable(f, firstCall+4, uint32(register)) {
			return 0, 0, false
		}
	}
	r.clobberCall()
	r[0] = armValue{kind: armResult, id: call}
	var slot int64
	var stored bool
	var skip uint64
	for pc := call + 4; pc < f.end() && pc-call <= 48; pc += 4 {
		word := f.word(pc)
		if memory, ok := armLoadStore(word); ok && !memory.pair && !memory.update && memory.width == 8 && r[memory.base].argument(0, false) {
			if memory.load {
				r[memory.first] = armValue{kind: armLoaded, id: uint64(memory.offset)}
			} else if r[memory.first].kind == armResult && r[memory.first].id == call {
				slot, stored = memory.offset, true
			} else {
				return 0, 0, false
			}
		} else if word&0xff000000 == 0xb4000000 && stored && r[word&31].kind == armLoaded && int64(r[word&31].id) == slot {
			skip, _ = armConditional(word, pc)
		} else if target, linked, branch := armBranch(word, pc); branch {
			return target, slot, linked && stored && skip > pc && skip < f.end() && r[0].kind == armLoaded && int64(r[0].id) == slot
		} else if !r.move(word, pc) {
			return 0, 0, false
		}
	}
	return 0, 0, false
}

func chromiumSocketSlot(f *chromiumFunction, call uint64) (int64, bool) {
	r, firstCall, ok := armPrelude(f)
	if !ok {
		return 0, false
	}
	for register := 19; register <= 28; register++ {
		if r[register].argument(0, false) && !armSavedStable(f, firstCall+4, uint32(register)) {
			return 0, false
		}
	}
	r.clobberCall()
	for pc := max(f.address, call-min(call-f.address, 24)); pc < call; pc += 4 {
		word := f.word(pc)
		if memory, ok := armLoadStore(word); ok && memory.load && !memory.pair && !memory.update {
			value := armValue{}
			if memory.width == 8 && r[memory.base].argument(0, false) {
				value = armValue{kind: armLoaded, id: uint64(memory.offset)}
			}
			r[memory.first] = value
		} else if _, _, branch := armBranch(word, pc); branch {
			r.clobberCall()
		} else if _, branch := armConditional(word, pc); branch {
			r.clobberCall()
		} else if !r.simple(word, pc) {
			return 0, false
		}
	}
	return int64(r[0].id), r[0].kind == armLoaded
}

func chromiumFree(f *chromiumFunction) (uint64, uint64, bool) {
	pc := f.address
	for pc < f.end() && armHint(f.word(pc)) {
		pc += 4
	}
	if pc >= f.end() || f.word(pc)&0xfe00001f != 0xb4000000 {
		return 0, 0, false
	}
	word := f.word(pc)
	target, _ := armConditional(word, pc)
	body, empty := pc+4, target
	if word&(1<<24) != 0 { // CBNZ x0, body; otherwise CBZ x0, empty.
		body, empty = target, pc+4
	}
	if empty < f.address || empty >= f.end() || !armReturn(f.word(empty)) {
		return 0, 0, false
	}
	r := armArguments()
	var destructor, deallocate uint64
	for pc, steps := body, 0; pc >= f.address && pc < f.end() && steps < 64; pc, steps = pc+4, steps+1 {
		word := f.word(pc)
		if target, call, branch := armBranch(word, pc); branch {
			if !r[0].argument(0, false) || target >= f.address && target < f.end() {
				return 0, 0, false
			}
			if destructor == 0 && call {
				destructor = target
				r.clobberCall()
			} else if destructor != 0 && deallocate == 0 && target != destructor {
				deallocate = target
				if !call {
					return destructor, deallocate, true
				}
				r.clobberCall()
			} else {
				return 0, 0, false
			}
		} else if armReturn(word) {
			return destructor, deallocate, destructor != 0 && deallocate != 0
		} else if !r.simple(word, pc) {
			return 0, 0, false
		}
	}
	return 0, 0, false
}

func chromiumReturns(f *chromiumFunction, pc uint64, r armRegisters, expected armValue) bool {
	for steps := 0; pc >= f.address && pc < f.end() && steps < 40; steps++ {
		word := f.word(pc)
		if armReturn(word) {
			return r[0].kind == expected.kind && r[0].id == expected.id &&
				(expected.kind == armConstant || r[0].narrow == expected.narrow)
		}
		if target, call, branch := armBranch(word, pc); branch {
			if call {
				return false
			}
			pc = target
			continue
		}
		if !r.simple(word, pc) {
			return false
		}
		pc += 4
	}
	return false
}

func chromiumAllocationCleanup(f *chromiumFunction, allocated uint32, destructor, deallocate uint64) bool {
	calls := armCalls(f)
	for index := 0; index+1 < len(calls); index++ {
		first, second := calls[index], calls[index+1]
		if first.target != destructor || second.target != deallocate || second.pc-first.pc > 32 || first.pc < f.address+4 {
			continue
		}
		r := armArguments()
		r[allocated] = armValue{kind: armResult, id: f.address}
		if !r.move(f.word(first.pc-4), first.pc-4) || r[0] != r[allocated] {
			continue
		}
		r.clobberCall()
		valid := true
		for pc := first.pc + 4; pc < second.pc; pc += 4 {
			valid = valid && r.move(f.word(pc), pc)
		}
		if !valid || r[0] != r[allocated] {
			continue
		}
		r.clobberCall()
		if chromiumReturns(f, second.pc+4, r, armValue{kind: armConstant}) {
			return true
		}
	}
	return false
}

func chromiumRead(f *chromiumFunction, peek uint64) bool {
	r, call, ok := armPrelude(f)
	if !ok || len(f.code) > 2048 || !r[0].argument(0, false) || !r[1].argument(1, false) || !r[2].argument(2, true) {
		return false
	}
	if target, linked, _ := armBranch(f.word(call), call); !linked || target != peek {
		return false
	}
	r.clobberCall()
	result := armValue{kind: armResult, narrow: true, id: call}
	r[0] = result
	var body, exit uint64
	for pc := call + 4; pc+4 < f.end() && pc-call <= 48; pc += 4 {
		word := f.word(pc)
		if word&0xffc0001f == 0x7100001f && r[(word>>5)&31] == result {
			immediate := (word >> 10) & 4095
			branch := f.word(pc + 4)
			condition := branch & 15
			if branch&0xff000010 != 0x54000000 || !(immediate == 1 && condition == 11 || immediate == 0 && condition == 13) {
				return false
			}
			exit, _ = armConditional(branch, pc+4)
			body = pc + 8
			break
		}
		if !r.move(word, pc) {
			return false
		}
	}
	if body == 0 || exit <= body || exit >= f.end() {
		return false
	}
	type location struct {
		base   armValue
		offset int64
	}
	loads := make(map[uint64]location)
	var advanced, reduced armValue
	for pc := body; pc < exit; pc += 4 {
		word := f.word(pc)
		if memory, ok := armLoadStore(word); ok {
			if memory.update || memory.width != 8 {
				return false
			}
			base := r[memory.base]
			count := 1
			if memory.pair {
				count = 2
			}
			for index := 0; index < count; index++ {
				register := memory.first
				if index == 1 {
					register = memory.second
				}
				where := location{base, memory.offset + int64(index)*8}
				if memory.load {
					if !base.argument(0, false) && base.kind != armLoaded {
						return false
					}
					id := pc + uint64(index)
					loads[id] = where
					r[register] = armValue{kind: armLoaded, id: id}
				} else {
					value := r[register]
					if original, found := loads[value.id]; !found || original != where {
						return false
					}
					switch value.kind {
					case armAdvanced:
						advanced = base
					case armReduced:
						reduced = base
					default:
						return false
					}
				}
			}
		} else if word&0xffe0fc00 == 0x8b000000 || word&0xffe0fc00 == 0xcb000000 {
			left, right := r[(word>>5)&31], r[(word>>16)&31]
			if left.kind != armLoaded || right != result {
				return false
			}
			kind := armAdvanced
			if word&(1<<30) != 0 {
				kind = armReduced
			}
			r[word&31] = armValue{kind: kind, id: left.id}
		} else if target, conditional := armConditional(word, pc); conditional {
			if target < exit || target >= f.end() {
				return false
			}
		} else if _, linked, branch := armBranch(word, pc); branch {
			if !linked || advanced.kind == armUnknown || reduced.kind == armUnknown {
				return false
			}
			r.clobberCall()
		} else if !r.simple(word, pc) {
			return false
		}
	}
	// SSL_read must consume the peeked bytes, preserve the signed byte count,
	// and advance/decrease the same buffer span. SSL_peek and Boolean wrappers
	// cannot satisfy these relationships, regardless of their code addresses.
	return advanced.kind != armUnknown && advanced == reduced && chromiumReturns(f, exit, r, result)
}
