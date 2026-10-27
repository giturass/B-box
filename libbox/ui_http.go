package libbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const uiBackupLimit = 64 * 1024 * 1024

// UIHTTPResponse retains WebDAV's status codes without changing HTTPRequest.Execute.
type UIHTTPResponse struct {
	Status  int32
	Content []byte
}

// UIWebDAVRequest uses libbox's Android certificate store and modern TLS.
// Credentials and write bodies never follow a redirect to another origin.
func UIWebDAVRequest(method, link, username, password string, content []byte) (*UIHTTPResponse, error) {
	return uiHTTPRequest(method, link, username, password, content, uiBackupLimit, 30*time.Second, false)
}

// UIHTTPGet bounds release metadata reads separately from backup files.
func UIHTTPGet(link string) (*UIHTTPResponse, error) {
	// GitHub release assets redirect to a separate HTTPS asset origin. This
	// anonymous GET carries neither a backup body nor account credentials.
	return uiHTTPRequest(http.MethodGet, link, "", "", nil, 2*1024*1024, 15*time.Second, true)
}

func uiHTTPRequest(method, link, username, password string, content []byte, limit int64, timeout time.Duration, anonymousRedirects bool) (*UIHTTPResponse, error) {
	switch method {
	case http.MethodGet, http.MethodPut, http.MethodOptions, "MKCOL":
	default:
		return nil, fmt.Errorf("unsupported WebDAV method")
	}
	if int64(len(content)) > limit || len(username) > 4096 || len(password) > 4096 {
		return nil, fmt.Errorf("WebDAV request exceeds size limit")
	}
	if anonymousRedirects && (method != http.MethodGet || username != "" || password != "" || len(content) != 0) {
		return nil, fmt.Errorf("only anonymous metadata GET can redirect to another origin")
	}
	root, err := url.Parse(link)
	if err != nil || root == nil || root.Hostname() == "" || root.User != nil || root.Fragment != "" ||
		(root.Scheme != "http" && root.Scheme != "https") {
		return nil, fmt.Errorf("invalid HTTP or HTTPS address")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, root.String(), bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("invalid HTTP request")
	}
	request.Header.Set("User-Agent", "B-box")
	if method == http.MethodPut {
		request.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	if username != "" || password != "" {
		request.SetBasicAuth(username, password)
	}
	client := NewHTTPClient().(*httpClient)
	defer client.Close()
	client.ModernTLS()
	client.client.Timeout = timeout
	client.client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("too many HTTP redirects")
		}
		if next.URL.User != nil || next.URL.Hostname() == "" || (next.URL.Scheme != "https" && next.URL.Scheme != "http") {
			return fmt.Errorf("invalid HTTP redirect address")
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && next.URL.Scheme != "https" {
			return fmt.Errorf("HTTP redirect downgraded HTTPS")
		}
		if !anonymousRedirects && !uiSameOrigin(root, next.URL) {
			return fmt.Errorf("HTTP redirect changed origin")
		}
		// A 301/302/303 can turn a PUT or OPTIONS into GET. Keep the response
		// visible to the caller so a failed write is never reported as success.
		if next.Method != method {
			return http.ErrUseLastResponse
		}
		if anonymousRedirects {
			next.Header.Del("Authorization")
			next.Header.Del("Proxy-Authorization")
			next.Header.Del("Cookie")
		}
		return nil
	}
	response, err := client.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.ContentLength > limit {
		return nil, fmt.Errorf("HTTP response exceeds size limit")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("HTTP response exceeds size limit")
	}
	return &UIHTTPResponse{Status: int32(response.StatusCode), Content: body}, nil
}

func uiSameOrigin(a, b *url.URL) bool {
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		if u.Scheme == "https" {
			return "443"
		}
		return "80"
	}
	return a.Scheme == b.Scheme && strings.EqualFold(a.Hostname(), b.Hostname()) && port(a) == port(b)
}
