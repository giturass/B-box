package httpdebug

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"slices"
	"sync"
	"unicode/utf8"
)

// BodyReader belongs to one body-inspection task. Pages keep their offsets in
// HTTP-decoded bytes (or saved bytes for raw reads), while compressed prefixes
// are streamed once into a private, seekable file. It never retains a whole
// body in memory. Callers cancel the active context before waiting in Close.
type BodyReader struct {
	gate              chan struct{}
	done              chan struct{}
	closeOnce         sync.Once
	store             *Store
	id                string
	response          bool
	bound             bool
	raw               bool
	fallback          string
	fallbackTruncated bool
	state             *bodyReaderState
}

type bodyReaderState struct {
	snapshot *bodySnapshot
	message  Message
	fileInfo os.FileInfo
	context  bodyReadContext
	decoder  io.ReadCloser
	decoded  *os.File
	size     int64
	terminal error
	charset  bodyCharset
	sniffed  bool
}

func NewBodyReader() *BodyReader {
	reader := &BodyReader{gate: make(chan struct{}, 1), done: make(chan struct{})}
	reader.gate <- struct{}{}
	return reader
}

func (reader *BodyReader) Read(ctx context.Context, store *Store, id string, response bool, offset int64, raw bool) (Preview, error) {
	if offset < 0 || offset > math.MaxInt64-BodyPageSize-utf8.UTFMax {
		return Preview{}, errors.New("invalid body page offset")
	}
	if store == nil {
		return Preview{}, errors.New("missing capture store")
	}
	select {
	case <-ctx.Done():
		return Preview{}, ctx.Err()
	case <-reader.done:
		return Preview{}, os.ErrClosed
	case <-reader.gate:
	}
	defer func() { reader.gate <- struct{}{} }()
	select {
	case <-reader.done:
		return Preview{}, os.ErrClosed
	default:
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	if reader.bound && (reader.store != store || reader.id != id || reader.response != response || raw != reader.raw && reader.fallback == "") {
		return Preview{}, errors.New("body reader is already bound to a different body or source")
	}
	// Never serve a disk cache without first confirming the retained record.
	// Snapshot opens/pins files under the index lock and performs I/O outside it.
	snapshot, err := store.snapshot(id)
	if err != nil {
		reader.reset()
		return Preview{}, err
	}
	if ctx.Err() != nil {
		snapshot.close()
		return Preview{}, ctx.Err()
	}
	if !reader.bound {
		reader.bound, reader.store, reader.id, reader.response, reader.raw = true, store, id, response, raw
	}
	message := snapshot.message(response)
	same, err := reader.sameSource(snapshot, message)
	if err != nil {
		snapshot.close()
		reader.reset()
		return Preview{}, err
	}
	if same {
		snapshot.close()
		reader.state.message = message
		reader.state.context.ctx = ctx
	} else {
		reader.reset()
		reader.state = &bodyReaderState{snapshot: snapshot, message: message, context: bodyReadContext{ctx: ctx}}
		file := snapshot.files[bodyDirection(response)]
		if file != nil {
			reader.state.fileInfo, err = file.Stat()
			if err != nil {
				reader.reset()
				return Preview{}, err
			}
		}
		if err = reader.openDecoder(offset); err != nil {
			reader.reset()
			return Preview{}, err
		}
	}
	page, err := reader.readPage(ctx, offset)
	if ctx.Err() != nil {
		reader.reset()
		return Preview{}, ctx.Err()
	}
	if err != nil {
		reader.reset()
		return Preview{}, err
	}
	// An in-flight read may finish from a pinned file after Clear, but the task
	// must not publish it as if the removed record were still retained.
	store.mu.Lock()
	retained := store.records[id] != nil
	store.mu.Unlock()
	if !retained {
		reader.reset()
		return Preview{}, os.ErrNotExist
	}
	return page, nil
}

func bodyDirection(response bool) int {
	if response {
		return 1
	}
	return 0
}

func (reader *BodyReader) sameSource(snapshot *bodySnapshot, message Message) (bool, error) {
	state := reader.state
	if state == nil || state.message.CapturedBytes != message.CapturedBytes ||
		messageContentEncoding(state.message) != messageContentEncoding(message) ||
		headerValue(state.message, "Content-Type") != headerValue(message, "Content-Type") {
		return false, nil
	}
	file := snapshot.files[bodyDirection(reader.response)]
	if file == nil {
		return state.fileInfo == nil, nil
	}
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	return state.fileInfo != nil && os.SameFile(state.fileInfo, info), nil
}

func (reader *BodyReader) openDecoder(offset int64) error {
	state := reader.state
	if reader.raw || state.message.CapturedBytes == 0 {
		return nil
	}
	encodings, err := contentEncodings(messageContentEncoding(state.message))
	if err == nil && !slices.ContainsFunc(encodings, func(encoding string) bool { return encoding != "identity" }) {
		return nil
	}
	if err == nil {
		state.decoder, err = newBodyContentReader(&state.context, state.snapshot.reader(reader.response), encodings)
	}
	if err != nil {
		if state.context.ctx.Err() != nil {
			return state.context.ctx.Err()
		}
		if offset != 0 {
			return fmt.Errorf("body decoding unavailable: %w", err)
		}
		reader.raw, reader.fallback = true, err.Error()
		return nil
	}
	state.decoded, err = os.CreateTemp(reader.store.directory, "body-preview-*.tmp")
	return err
}

func (reader *BodyReader) readPage(ctx context.Context, offset int64) (Preview, error) {
	state := reader.state
	var source io.Reader
	var readErr error
	if state.decoder != nil {
		if err := state.ensureDecoded(ctx, offset+BodyPageSize+utf8.UTFMax); err != nil {
			return Preview{}, err
		}
		if state.size == 0 && state.terminal != nil && !errors.Is(state.terminal, io.EOF) && offset == 0 {
			// No plaintext is available. Preserve exactly the saved bytes and
			// keep the task on that source for all later offsets.
			decodeErr := state.terminal
			state.closeDecoded()
			reader.raw, reader.fallback = true, decodeErr.Error()
			reader.fallbackTruncated = true
			page, err := reader.readPage(ctx, 0)
			page.Truncated = true
			return page, err
		}
		if offset > state.size {
			return Preview{}, errors.New("body page is past the readable content")
		}
		source = io.NewSectionReader(state.decoded, 0, state.size)
		if state.terminal != nil && !errors.Is(state.terminal, io.EOF) && offset+BodyPageSize+utf8.UTFMax >= state.size {
			readErr = state.terminal
		}
	} else {
		if offset > state.message.CapturedBytes {
			return Preview{}, errors.New("body page is past the saved content")
		}
		source = state.snapshot.reader(reader.response)
	}
	if !reader.raw && !state.sniffed {
		prefix, err := readBodyBytes(ctx, source, bodyCharsetPrefixSize)
		if err != nil {
			return Preview{}, err
		}
		state.charset, state.sniffed = detectBodyCharset(state.message, prefix), true
	}
	if seeker, ok := source.(io.Seeker); ok {
		if _, err := seeker.Seek(offset, io.SeekStart); err != nil {
			return Preview{}, err
		}
	} else if err := skipBodyBytes(ctx, source, offset); err != nil {
		return Preview{}, err
	}
	data, err := readBodyBytes(ctx, source, BodyPageSize+utf8.UTFMax)
	if err != nil {
		return Preview{}, err
	}
	page, err := previewBodyBytes(ctx, state.message, data, offset, reader.raw, state.charset, readErr)
	if reader.fallback != "" {
		page.Error = reader.fallback
		page.Truncated = page.Truncated || reader.fallbackTruncated
	}
	return page, err
}

func (state *bodyReaderState) ensureDecoded(ctx context.Context, target int64) error {
	buffer := make([]byte, 32*1024)
	noProgress := 0
	for state.size < target && state.terminal == nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		data := buffer[:min(int64(len(buffer)), target-state.size)]
		n, err := state.decoder.Read(data)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if n > 0 {
			if writeErr := writeBodyBytes(ctx, state.decoded, data[:n]); writeErr != nil {
				return writeErr
			}
			state.size += int64(n)
			noProgress = 0
		} else if err == nil {
			noProgress++
			if noProgress >= 100 {
				err = io.ErrNoProgress
			}
		}
		if err != nil {
			state.terminal = err
		}
	}
	return ctx.Err()
}

func (state *bodyReaderState) closeDecoded() {
	if state.decoder != nil {
		_ = state.decoder.Close()
		state.decoder = nil
	}
	if state.decoded != nil {
		name := state.decoded.Name()
		_ = state.decoded.Close()
		_ = os.Remove(name)
		state.decoded = nil
	}
	state.size, state.terminal, state.sniffed = 0, nil, false
}

func (reader *BodyReader) reset() {
	if reader.state != nil {
		reader.state.closeDecoded()
		reader.state.snapshot.close()
		reader.state = nil
	}
}

func (reader *BodyReader) Close() {
	reader.closeOnce.Do(func() {
		close(reader.done)
		<-reader.gate
		reader.reset()
		reader.gate <- struct{}{}
	})
}

// The context pointer can be replaced between serialized pages. A canceled
// read invalidates its decoder before another context can use that state.
type bodyReadContext struct{ ctx context.Context }

type bodyContextReader struct {
	state  *bodyReadContext
	reader io.Reader
}

func (reader *bodyContextReader) Read(data []byte) (int, error) {
	if err := reader.state.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := reader.reader.Read(data)
	if reader.state.ctx.Err() != nil {
		return n, reader.state.ctx.Err()
	}
	return n, err
}

// Wrap every layer, including its constructor's header reads, so stacked
// compression cannot bypass cancellation while producing intermediate bytes.
func newBodyContentReader(state *bodyReadContext, raw io.Reader, encodings []string) (io.ReadCloser, error) {
	chain := &contentReader{Reader: &bodyContextReader{state: state, reader: raw}}
	for index := len(encodings) - 1; index >= 0; index-- {
		if err := state.ctx.Err(); err != nil {
			_ = chain.Close()
			return nil, err
		}
		if encodings[index] == "identity" {
			continue
		}
		layer, err := newContentReader(chain.Reader, encodings[index:index+1])
		if err != nil {
			_ = chain.Close()
			return nil, err
		}
		chain.Reader = &bodyContextReader{state: state, reader: layer}
		chain.closers = append(chain.closers, func() { _ = layer.Close() })
		if err := state.ctx.Err(); err != nil {
			_ = chain.Close()
			return nil, err
		}
	}
	return chain, nil
}

// Bounded body and page buffers use explicit loops; io.Copy's optional fast
// paths must not bypass cancellation. A negative limit is only for already
// bounded page text conversion, never a full captured body.
func readBodyBytes(ctx context.Context, reader io.Reader, limit int) ([]byte, error) {
	var output []byte
	buffer := make([]byte, 4096)
	if limit >= 0 {
		output = make([]byte, 0, min(limit, len(buffer)))
	}
	noProgress := 0
	for limit < 0 || len(output) < limit {
		if err := ctx.Err(); err != nil {
			return output, err
		}
		data := buffer
		if limit >= 0 {
			data = data[:min(len(data), limit-len(output))]
		}
		n, err := reader.Read(data)
		output = append(output, data[:n]...)
		if ctx.Err() != nil {
			return output, ctx.Err()
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = nil
			}
			return output, err
		}
		if n == 0 {
			noProgress++
			if noProgress >= 100 {
				return output, io.ErrNoProgress
			}
		} else {
			noProgress = 0
		}
	}
	return output, nil
}

func skipBodyBytes(ctx context.Context, reader io.Reader, count int64) error {
	buffer := make([]byte, 32*1024)
	noProgress := 0
	for count > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := reader.Read(buffer[:min(int64(len(buffer)), count)])
		count -= int64(n)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if count == 0 {
			return nil
		}
		if err != nil {
			return err
		}
		if n == 0 {
			noProgress++
			if noProgress >= 100 {
				return io.ErrNoProgress
			}
		} else {
			noProgress = 0
		}
	}
	return nil
}

func writeBodyBytes(ctx context.Context, writer io.Writer, data []byte) error {
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := writer.Write(data)
		data = data[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return ctx.Err()
}
