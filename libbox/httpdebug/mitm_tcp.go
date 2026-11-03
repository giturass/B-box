package httpdebug

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// A decrypted TCP stream has no HTTP framing. Preserve both byte streams and
// half-closes, including server-first protocols and payloads containing zeros.
func (e *Engine) serveTCP(ctx context.Context, client, upstream net.Conn, metadata Metadata) error {
	id := ""
	if e.captureEnabled && e.config.matches(metadata) {
		request := &http.Request{Method: "TCP", Proto: "TLS/TCP", Header: make(http.Header),
			URL:  &url.URL{Scheme: "tls", Host: net.JoinHostPort(metadata.Host, strconv.Itoa(int(metadata.Port)))},
			Body: io.NopCloser(strings.NewReader(""))}
		id = e.store.begin(metadata, request, e.config.BodyLimit)
		e.store.response(id, &http.Response{Proto: "TLS/TCP", Header: make(http.Header), ContentLength: -1,
			Body: io.NopCloser(strings.NewReader(""))})
		e.store.addNote(id, "Decrypted TLS/TCP stream; each direction is a byte stream, not an HTTP message")
		if id != "" {
			e.recordedRequests.Add(1)
		}
	}
	defer e.store.finish(id)
	stop := context.AfterFunc(ctx, func() { client.Close(); upstream.Close() })
	defer stop()
	results := make(chan error, 2)
	copyStream := func(dst, src net.Conn, response bool) {
		reader := &tapBody{ReadCloser: src, store: e.store, id: id, response: response, expected: -1, engine: e}
		_, err := io.CopyBuffer(dst, reader, make([]byte, 32*1024))
		if err == nil {
			if writer, ok := dst.(interface{ CloseWrite() error }); ok {
				err = writer.CloseWrite()
			} else {
				err = dst.Close()
			}
		}
		results <- err
	}
	go copyStream(upstream, client, false)
	go copyStream(client, upstream, true)
	first := <-results
	if first != nil {
		client.Close()
		upstream.Close()
	}
	err := errors.Join(first, <-results)
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		e.store.setError(id, err)
		e.reportError("TLS/TCP stream for " + metadata.Host + ": " + err.Error())
	}
	return err
}
