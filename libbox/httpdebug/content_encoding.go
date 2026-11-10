package httpdebug

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func contentEncodings(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	encodings := strings.Split(value, ",")
	for i, encoding := range encodings {
		encodings[i] = strings.ToLower(strings.TrimSpace(encoding))
		switch encodings[i] {
		case "identity", "gzip", "x-gzip", "deflate", "br", "zstd":
		default:
			return nil, fmt.Errorf("unsupported Content-Encoding %q", encoding)
		}
	}
	return encodings, nil
}

func contentEncoding(header http.Header) string {
	return strings.Join(header.Values("Content-Encoding"), ",")
}

// Allocate as bytes arrive, not according to the configured maximum. Read one
// extra byte only when a user limit is present so an oversized stream can replay.
func readBodyPrefix(reader io.Reader, limit int64) ([]byte, error) {
	if limit > 0 && limit < int64(^uint64(0)>>1) {
		reader = io.LimitReader(reader, limit+1)
	}
	return io.ReadAll(reader)
}

// Each intermediate representation has the same limit as the final body.
func decodeContent(body []byte, encodings []string, limit int64) ([]byte, error) {
	data, err := decodeContentWithReader(body, encodings, func(reader io.Reader) ([]byte, error) {
		data, err := readBodyPrefix(reader, limit)
		if exceedsBodyLimit(int64(len(data)), limit) {
			err = errRewriteSize
		}
		return data, err
	})
	if err == nil && exceedsBodyLimit(int64(len(data)), limit) {
		err = errRewriteSize
	}
	return data, err
}

// Content-Encoding lists encoders in application order, so decode backwards.
// A partial intermediate layer must never be presented as decoded plaintext.
func decodeContentWithReader(body []byte, encodings []string, read func(io.Reader) ([]byte, error)) ([]byte, error) {
	data := body
	for i := len(encodings) - 1; i >= 0; i-- {
		var reader io.Reader
		closeReader := func() {}
		var err error
		switch encodings[i] {
		case "identity":
			continue
		case "gzip", "x-gzip":
			var decoder *gzip.Reader
			decoder, err = gzip.NewReader(bytes.NewReader(data))
			if err == nil {
				reader, closeReader = decoder, func() { decoder.Close() }
			}
		case "deflate":
			var decoder io.ReadCloser
			decoder, err = zlib.NewReader(bytes.NewReader(data))
			if err != nil {
				decoder, err = flate.NewReader(bytes.NewReader(data)), nil
			}
			reader, closeReader = decoder, func() { decoder.Close() }
		case "br":
			reader = brotli.NewReader(bytes.NewReader(data))
		case "zstd":
			// RFC 9659 section 3 limits HTTP zstd frames to an 8 MiB window.
			var decoder *zstd.Decoder
			decoder, err = zstd.NewReader(bytes.NewReader(data), zstd.WithDecoderConcurrency(1),
				zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(8*1024*1024),
				zstd.WithDecoderMaxWindow(8*1024*1024), zstd.WithDecodeBuffersBelow(0))
			if err == nil {
				reader, closeReader = decoder, decoder.Close
			}
		}
		if err != nil {
			return nil, err
		}
		data, err = read(reader)
		closeReader()
		if err != nil {
			if i != 0 {
				return nil, err
			}
			return data, err
		}
	}
	return data, nil
}

func streamingContent(header http.Header) bool {
	mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(header.Get("Content-Type"), ";", 2)[0]))
	return mediaType == "text/event-stream" || mediaType == "application/x-ndjson" ||
		mediaType == "application/ndjson" || mediaType == "application/json-seq" ||
		mediaType == "multipart/x-mixed-replace" || mediaType == "application/grpc" ||
		strings.HasPrefix(mediaType, "application/grpc+") || strings.HasPrefix(mediaType, "application/grpc-web")
}

func bodyEligible(header http.Header, note func(string), unaryGRPC bool) bool {
	if streamingContent(header) && !unaryGRPC || header.Get("Upgrade") != "" {
		note("Body processing skipped: streaming content or protocol upgrade; original stream preserved")
		return false
	}
	if _, err := contentEncodings(contentEncoding(header)); err != nil {
		note("Body processing skipped: " + err.Error() + "; original stream preserved")
		return false
	}
	return true
}
