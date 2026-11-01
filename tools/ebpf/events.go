package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/cilium/ebpf/perf"
	"golang.org/x/sys/unix"
)

type rawEvent struct {
	ssl, generation, sequence, otherSequence uint64
	pid, uid, kind, length, offset, flags    uint32
	data                                     []byte
}

// SeeFlowerX/ebpf preserves the PERF_SAMPLE_RAW length word in RawSample so
// stackplz can also decode registers/stacks. NewReader enables only RAW here.
// Strip this envelope explicitly; it is not the upstream cilium slice format.
func decodePerfRecord(record perf.Record) (rawEvent, error) {
	if record.RecordType != unix.PERF_RECORD_SAMPLE || len(record.RawSample) < 4 {
		return rawEvent{}, errors.New("unexpected eBPF perf record")
	}
	data := record.RawSample
	size := int(binary.LittleEndian.Uint32(data))
	if size < 72 || size > 72+4096+7 || size > len(data)-4 || len(data)-4-size > 7 {
		return rawEvent{}, errors.New("invalid eBPF perf sample length")
	}
	return decodeEvent(data[4 : 4+size])
}

func decodeEvent(data []byte) (rawEvent, error) {
	if len(data) < 72 {
		return rawEvent{}, errors.New("short eBPF event")
	}
	b := binary.LittleEndian
	event := rawEvent{otherSequence: b.Uint64(data[0:]), ssl: b.Uint64(data[8:]), generation: b.Uint64(data[16:]), sequence: b.Uint64(data[24:]),
		pid: b.Uint32(data[32:]), uid: b.Uint32(data[40:]), kind: b.Uint32(data[44:]), length: b.Uint32(data[52:]),
		offset: b.Uint32(data[56:]), flags: b.Uint32(data[64:])}
	length := b.Uint32(data[60:])
	if length > 4096 || int(length)+72 > len(data) || event.kind < 1 || event.kind > 5 ||
		event.length > 128*1024 || event.offset > event.length || length > event.length-event.offset {
		return rawEvent{}, fmt.Errorf("invalid eBPF event bounds: kind=%d total=%d offset=%d captured=%d sample=%d",
			event.kind, event.length, event.offset, length, len(data))
	}
	if event.kind == 5 && (event.flags < 1 || event.flags > 8 || event.length != 0 || length != 0 || event.sequence != 0) {
		return rawEvent{}, errors.New("invalid eBPF gap event")
	}
	event.data = append([]byte(nil), data[72:72+length]...)
	return event, nil
}
func (e rawEvent) id() string { return fmt.Sprintf("%d-%x-%x", e.pid, e.ssl, e.generation) }

type pipeEvent struct {
	Type      string   `json:"type"`
	Version   int      `json:"version,omitempty"`
	Stage     string   `json:"stage,omitempty"`
	ID        string   `json:"id,omitempty"`
	PID       uint32   `json:"pid,omitempty"`
	UID       uint32   `json:"uid,omitempty"`
	Packages  []string `json:"packages,omitempty"`
	Direction string   `json:"direction,omitempty"`
	Data      []byte   `json:"data,omitempty"`
	Gap       bool     `json:"gap,omitempty"`
	Libraries int      `json:"libraries,omitempty"`
	Scanning  bool     `json:"scanning,omitempty"`
	Error     string   `json:"error,omitempty"`
	Time      int64    `json:"time,omitempty"`
	Level     string   `json:"level,omitempty"`
	Message   string   `json:"message,omitempty"`
}

func diagnosticEvent(level, message string, startup bool) pipeEvent {
	event := pipeEvent{Type: "log", Version: 1, Time: time.Now().UnixMilli(), Level: level, Message: fmt.Sprintf("%.4096s", message)}
	if startup {
		event.Type, event.Stage = "progress", fmt.Sprintf("%.512s", message)
	}
	return event
}

type eventGroup struct {
	first rawEvent
	parts map[uint32][]byte
	size  int
	at    time.Time
}
type eventKey struct {
	direction uint32
	sequence  uint64
}
type eventFlow struct {
	open    bool
	next    [2]uint64
	pending map[eventKey]*eventGroup
	closing *rawEvent
	closeAt time.Time
	broken  *rawEvent
	gapSent bool
	created time.Time
}
type eventAssembler struct {
	config  collectorConfig
	flows   map[string]*eventFlow
	retired map[string]time.Time
	bytes   int
	emit    func(pipeEvent) error
}

func newAssembler(config collectorConfig, emit func(pipeEvent) error) *eventAssembler {
	return &eventAssembler{config: config, flows: make(map[string]*eventFlow), retired: make(map[string]time.Time), emit: emit}
}
func (a *eventAssembler) accept(event rawEvent) error {
	id := event.id()
	if _, done := a.retired[id]; done {
		return nil
	}
	flow := a.flows[id]
	if flow == nil {
		if len(a.flows) >= 8192 {
			return errors.New("eBPF connection limit reached")
		}
		flow = &eventFlow{next: [2]uint64{1, 1}, pending: make(map[eventKey]*eventGroup), created: time.Now()}
		a.flows[id] = flow
	}
	switch event.kind {
	case 1:
		if flow.open || event.sequence != 0 {
			return errors.New("duplicate eBPF connection open")
		}
		flow.open = true
		if err := a.emit(pipeEvent{Type: "open", ID: id, PID: event.pid, UID: event.uid,
			Packages: a.config.Packages[fmt.Sprint(event.uid)]}); err != nil {
			return err
		}
	case 4:
		if flow.closing != nil {
			return errors.New("duplicate eBPF connection close")
		}
		copy := event
		flow.closing = &copy
		flow.closeAt = time.Now()
		if event.flags != 0 {
			a.breakFlow(flow, event)
		}
	case 5:
		a.breakFlow(flow, event)
	case 2, 3:
		if flow.broken != nil {
			return nil
		}
		direction := int(event.kind - 2)
		if event.sequence < flow.next[direction] {
			return errors.New("duplicate eBPF sequence")
		}
		key := eventKey{event.kind, event.sequence}
		group := flow.pending[key]
		if group == nil {
			if len(flow.pending) >= 64 {
				return errors.New("eBPF reorder queue overflow")
			}
			group = &eventGroup{first: event, parts: make(map[uint32][]byte), at: time.Now()}
			flow.pending[key] = group
		}
		if event.uid != group.first.uid || event.length != group.first.length {
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
	default:
		return errors.New("invalid eBPF event kind")
	}
	if !flow.open {
		return nil
	}
	if flow.broken != nil {
		if !flow.gapSent {
			if err := a.emit(pipeEvent{Type: "data", ID: id, PID: event.pid, UID: event.uid,
				Direction: "write", Gap: true, Error: gapReason(flow.broken.flags)}); err != nil {
				return err
			}
			flow.gapSent = true
		}
		if flow.closing != nil {
			return a.closeFlow(id, flow.closing.pid, flow.closing.uid)
		}
		return nil
	}
	for direction := 0; direction < 2; direction++ {
		for {
			key := eventKey{uint32(direction + 2), flow.next[direction]}
			group := flow.pending[key]
			if group == nil || group.first.flags == 0 && group.size != int(group.first.length) {
				break
			}
			event = group.first
			message := pipeEvent{Type: "data", ID: id, PID: event.pid, UID: event.uid, Direction: "write"}
			if direction == 1 {
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
			a.bytes -= group.size
			delete(flow.pending, key)
			flow.next[direction]++
		}
	}
	if closing := flow.closing; closing != nil {
		if flow.next[0] > closing.sequence+1 || flow.next[1] > closing.otherSequence+1 {
			return errors.New("eBPF data after SSL_free")
		}
		if flow.next[0] == closing.sequence+1 && flow.next[1] == closing.otherSequence+1 {
			if len(flow.pending) != 0 {
				return errors.New("eBPF fragments after SSL_free")
			}
			return a.closeFlow(id, closing.pid, closing.uid)
		}
	}
	return nil
}

func (a *eventAssembler) breakFlow(flow *eventFlow, event rawEvent) {
	if flow.broken != nil {
		return
	}
	flow.broken = &event
	for _, group := range flow.pending {
		a.bytes -= group.size
	}
	clear(flow.pending)
}

func gapReason(reason uint32) string {
	switch reason {
	case 1:
		return "TLS call exceeds the 128 KiB capture limit or returned an invalid length"
	case 2:
		return "TLS calls overlapped in the same direction; their byte order is unknown"
	case 3:
		return "TLS plaintext could not be read from the application"
	case 5:
		return "A TLS call return was not observed; its SSL generation is incomplete"
	case 6:
		return "SSL_free or SSL_clear ran while TLS I/O was still active"
	case 7:
		return "SSL_new reused an address before the previous SSL lifetime ended"
	case 8:
		return "TLS wrapper nesting exceeded the capture limit"
	default:
		return "TLS object lifetime or call tracking was lost"
	}
}

func (a *eventAssembler) closeFlow(id string, pid, uid uint32) error {
	if err := a.emit(pipeEvent{Type: "close", ID: id, PID: pid, UID: uid}); err != nil {
		return err
	}
	delete(a.flows, id)
	if len(a.retired) >= 16384 {
		return errors.New("eBPF retired connection limit reached")
	}
	// A close may overtake another CPU's gap event. Remember retired
	// generations so that late events cannot recreate a completed stream.
	a.retired[id] = time.Now()
	return nil
}

func (a *eventAssembler) checkLoss(now time.Time) error {
	for id, at := range a.retired {
		if now.Sub(at) > 10*time.Second {
			delete(a.retired, id)
		}
	}
	for _, flow := range a.flows {
		if !flow.open && now.Sub(flow.created) > 3*time.Second {
			return errors.New("eBPF connection start was lost")
		}
		for _, group := range flow.pending {
			if now.Sub(group.at) > 3*time.Second {
				return errors.New("eBPF sequence gap; captured transactions are incomplete")
			}
		}
		if flow.closing != nil && now.Sub(flow.closeAt) > 3*time.Second {
			return errors.New("eBPF close arrived with missing data")
		}
	}
	return nil
}
