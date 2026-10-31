package httpdebug

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// This is a private, versioned pipe protocol, not a sing-box configuration or
// network service. A flow starts only after an observed SSL_new.
type plaintextEvent struct {
	Type      string   `json:"type"`
	Version   int      `json:"version,omitempty"`
	ID        string   `json:"id,omitempty"`
	PID       uint32   `json:"pid,omitempty"`
	UID       int32    `json:"uid,omitempty"`
	Packages  []string `json:"packages,omitempty"`
	Direction string   `json:"direction,omitempty"`
	Data      []byte   `json:"data,omitempty"`
	Gap       bool     `json:"gap,omitempty"`
	Libraries int64    `json:"libraries,omitempty"`
	Error     string   `json:"error,omitempty"`
}

func (e *Engine) readPlaintext(reader io.Reader) {
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stopped:
				return
			case now := <-ticker.C:
				e.passive.expire(now)
			}
		}
	}()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 8192), 64*1024)
	ready := false
	for scanner.Scan() {
		var event plaintextEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			failCapture(e, fmt.Errorf("invalid eBPF event: %w", err))
			return
		}
		if !ready {
			if event.Type != "ready" || event.Version != 1 || event.Libraries < 1 {
				failCapture(e, errors.New("eBPF collector did not attach compatible TLS probes"))
				return
			}
			ready = true
			e.libraries.Store(event.Libraries)
			continue
		}
		if event.Type == "error" {
			failCapture(e, fmt.Errorf("eBPF capture stopped: %.4096s", event.Error))
			return
		}
		if event.Type == "status" {
			e.libraries.Store(event.Libraries)
			continue
		}
		if err := e.passive.accept(event); err != nil {
			failCapture(e, err)
			return
		}
	}
	err := scanner.Err()
	if err == nil {
		err = errors.New("eBPF collector disconnected")
	}
	failCapture(e, err)
}

type pendingFlow struct {
	metadata Metadata
	at       time.Time
}

type plaintextCapture struct {
	engine *Engine
	mu     sync.Mutex
	closed bool
	known  map[string]pendingFlow
	flows  map[string]*plaintextFlow
	wg     sync.WaitGroup
}

func newPlaintextCapture(e *Engine) *plaintextCapture {
	return &plaintextCapture{engine: e, known: make(map[string]pendingFlow), flows: make(map[string]*plaintextFlow)}
}

func (p *plaintextCapture) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.flows)
}

func (p *plaintextCapture) accept(event plaintextEvent) error {
	if event.ID == "" || len(event.ID) > 96 || len(event.Data) > 4096 || len(event.Packages) > 256 {
		return errors.New("invalid eBPF connection event")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	now := time.Now()
	switch event.Type {
	case "open":
		if p.flows[event.ID] != nil {
			return errors.New("duplicate eBPF connection")
		}
		for id, value := range p.known {
			if now.Sub(value.at) > 2*time.Minute {
				delete(p.known, id)
			}
		}
		if len(p.known) >= 1024 {
			p.engine.skipped.Add(1)
			return nil
		}
		p.known[event.ID] = pendingFlow{Metadata{Protocol: "tls", UID: event.UID, PID: event.PID,
			Packages: event.Packages, CaptureSource: "eBPF"}, now}
	case "data":
		if event.Direction != "read" && event.Direction != "write" {
			return errors.New("invalid eBPF direction")
		}
		flow := p.flows[event.ID]
		if flow == nil {
			pending, ok := p.known[event.ID]
			if !ok {
				return nil
			} // Never restart a flow after a gap, overflow or eviction.
			delete(p.known, event.ID)
			if len(p.flows) >= MaxConnections {
				p.engine.skipped.Add(1)
				return nil
			}
			flow = newPlaintextFlow(p.engine, pending.metadata)
			p.flows[event.ID] = flow
			p.wg.Add(1)
			go func(id string, f *plaintextFlow) {
				defer p.wg.Done()
				f.run()
				p.mu.Lock()
				delete(p.flows, id)
				p.mu.Unlock()
			}(event.ID, flow)
		}
		if event.Gap {
			flow.stop(errors.New("eBPF plaintext was truncated or lost; reconnect to capture again"))
		} else if len(event.Data) > 0 {
			flow.lastActivity.Store(now.Unix())
			input := flow.request
			if event.Direction == "read" {
				input = flow.response
			}
			if !input.push(event.Data) {
				flow.stop(errors.New("eBPF parsing queue overflow; transaction is incomplete"))
			}
		}
	case "close":
		delete(p.known, event.ID)
		if flow := p.flows[event.ID]; flow != nil {
			flow.stop(nil)
		}
	default:
		return fmt.Errorf("unknown eBPF event %q", event.Type)
	}
	return nil
}

func (p *plaintextCapture) close(reason string) {
	p.mu.Lock()
	p.closed = true
	p.known = nil
	for _, f := range p.flows {
		f.stop(errors.New(reason))
	}
	p.mu.Unlock()
	p.wg.Wait()
}

// A bounded producer never waits for HTTP parsing or disk IO. Overflow ends the
// connection instead of joining non-contiguous fragments into a plausible body.
type plaintextInput struct {
	mu    sync.Mutex
	cond  *sync.Cond
	queue [][]byte
	bytes int
	err   error
}

func newPlaintextInput() *plaintextInput {
	r := new(plaintextInput)
	r.cond = sync.NewCond(&r.mu)
	return r
}
func (r *plaintextInput) push(data []byte) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return true
	}
	if r.bytes+len(data) > 256*1024 {
		return false
	}
	r.queue = append(r.queue, data)
	r.bytes += len(data)
	r.cond.Signal()
	return true
}
func (r *plaintextInput) Read(data []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for r.bytes == 0 && r.err == nil {
		r.cond.Wait()
	}
	if r.bytes == 0 {
		return 0, r.err
	}
	n := copy(data, r.queue[0])
	r.bytes -= n
	r.queue[0] = r.queue[0][n:]
	if len(r.queue[0]) == 0 {
		r.queue[0] = nil
		r.queue = r.queue[1:]
	}
	return n, nil
}
func (r *plaintextInput) close(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil && (err == nil || !errors.Is(r.err, io.EOF)) {
		return
	}
	if err == nil {
		err = io.EOF
	} else {
		r.queue = nil
		r.bytes = 0
	}
	r.err = err
	r.cond.Broadcast()
}

type plaintextFlow struct {
	engine            *Engine
	metadata          Metadata
	request, response *plaintextInput
	done              chan struct{}
	once              sync.Once
	mu                sync.Mutex
	failure           error
	records           map[string][2]bool
	lastActivity      atomic.Int64
}

func newPlaintextFlow(e *Engine, metadata Metadata) *plaintextFlow {
	return &plaintextFlow{engine: e, metadata: metadata, request: newPlaintextInput(),
		response: newPlaintextInput(), done: make(chan struct{}), records: make(map[string][2]bool)}
}

func (f *plaintextFlow) stop(err error) {
	if err != nil {
		f.mu.Lock()
		if f.failure == nil {
			f.failure = err
		}
		f.mu.Unlock()
	}
	f.request.close(err)
	f.response.close(err)
	f.once.Do(func() { close(f.done) })
}

func (f *plaintextFlow) run() {
	defer func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		err := f.failure
		if err == nil {
			err = errors.New("TLS connection closed before the transaction finished")
		}
		for id := range f.records {
			f.engine.store.setError(id, err)
			f.engine.store.finish(id)
		}
	}()
	request := bufio.NewReaderSize(f.request, 4096)
	prefix, err := request.Peek(3)
	if err == nil {
		if string(prefix) == "PRI" {
			err = f.http2(request)
		} else {
			err = f.http1(request)
		}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		f.engine.skippedFlow(err)
		f.stop(err)
	} else {
		f.stop(nil)
	}
}

func (f *plaintextFlow) begin(request *http.Request) string {
	metadata := f.metadata
	metadata.Host = normalizeHost(request.URL.Hostname())
	port := 443
	if value := request.URL.Port(); value != "" {
		var err error
		port, err = strconv.Atoi(value)
		if err != nil || port < 1 || port > 65535 {
			return ""
		}
	}
	metadata.Port = uint16(port)
	if !validHost(metadata.Host) || !f.engine.config.matches(metadata) {
		return ""
	}
	id := f.engine.store.begin(metadata, request, f.engine.config.BodyLimit)
	f.engine.store.addNote(id, "TLS plaintext observed with eBPF. Port is the HTTP authority port; socket endpoints and sing-box route are unavailable.")
	if id != "" {
		f.mu.Lock()
		f.records[id] = [2]bool{}
		f.mu.Unlock()
	}
	return id
}
func (f *plaintextFlow) complete(id string, response bool) {
	if id == "" {
		return
	}
	f.engine.store.bodyComplete(id, response)
	f.mu.Lock()
	defer f.mu.Unlock()
	done, ok := f.records[id]
	if !ok {
		return
	}
	index := 0
	if response {
		index = 1
	}
	done[index] = true
	if done[0] && done[1] {
		f.engine.store.finish(id)
		delete(f.records, id)
	} else {
		f.records[id] = done
	}
}

func (f *plaintextFlow) requestURL(r *http.Request) error {
	if r.Method == http.MethodConnect {
		r.URL = &url.URL{Scheme: "https", Host: r.Host}
	} else {
		if r.URL.IsAbs() && r.URL.Scheme != "https" {
			return errors.New("unexpected scheme in TLS plaintext")
		}
		r.URL.Scheme, r.URL.Host = "https", r.Host
	}
	if r.URL.User != nil || r.URL.Host == "" {
		return errors.New("invalid HTTPS authority")
	}
	// URL.Port already validates malformed/negative ports in parsed requests;
	// validate authorities assigned from Host as well.
	_, err := url.Parse(r.URL.String())
	return err
}

// Header bytes buffered in bufio count toward the same limit. The limiter is
// relaxed only while a framed body is consumed, so long/chunked bodies stream.
type headerBudget struct {
	reader    io.Reader
	remaining int64
}

func (r *headerBudget) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, errors.New("captured HTTP headers exceed 64 KiB")
	}
	if r.remaining > 0 && int64(len(p)) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	if r.remaining > 0 {
		r.remaining -= int64(n)
	}
	return n, err
}

type plaintextRequest struct {
	request *http.Request
	id      string
}

func (f *plaintextFlow) http1(prefix *bufio.Reader) error {
	// Prefix may contain body bytes; all bytes still pass the bounded reader.
	input := &headerBudget{reader: prefix, remaining: -1}
	requests := bufio.NewReaderSize(input, 4096)
	queue := make(chan plaintextRequest, 16)
	responseDone := make(chan error, 1)
	go func() {
		err := f.http1Responses(queue)
		if err != nil {
			f.stop(err)
		}
		responseDone <- err
	}()
	var parseErr error
	for {
		input.remaining = int64(MaxHeaderBytes - requests.Buffered())
		request, err := http.ReadRequest(requests)
		input.remaining = -1
		if err != nil {
			parseErr = err
			break
		}
		if request.ProtoMajor != 1 {
			parseErr = errors.New("unsupported captured HTTP protocol")
			break
		}
		if err = f.requestURL(request); err != nil {
			parseErr = err
			break
		}
		id := f.begin(request)
		select {
		case queue <- plaintextRequest{request, id}:
		case <-f.done:
			// SSL_free can arrive before the parser drains queued bytes.
			select {
			case queue <- plaintextRequest{request, id}:
			default:
				parseErr = io.ErrUnexpectedEOF
			}
		}
		if parseErr != nil {
			break
		}
		if err = f.copyBody(id, false, request.Body); err != nil {
			parseErr = err
			break
		}
		f.complete(id, false)
	}
	close(queue)
	if parseErr != nil && !errors.Is(parseErr, io.EOF) {
		f.stop(parseErr)
	}
	responseErr := <-responseDone
	if parseErr != nil && !errors.Is(parseErr, io.EOF) {
		return parseErr
	}
	return responseErr
}

func (f *plaintextFlow) http1Responses(queue <-chan plaintextRequest) error {
	input := &headerBudget{reader: f.response, remaining: -1}
	responses := bufio.NewReaderSize(input, 4096)
	for item := range queue {
		for {
			input.remaining = int64(MaxHeaderBytes - responses.Buffered())
			response, err := http.ReadResponse(responses, item.request)
			input.remaining = -1
			if err != nil {
				return err
			}
			if response.StatusCode >= 100 && response.StatusCode < 200 && response.StatusCode != 101 {
				continue
			}
			f.engine.store.response(item.id, response)
			if response.StatusCode == 101 || item.request.Method == http.MethodConnect && response.StatusCode/100 == 2 {
				f.engine.store.addNote(item.id, "Protocol upgraded; only the HTTP handshake is recorded")
				f.complete(item.id, true)
				f.stop(nil)
				return nil
			}
			if err = f.copyBody(item.id, true, response.Body); err != nil {
				return err
			}
			if response.Body != nil && response.Body != http.NoBody && response.ContentLength < 0 && len(response.TransferEncoding) == 0 {
				return errors.New("EOF-delimited HTTPS response ended without a verifiable HTTP body length")
			}
			f.complete(item.id, true)
			break
		}
	}
	return nil
}

type plaintextBodyWriter struct {
	flow     *plaintextFlow
	id       string
	response bool
}

func (w plaintextBodyWriter) Write(p []byte) (int, error) {
	w.flow.engine.store.appendBody(w.id, w.response, p)
	return len(p), nil
}
func (f *plaintextFlow) copyBody(id string, response bool, body io.ReadCloser) error {
	if body == nil || body == http.NoBody {
		return nil
	}
	_, err := io.CopyBuffer(plaintextBodyWriter{f, id, response}, body, make([]byte, 16*1024))
	// Do not drain an unfinished body after a capture error.
	if err == nil {
		err = body.Close()
	}
	return err
}

func (p *plaintextCapture) expire(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, flow := range p.flows {
		if now.Unix()-flow.lastActivity.Load() > 120 {
			flow.stop(errors.New("eBPF connection idle for two minutes; reconnect to capture again"))
		}
	}
}
