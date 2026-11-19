package libbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type uiDownloadProgressTest struct {
	values [][2]int64
}

func (p *uiDownloadProgressTest) Update(downloaded, total int64) {
	p.values = append(p.values, [2]int64{downloaded, total})
}

func TestUIHTTPDownloadStreamsDigestAndProgress(t *testing.T) {
	content := bytes.Repeat([]byte("release APK fixture"), 32*1024)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "" || r.Header.Get("Accept-Encoding") != "identity" {
			t.Error("release request must be anonymous and preserve the raw asset bytes")
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(content)))
		_, _ = w.Write(content)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "release.part")
	progress := new(uiDownloadProgressTest)
	result, err := uiHTTPDownload(context.Background(), server.Client(), server.URL, path, int64(len(content)), progress)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	if result.Status != 200 || result.Size != int64(len(content)) || result.Digest != hex.EncodeToString(digest[:]) {
		t.Fatalf("incorrect completed download: %+v", result)
	}
	stored, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(stored, content) {
		t.Fatalf("incomplete staging file: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("staging file is not private: %v %v", info, err)
	}
	if len(progress.values) < 3 || progress.values[0] != [2]int64{0, int64(len(content))} || progress.values[len(progress.values)-1][0] != result.Size {
		t.Fatal("download did not report streamed progress", progress.values)
	}
	for index, value := range progress.values {
		if value[1] != result.Size || value[0] > result.Size || index > 0 && value[0] < progress.values[index-1][0] {
			t.Fatal("invalid download progress", progress.values)
		}
	}
}

func TestUIHTTPDownloadRejectsIncorrectLengthsAndKeepsExistingFiles(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/declared" {
			w.Header().Set("Content-Length", "6")
		} else {
			w.(http.Flusher).Flush()
		}
		_, _ = io.WriteString(w, "123456")
	}))
	defer server.Close()
	for _, test := range []struct {
		path string
		size int64
	}{
		{"/declared", 5}, {"/declared", 7}, {"/chunked", 5}, {"/chunked", 7},
	} {
		t.Run(test.path+strconv.FormatInt(test.size, 10), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "release.part")
			if result, err := uiHTTPDownload(context.Background(), server.Client(), server.URL+test.path, path, test.size, nil); err == nil || result != nil {
				t.Fatal("incorrect asset size was accepted", result, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed download retained its staging file", err)
			}
		})
	}
	path := filepath.Join(t.TempDir(), "existing.apk")
	if err := os.WriteFile(path, []byte("previous verified APK"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := uiHTTPDownload(context.Background(), server.Client(), server.URL+"/declared", path, 6, nil); !errors.Is(err, os.ErrExist) {
		t.Fatal("download overwrote an existing file", err)
	}
	stored, _ := os.ReadFile(path)
	if string(stored) != "previous verified APK" {
		t.Fatal("failed exclusive creation changed the previous APK")
	}
}

func TestUIHTTPDownloadRejectsUnsafeAddressesAndRedirects(t *testing.T) {
	var leaked atomic.Bool
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(true)
		_, _ = io.WriteString(w, "apk")
	}))
	defer plain.Close()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/downgrade" {
			http.Redirect(w, r, plain.URL, http.StatusFound)
		} else {
			http.Redirect(w, r, "/loop", http.StatusTemporaryRedirect)
		}
	}))
	defer server.Close()
	for _, link := range []string{
		plain.URL, strings.Replace(server.URL, "https://", "https://account:secret@", 1),
		server.URL + "#fragment", server.URL + "/downgrade", server.URL + "/loop",
	} {
		path := filepath.Join(t.TempDir(), "release.part")
		if _, err := uiHTTPDownload(context.Background(), server.Client(), link, path, 3, nil); err == nil {
			t.Fatal("unsafe release address was accepted", link)
		}
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("unsafe request created a staging file", err)
		}
	}
	if leaked.Load() {
		t.Fatal("HTTPS downgrade followed the insecure redirect")
	}
	for _, size := range []int64{0, -1, math.MaxInt64} {
		if _, err := uiHTTPDownload(context.Background(), server.Client(), server.URL, filepath.Join(t.TempDir(), "release.part"), size, nil); err == nil {
			t.Fatal("invalid asset size was accepted", size)
		}
	}
}

func TestUIHTTPDownloadTaskCancellationRemovesStagingFile(t *testing.T) {
	for _, body := range []bool{false, true} {
		t.Run(strconv.FormatBool(body), func(t *testing.T) {
			started := make(chan struct{})
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if body {
					w.(http.Flusher).Flush()
					_, _ = io.WriteString(w, "partial")
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
			}))
			defer server.Close()
			task := NewUIHTTPTask()
			defer task.Close()
			path := filepath.Join(t.TempDir(), "release.part")
			finished := make(chan error, 1)
			go func() {
				_, err := uiHTTPDownload(task.ctx, server.Client(), server.URL, path, 100, nil)
				finished <- err
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("download did not start")
			}
			task.Close()
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("cancelled task did not propagate cancellation", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled download kept reading the response")
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("cancelled download retained its staging file", err)
			}
		})
	}
}

func TestUIHTTPTaskMetadataGetCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	task := NewUIHTTPTask()
	defer task.Close()
	finished := make(chan error, 1)
	go func() {
		_, err := task.GetWithTimeout(server.URL, 10_000)
		finished <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("metadata request did not start")
	}
	task.Close()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("metadata task did not propagate cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("metadata cancellation did not stop the response read")
	}
}
