package main

// A deliberately small ARM64 decoder for static recognition, not an emulator.
// Unknown instructions invalidate a match. No instruction is executed and no
// offsets into an application's SSL objects are read by the collector.

func armSigned(value uint32, bits uint) int64 {
	return int64(int32(value<<(32-bits)) >> (32 - bits))
}

func armAddress(word uint32, pc uint64) (uint32, uint64, bool) {
	kind := word & 0x9f000000
	if kind != 0x10000000 && kind != 0x90000000 {
		return 0, 0, false
	}
	imm := armSigned(((word>>29)&3)|((word>>5)&0x7ffff)<<2, 21)
	if kind == 0x90000000 {
		pc &= ^uint64(4095)
		imm <<= 12
	}
	return word & 31, addSigned(pc, imm), true
}

func armBranch(word uint32, pc uint64) (uint64, bool, bool) {
	if word&0x7c000000 != 0x14000000 {
		return 0, false, false
	}
	return addSigned(pc, armSigned(word&0x3ffffff, 26)*4), word>>31 != 0, true
}

func armConditional(word uint32, pc uint64) (uint64, bool) {
	switch {
	case word&0xff000010 == 0x54000000, word&0x7e000000 == 0x34000000:
		return addSigned(pc, armSigned((word>>5)&0x7ffff, 19)*4), true
	case word&0x7e000000 == 0x36000000:
		return addSigned(pc, armSigned((word>>5)&0x3fff, 14)*4), true
	}
	return 0, false
}

func armReturn(word uint32) bool {
	return word == 0xd65f03c0 || word == 0xd65f0bff || word == 0xd65f0fff
}

func armHint(word uint32) bool { return word&0xfffff01f == 0xd503201f }

type armValue struct {
	kind   uint8
	narrow bool
	id     uint64
}

const (
	armUnknown uint8 = iota
	armConstant
	armArgument
	armResult
	armLoaded
	armAdvanced
	armReduced
	armStack
)

type armRegisters [32]armValue

func armArguments() armRegisters {
	var registers armRegisters
	for index := 0; index < 8; index++ {
		registers[index] = armValue{kind: armArgument, id: uint64(index)}
	}
	registers[31] = armValue{kind: armConstant}
	return registers
}

func (r *armRegisters) clobberCall() {
	for index := 0; index <= 18; index++ {
		r[index] = armValue{}
	}
	r[30] = armValue{}
}

func (v armValue) argument(index uint64, narrow bool) bool {
	return v.kind == armArgument && v.id == index && (narrow || !v.narrow)
}

// Move/constant/address operations have the same meaning across register
// allocation changes. Source line numbers are ordinary constants, never keys.
func (r *armRegisters) move(word uint32, pc uint64) bool {
	destination := word & 31
	if armHint(word) {
		return true
	}
	if destination == 31 {
		// ADD/SUB sp, sp, #imm is a stack adjustment, not a write to XZR.
		return word&0x1f000000 == 0x11000000 && word&(1<<29) == 0 && (word>>5)&31 == 31
	}
	if register, address, ok := armAddress(word, pc); ok {
		r[register] = armValue{kind: armConstant, id: address}
		return true
	}
	if word&0x7fe0ffe0 == 0x2a0003e0 { // MOV Wd, Wm / MOV Xd, Xm (ORR alias).
		value := r[(word>>16)&31]
		if word>>31 == 0 {
			value.narrow = true
			if value.kind == armConstant {
				value.id = uint64(uint32(value.id))
			}
		}
		r[destination] = value
		return true
	}
	if word&0x1f800000 == 0x12800000 { // MOVN / MOVZ / MOVK.
		shift := ((word >> 21) & 3) * 16
		if word>>31 == 0 && shift >= 32 {
			return false
		}
		value := uint64((word>>5)&0xffff) << shift
		switch (word >> 29) & 3 {
		case 0:
			value = ^value
		case 2:
		case 3:
			if r[destination].kind != armConstant {
				r[destination] = armValue{}
				return true
			}
			value |= r[destination].id & ^(uint64(0xffff) << shift)
		default:
			return false
		}
		if word>>31 == 0 {
			value = uint64(uint32(value))
		}
		r[destination] = armValue{kind: armConstant, narrow: word>>31 == 0, id: value}
		return true
	}
	if word&0x1f000000 == 0x11000000 && word&(1<<29) == 0 { // ADD/SUB immediate, without flags.
		source := (word >> 5) & 31
		value := r[source]
		immediate := uint64((word >> 10) & 4095)
		if word&(1<<22) != 0 {
			immediate <<= 12
		}
		if source == 31 || value.kind == armStack {
			r[destination] = armValue{kind: armStack}
		} else if value.kind == armConstant {
			if word&(1<<30) != 0 {
				value.id -= immediate
			} else {
				value.id += immediate
			}
			if word>>31 == 0 {
				value.id = uint64(uint32(value.id))
				value.narrow = true
			}
			r[destination] = value
		} else if immediate == 0 {
			value.narrow = value.narrow || word>>31 == 0
			r[destination] = value
		} else {
			r[destination] = armValue{}
		}
		return true
	}
	return false
}

type armMemory struct {
	base, first, second uint32
	offset              int64
	width               uint32
	load, pair, update  bool
}

func armLoadStore(word uint32) (armMemory, bool) {
	m := armMemory{base: (word >> 5) & 31, first: word & 31, second: (word >> 10) & 31}
	if word&(1<<26) != 0 { // SIMD registers occupy a different register file.
		return m, false
	}
	switch {
	case word&0x3b000000 == 0x39000000: // Unsigned scaled immediate.
		op := (word >> 22) & 3
		if op > 1 && (word>>30 == 3 || word>>30 == 2 && op == 3) {
			return m, false
		}
		m.load, m.width = op != 0, 1<<(word>>30)
		m.offset = int64((word>>10)&4095) * int64(m.width)
	case word&0x3b200000 == 0x38000000: // Unscaled/pre/post indexed immediate.
		op, mode := (word>>22)&3, (word>>10)&3
		if mode == 2 || op > 1 && (word>>30 == 3 || word>>30 == 2 && op == 3) {
			return m, false
		}
		m.load, m.width = op != 0, 1<<(word>>30)
		m.offset, m.update = armSigned((word>>12)&511, 9), mode != 0
		if mode == 1 {
			m.offset = 0
		}
	case word&0x3a000000 == 0x28000000: // Integer LDP/STP.
		op, mode := word>>30, (word>>23)&3
		if (op != 0 && op != 2) || mode == 0 {
			return m, false
		}
		m.load, m.pair, m.width = word&(1<<22) != 0, true, 4+op*2
		m.offset, m.update = armSigned((word>>15)&127, 7)*int64(m.width), mode != 2
		if mode == 1 {
			m.offset = 0
		}
	default:
		return m, false
	}
	return m, true
}

func armCompare(word uint32) bool {
	// CMP/CMN immediate, shifted register or extended register. The result is
	// NZCV, not a general register. Conditional branches are handled separately.
	return word&31 == 31 && word&(1<<29) != 0 &&
		(word&0x1f000000 == 0x11000000 || word&0x1f000000 == 0x0b000000)
}

func (r *armRegisters) simple(word uint32, pc uint64) bool {
	if r.move(word, pc) || armCompare(word) {
		return true
	}
	if memory, ok := armLoadStore(word); ok {
		if memory.load {
			r[memory.first] = armValue{}
			if memory.pair {
				r[memory.second] = armValue{}
			}
		}
		if memory.update && memory.base != 31 {
			r[memory.base] = armValue{}
		}
		r[31] = armValue{kind: armConstant}
		return true
	}
	if word&0xfff00000 == 0xd5300000 { // MRS (e.g. a stack canary's thread pointer).
		r[word&31] = armValue{}
		return true
	}
	return false
}

func armPrelude(f *chromiumFunction) (armRegisters, uint64, bool) {
	r := armArguments()
	for pc := f.address; pc < f.end() && pc-f.address < 512; pc += 4 {
		word := f.word(pc)
		if _, call, branch := armBranch(word, pc); branch {
			return r, pc, call
		}
		if target, conditional := armConditional(word, pc); conditional {
			if target <= pc || target >= f.end() {
				return r, 0, false
			}
			continue
		}
		if !r.simple(word, pc) {
			return r, 0, false
		}
	}
	return r, 0, false
}

type armCall struct{ pc, target uint64 }

func armCalls(f *chromiumFunction) []armCall {
	var calls []armCall
	for pc := f.address; pc < f.end(); pc += 4 {
		if target, call, branch := armBranch(f.word(pc), pc); branch && (call || target < f.address || target >= f.end()) {
			calls = append(calls, armCall{pc, target})
		}
	}
	return calls
}

// A saved entry argument may only be restored on a straight path to RET.
// This prevents a later reuse of (say) x19 from masquerading as the original
// SSL/socket pointer or integer length in local call/guard checks.
func armSavedStable(f *chromiumFunction, start uint64, register uint32) bool {
	for pc := start; pc < f.end(); pc += 4 {
		word := f.word(pc)
		if armHint(word) || armReturn(word) {
			continue
		}
		if _, _, branch := armBranch(word, pc); branch {
			continue
		}
		if _, branch := armConditional(word, pc); branch {
			continue
		}
		if memory, ok := armLoadStore(word); ok {
			if memory.update && memory.base == register {
				return false
			}
			if memory.load && (memory.first == register || memory.pair && memory.second == register) &&
				((memory.base != 31 && memory.base != 29) || !armStackReturn(f, pc)) {
				return false
			}
			continue
		}
		if word&0x0a000000 == 0x08000000 {
			// An unfamiliar load/store can write multiple registers. SIMD uses
			// a separate register file but can still update its address register.
			if word&(1<<26) != 0 {
				if memory, ok := armLoadStore(word &^ (1 << 26)); ok && (!memory.update || memory.base != register) {
					continue
				}
			}
			return false
		}
		if word&31 == register {
			return false
		}
	}
	return true
}

func armStackReturn(f *chromiumFunction, pc uint64) bool {
	var r armRegisters
	for steps := 0; pc < f.end() && steps < 32; pc, steps = pc+4, steps+1 {
		word := f.word(pc)
		if armReturn(word) {
			return true
		}
		if memory, ok := armLoadStore(word); ok && memory.base != 31 && memory.base != 29 {
			return false
		}
		if !r.simple(word, pc) {
			return false
		}
	}
	return false
}
