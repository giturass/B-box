package httpdebug

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

func headerValue(m Message, name string) string {
	for _, h := range m.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func messageContentEncoding(m Message) string {
	var values []string
	for _, header := range m.Headers {
		if strings.EqualFold(header.Name, "Content-Encoding") {
			values = append(values, header.Value)
		}
	}
	return strings.Join(values, ",")
}

func (s *Store) WriteHAR(output io.Writer, query string) error {
	page := s.Page(0, query)
	ids := make([]string, 0, len(page.Items))
	for _, summary := range page.Items {
		if !summary.DomainOnly && summary.Protocol != "TLS/TCP" {
			ids = append(ids, summary.ID)
		}
	}
	return s.writeHAR(output, ids)
}

// Export exactly the selected exchanges, even if the filter or store changes
// while the document picker is open. An empty selection never means all records.
func (s *Store) WriteHARSelection(output io.Writer, ids []string) error {
	selected := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if !validID(id) {
			return errors.New("invalid request ID in HAR selection")
		}
		if !seen[id] {
			selected = append(selected, id)
			seen[id] = true
		}
	}
	return s.writeHAR(output, selected)
}

func (s *Store) writeHAR(output io.Writer, ids []string) error {
	buffer := bufio.NewWriter(output)
	if _, err := io.WriteString(buffer, `{"log":{"version":"1.2","creator":{"name":"B-box","version":"1"},"entries":[`); err != nil {
		return err
	}
	first := true
	for _, id := range ids {
		err := func() error {
			snapshot, err := s.snapshot(id)
			if errors.Is(err, os.ErrNotExist) {
				return nil // Never substitute a different record after Clear.
			}
			if err != nil {
				return err
			}
			defer snapshot.close()
			if snapshot.DomainOnly || snapshot.Metadata.RawTCP || snapshot.Request.Protocol == "TLS/TCP" {
				return nil
			}
			entry, err := harEntry(snapshot)
			if err != nil {
				return err
			}
			if !first {
				if _, err := io.WriteString(buffer, ","); err != nil {
					return err
				}
			}
			first = false
			return writeHARValue(buffer, entry)
		}()
		if err != nil {
			return err
		}
	}
	if _, err := io.WriteString(buffer, "]}}"); err != nil {
		return err
	}
	return buffer.Flush()
}

func harEntry(d *bodySnapshot) (map[string]any, error) {
	query := make([]map[string]string, 0)
	if u, err := url.Parse(d.Request.URL); err == nil {
		for key, values := range u.Query() {
			for _, value := range values {
				query = append(query, map[string]string{"name": key, "value": value})
			}
		}
	}
	request := map[string]any{"method": d.Request.Method, "url": d.Request.URL, "httpVersion": d.Request.Protocol,
		"headers": d.Request.Headers, "_trailers": d.Request.Trailers, "queryString": query, "cookies": []any{}, "headersSize": -1, "bodySize": d.Request.BodyBytes}
	if d.Request.CapturedBytes > 0 {
		content, err := harContent(d.Request, func() io.Reader { return d.reader(false) }, true)
		if err != nil {
			return nil, err
		}
		request["postData"] = content
	}
	content, err := harContent(d.Response, func() io.Reader { return d.reader(true) }, false)
	if err != nil {
		return nil, err
	}
	response := map[string]any{"status": d.Response.Status, "statusText": http.StatusText(d.Response.Status), "httpVersion": d.Response.Protocol,
		"headers": d.Response.Headers, "_trailers": d.Response.Trailers, "cookies": []any{}, "content": content, "redirectURL": headerValue(d.Response, "Location"),
		"headersSize": -1, "bodySize": d.Response.BodyBytes}
	return map[string]any{"startedDateTime": time.UnixMilli(d.StartedAt).UTC().Format(time.RFC3339Nano), "time": d.DurationMS,
		"request": request, "response": response, "cache": map[string]any{},
		"timings": map[string]int64{"send": 0, "wait": d.DurationMS, "receive": 0},
		"comment": "B-box measures total elapsed time only. HAR wait contains that total; send/receive are placeholders, not measured phases.",
		"_bBox": map[string]any{"metadata": d.Metadata, "complete": d.Complete, "error": d.Error, "note": d.Note, "originalUrl": d.OriginalURL,
			"timingAccuracy": "total-only", "requestBodyComplete": d.Request.BodyComplete, "requestBodyTruncated": d.Request.BodyTruncated,
			"requestHeadersTruncated": d.Request.HeadersTruncated, "responseHeadersTruncated": d.Response.HeadersTruncated,
			"requestTrailersTruncated": d.Request.TrailersTruncated, "responseTrailersTruncated": d.Response.TrailersTruncated}}, nil
}

// Scan decoded content to establish its size and request text encoding. A
// second streaming pass writes it, so even a highly compressed body does not
// require memory proportional to its decoded size.
func harContent(message Message, raw func() io.Reader, request bool) (map[string]any, error) {
	encoding := strings.ToLower(strings.TrimSpace(messageContentEncoding(message)))
	content := map[string]any{"size": message.BodyBytes, "mimeType": headerValue(message, "Content-Type"),
		"_capturedBytes": message.CapturedBytes, "_truncated": message.BodyTruncated, "_complete": message.BodyComplete}
	open := func() (io.ReadCloser, error) { return io.NopCloser(raw()), nil }
	encoded := encoding != "" && encoding != "identity"
	size, text := message.CapturedBytes, false
	var decodeErr error
	unsupported := false
	if encoded {
		content["_rawBody"] = harBody{open: open, size: size}
		content["_rawBodyEncoding"], content["_contentEncoding"] = "base64", encoding
		size = 0
		var encodings []string
		encodings, decodeErr = contentEncodings(encoding)
		unsupported = decodeErr != nil
		open = func() (io.ReadCloser, error) { return newContentReader(raw(), encodings) }
	}
	if (encoded || request) && decodeErr == nil {
		var reader io.ReadCloser
		reader, decodeErr = open()
		size = 0
		if decodeErr == nil {
			probe := bodyTextProbe{valid: true}
			size, decodeErr = io.Copy(&probe, reader)
			reader.Close()
			text = probe.valid && probe.pendingSize == 0
		}
	}
	if !encoded && decodeErr != nil {
		return nil, decodeErr
	}
	if encoded {
		truncated := message.BodyTruncated || decodeErr != nil && !unsupported
		content["size"], content["_truncated"] = size, truncated
		content["_sizeIsPrefix"] = truncated || !message.BodyComplete
		if decodeErr != nil {
			content["_decodingError"] = decodeErr.Error()
			if size == 0 {
				content["size"], content["_sizeUnknown"] = 0, true
				content["comment"] = "Content decoding unavailable; captured bytes are in _rawBody with the original _contentEncoding."
				return content, nil
			}
		}
	}
	key := "encoding"
	if request {
		key = "_encoding"
	}
	content[key] = "base64"
	if request && text {
		content[key] = ""
	}
	content["text"] = harBody{open: open, size: size, text: request && text}
	return content, nil
}

// Only body values bypass encoding/json, which has no streaming string API.
// Metadata is encoded normally; body strings are emitted in bounded chunks.
type harBody struct {
	open func() (io.ReadCloser, error)
	size int64
	text bool
}

func (body harBody) write(output io.Writer) error {
	reader, err := body.open()
	if err != nil {
		return err
	}
	defer reader.Close()
	if _, err = io.WriteString(output, "\""); err != nil {
		return err
	}
	if body.text {
		writer := &jsonTextWriter{output: output}
		if _, err = io.CopyN(writer, reader, body.size); err != nil {
			return err
		}
		if writer.pendingSize != 0 {
			return errors.New("incomplete UTF-8 in HAR text")
		}
	} else {
		writer := base64.NewEncoder(base64.StdEncoding, output)
		if _, err = io.CopyN(writer, reader, body.size); err != nil {
			return err
		}
		if err = writer.Close(); err != nil {
			return err
		}
	}
	_, err = io.WriteString(output, "\"")
	return err
}

func writeHARValue(output io.Writer, value any) error {
	switch value := value.(type) {
	case harBody:
		return value.write(output)
	case map[string]any:
		if _, err := io.WriteString(output, "{"); err != nil {
			return err
		}
		first := true
		for key, item := range value {
			if !first {
				if _, err := io.WriteString(output, ","); err != nil {
					return err
				}
			}
			first = false
			if err := writeHARValue(output, key); err != nil {
				return err
			}
			if _, err := io.WriteString(output, ":"); err != nil {
				return err
			}
			if err := writeHARValue(output, item); err != nil {
				return err
			}
		}
		_, err := io.WriteString(output, "}")
		return err
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return err
		}
		_, err = output.Write(data)
		return err
	}
}

type bodyTextProbe struct {
	valid       bool
	pending     [utf8.UTFMax]byte
	pendingSize int
}

func (p *bodyTextProbe) Write(data []byte) (int, error) {
	size := len(data)
	if !p.valid {
		return size, nil
	}
	if p.pendingSize > 0 {
		data = append(p.pending[:p.pendingSize:p.pendingSize], data...)
	}
	end := completeUTF8Prefix(data)
	p.valid = utf8.Valid(data[:end]) && bytes.IndexByte(data, 0) < 0
	p.pendingSize = copy(p.pending[:], data[end:])
	return size, nil
}

type jsonTextWriter struct {
	output      io.Writer
	pending     [utf8.UTFMax]byte
	pendingSize int
}

func (w *jsonTextWriter) Write(data []byte) (int, error) {
	size := len(data)
	if w.pendingSize > 0 {
		data = append(w.pending[:w.pendingSize:w.pendingSize], data...)
	}
	end := completeUTF8Prefix(data)
	encoded, err := json.Marshal(string(data[:end]))
	if err != nil {
		return 0, err
	}
	if _, err = w.output.Write(encoded[1 : len(encoded)-1]); err != nil {
		return 0, err
	}
	w.pendingSize = copy(w.pending[:], data[end:])
	return size, nil
}

func (s *Store) WriteCurl(output io.Writer, id string) error {
	d, err := s.snapshot(id)
	if err != nil {
		return err
	}
	defer d.close()
	if d.DomainOnly || d.Metadata.RawTCP || !strings.HasPrefix(d.Request.Protocol, "HTTP/") || !d.Request.BodyComplete || d.Request.BodyTruncated || d.Request.HeadersTruncated {
		return errors.New("cURL export requires complete, untruncated HTTP request headers and body")
	}
	if len(d.Request.Trailers) > 0 || d.Request.TrailersTruncated {
		return errors.New("cURL export cannot reproduce request trailers; use HAR")
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	out := bufio.NewWriter(output)
	if d.Request.CapturedBytes > 0 {
		if _, err = io.WriteString(out, "printf '%s' '"); err != nil {
			return err
		}
		encoder := base64.NewEncoder(base64.StdEncoding, out)
		if _, err = io.CopyN(encoder, d.reader(false), d.Request.CapturedBytes); err != nil {
			return err
		}
		if err = encoder.Close(); err != nil {
			return err
		}
		out.WriteString("' | base64 -d | ")
	}
	fmt.Fprintf(out, "curl --globoff --path-as-is --request %s --url %s", quote(d.Request.Method), quote(d.Request.URL))
	for _, h := range d.Request.Headers {
		switch strings.ToLower(h.Name) {
		case "content-length", "transfer-encoding", "connection", "proxy-connection":
			continue
		}
		fmt.Fprintf(out, " \\\n  --header %s", quote(h.Name+": "+h.Value))
	}
	if d.Request.CapturedBytes > 0 {
		if headerValue(d.Request, "Content-Type") == "" {
			out.WriteString(" \\\n  --header 'Content-Type:'")
		}
		out.WriteString(" \\\n  --data-binary @-")
	}
	out.WriteByte('\n')
	return out.Flush()
}

// Compatibility helper for callers explicitly requesting an in-memory string.
// Android file exports use WriteCurl through ExportUICaptureCurlFile.
func (s *Store) Curl(id string) (string, error) {
	var output strings.Builder
	if err := s.WriteCurl(&output, id); err != nil {
		return "", err
	}
	return output.String(), nil
}
