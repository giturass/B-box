package httpdebug

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func bodyViewRecord(t *testing.T, wire []byte, headers http.Header, limit int64, complete bool, trailers ...http.Header) (*Store, string) {
	t.Helper()
	saved, request := bodyTestStore(t)
	id := saved.begin(Metadata{}, request, limit)
	response := &http.Response{StatusCode: http.StatusOK, Proto: "HTTP/2.0", Header: headers,
		Body: io.NopCloser(bytes.NewReader(wire)), ContentLength: -1}
	saved.response(id, response)
	saved.appendBody(id, true, wire)
	if complete {
		saved.bodyComplete(id, true)
	}
	for _, trailer := range trailers {
		saved.trailers(id, true, trailer)
	}
	saved.finish(id)
	return saved, id
}

func assertBodyViewScratchReleased(t *testing.T, saved *Store) {
	t.Helper()
	entries, err := os.ReadDir(saved.directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "body-view-") {
			t.Fatalf("body view retained scratch file %s", entry.Name())
		}
	}
}

func readBodyViewTextPages(t *testing.T, text []byte) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "derived.txt")
	if err := os.WriteFile(path, text, 0o600); err != nil {
		t.Fatal(err)
	}
	var recovered bytes.Buffer
	var offset int64
	for number := 0; ; number++ {
		page, err := ReadBodyViewPage(context.Background(), path, offset)
		if err != nil || page.Source != "view" || page.Encoding != "text" || page.Offset != offset ||
			page.Message != nil || page.Truncated || page.Error != "" || !utf8.ValidString(page.Text) ||
			page.NextOffset-offset != int64(len(page.Text)) || len(page.Text) > BodyPageSize {
			t.Fatalf("invalid derived text page: %+v error=%v", page, err)
		}
		recovered.WriteString(page.Text)
		if !page.HasMore {
			break
		}
		if page.NextOffset <= offset || number > len(text)/BodyPageSize+2 {
			t.Fatal("derived body paging made no progress")
		}
		offset = page.NextOffset
	}
	return recovered.Bytes()
}

func TestLargeJSONBodyViewPreservesEveryLiteralAndAllPages(t *testing.T) {
	row := `{"number":18446744073709551615,"number":1.2300e+007,"number":-0.000e+999,"escape":"\u4E2D\/\\\"\n\t","real":"中文😀"}`
	body := []byte("[" + strings.Repeat(row+",", 1000) + row + "]")
	var want bytes.Buffer
	if err := json.Indent(&want, body, "", "  "); err != nil {
		t.Fatal("invalid JSON fixture:", err)
	}
	if len(body) <= BodyPageSize || want.Len() <= 65536 {
		t.Fatal("JSON fixture does not cross the former page and display limits")
	}
	for _, coding := range []string{"identity", "gzip", "deflate", "raw-deflate", "br", "zstd", "gzip,br,zstd"} {
		t.Run(coding, func(t *testing.T) {
			headers := http.Header{"Content-Type": {"application/json; charset=utf-8"}}
			wire := body
			if coding != "identity" {
				layers := strings.Split(coding, ",")
				wire = encodedBody(t, body, layers...)
				for _, layer := range layers {
					headers.Add("Content-Encoding", strings.ReplaceAll(layer, "raw-", ""))
				}
			}
			saved, id := bodyViewRecord(t, wire, headers, -1, true)
			before := saved.Page(0, "").StoredBytes
			var out bytes.Buffer
			message, err := saved.WriteBodyView(context.Background(), &out, id, true, "json")
			if err != nil || !bytes.Equal(out.Bytes(), want.Bytes()) || message.CapturedBytes != int64(len(wire)) ||
				message.BodyBytes != int64(len(wire)) || !message.BodyComplete || message.BodyTruncated {
				t.Fatalf("JSON view changed literals, keys, escapes or retained size: bytes=%d error=%v", out.Len(), err)
			}
			if !bytes.Equal(readBodyViewTextPages(t, out.Bytes()), want.Bytes()) {
				t.Fatal("reading the complete JSON view lost UTF-8 bytes between pages")
			}
			retained, err := os.ReadFile(filepath.Join(saved.directory, id+".response"))
			if err != nil || !bytes.Equal(retained, wire) || saved.Page(0, "").StoredBytes != before {
				t.Fatal("derived JSON view changed the captured representation", err)
			}
			assertBodyViewScratchReleased(t, saved)
		})
	}
}

func TestLargeJSONBodyViewDecodesDeclaredCharsetsBeforeFormatting(t *testing.T) {
	const count = 6000
	for _, fixture := range []struct {
		name, charset, text string
		encoded             []byte
		utf16, littleEndian bool
	}{
		{"gbk", "gbk", "中文", []byte{0xd6, 0xd0, 0xce, 0xc4}, false, false},
		{"gb18030", "gb18030", "😀中文", []byte{0x94, 0x39, 0xfc, 0x36, 0xd6, 0xd0, 0xce, 0xc4}, false, false},
		{"big5", "big5", "中文", []byte{0xa4, 0xa4, 0xa4, 0xe5}, false, false},
		{"windows-1252", "windows-1252", "€ café", []byte{0x80, ' ', 'c', 'a', 'f', 0xe9}, false, false},
		{"utf-16le BOM", "utf-16", "中文😀", nil, true, true},
		{"utf-16be BOM", "utf-16", "中文😀", nil, true, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			text := `{"n":18446744073709551615,"value":"` + strings.Repeat(fixture.text, count) + `"}`
			wire := append([]byte(`{"n":18446744073709551615,"value":"`), bytes.Repeat(fixture.encoded, count)...)
			wire = append(wire, []byte(`"}`)...)
			if fixture.utf16 {
				wire = utf16BodyForTest(text, fixture.littleEndian, true)
			}
			wire = encodedBody(t, wire, "gzip")
			var want bytes.Buffer
			if err := json.Indent(&want, []byte(text), "", "  "); err != nil || want.Len() <= BodyPageSize {
				t.Fatal("charset JSON fixture does not span multiple native pages", err)
			}
			saved, id := bodyViewRecord(t, wire, http.Header{"Content-Type": {"application/json; charset=" + fixture.charset}, "Content-Encoding": {"gzip"}}, -1, true)
			var out bytes.Buffer
			if _, err := saved.WriteBodyView(context.Background(), &out, id, true, "json"); err != nil || !bytes.Equal(out.Bytes(), want.Bytes()) {
				t.Fatal("complete formatted JSON changed numbers or decoded text", err)
			}
			if !bytes.Equal(readBodyViewTextPages(t, out.Bytes()), want.Bytes()) {
				t.Fatal("complete charset JSON view lost text between pages")
			}
			assertBodyViewScratchReleased(t, saved)
		})
	}
}

func TestJSONBodyViewRejectsMalformedGrammarAndInvalidUTF8(t *testing.T) {
	for _, body := range []string{
		"", " ", "{", "[1", `{"a" 1}`, `{"a":}`, `{"a":1,}`, `[1,]`, `[1 2]`,
		`{"a":1]`, `{"a":01}`, `[-01]`, `[+1]`, `[.1]`, `[1.]`, `[1e]`, `[1e+]`,
		`[--1]`, `truefalse`, `null null`, `"\x"`, `"\u123g"`, `"unterminated`, "\"line\nfeed\"", "\"\xff\"",
	} {
		t.Run(strconv.Quote(body), func(t *testing.T) {
			if utf8.ValidString(body) && json.Valid([]byte(body)) {
				t.Fatal("malformed JSON fixture was valid")
			}
			saved, id := bodyViewRecord(t, []byte(body), http.Header{"Content-Type": {"application/json"}}, -1, true)
			if _, err := saved.WriteBodyView(context.Background(), io.Discard, id, true, "json"); err == nil {
				t.Fatal("malformed JSON was published as a formatted view")
			}
			assertBodyViewScratchReleased(t, saved)
		})
	}
}

func viewTestVarint(dst []byte, number, value uint64) []byte {
	dst = binary.AppendUvarint(dst, number<<3)
	return binary.AppendUvarint(dst, value)
}

func viewTestBytes(dst []byte, number uint64, value []byte) []byte {
	dst = binary.AppendUvarint(dst, number<<3|2)
	dst = binary.AppendUvarint(dst, uint64(len(value)))
	return append(dst, value...)
}

func TestLargeProtobufBodyViewPreservesCrossPageFieldsAndText(t *testing.T) {
	padding := strings.Repeat("p", BodyPageSize-5)
	wire := viewTestBytes(nil, 1, []byte(padding))
	if len(wire)+1 != BodyPageSize-1 {
		t.Fatal("uint64 fixture does not cross a native page boundary")
	}
	wire = viewTestVarint(wire, 2, ^uint64(0))
	longText := strings.Repeat("中文😀 \"quoted\" \\ line\n\t", 4000)
	wire = viewTestBytes(wire, 3, []byte(longText))
	wire = viewTestVarint(wire, 2, 42)
	wire = viewTestVarint(wire, 2, 0)
	wire = binary.AppendUvarint(wire, 4<<3|1)
	wire = binary.LittleEndian.AppendUint64(wire, 0xfedcba9876543210)
	wire = binary.AppendUvarint(wire, 5<<3|5)
	wire = binary.LittleEndian.AppendUint32(wire, 0x12345678)
	nested := viewTestVarint(nil, 1, 17)
	nested = viewTestBytes(nested, 2, []byte("nested 中文"))
	wire = viewTestBytes(wire, 6, nested)
	want := "1: " + strconv.Quote(padding) + "\n2: 18446744073709551615\n3: " + strconv.Quote(longText) +
		"\n2: 42\n2: 0\n4: 0xfedcba9876543210\n5: 0x12345678\n6 {\n  1: 17\n  2: \"nested 中文\"\n}\n"
	saved, id := bodyViewRecord(t, wire, http.Header{"Content-Type": {"application/x-protobuf"}}, -1, true)
	var out bytes.Buffer
	if _, err := saved.WriteBodyView(context.Background(), &out, id, true, "protobuf"); err != nil || out.String() != want {
		t.Fatalf("protobuf fields or long text were shortened or changed: bytes=%d want=%d error=%v", out.Len(), len(want), err)
	}
	if !bytes.Equal(readBodyViewTextPages(t, out.Bytes()), []byte(want)) {
		t.Fatal("protobuf view lost Chinese text or escaped field data between pages")
	}
	assertBodyViewScratchReleased(t, saved)
}

func TestProtobufBodyViewRetainsAllRepeatedFieldsBeyondFormerDisplayBudgets(t *testing.T) {
	const fields = 15000
	wire := bytes.Repeat(viewTestVarint(nil, 1, 0), fields)
	want := strings.Repeat("1: 0\n", fields)
	saved, id := bodyViewRecord(t, wire, http.Header{"Content-Type": {"application/protobuf"}}, -1, true)
	var out bytes.Buffer
	if _, err := saved.WriteBodyView(context.Background(), &out, id, true, "protobuf"); err != nil || out.String() != want {
		t.Fatalf("complete protobuf view lost repeated fields: bytes=%d want=%d error=%v", out.Len(), len(want), err)
	}
	assertBodyViewScratchReleased(t, saved)
}

func viewTestGRPCWebChunks(wire []byte) []byte {
	var text strings.Builder
	start := 0
	for _, end := range []int{2, 7, BodyPageSize - 1, len(wire) - 2, len(wire)} {
		if end <= start || end > len(wire) {
			continue
		}
		text.WriteString(base64.StdEncoding.EncodeToString(wire[start:end]))
		text.WriteString("\r\n \t")
		start = end
	}
	return []byte(text.String())
}

func TestLargeGRPCBodyViewDecodesHTTPAndMessageCompressionAndWebTextChunks(t *testing.T) {
	longText := strings.Repeat("rpc 中文😀", 5000)
	proto := viewTestBytes(nil, 1, []byte(longText))
	proto = viewTestVarint(proto, 2, ^uint64(0))
	for _, coding := range []string{"gzip", "deflate", "raw-deflate"} {
		for _, mime := range []string{"application/grpc+proto", "application/grpc-web+proto", "application/grpc-web-text+proto"} {
			for _, httpCoding := range []string{"identity", "gzip,br"} {
				t.Run(fmt.Sprintf("%s/%s/http=%s", mime, coding, httpCoding), func(t *testing.T) {
					wire := append(grpcTestFrame(0, proto), grpcTestFrame(1, encodedBody(t, proto, coding))...)
					if strings.Contains(mime, "grpc-web") {
						wire = append(wire, grpcTestFrame(0x80, []byte("grpc-status: 0\r\nx-web-end: kept\r\n"))...)
					}
					if strings.Contains(mime, "grpc-web-text") {
						wire = viewTestGRPCWebChunks(wire)
					}
					headers := http.Header{"Content-Type": {mime}, "Grpc-Encoding": {strings.ReplaceAll(coding, "raw-", "")}}
					if httpCoding != "identity" {
						layers := strings.Split(httpCoding, ",")
						wire = encodedBody(t, wire, layers...)
						for _, layer := range layers {
							headers.Add("Content-Encoding", layer)
						}
					}
					saved, id := bodyViewRecord(t, wire, headers, -1, true, http.Header{"Grpc-Status": {"0"}, "X-Http-End": {"kept"}})
					var out bytes.Buffer
					if _, err := saved.WriteBodyView(context.Background(), &out, id, true, "grpc"); err != nil {
						t.Fatal(err)
					}
					text := out.String()
					if strings.Count(text, "1: "+strconv.Quote(longText)) != 2 || strings.Count(text, "2: 18446744073709551615") != 2 ||
						!strings.Contains(text, "# 1\n") || !strings.Contains(text, "# 2 [compressed:") || strings.Contains(text, "decoding failed") {
						t.Fatalf("gRPC full messages were not decoded: bytes=%d", out.Len())
					}
					if strings.Contains(mime, "grpc-web") && (!strings.Contains(text, "# 3 [trailers]") || !strings.Contains(text, "x-web-end: kept\r\n")) {
						t.Fatal("gRPC-Web trailers were lost")
					}
					if !strings.Contains(text, "# HTTP trailers\n") || !strings.Contains(text, "Grpc-Status: 0\n") || !strings.Contains(text, "X-Http-End: kept\n") {
						t.Fatal("HTTP trailers were lost from the complete gRPC view")
					}
					if !bytes.Equal(readBodyViewTextPages(t, out.Bytes()), out.Bytes()) {
						t.Fatal("complete gRPC view lost message text between derived pages")
					}
					assertBodyViewScratchReleased(t, saved)
				})
			}
		}
	}
}

func TestBodyViewRejectsMalformedFramesEncodingsAndIncompleteSavedBodies(t *testing.T) {
	badGzip := encodedBody(t, []byte(`{"x":1}`), "gzip")
	badGzip[len(badGzip)-8] ^= 0xff
	for _, test := range []struct {
		name, format, mime, encoding string
		wire                         []byte
		limit                        int64
		complete                     bool
	}{
		{"unfinished body", "json", "application/json", "", []byte(`{"x":1}`), -1, false},
		{"truncated body", "json", "application/json", "", []byte(`{"x":1}`), 3, true},
		{"invalid HTTP encoding", "json", "application/json", "future", []byte(`{"x":1}`), -1, true},
		{"corrupt HTTP gzip trailer", "json", "application/json", "gzip", badGzip, -1, true},
		{"zero protobuf field number", "protobuf", "application/protobuf", "", []byte{0, 1}, -1, true},
		{"incomplete protobuf varint", "protobuf", "application/protobuf", "", []byte{8, 0x80}, -1, true},
		{"overflow protobuf uint64", "protobuf", "application/protobuf", "", append([]byte{8}, bytes.Repeat([]byte{0xff}, 10)...), -1, true},
		{"incomplete protobuf string", "protobuf", "application/protobuf", "", []byte{10, 3, 'x'}, -1, true},
		{"incomplete protobuf fixed32", "protobuf", "application/protobuf", "", []byte{13, 0, 1}, -1, true},
		{"short grpc frame", "grpc", "application/grpc+proto", "", []byte{0, 0, 0, 0}, -1, true},
		{"invalid grpc flag", "grpc", "application/grpc+proto", "", []byte{2, 0, 0, 0, 0}, -1, true},
		{"incorrect grpc length", "grpc", "application/grpc+proto", "", []byte{0, 0, 0, 0, 2, 8}, -1, true},
		{"invalid web text base64", "grpc", "application/grpc-web-text+proto", "", []byte("@@=="), -1, true},
		{"noncanonical web text pad bits", "grpc", "application/grpc-web-text+proto", "", []byte("AB=="), -1, true},
		{"incomplete web text quartet", "grpc", "application/grpc-web-text+proto", "", []byte("AAA"), -1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			saved, id := bodyViewRecord(t, test.wire, http.Header{"Content-Type": {test.mime}, "Content-Encoding": {test.encoding}}, test.limit, test.complete)
			if _, err := saved.WriteBodyView(context.Background(), io.Discard, id, true, test.format); err == nil {
				t.Fatal("malformed or incomplete body was published as a complete view")
			}
			assertBodyViewScratchReleased(t, saved)
		})
	}
}

func TestEmptyGRPCMessageHasACompleteDefaultProtobufView(t *testing.T) {
	saved, id := bodyViewRecord(t, grpcTestFrame(0, nil), http.Header{"Content-Type": {"application/grpc+proto"}}, -1, true)
	var out bytes.Buffer
	if _, err := saved.WriteBodyView(context.Background(), &out, id, true, "grpc"); err != nil || !strings.Contains(out.String(), "# 1\n") {
		t.Fatal("empty default gRPC message was rejected", err)
	}
	assertBodyViewScratchReleased(t, saved)
}

func TestExplicitGRPCViewSupportsMissingContentType(t *testing.T) {
	body := grpcTestFrame(0, viewTestBytes(nil, 1, []byte("完整正文 中文😀")))
	saved, id := bodyViewRecord(t, body, http.Header{}, -1, true)
	var out bytes.Buffer
	if _, err := saved.WriteBodyView(context.Background(), &out, id, true, "grpc"); err != nil ||
		out.String() != "# 1\n1: \"完整正文 中文😀\"\n\n" {
		t.Fatal("an explicitly selected gRPC frame view needed a Content-Type header", err)
	}
	assertBodyViewScratchReleased(t, saved)
}

func TestGRPCJSONViewUsesDeclaredFormatWhenPayloadAlsoLooksLikeProtobuf(t *testing.T) {
	// The opening quote is protobuf tag 4/wire 2 and F is a length of 70.
	// This valid JSON string also passes a schema-free protobuf wire scan.
	value := `"F` + strings.Repeat("a", 69) + `"`
	if !json.Valid([]byte(value)) || !validViewProtobuf(context.Background(), bytes.NewReader([]byte(value)), 0, int64(len(value)), 0) {
		t.Fatal("gRPC JSON fixture does not exercise ambiguous wire inference")
	}
	for _, mime := range []string{"application/grpc+json", "application/grpc-web+json", "application/grpc-web-text+json"} {
		t.Run(mime, func(t *testing.T) {
			wire := grpcTestFrame(0, []byte(value))
			if strings.Contains(mime, "grpc-web-text") {
				wire = viewTestGRPCWebChunks(wire)
			}
			saved, id := bodyViewRecord(t, wire, http.Header{"Content-Type": {mime}}, -1, true)
			var out bytes.Buffer
			if _, err := saved.WriteBodyView(context.Background(), &out, id, true, "grpc"); err != nil || out.String() != "# 1\n"+value+"\n" {
				t.Fatal("gRPC JSON payload was shown as guessed protobuf fields", out.String(), err)
			}
			assertBodyViewScratchReleased(t, saved)
		})
	}
}

func TestLargeGRPCJSONBodyViewPreservesValuesAcrossFramesAndPages(t *testing.T) {
	row := `{"n":18446744073709551615,"n":-0.000e+999,"value":"中文😀\n\t"}`
	message := []byte("[" + strings.Repeat(row+",", 2000) + row + "]")
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, message, "", "  "); err != nil || formatted.Len() <= 65536 {
		t.Fatal("gRPC JSON fixture does not cross former full-view limits", err)
	}
	want := "# 1\n" + formatted.String() + "\n# 2 [compressed: \"gzip\"]\n" + formatted.String() + "\n"
	for _, mime := range []string{"application/grpc+json", "application/grpc-web+json", "application/grpc-web-text+json"} {
		t.Run(mime, func(t *testing.T) {
			wire := append(grpcTestFrame(0, message), grpcTestFrame(1, encodedBody(t, message, "gzip"))...)
			if strings.Contains(mime, "grpc-web-text") {
				wire = viewTestGRPCWebChunks(wire)
			}
			saved, id := bodyViewRecord(t, wire, http.Header{"Content-Type": {mime}, "Grpc-Encoding": {"gzip"}}, -1, true)
			var out bytes.Buffer
			if _, err := saved.WriteBodyView(context.Background(), &out, id, true, "grpc"); err != nil || out.String() != want {
				t.Fatal("complete gRPC JSON view lost literals or complete messages", err)
			}
			if !bytes.Equal(readBodyViewTextPages(t, out.Bytes()), []byte(want)) {
				t.Fatal("complete gRPC JSON view lost text between pages")
			}
			assertBodyViewScratchReleased(t, saved)
		})
	}
}

func TestLargeImageBodyViewWritesTheEntireDecodedImage(t *testing.T) {
	pixels := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	state := uint32(17)
	for index := range pixels.Pix {
		state = state*1664525 + 1013904223
		pixels.Pix[index] = byte(state >> 24)
		if index%4 == 3 {
			pixels.Pix[index] = 255
		}
	}
	var imageBody bytes.Buffer
	if err := png.Encode(&imageBody, pixels); err != nil || imageBody.Len() <= 64*1024 {
		t.Fatal("large image fixture was not generated", err)
	}
	wire := encodedBody(t, imageBody.Bytes(), "gzip")
	saved, id := bodyViewRecord(t, wire, http.Header{"Content-Type": {"image/png"}, "Content-Encoding": {"gzip"}}, -1, true)
	var out bytes.Buffer
	if _, err := saved.WriteBodyView(context.Background(), &out, id, true, "image"); err != nil || !bytes.Equal(out.Bytes(), imageBody.Bytes()) {
		t.Fatalf("complete image was limited to the first native page: bytes=%d error=%v", out.Len(), err)
	}
	config, err := png.DecodeConfig(bytes.NewReader(out.Bytes()))
	if err != nil || config.Width != 256 || config.Height != 256 {
		t.Fatal("derived image is not the original complete image", err)
	}
	assertBodyViewScratchReleased(t, saved)
}

type cancellingBodyViewWriter struct {
	bytes.Buffer
	cancel context.CancelFunc
}

func (w *cancellingBodyViewWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.cancel()
	return n, err
}

type clearingBodyViewWriter struct {
	bytes.Buffer
	saved *Store
}

func (w *clearingBodyViewWriter) Write(p []byte) (int, error) {
	n, err := w.Buffer.Write(p)
	w.saved.Clear()
	return n, err
}

func TestBodyViewRemovedDuringFinalWriteDoesNotPublishSuccess(t *testing.T) {
	for _, test := range []struct {
		format, mime, encoding string
		body                   []byte
	}{
		{"json", "application/json", "", []byte(`{"x":"中文"}`)},
		{"protobuf", "application/protobuf", "", viewTestBytes(nil, 1, []byte("中文"))},
		{"grpc", "application/grpc+proto", "", grpcTestFrame(0, viewTestBytes(nil, 1, []byte("中文")))},
		{"image", "image/png", "gzip", []byte("small image payload")},
	} {
		t.Run(test.format, func(t *testing.T) {
			wire := test.body
			if test.encoding != "" {
				wire = encodedBody(t, wire, test.encoding)
			}
			saved, id := bodyViewRecord(t, wire, http.Header{"Content-Type": {test.mime}, "Content-Encoding": {test.encoding}}, -1, true)
			out := &clearingBodyViewWriter{saved: saved}
			if _, err := saved.WriteBodyView(context.Background(), out, id, true, test.format); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("removed captured body was published from a pinned input file: error=%v", err)
			}
			if saved.Page(0, "").Total != 0 {
				t.Fatal("deriving a view recreated the removed captured body")
			}
			assertBodyViewScratchReleased(t, saved)
		})
	}
}

func TestCheckBodyViewRejectsRemovedRecordAndCancelledContext(t *testing.T) {
	saved, id := bodyViewRecord(t, []byte(`{"x":"中文"}`), http.Header{"Content-Type": {"application/json"}}, -1, true)
	if err := saved.CheckBodyView(context.Background(), id); err != nil {
		t.Fatal("retained record cannot keep its derived view open", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := saved.CheckBodyView(ctx, id); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled body view remained readable", err)
	}
	saved.Clear()
	if err := saved.CheckBodyView(context.Background(), id); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("derived body view remained readable after its record was removed", err)
	}
	var out bytes.Buffer
	if _, err := saved.WriteBodyView(context.Background(), &out, id, true, "json"); !errors.Is(err, os.ErrNotExist) || out.Len() != 0 {
		t.Fatal("removed body was re-derived or written", err)
	}
	assertBodyViewScratchReleased(t, saved)
}

func TestBodyViewCancellationDuringFinalWriteDoesNotPublishSuccess(t *testing.T) {
	for _, test := range []struct {
		format, mime, encoding string
		body                   []byte
	}{
		{"json", "application/json", "", []byte(`{"x":"中文"}`)},
		{"protobuf", "application/protobuf", "", viewTestBytes(nil, 1, []byte("中文"))},
		{"grpc", "application/grpc+proto", "", grpcTestFrame(0, viewTestBytes(nil, 1, []byte("中文")))},
		{"image", "image/png", "gzip", []byte("small image payload")},
	} {
		t.Run(test.format, func(t *testing.T) {
			wire := test.body
			if test.encoding != "" {
				wire = encodedBody(t, wire, test.encoding)
			}
			saved, id := bodyViewRecord(t, wire, http.Header{"Content-Type": {test.mime}, "Content-Encoding": {test.encoding}}, -1, true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			out := &cancellingBodyViewWriter{cancel: cancel}
			if _, err := saved.WriteBodyView(ctx, out, id, true, test.format); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled final output was published: error=%v", err)
			}
			assertBodyViewScratchReleased(t, saved)
		})
	}
}

func TestBodyViewRejectsCancellationBeforeReadingAndReleasesScratch(t *testing.T) {
	saved, id := bodyViewRecord(t, []byte(`{"x":"中文"}`), http.Header{"Content-Type": {"application/json"}}, -1, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	if _, err := saved.WriteBodyView(ctx, &out, id, true, "json"); !errors.Is(err, context.Canceled) || out.Len() != 0 {
		t.Fatal("body view read or wrote after cancellation", err)
	}
	assertBodyViewScratchReleased(t, saved)
}

type cancellingViewSource struct {
	io.Reader
	cancel context.CancelFunc
	reads  int
}

func (r *cancellingViewSource) Read(data []byte) (int, error) {
	r.reads++
	n, err := r.Reader.Read(data)
	r.cancel()
	return n, err
}

type cancellingViewSourceAt struct {
	io.ReaderAt
	cancel context.CancelFunc
}

func (r *cancellingViewSourceAt) ReadAt(data []byte, offset int64) (int, error) {
	n, err := r.ReaderAt.ReadAt(data, offset)
	r.cancel()
	return n, err
}

func TestBodyViewDecodersStopWhenTheUnderlyingReadCancels(t *testing.T) {
	for _, coding := range []string{"identity", "gzip", "deflate", "raw-deflate", "br", "zstd", "gzip,br,zstd"} {
		t.Run(coding, func(t *testing.T) {
			layers := strings.Split(coding, ",")
			wire := []byte(strings.Repeat("中文😀 decoder payload", 3000))
			if coding != "identity" {
				wire = encodedBody(t, wire, layers...)
			}
			for index := range layers {
				layers[index] = strings.ReplaceAll(layers[index], "raw-", "")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			source := &cancellingViewSource{Reader: bytes.NewReader(wire), cancel: cancel}
			decoder, err := newBodyContentReader(&bodyReadContext{ctx: ctx}, source, layers)
			if err == nil {
				defer decoder.Close()
				_, err = io.ReadAll(decoder)
			}
			if !errors.Is(err, context.Canceled) || source.reads != 1 {
				t.Fatal("compression read or buffered layer continued after cancellation", source.reads, err)
			}
		})
	}
}

func TestBodyViewParsersStopWhenTheUnderlyingReadCancels(t *testing.T) {
	t.Run("JSON", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		source := &cancellingViewSource{Reader: strings.NewReader(`{"text":"中文😀"}`), cancel: cancel}
		if err := writeBodyJSON(ctx, io.Discard, source); !errors.Is(err, context.Canceled) || source.reads != 1 {
			t.Fatal("JSON formatted source bytes after their read was cancelled", source.reads, err)
		}
	})
	t.Run("protobuf", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		body := viewTestBytes(nil, 1, []byte("中文😀"))
		source := &cancellingViewSourceAt{ReaderAt: bytes.NewReader(body), cancel: cancel}
		if err := writeBodyProtobuf(ctx, io.Discard, source, 0, int64(len(body)), 0); !errors.Is(err, context.Canceled) {
			t.Fatal("protobuf formatted source bytes after their read was cancelled", err)
		}
	})
	t.Run("grpc-web text", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		body := grpcTestFrame(0, viewTestBytes(nil, 1, []byte("中文😀")))
		source := &cancellingViewSource{Reader: bytes.NewReader(viewTestGRPCWebChunks(body)), cancel: cancel}
		if _, err := decodeBodyGRPCWeb(ctx, io.Discard, source); !errors.Is(err, context.Canceled) || source.reads != 1 {
			t.Fatal("grpc-web text decoded source bytes after their read was cancelled", source.reads, err)
		}
	})
	for _, charset := range []string{"utf-8", "utf-16le", "gbk"} {
		t.Run(charset, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			codec := lookupBodyCharset(charset)
			body, err := codec.codec.NewEncoder().Bytes([]byte("中文"))
			if err != nil {
				t.Fatal(err)
			}
			source := &cancellingViewSource{Reader: bytes.NewReader(body), cancel: cancel}
			if _, err := io.ReadAll(newBodyTextReader(ctx, codec, source)); !errors.Is(err, context.Canceled) || source.reads != 1 {
				t.Fatal("charset decoder continued after its source read was cancelled", source.reads, err)
			}
		})
	}
}

func TestDerivedBodyViewPageOffsetsKeepChineseAndEmojiIntact(t *testing.T) {
	text := []byte(strings.Repeat("a", BodyPageSize-1) + "中😀" + strings.Repeat("中文😀", 12000) + "end")
	if !bytes.Equal(readBodyViewTextPages(t, text), text) {
		t.Fatal("derived body pages lost or changed UTF-8 bytes")
	}
	path := filepath.Join(t.TempDir(), "derived.txt")
	if err := os.WriteFile(path, text, 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := ReadBodyViewPage(context.Background(), path, 0)
	if err != nil || first.NextOffset != BodyPageSize-1 || !first.HasMore {
		t.Fatal("derived body page split the first Chinese character", err)
	}
	for _, offset := range []int64{-1, int64(len(text)) + 1, 1<<63 - 1} {
		if _, err := ReadBodyViewPage(context.Background(), path, offset); err == nil {
			t.Fatalf("invalid derived body offset %d was accepted", offset)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ReadBodyViewPage(ctx, path, 0); !errors.Is(err, context.Canceled) {
		t.Fatal("derived page read ignored cancellation", err)
	}
}
