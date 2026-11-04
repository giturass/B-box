package httpdebug

import "net/http"

// Recording changes at HTTP request/TCP stream boundaries. Existing transports,
// TLS handshakes, rewrites and cron jobs belong to the service, not a recording.
func (e *Engine) beginRecording(metadata Metadata, request *http.Request) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	config := e.recording.Load()
	if e.closed || config == nil || !config.matches(metadata) {
		return ""
	}
	return e.store.begin(metadata, request, config.BodyLimit)
}

func (e *Engine) stopRecording() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.recording.Swap(nil) == nil {
		return
	}
	// Freeze retained prefixes immediately. A later EOF or response must not
	// make a partial record look complete, or append data after the user stops.
	e.store.stopRecording()
}

func (s *Store) stopRecording() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.records {
		if record.finishing || record.Complete {
			continue
		}
		appendRecordNote(record, "Recording stopped; retained data may be incomplete; connection continues")
		s.finishLocked(record)
	}
	s.trimLocked()
}
