package httpdebug

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"unicode/utf8"
)

const (
	webSocketContinuation = 0
	webSocketText         = 1
	webSocketBinary       = 2
	webSocketClose        = 8
	webSocketPing         = 9
	webSocketPong         = 10
)

type webSocketProtocolError struct {
	code   uint16
	reason string
}

func (e *webSocketProtocolError) Error() string { return "WebSocket: " + e.reason }

func webSocketError(code uint16, reason string) error {
	return &webSocketProtocolError{code: code, reason: reason}
}

// Lengths are checked before conversion or allocation. Payloads are consumed in
// chunks; a frame's declared size is never used as an allocation size.
type webSocketFrame struct {
	final      bool
	compressed bool
	opcode     int
	masked     bool
	length     int64
	mask       [4]byte
	header     []byte
}

func readWebSocketFrame(reader io.Reader, client, compression bool) (webSocketFrame, error) {
	var frame webSocketFrame
	var header [14]byte
	if _, err := io.ReadFull(reader, header[:2]); err != nil {
		return frame, err
	}
	frame.final = header[0]&0x80 != 0
	frame.compressed = header[0]&0x40 != 0
	frame.opcode = int(header[0] & 0x0f)
	frame.masked = header[1]&0x80 != 0
	if header[0]&0x30 != 0 || frame.compressed && !compression {
		return frame, webSocketError(1002, "unnegotiated reserved frame bits")
	}
	if frame.masked != client {
		return frame, webSocketError(1002, "incorrect frame masking for its direction")
	}
	switch frame.opcode {
	case webSocketContinuation, webSocketText, webSocketBinary, webSocketClose, webSocketPing, webSocketPong:
	default:
		return frame, webSocketError(1002, "unknown frame opcode")
	}
	length := uint64(header[1] & 0x7f)
	size := 2
	switch length {
	case 126:
		if _, err := io.ReadFull(reader, header[size:size+2]); err != nil {
			return frame, err
		}
		length = uint64(binary.BigEndian.Uint16(header[size : size+2]))
		size += 2
		if length < 126 {
			return frame, webSocketError(1002, "nonminimal frame length")
		}
	case 127:
		if _, err := io.ReadFull(reader, header[size:size+8]); err != nil {
			return frame, err
		}
		length = binary.BigEndian.Uint64(header[size : size+8])
		size += 8
		if length < 65536 || length > math.MaxInt64 {
			return frame, webSocketError(1002, "invalid 64-bit frame length")
		}
	}
	frame.length = int64(length)
	if frame.opcode >= webSocketClose && (!frame.final || length > 125 || frame.compressed) {
		return frame, webSocketError(1002, "invalid control frame")
	}
	if frame.opcode == webSocketContinuation && frame.compressed {
		return frame, webSocketError(1002, "compressed continuation frame")
	}
	if frame.masked {
		if _, err := io.ReadFull(reader, header[size:size+4]); err != nil {
			return frame, err
		}
		copy(frame.mask[:], header[size:size+4])
		size += 4
	}
	frame.header = append([]byte(nil), header[:size]...)
	return frame, nil
}

// wire receives the original masked bytes; payload receives decoded frame bytes.
// Both destinations are optional, and errors cannot silently lose payload bytes.
func readWebSocketPayload(ctx context.Context, reader io.Reader, frame webSocketFrame, wire, payload io.Writer) error {
	buffer := make([]byte, 32*1024)
	for offset := int64(0); offset < frame.length; {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := buffer[:min(int64(len(buffer)), frame.length-offset)]
		n, err := io.ReadFull(reader, chunk)
		if n > 0 {
			chunk = chunk[:n]
			if wire != nil {
				if err := writeWebSocketBytes(wire, chunk); err != nil {
					return err
				}
			}
			if frame.masked {
				for index := range chunk {
					chunk[index] ^= frame.mask[(offset+int64(index))&3]
				}
			}
			if payload != nil {
				if err := writeWebSocketBytes(payload, chunk); err != nil {
					return err
				}
			}
			offset += int64(n)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func writeWebSocketBytes(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if n < 0 || n > len(data) {
			return errors.New("WebSocket writer returned an invalid byte count")
		}
		data = data[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func writeWebSocketFrame(ctx context.Context, writer io.Writer, opcode int, final, compressed, masked bool, payload io.Reader, length int64) error {
	if length < 0 || opcode != webSocketContinuation && opcode != webSocketText && opcode != webSocketBinary &&
		opcode != webSocketClose && opcode != webSocketPing && opcode != webSocketPong {
		return errors.New("invalid WebSocket output frame")
	}
	if opcode >= webSocketClose && (!final || length > 125 || compressed) || opcode == webSocketContinuation && compressed {
		return errors.New("invalid WebSocket output control or continuation frame")
	}
	var header [14]byte
	header[0] = byte(opcode)
	if final {
		header[0] |= 0x80
	}
	if compressed {
		header[0] |= 0x40
	}
	size := 2
	switch {
	case length < 126:
		header[1] = byte(length)
	case length <= 65535:
		header[1] = 126
		binary.BigEndian.PutUint16(header[2:4], uint16(length))
		size += 2
	default:
		header[1] = 127
		binary.BigEndian.PutUint64(header[2:10], uint64(length))
		size += 8
	}
	var mask [4]byte
	if masked {
		header[1] |= 0x80
		if _, err := rand.Read(mask[:]); err != nil {
			return fmt.Errorf("WebSocket masking key: %w", err)
		}
		copy(header[size:size+4], mask[:])
		size += 4
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeWebSocketBytes(writer, header[:size]); err != nil {
		return err
	}
	buffer := make([]byte, 32*1024)
	for offset := int64(0); offset < length; {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := buffer[:min(int64(len(buffer)), length-offset)]
		if _, err := io.ReadFull(payload, chunk); err != nil {
			return err
		}
		if masked {
			for index := range chunk {
				chunk[index] ^= mask[(offset+int64(index))&3]
			}
		}
		if err := writeWebSocketBytes(writer, chunk); err != nil {
			return err
		}
		offset += int64(len(chunk))
	}
	return nil
}

type webSocketUTF8 struct {
	pending [utf8.UTFMax]byte
	size    int
}

func (v *webSocketUTF8) write(data []byte) error {
	if v.size > 0 {
		for len(data) > 0 && !utf8.FullRune(v.pending[:v.size]) {
			v.pending[v.size] = data[0]
			v.size++
			data = data[1:]
		}
		if utf8.FullRune(v.pending[:v.size]) {
			r, size := utf8.DecodeRune(v.pending[:v.size])
			if r == utf8.RuneError && size == 1 {
				return webSocketError(1007, "invalid UTF-8 text message")
			}
			v.size = 0
		}
	}
	for len(data) > 0 {
		if !utf8.FullRune(data) {
			v.size = copy(v.pending[:], data)
			return nil
		}
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			return webSocketError(1007, "invalid UTF-8 text message")
		}
		data = data[size:]
	}
	return nil
}

func (v *webSocketUTF8) finish() error {
	if v.size != 0 {
		return webSocketError(1007, "incomplete UTF-8 text message")
	}
	return nil
}

func validateWebSocketClose(payload []byte) error {
	if len(payload) == 1 {
		return webSocketError(1002, "one-byte close payload")
	}
	if len(payload) == 0 {
		return nil
	}
	code := binary.BigEndian.Uint16(payload)
	if code < 1000 || code >= 5000 || code >= 1016 && code < 3000 || code == 1004 || code == 1005 || code == 1006 || code == 1015 {
		return webSocketError(1002, "invalid close status")
	}
	if !utf8.Valid(payload[2:]) {
		return webSocketError(1007, "invalid UTF-8 close reason")
	}
	return nil
}
