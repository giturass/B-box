package httpdebug

import (
	"bytes"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

type webSocketCompressionDirection struct {
	noContext bool
	window    int
}

type webSocketCompression struct {
	enabled bool
	client  webSocketCompressionDirection
	server  webSocketCompressionDirection
}

type webSocketExtension struct {
	name   string
	params map[string]string
}

// Unknown extensions must keep the original opaque upgrade. RSV bits and payload
// transformations cannot be interpreted safely without the negotiated extension.
func negotiateWebSocketCompression(request, response http.Header) (webSocketCompression, string) {
	config := webSocketCompression{
		client: webSocketCompressionDirection{window: 15},
		server: webSocketCompressionDirection{window: 15},
	}
	offers, err := parseWebSocketExtensions(request.Values("Sec-WebSocket-Extensions"))
	if err != nil {
		return config, "WebSocket messages not inspected: invalid extension offer"
	}
	selected, err := parseWebSocketExtensions(response.Values("Sec-WebSocket-Extensions"))
	if err != nil {
		return config, "WebSocket messages not inspected: invalid extension negotiation"
	}
	if len(selected) == 0 {
		return config, ""
	}
	if len(selected) != 1 || selected[0].name != "permessage-deflate" {
		return config, "WebSocket messages not inspected: unsupported negotiated extension"
	}
	extension := selected[0]
	for name, value := range extension.params {
		switch name {
		case "client_no_context_takeover", "server_no_context_takeover":
			if value != "" {
				return config, "WebSocket messages not inspected: invalid context-takeover parameter"
			}
		case "client_max_window_bits", "server_max_window_bits":
			if _, valid := webSocketWindow(value); !valid {
				return config, "WebSocket messages not inspected: invalid compression window"
			}
		default:
			return config, "WebSocket messages not inspected: unsupported compression parameter"
		}
	}
	matched := false
	for _, offer := range offers {
		if offer.name != "permessage-deflate" {
			continue
		}
		valid := true
		for name, value := range offer.params {
			switch name {
			case "client_no_context_takeover", "server_no_context_takeover":
				valid = valid && value == ""
			case "client_max_window_bits":
				if value != "" {
					_, accepted := webSocketWindow(value)
					valid = valid && accepted
				}
			case "server_max_window_bits":
				_, accepted := webSocketWindow(value)
				valid = valid && accepted
			default:
				valid = false
			}
		}
		// client_max_window_bits in an offer is a hint, unlike the server's
		// mandatory bound. The parameter itself still must have been offered.
		if _, ok := extension.params["client_max_window_bits"]; ok {
			_, offered := offer.params["client_max_window_bits"]
			valid = valid && offered
		}
		if limit, ok := offer.params["server_max_window_bits"]; ok {
			bound, _ := strconv.Atoi(limit)
			value, exists := extension.params["server_max_window_bits"]
			bits, _ := strconv.Atoi(value)
			valid = valid && exists && bits <= bound
		}
		if _, offered := offer.params["server_no_context_takeover"]; offered {
			_, accepted := extension.params["server_no_context_takeover"]
			valid = valid && accepted
		}
		if valid {
			matched = true
			break
		}
	}
	if !matched {
		return config, "WebSocket messages not inspected: compression was not validly offered"
	}
	config.enabled = true
	_, config.client.noContext = extension.params["client_no_context_takeover"]
	_, config.server.noContext = extension.params["server_no_context_takeover"]
	if value := extension.params["client_max_window_bits"]; value != "" {
		config.client.window, _ = strconv.Atoi(value)
	}
	if value := extension.params["server_max_window_bits"]; value != "" {
		config.server.window, _ = strconv.Atoi(value)
	}
	return config, ""
}

func webSocketWindow(value string) (int, bool) {
	bits, err := strconv.Atoi(value)
	return bits, err == nil && bits >= 8 && bits <= 15 && strconv.Itoa(bits) == value
}

func parseWebSocketExtensions(values []string) ([]webSocketExtension, error) {
	var extensions []webSocketExtension
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			continue
		}
		entries, err := splitWebSocketHeader(value, ',')
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			parts, err := splitWebSocketHeader(entry, ';')
			if err != nil {
				return nil, err
			}
			name := strings.ToLower(strings.TrimSpace(parts[0]))
			if !webSocketToken(name) {
				return nil, errors.New("invalid WebSocket extension token")
			}
			extension := webSocketExtension{name: name, params: make(map[string]string)}
			for _, parameter := range parts[1:] {
				name, value, hasValue := strings.Cut(parameter, "=")
				name = strings.ToLower(strings.TrimSpace(name))
				if !webSocketToken(name) {
					return nil, errors.New("invalid WebSocket extension parameter")
				}
				if _, duplicate := extension.params[name]; duplicate {
					return nil, errors.New("duplicate WebSocket extension parameter")
				}
				value = strings.TrimSpace(value)
				if hasValue {
					if strings.HasPrefix(value, "\"") {
						value, err = strconv.Unquote(value)
						if err != nil {
							return nil, err
						}
					}
					if !webSocketToken(value) {
						return nil, errors.New("invalid WebSocket extension parameter value")
					}
				}
				extension.params[name] = value
			}
			extensions = append(extensions, extension)
		}
	}
	return extensions, nil
}

func splitWebSocketHeader(value string, separator byte) ([]string, error) {
	var parts []string
	quoted, escaped, start := false, false, 0
	for index := 0; index < len(value); index++ {
		if escaped {
			escaped = false
			continue
		}
		if quoted && value[index] == '\\' {
			escaped = true
			continue
		}
		if value[index] == '"' {
			quoted = !quoted
		} else if !quoted && value[index] == separator {
			parts = append(parts, value[start:index])
			start = index + 1
		}
	}
	if quoted || escaped {
		return nil, errors.New("unterminated WebSocket extension string")
	}
	return append(parts, value[start:]), nil
}

func webSocketToken(value string) bool {
	if value == "" {
		return false
	}
	for index := range value {
		ch := value[index]
		if ch <= 32 || ch >= 127 || strings.ContainsRune("()<>@,;:\\\"/[]?={} \t", rune(ch)) {
			return false
		}
	}
	return true
}

// RFC 7692 strips the sync-flush tail. Add it and a final empty block so Go's
// DEFLATE reader reaches a verifiable EOF at exactly this message's boundary.
var webSocketDeflateTail = []byte{0, 0, 255, 255, 1, 0, 0, 255, 255}

type webSocketDeflateReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *webSocketDeflateReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}

func (r *webSocketDeflateReader) ReadByte() (byte, error) {
	var data [1]byte
	_, err := io.ReadFull(r, data[:])
	return data[0], err
}

type webSocketHistoryWriter struct {
	writer io.Writer
	limit  int
	data   []byte
}

func (w *webSocketHistoryWriter) Write(data []byte) (int, error) {
	n, err := w.writer.Write(data)
	if n > 0 {
		data = data[:n]
		if len(data) >= w.limit {
			w.data = append(w.data[:0], data[len(data)-w.limit:]...)
		} else {
			keep := min(len(w.data), w.limit-len(data))
			copy(w.data, w.data[len(w.data)-keep:])
			w.data = append(w.data[:keep], data...)
		}
	}
	return n, err
}

// Only compressed messages update a context-takeover dictionary. Uncompressed
// messages and injected messages do not enter the original sender's history.
func inflateWebSocketMessage(ctx context.Context, payload io.Reader, output io.Writer, config webSocketCompressionDirection, dictionary []byte) ([]byte, error) {
	if config.noContext {
		dictionary = nil
	}
	limit := 1 << config.window
	if len(dictionary) > limit {
		dictionary = dictionary[len(dictionary)-limit:]
	}
	input := &webSocketDeflateReader{ctx: ctx, reader: io.MultiReader(payload, bytes.NewReader(webSocketDeflateTail))}
	reader := flate.NewReaderDict(input, dictionary)
	defer reader.Close()
	history := &webSocketHistoryWriter{writer: output, limit: limit, data: append([]byte(nil), dictionary...)}
	_, err := io.CopyBuffer(history, reader, make([]byte, 32*1024))
	// Drain after early BFINAL or an invalid block so the frame reader's pipe
	// writer cannot remain blocked and hide a protocol error until service stop.
	remaining, drainErr := io.CopyBuffer(io.Discard, input, make([]byte, 32*1024))
	if err != nil {
		var protocol *webSocketProtocolError
		if errors.As(err, &protocol) || ctx.Err() != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", webSocketError(1002, "invalid permessage-deflate payload"), err)
	}
	if drainErr != nil {
		return nil, drainErr
	}
	if remaining != 0 {
		return nil, webSocketError(1002, "compressed message ended before its frame boundary")
	}
	if config.noContext {
		return nil, nil
	}
	return history.data, nil
}
