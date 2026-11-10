package httpdebug

import (
	"bufio"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// Chain streaming decoders for inspection/export. Processing has separate
// per-layer user limits; no intermediate body is materialized here.
func newContentReader(raw io.Reader, encodings []string) (io.ReadCloser, error) {
	reader := &contentReader{Reader: raw}
	for i := len(encodings) - 1; i >= 0; i-- {
		var err error
		switch encodings[i] {
		case "identity":
			continue
		case "gzip", "x-gzip":
			var decoder *gzip.Reader
			decoder, err = gzip.NewReader(reader.Reader)
			if err == nil {
				reader.Reader = decoder
				reader.closers = append(reader.closers, func() { decoder.Close() })
			}
		case "deflate":
			buffer := bufio.NewReader(reader.Reader)
			header, _ := buffer.Peek(2)
			var decoder io.ReadCloser
			// HTTP deflate is zlib-wrapped, but older servers send raw DEFLATE.
			// Inspect the wrapper without buffering or consuming the whole stream.
			wrapped := len(header) == 2 && header[0]&15 == 8 && header[0]>>4 <= 7 &&
				(uint16(header[0])<<8|uint16(header[1]))%31 == 0
			if wrapped {
				decoder, err = zlib.NewReader(buffer)
			} else {
				decoder = flate.NewReader(buffer)
			}
			if err == nil {
				reader.Reader = decoder
				reader.closers = append(reader.closers, func() { decoder.Close() })
			}
		case "br":
			reader.Reader = brotli.NewReader(reader.Reader)
		case "zstd":
			var decoder *zstd.Decoder
			// RFC 9659 limits HTTP zstd frames to an 8 MiB window.
			decoder, err = zstd.NewReader(reader.Reader, zstd.WithDecoderConcurrency(1),
				zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(8*1024*1024),
				zstd.WithDecoderMaxWindow(8*1024*1024), zstd.WithDecodeBuffersBelow(0))
			if err == nil {
				reader.Reader = decoder
				reader.closers = append(reader.closers, decoder.Close)
			}
		}
		if err != nil {
			reader.Close()
			return nil, err
		}
	}
	return reader, nil
}

type contentReader struct {
	io.Reader
	closers []func()
}

func (r *contentReader) Close() error {
	for i := len(r.closers) - 1; i >= 0; i-- {
		r.closers[i]()
	}
	r.closers = nil
	return nil
}
