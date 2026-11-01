package httpdebug

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestCaptureLogsBoundCountBytesAndUnicodeWithoutChangingSnapshots(t *testing.T) {
	var buffer captureLogBuffer
	for i := 0; i < maxCaptureLogs+20; i++ {
		buffer.append(int64(i+1), "debug", fmt.Sprintf("library-%d", i))
	}
	logs := buffer.snapshot()
	if len(logs) != maxCaptureLogs || logs[0].Message != "library-20" || logs[0].ID != 21 || logs[0].ReceivedAt != 21 {
		t.Fatalf("incorrect retained log range: count=%d first=%+v", len(logs), logs[0])
	}
	lastID := logs[len(logs)-1].ID
	buffer.clear()
	buffer.append(0, "invalid", "next capture")
	if next := buffer.snapshot()[0]; next.ID <= lastID || next.Level != "info" || next.ReceivedAt <= 0 {
		t.Fatalf("clear reused IDs or lost normalized fields: %+v", next)
	}
	if logs[0].Message != "library-20" {
		t.Fatal("clear mutated an existing snapshot")
	}
	for i := 0; i < 200; i++ {
		buffer.append(0, "warn", strings.Repeat("库🔍", 2000))
	}
	logs = buffer.snapshot()
	bytes := 0
	for _, log := range logs {
		bytes += len(log.Message)
		if len(log.Message) > maxCaptureLogLine || !utf8.ValidString(log.Message) || !strings.HasSuffix(log.Message, "…") {
			t.Fatal("log truncation broke its length or UTF-8 boundary")
		}
	}
	if bytes > maxCaptureLogBytes || len(logs) >= 200 {
		t.Fatalf("message byte limit was not enforced: count=%d bytes=%d", len(logs), bytes)
	}
	logs[0].Message = "changed copy"
	if buffer.snapshot()[0].Message == "changed copy" {
		t.Fatal("snapshot aliases the live log buffer")
	}
}

func TestCaptureLogsAllowConcurrentRecordingReadingAndClearing(t *testing.T) {
	var buffer captureLogBuffer
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 500; j++ {
				buffer.append(0, "info", "library inspected")
			}
		}()
	}
	for i := 0; i < 100; i++ {
		logs := buffer.snapshot()
		for j := 1; j < len(logs); j++ {
			if logs[j].ID <= logs[j-1].ID {
				t.Fatal("log IDs lost their order")
			}
		}
		buffer.clear()
	}
	workers.Wait()
}
