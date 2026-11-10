package httpdebug

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const previewLimit = 64 * 1024

type Preview struct {
	Text      string `json:"text"`
	Encoding  string `json:"encoding"`
	Truncated bool   `json:"truncated"`
}

func headerValue(m Message, name string) string {
	for _, h := range m.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

// Decode only a bounded prefix. A compressed response cannot allocate an
// unbounded expansion, and unsupported/binary data remains available as base64.
func decodeBody(m Message, body []byte, limit int) (data []byte, decoded, truncated bool) {
	encodings, err := contentEncodings(messageContentEncoding(m))
	if err != nil {
		return body, false, m.BodyTruncated
	}
	lease := reserveBodyWork(int64(limit))
	if lease == nil {
		return body, false, m.BodyTruncated
	}
	defer lease.release()
	data, err = decodeContent(body, encodings, int64(limit))
	if len(data) == 0 && err != nil {
		return body, false, true
	}
	truncated = len(data) > limit || m.BodyTruncated || err != nil
	if len(data) > limit {
		data = data[:limit]
	}
	return data, true, truncated
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

func preview(m Message, body []byte) Preview {
	data, decoded, truncated := decodeBody(m, body, previewLimit)
	if !decoded {
		return binaryPreview(body, truncated)
	}
	// Do not classify a cut UTF-8 code point at the preview boundary as binary.
	if truncated {
		for i := 0; i < 3 && len(data) > 0 && !utf8.Valid(data); i++ {
			data = data[:len(data)-1]
		}
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return binaryPreview(body, truncated)
	}
	return Preview{Text: string(data), Encoding: "text", Truncated: truncated}
}

func binaryPreview(body []byte, truncated bool) Preview {
	return Preview{Text: base64.StdEncoding.EncodeToString(body[:min(len(body), previewLimit)]),
		Encoding: "base64", Truncated: truncated || len(body) > previewLimit}
}

func (s *Store) WriteHAR(output io.Writer, query string) error {
	page := s.Page(MaxEntries, query)
	ids := make([]string, 0, len(page.Items))
	for _, summary := range page.Items {
		if !summary.DomainOnly && summary.Protocol != "TLS/TCP" {
			ids = append(ids, summary.ID)
		}
	}
	return s.writeHAR(output, ids)
}

// A unified activity view searches connection metadata as well as HTTP fields.
// Export exactly its selected exchanges, even if the filter or store changes
// while the document picker is open. An empty selection never means all records.
func (s *Store) WriteHARSelection(output io.Writer, ids []string) error {
	if len(ids) > MaxEntries {
		return errors.New("too many requests selected for HAR export")
	}
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
	if _, err := io.WriteString(output, `{"log":{"version":"1.2","creator":{"name":"B-box","version":"1"},"entries":[`); err != nil {
		return err
	}
	first := true
	for _, id := range ids {
		detail, err := s.Detail(id)
		if err != nil {
			continue
		} // An evicted record is never replaced with invented data.
		if detail.DomainOnly || detail.Metadata.RawTCP || detail.Request.Protocol == "TLS/TCP" {
			continue
		} // SNI observations and TCP streams are not HTTP exchanges.
		entry := harEntry(detail)
		if !first {
			if _, err := io.WriteString(output, ","); err != nil {
				return err
			}
		}
		first = false
		if err := json.NewEncoder(output).Encode(entry); err != nil {
			return err
		}
	}
	_, err := io.WriteString(output, "]}}")
	return err
}

func harEntry(d *Detail) map[string]any {
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
	if len(d.RequestBody) > 0 {
		content := harContent(d.Request, d.RequestBody)
		content["_encoding"] = content["encoding"]
		if text, ok := content["text"].(string); ok {
			if data, err := base64.StdEncoding.DecodeString(text); err == nil && utf8.Valid(data) && bytes.IndexByte(data, 0) < 0 {
				content["text"], content["_encoding"] = string(data), ""
			}
		}
		delete(content, "encoding")
		request["postData"] = content
	}
	content := harContent(d.Response, d.ResponseBody)
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
			"requestTrailersTruncated": d.Request.TrailersTruncated, "responseTrailersTruncated": d.Response.TrailersTruncated}}
}

func harContent(message Message, body []byte) map[string]any {
	encoding := strings.ToLower(strings.TrimSpace(messageContentEncoding(message)))
	content := map[string]any{"size": message.BodyBytes, "mimeType": headerValue(message, "Content-Type"),
		"_capturedBytes": len(body), "_truncated": message.BodyTruncated, "_complete": message.BodyComplete}
	data, decoded, truncated := body, true, message.BodyTruncated
	if encoding != "" && encoding != "identity" {
		// HAR content.text is the decoded representation. Preserve the complete
		// captured wire prefix separately, including unsupported content encodings.
		data, decoded, truncated = decodeBody(message, body, previewLimit)
		content["_rawBody"] = base64.StdEncoding.EncodeToString(body)
		content["_rawBodyEncoding"], content["_contentEncoding"] = "base64", encoding
		content["size"] = len(data)
		content["_sizeIsPrefix"] = truncated || !message.BodyComplete
	}
	content["_truncated"] = truncated
	if decoded {
		content["text"], content["encoding"] = base64.StdEncoding.EncodeToString(data), "base64"
	} else {
		content["size"] = 0
		content["_sizeUnknown"] = true
		content["comment"] = "Content decoding unavailable; captured bytes are in _rawBody with the original _contentEncoding."
	}
	return content
}

func (s *Store) Curl(id string) (string, error) {
	d, err := s.Detail(id)
	if err != nil {
		return "", err
	}
	if d.DomainOnly || d.Metadata.RawTCP || !strings.HasPrefix(d.Request.Protocol, "HTTP/") || !d.Request.BodyComplete || d.Request.BodyTruncated || d.Request.HeadersTruncated {
		return "", errors.New("cURL export requires complete, untruncated HTTP request headers and body")
	}
	if len(d.Request.Trailers) > 0 || d.Request.TrailersTruncated {
		return "", errors.New("cURL export cannot reproduce request trailers; use HAR")
	}
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	var out strings.Builder
	if len(d.RequestBody) > 0 {
		fmt.Fprintf(&out, "printf '%%s' %s | base64 -d | ", quote(base64.StdEncoding.EncodeToString(d.RequestBody)))
	}
	fmt.Fprintf(&out, "curl --globoff --path-as-is --request %s --url %s", quote(d.Request.Method), quote(d.Request.URL))
	for _, h := range d.Request.Headers {
		switch strings.ToLower(h.Name) {
		case "content-length", "transfer-encoding", "connection", "proxy-connection":
			continue
		}
		fmt.Fprintf(&out, " \\\n  --header %s", quote(h.Name+": "+h.Value))
	}
	if len(d.RequestBody) > 0 {
		if headerValue(d.Request, "Content-Type") == "" {
			out.WriteString(" \\\n  --header 'Content-Type:'")
		}
		out.WriteString(" \\\n  --data-binary @-")
	}
	out.WriteByte('\n')
	return out.String(), nil
}
