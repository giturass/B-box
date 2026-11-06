package libbox

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestUIProfileGetPreservesSubscriptionRequests(t *testing.T) {
	const userAgent = "SFA (sing-box test; language zh_CN)"
	const content = `{"log":{"level":"warn"}}`
	asset := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.UserAgent() != userAgent || r.URL.Query().Get("token") != "subscription" {
			t.Error("subscription method, user agent or URL query changed")
		}
		_, _ = io.WriteString(w, content)
	}))
	defer asset.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth" {
			user, password, ok := r.BasicAuth()
			if !ok || user != "user" || password != "password" || r.UserAgent() != userAgent {
				t.Error("subscription authentication or user agent changed")
			}
			_, _ = io.WriteString(w, content)
			return
		}
		http.Redirect(w, r, asset.URL+"/profile.json?token=subscription", http.StatusFound)
	}))
	defer server.Close()
	auth, _ := url.Parse(server.URL + "/auth")
	auth.User = url.UserPassword("user", "password")
	for _, link := range []string{auth.String(), server.URL + "/redirect"} {
		result, err := UIProfileGet(link, userAgent)
		if err != nil || result == nil || result.Value != content {
			t.Fatalf("subscription download: result=%v err=%v", result, err)
		}
	}
}

func TestUIProfileGetLimitsDeclaredChunkedAndDecodedBodies(t *testing.T) {
	content := bytes.Repeat([]byte(" "), uiProfileLimit+1)
	copy(content, "{}")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		size, _ := strconv.Atoi(r.URL.Query().Get("size"))
		switch r.URL.Path {
		case "/declared":
			w.Header().Set("Content-Length", strconv.Itoa(size))
		case "/chunked":
			w.(http.Flusher).Flush()
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			compressed := gzip.NewWriter(w)
			_, _ = compressed.Write(content[:size])
			_ = compressed.Close()
			return
		}
		_, _ = w.Write(content[:size])
	}))
	defer server.Close()
	for _, mode := range []string{"declared", "chunked", "gzip"} {
		for _, size := range []int{uiProfileLimit, uiProfileLimit + 1} {
			t.Run(mode+"/"+strconv.Itoa(size), func(t *testing.T) {
				result, err := UIProfileGet(server.URL+"/"+mode+"?size="+strconv.Itoa(size), "SFA test")
				if size > uiProfileLimit {
					if err == nil || result != nil || !strings.Contains(err.Error(), "Subscription exceeds 16 MiB") {
						t.Fatalf("oversized subscription: result present=%v err=%v", result != nil, err)
					}
				} else if err != nil || result == nil || len(result.Value) != size || !strings.HasPrefix(result.Value, "{}") {
					t.Fatalf("subscription at the size limit: result present=%v err=%v", result != nil, err)
				}
			})
		}
	}
}

func TestUIProfileGetDeadlineIncludesHeadersAndBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			select {
			case <-r.Context().Done():
			case <-time.After(40 * time.Millisecond):
				http.Redirect(w, r, "/redirect", http.StatusFound)
			}
			return
		}
		if r.URL.Path != "/headers" {
			w.(http.Flusher).Flush()
		}
		if r.URL.Path != "/trickle" {
			<-r.Context().Done()
			return
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-ticker.C:
				if _, err := io.WriteString(w, " "); err != nil {
					return
				}
				w.(http.Flusher).Flush()
			}
		}
	}))
	defer server.Close()
	for _, path := range []string{"/headers", "/body", "/trickle", "/redirect"} {
		t.Run(path, func(t *testing.T) {
			result, err := uiProfileGet(server.URL+path, "SFA test", 100*time.Millisecond)
			if result != nil || !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("download deadline did not stop the request: result=%v err=%v", result, err)
			}
		})
	}
}

func TestUIProfileGetRejectsErrorAndOversizedHeadersWithoutReading(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(uiProfileLimit+1))
		if r.URL.Path == "/error" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	for path, expected := range map[string]string{
		"/error": "HTTP 503 Service Unavailable",
		"/large": "Subscription exceeds 16 MiB",
	} {
		result, err := uiProfileGet(server.URL+path, "SFA test", time.Second)
		if result != nil || err == nil || err.Error() != expected {
			t.Fatalf("response headers must fail without waiting for a body: result=%v err=%v", result, err)
		}
	}
}

func TestUIProfileGetRejectsRedirectLoopsAndInvalidAddresses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer server.Close()
	for _, link := range []string{server.URL, "file:///profile.json", "https:///profile.json", "://invalid"} {
		if result, err := UIProfileGet(link, "SFA test"); result != nil || err == nil {
			t.Fatalf("invalid subscription or redirect loop accepted: %s", link)
		}
	}
}
