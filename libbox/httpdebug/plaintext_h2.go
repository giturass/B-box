package httpdebug

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

type plaintextH2 struct {
	flow        *plaintextFlow
	mu          sync.Mutex
	streams     map[uint32]*plaintextH2Stream
	lastRequest uint32
	requestDone chan struct{}
}

type plaintextH2Stream struct {
	id           string
	requestReady chan struct{}
	method       string
	closed       bool
	headers      [2]bool
	ended        [2]bool
	length       [2]int64
	bytes        [2]int64
}

func (f *plaintextFlow) http2(request *bufio.Reader) error {
	preface := make([]byte, len(http2.ClientPreface))
	if _, err := io.ReadFull(request, preface); err != nil {
		return err
	}
	if string(preface) != http2.ClientPreface {
		return errors.New("HTTP/2 preface missing; reconnect after enabling capture")
	}
	h := &plaintextH2{flow: f, streams: make(map[uint32]*plaintextH2Stream), requestDone: make(chan struct{})}
	responseDone := make(chan error, 1)
	go func() {
		err := h.read(f.response, true)
		if err != nil {
			f.stop(err)
		}
		responseDone <- err
	}()
	err := h.read(request, false)
	close(h.requestDone)
	if err != nil {
		f.stop(err)
	}
	responseErr := <-responseDone
	if err != nil {
		return err
	}
	return responseErr
}

func (h *plaintextH2) stream(id uint32, response bool) (*plaintextH2Stream, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if id == 0 || id%2 == 0 {
		return nil, errors.New("HTTP/2 server push is not supported by capture")
	}
	if stream := h.streams[id]; stream != nil {
		return stream, nil
	}
	if id <= h.lastRequest {
		// Closed streams need no tombstones: the client opens streams in order.
		// Their HEADERS have already passed through the shared HPACK decoder.
		return nil, nil
	}
	if len(h.streams) >= 32 {
		return nil, errors.New("HTTP/2 capture exceeds 32 concurrent streams")
	}
	stream := &plaintextH2Stream{requestReady: make(chan struct{}), length: [2]int64{-1, -1}}
	h.streams[id] = stream
	return stream, nil
}

func (h *plaintextH2) read(input io.Reader, response bool) error {
	framer := http2.NewFramer(nil, input)
	framer.SetMaxReadFrameSize(256 * 1024)
	framer.MaxHeaderListSize = MaxHeaderBytes
	framer.ReadMetaHeaders = hpack.NewDecoder(4096, nil)
	framer.ReadMetaHeaders.SetAllowedMaxDynamicTableSize(MaxHeaderBytes)
	for {
		frame, err := framer.ReadFrame()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("captured HTTP/2 framing: %w", err)
		}
		switch frame := frame.(type) {
		case *http2.PushPromiseFrame:
			return errors.New("HTTP/2 server push is not supported by capture")
		case *http2.MetaHeadersFrame:
			if frame.Truncated {
				return errors.New("captured HTTP/2 headers exceed 64 KiB")
			}
			stream, err := h.stream(frame.StreamID, response)
			if err != nil {
				return err
			}
			if stream == nil {
				continue
			}
			if response {
				if err := h.waitRequest(stream); err != nil {
					return err
				}
			}
			if err = h.headers(stream, frame, response); err != nil {
				return err
			}
		case *http2.DataFrame:
			h.mu.Lock()
			stream := h.streams[frame.StreamID]
			if stream == nil && frame.StreamID != 0 && frame.StreamID%2 == 1 && frame.StreamID <= h.lastRequest {
				h.mu.Unlock()
				continue // In-flight data may arrive after the other side reset.
			}
			index := 0
			if response {
				index = 1
			}
			if stream == nil || !stream.headers[index] || stream.ended[index] {
				h.mu.Unlock()
				return errors.New("HTTP/2 body has no open captured headers")
			}
			h.flow.engine.store.appendBody(stream.id, response, frame.Data())
			stream.bytes[index] += int64(len(frame.Data()))
			if frame.StreamEnded() {
				err = h.endLocked(stream, frame.StreamID, response)
			}
			h.mu.Unlock()
			if err != nil {
				return err
			}
		case *http2.RSTStreamFrame:
			if err := h.reset(frame, response); err != nil {
				return err
			}
		case *http2.GoAwayFrame:
			if frame.ErrCode != http2.ErrCodeNo {
				return fmt.Errorf("HTTP/2 GOAWAY: %s", frame.ErrCode)
			}
		}
	}
}

func (h *plaintextH2) waitRequest(stream *plaintextH2Stream) error {
	select {
	case <-stream.requestReady:
		return nil
	case <-h.requestDone:
		select {
		case <-stream.requestReady:
			return nil
		default:
			return errors.New("HTTP/2 response has no captured request")
		}
	}
}

func (h *plaintextH2) reset(frame *http2.RSTStreamFrame, response bool) error {
	stream, err := h.stream(frame.StreamID, response)
	if err != nil || stream == nil {
		return err
	}
	if response {
		// Perf and the two parsers can deliver the server reset before the
		// client's HEADERS. Retain that request and mark only it incomplete.
		if err := h.waitRequest(stream); err != nil {
			return err
		}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if stream.closed {
		return nil
	}
	if !stream.headers[0] {
		return errors.New("HTTP/2 reset of an idle stream")
	}
	stream.closed = true
	delete(h.streams, frame.StreamID)
	reason := fmt.Errorf("HTTP/2 stream %d reset (%s)", frame.StreamID, frame.ErrCode)
	h.flow.cancel(stream.id, reason)
	AppendCaptureLog("debug", fmt.Sprintf("TLS connection %s: %s; other streams remain active", h.flow.id, reason))
	return nil
}

func (h *plaintextH2) headers(stream *plaintextH2Stream, frame *http2.MetaHeadersFrame, response bool) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if stream.closed {
		return nil
	}
	index := 0
	if response {
		index = 1
	}
	if stream.ended[index] {
		return errors.New("HTTP/2 headers after END_STREAM")
	}
	if stream.headers[index] {
		if len(frame.PseudoFields()) > 0 || !frame.StreamEnded() {
			return errors.New("invalid captured HTTP/2 trailers")
		}
		h.flow.engine.store.addNote(stream.id, "HTTP/2 trailers received; trailers are not included in the header preview")
		return h.endLocked(stream, frame.StreamID, response)
	}
	headers := make(http.Header)
	for _, field := range frame.RegularFields() {
		headers.Add(field.Name, field.Value)
	}
	length, err := capturedContentLength(headers)
	if err != nil {
		return err
	}
	stream.length[index] = length
	var body io.ReadCloser = http.NoBody
	if !frame.StreamEnded() {
		body = io.NopCloser(strings.NewReader(""))
	}
	if response {
		status, err := strconv.Atoi(frame.PseudoValue("status"))
		if err != nil || status < 100 || status > 999 {
			return errors.New("invalid captured HTTP/2 status")
		}
		if status < 200 {
			if frame.StreamEnded() {
				return errors.New("HTTP/2 informational response ended the stream")
			}
			return nil
		}
		if stream.method == http.MethodHead || status == 204 || status == 304 {
			stream.length[1] = 0
		}
		h.flow.engine.store.response(stream.id, &http.Response{StatusCode: status, Proto: "HTTP/2.0",
			ProtoMajor: 2, Header: headers, Body: body, ContentLength: -1})
	} else {
		method, authority, path := frame.PseudoValue("method"), frame.PseudoValue("authority"), frame.PseudoValue("path")
		if method == "" || authority == "" {
			return errors.New("captured HTTP/2 method or authority missing")
		}
		if frame.StreamID <= h.lastRequest {
			return errors.New("HTTP/2 request stream is out of order")
		}
		h.lastRequest = frame.StreamID
		if method == http.MethodConnect {
			path = "/"
		} else if frame.PseudoValue("scheme") != "https" || path == "" {
			return errors.New("invalid captured HTTP/2 scheme or path")
		}
		u, err := url.ParseRequestURI(path)
		if err != nil || u.IsAbs() {
			return errors.New("invalid captured HTTP/2 request target")
		}
		u.Scheme, u.Host = "https", authority
		request := &http.Request{Method: method, URL: u, Host: authority, Proto: "HTTP/2.0",
			ProtoMajor: 2, Header: headers, Body: body, ContentLength: length}
		if err := h.flow.requestURL(request); err != nil {
			return err
		}
		stream.id, stream.method = h.flow.begin(request), method
		close(stream.requestReady)
	}
	stream.headers[index] = true
	if frame.StreamEnded() {
		return h.endLocked(stream, frame.StreamID, response)
	}
	return nil
}

func (h *plaintextH2) endLocked(stream *plaintextH2Stream, streamID uint32, response bool) error {
	index := 0
	if response {
		index = 1
	}
	if stream.length[index] >= 0 && stream.bytes[index] != stream.length[index] {
		return errors.New("captured HTTP/2 body does not match Content-Length")
	}
	stream.ended[index] = true
	h.flow.complete(stream.id, response)
	if stream.ended[0] && stream.ended[1] {
		stream.closed = true
		delete(h.streams, streamID)
	}
	return nil
}

func capturedContentLength(header http.Header) (int64, error) {
	values := header.Values("Content-Length")
	if len(values) == 0 {
		return -1, nil
	}
	value, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("invalid captured Content-Length")
	}
	for _, other := range values[1:] {
		if other != values[0] {
			return 0, errors.New("conflicting captured Content-Length headers")
		}
	}
	return value, nil
}
