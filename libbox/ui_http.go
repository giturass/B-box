package libbox

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// UIHTTPResponse retains WebDAV's status codes without changing HTTPRequest.Execute.
type UIHTTPResponse struct {
	Status  int32
	Content []byte
}

// UIHTTPTask gives the Android coroutine an explicit cancellation handle. Reads
// have no device-independent byte or time ceiling; closing the task stops both
// response headers and body reads through the request context.
type UIHTTPTask struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func NewUIHTTPTask() *UIHTTPTask {
	ctx, cancel := context.WithCancel(context.Background())
	return &UIHTTPTask{ctx: ctx, cancel: cancel}
}

func (t *UIHTTPTask) Close() { t.cancel() }

func (t *UIHTTPTask) WebDAVRequest(method, link, username, password string, content []byte) (*UIHTTPResponse, error) {
	return uiHTTPRequest(t.ctx, method, link, username, password, content, false)
}

// UIWebDAVRequest uses libbox's Android certificate store and modern TLS.
// Credentials and write bodies never follow a redirect to another origin.
func UIWebDAVRequest(method, link, username, password string, content []byte) (*UIHTTPResponse, error) {
	task := NewUIHTTPTask()
	defer task.Close()
	return task.WebDAVRequest(method, link, username, password, content)
}

// UIHTTPGet retains the anonymous metadata redirect policy.
func UIHTTPGet(link string) (*UIHTTPResponse, error) {
	return UIHTTPGetWithTimeout(link, -1)
}

// UIHTTPGetWithTimeout lets a multi-request lookup share one overall deadline.
func UIHTTPGetWithTimeout(link string, timeoutMillis int64) (*UIHTTPResponse, error) {
	if timeoutMillis != -1 && (timeoutMillis <= 0 || timeoutMillis > math.MaxInt64/int64(time.Millisecond)) {
		return nil, fmt.Errorf("metadata timeout must be positive and representable as a duration, or -1 for unlimited")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if timeoutMillis > 0 {
		var cancelTimeout context.CancelFunc
		ctx, cancelTimeout = context.WithTimeout(ctx, time.Duration(timeoutMillis)*time.Millisecond)
		defer cancelTimeout()
	}
	// GitHub release assets redirect to a separate HTTPS asset origin. This
	// anonymous GET carries neither a backup body nor account credentials.
	return uiHTTPRequest(ctx, http.MethodGet, link, "", "", nil, true)
}

func uiHTTPRequest(ctx context.Context, method, link, username, password string, content []byte, anonymousRedirects bool) (*UIHTTPResponse, error) {
	switch method {
	case http.MethodGet, http.MethodPut, http.MethodOptions, "MKCOL":
	default:
		return nil, fmt.Errorf("unsupported WebDAV method")
	}
	if anonymousRedirects && (method != http.MethodGet || username != "" || password != "" || len(content) != 0) {
		return nil, fmt.Errorf("only anonymous metadata GET can redirect to another origin")
	}
	root, err := url.Parse(link)
	if err != nil || root == nil || root.Hostname() == "" || root.User != nil || root.Fragment != "" ||
		(root.Scheme != "http" && root.Scheme != "https") {
		return nil, fmt.Errorf("invalid HTTP or HTTPS address")
	}
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
	client.transport.TLSHandshakeTimeout = 0
	client.client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if err := uiRedirectLoop(next, via); err != nil {
			return err
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
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	return &UIHTTPResponse{Status: int32(response.StatusCode), Content: body}, nil
}

func uiRedirectLoop(next *http.Request, via []*http.Request) error {
	for _, previous := range via {
		if next.Method == previous.Method && next.URL.String() == previous.URL.String() {
			return fmt.Errorf("HTTP redirect loop")
		}
	}
	return nil
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
