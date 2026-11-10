package libbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestUIWebDAVStatusAndContent(t *testing.T) {
	var stored []byte
	var collection bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "account" || password != "password" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method {
		case http.MethodOptions:
			w.WriteHeader(http.StatusNoContent)
		case "MKCOL":
			if collection {
				w.WriteHeader(http.StatusMethodNotAllowed)
			} else {
				collection = true
				w.WriteHeader(http.StatusCreated)
			}
		case http.MethodPut:
			previous := stored
			stored, _ = io.ReadAll(r.Body)
			if len(previous) == 0 {
				w.WriteHeader(http.StatusCreated)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
		case http.MethodGet:
			_, _ = w.Write(stored)
		}
	}))
	defer server.Close()
	for _, test := range []struct {
		method string
		body   []byte
		status int32
	}{
		{http.MethodOptions, nil, 204}, {"MKCOL", nil, 201}, {"MKCOL", nil, 405},
		{http.MethodPut, []byte(`{"format":"bbox-1"}`), 201},
		{http.MethodPut, []byte(`{"format":"bbox-1","profiles":[]}`), 204},
		{http.MethodGet, nil, 200},
	} {
		response, err := UIWebDAVRequest(test.method, server.URL+"/B-box/backup.json", "account", "password", test.body)
		if err != nil || response.Status != test.status {
			t.Fatalf("%s: response=%v, err=%v", test.method, response, err)
		}
		if test.method == http.MethodGet && !bytes.Equal(response.Content, stored) {
			t.Fatal("download differs from uploaded backup")
		}
	}
}

func TestUIWebDAVRedirects(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer other.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cross-origin":
			http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
		case "/preserve":
			http.Redirect(w, r, "/target", http.StatusTemporaryRedirect)
		case "/change-method":
			http.Redirect(w, r, "/target", http.StatusSeeOther)
		case "/target":
			body, _ := io.ReadAll(r.Body)
			if user, password, ok := r.BasicAuth(); r.Method != http.MethodPut || string(body) != "backup" || !ok || user != "user" || password != "secret" {
				w.WriteHeader(http.StatusBadRequest)
			} else {
				w.WriteHeader(http.StatusNoContent)
			}
		}
	}))
	defer server.Close()
	if _, err := UIWebDAVRequest(http.MethodPut, server.URL+"/cross-origin", "user", "secret", []byte("backup")); err == nil || leaked.Load() {
		t.Fatal("cross-origin redirect must stop before sending credentials or backup")
	}
	response, err := UIWebDAVRequest(http.MethodPut, server.URL+"/preserve", "user", "secret", []byte("backup"))
	if err != nil || response.Status != 204 {
		t.Fatalf("307 must preserve method, body and same-origin authentication: %v %v", response, err)
	}
	response, err = UIWebDAVRequest(http.MethodPut, server.URL+"/change-method", "user", "secret", []byte("backup"))
	if err != nil || response.Status != 303 {
		t.Fatalf("method-changing redirect must remain a failed write: %v %v", response, err)
	}
}

func TestUIWebDAVTransferBeyondFormerSizeLimit(t *testing.T) {
	content := bytes.Repeat([]byte("x"), 64*1024*1024+1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			n, err := io.Copy(io.Discard, r.Body)
			if err != nil || n != int64(len(content)) {
				t.Error("upload was incomplete", n, err)
			}
			_, _ = io.WriteString(w, strconv.FormatInt(n, 10))
		} else {
			_, _ = w.Write(content)
		}
	}))
	defer server.Close()
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		var body []byte
		if method == http.MethodPut {
			body = content
		}
		response, err := UIWebDAVRequest(method, server.URL, "", "", body)
		if err != nil {
			t.Fatal(err)
		}
		if method == http.MethodGet && !bytes.Equal(response.Content, content) || method == http.MethodPut && string(response.Content) != strconv.Itoa(len(content)) {
			t.Fatal("large WebDAV transfer was truncated")
		}
	}
}

func TestUIHTTPTaskCloseCancelsResponseHeadersAndBodies(t *testing.T) {
	for _, headers := range []bool{false, true} {
		for _, profile := range []bool{false, true} {
			started := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if headers {
					w.(http.Flusher).Flush()
				}
				close(started)
				<-r.Context().Done()
			}))
			task := NewUIHTTPTask()
			finished := make(chan error, 1)
			go func() {
				var err error
				if profile {
					_, err = task.ProfileGet(server.URL, "SFA test")
				} else {
					_, err = task.WebDAVRequest(http.MethodGet, server.URL, "", "", nil)
				}
				finished <- err
			}()
			<-started
			task.Close()
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Error("task did not propagate cancellation", err)
				}
			case <-time.After(3 * time.Second):
				t.Error("closed task retained a blocked download")
			}
			server.Close()
		}
	}
}

func TestUIHTTPGetMetadataRedirect(t *testing.T) {
	const metadata = `{"applicationId":"io.ericlee.bfa","versionCode":123}`
	var downloaded atomic.Bool
	asset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Method != http.MethodGet || len(body) != 0 || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("metadata redirect carried a body or credentials")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		downloaded.Store(true)
		_, _ = io.WriteString(w, metadata)
	}))
	defer asset.Close()
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, asset.URL+"/metadata.json", http.StatusFound)
	}))
	defer release.Close()
	response, err := UIHTTPGet(release.URL)
	if err != nil || response.Status != 200 || string(response.Content) != metadata || !downloaded.Load() {
		t.Fatalf("release metadata redirect failed: response=%v err=%v", response, err)
	}
	downloaded.Store(false)
	if _, err := UIWebDAVRequest(http.MethodGet, release.URL, "account", "password", nil); err == nil || downloaded.Load() {
		t.Fatal("WebDAV GET must still reject cross-origin redirects")
	}
	if _, err := uiHTTPRequest(context.Background(), http.MethodGet, release.URL, "account", "password", nil, true); err == nil {
		t.Fatal("metadata redirect policy accepted an authenticated request")
	}
}

func TestUIHTTPGetLargeMetadataAndRedirectLoop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/large" {
			_, _ = io.WriteString(w, strings.Repeat("x", 2*1024*1024+1))
			return
		}
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer server.Close()
	if result, err := UIHTTPGetWithTimeout(server.URL+"/large", 15001); err != nil || len(result.Content) != 2*1024*1024+1 {
		t.Fatal("large metadata was rejected or truncated", err)
	}
	if _, err := UIHTTPGet(server.URL + "/loop"); err == nil {
		t.Fatal("unbounded release redirect loop")
	}
}

func TestUIHTTPGetSharedDeadline(t *testing.T) {
	cancelled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(cancelled)
	}))
	defer server.Close()
	if _, err := UIHTTPGetWithTimeout(server.URL, 100); err == nil {
		t.Fatal("metadata timeout must cancel a pending response body")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("timed-out metadata request still owns its connection")
	}
	for _, timeout := range []int64{0, -2, math.MaxInt64} {
		if _, err := UIHTTPGetWithTimeout(server.URL, timeout); err == nil {
			t.Fatalf("accepted invalid timeout %d", timeout)
		}
	}
}
