package main

// SSL_peek may be inlined into SSL_read. In that layout the public int length
// bounds a memcpy, and exactly its byte count advances/reduces one buffer span.
// Verify these relationships, including the signed guards and returned count,
// instead of treating a matching error code or a copy as a consuming read.
// https://boringssl.googlesource.com/boringssl/+/3a9254f16eda7a4c5d2260039ff23456a0a34de4/ssl/ssl_lib.cc#913
type chromiumLocation struct {
	base   armValue
	offset int64
}

func (c *chromiumImage) inlinedRead(f *chromiumFunction, readImpl uint64) bool {
	if len(f.code) > 2048 || c.loadPLTImports() != nil {
		return false
	}
	r, call, ok := armForwardedCall(f, readImpl)
	if !ok {
		return false
	}
	r.clobberCall()
	status := armValue{kind: armResult, narrow: true, id: call}
	r[0] = status
	pc, ok := chromiumReadGuard(f, call+4, r, status)
	if !ok {
		return false
	}
	pc, ok = chromiumReadGuard(f, pc, r, armValue{kind: armArgument, narrow: true, id: 2})
	if !ok {
		return false
	}
	values := make(map[chromiumLocation]armValue)
	locations := make(map[uint64]chromiumLocation)
	load := func(where chromiumLocation) armValue {
		if value, found := values[where]; found {
			return value
		}
		value := armValue{kind: armLoaded, id: uint64(len(locations) + 1)}
		values[where], locations[value.id] = value, where
		return value
	}
	loadMemory := func(word uint32) bool {
		m, ok := armLoadStore(word)
		if !ok || !m.load || m.pair || m.update || m.width != 8 || m.first == 31 {
			return false
		}
		base := r[m.base]
		if !base.argument(0, false) && base.kind != armLoaded {
			return false
		}
		r[m.first] = load(chromiumLocation{base, m.offset})
		return true
	}
	var length, pointer armValue
	var compared, countSet, positive bool
	var comparePC uint64
	count := armValue{kind: armResult, id: call + 1}
	var copied bool
	for steps := 0; pc < f.end() && steps < 32; pc, steps = pc+4, steps+1 {
		word := f.word(pc)
		if target, linked, branch := armBranch(word, pc); branch {
			if !linked || c.pltImport(target) != "memcpy" || !countSet || !positive ||
				!r[0].argument(1, false) || r[1].kind != armLoaded || r[2] != count {
				return false
			}
			pointer = r[1]
			span, ptr := locations[length.id], locations[pointer.id]
			if span.base != ptr.base || ptr.offset+8 != span.offset {
				return false
			}
			r.clobberCall()
			pc += 4
			copied = true
			break
		}
		switch {
		case word&0xffe0fc1f == 0xeb00001f: // CMP Xpending, X(uint32(num)).
			left, right := r[(word>>5)&31], r[(word>>16)&31]
			field, found := locations[left.id]
			owner, owned := locations[field.base.id]
			if left.kind != armLoaded || !found || field.base.kind != armLoaded || !owned ||
				!owner.base.argument(0, false) || !right.argument(2, true) || !right.narrow {
				return false
			}
			length, compared, comparePC = left, true, pc
		case word&0xffe00c00 == 0x9a800000: // CSEL Xcount, Xpending, Xnum, LO.
			if !compared || pc != comparePC+4 || (word>>12)&15 != 3 || r[(word>>5)&31] != length ||
				!r[(word>>16)&31].argument(2, true) || !r[(word>>16)&31].narrow || word&31 == 31 {
				return false
			}
			r[word&31], countSet = count, true
		case word&0xff000000 == 0xb4000000: // Empty pending span returns zero.
			target, _ := armConditional(word, pc)
			if !countSet || r[word&31] != length || !chromiumReturns(f, target, r, armValue{kind: armConstant}) {
				return false
			}
			positive = true
		default:
			if !loadMemory(word) && !r.move(word, pc) {
				return false
			}
		}
	}
	if !copied {
		return false
	}
	var advanced, reduced bool
	var subtractPC uint64
	for steps := 0; pc < f.end() && steps < 32; pc, steps = pc+4, steps+1 {
		word := f.word(pc)
		if m, ok := armLoadStore(word); ok && !m.load && !m.update && m.width == 8 {
			stores := 1
			if m.pair {
				stores = 2
			}
			for i := 0; i < stores; i++ {
				register := m.first
				if i == 1 {
					register = m.second
				}
				where := chromiumLocation{r[m.base], m.offset + int64(i)*8}
				value := r[register]
				switch {
				case value.kind == armAdvanced && value.id == pointer.id && where == locations[pointer.id]:
					advanced = true
				case value.kind == armReduced && value.id == length.id && where == locations[length.id]:
					reduced = true
				default:
					return false
				}
				values[where] = value
			}
			if advanced && reduced {
				count.narrow = true // Public SSL_read returns int, not size_t or bool.
				return c.inlinedReadReturn(f, pc+4, r, count)
			}
		} else if word&0xffe0fc00 == 0xeb000000 { // SUBS XnewLength, Xpending, Xcount.
			if r[(word>>5)&31] != length || r[(word>>16)&31] != count || word&31 == 31 {
				return false
			}
			r[word&31] = armValue{kind: armReduced, id: length.id}
			subtractPC = pc
		} else if word&0xff00001f == 0x54000003 { // B.LO to a verified fatal path.
			target, _ := armConditional(word, pc)
			if pc != subtractPC+4 || !c.trapExit(f, target) {
				return false
			}
		} else if word&0xffe0fc00 == 0x8b000000 { // ADD XnewPointer, Xdata, Xcount.
			if r[(word>>5)&31] != pointer || r[(word>>16)&31] != count || word&31 == 31 {
				return false
			}
			r[word&31] = armValue{kind: armAdvanced, id: pointer.id}
		} else if !loadMemory(word) && !r.move(word, pc) {
			return false
		}
	}
	return false
}

func chromiumReadGuard(f *chromiumFunction, pc uint64, r armRegisters, value armValue) (uint64, bool) {
	if pc < f.address || pc+8 > f.end() {
		return 0, false
	}
	word, branch := f.word(pc), f.word(pc+4)
	immediate, condition := (word>>10)&4095, branch&15
	if word&0xffc0001f != 0x7100001f || r[(word>>5)&31] != value || branch&0xff000010 != 0x54000000 ||
		!(immediate == 1 && condition == 11 || immediate == 0 && condition == 13) {
		return 0, false
	}
	target, _ := armConditional(branch, pc+4)
	return pc + 8, chromiumReturns(f, target, r, value)
}

func (c *chromiumImage) trapExit(f *chromiumFunction, pc uint64) bool {
	for steps := 0; pc >= f.address && pc < f.end() && steps < 8; pc, steps = pc+4, steps+1 {
		word := f.word(pc)
		if armTrap(word) {
			return true
		}
		if target, linked, branch := armBranch(word, pc); branch {
			return linked && c.noReturnCall(target)
		}
		// An outlined fatal block can open a frame before calling abort.
		// No data-dependent branches, target changes or ordinary calls qualify.
		if armHint(word) {
			continue
		}
		if m, ok := armLoadStore(word); ok && !m.load && m.base == 31 {
			continue
		}
		return false
	}
	return false
}

func (c *chromiumImage) inlinedReadReturn(f *chromiumFunction, pc uint64, r armRegisters, count armValue) bool {
	cleanup := false
	for steps := 0; pc >= f.address && pc < f.end() && steps < 40; steps++ {
		word := f.word(pc)
		if armReturn(word) {
			return r[0] == count
		}
		if target, linked, branch := armBranch(word, pc); branch {
			if !linked {
				pc = target
				continue
			}
			if cleanup {
				return false
			}
			cleanup = true
			r.clobberCall()
		} else if target, conditional := armConditional(word, pc); conditional {
			if !chromiumReturns(f, target, r, count) {
				return false
			}
		} else {
			if m, ok := armLoadStore(word); ok && !m.load && m.base != 31 && m.base != 29 {
				return false
			}
			if !r.simple(word, pc) {
				return false
			}
		}
		pc += 4
	}
	return false
}
