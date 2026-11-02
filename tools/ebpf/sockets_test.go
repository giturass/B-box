package main

import (
	"encoding/binary"
	"testing"
)

func TestSocketEventsWaitForOpenAndSurviveLateCloseDelivery(t *testing.T) {
	var events []pipeEvent
	a := newAssembler(collectorConfig{}, func(event pipeEvent) error { events = append(events, event); return nil })
	base := rawEvent{pid: 1, uid: 10001, ssl: 2, generation: 3, kind: 1, flags: 1}
	socket := base
	socket.kind, socket.fd, socket.flags, socket.length = 6, 0, 0, 56
	socket.data = make([]byte, 56)
	binary.LittleEndian.PutUint64(socket.data, 42)
	copy(socket.data[8:], []byte{127, 0, 0, 1})
	copy(socket.data[24:], []byte{127, 0, 0, 2})
	binary.LittleEndian.PutUint16(socket.data[40:], 1234)
	binary.LittleEndian.PutUint16(socket.data[42:], 443)
	binary.LittleEndian.PutUint16(socket.data[44:], 2)
	binary.LittleEndian.PutUint32(socket.data[48:], 7)
	if err := a.accept(socket); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatal("socket event preceded its TLS lifetime")
	}
	if err := a.accept(base); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Adapter != "Go TLS" || events[1].Socket.FD != 0 || events[1].Socket.Inode != "2a" || events[1].Socket.Destination != "127.0.0.2:443" {
		t.Fatalf("invalid socket event: %+v", events)
	}
	conflict := socket
	conflict.flags = 1
	if err := a.accept(conflict); err != nil || !events[len(events)-1].SocketConflict {
		t.Fatal("socket conflict missing", err)
	}
	closed := base
	closed.kind, closed.flags = 4, 0
	if err := a.accept(closed); err != nil {
		t.Fatal(err)
	}
	count := len(events)
	if err := a.accept(socket); err != nil || len(events) != count || len(a.flows) != 0 {
		t.Fatal("late socket event revived an ambiguous association", err)
	}
	// A late endpoint can still fill in a closed generation that never had a
	// conflict, without creating another live flow.
	base.generation++
	closed.generation, socket.generation = base.generation, base.generation
	for _, value := range []rawEvent{base, closed, socket} {
		if err := a.accept(value); err != nil {
			t.Fatal(err)
		}
	}
	last := events[len(events)-1]
	if last.Type != "socket" || last.Socket == nil || last.ID != base.id() || len(a.flows) != 0 {
		t.Fatal("late endpoint lost its retired TLS generation")
	}
}
