// Schema-free gRPC display adapted from FlClash (GPL-3.0), fixed reference
// 5e579c4987e50e5c68e6023cc31ff4db55d480bb: lib/common/mitm_protobuf.dart.
// B-box streams HTTP/message decoding and complete file-backed body views.
package httpdebug

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// WriteBodyView makes a complete, derived view without transferring a complete
// body across JNI or retaining it in memory. The caller owns the output file.
// The original captured files, limits and export representation remain intact.
func (s *Store) WriteBodyView(ctx context.Context, out io.Writer, id string, response bool, format string) (Message, error) {
	switch format {
	case "image", "json", "protobuf", "grpc":
	default:
		return Message{}, errors.New("invalid body view format")
	}
	snapshot, err := s.snapshot(id)
	if err != nil {
		return Message{}, err
	}
	defer snapshot.close()
	message := snapshot.message(response)
	if !message.BodyComplete || message.BodyTruncated {
		return Message{}, errors.New("a complete, untruncated saved body is required")
	}
	encodings, err := contentEncodings(messageContentEncoding(message))
	if err != nil {
		return Message{}, err
	}
	decoder, err := newBodyContentReader(&bodyReadContext{ctx: ctx}, snapshot.reader(response), encodings)
	if err != nil {
		return Message{}, err
	}
	defer decoder.Close()
	writer := &viewContextWriter{ctx, out}
	if format == "image" {
		_, err = io.Copy(writer, &viewContextReader{ctx, decoder})
		if err == nil {
			err = s.CheckBodyView(ctx, id)
		}
		return message, err
	}
	// Random access supports frame boundaries and nested protobuf inference.
	// It also verifies the HTTP compression trailer before publishing a view.
	source, err := os.CreateTemp(s.directory, "body-view-source-*.tmp")
	if err != nil {
		return Message{}, err
	}
	defer func() { source.Close(); os.Remove(source.Name()) }()
	length, err := io.Copy(source, &viewContextReader{ctx, decoder})
	if err != nil {
		return Message{}, err
	}
	buffer := bufio.NewWriter(writer)
	if format == "json" {
		prefix := make([]byte, min(length, int64(1024)))
		if _, err = source.ReadAt(prefix, 0); err != nil && err != io.EOF {
			return Message{}, err
		}
		charset := detectBodyCharset(message, prefix)
		err = writeBodyJSON(ctx, buffer, newBodyTextReader(ctx, charset, io.NewSectionReader(source, 0, length)))
	} else if format == "protobuf" {
		err = writeBodyProtobuf(ctx, buffer, source, 0, length, 0)
	} else {
		err = writeBodyGRPC(ctx, buffer, source, length, message, s.directory)
	}
	if err == nil {
		err = buffer.Flush()
	}
	if err == nil {
		err = s.CheckBodyView(ctx, id)
	}
	return message, err
}

// CheckBodyView also protects readers owned by another Activity when the
// shared store is explicitly cleared. It never rereads or decodes the body.
func (s *Store) CheckBodyView(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	retained := s.records[id] != nil
	s.mu.Unlock()
	if !retained {
		return os.ErrNotExist
	}
	return nil
}

// ReadBodyViewPage uses UTF-8 byte offsets in the derived text file. Those
// offsets are deliberately separate from the original body's byte offsets.
func ReadBodyViewPage(ctx context.Context, path string, offset int64) (Preview, error) {
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return Preview{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Preview{}, err
	}
	message := Message{BodyBytes: info.Size(), CapturedBytes: info.Size(), BodyComplete: true}
	page, err := readBodyPageContext(ctx, message, func() io.Reader { return &viewContextSeeker{ctx, io.NewSectionReader(file, 0, info.Size())} }, offset, false)
	page.Source = "view"
	page.Message = nil
	return page, err
}

type viewContextReader struct {
	ctx context.Context
	io.Reader
}

func (r *viewContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.Reader.Read(p)
	if cancelled := r.ctx.Err(); cancelled != nil {
		return n, cancelled
	}
	return n, err
}

type viewContextWriter struct {
	ctx context.Context
	io.Writer
}

type viewContextSeeker struct {
	ctx context.Context
	*io.SectionReader
}

func (r *viewContextSeeker) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.SectionReader.Read(p)
}

func (r *viewContextSeeker) Seek(offset int64, whence int) (int64, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.SectionReader.Seek(offset, whence)
}

func (w *viewContextWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := w.Writer.Write(p)
	if cancelled := w.ctx.Err(); cancelled != nil {
		return n, cancelled
	}
	return n, err
}

func viewHeader(message Message, name string) string {
	var values []string
	for _, header := range message.Headers {
		if strings.EqualFold(header.Name, name) {
			values = append(values, header.Value)
		}
	}
	return strings.Join(values, ",")
}

func writeBodyGRPC(ctx context.Context, out io.Writer, source *os.File, length int64, message Message, directory string) error {
	mime := strings.ToLower(strings.TrimSpace(strings.SplitN(viewHeader(message, "content-type"), ";", 2)[0]))
	if !strings.HasPrefix(mime, "application/grpc") {
		mime = "application/grpc" // an explicitly selected schema-free frame view
	}
	web := strings.HasPrefix(mime, "application/grpc-web")
	if strings.HasPrefix(mime, "application/grpc-web-text") {
		decoded, err := os.CreateTemp(directory, "body-view-grpc-web-*.tmp")
		if err != nil {
			return err
		}
		defer func() { decoded.Close(); os.Remove(decoded.Name()) }()
		length, err = decodeBodyGRPCWeb(ctx, decoded, io.NewSectionReader(source, 0, length))
		if err != nil {
			return err
		}
		source = decoded
	}
	index := 0
	for offset := int64(0); offset < length; {
		if err := ctx.Err(); err != nil {
			return err
		}
		var header [5]byte
		if length-offset < 5 {
			return errors.New("incomplete gRPC frame header")
		}
		if _, err := source.ReadAt(header[:], offset); err != nil {
			return err
		}
		flag := header[0]
		if flag != 0 && flag != 1 && !(web && flag == 0x80) {
			return errors.New("invalid gRPC frame flag")
		}
		size := int64(binary.BigEndian.Uint32(header[1:]))
		start := offset + 5
		if size > length-start {
			return errors.New("gRPC message length does not match the saved body")
		}
		index++
		note := ""
		payload, payloadStart, payloadSize := source, start, size
		var decoded *os.File
		if flag == 0x80 {
			note = " [trailers]"
		} else if flag == 1 {
			coding := strings.ToLower(strings.TrimSpace(viewHeader(message, "grpc-encoding")))
			note = " [compressed: " + strconv.Quote(coding) + "]"
			if coding == "gzip" || coding == "deflate" {
				var err error
				decoded, err = os.CreateTemp(directory, "body-view-grpc-message-*.tmp")
				if err != nil {
					return err
				}
				decoder, decodeErr := newContentReader(&viewContextReader{ctx, io.NewSectionReader(source, start, size)}, []string{coding})
				if decodeErr == nil {
					payloadSize, decodeErr = io.Copy(decoded, &viewContextReader{ctx, decoder})
					decoder.Close()
				}
				if decodeErr == nil {
					payload, payloadStart = decoded, 0
				} else {
					decoded.Close()
					os.Remove(decoded.Name())
					decoded = nil
					payloadSize = size
					if ctx.Err() != nil {
						return ctx.Err()
					}
					note += " [message decoding failed]"
				}
			}
		}
		// Every frame is released before visiting the next one, including errors.
		err := func() error {
			if decoded != nil {
				defer func() { decoded.Close(); os.Remove(decoded.Name()) }()
			}
			if _, err := fmt.Fprintf(out, "# %d%s\n", index, note); err != nil {
				return err
			}
			if flag == 0x80 {
				if viewPrintable(ctx, payload, payloadStart, payloadSize, true) {
					if _, err := io.Copy(out, &viewContextReader{ctx, io.NewSectionReader(payload, payloadStart, payloadSize)}); err != nil {
						return err
					}
				} else if err := writeViewHex(out, payload, payloadStart, payloadSize); err != nil {
					return err
				}
			} else if strings.HasSuffix(mime, "+json") && (flag == 0 || decoded != nil) {
				// Validate before writing so a malformed JSON message can still be
				// represented by its saved bytes instead of a partial derived value.
				if err := writeBodyJSON(ctx, io.Discard, io.NewSectionReader(payload, payloadStart, payloadSize)); err == nil {
					if err = writeBodyJSON(ctx, out, io.NewSectionReader(payload, payloadStart, payloadSize)); err != nil {
						return err
					}
				} else if err = writeViewHex(out, payload, payloadStart, payloadSize); err != nil {
					return err
				}
			} else if (flag == 0 || decoded != nil) && validViewProtobuf(ctx, payload, payloadStart, payloadSize, 0) {
				if err := writeBodyProtobuf(ctx, out, payload, payloadStart, payloadSize, 0); err != nil {
					return err
				}
			} else if err := writeViewHex(out, payload, payloadStart, payloadSize); err != nil {
				return err
			}
			_, err := io.WriteString(out, "\n")
			return err
		}()
		if err != nil {
			return err
		}
		offset = start + size
	}
	if index == 0 {
		return errors.New("no gRPC frames in the saved body")
	}
	if len(message.Trailers) > 0 {
		if _, err := io.WriteString(out, "# HTTP trailers\n"); err != nil {
			return err
		}
		for _, header := range message.Trailers {
			if _, err := fmt.Fprintf(out, "%s: %s\n", header.Name, header.Value); err != nil {
				return err
			}
		}
	}
	return ctx.Err()
}

// gRPC-Web accepts concatenated independently padded Base64 blocks. Decode
// quartets so no complete text or message allocation is needed.
func decodeBodyGRPCWeb(ctx context.Context, out io.Writer, source io.Reader) (int64, error) {
	reader := bufio.NewReader(&viewContextReader{ctx, source})
	var block [4]byte
	var decoded [3]byte
	var total int64
	count := 0
	for {
		value, err := reader.ReadByte()
		if err == io.EOF && count == 0 {
			return total, nil
		}
		if err != nil {
			if err == io.EOF {
				err = errors.New("incomplete grpc-web-text Base64 quartet")
			}
			return total, err
		}
		if value == ' ' || value == '\r' || value == '\n' || value == '\t' {
			continue
		}
		block[count] = value
		count++
		if count != 4 {
			continue
		}
		n, err := base64.StdEncoding.Strict().Decode(decoded[:], block[:])
		if err != nil {
			return total, fmt.Errorf("invalid grpc-web-text Base64: %w", err)
		}
		written, err := out.Write(decoded[:n])
		total += int64(written)
		if err != nil {
			return total, err
		}
		if written != n {
			return total, io.ErrShortWrite
		}
		count = 0
	}
}
