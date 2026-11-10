package httpdebug

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"
	"unicode/utf8"
)

// This is an I/O and UI page size, not a capture or export retention limit.
const BodyPageSize = 16 * 1024

type Preview struct {
	Text       string `json:"text"`
	Encoding   string `json:"encoding"`
	Source     string `json:"source"`
	Offset     int64  `json:"offset"`
	NextOffset int64  `json:"nextOffset"`
	HasMore    bool   `json:"hasMore"`
	Truncated  bool   `json:"truncated"`
	Error      string `json:"error,omitempty"`
}

func (s *Store) BodyPage(id string, response bool, offset int64, raw bool) (Preview, error) {
	snapshot, err := s.snapshot(id)
	if err != nil {
		return Preview{}, err
	}
	defer snapshot.close()
	return readBodyPage(snapshot.message(response), func() io.Reader { return snapshot.reader(response) }, offset, raw)
}

func readBodyPage(message Message, open func() io.Reader, offset int64, raw bool) (Preview, error) {
	if offset < 0 {
		return Preview{}, errors.New("invalid body page offset")
	}
	page := Preview{Encoding: "text", Source: "decoded", Offset: offset, NextOffset: offset, Truncated: message.BodyTruncated}
	var reader io.Reader = open()
	if !raw && message.CapturedBytes > 0 {
		encodings, err := contentEncodings(messageContentEncoding(message))
		var decoder io.ReadCloser
		if err == nil && slices.ContainsFunc(encodings, func(encoding string) bool { return encoding != "identity" }) {
			decoder, err = newContentReader(reader, encodings)
		}
		if err != nil {
			if offset != 0 {
				return Preview{}, fmt.Errorf("body decoding unavailable: %w", err)
			}
			raw, page.Error, reader = true, err.Error(), open()
		} else if decoder != nil {
			defer decoder.Close()
			reader = decoder
		}
	}
	if raw {
		page.Source = "raw"
	}
	if offset > 0 {
		if seeker, ok := reader.(io.Seeker); ok {
			if offset > message.CapturedBytes {
				return Preview{}, errors.New("body page is past the saved content")
			}
			if _, err := seeker.Seek(offset, io.SeekStart); err != nil {
				return Preview{}, err
			}
		} else if _, err := io.CopyN(io.Discard, reader, offset); err != nil {
			return Preview{}, fmt.Errorf("body page is past the readable content: %w", err)
		}
	}
	// Look ahead only enough to keep a UTF-8 character intact at a page edge.
	data, err := io.ReadAll(io.LimitReader(reader, BodyPageSize+utf8.UTFMax))
	if err != nil {
		if raw {
			return Preview{}, err
		}
		if len(data) == 0 && offset == 0 {
			fallback, readErr := readBodyPage(message, open, 0, true)
			fallback.Truncated, fallback.Error = true, err.Error()
			return fallback, readErr
		}
		page.Truncated, page.Error = true, err.Error()
	}
	end := min(len(data), BodyPageSize)
	if !raw && end < len(data) {
		for i := 0; i < utf8.UTFMax-1 && end > 0 && !utf8.RuneStart(data[end]); i++ {
			end--
		}
	}
	if raw || !utf8.Valid(data[:end]) || bytes.IndexByte(data[:end], 0) >= 0 {
		end = min(len(data), BodyPageSize)
		page.Text, page.Encoding = base64.StdEncoding.EncodeToString(data[:end]), "base64"
	} else {
		page.Text = string(data[:end])
	}
	page.NextOffset = offset + int64(end)
	page.HasMore = len(data) > end
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
