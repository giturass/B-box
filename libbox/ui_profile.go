package libbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// UIProfileGet retains the caller's subscription User-Agent and URL
// authentication. Android uses UIHTTPTask.ProfileGet to propagate cancellation.
func UIProfileGet(link, userAgent string) (*StringBox, error) {
	task := NewUIHTTPTask()
	defer task.Close()
	return task.ProfileGet(link, userAgent)
}

func (t *UIHTTPTask) ProfileGet(link, userAgent string) (*StringBox, error) {
	return uiProfileGet(t.ctx, link, userAgent)
}

func uiProfileGet(ctx context.Context, link, userAgent string) (*StringBox, error) {
	root, err := url.Parse(link)
	if err != nil || root == nil || root.Hostname() == "" || (root.Scheme != "http" && root.Scheme != "https") {
		return nil, fmt.Errorf("invalid HTTP or HTTPS subscription address")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, root.String(), nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", userAgent)
	client := NewHTTPClient().(*httpClient)
	defer client.Close()
	client.ModernTLS()
	client.transport.TLSHandshakeTimeout = 0
	client.client.CheckRedirect = uiRedirectLoop
	response, err := client.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	// A failed request does not need an error body to report the HTTP status.
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s", response.Status)
	}
	content, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	return wrapString(string(content)), nil
}
