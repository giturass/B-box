package main

// Older size-optimized Cronet builds outline MakeUnique and unique_ptr::reset.
// Follow their argument, allocation and release relationships; the helper's
// address, allocation size and socket member offset are not version signatures.
// https://boringssl.googlesource.com/boringssl/+/d24a38200fef19150eef00cad35b138936c08767/ssl/ssl_lib.cc

func armPointerAdd(word uint32) (destination, source uint32, offset int64, ok bool) {
	if word&0xbf800000 != 0x91000000 { // 64-bit ADD/SUB immediate, without flags.
		return 0, 0, 0, false
	}
	offset = int64((word >> 10) & 4095)
	if word&(1<<22) != 0 {
		offset <<= 12
	}
	if word&(1<<30) != 0 {
		offset = -offset
	}
	return word & 31, (word >> 5) & 31, offset, true
}

func (c *chromiumImage) outlinedAllocation(f *chromiumFunction, errorHelper uint64) (uint32, bool) {
	r := armArguments()
	var slot int64
	stored, guarded := false, false
	for pc := f.address; pc < f.end() && pc-f.address < 512; pc += 4 {
		word := f.word(pc)
		if m, ok := armLoadStore(word); ok {
			// The helper receives a reference to the actual entry context. A
			// stack address by itself is insufficient; track the stored value.
			if m.base != 31 || m.update && stored {
				return 0, false
			}
			if !m.load && stored && m.offset <= slot+7 && m.offset+int64(m.width)*(1+btoi(m.pair)) > slot {
				stored = false
			}
			if !m.load && !m.pair && !m.update && m.width == 8 && r[m.first].argument(0, false) {
				slot, stored = m.offset, true
			}
			if !r.simple(word, pc) {
				return 0, false
			}
		} else if dst, src, offset, ok := armPointerAdd(word); ok && src == 31 {
			if dst == 31 {
				if stored {
					return 0, false // Do not carry a slot through an untracked SP change.
				}
			} else {
				r[dst] = armValue{kind: armStack, id: uint64(offset)}
			}
		} else if target, branch := armConditional(word, pc); branch {
			if guarded || word&0xff000000 != 0xb4000000 || !r[word&31].argument(0, false) {
				return 0, false
			}
			site, ok := c.errorCall(f, target, target)
			guarded = ok && site.reason == sslNullContext && site.target == errorHelper
		} else if target, linked, branch := armBranch(word, pc); branch {
			if !linked || !guarded || !stored || r[0] != (armValue{kind: armStack, id: uint64(slot)}) {
				return 0, false
			}
			helper, err := c.entryFunction(target)
			if err != nil || !chromiumMakeUnique(helper) {
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
		} else if !r.move(word, pc) {
			return 0, false
		}
	}
	return 0, false
}

func btoi(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func chromiumMakeUnique(f *chromiumFunction) bool {
	if len(f.code) > 256 {
		return false
	}
	r, call, ok := armPrelude(f)
	if !ok || r[0].kind != armConstant || r[0].id == 0 {
		return false
	}
	r.clobberCall()
	allocated := armValue{kind: armResult, id: call}
	context := armValue{kind: armLoaded, id: call}
	r[0] = allocated
	guarded, constructed := false, false
	for pc := call + 4; pc < f.end(); pc += 4 {
		word := f.word(pc)
		if target, branch := armConditional(word, pc); branch {
			if guarded || word&0xff000000 != 0xb4000000 || r[word&31] != allocated ||
				!chromiumReturns(f, target, r, allocated) {
				return false
			}
			guarded = true
		} else if _, linked, branch := armBranch(word, pc); branch {
			if !linked || !guarded || constructed || r[0] != allocated || r[1] != context {
				return false
			}
			constructed = true
			r.clobberCall()
		} else if armReturn(word) {
			return constructed && r[0] == allocated
		} else if m, ok := armLoadStore(word); ok && m.base != 31 && m.base != 29 {
			if !m.load || m.pair || m.update || m.width != 8 || m.offset != 0 || !r[m.base].argument(0, false) {
				return false
			}
			r[m.first] = context
		} else if !r.simple(word, pc) {
			return false
		}
	}
	return false
}

func (c *chromiumImage) outlinedReset(f *chromiumFunction, call uint64) (uint64, int64, bool) {
	r, firstCall, ok := armPrelude(f)
	if !ok {
		return 0, 0, false
	}
	for register := 19; register <= 28; register++ {
		if r[register].argument(0, false) && !armSavedAtCall(f, firstCall, call, uint32(register)) {
			return 0, 0, false
		}
	}
	r.clobberCall()
	for pc := max(firstCall+4, call-min(call-f.address, 24)); pc < call; pc += 4 {
		word := f.word(pc)
		if dst, src, offset, ok := armPointerAdd(word); ok && dst != 31 && r[src].argument(0, false) {
			r[dst] = armValue{kind: armMember, id: uint64(offset)}
		} else if _, _, branch := armBranch(word, pc); branch {
			r.clobberCall()
		} else if _, branch := armConditional(word, pc); branch {
			return 0, 0, false
		} else if !r.simple(word, pc) {
			return 0, 0, false
		}
	}
	r.clobberCall()
	allocated := armValue{kind: armResult, id: call}
	r[0] = allocated
	for pc := call + 4; pc < f.end() && pc-call <= 32; pc += 4 {
		word := f.word(pc)
		if target, linked, branch := armBranch(word, pc); branch {
			if !linked || r[0].kind != armMember || r[1] != allocated {
				return 0, 0, false
			}
			helper, err := c.entryFunction(target)
			if err != nil {
				return 0, 0, false
			}
			free, ok := chromiumResetHelper(helper)
			return free, int64(r[0].id), ok
		}
		if !r.move(word, pc) {
			return 0, 0, false
		}
	}
	return 0, 0, false
}

func chromiumResetHelper(f *chromiumFunction) (uint64, bool) {
	if len(f.code) > 128 {
		return 0, false
	}
	r := armArguments()
	old := armValue{kind: armLoaded, id: f.address}
	loaded, stored, guarded := false, false, false
	for pc := f.address; pc < f.end(); pc += 4 {
		word := f.word(pc)
		if m, ok := armLoadStore(word); ok {
			if m.pair || m.update || m.width != 8 || m.offset != 0 || !r[m.base].argument(0, false) {
				return 0, false
			}
			if m.load {
				if loaded || stored {
					return 0, false
				}
				r[m.first], loaded = old, true
			} else {
				if !loaded || stored || !r[m.first].argument(1, false) {
					return 0, false
				}
				stored = true
			}
		} else if target, conditional := armConditional(word, pc); conditional {
			if !stored || guarded || word&0xff000000 != 0xb4000000 || r[word&31] != old ||
				target <= pc || target >= f.end() || !armStackReturn(f, target) {
				return 0, false
			}
			guarded = true
		} else if target, linked, branch := armBranch(word, pc); branch {
			return target, guarded && r[0] == old && (target < f.address || target >= f.end()) &&
				(!linked || armStackReturn(f, pc+4))
		} else if !r.move(word, pc) {
			return 0, false
		}
	}
	return 0, false
}

func (c *chromiumImage) freeFunction(address uint64) (*chromiumFunction, uint64, uint64, error) {
	for hops := 0; hops < 3; hops++ {
		entry, err := c.tailCallEntry(address)
		if err != nil {
			return nil, 0, 0, err
		}
		address = entry
		f, err := c.entryFunction(address)
		if err != nil {
			return nil, 0, 0, err
		}
		if destructor, deallocate, ok := chromiumFree(f); ok {
			return f, destructor, deallocate, nil
		}
		// A public SSL_free can tail-forward unchanged x0 to its deleter.
		pc := f.address
		for pc < f.end() && armHint(f.word(pc)) {
			pc += 4
		}
		if pc+4 != f.end() {
			break
		}
		target, linked, branch := armBranch(f.word(pc), pc)
		if !branch || linked || target == address {
			break
		}
		address = target
	}
	return nil, 0, 0, nil
}
