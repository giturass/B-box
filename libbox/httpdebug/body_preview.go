package httpdebug

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"unicode/utf8"
)

// This is an I/O and UI page size, not a capture or export retention limit.
const BodyPageSize = 16 * 1024

type Preview struct {
	Text        string   `json:"text"`
	Encoding    string   `json:"encoding"`
	Source      string   `json:"source"`
	Offset      int64    `json:"offset"`
	NextOffset  int64    `json:"nextOffset"`
	HasMore     bool     `json:"hasMore"`
	Truncated   bool     `json:"truncated"`
	Error       string   `json:"error,omitempty"`
	Message     *Message `json:"message,omitempty"`
	Charset     string   `json:"charset,omitempty"`
	DisplayText *string  `json:"displayText,omitempty"`
}

func (s *Store) BodyPage(id string, response bool, offset int64, raw bool) (Preview, error) {
	reader := NewBodyReader()
	defer reader.Close()
	return reader.Read(context.Background(), s, id, response, offset, raw)
}

func readBodyPage(message Message, open func() io.Reader, offset int64, raw bool) (Preview, error) {
	return readBodyPageContext(context.Background(), message, open, offset, raw)
}

// Detail's first-page preview remains stateless and bounded. Interactive page
// reads hold a BodyReader so a compressed prefix is decoded only once.
func readBodyPageContext(ctx context.Context, message Message, open func() io.Reader, offset int64, raw bool) (Preview, error) {
	if offset < 0 || offset > math.MaxInt64-BodyPageSize-utf8.UTFMax {
		return Preview{}, errors.New("invalid body page offset")
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	var decodingError error
	var reader io.Reader = open()
	seeker, _ := reader.(io.Seeker)
	state := &bodyReadContext{ctx: ctx}
	reader = &bodyContextReader{state: state, reader: reader}
	if !raw && message.CapturedBytes > 0 {
		encodings, err := contentEncodings(messageContentEncoding(message))
		var decoder io.ReadCloser
		if err == nil && slices.ContainsFunc(encodings, func(encoding string) bool { return encoding != "identity" }) {
			decoder, err = newBodyContentReader(state, reader, encodings)
		}
		if err != nil {
			if ctx.Err() != nil {
				return Preview{}, ctx.Err()
			}
			if offset != 0 {
				return Preview{}, fmt.Errorf("body decoding unavailable: %w", err)
			}
			reader = open()
			seeker, _ = reader.(io.Seeker)
			raw, decodingError, reader = true, err, &bodyContextReader{state: state, reader: reader}
		} else if decoder != nil {
			defer decoder.Close()
			reader = decoder
			seeker = nil
		}
	}
	// Replay this bounded sniff prefix rather than rereading the first page.
	// Later pages still derive charset from the beginning of the same body.
	prefix, prefixErr := readBodyBytes(ctx, reader, bodyCharsetPrefixSize)
	if ctx.Err() != nil {
		return Preview{}, ctx.Err()
	}
	charset := detectBodyCharset(message, prefix)
	if seeker != nil && offset > 0 {
		if offset > message.CapturedBytes {
			return Preview{}, errors.New("body page is past the saved content")
		}
		if _, err := seeker.Seek(offset, io.SeekStart); err != nil {
			return Preview{}, err
		}
	} else {
		reader = io.MultiReader(bytes.NewReader(prefix), reader)
		if offset > 0 {
			if err := skipBodyBytes(ctx, reader, offset); err != nil {
				return Preview{}, fmt.Errorf("body page is past the readable content: %w", err)
			}
		}
	}
	data, err := readBodyBytes(ctx, reader, BodyPageSize+utf8.UTFMax)
	if err == nil {
		err = prefixErr
	}
	if err != nil {
		if ctx.Err() != nil {
			return Preview{}, ctx.Err()
		}
		if raw {
			return Preview{}, err
		}
		if len(data) == 0 && offset == 0 {
			fallback, readErr := readBodyPageContext(ctx, message, open, 0, true)
			fallback.Truncated, fallback.Error = true, err.Error()
			return fallback, readErr
		}
	}
	page, pageErr := previewBodyBytes(ctx, message, data, offset, raw, charset, err)
	if decodingError != nil {
		page.Error = decodingError.Error()
	}
	return page, pageErr
}

func previewBodyBytes(ctx context.Context, message Message, data []byte, offset int64, raw bool, charset bodyCharset, readErr error) (Preview, error) {
	page := Preview{Encoding: "text", Source: "decoded", Offset: offset, NextOffset: offset, Truncated: message.BodyTruncated, Message: &message}
	end := min(len(data), BodyPageSize)
	if raw {
		page.Source = "raw"
	} else {
		page.Charset = charset.name
		end = bodyPageEnd(charset, data, BodyPageSize)
		page.Error = charset.error
	}
	if readErr != nil {
		page.Truncated = true
		if page.Error != "" {
			page.Error += "; "
		}
		page.Error += readErr.Error()
	}
	if raw || !utf8.Valid(data[:end]) || bytes.IndexByte(data[:end], 0) >= 0 {
		page.Text, page.Encoding = base64.StdEncoding.EncodeToString(data[:end]), "base64"
	} else {
		page.Text = string(data[:end])
	}
	page.NextOffset = offset + int64(end)
	page.HasMore = len(data) > end
	if !raw {
		display, err := decodeBodyDisplay(ctx, charset, data[:end], offset)
		if err != nil {
			return Preview{}, err
		}
		if display != nil && (charset.kind != bodyCharsetUTF8 || page.Encoding != "text" || *display != page.Text) {
			page.DisplayText = display
		}
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	return page, nil
}

// Retain at most one incomplete rune between streaming UTF-8 chunks.
func completeUTF8Prefix(data []byte) int {
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:]) {
				return i
			}
			break
		}
	}
	return len(data)
}
