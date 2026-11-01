package httpdebug

import (
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxCaptureLogs     = 1000
	maxCaptureLogBytes = 512 * 1024
	maxCaptureLogLine  = 4096
)

type CaptureLog struct {
	ID         int64  `json:"id"`
	ReceivedAt int64  `json:"receivedAt"`
	Level      string `json:"level"`
	Message    string `json:"message"`
}

// Diagnostics belong to the capture process, not a visible page or request
// search. Keep a bounded history across stop/start, including startup failures.
type captureLogBuffer struct {
	mu           sync.Mutex
	entries      [maxCaptureLogs]CaptureLog
	start, count int
	bytes        int
	nextID       int64
}

var captureLogs captureLogBuffer

func (b *captureLogBuffer) append(at int64, level, message string) {
	message = strings.TrimSpace(strings.ToValidUTF8(message, "�"))
	if len(message) > maxCaptureLogLine {
		message = message[:maxCaptureLogLine-3]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
		message += "…"
	}
	if message == "" {
		return
	}
	// TrimSpace can retain a much larger backing string than the visible line.
	message = strings.Clone(message)
	switch level {
	case "debug", "info", "warn", "error":
	default:
		level = "info"
	}
	if at <= 0 {
		at = time.Now().UnixMilli()
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.count > 0 && (b.count == len(b.entries) || b.bytes+len(message) > maxCaptureLogBytes) {
		b.bytes -= len(b.entries[b.start].Message)
		b.entries[b.start] = CaptureLog{}
		b.start = (b.start + 1) % len(b.entries)
		b.count--
	}
	b.nextID++
	b.entries[(b.start+b.count)%len(b.entries)] = CaptureLog{b.nextID, at, level, message}
	b.bytes += len(message)
	b.count++
}

func (b *captureLogBuffer) snapshot() []CaptureLog {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]CaptureLog, b.count)
	for i := range result {
		result[i] = b.entries[(b.start+i)%len(b.entries)]
	}
	return result
}

func (b *captureLogBuffer) clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	clear(b.entries[:])
	b.start, b.count, b.bytes = 0, 0, 0
}

func AppendCaptureLog(level, message string) { captureLogs.append(0, level, message) }
func CaptureLogs() []CaptureLog              { return captureLogs.snapshot() }
func ClearCaptureLogs()                      { captureLogs.clear() }
