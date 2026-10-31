package main

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
	"time"
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
func TestSelectionAndPrivateConfigValidation(t *testing.T) {
	c := collectorConfig{ExcludeUID: 10001, UserID: 1}
	if c.selected(10001) || c.selected(10002) || !c.selected(110002) {
		t.Fatal("Android user/owner isolation failed")
	}
	c.UIDs = []uint32{110003}
	if c.selected(110002) || !c.selected(110003) {
		t.Fatal("UID allowlist ignored")
	}
	if _, err := readSymbols(strings.NewReader("not ELF")); err == nil {
		t.Fatal("non ELF library accepted")
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
