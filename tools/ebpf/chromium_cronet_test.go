package main

import (
	"bytes"
	"context"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCronetResolvesSparseUnwindAndInlinedRead(t *testing.T) {
	for _, source := range []string{"cronet-119.json", "cronet-134.json", "cronet-150.json"} {
		for _, seed := range []int64{1, 11, 29} {
			for _, sparse := range []bool{false, true} {
				for _, shared := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%d/sparse=%t/shared=%t", source, seed, sparse, shared), func(t *testing.T) {
						fixture := makeChromiumFixtureSource(t, source, seed, false, true)
						if sparse {
							fixture.sparseUnwind()
						}
						if shared {
							fixture.mergeLoadSegments()
						}
						resolved, method, err := resolveSymbolsForMachine(context.Background(), bytes.NewReader(fixture.data), "renamed-library.so", elf.EM_AARCH64)
						if err != nil {
							t.Fatal(err)
						}
						base := uint64(0x10000000) + uint64(seed)*0x1000000
						want := make(map[string]uint64)
						for _, name := range requiredSymbols {
							want[name] = fixture.functions[name].address - base
						}
						if !reflect.DeepEqual(resolved, want) || method != "BoringSSL code analysis" {
							t.Fatalf("resolved %v (%s); expected %v", resolved, method, want)
						}
					})
				}
			}
		}
	}
}

func TestCronetOutlinedHelpersMustPreserveContextOwnershipAndConsumption(t *testing.T) {
	word := func(f chromiumFixture, name string, offset int, value uint32) {
		binary.LittleEndian.PutUint32(f.functions[name].code[offset:], value)
	}
	change := func(f chromiumFixture, name string, offset int, clear, set uint32) {
		word(f, name, offset, binary.LittleEndian.Uint32(f.functions[name].code[offset:])&^clear|set)
	}
	cases := map[string]func(chromiumFixture){
		"wrong context stored":                               func(f chromiumFixture) { change(f, "SSL_new", 0x10, 31, 1) },
		"reference to another stack slot":                    func(f chromiumFixture) { change(f, "SSL_new", 0x18, 4095<<10, 16<<10) },
		"constructor receives allocation instead of context": func(f chromiumFixture) { change(f, "MakeUnique", 0x20, 31<<5, 19<<5) },
		"truncated allocation pointer":                       func(f chromiumFixture) { word(f, "MakeUnique", 0x2c, 0x2a1303e0) },
		"reset stores old allocation":                        func(f chromiumFixture) { change(f, "SSL_reset", 0xc, 31, 0) },
		"reset releases only null":                           func(f chromiumFixture) { change(f, "SSL_reset", 0x10, 0, 1<<24) },
		"free wrapper changes argument":                      func(f chromiumFixture) { word(f, "SSL_free_wrapper", 0, 0xaa0103e0) },
		"free wrapper changes stack":                         func(f chromiumFixture) { word(f, "SSL_free_wrapper", 0, 0xd10043ff) },
		"free wrapper returns without freeing":               func(f chromiumFixture) { word(f, "SSL_free_wrapper", 4, 0xd65f03c0) },
		"free wrapper changes link register":                 func(f chromiumFixture) { change(f, "SSL_free_wrapper", 4, 0, 1<<31) },
		"free wrapper loops":                                 func(f chromiumFixture) { word(f, "SSL_free_wrapper", 4, 0x17ffffff) },
		"free wrapper jumps inside a function": func(f chromiumFixture) {
			pc, target := f.functions["SSL_free_wrapper"].address+4, f.functions["SSL_free"].address+4
			word(f, "SSL_free_wrapper", 4, 0x14000000|uint32((int64(target)-int64(pc))/4)&0x3ffffff)
		},
		"cleanup frees another pointer":         func(f chromiumFixture) { word(f, "SSL_new", 0x2b0, 0xaa1403e0) },
		"read unsigned positive guard":          func(f chromiumFixture) { change(f, "SSL_read", 0x1c, 15, 3) },
		"subspan receives another count":        func(f chromiumFixture) { word(f, "SSL_read", 0x24, 0x2a1403e1) },
		"subspan has a finite limit":            func(f chromiumFixture) { word(f, "SSL_read", 0x28, 0xd2800022) },
		"writeback targets another owner":       func(f chromiumFixture) { change(f, "SSL_read", 0x34, 4095<<10, 7<<10) },
		"writeback reverses pointer and length": func(f chromiumFixture) { change(f, "SSL_read", 0x38, 31|31<<10, 1) },
		"writeback omitted":                     func(f chromiumFixture) { word(f, "SSL_read", 0x38, 0xd503201f) },
		"Boolean return":                        func(f chromiumFixture) { word(f, "SSL_read", 0x50, 0x52800020) },
		"subspan wrong bounds condition":        func(f chromiumFixture) { change(f, "Span_subspan", 0xc, 15, 2) },
		"subspan moves pointer backwards":       func(f chromiumFixture) { change(f, "Span_subspan", 0x18, 0, 1<<30) },
		"subspan selects maximum length":        func(f chromiumFixture) { change(f, "Span_subspan", 0x1c, 15<<12, 2<<12) },
		"bounds failure returns": func(f chromiumFixture) {
			sample := f.functions["Span_subspan"]
			pc, target := sample.address+0x2c, f.imports["memcpy"].address
			word(f, "Span_subspan", 0x2c, 0x94000000|uint32((int64(target)-int64(pc))/4)&0x3ffffff)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := makeChromiumFixtureSource(t, "cronet-119.json", 13, false, true)
			fixture.sparseUnwind()
			mutate(fixture)
			if symbols, err := resolveFixture(context.Background(), fixture); err == nil || len(symbols) != 0 {
				t.Fatalf("unverified outlined ABI accepted: %v, %v", symbols, err)
			}
		})
	}
}

func TestCronetFreeWrapperNeedsNoInventedCallEntry(t *testing.T) {
	fixture := makeChromiumFixtureSource(t, "cronet-119.json", 19, false, true)
	fixture.sparseUnwind()
	wrapper := fixture.functions["SSL_free_wrapper"]
	for _, sample := range fixture.functions {
		for _, call := range armCalls(&chromiumFunction{sample.address, sample.code}) {
			if _, linked, _ := armBranch(binary.LittleEndian.Uint32(sample.code[call.pc-sample.address:]), call.pc); linked && call.target == wrapper.address {
				t.Fatal("fixture invented a BL caller absent from the installed library")
			}
		}
	}
	resolved, err := resolveFixture(context.Background(), fixture)
	if err != nil || resolved["SSL_free"] != fixture.functions["SSL_free"].address-(0x10000000+19*0x1000000) {
		t.Fatalf("tail-only release wrapper: %v, %v", resolved, err)
	}
}

func TestCronetTailWrapperCannotBypassCorruptUnwind(t *testing.T) {
	fixture := makeChromiumFixtureSource(t, "cronet-119.json", 19, false, true)
	wrapper := fixture.functions["SSL_free_wrapper"]
	binary.LittleEndian.PutUint32(fixture.data[wrapper.frame+12:], 0)
	if symbols, err := resolveFixture(context.Background(), fixture); err == nil || len(symbols) != 0 || !strings.Contains(err.Error(), "invalid unwind function range") {
		t.Fatalf("tail-only wrapper hid a corrupt FDE: %v, %v", symbols, err)
	}
}

func TestCronetSocketArithmeticCannotPreserveAClobberedSSLPointer(t *testing.T) {
	fixture := makeChromiumFixtureSource(t, "cronet-134.json", 11, false, true)
	code := fixture.functions["Chrome_Read"].code
	// ADD x1, x8, x26 computes a buffer address. Changing its destination to
	// x0 overwrites the SSL member and must invalidate socket ownership.
	binary.LittleEndian.PutUint32(code[0x78:], binary.LittleEndian.Uint32(code[0x78:])&^uint32(31))
	if symbols, err := resolveFixture(context.Background(), fixture); err == nil || len(symbols) != 0 {
		t.Fatalf("buffer arithmetic was mistaken for an SSL alias: %v, %v", symbols, err)
	}
}

func TestCronetRejectsUnverifiedCopyConsumptionAndBoundaries(t *testing.T) {
	word := func(f chromiumFixture, name string, offset int, value uint32) {
		binary.LittleEndian.PutUint32(f.functions[name].code[offset:], value)
	}
	cases := map[string]func(chromiumFixture){
		"non-consuming read": func(f chromiumFixture) { word(f, "SSL_read", 0xb4, 0xd503201f) },
		"Boolean result":     func(f chromiumFixture) { word(f, "SSL_read", 0x3c, 0x52800020) },
		"copy another buffer": func(f chromiumFixture) {
			word(f, "SSL_read", 0x90, 0xaa1403e0) // x20 holds SSL, x21 holds the caller's buffer.
		},
		"unsigned length guard": func(f chromiumFixture) {
			code := f.functions["SSL_read"].code
			word(f, "SSL_read", 0x70, binary.LittleEndian.Uint32(code[0x70:])&^uint32(15)|3)
		},
		"maximum copy length": func(f chromiumFixture) {
			code := f.functions["SSL_read"].code
			word(f, "SSL_read", 0x84, binary.LittleEndian.Uint32(code[0x84:])&^(uint32(15)<<12)|8<<12)
		},
		"unresolved read branch":  func(f chromiumFixture) { word(f, "SSL_read", 0x18, 0xd61f0100) },
		"no allocation/free pair": func(f chromiumFixture) { word(f, "SSL_free", 0x1c, 0xd503201f) },
		"reused socket loops back to allocation": func(f chromiumFixture) {
			// The later error path reuses x19 for a stack object. It is harmless
			// only while that path cannot return to the SSL ownership check.
			init := f.functions["Chrome_Init"]
			at, target := init.address+0x4cc, init.address+0xb4
			word(f, "Chrome_Init", 0x4cc, 0x14000000|uint32((int64(target)-int64(at))/4)&0x3ffffff)
		},
		"copy import overwritten": func(f chromiumFixture) {
			// The PLT loads x17, then clobbers it before the indirect branch.
			binary.LittleEndian.PutUint32(f.imports["memcpy"].code[8:], 0xd2800011)
		},
		"unresolved abort import": func(f chromiumFixture) {
			binary.LittleEndian.PutUint32(f.imports["abort"].code[4:], 0xd503201f)
		},
		"read entry only has jumps": func(f chromiumFixture) {
			address := f.functions["SSL_read"].address
			for _, sample := range f.functions {
				for at := 0; at < len(sample.code); at += 4 {
					value := binary.LittleEndian.Uint32(sample.code[at:])
					if target, linked, ok := armBranch(value, sample.address+uint64(at)); ok && linked && target == address {
						binary.LittleEndian.PutUint32(sample.code[at:], value&^(uint32(1)<<31))
					}
				}
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			fixture := makeChromiumFixtureSource(t, "cronet-150.json", 13, false, true)
			fixture.sparseUnwind()
			mutate(fixture)
			if symbols, err := resolveFixture(context.Background(), fixture); err == nil || len(symbols) != 0 {
				t.Fatalf("unverified Cronet accepted: %v, %v", symbols, err)
			}
		})
	}
}

func TestChromiumSparseUnwindDoesNotHideCorruptMetadata(t *testing.T) {
	fixture := makeChromiumFixtureSource(t, "cronet-150.json", 17, false, true)
	fixture.sparseUnwind()
	frame := fixture.header + uint64(int64(int32(binary.LittleEndian.Uint32(fixture.data[fixture.header+16:]))))
	binary.LittleEndian.PutUint32(fixture.data[frame+8:], 0)
	if symbols, err := resolveFixture(context.Background(), fixture); err == nil || len(symbols) != 0 || !strings.Contains(err.Error(), "invalid unwind function range") {
		t.Fatalf("corrupt FDE was treated as missing: %v, %v", symbols, err)
	}
}

func TestChromiumPLTImportsAndFlowCancellation(t *testing.T) {
	fixture := makeChromiumFixtureSource(t, "cronet-150.json", 7, false, true)
	fixture.sparseUnwind()
	file, err := elf.NewFile(bytes.NewReader(fixture.data))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	image, err := openChromiumImage(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	if err := image.loadCodeEntries(); err != nil {
		t.Fatal(err)
	}
	for name, sample := range fixture.imports {
		if got := image.pltImport(sample.address); got != name {
			t.Fatalf("PLT %s resolved as %q", name, got)
		}
	}
	if image.noReturnCall(fixture.imports["memcpy"].address) || image.pltImport(fixture.functions["SSL_read"].address) != "" {
		t.Fatal("returning function or ordinary code treated as a fatal import")
	}
	if m, ok := armLoadStore(0xc8dffd14); !ok || !m.load || m.width != 8 || m.base != 8 || m.first != 20 || m.update {
		t.Fatalf("LDAR singleton load decoded incorrectly: %+v", m)
	}
	cancel()
	f := fixture.functions["SSL_write"]
	if _, err := image.walkCode(f.address, f.code); !errors.Is(err, context.Canceled) {
		t.Fatalf("control-flow scan ignored cancellation: %v", err)
	}
}
