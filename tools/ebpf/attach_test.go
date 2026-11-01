package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"testing"
)

type closeFunc func() error

func (f closeFunc) Close() error { return f() }

func testProbeLibrary() library {
	return library{base: 0x10000, symbols: map[string]uint64{
		"SSL_new": 0x200, "SSL_free": 0x220, "SSL_read": 0x240, "SSL_write": 0x260,
	}}
}

func TestProbePlanUsesBackingFileOffsetsAndRejectsABIAliases(t *testing.T) {
	value := testProbeLibrary()
	probes, err := planProbes(value)
	if err != nil {
		t.Fatal(err)
	}
	last := probes[len(probes)-1]
	if last.symbol != "SSL_new" || !last.ret || last.offset != 0x10200 {
		t.Fatalf("allocations were not enabled last at the APK file offset: %+v", probes)
	}
	for _, probe := range probes {
		if probe.offset != value.base+value.symbols[probe.symbol] {
			t.Fatal("used a virtual or ELF-only offset")
		}
	}
	value.symbols["SSL_read_ex"] = value.symbols["SSL_read"]
	if _, err := planProbes(value); err == nil {
		t.Fatal("normal and _ex ABIs were aliased")
	}
	delete(value.symbols, "SSL_read_ex")
	value.symbols["SSL_write_ex"], value.symbols["SSL_write_early_data"] = 0x300, 0x300
	probes, err = planProbes(value)
	if err != nil || len(probes) != 8 {
		t.Fatalf("compatible aliases not deduplicated: %v, %v", probes, err)
	}
}

func TestPartialAttachAndCancellationReleaseAllHandlesInReverse(t *testing.T) {
	for _, cancelDuringAttach := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		var opened int
		var closed []int
		probes, err := attachProbes(ctx, testProbeLibrary(), func(probe probeSpec) (io.Closer, error) {
			opened++
			index := opened
			if opened == 4 {
				return nil, errors.New("test kernel rejected uprobe")
			}
			if cancelDuringAttach && opened == 3 {
				cancel()
			}
			return closeFunc(func() error { closed = append(closed, index); return nil }), nil
		})
		cancel()
		if err == nil || len(probes) != 0 || !reflect.DeepEqual(closed, []int{3, 2, 1}) {
			t.Fatalf("attach rollback: probes=%v error=%v closed=%v", probes, err, closed)
		}
		if cancelDuringAttach && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
}

func TestRequiredLifetimeOffsetsCannotBeOmitted(t *testing.T) {
	for _, symbol := range requiredSymbols {
		value := testProbeLibrary()
		delete(value.symbols, symbol)
		if _, err := planProbes(value); err == nil {
			t.Fatal(fmt.Sprintf("accepted missing %s", symbol))
		}
	}
}
