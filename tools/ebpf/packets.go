package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const packetSnaplen = 32768

type packetTuple struct{ source, destination netip.AddrPort }

func (t packetTuple) reverse() packetTuple { return packetTuple{t.destination, t.source} }

type networkPacket struct {
	tuple    packetTuple
	data     []byte
	at       int64
	length   int
	err      error
	sequence uint32
	flags    byte
}
type packetSource struct {
	fd      int
	dropped atomic.Uint64
}

func openPacketSource() (*packetSource, error) {
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, int(uint16(unix.ETH_P_ALL)<<8))
	if err != nil {
		return nil, err
	}
	if err = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_RCVBUF, 4*1024*1024); err == nil {
		err = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_TIMESTAMPNS, 1)
	}
	if err != nil {
		unix.Close(fd)
		return nil, err
	}
	return &packetSource{fd: fd}, nil
}

func (s *packetSource) read(ctx context.Context, results chan<- networkPacket) {
	buffer, control := make([]byte, packetSnaplen), make([]byte, 128)
	fds := []unix.PollFd{{Fd: int32(s.fd), Events: unix.POLLIN}}
	for ctx.Err() == nil {
		if _, err := unix.Poll(fds, 100); err != nil && !errors.Is(err, unix.EINTR) {
			select {
			case results <- networkPacket{err: err}:
			case <-ctx.Done():
			}
			return
		}
		n, oob, _, from, err := unix.Recvmsg(s.fd, buffer, control, unix.MSG_TRUNC)
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			select {
			case results <- networkPacket{err: err}:
			case <-ctx.Done():
			}
			return
		}
		address, ok := from.(*unix.SockaddrLinklayer)
		if !ok || address.Hatype == unix.ARPHRD_LOOPBACK && address.Pkttype == unix.PACKET_OUTGOING {
			continue
		}
		data := buffer[:min(n, len(buffer))]
		packet, ok := parseTCPPacket(data)
		if !ok {
			continue
		}
		at := time.Now().UnixNano()
		messages, _ := unix.ParseSocketControlMessage(control[:oob])
		for _, message := range messages {
			if message.Header.Level == unix.SOL_SOCKET && message.Header.Type == unix.SO_TIMESTAMPNS && len(message.Data) >= 16 {
				seconds, nanos := binary.LittleEndian.Uint64(message.Data), binary.LittleEndian.Uint64(message.Data[8:])
				if seconds < 1<<33 && nanos < 1e9 {
					at = int64(seconds)*1e9 + int64(nanos)
				}
			}
		}
		packet.data, packet.at, packet.length = append([]byte(nil), data...), at, n
		select {
		case results <- packet:
		case <-ctx.Done():
			return
		default:
			s.dropped.Add(1)
		}
	}
}

// Packet bytes are the actual IPv4/IPv6 datagrams returned by SOCK_DGRAM.
// Fragmented IP cannot be matched safely without IP reassembly and is omitted.
func parsePacketTuple(data []byte) (packetTuple, bool) {
	packet, ok := parseTCPPacket(data)
	return packet.tuple, ok
}

func parseTCPPacket(data []byte) (networkPacket, bool) {
	var source, destination netip.Addr
	var offset int
	if len(data) >= 20 && data[0]>>4 == 4 {
		offset = int(data[0]&15) * 4
		if offset < 20 || len(data) < offset+20 || data[9] != 6 || binary.BigEndian.Uint16(data[6:])&0x3fff != 0 {
			return networkPacket{}, false
		}
		source, destination = netip.AddrFrom4([4]byte(data[12:16])), netip.AddrFrom4([4]byte(data[16:20]))
	} else if len(data) >= 60 && data[0]>>4 == 6 {
		source, destination = netip.AddrFrom16([16]byte(data[8:24])).Unmap(), netip.AddrFrom16([16]byte(data[24:40])).Unmap()
		next := data[6]
		offset = 40
		for count := 0; next != 6 && count < 8; count++ {
			if len(data) < offset+2 {
				return networkPacket{}, false
			}
			var length int
			switch next {
			case 0, 43, 60:
				length = (int(data[offset+1]) + 1) * 8
			case 51:
				length = (int(data[offset+1]) + 2) * 4
			default:
				return networkPacket{}, false
			}
			next = data[offset]
			offset += length
		}
		if next != 6 || len(data) < offset+20 {
			return networkPacket{}, false
		}
	} else {
		return networkPacket{}, false
	}
	if size := int(data[offset+12]>>4) * 4; size < 20 || len(data) < offset+size {
		return networkPacket{}, false
	}
	a, b := binary.BigEndian.Uint16(data[offset:]), binary.BigEndian.Uint16(data[offset+2:])
	if a == 0 || b == 0 {
		return networkPacket{}, false
	}
	return networkPacket{tuple: packetTuple{netip.AddrPortFrom(source, a), netip.AddrPortFrom(destination, b)},
		sequence: binary.BigEndian.Uint32(data[offset+4:]), flags: data[offset+13]}, true
}

type packetOwner struct {
	id          string
	tuple       packetTuple
	until       time.Time
	start       int64
	synSequence uint32
	hasSYN      bool
	packets     uint64
}
type packetCapture struct {
	netns           uint32
	owners          map[packetTuple]*packetOwner
	blocked         map[packetTuple]bool
	seen            map[packetTuple]string
	closed          map[string]time.Time
	pending         []networkPacket
	bytes           int
	emit            func(pipeEvent) error
	pendingWarned   bool
	namespaceWarned bool
}

func newPacketCapture(emit func(pipeEvent) error) (*packetCapture, error) {
	info, err := os.Stat("/proc/self/ns/net")
	if err != nil {
		return nil, err
	}
	return &packetCapture{netns: uint32(info.Sys().(*syscall.Stat_t).Ino), owners: make(map[packetTuple]*packetOwner),
		blocked: make(map[packetTuple]bool), seen: make(map[packetTuple]string), closed: make(map[string]time.Time), emit: emit}, nil
}

func (p *packetCapture) forget(id string) {
	for tuple, owner := range p.owners {
		if owner.id == id {
			delete(p.owners, tuple)
		}
	}
}

func (p *packetCapture) observe(event pipeEvent) error {
	p.expire(time.Now())
	if event.Type == "close" {
		if len(p.closed) >= 8192 {
			return errors.New("network packet close history limit reached")
		}
		p.closed[event.ID] = time.Now().Add(3 * time.Second)
		for _, owner := range p.owners {
			if owner.id == event.ID {
				owner.until = p.closed[event.ID]
			}
		}
		return nil
	}
	if event.Type != "socket" {
		return nil
	}
	if until := p.closed[event.ID]; !until.IsZero() && !time.Now().Before(until) {
		return nil
	}
	if event.SocketConflict {
		for tuple, id := range p.seen {
			if id == event.ID {
				p.blocked[tuple] = true
			}
		}
		p.forget(event.ID)
		return p.emit(pipeEvent{Type: "packet_loss", Error: "Network packets omitted: TLS socket association became ambiguous"})
	}
	if event.Socket == nil {
		return nil
	}
	if event.Socket.NetNS != p.netns {
		if !p.namespaceWarned {
			p.namespaceWarned = true
			return p.emit(pipeEvent{Type: "packet_loss", Error: "Network packets unavailable for a target socket in another network namespace"})
		}
		return nil
	}
	a, err := netip.ParseAddrPort(event.Socket.Source)
	if err != nil {
		return err
	}
	b, err := netip.ParseAddrPort(event.Socket.Destination)
	if err != nil {
		return err
	}
	tuple := packetTuple{a, b}
	if p.blocked[tuple] {
		return nil
	}
	if prior := p.seen[tuple]; prior != "" {
		if prior == event.ID {
			return nil
		}
		// Packet headers carry no inode or TLS generation. Even after close,
		// a reused tuple cannot safely accept an old CPU's delayed packets.
		return p.block(tuple, "Network packets omitted: a TCP tuple was shared or reused by another TLS generation")
	}
	if len(p.seen) >= 2048 {
		return p.emit(pipeEvent{Type: "packet_loss", Error: "Network packet socket limit reached"})
	}
	owner := &packetOwner{id: event.ID, tuple: tuple, start: event.TimestampNS, until: p.closed[event.ID]}
	p.seen[tuple], p.seen[tuple.reverse()] = event.ID, event.ID
	p.owners[tuple], p.owners[tuple.reverse()] = owner, owner
	kept := p.pending[:0]
	for _, packet := range p.pending {
		if packet.tuple == tuple || packet.tuple == tuple.reverse() {
			p.bytes -= len(packet.data)
			if err := p.deliver(owner, packet); err != nil {
				return err
			}
		} else {
			kept = append(kept, packet)
		}
	}
	clear(p.pending[len(kept):])
	p.pending = kept
	return nil
}

func (p *packetCapture) accept(packet networkPacket) error {
	p.expire(time.Now())
	if p.blocked[packet.tuple] {
		return nil
	}
	if owner := p.owners[packet.tuple]; owner != nil {
		return p.deliver(owner, packet)
	}
	if p.seen[packet.tuple] != "" {
		return nil
	}
	// Short pre-association history retains ClientHello/SYN while the socket
	// perf event crosses CPUs. Unselected traffic is never emitted or saved.
	p.pending = append(p.pending, packet)
	p.bytes += len(packet.data)
	for len(p.pending) > 4096 || p.bytes > 4*1024*1024 {
		p.bytes -= len(p.pending[0].data)
		p.pending[0] = networkPacket{}
		p.pending = p.pending[1:]
		if !p.pendingWarned {
			p.pendingWarned = true
			if err := p.emit(pipeEvent{Type: "packet_loss", Error: "Network packet pre-association buffer overflow; early packets may be missing"}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (p *packetCapture) expire(now time.Time) {
	for id, until := range p.closed {
		if !now.Before(until.Add(7 * time.Second)) {
			delete(p.closed, id)
		}
	}
	for tuple, owner := range p.owners {
		if !owner.until.IsZero() && !now.Before(owner.until) {
			delete(p.owners, tuple)
		}
	}
	for len(p.pending) > 0 && now.UnixNano()-p.pending[0].at > int64(3*time.Second) {
		p.bytes -= len(p.pending[0].data)
		p.pending[0] = networkPacket{}
		p.pending = p.pending[1:]
	}
}
func (p *packetCapture) block(tuple packetTuple, reason string) error {
	if prior := p.owners[tuple]; prior != nil {
		p.forget(prior.id)
	}
	p.blocked[tuple], p.blocked[tuple.reverse()] = true, true
	return p.emit(pipeEvent{Type: "packet_loss", Error: reason})
}
func (p *packetCapture) deliver(owner *packetOwner, packet networkPacket) error {
	if p.blocked[packet.tuple] || packet.at < owner.start {
		return nil
	}
	if packet.flags&0x12 == 0x02 { // SYN without ACK; permit only retransmission of the same SYN.
		if owner.hasSYN && owner.synSequence != packet.sequence || !owner.hasSYN && owner.packets != 0 {
			return p.block(packet.tuple, "Network packets omitted: a new TCP handshake reused a captured tuple")
		}
		owner.hasSYN, owner.synSequence = true, packet.sequence
	}
	owner.packets++
	return p.write(owner.id, packet)
}
func (p *packetCapture) write(id string, packet networkPacket) error {
	return p.emit(pipeEvent{Type: "packet", ID: id, Data: packet.data, TimestampNS: packet.at, PacketLength: packet.length})
}

func (s *packetSource) losses() (uint64, error) {
	stats, err := unix.GetsockoptTpacketStats(s.fd, unix.SOL_PACKET, unix.PACKET_STATISTICS)
	if err != nil {
		return 0, fmt.Errorf("read packet loss counters: %w", err)
	}
	return uint64(stats.Drops) + s.dropped.Swap(0), nil
}
