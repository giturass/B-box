package httpdebug

import (
	"bufio"
	"context"
	"errors"
	"io"
	"unicode/utf8"
)

// Formatting uses a streaming JSON grammar rather than decoding into maps.
// Numeric literals, duplicate keys and the exact spelling of escapes survive.
const (
	viewJSONValue = iota
	viewJSONDone
	viewJSONArrayFirst
	viewJSONArrayComma
	viewJSONArrayValue
	viewJSONObjectFirst
	viewJSONObjectColon
	viewJSONObjectValue
	viewJSONObjectComma
	viewJSONObjectKey
)

type viewJSONFrame struct {
	kind  byte
	state int
}

func writeBodyJSON(ctx context.Context, out io.Writer, source io.Reader) (err error) {
	reader := bufio.NewReader(&viewContextReader{ctx, source})
	writer := bufio.NewWriter(&viewContextWriter{ctx, out})
	defer func() {
		if err == nil {
			err = writer.Flush()
		}
	}()
	stack := []viewJSONFrame{{state: viewJSONValue}}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		value, readErr := peekViewJSON(reader)
		frame := &stack[len(stack)-1]
		if readErr == io.EOF {
			if len(stack) == 1 && frame.state == viewJSONDone {
				return nil
			}
			return errors.New("incomplete JSON body")
		}
		if readErr != nil {
			return readErr
		}
		switch frame.state {
		case viewJSONDone:
			return errors.New("additional data after the JSON value")
		case viewJSONObjectColon:
			if value != ':' {
				return errors.New("missing JSON object colon")
			}
			reader.ReadByte()
			if _, err = writer.WriteString(": "); err != nil {
				return err
			}
			frame.state = viewJSONObjectValue
			continue
		case viewJSONArrayComma, viewJSONObjectComma:
			closing := byte(']')
			if frame.kind == '{' {
				closing = '}'
			}
			if value == closing {
				if err = writeViewJSONClose(writer, reader, stack, true); err != nil {
					return err
				}
				stack = stack[:len(stack)-1]
				continue
			}
			if value != ',' {
				return errors.New("missing JSON comma or closing delimiter")
			}
			reader.ReadByte()
			if err = writer.WriteByte(','); err != nil {
				return err
			}
			frame.state = viewJSONArrayValue
			if frame.kind == '{' {
				frame.state = viewJSONObjectKey
			}
			continue
		case viewJSONObjectFirst, viewJSONObjectKey:
			if value == '}' && frame.state == viewJSONObjectFirst {
				if err = writeViewJSONClose(writer, reader, stack, false); err != nil {
					return err
				}
				stack = stack[:len(stack)-1]
				continue
			}
			if value != '"' {
				return errors.New("JSON object keys must be strings")
			}
			if err = viewJSONNewline(writer, len(stack)-1); err != nil {
				return err
			}
			if err = writeViewJSONString(writer, reader); err != nil {
				return err
			}
			frame.state = viewJSONObjectColon
			continue
		case viewJSONArrayFirst:
			if value == ']' {
				if err = writeViewJSONClose(writer, reader, stack, false); err != nil {
					return err
				}
				stack = stack[:len(stack)-1]
				continue
			}
			fallthrough
		case viewJSONArrayValue:
			if err = viewJSONNewline(writer, len(stack)-1); err != nil {
				return err
			}
		}
		// Publish the parent's post-value state before pushing a nested value.
		switch frame.kind {
		case '[':
			frame.state = viewJSONArrayComma
		case '{':
			frame.state = viewJSONObjectComma
		default:
			frame.state = viewJSONDone
		}
		switch value {
		case '{', '[':
			reader.ReadByte()
			if err = writer.WriteByte(value); err != nil {
				return err
			}
			state := viewJSONObjectFirst
			if value == '[' {
				state = viewJSONArrayFirst
			}
			stack = append(stack, viewJSONFrame{value, state})
		case '"':
			err = writeViewJSONString(writer, reader)
		case 't':
			err = writeViewJSONLiteral(writer, reader, "true")
		case 'f':
			err = writeViewJSONLiteral(writer, reader, "false")
		case 'n':
			err = writeViewJSONLiteral(writer, reader, "null")
		default:
			if value != '-' && (value < '0' || value > '9') {
				return errors.New("invalid JSON value")
			}
			err = writeViewJSONNumber(writer, reader)
		}
		if err != nil {
			return err
		}
	}
}

func peekViewJSON(reader *bufio.Reader) (byte, error) {
	for {
		data, err := reader.Peek(1)
		if err != nil {
			return 0, err
		}
		if data[0] != ' ' && data[0] != '\n' && data[0] != '\r' && data[0] != '\t' {
			return data[0], nil
		}
		reader.ReadByte()
	}
}

func viewJSONNewline(writer *bufio.Writer, depth int) error {
	if err := writer.WriteByte('\n'); err != nil {
		return err
	}
	return writeViewIndent(writer, depth)
}

func writeViewJSONClose(writer *bufio.Writer, reader *bufio.Reader, stack []viewJSONFrame, nonempty bool) error {
	if nonempty {
		if err := viewJSONNewline(writer, len(stack)-2); err != nil {
			return err
		}
	}
	value, err := reader.ReadByte()
	if err != nil {
		return err
	}
	return writer.WriteByte(value)
}

func writeViewJSONString(writer *bufio.Writer, reader *bufio.Reader) error {
	reader.ReadByte() // opening quote, already checked by the caller
	if err := writer.WriteByte('"'); err != nil {
		return err
	}
	for {
		value, size, err := reader.ReadRune()
		if err != nil {
			return err
		}
		if value == utf8.RuneError && size == 1 || value < 0x20 {
			return errors.New("invalid character in a JSON string")
		}
		if _, err = writer.WriteRune(value); err != nil {
			return err
		}
		if value == '"' {
			return nil
		}
		if value != '\\' {
			continue
		}
		escape, err := reader.ReadByte()
		if err != nil {
			return err
		}
		if escape != '"' && escape != '\\' && escape != '/' && escape != 'b' && escape != 'f' && escape != 'n' && escape != 'r' && escape != 't' && escape != 'u' {
			return errors.New("invalid JSON string escape")
		}
		if err = writer.WriteByte(escape); err != nil {
			return err
		}
		if escape == 'u' {
			for i := 0; i < 4; i++ {
				digit, err := reader.ReadByte()
				if err != nil {
					return err
				}
				if !(digit >= '0' && digit <= '9' || digit >= 'a' && digit <= 'f' || digit >= 'A' && digit <= 'F') {
					return errors.New("invalid JSON Unicode escape")
				}
				if err = writer.WriteByte(digit); err != nil {
					return err
				}
			}
		}
	}
}

func writeViewJSONLiteral(writer *bufio.Writer, reader *bufio.Reader, literal string) error {
	for i := range literal {
		value, err := reader.ReadByte()
		if err != nil {
			return err
		}
		if value != literal[i] {
			return errors.New("invalid JSON literal")
		}
		if err = writer.WriteByte(value); err != nil {
			return err
		}
	}
	return nil
}

func writeViewJSONNumber(writer *bufio.Writer, reader *bufio.Reader) error {
	peek := func() byte {
		data, _ := reader.Peek(1)
		if len(data) == 0 {
			return 0
		}
		return data[0]
	}
	copyByte := func() error {
		value, err := reader.ReadByte()
		if err != nil {
			return err
		}
		return writer.WriteByte(value)
	}
	if peek() == '-' {
		if err := copyByte(); err != nil {
			return err
		}
	}
	if value := peek(); value == '0' {
		if err := copyByte(); err != nil {
			return err
		}
	} else if value >= '1' && value <= '9' {
		for value := peek(); value >= '0' && value <= '9'; value = peek() {
			if err := copyByte(); err != nil {
				return err
			}
		}
	} else {
		return errors.New("invalid JSON number")
	}
	digits := func() error {
		if value := peek(); value < '0' || value > '9' {
			return errors.New("missing JSON number digits")
		}
		for value := peek(); value >= '0' && value <= '9'; value = peek() {
			if err := copyByte(); err != nil {
				return err
			}
		}
		return nil
	}
	if peek() == '.' {
		if err := copyByte(); err != nil {
			return err
		}
		if err := digits(); err != nil {
			return err
		}
	}
	if value := peek(); value == 'e' || value == 'E' {
		if err := copyByte(); err != nil {
			return err
		}
		if value := peek(); value == '-' || value == '+' {
			if err := copyByte(); err != nil {
				return err
			}
		}
		if err := digits(); err != nil {
			return err
		}
	}
	return nil
}

func writeViewIndent(out io.Writer, depth int) error {
	const spaces = "                                                                "
	for depth > 0 {
		count := min(depth, len(spaces)/2)
		if _, err := io.WriteString(out, spaces[:count*2]); err != nil {
			return err
		}
		depth -= count
	}
	return nil
}
