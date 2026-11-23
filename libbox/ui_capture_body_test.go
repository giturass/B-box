package libbox

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sagernet/sing-box/experimental/httpdebug"
)

func TestUICaptureBodyBindingsIntegration(t *testing.T) {
	const childEnvironment = "BBOX_UI_CAPTURE_BODY_TEST_CHILD"
	if os.Getenv(childEnvironment) != "1" {
		// The capture manager keeps one store directory for the whole process.
		// Isolate these real HTTP fixtures from the existing processing tests.
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestUICaptureBodyBindingsIntegration$", "-test.count=1", "-test.timeout=25s")
		command.Env = append(os.Environ(), childEnvironment+"=1")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated body binding regressions failed: %v\n%s", err, output)
		}
		return
	}

	directory := t.TempDir()
	if err := httpdebug.Start(directory, `{"version":3,"bodyLimit":-1,"ports":[80]}`, "", nil); err != nil {
		t.Fatal(err)
	}
	defer httpdebug.StopService()
	engine := httpdebug.Current()
	store, err := httpdebug.Saved()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Clear)

	const repetitions = httpdebug.BodyPageSize/2 + 3
	body := append([]byte(`{"message":"`), bytes.Repeat([]byte{0xd6, 0xd0, 0xce, 0xc4}, repetitions)...)
	body = append(body, []byte(`","number":9007199254740993}`)...)
	text := `{"message":"` + strings.Repeat("中文", repetitions) + `","number":9007199254740993}`
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, []byte(text), "", "  "); err != nil {
		t.Fatal(err)
	}
	wire := uiCaptureBodyGzip(t, body)
	image, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+ip1sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = png.Decode(bytes.NewReader(image)); err != nil {
		t.Fatal("image binding fixture is not a complete valid PNG", err)
	}
	fixtures := map[string]struct {
		body            []byte
		contentType     string
		contentEncoding string
	}{
		"/body":    {wire, "application/json; charset=GBK", "gzip"},
		"/invalid": {[]byte(`{"partial":`), "application/json; charset=utf-8", ""},
		"/unknown": {[]byte(`{"valid":"ASCII"}`), "application/json; charset=x-missing-charset", ""},
		"/image":   {uiCaptureBodyGzip(t, image), "image/png", "gzip"},
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			t.Error(readErr)
		}
		if r.URL.Path == "/body" && !bytes.Equal(request, wire) {
			t.Error("HTTP fixture received different compressed request bytes")
		}
		fixture, found := fixtures[r.URL.Path]
		if !found {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", fixture.contentType)
		if fixture.contentEncoding != "" {
			w.Header().Set("Content-Encoding", fixture.contentEncoding)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(fixture.body)
	}))
	defer origin.Close()
	var connections atomic.Int32
	transport := &http.Transport{
		DisableCompression: true,
		DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
			client, server := net.Pipe()
			metadata := httpdebug.Metadata{ConnectionID: fmt.Sprintf("body-binding-%d", connections.Add(1)),
				Host: "body.test", Port: 80, Protocol: "http", Outbound: "original-route"}
			if !engine.Handle(context.Background(), server, metadata, func(ctx context.Context) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "tcp", origin.Listener.Addr().String())
			}, nil, nil) {
				client.Close()
				server.Close()
				return nil, errors.New("body fixture was not admitted for capture")
			}
			return client, nil
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	record := func(path string, requestBody []byte) string {
		t.Helper()
		method := http.MethodGet
		if requestBody != nil {
			method = http.MethodPost
		}
		request, requestErr := http.NewRequest(method, "http://body.test"+path, bytes.NewReader(requestBody))
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		if requestBody != nil {
			request.Header.Set("Content-Type", fixtures[path].contentType)
			request.Header.Set("Content-Encoding", fixtures[path].contentEncoding)
		}
		response, responseErr := client.Do(request)
		if responseErr != nil {
			t.Fatal(responseErr)
		}
		reply, responseErr := io.ReadAll(response.Body)
		response.Body.Close()
		if responseErr != nil || response.StatusCode != http.StatusCreated || !bytes.Equal(reply, fixtures[path].body) {
			t.Fatal("recording changed the fixture response", responseErr)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			for _, item := range store.Page(0, "").Items {
				if item.URL == request.URL.String() && item.Complete {
					return item.ID
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("HTTP capture did not complete for %s: %+v", path, store.Page(0, ""))
		return ""
	}
	bodyID := record("/body", wire)
	invalidID := record("/invalid", nil)
	unknownID := record("/unknown", nil)
	imageID := record("/image", nil)

	t.Run("paged-bytes-metadata-and-legacy-binding", func(t *testing.T) {
		task := NewUICaptureBodyTask()
		defer task.Close()
		first := uiCaptureBodyRead(t, task, bodyID, true, 0, false)
		if first.Source != "decoded" || first.Charset != "gbk" || first.DisplayText == nil ||
			first.Message == nil || first.Message.Status != http.StatusCreated || !first.Message.BodyComplete ||
			first.Message.BodyTruncated || first.Message.CapturedBytes != int64(len(wire)) {
			t.Fatalf("binding lost current body metadata or charset: %+v", first)
		}
		legacy, err := ReadUICaptureBody(bodyID, true, 0, false)
		if err != nil || !reflect.DeepEqual(uiCaptureBodyPage(t, legacy), first) {
			t.Fatal("existing page binding changed its serialized body contract", err)
		}
		recovered, display := uiCaptureBodyCollect(t, first, func(offset int64) (string, error) {
			return task.Read(bodyID, true, offset, false)
		})
		if !bytes.Equal(recovered, body) || display != text {
			t.Fatal("JNI paging changed original GBK bytes or their decoded display text")
		}
		again := uiCaptureBodyRead(t, task, bodyID, true, 0, false)
		if !reflect.DeepEqual(again, first) {
			t.Fatal("backward paging changed the original response page")
		}
		for _, changed := range []struct {
			id       string
			response bool
			raw      bool
		}{{invalidID, true, false}, {bodyID, false, false}, {bodyID, true, true}} {
			if _, err := task.Read(changed.id, changed.response, 0, changed.raw); err == nil {
				t.Fatal("a reader changed its bound exchange, direction or source")
			}
		}
		task.Close()
		uiCaptureBodyNoScratch(t, directory)

		for _, response := range []bool{false, true} {
			rawTask := NewUICaptureBodyTask()
			raw := uiCaptureBodyRead(t, rawTask, bodyID, response, 0, true)
			rawTask.Close()
			if raw.Source != "raw" || raw.Encoding != "base64" || raw.DisplayText != nil || raw.HasMore ||
				raw.Message == nil || raw.Message.CapturedBytes != int64(len(wire)) ||
				!bytes.Equal(uiCaptureBodyBytes(t, raw), wire) {
				t.Fatal("raw request/response binding did not preserve exact compressed bytes")
			}
		}
	})

	t.Run("complete-json-view-is-private-atomic-and-paged", func(t *testing.T) {
		task := NewUICaptureBodyTask()
		defer task.Close()
		viewDirectory := t.TempDir()
		path := filepath.Join(viewDirectory, "body.view")
		resultJSON, err := task.PrepareView(path, bodyID, true, "json")
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Format  string            `json:"format"`
			Message httpdebug.Message `json:"message"`
			Page    httpdebug.Preview `json:"page"`
		}
		if err := json.Unmarshal([]byte(resultJSON), &result); err != nil || result.Format != "json" ||
			result.Message.CapturedBytes != int64(len(wire)) || !result.Message.BodyComplete ||
			result.Page.Source != "view" || result.Page.Message != nil || !result.Page.HasMore {
			t.Fatal("prepared view lost its source metadata or initial page", err)
		}
		derived, _ := uiCaptureBodyCollect(t, result.Page, task.ReadView)
		if !bytes.Equal(derived, formatted.Bytes()) {
			t.Fatal("complete view binding lost Unicode text, a numeric literal or later pages")
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatal("derived body file was not private", err)
		}
		uiCaptureBodyNoScratch(t, directory, viewDirectory)
		for _, target := range []struct{ id, format string }{{invalidID, "json"}, {unknownID, "json"}, {bodyID, "unsupported"}} {
			if _, err := task.PrepareView(path, target.id, true, target.format); err == nil {
				t.Fatal("invalid full view was published", target)
			}
			retained, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(retained, formatted.Bytes()) {
				t.Fatal("failed preparation replaced the last complete derived file", err)
			}
			uiCaptureBodyNoScratch(t, directory, viewDirectory)
		}
		if _, err := task.ReadView(0); err != nil {
			t.Fatal("failed replacement invalidated the previous valid view", err)
		}
		task.Close()
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("normal task closure retained its derived body", err)
		}
		uiCaptureBodyNoScratch(t, directory, viewDirectory)
	})

	t.Run("image-view-returns-a-file-and-removes-the-previous-view", func(t *testing.T) {
		task := NewUICaptureBodyTask()
		defer task.Close()
		viewDirectory := t.TempDir()
		previous := filepath.Join(viewDirectory, "previous.view")
		if _, err := task.PrepareView(previous, bodyID, true, "json"); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(viewDirectory, "image.view")
		resultJSON, err := task.PrepareView(path, imageID, true, "image")
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal([]byte(resultJSON), &result); err != nil {
			t.Fatal(err)
		}
		var returnedPath string
		if err := json.Unmarshal(result["path"], &returnedPath); err != nil || returnedPath != path || result["page"] != nil {
			t.Fatal("image preparation did not return only its private file path", err)
		}
		retained, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(retained, image) {
			t.Fatal("image binding changed complete HTTP-decoded image bytes", err)
		}
		if _, err := os.Stat(previous); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("successful replacement retained the previous view", err)
		}
		if _, err := task.ReadView(0); err == nil {
			t.Fatal("image view was exposed as a derived text page")
		}
		task.Close()
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("task closure retained the prepared image", err)
		}
		uiCaptureBodyNoScratch(t, directory, viewDirectory)
	})

	t.Run("cancel-and-close-reject-all-future-operations", func(t *testing.T) {
		task := NewUICaptureBodyTask()
		defer task.Close()
		_ = uiCaptureBodyRead(t, task, bodyID, true, 0, false)
		viewDirectory := t.TempDir()
		path := filepath.Join(viewDirectory, "cancelled.view")
		if _, err := task.PrepareView(path, bodyID, true, "json"); err != nil {
			t.Fatal(err)
		}
		task.Cancel()
		for _, operation := range []func() (string, error){
			func() (string, error) { return task.Read(bodyID, true, 0, false) },
			func() (string, error) { return task.PrepareView(path, bodyID, true, "json") },
			func() (string, error) { return task.ReadView(0) },
		} {
			if _, err := operation(); !errors.Is(err, context.Canceled) {
				t.Fatal("cancelled task admitted a body operation", err)
			}
		}
		task.Close()
		task.Close()
		if _, err := task.Read(bodyID, true, 0, false); !errors.Is(err, context.Canceled) {
			t.Fatal("closed task resumed its original body reader", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("cancelled task retained its derived file after close", err)
		}
		uiCaptureBodyNoScratch(t, directory, viewDirectory)
	})

	t.Run("clearing-records-invalidates-cached-pages-and-views", func(t *testing.T) {
		task := NewUICaptureBodyTask()
		defer task.Close()
		_ = uiCaptureBodyRead(t, task, bodyID, true, 0, false)
		viewDirectory := t.TempDir()
		path := filepath.Join(viewDirectory, "cleared.view")
		if _, err := task.PrepareView(path, bodyID, true, "json"); err != nil {
			t.Fatal(err)
		}
		if err := ClearUICapture(); err != nil {
			t.Fatal(err)
		}
		if _, err := task.ReadView(0); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("a cached view revived an explicitly removed record", err)
		}
		if _, err := task.Read(bodyID, true, 0, false); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("a decoded page cache revived an explicitly removed record", err)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("failed view retention check left its derived file readable", err)
		}
		if store.Page(0, "").Total != 0 {
			t.Fatal("binding clear did not remove the retained exchanges")
		}
		task.Close()
		uiCaptureBodyNoScratch(t, directory, viewDirectory)
	})
}

func uiCaptureBodyGzip(t *testing.T, body []byte) []byte {
	t.Helper()
	var wire bytes.Buffer
	writer := gzip.NewWriter(&wire)
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return wire.Bytes()
}

func uiCaptureBodyRead(t *testing.T, task *UICaptureBodyTask, id string, response bool, offset int64, raw bool) httpdebug.Preview {
	t.Helper()
	value, err := task.Read(id, response, offset, raw)
	if err != nil {
		t.Fatal(err)
	}
	return uiCaptureBodyPage(t, value)
}

func uiCaptureBodyPage(t *testing.T, value string) httpdebug.Preview {
	t.Helper()
	var page httpdebug.Preview
	if err := json.Unmarshal([]byte(value), &page); err != nil {
		t.Fatal(err)
	}
	return page
}

func uiCaptureBodyBytes(t *testing.T, page httpdebug.Preview) []byte {
	t.Helper()
	switch page.Encoding {
	case "text":
		return []byte(page.Text)
	case "base64":
		data, err := base64.StdEncoding.DecodeString(page.Text)
		if err != nil {
			t.Fatal(err)
		}
		return data
	default:
		t.Fatalf("invalid body page encoding %q", page.Encoding)
		return nil
	}
}

func uiCaptureBodyCollect(t *testing.T, page httpdebug.Preview, read func(int64) (string, error)) ([]byte, string) {
	t.Helper()
	var recovered bytes.Buffer
	var display strings.Builder
	source := page.Source
	offset := int64(0)
	for number := 0; ; number++ {
		data := uiCaptureBodyBytes(t, page)
		if page.Source != source || page.Offset != offset || page.NextOffset != offset+int64(len(data)) ||
			len(data) > httpdebug.BodyPageSize || page.Truncated || page.Error != "" {
			t.Fatalf("body binding returned inconsistent pagination: %+v", page)
		}
		if source == "view" && (page.Message != nil || page.Encoding != "text") {
			t.Fatal("derived view page claimed original body metadata or binary text")
		}
		if source == "decoded" && (page.Message == nil || page.Charset != "gbk" || page.DisplayText == nil) {
			t.Fatal("decoded page lost its original message or validated display text")
		}
		recovered.Write(data)
		if page.DisplayText != nil {
			display.WriteString(*page.DisplayText)
		} else if page.Encoding == "text" {
			display.WriteString(page.Text)
		}
		if !page.HasMore {
			return recovered.Bytes(), display.String()
		}
		if page.NextOffset <= offset || number >= 128 {
			t.Fatal("body binding did not advance through its saved pages")
		}
		offset = page.NextOffset
		value, err := read(offset)
		if err != nil {
			t.Fatal(err)
		}
		page = uiCaptureBodyPage(t, value)
	}
}

func uiCaptureBodyNoScratch(t *testing.T, directories ...string) {
	t.Helper()
	for _, directory := range directories {
		entries, err := os.ReadDir(directory)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "body-preview-") || strings.HasPrefix(entry.Name(), "body-view-") {
				t.Fatalf("body binding retained temporary file %s", filepath.Join(directory, entry.Name()))
			}
		}
	}
}
