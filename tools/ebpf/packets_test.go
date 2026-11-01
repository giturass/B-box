package main

import (
	"encoding/binary"
	"net/netip"
	"testing"
	"time"
)

func TestSocketEventsWaitForOpenAndSurviveLateCloseDelivery(t *testing.T) {
	var events []pipeEvent
	a := newAssembler(collectorConfig{TLSKeys: true}, func(event pipeEvent) error { events = append(events, event); return nil })
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
	if err := a.accept(socket); err != nil || len(events) != count {
		t.Fatal("late socket event revived an ambiguous association", err)
	}
	secret := base
	secret.kind, secret.flags, secret.length = 7, 0, 164
	secret.data = make([]byte, 164)
	secret.data[0], secret.data[1] = 13, 48
	copy(secret.data[4:], "CLIENT_RANDOM")
	if err := a.accept(secret); err != nil {
		t.Fatal(err)
	}
	if events[len(events)-1].Type != "secret" || len(a.flows) != 0 {
		t.Fatal("late key recreated or lost a retired TLS generation")
	}
}

func TestPacketsRequireSameNamespaceAndUnambiguousSocketGeneration(t *testing.T) {
	var events []pipeEvent
	p := &packetCapture{netns: 1, owners: make(map[packetTuple]*packetOwner), blocked: make(map[packetTuple]bool),
		seen: make(map[packetTuple]string), closed: make(map[string]time.Time), emit: func(event pipeEvent) error { events = append(events, event); return nil }}
	tuple := packetTuple{netip.MustParseAddrPort("127.0.0.1:1234"), netip.MustParseAddrPort("127.0.0.2:443")}
	packet := networkPacket{tuple: tuple, data: []byte{0x45}, length: 1, at: time.Now().UnixNano()}
	if err := p.accept(packet); err != nil {
		t.Fatal(err)
	}
	event := pipeEvent{Type: "socket", ID: "first", Socket: &socketInfo{FD: 4, Inode: "1", NetNS: 2, Source: tuple.source.String(), Destination: tuple.destination.String()}}
	if err := p.observe(event); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "packet_loss" {
		t.Fatal("another namespace was not reported as unavailable")
	}
	events = nil
	event.Socket.NetNS = 1
	if err := p.observe(event); err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "packet" || events[0].ID != "first" {
		t.Fatal("pre-association ClientHello was not retained")
	}
	event.ID, event.Socket.Inode = "second", "2"
	if err := p.observe(event); err != nil {
		t.Fatal(err)
	}
	if err := p.accept(packet); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Type != "packet_loss" || len(p.owners) != 0 {
		t.Fatal("ambiguous live generations were merged")
	}
	// The same numeric FD on a different tuple belongs only to its new ID.
	event.Socket.Source = "127.0.0.1:2345"
	if err := p.observe(event); err != nil {
		t.Fatal(err)
	}
	packet.tuple.source = netip.MustParseAddrPort(event.Socket.Source)
	if err := p.accept(packet); err != nil {
		t.Fatal(err)
	}
	if events[len(events)-1].ID != "second" {
		t.Fatal("FD reuse selected a stale owner")
	}
	_ = p.observe(pipeEvent{Type: "close", ID: "second"})
	p.expire(time.Now().Add(4 * time.Second))
	if len(p.owners) != 0 || p.bytes != 0 {
		t.Fatal("closed sockets or expired prehistory leaked")
	}
}

func TestPacketTupleParsingIPv4IPv6AndFragments(t *testing.T) {
	v4 := make([]byte, 40)
	v4[0], v4[9], v4[32] = 0x45, 6, 0x50
	copy(v4[12:], []byte{127, 0, 0, 1})
	copy(v4[16:], []byte{127, 0, 0, 2})
	binary.BigEndian.PutUint16(v4[20:], 1234)
	binary.BigEndian.PutUint16(v4[22:], 443)
	if tuple, ok := parsePacketTuple(v4); !ok || tuple.destination.String() != "127.0.0.2:443" {
		t.Fatal(tuple, ok)
	}
	v4[6] = 0x20
	if _, ok := parsePacketTuple(v4); ok {
		t.Fatal("fragment was treated as a complete TCP packet")
	}
	v6 := make([]byte, 68)
	v6[0], v6[6], v6[40], v6[60] = 0x60, 0, 6, 0x50
	v6[23], v6[39] = 1, 2
	binary.BigEndian.PutUint16(v6[48:], 1234)
	binary.BigEndian.PutUint16(v6[50:], 443)
	if tuple, ok := parsePacketTuple(v6); !ok || tuple.destination.String() != "[::2]:443" {
		t.Fatal(tuple, ok)
	}
	v6[40] = 44
	if _, ok := parsePacketTuple(v6); ok {
		t.Fatal("IPv6 fragments were accepted without reassembly")
	}
}

func TestPacketsRejectPriorLifetimesNewSYNAndClosedTupleReuse(t *testing.T) {
	var events []pipeEvent
	p, err := newPacketCapture(func(event pipeEvent) error { events = append(events, event); return nil })
	if err != nil {
		t.Fatal(err)
	}
	tuple := packetTuple{netip.MustParseAddrPort("127.0.0.1:3456"), netip.MustParseAddrPort("127.0.0.2:443")}
	start := time.Now().UnixNano()
	packet := networkPacket{tuple: tuple, data: []byte{0x45}, length: 1, at: start - 1e9}
	if err := p.accept(packet); err != nil {
		t.Fatal(err)
	}
	socket := pipeEvent{Type: "socket", ID: "one", TimestampNS: start,
		Socket: &socketInfo{FD: 5, Inode: "1", NetNS: p.netns, Source: tuple.source.String(), Destination: tuple.destination.String()}}
	if err := p.observe(socket); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatal("prehistory from before this TLS generation leaked into the export")
	}
	packet.at, packet.flags, packet.sequence = start+1, 2, 100
	for i := 0; i < 2; i++ {
		if err := p.accept(packet); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 2 {
		t.Fatal("initial SYN/retransmission was discarded")
	}
	packet.sequence++
	if err := p.accept(packet); err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[2].Type != "packet_loss" || len(p.owners) != 0 {
		t.Fatal("new TCP handshake was assigned to the old TLS generation")
	}

	// Close can cross CPUs before the first socket event. It must still put
	// that late association on the short drain deadline.
	socket.ID, socket.Socket.Source = "late", "127.0.0.1:4567"
	if err := p.observe(pipeEvent{Type: "close", ID: "late"}); err != nil {
		t.Fatal(err)
	}
	if err := p.observe(socket); err != nil {
		t.Fatal(err)
	}
	p.expire(time.Now().Add(4 * time.Second))
	if len(p.owners) != 0 {
		t.Fatal("late socket event revived a closed connection indefinitely")
	}
	socket.ID = "reused-after-close"
	if err := p.observe(socket); err != nil {
		t.Fatal(err)
	}
	if len(p.owners) != 0 || events[len(events)-1].Type != "packet_loss" {
		t.Fatal("a retired TCP tuple was reused without a packet generation identifier")
	}
}
