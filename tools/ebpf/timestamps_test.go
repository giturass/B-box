package main

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestClockSamplingKeepsEarlyClientHelloAndRejectsPriorPackets(t *testing.T) {
	const generation = uint64(time.Second)
	realtimeBase := time.Now().UnixNano() - int64(generation)
	// The first pair reproduces the 273 us scheduling delay seen in the
	// ARM64 wire fixture. Later samples bracket a normal clock read.
	delays := []int64{273000, 500, 2000, 1500, 3000}
	call := 0
	timestamp, err := generationTimestamp(generation, func(clock int32) (int64, error) {
		sample, part := call/3, call%3
		call++
		monotonic := int64(generation) + int64(sample+1)*1000000
		if part == 1 {
			if clock != unix.CLOCK_REALTIME {
				t.Fatal("wall time was not bracketed by monotonic reads")
			}
			return realtimeBase + monotonic + delays[sample], nil
		}
		if clock != unix.CLOCK_MONOTONIC {
			t.Fatal("clock sample has no monotonic bound")
		}
		if part == 2 {
			monotonic += delays[sample] * 2
		}
		return monotonic, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var emitted []pipeEvent
	packets, err := newPacketCapture(func(event pipeEvent) error { emitted = append(emitted, event); return nil })
	if err != nil {
		t.Fatal(err)
	}
	tuple := packetTuple{netip.MustParseAddrPort("127.0.0.1:1234"), netip.MustParseAddrPort("127.0.0.1:443")}
	actualStart := realtimeBase + int64(generation)
	for _, packet := range []networkPacket{
		{tuple: tuple, at: actualStart - 1, data: []byte("prior connection"), length: 16},
		{tuple: tuple, at: actualStart + 60000, data: []byte("ClientHello"), length: 11},
	} {
		if err := packets.accept(packet); err != nil {
			t.Fatal(err)
		}
	}
	if err := packets.observe(pipeEvent{Type: "socket", ID: "current", TimestampNS: timestamp,
		Socket: &socketInfo{NetNS: packets.netns, Source: tuple.source.String(), Destination: tuple.destination.String()}}); err != nil {
		t.Fatal(err)
	}
	if len(emitted) != 1 || string(emitted[0].Data) != "ClientHello" {
		t.Fatal("clock jitter dropped early TLS data or admitted an earlier lifetime", emitted)
	}
}

func TestClockSamplingPropagatesReadFailures(t *testing.T) {
	want := errors.New("clock unavailable")
	for _, failAt := range []int{0, 1, 2} {
		call := 0
		_, err := generationTimestamp(1, func(int32) (int64, error) {
			call++
			if call == failAt+1 {
				return 0, want
			}
			return 1, nil
		})
		if !errors.Is(err, want) {
			t.Fatal("clock read failure was ignored", failAt, err)
		}
	}
}
