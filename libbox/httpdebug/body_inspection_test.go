package httpdebug

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// Protocol/storage tests may explicitly read the full captured fixture. The
// production Detail API carries only bounded pages and never fills these slices.
type detailWithBodies struct {
	*Detail
	RequestBody, ResponseBody []byte
}

func (s *Store) detailWithBodies(id string) (*detailWithBodies, error) {
	snapshot, err := s.snapshot(id)
	if err != nil {
		return nil, err
	}
	defer snapshot.close()
	detail, err := snapshot.detail()
	if err != nil {
		return nil, err
	}
	request, err := io.ReadAll(snapshot.reader(false))
	if err != nil {
		return nil, err
	}
	response, err := io.ReadAll(snapshot.reader(true))
	if err != nil {
		return nil, err
	}
	return &detailWithBodies{detail, request, response}, nil
}

func previewForTest(t *testing.T, message Message, wire []byte) Preview {
	t.Helper()
	message.CapturedBytes = int64(len(wire))
	page, err := readBodyPage(message, func() io.Reader { return bytes.NewReader(wire) }, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func harContentForTest(t *testing.T, message Message, wire []byte, request bool) map[string]any {
	t.Helper()
	message.CapturedBytes = int64(len(wire))
	content, err := harContent(message, func() io.Reader { return bytes.NewReader(wire) }, request)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := writeHARValue(&output, content); err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestBodyPagesPreserveUTF8AndBinaryWithoutRetainingEarlierPages(t *testing.T) {
	text := strings.Repeat("a", BodyPageSize-1) + "😀" + strings.Repeat("中文", 12000) + "end"
	for _, encoding := range []string{"", "gzip", "deflate", "br", "zstd", "gzip,br,zstd", "unsupported"} {
		t.Run(encoding, func(t *testing.T) {
			original := []byte(text)
			wire := original
			if encoding != "" && encoding != "unsupported" {
				wire = encodedBody(t, original, strings.Split(encoding, ",")...)
			}
			if encoding == "unsupported" {
				wire = append([]byte{0, 255}, wire...)
				original = wire
			}
			message := Message{Headers: []Header{{"Content-Encoding", encoding}}, BodyBytes: int64(len(wire)), CapturedBytes: int64(len(wire)), BodyComplete: true}
			var recovered bytes.Buffer
			var offset int64
			raw := false
			for pageNumber := 0; ; pageNumber++ {
				page, err := readBodyPage(message, func() io.Reader { return bytes.NewReader(wire) }, offset, raw)
				if err != nil || page.Truncated || page.Offset != offset || page.NextOffset-offset > BodyPageSize {
					t.Fatalf("invalid body page: %+v %v", page, err)
				}
				data := []byte(page.Text)
				if page.Encoding == "base64" {
					data, err = base64.StdEncoding.DecodeString(page.Text)
					if err != nil {
						t.Fatal(err)
					}
				} else if !utf8.ValidString(page.Text) {
					t.Fatal("UTF-8 character split across pages")
				}
				recovered.Write(data)
				if int64(len(data)) != page.NextOffset-offset {
					t.Fatal("page offsets do not describe the returned bytes")
				}
				if pageNumber == 0 && encoding != "unsupported" && page.NextOffset != BodyPageSize-1 {
					t.Fatal("first page split the emoji")
				}
				if !page.HasMore {
					break
				}
				if page.NextOffset <= offset {
					t.Fatal("body paging made no progress")
				}
				offset, raw = page.NextOffset, page.Source == "raw"
			}
			if !bytes.Equal(recovered.Bytes(), original) {
				t.Fatal("paging lost or duplicated body bytes")
			}
		})
	}
}

type countedBodyReader struct {
	io.Reader
	count int
}

func (r *countedBodyReader) Read(data []byte) (int, error) {
	n, err := r.Reader.Read(data)
	r.count += n
	return n, err
}

func TestBodyPreviewDoesNotReadOrDecodeTheWholeSavedBody(t *testing.T) {
	plain := strings.Repeat("0123456789", 200000)
	reader := &countedBodyReader{Reader: strings.NewReader(plain)}
	message := Message{BodyBytes: int64(len(plain)), CapturedBytes: int64(len(plain)), BodyComplete: true}
	page, err := readBodyPage(message, func() io.Reader { return reader }, 0, false)
	if err != nil || !page.HasMore || reader.count > BodyPageSize+utf8.UTFMax || len(page.Text) != BodyPageSize {
		t.Fatal("preview read beyond its page", reader.count, err)
	}
	wire := encodedBody(t, []byte(plain), "gzip")
	// Corruption after the first decoded page must not be visited by a preview.
	wire[len(wire)-1] ^= 255
	message.Headers = []Header{{"Content-Encoding", "gzip"}}
	message.BodyBytes, message.CapturedBytes = int64(len(wire)), int64(len(wire))
	page = previewForTest(t, message, wire)
	if !page.HasMore || page.Truncated || page.Error != "" || page.Text != plain[:BodyPageSize] {
		t.Fatal("preview decompressed the tail instead of just its page", page.Error)
	}
	content := harContentForTest(t, message, wire, false)
	if content["_truncated"] != true || content["_rawBody"] != base64.StdEncoding.EncodeToString(wire) {
		t.Fatal("full export hid the decode error or lost the saved bytes")
	}
	if _, err = readBodyPage(message, func() io.Reader { return bytes.NewReader(wire) }, -1, false); err == nil {
		t.Fatal("negative page offset accepted")
	}
}

// Encoding an entire HAR entry or cURL body in one allocation would also issue
// a large write. Exercise streaming writes as well as full-content equality.
type chunkedExportWriter struct {
	bytes.Buffer
}

func (w *chunkedExportWriter) Write(data []byte) (int, error) {
	if len(data) > 64*1024 {
		return 0, errors.New("export issued an unbounded body write")
	}
	return w.Buffer.Write(data)
}

func TestStreamingHARAndCurlPreserveCompleteSavedBodies(t *testing.T) {
	for _, kind := range []string{"text", "gzip", "binary"} {
		t.Run(kind, func(t *testing.T) {
			body := []byte(strings.Repeat(strings.Repeat("a", 32765)+"😀\"\\\n中文", 40))
			if kind == "binary" {
				body = bytes.Repeat([]byte{0, 255, 1, 2, 3}, 100000)
			}
			wire := body
			store, request := bodyTestStore(t)
			request.Header.Set("Content-Type", "application/octet-stream")
			if kind == "gzip" {
				wire = encodedBody(t, body, "gzip")
				request.Header.Set("Content-Encoding", "gzip")
			}
			id := store.begin(Metadata{}, request, -1)
			store.response(id, &http.Response{StatusCode: 200, Proto: "HTTP/1.1", Header: request.Header.Clone()})
			for _, response := range []bool{false, true} {
				store.appendBody(id, response, wire)
				store.bodyComplete(id, response)
			}
			store.finish(id)
			detail, err := store.Detail(id)
			if err != nil || !detail.RequestPreview.HasMore || !detail.ResponsePreview.HasMore || detail.ResponsePreview.Truncated {
				t.Fatal("full capture did not expose bounded previews", err)
			}
			var har chunkedExportWriter
			if err = store.WriteHARSelection(&har, []string{id}); err != nil {
				t.Fatal(err)
			}
			var document struct {
				Log struct{ Entries []map[string]any }
			}
			if err = json.Unmarshal(har.Bytes(), &document); err != nil {
				t.Fatal(err)
			}
			entry := document.Log.Entries[0]
			responseContent := entry["response"].(map[string]any)["content"].(map[string]any)
			postData := entry["request"].(map[string]any)["postData"].(map[string]any)
			for _, content := range []map[string]any{responseContent, postData} {
				data := []byte(content["text"].(string))
				if content["encoding"] == "base64" || content["_encoding"] == "base64" {
					data, err = base64.StdEncoding.DecodeString(string(data))
				}
				if err != nil || !bytes.Equal(data, body) || content["_truncated"] != false || content["_complete"] != true {
					t.Fatal("full HAR content differs from capture", err)
				}
				if kind == "gzip" && content["_rawBody"] != base64.StdEncoding.EncodeToString(wire) {
					t.Fatal("HAR lost original compressed data")
				}
			}
			var curl chunkedExportWriter
			if err = store.WriteCurl(&curl, id); err != nil {
				t.Fatal(err)
			}
			start := "printf '%s' '"
			end := strings.Index(curl.String(), "' | base64 -d | curl ")
			if !strings.HasPrefix(curl.String(), start) || end < len(start) {
				t.Fatal("missing cURL body pipeline")
			}
			data, err := base64.StdEncoding.DecodeString(curl.String()[len(start):end])
			if err != nil || !bytes.Equal(data, wire) {
				t.Fatal("cURL changed the captured request bytes", err)
			}
		})
	}
}

func TestBodySnapshotPinsSavedPrefixAcrossAppendAndClear(t *testing.T) {
	store, request := bodyTestStore(t)
	body := bytes.Repeat([]byte("x"), BodyPageSize+23)
	id := store.begin(Metadata{}, request, -1)
	store.appendBody(id, false, body)
	snapshot, err := store.snapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.close()
	store.appendBody(id, false, []byte("later"))
	store.Clear()
	first, err := snapshot.detail()
	if err != nil || first.Request.CapturedBytes != int64(len(body)) || !first.RequestPreview.HasMore {
		t.Fatal("snapshot changed or failed after clear", err)
	}
	last, err := readBodyPage(snapshot.Request, func() io.Reader { return snapshot.reader(false) }, first.RequestPreview.NextOffset, false)
	if err != nil || last.HasMore || first.RequestPreview.Text+last.Text != string(body) {
		t.Fatal("snapshot included late bytes or lost the cleared file", err)
	}
	if _, err = store.BodyPage(id, false, 0, false); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("new page query resurrected a cleared capture", err)
	}
}
