package httpdebug

import (
	"strings"
	"sync/atomic"
	"unicode/utf8"
)

type logWriter struct{ write func(level, message string) }

var serviceLogger atomic.Pointer[logWriter]

// SetLogger routes diagnostics and script console output to the existing
// service log. HTTP processing has no separate log history or reader API.
func SetLogger(write func(level, message string)) {
	if write == nil {
		serviceLogger.Store(nil)
	} else {
		serviceLogger.Store(&logWriter{write})
	}
}

func writeLog(level, message string) {
	logger := serviceLogger.Load()
	if logger == nil {
		return
	}
	message = strings.TrimSpace(strings.ToValidUTF8(message, "�"))
	if len(message) > 4096 {
		message = message[:4093]
		for !utf8.ValidString(message) {
			message = message[:len(message)-1]
		}
		message += "…"
	}
	if message != "" {
		logger.write(level, strings.Clone(message))
	}
}
