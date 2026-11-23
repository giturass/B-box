package httpdebug

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

func bodyPageBytesForTest(t *testing.T, page Preview) []byte {
	t.Helper()
	if page.Encoding == "text" {
		return []byte(page.Text)
	}
	data, err := base64.StdEncoding.DecodeString(page.Text)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestBodyPagesKeepValidUTF8TailWhenThePageContainsBinary(t *testing.T) {
	for _, binaryByte := range []byte{0, 0xff} {
		data := bytes.Repeat([]byte("a"), BodyPageSize-1)
		data[7] = binaryByte
		data = append(data, []byte("中😀end")...)
		message := Message{CapturedBytes: int64(len(data)), BodyBytes: int64(len(data)), BodyComplete: true}
		first, err := readBodyPage(message, func() io.Reader { return bytes.NewReader(data) }, 0, false)
		if err != nil || first.Encoding != "base64" || first.NextOffset != BodyPageSize-1 || !first.HasMore {
			t.Fatalf("binary page split a valid trailing character: %+v %v", first, err)
		}
		second, err := readBodyPage(message, func() io.Reader { return bytes.NewReader(data) }, first.NextOffset, false)
		if err != nil || second.Encoding != "text" || second.Text != "中😀end" || second.HasMore {
			t.Fatalf("following UTF-8 page is not readable: %+v %v", second, err)
		}
		if !bytes.Equal(append(bodyPageBytesForTest(t, first), bodyPageBytesForTest(t, second)...), data) {
			t.Fatal("binary classification lost or duplicated source bytes")
		}
	}
	// A stray continuation byte just after the window is not a character that
	// crosses its edge; the preceding ASCII byte must stay on this page.
	data := append(bytes.Repeat([]byte("a"), BodyPageSize), 0x80, 'z')
	first := previewForTest(t, Message{}, data)
	if first.NextOffset != BodyPageSize {
		t.Fatal("invalid lookahead shortened an otherwise complete page", first.NextOffset)
	}
	last := previewForTest(t, Message{}, []byte{'a', 0xe4, 0xb8})
	if last.NextOffset != 3 || last.HasMore || !bytes.Equal(bodyPageBytesForTest(t, last), []byte{'a', 0xe4, 0xb8}) {
		t.Fatal("an incomplete final character was discarded")
	}
}

func utf16BodyForTest(text string, littleEndian, bom bool) []byte {
	var data []byte
	if bom {
		if littleEndian {
			data = append(data, 0xff, 0xfe)
		} else {
			data = append(data, 0xfe, 0xff)
		}
	}
	for _, unit := range utf16.Encode([]rune(text)) {
		var encoded [2]byte
		if littleEndian {
			binary.LittleEndian.PutUint16(encoded[:], unit)
		} else {
			binary.BigEndian.PutUint16(encoded[:], unit)
		}
		data = append(data, encoded[:]...)
	}
	return data
}

func TestBodyCharsetPagesPreserveSourceBytesAndReadableText(t *testing.T) {
	fixtures := []struct {
		charset string
		text    string
		encoded []byte
		prefix  int
		first   int64
	}{
		{"gbk", "中文", []byte{0xd6, 0xd0, 0xce, 0xc4}, BodyPageSize - 1, BodyPageSize - 1},
		{"gb18030", "😀中文", []byte{0x94, 0x39, 0xfc, 0x36, 0xd6, 0xd0, 0xce, 0xc4}, BodyPageSize - 1, BodyPageSize - 1},
		{"big5", "中文", []byte{0xa4, 0xa4, 0xa4, 0xe5}, BodyPageSize - 1, BodyPageSize - 1},
		{"windows-1252", "€ café ‘quoted’", []byte{0x80, ' ', 'c', 'a', 'f', 0xe9, ' ', 0x91, 'q', 'u', 'o', 't', 'e', 'd', 0x92}, BodyPageSize - 1, BodyPageSize},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.charset, func(t *testing.T) {
			prefix := strings.Repeat("a", fixture.prefix)
			wire := append([]byte(prefix), bytes.Repeat(fixture.encoded, 6000)...)
			message := Message{Headers: []Header{{"Content-Type", "text/plain; charset=" + fixture.charset}}, CapturedBytes: int64(len(wire)), BodyBytes: int64(len(wire)), BodyComplete: true}
			var recovered bytes.Buffer
			var displayed strings.Builder
			var offset int64
			for {
				page, err := readBodyPage(message, func() io.Reader { return bytes.NewReader(wire) }, offset, false)
				if err != nil || page.Charset != fixture.charset || page.Truncated || page.Error != "" {
					t.Fatalf("charset page is unavailable: %+v %v", page, err)
				}
				if offset == 0 && page.NextOffset != fixture.first {
					t.Fatalf("encoded character split at first edge: %d", page.NextOffset)
				}
				recovered.Write(bodyPageBytesForTest(t, page))
				if page.DisplayText != nil {
					if !utf8.ValidString(*page.DisplayText) {
						t.Fatal("derived display is not UTF-8")
					}
					displayed.WriteString(*page.DisplayText)
				} else if page.Encoding == "text" {
					displayed.WriteString(page.Text)
				} else {
					t.Fatal("valid encoded text has no readable display")
				}
				if page.NextOffset-offset != int64(len(bodyPageBytesForTest(t, page))) {
					t.Fatal("display conversion changed original byte offsets")
				}
				if !page.HasMore {
					break
				}
				if page.NextOffset <= offset {
					t.Fatal("charset page made no progress")
				}
				offset = page.NextOffset
			}
			if !bytes.Equal(recovered.Bytes(), wire) || displayed.String() != prefix+strings.Repeat(fixture.text, 6000) {
				t.Fatal("charset paging changed the stored bytes or readable text")
			}
		})
	}
	for _, littleEndian := range []bool{false, true} {
		for _, bom := range []bool{false, true} {
			prefixLength := (BodyPageSize - 2) / 2
			if bom {
				prefixLength--
			}
			text := strings.Repeat("a", prefixLength) + strings.Repeat("😀中文", 9000)
			wire := utf16BodyForTest(text, littleEndian, bom)
			label, canonical := "utf-16be", "utf-16be"
			if littleEndian {
				label, canonical = "utf-16le", "utf-16le"
			}
			if bom {
				label = "utf-16"
			}
			message := Message{Headers: []Header{{"Content-Type", "text/plain; charset=" + label}}, CapturedBytes: int64(len(wire)), BodyComplete: true}
			var recovered bytes.Buffer
			var displayed strings.Builder
			var offset int64
			for {
				page, err := readBodyPage(message, func() io.Reader { return bytes.NewReader(wire) }, offset, false)
				if err != nil || page.Charset != canonical || page.DisplayText == nil {
					t.Fatalf("UTF-16 page is unreadable: label=%s bom=%v %+v %v", label, bom, page, err)
				}
				if offset == 0 && page.NextOffset != BodyPageSize-2 {
					t.Fatal("UTF-16 page split its surrogate pair", page.NextOffset)
				}
				recovered.Write(bodyPageBytesForTest(t, page))
				displayed.WriteString(*page.DisplayText)
				if !page.HasMore {
					break
				}
				offset = page.NextOffset
			}
			if !bytes.Equal(recovered.Bytes(), wire) || displayed.String() != text {
				t.Fatal("UTF-16 pages lost bytes, BOM position or readable text")
			}
		}
	}
}

func TestBodyMarkupCharsetIsFixedAtTheBeginningForLaterPages(t *testing.T) {
	for _, declaration := range []string{
		`<html><head><meta charset="gbk"></head>`,
		`<html><head><meta http-equiv="Content-Type" content="text/html; charset=gbk"></head>`,
		`<?xml version="1.0" encoding="GBK"?><document>`,
	} {
		prefix := declaration + strings.Repeat("a", BodyPageSize-1-len(declaration))
		wire := append([]byte(prefix), []byte{0xd6, 0xd0, 0xce, 0xc4}...)
		message := Message{CapturedBytes: int64(len(wire)), BodyComplete: true}
		first, err := readBodyPage(message, func() io.Reader { return bytes.NewReader(wire) }, 0, false)
		if err != nil || first.Charset != "gbk" || first.NextOffset != BodyPageSize-1 {
			t.Fatal("markup charset was not detected from the body beginning", first.Charset, first.NextOffset, err)
		}
		second, err := readBodyPage(message, func() io.Reader { return bytes.NewReader(wire) }, first.NextOffset, false)
		if err != nil || second.Charset != "gbk" || second.DisplayText == nil || *second.DisplayText != "中文" {
			t.Fatal("later page forgot the original markup charset", second.Charset, err)
		}
	}
	message := Message{Headers: []Header{{"Content-Type", "text/html; charset=big5"}}}
	if charset := detectBodyCharset(message, []byte(`<meta charset="gbk">`)); charset.name != "big5" {
		t.Fatal("markup overrode an HTTP charset")
	}
	if charset := detectBodyCharset(Message{}, []byte(`<html><script>"<meta charset='gbk'>"</script>`)); charset.name != "utf-8" {
		t.Fatal("script text was mistaken for a charset declaration")
	}
	prefix := "<html>" + strings.Repeat("a", bodyCharsetPrefixSize) + `<meta charset="gbk">`
	if charset := detectBodyCharset(Message{}, []byte(prefix)); charset.name != "utf-8" {
		t.Fatal("charset sniffing read beyond its fixed prefix")
	}
}

type countedBodyDecoder struct {
	io.ReadCloser
	bytes  int64
	calls  int
	cancel context.CancelFunc
}

func (decoder *countedBodyDecoder) Read(data []byte) (int, error) {
	n, err := decoder.ReadCloser.Read(data)
	decoder.bytes += int64(n)
	decoder.calls++
	if decoder.cancel != nil {
		decoder.cancel()
	}
	return n, err
}

func TestBodyReaderReusesDecodedPrefixForForwardAndBackwardPages(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate", "br", "zstd", "gzip,br,zstd"} {
		t.Run(encoding, func(t *testing.T) {
			store, request := bodyTestStore(t)
			plain := []byte(strings.Repeat("abcdef中文😀", BodyPageSize))
			wire := encodedBody(t, plain, strings.Split(encoding, ",")...)
			request.Header.Set("Content-Encoding", encoding)
			id := store.begin(Metadata{}, request, -1)
			store.appendBody(id, false, wire)
			store.bodyComplete(id, false)
			reader := NewBodyReader()
			defer reader.Close()
			first, err := reader.Read(context.Background(), store, id, false, 0, false)
			if err != nil || !first.HasMore || first.Truncated || reader.state.decoded == nil {
				t.Fatal("compressed first page is unavailable", err)
			}
			spoolName := reader.state.decoded.Name()
			if reader.state.size > BodyPageSize+utf8.UTFMax {
				t.Fatal("first page eagerly decoded the whole body")
			}
			counter := &countedBodyDecoder{ReadCloser: reader.state.decoder}
			reader.state.decoder = counter
			repeat, err := reader.Read(context.Background(), store, id, false, 0, false)
			if err != nil || repeat.Text != first.Text || counter.calls != 0 {
				t.Fatal("repeated page decompressed its prefix again", counter.calls, err)
			}
			second, err := reader.Read(context.Background(), store, id, false, first.NextOffset, false)
			if err != nil || counter.calls == 0 || reader.state.decoder != counter {
				t.Fatal("forward paging restarted instead of continuing the decoder", err)
			}
			calls := counter.calls
			back, err := reader.Read(context.Background(), store, id, false, 0, false)
			if err != nil || back.Text != first.Text || counter.calls != calls {
				t.Fatal("backward paging did not seek the existing decoded file", err)
			}
			var recovered bytes.Buffer
			recovered.Write(bodyPageBytesForTest(t, first))
			page := second
			for {
				recovered.Write(bodyPageBytesForTest(t, page))
				if page.Truncated || page.Error != "" {
					t.Fatal("healthy compressed page reported corruption", page.Error)
				}
				if !page.HasMore {
					break
				}
				page, err = reader.Read(context.Background(), store, id, false, page.NextOffset, false)
				if err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(recovered.Bytes(), plain) || reader.state.decoded.Name() != spoolName || counter.bytes > int64(len(plain)) {
				t.Fatal("stable compressed paging restarted or changed source bytes")
			}
			reader.Close()
			if _, err := os.Stat(spoolName); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("closing body reader left its private decoded file", err)
			}
		})
	}
}

func TestBodyReaderRefreshesMetadataAndInvalidatesGrowingCompressedBodies(t *testing.T) {
	store, request := bodyTestStore(t)
	plain := []byte(strings.Repeat("body 中文 😀 ", BodyPageSize))
	wire := encodedBody(t, plain, "gzip")
	request.Header.Set("Content-Encoding", "gzip")
	id := store.begin(Metadata{}, request, -1)
	store.appendBody(id, false, wire[:len(wire)/2])
	reader := NewBodyReader()
	defer reader.Close()
	first, err := reader.Read(context.Background(), store, id, false, 0, false)
	if err != nil || first.Message == nil || first.Message.BodyComplete || first.Message.CapturedBytes != int64(len(wire)/2) {
		t.Fatal("initial page did not carry current message metadata", err)
	}
	oldName := reader.state.decoded.Name()
	store.appendBody(id, false, wire[len(wire)/2:])
	store.bodyComplete(id, false)
	updated, err := reader.Read(context.Background(), store, id, false, 0, false)
	if err != nil || updated.Message == nil || !updated.Message.BodyComplete || updated.Message.CapturedBytes != int64(len(wire)) || updated.Truncated {
		t.Fatal("growing body retained a stale decode error or stale metadata", err)
	}
	if reader.state.decoded.Name() == oldName {
		t.Fatal("growing compressed snapshot kept its old decoder state")
	}
	if _, err := os.Stat(oldName); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("revised body left its previous decoded file", err)
	}
	state := reader.state
	// A later forwarded-byte count need not change the saved source. The
	// current metadata must update without paying for another decode.
	store.appendBody(id, false, []byte("not retained after body EOF"))
	updated, err = reader.Read(context.Background(), store, id, false, 0, false)
	if err != nil || reader.state != state || updated.Message.BodyBytes <= int64(len(wire)) || !updated.Message.BodyTruncated || !updated.Truncated {
		t.Fatal("metadata-only revision restarted decoding or stayed stale", err)
	}
	legacy, err := store.BodyPage(id, false, 0, false)
	if err != nil || legacy.Message == nil || legacy.Message.BodyBytes != updated.Message.BodyBytes {
		t.Fatal("compatibility BodyPage omitted current message metadata", err)
	}
}

func TestBodyReaderDistinguishesEmptyDecodedBodyAndUnavailableEncoding(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate", "br", "zstd"} {
		store, request := bodyTestStore(t)
		wire := encodedBody(t, nil, encoding)
		request.Header.Set("Content-Encoding", encoding)
		id := store.begin(Metadata{}, request, -1)
		store.appendBody(id, false, wire)
		reader := NewBodyReader()
		page, err := reader.Read(context.Background(), store, id, false, 0, false)
		reader.Close()
		if err != nil || page.Source != "decoded" || page.Text != "" || page.NextOffset != 0 || page.HasMore || page.Truncated || page.Error != "" {
			t.Fatal("valid empty compressed body fell back to raw bytes", encoding, page.Source, page.Error, err)
		}
	}
	store, request := bodyTestStore(t)
	request.Header.Set("Content-Encoding", "unsupported")
	wire := bytes.Repeat([]byte{0, 0xff, 2}, BodyPageSize)
	id := store.begin(Metadata{}, request, -1)
	store.appendBody(id, false, wire)
	reader := NewBodyReader()
	defer reader.Close()
	first, err := reader.Read(context.Background(), store, id, false, 0, false)
	if err != nil || first.Source != "raw" || first.Encoding != "base64" || first.Error == "" || first.Truncated || first.Message == nil {
		t.Fatal("unavailable encoding did not preserve an explicit raw fallback", err)
	}
	second, err := reader.Read(context.Background(), store, id, false, first.NextOffset, true)
	if err != nil || second.Source != "raw" || second.Offset != first.NextOffset || second.Error != first.Error {
		t.Fatal("later raw fallback page changed byte source", err)
	}
	if !bytes.Equal(append(bodyPageBytesForTest(t, first), bodyPageBytesForTest(t, second)...), wire[:second.NextOffset]) {
		t.Fatal("raw fallback offsets did not describe exact saved bytes")
	}
}

func TestBodyReaderCancellationAndClearReleaseDecodedFiles(t *testing.T) {
	store, request := bodyTestStore(t)
	plain := []byte(strings.Repeat("cancel 中文 ", 100000))
	wire := encodedBody(t, plain, "gzip")
	request.Header.Set("Content-Encoding", "gzip")
	id := store.begin(Metadata{}, request, -1)
	store.appendBody(id, false, wire)
	reader := NewBodyReader()
	defer reader.Close()
	if _, err := reader.Read(context.Background(), store, id, false, 0, false); err != nil {
		t.Fatal(err)
	}
	name := reader.state.decoded.Name()
	ctx, cancel := context.WithCancel(context.Background())
	reader.state.decoder = &countedBodyDecoder{ReadCloser: reader.state.decoder, cancel: cancel}
	if _, err := reader.Read(ctx, store, id, false, int64(len(plain)-100), false); !errors.Is(err, context.Canceled) {
		t.Fatal("in-flight decompression ignored cancellation", err)
	}
	if reader.state != nil {
		t.Fatal("canceled decoder remained reusable")
	}
	if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancellation left a decoded temporary file", err)
	}
	if _, err := reader.Read(context.Background(), store, id, false, 0, false); err != nil {
		t.Fatal("new context could not retry after canceled decoding", err)
	}
	name = reader.state.decoded.Name()
	store.Clear()
	if _, err := reader.Read(context.Background(), store, id, false, 0, false); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("disk cache resurrected a cleared body", err)
	}
	if reader.state != nil {
		t.Fatal("cleared body kept its cache")
	}
	if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("clear left its reader's decoded file", err)
	}
}

func TestBodyReaderQueuedReadCanCancelAndReaderBindsOneSource(t *testing.T) {
	store, request := bodyTestStore(t)
	id := store.begin(Metadata{}, request, -1)
	store.appendBody(id, false, []byte("body"))
	reader := NewBodyReader()
	defer reader.Close()
	<-reader.gate
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := reader.Read(ctx, store, id, false, 0, false)
		result <- err
	}()
	cancel()
	select {
	case err := <-result:
		reader.gate <- struct{}{}
		if !errors.Is(err, context.Canceled) {
			t.Fatal("queued read did not report cancellation", err)
		}
	case <-time.After(time.Second):
		reader.gate <- struct{}{}
		t.Fatal("queued body read blocked behind an uncancellable lock")
	}
	if _, err := reader.Read(context.Background(), store, id, false, 0, false); err != nil {
		t.Fatal(err)
	}
	for _, selection := range []struct {
		id            string
		response, raw bool
	}{{id, true, false}, {id, false, true}, {store.begin(Metadata{}, request, -1), false, false}} {
		if _, err := reader.Read(context.Background(), store, selection.id, selection.response, 0, selection.raw); err == nil {
			t.Fatal("reader accepted a different body or byte source")
		}
	}
	if _, err := reader.Read(context.Background(), store, id, false, math.MaxInt64, false); err == nil {
		t.Fatal("overflowing page offset was accepted")
	}
	reader.Close()
	if _, err := reader.Read(context.Background(), store, id, false, 0, false); !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed body task still read pages", err)
	}
}

func TestDerivedTextReaderRejectsInvalidAndUnknownCharsets(t *testing.T) {
	for _, fixture := range []struct {
		charset string
		data    []byte
	}{{"utf-8", []byte{'a', 0xff}}, {"utf-16le", []byte{0x00, 0xd8}}, {"gbk", []byte{0xff}}, {"x-missing-charset", []byte("valid ASCII")}} {
		charset := lookupBodyCharset(fixture.charset)
		if _, err := io.ReadAll(newBodyTextReader(context.Background(), charset, bytes.NewReader(fixture.data))); err == nil {
			t.Fatal("derived text silently replaced invalid source bytes", fixture.charset)
		}
	}
	for _, label := range []string{"utf-8", "utf-16le", "gb18030"} {
		charset := lookupBodyCharset(label)
		literal, err := charset.codec.NewEncoder().Bytes([]byte("literal \ufffd is text"))
		if err != nil {
			t.Fatal(err)
		}
		text, err := io.ReadAll(newBodyTextReader(context.Background(), charset, bytes.NewReader(literal)))
		if err != nil || string(text) != "literal \ufffd is text" {
			t.Fatal("a real replacement-codepoint character was rejected", label, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := io.ReadAll(newBodyTextReader(ctx, lookupBodyCharset("gbk"), bytes.NewReader([]byte{0xd6, 0xd0}))); !errors.Is(err, context.Canceled) {
		t.Fatal("derived charset reader ignored cancellation", err)
	}
}

func TestBodyCharsetsKeepStrictASCIIAndLatin1Declarations(t *testing.T) {
	for _, label := range []string{"ascii", "US-ASCII", "ANSI_X3.4-1968"} {
		message := Message{Headers: []Header{{"Content-Type", "text/plain; charset=" + label}}, CapturedBytes: 2, BodyComplete: true}
		data := []byte{'a', 0x80}
		page, err := readBodyPage(message, func() io.Reader { return bytes.NewReader(data) }, 0, false)
		if err != nil || page.Charset != "us-ascii" || page.Encoding != "base64" || page.DisplayText != nil || !bytes.Equal(bodyPageBytesForTest(t, page), data) {
			t.Fatal("ASCII declaration accepted a high-bit byte", label, err)
		}
		charset := detectBodyCharset(message, data)
		if _, err := io.ReadAll(newBodyTextReader(context.Background(), charset, strings.NewReader("{\"value\":\"é\"}"))); err == nil {
			t.Fatal("complete ASCII JSON silently accepted non-ASCII UTF-8", label)
		}
	}
	for _, fixture := range []struct{ label, expected, canonical string }{
		{"iso-8859-1", "\u0080", "iso-8859-1"}, {"Latin1", "\u0080", "iso-8859-1"}, {"windows-1252", "€", "windows-1252"},
	} {
		message := Message{Headers: []Header{{"Content-Type", "text/plain; charset=" + fixture.label}}, CapturedBytes: 1, BodyComplete: true}
		page, err := readBodyPage(message, func() io.Reader { return bytes.NewReader([]byte{0x80}) }, 0, false)
		if err != nil || page.Charset != fixture.canonical || page.DisplayText == nil || *page.DisplayText != fixture.expected || !bytes.Equal(bodyPageBytesForTest(t, page), []byte{0x80}) {
			t.Fatal("Latin-1 and Windows-1252 lost their distinct 0x80 semantics", fixture.label, err)
		}
		charset := detectBodyCharset(message, []byte{0x80})
		text, err := io.ReadAll(newBodyTextReader(context.Background(), charset, bytes.NewReader([]byte{'"', 0x80, '"'})))
		if err != nil || string(text) != "\""+fixture.expected+"\"" {
			t.Fatal("complete derived text changed the HTTP charset", fixture.label, err)
		}
	}
}

func TestNonUTF8BodyPagesAlwaysPublishValidatedDisplayText(t *testing.T) {
	data := bytes.Repeat([]byte{'a'}, BodyPageSize+7)
	for _, label := range []string{"gbk", "gb18030", "big5", "windows-1252", "shift_jis", "euc-jp", "euc-kr", "iso-8859-1", "us-ascii"} {
		t.Run(label, func(t *testing.T) {
			message := Message{Headers: []Header{{"Content-Type", "text/plain; charset=" + label}}, CapturedBytes: int64(len(data)), BodyComplete: true}
			for offset := int64(0); ; {
				page, err := readBodyPage(message, func() io.Reader { return bytes.NewReader(data) }, offset, false)
				if err != nil || page.Encoding != "text" || page.DisplayText == nil || *page.DisplayText != page.Text || page.Error != "" {
					t.Fatalf("valid ASCII page omitted native charset display text: page=%+v error=%v", page, err)
				}
				if !bytes.Equal(bodyPageBytesForTest(t, page), data[offset:page.NextOffset]) {
					t.Fatal("publishing display text changed original bytes or their offsets")
				}
				if !page.HasMore {
					break
				}
				if page.NextOffset <= offset {
					t.Fatal("body pages did not advance")
				}
				offset = page.NextOffset
			}
			raw, err := readBodyPage(message, func() io.Reader { return bytes.NewReader(data) }, 0, true)
			if err != nil || raw.DisplayText != nil || raw.Encoding != "base64" {
				t.Fatal("raw byte mode acquired a charset display contract", err)
			}
		})
	}
	utf8Page, err := readBodyPage(Message{CapturedBytes: int64(len(data))}, func() io.Reader { return bytes.NewReader(data) }, 0, false)
	if err != nil || utf8Page.DisplayText != nil || utf8Page.Text != string(data[:BodyPageSize]) {
		t.Fatal("identical UTF-8 text should keep its existing compact bridge representation", err)
	}
}
