package httpdebug

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func encodedBody(t *testing.T, body []byte, encodings ...string) []byte {
	t.Helper()
	for _, encoding := range encodings {
		var buffer bytes.Buffer
		var writer io.WriteCloser
		var err error
		switch encoding {
		case "gzip":
			writer = gzip.NewWriter(&buffer)
		case "deflate":
			writer = zlib.NewWriter(&buffer)
		case "raw-deflate":
			writer, err = flate.NewWriter(&buffer, flate.DefaultCompression)
		case "br":
			writer = brotli.NewWriter(&buffer)
		case "zstd":
			writer, err = zstd.NewWriter(&buffer, zstd.WithEncoderConcurrency(1))
		default:
			t.Fatal("unknown test encoding")
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err = writer.Write(body); err != nil {
			t.Fatal(err)
		}
		if err = writer.Close(); err != nil {
			t.Fatal(err)
		}
		body = buffer.Bytes()
	}
	return body
}

func TestBodyEncodingsAgreeAcrossRewritingPreviewAndHAR(t *testing.T) {
	for _, encoding := range []string{"gzip", "deflate", "raw-deflate", "br", "zstd", "gzip,br", "gzip,br,zstd", "gzip,br,gzip,br"} {
		t.Run(encoding, func(t *testing.T) {
			encodings := strings.Split(encoding, ",")
			wire := encodedBody(t, []byte("old"), encodings...)
			header := make(http.Header)
			for _, part := range encodings {
				header.Add("Content-Encoding", strings.ReplaceAll(part, "raw-", ""))
			}
			program := rewriteProgram(t, `{"body_rewrite":[{"pattern":".","phase":"request","regex":"old","replacement":"new"},{"pattern":".","phase":"response","regex":"old","replacement":"new"}]}`)
			request, _ := http.NewRequest("POST", "http://example.test/", bytes.NewReader(wire))
			request.Header = header.Clone()
			if _, err := program.request(request, "test", request.URL.String(), func(string) {}); err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(request.Body)
			request.Body.Close()
			if err != nil || string(got) != "new" || request.Header.Get("Content-Encoding") != "" {
				t.Fatalf("request was not decoded and rewritten: %q %v", got, err)
			}
			response := &http.Response{StatusCode: 200, Header: header.Clone(), Body: io.NopCloser(bytes.NewReader(wire))}
			if err = program.response(request, response, "test", request.URL.String(), func(string) {}); err != nil {
				t.Fatal(err)
			}
			got, err = io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil || string(got) != "new" || response.Header.Get("Content-Encoding") != "" {
				t.Fatalf("response was not decoded and rewritten: %q %v", got, err)
			}
			headers, _ := captureHeaders(header, "")
			message := Message{Headers: headers, BodyComplete: true, BodyBytes: int64(len(wire))}
			if got := previewForTest(t, message, wire); got.Text != "old" || got.Encoding != "text" || got.Truncated {
				t.Fatalf("preview disagrees with body decoding: %+v", got)
			}
			for _, content := range []map[string]any{
				harContentForTest(t, message, wire, false),
				harContentForTest(t, message, wire, true),
			} {
				decoded, err := base64.StdEncoding.DecodeString(content["text"].(string))
				if content["_encoding"] == "" {
					decoded, err = []byte(content["text"].(string)), nil
				}
				if err != nil || string(decoded) != "old" || content["_rawBody"] != base64.StdEncoding.EncodeToString(wire) {
					t.Fatal("HAR disagrees with decoding or lost the original bytes")
				}
			}
		})
	}
}

type observedBody struct {
	io.Reader
	reads int
}

func (b *observedBody) Read(p []byte) (int, error) { b.reads++; return b.Reader.Read(p) }
func (b *observedBody) Close() error               { return nil }

func TestIneligibleBodiesAreNotReadOrSubjectToRewriteSizeLimits(t *testing.T) {
	for _, test := range []struct{ name, field, value string }{
		{"unsupported encoding", "Content-Encoding", "future-encoding"},
		{"SSE", "Content-Type", "text/event-stream; charset=utf-8"},
		{"gRPC", "Content-Type", "application/grpc+proto"},
		{"NDJSON", "Content-Type", "application/x-ndjson"},
		{"JSON sequence", "Content-Type", "application/json-seq"},
		{"upgrade", "Upgrade", "websocket"},
	} {
		t.Run(test.name, func(t *testing.T) {
			program := rewriteProgram(t, `{"max_body_bytes":4,"body_rewrite":[{"pattern":".","phase":"request","regex":"old","replacement":"new"},{"pattern":".","phase":"response","regex":"old","replacement":"new"}]}`)
			program.scripts = scriptProgram(t, `{"http":[{"pattern":".","phase":"request","requires_body":true,"source":"throw Error('ineligible request read');"},{"pattern":".","phase":"response","requires_body":true,"source":"throw Error('ineligible response read');"}]}`)
			body := &observedBody{Reader: strings.NewReader("old and larger than the limit")}
			request, _ := http.NewRequest("POST", "http://example.test/", body)
			request.Header.Set(test.field, test.value)
			var notes []string
			note := func(value string) { notes = append(notes, value) }
			if _, err := program.request(request, "test", request.URL.String(), note); err != nil {
				t.Fatal("ineligible upload failed instead of streaming:", err)
			}
			response := &http.Response{StatusCode: 200, Header: request.Header.Clone(), Body: body}
			if err := program.response(request, response, "test", request.URL.String(), note); err != nil {
				t.Fatal("ineligible response failed instead of streaming:", err)
			}
			if body.reads != 0 || request.Body != body || response.Body != body || !strings.Contains(strings.Join(notes, "\n"), "skipped") {
				t.Fatal("ineligible body was consumed, replaced or silently skipped")
			}
		})
	}
}

func TestBodyReservationsUseActualBytesAndRetainOutputLease(t *testing.T) {
	const previousBudget = 384 * 1024 * 1024
	occupied := processingBodies.acquire(previousBudget)
	if occupied == nil || processingBodies.limit != 0 {
		t.Fatal("production body bookkeeping still imposes a global quota")
	}
	defer occupied.release()
	var bodies []io.ReadCloser
	defer func() {
		for _, body := range bodies {
			body.Close()
		}
	}()
	for range 8 {
		program := rewriteProgram(t, `{"max_body_bytes":1099511627776,"body_rewrite":[{"pattern":".","phase":"request","regex":"old","replacement":"new"}]}`)
		request, _ := http.NewRequest("POST", "http://example.test/", strings.NewReader("old"))
		if _, err := program.request(request, "test", request.URL.String(), func(string) {}); err != nil {
			t.Fatal(err)
		}
		bodies = append(bodies, request.Body)
	}
	if used := processingBodies.used.Load(); used <= previousBudget || used > previousBudget+1024 {
		t.Fatalf("small retained bodies reserved their configured maximum: %d", used)
	}
	for _, body := range bodies {
		data, err := io.ReadAll(body)
		body.Close()
		body.Close()
		if err != nil || string(data) != "new" {
			t.Fatal("a small body was skipped under the former budget", err)
		}
	}
	if processingBodies.used.Load() != previousBudget {
		t.Fatal("body leases leaked or were released twice")
	}
}

func TestBodyDecodingLimitsAndTruncatedUploadsPreserveIntegrity(t *testing.T) {
	for _, encoding := range []string{"gzip", "br", "zstd", "gzip,br"} {
		wire := encodedBody(t, bytes.Repeat([]byte("x"), 8192), strings.Split(encoding, ",")...)
		for _, response := range []bool{false, true} {
			body := io.NopCloser(bytes.NewReader(wire))
			_, available, err := readRewriteBody(context.Background(), &body, encoding, 1024, response, func(string) {})
			if available || !response && !errors.Is(err, errRewriteSize) || response && err != nil {
				t.Fatalf("%s response=%v: invalid oversize handling: %v", encoding, response, err)
			}
			data, readErr := io.ReadAll(body)
			body.Close()
			if readErr != nil || !bytes.Equal(data, wire) {
				t.Fatal("oversized body lost replay bytes")
			}
		}
	}
	body := io.NopCloser(io.MultiReader(strings.NewReader("partial"), errorReader{io.ErrUnexpectedEOF}))
	if _, available, err := readRewriteBody(context.Background(), &body, "", 1024, false, func(string) {}); available || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("truncated upload was accepted as a complete body")
	}
}

func TestHeaderOnlyScriptDoesNotRetainABodyReservationForTheOriginalStream(t *testing.T) {
	scripts := scriptProgram(t, `{"http":[{"pattern":".","phase":"request","source":"$done({headers:$request.headers});"}]}`)
	program := &httpProgram{RewriteProgram: &RewriteProgram{limit: scripts.limit}, scripts: scripts}
	body := &observedBody{Reader: strings.NewReader("streaming upload")}
	request, _ := http.NewRequest("POST", "http://example.test/", body)
	request.Header.Set("Content-Type", "application/grpc")
	if _, err := program.request(request, "test", request.URL.String(), func(string) {}); err != nil {
		t.Fatal(err)
	}
	if body.reads != 0 || request.Body != body || processingBodies.used.Load() != 0 {
		t.Fatal("header-only script retained body memory for an unbuffered stream")
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }
