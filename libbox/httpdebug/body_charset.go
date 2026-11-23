package httpdebug

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"mime"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/htmlindex"
	xunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// Charset declarations are properties of a body, not of its current page.
const bodyCharsetPrefixSize = 1024

type bodyCharsetKind uint8

const (
	bodyCharsetUnsupported bodyCharsetKind = iota
	bodyCharsetUTF8
	bodyCharsetASCII
	bodyCharsetUTF16LE
	bodyCharsetUTF16BE
	bodyCharsetGBK
	bodyCharsetGB18030
	bodyCharsetBig5
	bodyCharsetShiftJIS
	bodyCharsetEUCJP
	bodyCharsetEUCKR
	bodyCharsetSingleByte
)

type bodyCharset struct {
	name        string
	codec       encoding.Encoding
	kind        bodyCharsetKind
	error       string
	err         error
	replacement []byte
}

var bodyXMLEncoding = regexp.MustCompile(`(?i)^\s*<\?xml\s[^>]*\bencoding\s*=\s*["']\s*([a-z0-9._:+-]+)\s*["']`)

func detectBodyCharset(message Message, prefix []byte) bodyCharset {
	prefix = prefix[:min(len(prefix), bodyCharsetPrefixSize)]
	// A BOM fixes both the encoding and UTF-16 byte order, including a generic
	// charset=utf-16 declaration. Only the body's beginning is inspected.
	switch {
	case bytes.HasPrefix(prefix, []byte{0xff, 0xfe, 0x00, 0x00}):
		return lookupBodyCharset("utf-32le")
	case bytes.HasPrefix(prefix, []byte{0x00, 0x00, 0xfe, 0xff}):
		return lookupBodyCharset("utf-32be")
	case bytes.HasPrefix(prefix, []byte{0xef, 0xbb, 0xbf}):
		return lookupBodyCharset("utf-8")
	case bytes.HasPrefix(prefix, []byte{0xff, 0xfe}):
		return lookupBodyCharset("utf-16le")
	case bytes.HasPrefix(prefix, []byte{0xfe, 0xff}):
		return lookupBodyCharset("utf-16be")
	}
	contentType := headerValue(message, "Content-Type")
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err == nil && strings.TrimSpace(parameters["charset"]) != "" {
		return lookupBodyCharset(parameters["charset"])
	}
	if declaration := bodyMarkupCharset(mediaType, prefix); declaration != "" {
		return lookupBodyCharset(declaration)
	}
	return lookupBodyCharset("utf-8")
}

func bodyMarkupCharset(mediaType string, prefix []byte) string {
	text := strings.TrimSpace(string(prefix))
	lower := strings.ToLower(text)
	markup := mediaType == "text/html" || mediaType == "application/xhtml+xml" ||
		mediaType == "application/xml" || mediaType == "text/xml" || strings.HasSuffix(mediaType, "+xml") ||
		strings.HasPrefix(lower, "<?xml") || strings.HasPrefix(lower, "<!doctype html") ||
		strings.HasPrefix(lower, "<html") || strings.HasPrefix(lower, "<head") || strings.HasPrefix(lower, "<meta")
	if !markup {
		return ""
	}
	if match := bodyXMLEncoding.FindStringSubmatch(text); len(match) == 2 {
		return match[1]
	}
	// The tokenizer respects quoted attributes, comments and script raw text;
	// a charset-shaped substring in those places is not a meta declaration.
	tokens := html.NewTokenizer(bytes.NewReader(prefix))
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokens.Token()
			if token.Data != "meta" {
				continue
			}
			var charset, content, httpEquiv string
			for _, attribute := range token.Attr {
				switch attribute.Key {
				case "charset":
					charset = strings.TrimSpace(attribute.Val)
				case "content":
					content = attribute.Val
				case "http-equiv":
					httpEquiv = attribute.Val
				}
			}
			if charset != "" {
				return charset
			}
			if strings.EqualFold(strings.TrimSpace(httpEquiv), "content-type") {
				if _, parameters, err := mime.ParseMediaType(content); err == nil && parameters["charset"] != "" {
					return parameters["charset"]
				}
			}
		}
	}
}

func lookupBodyCharset(label string) bodyCharset {
	label = strings.ToLower(strings.TrimSpace(label))
	// Body bytes follow the declared HTTP charset. HTML's compatibility index
	// deliberately maps ASCII and Latin-1 labels to Windows-1252, which would
	// change bytes such as 0x80 in a captured body.
	switch label {
	case "us-ascii", "ascii", "ansi_x3.4-1968", "ansi_x3.4-1986", "iso-ir-6", "iso646-us", "iso_646.irv:1991", "csascii", "cp367", "ibm367", "us":
		return bodyCharset{name: "us-ascii", codec: encoding.Nop, kind: bodyCharsetASCII}
	case "iso-8859-1", "iso8859-1", "iso88591", "iso_8859-1", "iso_8859-1:1987", "iso-ir-100", "csisolatin1", "latin1", "latin-1", "l1", "cp819", "ibm819":
		return bodyCharset{name: "iso-8859-1", codec: charmap.ISO8859_1, kind: bodyCharsetSingleByte}
	}
	if label == "utf-16" || label == "utf16" {
		// RFC 2781's default applies when a generic declaration has no BOM.
		label = "utf-16be"
	}
	charset := bodyCharset{name: label}
	codec, err := htmlindex.Get(label)
	if err != nil {
		charset.error = "unsupported body charset: " + label
		charset.err = errors.New(charset.error)
		return charset
	}
	name, err := htmlindex.Name(codec)
	if err != nil {
		charset.error = "unsupported body charset: " + label
		charset.err = errors.New(charset.error)
		return charset
	}
	charset.name, charset.codec = name, codec
	switch name {
	case "utf-8":
		charset.kind = bodyCharsetUTF8
	case "utf-16le":
		charset.kind = bodyCharsetUTF16LE
		charset.codec = xunicode.UTF16(xunicode.LittleEndian, xunicode.IgnoreBOM)
	case "utf-16be":
		charset.kind = bodyCharsetUTF16BE
		charset.codec = xunicode.UTF16(xunicode.BigEndian, xunicode.IgnoreBOM)
	case "gbk":
		charset.kind = bodyCharsetGBK
	case "gb18030":
		charset.kind = bodyCharsetGB18030
	case "big5":
		charset.kind = bodyCharsetBig5
	case "shift_jis":
		charset.kind = bodyCharsetShiftJIS
	case "euc-jp":
		charset.kind = bodyCharsetEUCJP
	case "euc-kr":
		charset.kind = bodyCharsetEUCKR
	default:
		if strings.HasPrefix(name, "windows-") || strings.HasPrefix(name, "iso-8859-") ||
			strings.HasPrefix(name, "koi8-") || name == "ibm866" || name == "macintosh" || name == "x-mac-cyrillic" {
			charset.kind = bodyCharsetSingleByte
		} else {
			// Stateful encodings require prior decoder state at a byte page
			// boundary. Keep their exact bytes available without claiming a
			// readable representation that this inspection path cannot supply.
			charset.error = "unsupported body charset: " + name
			charset.err = errors.New(charset.error)
		}
	}
	if charset.err == nil {
		// A literal U+FFFD is valid text. Keep its source spelling to tell it
		// apart from the replacement x/text uses for an unmapped input value.
		charset.replacement, _ = charset.codec.NewEncoder().Bytes([]byte("\ufffd"))
	}
	return charset
}

// Shrink only for a complete, valid character crossing the page edge. Invalid
// bytes and NUL elsewhere in the page must not undo this alignment.
func bodyPageEnd(charset bodyCharset, data []byte, limit int) int {
	end := min(len(data), limit)
	if end == len(data) || charset.kind == bodyCharsetUnsupported {
		return end
	}
	for position := 0; position < end; {
		width, valid := charset.characterWidth(data[position:])
		if valid && position+width > end && charset.mappedCharacter(data[position:position+width]) {
			return position
		}
		position += max(1, width)
	}
	return end
}

func (charset bodyCharset) characterWidth(data []byte) (int, bool) {
	if len(data) == 0 {
		return 0, false
	}
	value := data[0]
	switch charset.kind {
	case bodyCharsetASCII:
		return 1, value < utf8.RuneSelf
	case bodyCharsetUTF8:
		r, width := utf8.DecodeRune(data)
		return width, r != utf8.RuneError || width > 1
	case bodyCharsetUTF16LE, bodyCharsetUTF16BE:
		if len(data) < 2 {
			return 1, false
		}
		unit := charset.utf16Unit(data)
		if unit >= 0xd800 && unit <= 0xdbff {
			if len(data) >= 4 {
				following := charset.utf16Unit(data[2:])
				if following >= 0xdc00 && following <= 0xdfff {
					return 4, true
				}
			}
			return 2, false
		}
		return 2, unit < 0xdc00 || unit > 0xdfff
	case bodyCharsetGB18030:
		if len(data) >= 4 && bodyGBLead(value) && bodyGBDigit(data[1]) && bodyGBLead(data[2]) && bodyGBDigit(data[3]) {
			return 4, true
		}
		fallthrough
	case bodyCharsetGBK:
		if value <= 0x80 {
			return 1, true
		}
		return bodyDoubleByteWidth(data, bodyGBLead(value), func(trail byte) bool { return trail >= 0x40 && trail <= 0xfe && trail != 0x7f })
	case bodyCharsetBig5:
		if value < 0x80 {
			return 1, true
		}
		return bodyDoubleByteWidth(data, bodyGBLead(value), func(trail byte) bool {
			return trail >= 0x40 && trail <= 0x7e || trail >= 0xa1 && trail <= 0xfe
		})
	case bodyCharsetShiftJIS:
		if value < 0x80 || value >= 0xa1 && value <= 0xdf {
			return 1, true
		}
		return bodyDoubleByteWidth(data, value >= 0x81 && value <= 0x9f || value >= 0xe0 && value <= 0xfc, func(trail byte) bool {
			return trail >= 0x40 && trail <= 0xfc && trail != 0x7f
		})
	case bodyCharsetEUCJP:
		if value < 0x80 {
			return 1, true
		}
		if value == 0x8f && len(data) >= 3 && bodyEUCTrail(data[1]) && bodyEUCTrail(data[2]) {
			return 3, true
		}
		if value == 0x8e {
			return bodyDoubleByteWidth(data, true, func(trail byte) bool { return trail >= 0xa1 && trail <= 0xdf })
		}
		return bodyDoubleByteWidth(data, bodyEUCTrail(value), bodyEUCTrail)
	case bodyCharsetEUCKR:
		if value < 0x80 {
			return 1, true
		}
		return bodyDoubleByteWidth(data, value >= 0x81 && value <= 0xfe, func(trail byte) bool {
			return trail >= 0x41 && trail <= 0x5a || trail >= 0x61 && trail <= 0x7a || trail >= 0x81 && trail <= 0xfe
		})
	case bodyCharsetSingleByte:
		return 1, true
	default:
		return 1, false
	}
}

func bodyGBLead(value byte) bool   { return value >= 0x81 && value <= 0xfe }
func bodyGBDigit(value byte) bool  { return value >= 0x30 && value <= 0x39 }
func bodyEUCTrail(value byte) bool { return value >= 0xa1 && value <= 0xfe }

func bodyDoubleByteWidth(data []byte, lead bool, trail func(byte) bool) (int, bool) {
	if lead && len(data) >= 2 && trail(data[1]) {
		return 2, true
	}
	return 1, false
}

func (charset bodyCharset) utf16Unit(data []byte) uint16 {
	if charset.kind == bodyCharsetUTF16LE {
		return binary.LittleEndian.Uint16(data)
	}
	return binary.BigEndian.Uint16(data)
}

func (charset bodyCharset) validPage(data []byte, offset int64) bool {
	if charset.kind == bodyCharsetUnsupported || (charset.kind == bodyCharsetUTF16LE || charset.kind == bodyCharsetUTF16BE) && offset%2 != 0 {
		return false
	}
	for position := 0; position < len(data); {
		width, valid := charset.characterWidth(data[position:])
		if !valid {
			return false
		}
		position += width
	}
	return true
}

func decodeBodyDisplay(ctx context.Context, charset bodyCharset, data []byte, offset int64) (*string, error) {
	if !charset.validPage(data, offset) {
		return nil, ctx.Err()
	}
	reader := bodyTextReaderAt(ctx, charset, bytes.NewReader(data), offset == 0)
	decoded, err := readBodyBytes(ctx, reader, -1)
	if err != nil {
		// Binary or malformed character data still has an exact byte page.
		// A full derived text view receives the strict reader's actual error.
		return nil, ctx.Err()
	}
	if !utf8.Valid(decoded) || bytes.IndexByte(decoded, 0) >= 0 {
		return nil, nil
	}
	text := string(decoded)
	return &text, nil
}

// Complete derived text streams use the same fixed charset as the body pages.
// This helper never changes the byte offsets of the underlying captured body.
func newBodyTextReader(ctx context.Context, charset bodyCharset, reader io.Reader) io.Reader {
	return bodyTextReaderAt(ctx, charset, reader, true)
}

func bodyTextReaderAt(ctx context.Context, charset bodyCharset, reader io.Reader, stripBOM bool) io.Reader {
	state := &bodyReadContext{ctx: ctx}
	if err := ctx.Err(); err != nil {
		return &bodyCharsetErrorReader{err: err}
	}
	if charset.err != nil {
		return &bodyCharsetErrorReader{err: charset.err}
	}
	reader = &bodyContextReader{state: state, reader: reader}
	if charset.codec != nil {
		reader = transform.NewReader(reader, &bodyCharsetTransformer{ctx: ctx, charset: charset, decoder: charset.codec.NewDecoder()})
	}
	reader = &bodyContextReader{state: state, reader: reader}
	if stripBOM {
		reader = &bodyTextBOMReader{reader: bufio.NewReader(reader)}
	}
	return &bodyContextReader{state: state, reader: reader}
}

type bodyTextBOMReader struct {
	reader  *bufio.Reader
	checked bool
}

func (reader *bodyTextBOMReader) Read(data []byte) (int, error) {
	if !reader.checked {
		reader.checked = true
		prefix, _ := reader.reader.Peek(3)
		if bytes.Equal(prefix, []byte{0xef, 0xbb, 0xbf}) {
			_, _ = reader.reader.Discard(3)
		}
	}
	return reader.reader.Read(data)
}
