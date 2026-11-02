package main

import (
	"errors"
	"math"

	"golang.org/x/sys/unix"
)

func clockNanos(clock int32) (int64, error) {
	var value unix.Timespec
	err := unix.ClockGettime(clock, &value)
	return value.Nano(), err
}

// Translate BPF's CLOCK_MONOTONIC generation into packet CLOCK_REALTIME.
// A scheduling delay between a single monotonic/realtime pair moves the start
// forward and can discard ClientHello. Bracket each wall-clock read and use the
// narrowest of several samples. The earlier monotonic reading gives a
// conservative upper bound, so jitter cannot admit a prior TLS lifetime.
func generationTimestamp(generation uint64, read func(int32) (int64, error)) (int64, error) {
	bestWindow := int64(math.MaxInt64)
	var offset int64
	for range 5 {
		before, err := read(unix.CLOCK_MONOTONIC)
		if err != nil {
			return 0, err
		}
		realtime, err := read(unix.CLOCK_REALTIME)
		if err != nil {
			return 0, err
		}
		after, err := read(unix.CLOCK_MONOTONIC)
		if err != nil {
			return 0, err
		}
		if before < 0 || after < before {
			return 0, errors.New("invalid monotonic clock sample")
		}
		if window := after - before; window < bestWindow {
			bestWindow, offset = window, realtime-before
		}
	}
	return offset + int64(generation), nil
}
