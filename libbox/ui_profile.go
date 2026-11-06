package libbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Keep this in sync with Android's ProfileContent validation limit.
const uiProfileLimit = 16 * 1024 * 1024
const uiProfileTimeout = 30 * time.Second

// UIProfileGet bounds a subscription's entire download, including redirects and
// the decoded response body. It retains the caller's subscription User-Agent,
// URL authentication and the upstream HTTP client's normal redirect handling.
func UIProfileGet(link, userAgent string) (*StringBox, error) {
	return uiProfileGet(link, userAgent, uiProfileTimeout)
}

func uiProfileGet(link, userAgent string, timeout time.Duration) (*StringBox, error) {
	root, err := url.Parse(link)
	if err != nil || root == nil || root.Hostname() == "" || (root.Scheme != "http" && root.Scheme != "https") {
		return nil, fmt.Errorf("invalid HTTP or HTTPS subscription address")
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, root.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", userAgent)
	client := NewHTTPClient().(*httpClient)
	defer client.Close()
	client.ModernTLS()
	client.client.Timeout = timeout
	response, err := client.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	// Failed requests must not read an unbounded (or stalled) error body.
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", response.Status)
	}
	if response.ContentLength > uiProfileLimit {
		return nil, fmt.Errorf("Subscription exceeds 16 MiB")
	}
	content, err := io.ReadAll(io.LimitReader(response.Body, uiProfileLimit+1))
	if err != nil {
		return nil, err
	}
	if len(content) > uiProfileLimit {
		return nil, fmt.Errorf("Subscription exceeds 16 MiB")
	}
	return wrapString(string(content)), nil
}
