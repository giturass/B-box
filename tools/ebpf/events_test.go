package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"strings"
	"testing"
	"time"

	"github.com/cilium/ebpf/perf"
	"golang.org/x/sys/unix"
)

func TestAssemblerOrdersCPUsAndChunks(t *testing.T) {
	var received []pipeEvent
	a := newAssembler(collectorConfig{Packages: map[string][]string{"10001": {"com.test"}}}, func(e pipeEvent) error { received = append(received, e); return nil })
	base := rawEvent{pid: 1, uid: 10001, ssl: 2, generation: 3}
	open := base
	open.kind = 1
	data := base
	data.kind = 2
	data.sequence = 1
	data.length = 5000
	tail := data
	tail.offset = 4096
	tail.data = bytes.Repeat([]byte{'b'}, 904)
	head := data
	head.data = bytes.Repeat([]byte{'a'}, 4096)
	close := base
	close.kind = 4
	close.sequence = 1
	for _, e := range []rawEvent{tail, close, head, open} {
		if err := a.accept(e); err != nil {
			t.Fatal(err)
		}
	}
	if len(received) != 4 || received[0].Type != "open" || received[3].Type != "close" {
		t.Fatalf("bad order: %+v", received)
	}
	if received[0].Packages[0] != "com.test" || !bytes.Equal(received[1].Data, head.data) || !bytes.Equal(received[2].Data, tail.data) {
		t.Fatal("lost data or package metadata")
	}
	if a.bytes != 0 || len(a.flows) != 0 {
		t.Fatal("completed buffers not released")
	}
}
func TestAssemblerDetectsGapsDuplicatesAndLoss(t *testing.T) {
	a := newAssembler(collectorConfig{}, func(pipeEvent) error { return nil })
	e := rawEvent{pid: 1, ssl: 2, generation: 3, kind: 2, sequence: 1, length: 1, data: []byte("x")}
	if err := a.accept(e); err != nil {
		t.Fatal(err)
	}
	if err := a.checkLoss(time.Now().Add(4 * time.Second)); err == nil {
		t.Fatal("missing open event was ignored")
	}
	if err := a.accept(e); err == nil {
		t.Fatal("duplicate chunk accepted")
	}
	a = newAssembler(collectorConfig{}, func(e pipeEvent) error {
		if e.Type == "data" && !e.Gap {
			t.Fatal("lost call emitted as complete")
		}
		return nil
	})
	open := rawEvent{pid: 1, ssl: 2, generation: 3, kind: 1}
	_ = a.accept(open)
	e.flags = 1
	e.data = nil
	if err := a.accept(e); err != nil {
		t.Fatal(err)
	}
}
func TestDecodeEventBounds(t *testing.T) {
	input := make([]byte, 75)
	binary.LittleEndian.PutUint32(input[44:], 2)
	binary.LittleEndian.PutUint32(input[52:], 3)
	binary.LittleEndian.PutUint32(input[60:], 3)
	copy(input[72:], "abc")
	if e, err := decodeEvent(input); err != nil || string(e.data) != "abc" {
		t.Fatal(e, err)
	}
	binary.LittleEndian.PutUint32(input[60:], 4097)
	if _, err := decodeEvent(input); err == nil {
		t.Fatal("oversized event accepted")
	}
	if _, err := decodeEvent(input[:10]); err == nil {
		t.Fatal("short event accepted")
	}
}

func TestAndroidForkPerfEnvelopeIsRemovedBeforeEventDecoding(t *testing.T) {
	payload := make([]byte, 75)
	binary.LittleEndian.PutUint32(payload[44:], 2)
	binary.LittleEndian.PutUint32(payload[52:], 3)
	binary.LittleEndian.PutUint32(payload[60:], 3)
	copy(payload[72:], "abc")
	for padding := 0; padding <= 7; padding++ {
		for _, included := range []bool{false, true} {
			data := make([]byte, 4+len(payload)+padding)
			size := len(payload)
			if included {
				size += padding
			}
			binary.LittleEndian.PutUint32(data, uint32(size))
			copy(data[4:], payload)
			event, err := decodePerfRecord(perf.Record{RecordType: unix.PERF_RECORD_SAMPLE, RawSample: data})
			if err != nil || string(event.data) != "abc" || event.kind != 2 {
				t.Fatalf("perf length word was treated as event data: %+v, %v", event, err)
			}
		}
	}
	for _, record := range []perf.Record{
		{RecordType: unix.PERF_RECORD_MMAP2, RawSample: payload},
		{RecordType: unix.PERF_RECORD_SAMPLE, RawSample: payload[:3]},
		{RecordType: unix.PERF_RECORD_SAMPLE, RawSample: []byte{255, 255, 255, 255}},
	} {
		if _, err := decodePerfRecord(record); err == nil {
			t.Fatal("invalid perf envelope accepted")
		}
	}
}
func TestSelectionAndPrivateConfigValidation(t *testing.T) {
	c := collectorConfig{ExcludeUID: 10001, UserID: 1}
	if c.selected(10001) || c.selected(10002) || !c.selected(110002) {
		t.Fatal("Android user/owner isolation failed")
	}
	c.UIDs = []uint32{110003}
	if c.selected(110002) || !c.selected(110003) {
		t.Fatal("UID allowlist ignored")
	}
	if _, err := readSymbols(context.Background(), strings.NewReader("not ELF"), "libssl.so"); err == nil {
		t.Fatal("non ELF library accepted")
	}
	for _, invalid := range []string{
		"{} {}\n", "{\"targets\":[\"relative.apk\"]}\n", "{\"offsets\":{}}\n",
	} {
		if _, err := readConfig(bufio.NewReader(strings.NewReader(invalid))); err == nil {
			t.Fatal("invalid private startup settings accepted", invalid)
		}
	}
	if _, err := readConfig(bufio.NewReader(strings.NewReader("{\"targets\":[\"/data/app/base.apk\"]}\n"))); err != nil {
		t.Fatal(err)
	}
}

func TestDirectionsHaveIndependentSequencesAndCloseWaitsForBoth(t *testing.T) {
	var received []pipeEvent
	a := newAssembler(collectorConfig{}, func(event pipeEvent) error {
		received = append(received, event)
		return nil
	})
	open := rawEvent{pid: 1, ssl: 2, generation: 3, kind: 1}
	write := open
	write.kind, write.sequence, write.length, write.data = 2, 1, 1, []byte("w")
	read := write
	read.kind, read.data = 3, []byte("r")
	close := open
	close.kind, close.sequence, close.otherSequence = 4, 1, 1
	for _, event := range []rawEvent{open, close, read} {
		if err := a.accept(event); err != nil {
			t.Fatal(err)
		}
	}
	if len(received) != 2 || received[1].Direction != "read" {
		t.Fatal("response direction blocked")
	}
	if err := a.accept(write); err != nil {
		t.Fatal(err)
	}
	if len(received) != 4 || received[2].Direction != "write" || received[3].Type != "close" {
		t.Fatalf("close lost pending data: %+v", received)
	}
}

func TestLocalGapDiscardsIncompleteCallButOtherConnectionsContinue(t *testing.T) {
	for _, closeFirst := range []bool{false, true} {
		var received []pipeEvent
		a := newAssembler(collectorConfig{}, func(event pipeEvent) error {
			received = append(received, event)
			return nil
		})
		first := rawEvent{pid: 1, ssl: 2, generation: 3, kind: 1}
		second := rawEvent{pid: 1, ssl: 4, generation: 5, kind: 1}
		part := first
		part.kind, part.sequence, part.length, part.data = 2, 1, 5000, bytes.Repeat([]byte{'x'}, 4096)
		gap := first
		gap.kind, gap.flags = 5, 3
		closed := first
		closed.kind, closed.sequence, closed.flags = 4, 1, 3
		events := []rawEvent{first, second, part}
		if closeFirst {
			events = append(events, closed, gap)
		} else {
			events = append(events, gap, closed)
		}
		valid := second
		valid.kind, valid.sequence, valid.length, valid.data = 2, 1, 2, []byte("ok")
		events = append(events, valid)
		for _, event := range events {
			if err := a.accept(event); err != nil {
				t.Fatal(err)
			}
		}
		if err := a.checkLoss(time.Now().Add(4 * time.Second)); err != nil {
			t.Fatal(err)
		}
		var gaps, validChunks int
		for _, event := range received {
			if event.Type != "data" {
				continue
			}
			if event.ID == first.id() {
				if !event.Gap || !strings.Contains(event.Error, "could not be read") {
					t.Fatal("incomplete call emitted as data", event)
				}
				gaps++
			} else if string(event.Data) == "ok" {
				validChunks++
			}
		}
		if gaps != 1 || validChunks != 1 || a.bytes != 0 {
			t.Fatal(gaps, validChunks, a.bytes)
		}
	}
}
