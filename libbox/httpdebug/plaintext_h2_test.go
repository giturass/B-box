package httpdebug

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/hpack"
)

func TestPlaintextHTTP2ClientResetKeepsHPACKAndOtherStreams(t *testing.T) {
	engine, store := testEngine(t, Config{HTTPS: true})
	openPlaintext(t, engine, "reset-client")
	var request, response, block bytes.Buffer
	request.WriteString(http2.ClientPreface)
	req, res := http2.NewFramer(&request, nil), http2.NewFramer(&response, nil)
	_ = req.WriteSettings()
	_ = res.WriteSettings()
	h2Headers(t, req, 1, false, ":method", "POST", ":scheme", "https", ":authority", "api.test", ":path", "/cancelled", "content-length", "9")
	_ = req.WriteData(1, false, []byte("part"))
	_ = req.WriteRSTStream(1, http2.ErrCodeCancel)
	feedPlaintext(t, engine, "reset-client", "write", request.Bytes(), 3)
	cancelled := waitComplete(t, store, 1)[0]
	detail, _ := store.Detail(cancelled.ID)
	if !strings.Contains(detail.Error, "CANCEL") || detail.Request.BodyComplete || detail.Response.BodyComplete || string(detail.RequestBody) != "part" {
		t.Fatalf("reset lost the partial transaction: %+v", detail)
	}
	if _, err := store.Curl(cancelled.ID); err == nil {
		t.Fatal("cancelled upload exported as complete")
	}
	request.Reset()
	h2Headers(t, req, 3, true, ":method", "GET", ":scheme", "https", ":authority", "api.test", ":path", "/kept")
	feedPlaintext(t, engine, "reset-client", "write", request.Bytes(), 3)

	// These response bytes were already in flight when the client cancelled.
	// The cancelled header block changes the connection-wide dynamic table;
	// stream 3 references that entry instead of sending the value again.
	encoder := hpack.NewEncoder(&block)
	for _, stream := range []uint32{1, 3} {
		block.Reset()
		_ = encoder.WriteField(hpack.HeaderField{Name: ":status", Value: "200"})
		_ = encoder.WriteField(hpack.HeaderField{Name: "x-shared", Value: "dynamic-after-reset"})
		_ = res.WriteHeaders(http2.HeadersFrameParam{StreamID: stream, EndHeaders: true, BlockFragment: append([]byte(nil), block.Bytes()...)})
		_ = res.WriteData(stream, true, []byte("ok"))
	}
	feedPlaintext(t, engine, "reset-client", "read", response.Bytes(), 2)
	for _, item := range waitComplete(t, store, 2) {
		d, _ := store.Detail(item.ID)
		if strings.HasSuffix(d.Request.URL, "/kept") {
			if d.Error != "" || !d.Response.BodyComplete || string(d.ResponseBody) != "ok" {
				t.Fatalf("reset stopped another stream: %+v", d)
			}
			found := false
			for _, header := range d.Response.Headers {
				found = found || strings.EqualFold(header.Name, "x-shared") && header.Value == "dynamic-after-reset"
			}
			if !found {
				t.Fatal("HPACK dynamic table was not retained across reset")
			}
		}
	}
	if engine.skipped.Load() != 0 {
		t.Fatal("stream cancellation was counted as a failed connection")
	}
	closePlaintext(t, engine, "reset-client")
}

func TestPlaintextHTTP2ServerResetsCanArriveBeforeRequests(t *testing.T) {
	engine, store := testEngine(t, Config{HTTPS: true})
	openPlaintext(t, engine, "reset-server")
	var request, response bytes.Buffer
	request.WriteString(http2.ClientPreface)
	req, res := http2.NewFramer(&request, nil), http2.NewFramer(&response, nil)
	_ = req.WriteSettings()
	_ = res.WriteSettings()
	// More resets than the active-stream limit must not accumulate tombstones.
	for stream := uint32(1); stream < 100; stream += 2 {
		h2Headers(t, req, stream, true, ":method", "GET", ":scheme", "https", ":authority", "api.test", ":path", "/")
		_ = res.WriteRSTStream(stream, http2.ErrCodeRefusedStream)
	}
	h2Headers(t, req, 101, true, ":method", "GET", ":scheme", "https", ":authority", "api.test", ":path", "/after")
	h2Headers(t, res, 101, true, ":status", "204")
	// Supply each request after its reset has reached the response reader,
	// keeping legitimate concurrent streams below the configured limit.
	feedPlaintext(t, engine, "reset-server", "read", response.Bytes(), 7)
	reader := http2.NewFramer(nil, bytes.NewReader(request.Bytes()[len(http2.ClientPreface):]))
	feedPlaintext(t, engine, "reset-server", "write", []byte(http2.ClientPreface), 2)
	var single bytes.Buffer
	writer := http2.NewFramer(&single, nil)
	for count := 0; ; {
		frame, err := reader.ReadFrame()
		if err != nil {
			break
		}
		single.Reset()
		switch frame := frame.(type) {
		case *http2.SettingsFrame:
			_ = writer.WriteSettings()
		case *http2.HeadersFrame:
			_ = writer.WriteHeaders(http2.HeadersFrameParam{StreamID: frame.StreamID, EndHeaders: true, EndStream: true, BlockFragment: frame.HeaderBlockFragment()})
			count++
		}
		feedPlaintext(t, engine, "reset-server", "write", single.Bytes(), 3)
		if count > 0 {
			waitComplete(t, store, count)
		}
	}
	items := waitComplete(t, store, 51)
	var successes, resets int
	for _, item := range items {
		d, _ := store.Detail(item.ID)
		if strings.HasSuffix(d.Request.URL, "/after") && d.Response.Status == 204 && d.Error == "" {
			successes++
		} else if strings.Contains(d.Error, "REFUSED_STREAM") && !d.Response.BodyComplete {
			resets++
		}
	}
	if successes != 1 || resets != 50 || engine.skipped.Load() != 0 {
		t.Fatalf("reset crossed stream boundaries: successes=%d resets=%d skipped=%d", successes, resets, engine.skipped.Load())
	}
	closePlaintext(t, engine, "reset-server")
}
