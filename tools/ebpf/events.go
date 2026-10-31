package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
)

type rawEvent struct {
	ssl, generation, sequence             uint64
	pid, uid, kind, length, offset, flags uint32
	data                                  []byte
}

func decodeEvent(data []byte) (rawEvent, error) {
	if len(data) < 72 {
		return rawEvent{}, errors.New("short eBPF event")
	}
	b := binary.LittleEndian
	event := rawEvent{ssl: b.Uint64(data[8:]), generation: b.Uint64(data[16:]), sequence: b.Uint64(data[24:]),
		pid: b.Uint32(data[32:]), uid: b.Uint32(data[40:]), kind: b.Uint32(data[44:]), length: b.Uint32(data[52:]),
		offset: b.Uint32(data[56:]), flags: b.Uint32(data[64:])}
	length := b.Uint32(data[60:])
	if length > 4096 || int(length)+72 > len(data) || event.kind < 1 || event.kind > 4 ||
		event.length > 128*1024 || event.offset+length > event.length {
		return rawEvent{}, errors.New("invalid eBPF event bounds")
	}
	event.data = append([]byte(nil), data[72:72+length]...)
	return event, nil
}
func (e rawEvent) id() string { return fmt.Sprintf("%d-%x-%x", e.pid, e.ssl, e.generation) }

type pipeEvent struct {
	Type      string   `json:"type"`
	Version   int      `json:"version,omitempty"`
	ID        string   `json:"id,omitempty"`
	PID       uint32   `json:"pid,omitempty"`
	UID       uint32   `json:"uid,omitempty"`
	Packages  []string `json:"packages,omitempty"`
	Direction string   `json:"direction,omitempty"`
	Data      []byte   `json:"data,omitempty"`
	Gap       bool     `json:"gap,omitempty"`
	Libraries int      `json:"libraries,omitempty"`
	Error     string   `json:"error,omitempty"`
}
type eventGroup struct {
	first rawEvent
	parts map[uint32][]byte
	size  int
	at    time.Time
}
type eventFlow struct {
	next    uint64
	pending map[uint64]*eventGroup
}
type eventAssembler struct {
	config collectorConfig
	flows  map[string]*eventFlow
	bytes  int
	emit   func(pipeEvent) error
}

func newAssembler(config collectorConfig, emit func(pipeEvent) error) *eventAssembler {
	return &eventAssembler{config: config, flows: make(map[string]*eventFlow), emit: emit}
}
func (a *eventAssembler) accept(event rawEvent) error {
	id := event.id()
	flow := a.flows[id]
	if flow == nil {
		if len(a.flows) >= 8192 {
			return errors.New("eBPF connection limit reached")
		}
		flow = &eventFlow{pending: make(map[uint64]*eventGroup)}
		a.flows[id] = flow
	}
	if event.sequence < flow.next {
		return errors.New("duplicate eBPF sequence")
	}
	if len(flow.pending) >= 64 && flow.pending[event.sequence] == nil {
		return errors.New("eBPF reorder queue overflow")
	}
	group := flow.pending[event.sequence]
	if group == nil {
		group = &eventGroup{first: event, parts: make(map[uint32][]byte), at: time.Now()}
		flow.pending[event.sequence] = group
	}
	if event.kind != group.first.kind || event.uid != group.first.uid || event.length != group.first.length {
		return errors.New("conflicting eBPF fragments")
	}
	if event.flags != 0 {
		group.first.flags = event.flags
	}
	if len(event.data) > 0 {
		if _, exists := group.parts[event.offset]; exists {
			return errors.New("duplicate eBPF fragment")
		}
		if event.offset%4096 != 0 || len(event.data) != min(4096, int(event.length-event.offset)) {
			return errors.New("invalid eBPF fragment alignment")
		}
		group.parts[event.offset] = event.data
		group.size += len(event.data)
		a.bytes += len(event.data)
		if a.bytes > 4*1024*1024 {
			return errors.New("eBPF reorder memory limit reached")
		}
	}
	for {
		group = flow.pending[flow.next]
		if group == nil || group.first.flags == 0 && group.size != int(group.first.length) {
			break
		}
		event = group.first
		message := pipeEvent{ID: id, PID: event.pid, UID: event.uid}
		switch event.kind {
		case 1:
			if flow.next != 0 {
				return errors.New("eBPF connection opened twice")
			}
			message.Type = "open"
			message.Packages = a.config.Packages[fmt.Sprint(event.uid)]
			if err := a.emit(message); err != nil {
				return err
			}
		case 4:
			message.Type = "close"
			if err := a.emit(message); err != nil {
				return err
			}
		case 2, 3:
			message.Type = "data"
			message.Direction = "write"
			if event.kind == 3 {
				message.Direction = "read"
			}
			if event.flags != 0 {
				message.Gap = true
				if err := a.emit(message); err != nil {
					return err
				}
			} else {
				for offset := uint32(0); offset < event.length; offset += 4096 {
					message.Data = group.parts[offset]
					if len(message.Data) == 0 {
						return errors.New("missing eBPF chunk")
					}
					if err := a.emit(message); err != nil {
						return err
					}
				}
			}
		}
		a.bytes -= group.size
		delete(flow.pending, flow.next)
		flow.next++
		if event.kind == 4 {
			if len(flow.pending) != 0 {
				return errors.New("eBPF data after SSL_free")
			}
			delete(a.flows, id)
			break
		}
	}
	return nil
}
func (a *eventAssembler) checkLoss(now time.Time) error {
	for _, flow := range a.flows {
		for _, group := range flow.pending {
			if now.Sub(group.at) > 3*time.Second {
				return errors.New("eBPF sequence gap; captured transactions are incomplete")
			}
		}
	}
	return nil
}
