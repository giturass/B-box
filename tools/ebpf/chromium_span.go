package main

// With size optimization, SSL_read may call Span::subspan instead of updating
// its pending span inline. Verify that helper's bounds, pointer advance, length
// reduction and two-register return, then require both words to be written back
// to the original span and the unmodified signed peek count to be returned.
func (c *chromiumImage) outlinedRead(f *chromiumFunction, peek uint64) bool {
	r, count, body, exit, ok := chromiumReadStart(f, peek)
	if !ok || c.loadPLTImports() != nil {
		return false
	}
	owners := make(map[chromiumLocation]armValue)
	span := armValue{kind: armMember, id: body}
	var where chromiumLocation
	spanSet, called := false, false
	for pc, steps := body, 0; pc < exit && steps < 24; pc, steps = pc+4, steps+1 {
		word := f.word(pc)
		if m, ok := armLoadStore(word); ok {
			if m.update || m.width != 8 || m.first == 31 {
				return false
			}
			if m.load {
				if m.pair || !r[m.base].argument(0, false) {
					return false
				}
				location := chromiumLocation{r[m.base], m.offset}
				value, found := owners[location]
				if !found {
					value = armValue{kind: armLoaded, id: uint64(len(owners) + 1)}
					owners[location] = value
				}
				r[m.first] = value
			} else {
				if !called || !m.pair || (chromiumLocation{r[m.base], m.offset}) != where ||
					r[m.first] != (armValue{kind: armAdvanced, id: span.id}) ||
					r[m.second] != (armValue{kind: armReduced, id: span.id}) {
					return false
				}
				return c.inlinedReadReturn(f, pc+4, r, count)
			}
		} else if dst, src, offset, ok := armPointerAdd(word); ok && r[src].kind == armLoaded {
			if called || spanSet || dst == 31 || offset < 0 {
				return false
			}
			where, spanSet, r[dst] = chromiumLocation{r[src], offset}, true, span
		} else if target, linked, branch := armBranch(word, pc); branch {
			if !linked || !spanSet || called || r[0] != span || r[1] != count ||
				r[2].kind != armConstant || r[2].narrow || r[2].id != ^uint64(0) {
				return false
			}
			helper, err := c.entryFunction(target)
			if err != nil || !c.subspanHelper(helper) {
				return false
			}
			r.clobberCall()
			r[0], r[1] = armValue{kind: armAdvanced, id: span.id}, armValue{kind: armReduced, id: span.id}
			called = true
		} else if !r.move(word, pc) {
			return false
		}
	}
	return false
}

func (c *chromiumImage) subspanHelper(f *chromiumFunction) bool {
	if len(f.code) > 256 {
		return false
	}
	r := armArguments()
	length, pointer := armValue{kind: armLoaded, id: 1}, armValue{kind: armLoaded, id: 2}
	reduced, advanced := armValue{kind: armReduced, id: 1}, armValue{kind: armAdvanced, id: 2}
	var subtract, compare uint64
	guarded := false
	for pc := f.address; pc < f.end(); pc += 4 {
		word := f.word(pc)
		switch {
		case armReturn(word):
			return guarded && r[0] == advanced && r[1] == reduced
		case word&0xffe0fc00 == 0xeb000000 && word&31 != 31: // SUBS Xremaining, Xlength, Xoffset.
			if r[(word>>5)&31] != length || !r[(word>>16)&31].argument(1, false) || word&31 == 31 {
				return false
			}
			r[word&31], subtract = reduced, pc
		case word&0xff00001f == 0x54000003: // B.LO to the verified bounds failure.
			target, _ := armConditional(word, pc)
			if subtract == 0 || pc != subtract+4 || !c.trapExit(f, target) {
				return false
			}
			guarded = true
		case word&0xffe0fc1f == 0xeb00001f: // CMP Xremaining, Xlimit.
			if !guarded || r[(word>>5)&31] != reduced || !r[(word>>16)&31].argument(2, false) {
				return false
			}
			compare = pc
		case word&0xffe0fc00 == 0x8b000000: // ADD Xdata, Xpointer, Xoffset.
			if !guarded || r[(word>>5)&31] != pointer || !r[(word>>16)&31].argument(1, false) || word&31 == 31 {
				return false
			}
			r[word&31] = advanced
		case word&0xffe00c00 == 0x9a800000: // CSEL Xlength, Xremaining, Xlimit, LO.
			if compare == 0 || pc-compare > 8 || (word>>12)&15 != 3 || r[(word>>5)&31] != reduced ||
				!r[(word>>16)&31].argument(2, false) || word&31 == 31 {
				return false
			}
			// The caller proved limit == SIZE_MAX, so min(remaining, limit)
			// is remaining. Neither a maximum nor a signed comparison qualifies.
			r[word&31] = reduced
		default:
			if m, ok := armLoadStore(word); ok {
				if !m.load || m.pair || m.update || m.width != 8 || m.first == 31 || !r[m.base].argument(0, false) {
					return false
				}
				switch m.offset {
				case 0:
					r[m.first] = pointer
				case 8:
					r[m.first] = length
				default:
					return false
				}
			} else if !r.move(word, pc) {
				return false
			}
		}
	}
	return false
}
