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

// TCP has no application framing. Preserve server-first protocols, binary
// payloads and half-closes, while exposing each read chunk to stream scripts.
func (e *Engine) serveTCP(ctx context.Context, client, upstream net.Conn, metadata Metadata) error {
	protocol, scheme := "TCP", "tcp"
	if metadata.TLS != nil || metadata.Protocol == "tls" {
		protocol, scheme = "TLS/TCP", "tls"
	}
	metadata.RawTCP, metadata.StreamID = true, newStreamID()
	metadata.StreamURL = (&url.URL{Scheme: scheme, Host: net.JoinHostPort(metadata.Host, strconv.Itoa(int(metadata.Port)))}).String()
	request := &http.Request{Method: "TCP", Proto: protocol, Header: make(http.Header),
		URL:  &url.URL{Scheme: scheme, Host: net.JoinHostPort(metadata.Host, strconv.Itoa(int(metadata.Port)))},
		Body: io.NopCloser(strings.NewReader(""))}
	id := e.beginRecording(metadata, request)
	if id != "" {
		e.store.response(id, &http.Response{Proto: protocol, Header: make(http.Header), ContentLength: -1,
			Body: io.NopCloser(strings.NewReader(""))})
		e.store.addNote(id, protocol+" stream; each direction is a byte stream, not an HTTP message")
		e.recordedRequests.Add(1)
	}
	defer e.store.finish(id)
	metadata.ParentID = id
	stop := context.AfterFunc(ctx, func() { client.Close(); upstream.Close() })
	defer stop()
	writers := map[string]*streamWriter{"client": newStreamWriter(upstream), "server": newStreamWriter(client)}
	send := func(callCtx context.Context, direction string, data []byte, opcode int, injected bool) error {
		writer := writers[direction]
		if writer == nil {
			return errors.New("invalid TCP message direction")
		}
		messageID := ""
		if injected {
			messageID = e.beginStreamMessage(metadata, "tcp", direction, 0, true, false)
		}
		n, err := writer.write(callCtx, data)
		if n > 0 {
			e.plaintextBytes.Add(int64(n))
			e.store.appendBody(id, direction == "server", data[:n])
			if injected {
				e.store.appendBody(messageID, direction == "server", data[:n])
			}
		}
		if injected {
			if err != nil {
				e.store.setError(messageID, err)
			} else {
				e.store.bodyComplete(messageID, direction == "server")
			}
			e.store.finish(messageID)
		}
		return err
	}
	unregister := e.registerStream(metadata.StreamID, "tcp", send)
	defer unregister()
	readCapture := protocol == "TLS/TCP"
	if e.scripts != nil {
		for _, script := range e.scripts.stream {
			if script.options.Type == "tcp" {
				readCapture = false
				break
			}
		}
	}
	results := make(chan error, 2)
	copyStream := func(src net.Conn, direction string) {
		buffer := make([]byte, 32*1024)
		for {
			n, readErr := src.Read(buffer)
			if n > 0 {
				data := buffer[:n]
				// Retain the original TLS aggregate's observed-read semantics. A
				// peer can stop recording immediately after receiving these bytes;
				// capturing only after Write would race that stop. Scripted output
				// and new data-block records use the actual accepted writes below.
				if readCapture {
					e.plaintextBytes.Add(int64(n))
					e.store.appendBody(id, direction == "server", data)
				}
				messageID := ""
				config := e.recording.Load()
				if config != nil && config.RawTCP {
					messageID = e.beginStreamMessage(metadata, "tcp", direction, 0, false, false)
				}
				messageMetadata := metadata
				messageMetadata.messageID = messageID
				result, err := e.processStreamMessage(ctx, messageMetadata, "tcp", direction, 0, data)
				if err != nil && ctx.Err() == nil && !errors.Is(err, errScriptAbort) {
					message := "TCP message script failed; original bytes preserved: " + err.Error()
					e.store.addNote(id, message)
					e.store.addNote(messageID, message)
					writeLog("warn", message+"; stream="+metadata.StreamID)
					result, err = streamResult{body: data}, nil
				}
				if err == nil && result.drop {
					e.store.markStreamResult(messageID, false, true)
					e.store.appendBody(messageID, direction == "server", data)
					e.store.bodyComplete(messageID, direction == "server")
				} else if err == nil {
					written, writeErr := writers[direction].write(ctx, result.body)
					err = writeErr
					if written > 0 {
						if !readCapture {
							e.plaintextBytes.Add(int64(written))
							e.store.appendBody(id, direction == "server", result.body[:written])
						}
						e.store.appendBody(messageID, direction == "server", result.body[:written])
					}
					if err == nil {
						e.store.bodyComplete(messageID, direction == "server")
					}
				}
				if err != nil {
					e.store.setError(messageID, err)
				}
				// Seal this chunk before injections create their own records.
				e.store.finish(messageID)
				if err == nil {
					for _, injection := range result.inject {
						if err = send(ctx, injection.Direction, injection.Body, injection.Opcode, true); err != nil {
							break
						}
					}
				}
				if err != nil {
					results <- err
					return
				}
			}
			if readErr != nil {
				if readErr == io.EOF {
					e.store.bodyComplete(id, direction == "server")
					readErr = writers[direction].closeWrite(ctx)
				}
				results <- readErr
				return
			}
		}
	}
	go copyStream(client, "client")
	go copyStream(upstream, "server")
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
		e.reportError(protocol + " stream for " + metadata.Host + ": " + err.Error())
	}
	return err
}
