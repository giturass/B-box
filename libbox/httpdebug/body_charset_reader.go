package httpdebug

import (
	"bytes"
	"context"
	"errors"
	"unicode/utf8"

	"golang.org/x/text/transform"
)

var errBodyCharsetData = errors.New("body contains invalid bytes for its declared charset")

type bodyCharsetErrorReader struct{ err error }

func (reader *bodyCharsetErrorReader) Read([]byte) (int, error) { return 0, reader.err }

// x/text decoders replace invalid or unmapped input by default. Derived body
// text must report that case so JSON formatting cannot silently change values.
type bodyCharsetTransformer struct {
	ctx     context.Context
	charset bodyCharset
	decoder transform.Transformer
}

func (decoder *bodyCharsetTransformer) Reset() { decoder.decoder.Reset() }

func (decoder *bodyCharsetTransformer) Transform(destination, source []byte, atEOF bool) (nDst, nSrc int, err error) {
	if err := decoder.ctx.Err(); err != nil {
		return 0, 0, err
	}
	validEnd := 0
	var validationError error
	for validEnd < len(source) {
		remaining := source[validEnd:]
		if !atEOF && decoder.charset.incompleteCharacter(remaining) {
			validationError = transform.ErrShortSrc
			break
		}
		width, valid := decoder.charset.characterWidth(remaining)
		if !valid {
			validationError = errBodyCharsetData
			break
		}
		validEnd += width
	}
	nDst, nSrc, err = decoder.decoder.Transform(destination, source[:validEnd], atEOF && validEnd == len(source))
	if count := bytes.Count(destination[:nDst], []byte("\ufffd")); count > 0 {
		literalCount := 0
		for position := 0; position < nSrc; {
			width, _ := decoder.charset.characterWidth(source[position:nSrc])
			if bytes.Equal(source[position:position+width], decoder.charset.replacement) {
				literalCount++
			}
			position += width
		}
		if count > literalCount {
			return nDst, nSrc, errBodyCharsetData
		}
	}
	if decoder.ctx.Err() != nil {
		return nDst, nSrc, decoder.ctx.Err()
	}
	if err == nil && nSrc == validEnd {
		err = validationError
	}
	return nDst, nSrc, err
}

func (charset bodyCharset) incompleteCharacter(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	switch charset.kind {
	case bodyCharsetUTF8:
		return !utf8.FullRune(data)
	case bodyCharsetUTF16LE, bodyCharsetUTF16BE:
		if len(data) < 2 {
			return true
		}
		unit := charset.utf16Unit(data)
		return unit >= 0xd800 && unit <= 0xdbff && len(data) < 4
	case bodyCharsetGB18030:
		if bodyGBLead(data[0]) && len(data) >= 2 && bodyGBDigit(data[1]) {
			return len(data) < 4
		}
		fallthrough
	case bodyCharsetGBK, bodyCharsetBig5:
		return bodyGBLead(data[0]) && len(data) < 2
	case bodyCharsetShiftJIS:
		return (data[0] >= 0x81 && data[0] <= 0x9f || data[0] >= 0xe0 && data[0] <= 0xfc) && len(data) < 2
	case bodyCharsetEUCJP:
		if data[0] == 0x8f {
			return len(data) < 3
		}
		return (data[0] == 0x8e || bodyEUCTrail(data[0])) && len(data) < 2
	case bodyCharsetEUCKR:
		return bodyGBLead(data[0]) && len(data) < 2
	default:
		return false
	}
}

func (charset bodyCharset) mappedCharacter(data []byte) bool {
	if charset.codec == nil {
		return false
	}
	decoded, err := charset.codec.NewDecoder().Bytes(data)
	return err == nil && (!bytes.Contains(decoded, []byte("\ufffd")) || bytes.Equal(data, charset.replacement))
}
