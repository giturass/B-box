// Schema-free field display adapted from FlClash (GPL-3.0), fixed reference
// 5e579c4987e50e5c68e6023cc31ff4db55d480bb: lib/common/mitm_protobuf.dart.
// B-box parses complete decoded files with ReaderAt and streams the derived view.
package httpdebug

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"
)

// This bounds schema-free nesting guesses, not the number of captured fields
// or the length of a complete view. Opaque fields retain a byte summary.
const bodyViewProtoDepth = 16

type viewProtoCursor struct {
	ctx         context.Context
	source      io.ReaderAt
	offset, end int64
}

type viewProtoField struct {
	number, value uint64
	wire          byte
	start, length int64
}

func (c *viewProtoCursor) varint() (uint64, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	var data [10]byte
	count := min(int64(len(data)), c.end-c.offset)
	if count <= 0 {
		return 0, io.ErrUnexpectedEOF
	}
	n, err := c.source.ReadAt(data[:count], c.offset)
	if err != nil && err != io.EOF {
		return 0, err
	}
	var value uint64
	for i, b := range data[:n] {
		if i == 9 && b > 1 {
			return 0, errors.New("protobuf varint overflow")
		}
		value |= uint64(b&0x7f) << (7 * i)
		if b < 0x80 {
			c.offset += int64(i + 1)
			return value, nil
		}
	}
	return 0, errors.New("incomplete protobuf varint")
}

func (c *viewProtoCursor) field() (viewProtoField, error) {
	tag, err := c.varint()
	if err != nil {
		return viewProtoField{}, err
	}
	field := viewProtoField{number: tag >> 3, wire: byte(tag & 7)}
	if field.number == 0 || field.number > (1<<29)-1 {
		return field, errors.New("invalid protobuf field number")
	}
	switch field.wire {
	case 0:
		field.value, err = c.varint()
	case 1, 5:
		field.length = 8
		if field.wire == 5 {
			field.length = 4
		}
		if field.length > c.end-c.offset {
			return field, io.ErrUnexpectedEOF
		}
		var data [8]byte
		if _, err = c.source.ReadAt(data[:field.length], c.offset); err != nil {
			return field, err
		}
		if field.wire == 1 {
			field.value = binary.LittleEndian.Uint64(data[:])
		} else {
			field.value = uint64(binary.LittleEndian.Uint32(data[:4]))
		}
		c.offset += field.length
	case 2:
		var length uint64
		length, err = c.varint()
		if err != nil {
			return field, err
		}
		if length > uint64(c.end-c.offset) {
			return field, io.ErrUnexpectedEOF
		}
		field.start, field.length = c.offset, int64(length)
		c.offset += field.length
	default:
		return field, errors.New("unsupported protobuf wire type")
	}
	return field, err
}

func validViewProtobuf(ctx context.Context, source io.ReaderAt, start, length int64, depth int) bool {
	if length == 0 {
		return depth == 0 // an empty gRPC message is a valid default message
	}
	cursor := viewProtoCursor{ctx, source, start, start + length}
	for cursor.offset < cursor.end {
		if _, err := cursor.field(); err != nil {
			return false
		}
	}
	return ctx.Err() == nil
}

func writeBodyProtobuf(ctx context.Context, out io.Writer, source io.ReaderAt, start, length int64, depth int) error {
	if !validViewProtobuf(ctx, source, start, length, depth) {
		if err := ctx.Err(); err != nil {
			return err
		}
		return errors.New("invalid protobuf structure")
	}
	return writeViewProtoFields(ctx, out, source, start, length, depth)
}

func writeViewProtoFields(ctx context.Context, out io.Writer, source io.ReaderAt, start, length int64, depth int) error {
	cursor := viewProtoCursor{ctx, source, start, start + length}
	for cursor.offset < cursor.end {
		field, err := cursor.field()
		if err != nil {
			return err
		}
		if err = writeViewIndent(out, depth); err != nil {
			return err
		}
		if _, err = io.WriteString(out, strconv.FormatUint(field.number, 10)); err != nil {
			return err
		}
		if field.wire == 2 {
			if viewPrintable(ctx, source, field.start, field.length, true) {
				if _, err = io.WriteString(out, ": "); err != nil {
					return err
				}
				if err = writeViewQuoted(ctx, out, source, field.start, field.length); err != nil {
					return err
				}
			} else if depth < bodyViewProtoDepth && validViewProtobuf(ctx, source, field.start, field.length, depth+1) {
				if _, err = io.WriteString(out, " {\n"); err != nil {
					return err
				}
				if err = writeViewProtoFields(ctx, out, source, field.start, field.length, depth+1); err != nil {
					return err
				}
				if err = writeViewIndent(out, depth); err != nil {
					return err
				}
				if _, err = io.WriteString(out, "}"); err != nil {
					return err
				}
			} else {
				if _, err = io.WriteString(out, ": "); err != nil {
					return err
				}
				if err = writeViewHex(out, source, field.start, field.length); err != nil {
					return err
				}
			}
		} else {
			value := strconv.FormatUint(field.value, 10)
			if field.wire == 1 || field.wire == 5 {
				value = fmt.Sprintf("0x%0*x", field.length*2, field.value)
			}
			if _, err = io.WriteString(out, ": "+value); err != nil {
				return err
			}
		}
		if _, err = io.WriteString(out, "\n"); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func viewPrintable(ctx context.Context, source io.ReaderAt, start, length int64, lines bool) bool {
	reader := bufio.NewReader(&viewContextReader{ctx, io.NewSectionReader(source, start, length)})
	for {
		value, size, err := reader.ReadRune()
		if err == io.EOF {
			return ctx.Err() == nil
		}
		if err != nil || value == utf8.RuneError && size == 1 ||
			(value < 0x20 && !(lines && (value == '\n' || value == '\r' || value == '\t'))) || value >= 0x7f && value <= 0x9f {
			return false
		}
	}
}

func writeViewQuoted(ctx context.Context, out io.Writer, source io.ReaderAt, start, length int64) error {
	if _, err := io.WriteString(out, "\""); err != nil {
		return err
	}
	reader := bufio.NewReader(&viewContextReader{ctx, io.NewSectionReader(source, start, length)})
	for {
		value, size, err := reader.ReadRune()
		if err == io.EOF {
			_, err = io.WriteString(out, "\"")
			return err
		}
		if err != nil {
			return err
		}
		if value == utf8.RuneError && size == 1 {
			return errors.New("invalid protobuf text field")
		}
		if value == '\n' || value == '\r' || value == '\t' {
			escape := "\\n"
			if value == '\r' {
				escape = "\\r"
			} else if value == '\t' {
				escape = "\\t"
			}
			if _, err = io.WriteString(out, escape); err != nil {
				return err
			}
			continue
		}
		if value < 0x20 {
			return errors.New("invalid protobuf text field")
		}
		if value == '"' || value == '\\' {
			if _, err = io.WriteString(out, "\\"); err != nil {
				return err
			}
		}
		if _, err = io.WriteString(out, string(value)); err != nil {
			return err
		}
	}
}

func writeViewHex(out io.Writer, source io.ReaderAt, start, length int64) error {
	var data [256]byte
	count := min(int64(len(data)), length)
	if _, err := source.ReadAt(data[:count], start); err != nil && !(count == 0 && err == io.EOF) {
		return err
	}
	value := "<" + hex.EncodeToString(data[:count])
	if count < length {
		value += "… " + strconv.FormatInt(length, 10) + " bytes"
	}
	_, err := io.WriteString(out, value+">")
	return err
}
