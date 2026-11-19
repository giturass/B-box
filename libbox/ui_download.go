package libbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// UIHTTPDownloadProgress reports bytes stored in the caller's private staging file.
type UIHTTPDownloadProgress interface {
	Update(downloaded, total int64)
}

type UIHTTPDownloadResult struct {
	Status int32
	Size   int64
	Digest string
}

// DownloadFile streams an anonymous HTTPS release asset without retaining its body
// in the binding. Android supplies the confirmed asset size and an exclusive cache
// path, then verifies the digest, APK identity and signatures before installation.
func (t *UIHTTPTask) DownloadFile(link, path string, expectedSize int64, progress UIHTTPDownloadProgress) (*UIHTTPDownloadResult, error) {
	client := NewHTTPClient().(*httpClient)
	defer client.Close()
	client.ModernTLS()
	client.transport.TLSHandshakeTimeout = 0
	return uiHTTPDownload(t.ctx, &client.client, link, path, expectedSize, progress)
}

func uiHTTPDownload(ctx context.Context, client *http.Client, link, path string, expectedSize int64, progress UIHTTPDownloadProgress) (*UIHTTPDownloadResult, error) {
	if expectedSize <= 0 || expectedSize == math.MaxInt64 || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("invalid release asset size or staging path")
	}
	root, err := url.Parse(link)
	if err != nil || root == nil || root.Scheme != "https" || root.Hostname() == "" || root.User != nil || root.Fragment != "" {
		return nil, fmt.Errorf("release assets require an anonymous HTTPS address")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, root.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("invalid release asset request")
	}
	request.Header.Set("User-Agent", "B-box")
	request.Header.Set("Accept-Encoding", "identity")
	// Copy the caller's client so tests and other requests keep their own policy.
	downloadClient := *client
	downloadClient.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if err := uiRedirectLoop(next, via); err != nil {
			return err
		}
		if next.Method != http.MethodGet || next.URL.Scheme != "https" || next.URL.Hostname() == "" || next.URL.User != nil || next.URL.Fragment != "" {
			return fmt.Errorf("invalid HTTPS release asset redirect")
		}
		next.Header.Del("Authorization")
		next.Header.Del("Proxy-Authorization")
		next.Header.Del("Cookie")
		return nil
	}
	response, err := downloadClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("release download failed: HTTP %d", response.StatusCode)
	}
	if response.ContentLength >= 0 && response.ContentLength != expectedSize {
		return nil, fmt.Errorf("release asset length differs from metadata")
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		file.Close()
		if !complete {
			os.Remove(path)
		}
	}()
	if progress != nil {
		progress.Update(0, expectedSize)
	}
	digest := sha256.New()
	writer := &uiHTTPDownloadWriter{writer: io.MultiWriter(file, digest), limit: expectedSize, progress: progress}
	written, err := io.CopyBuffer(writer, io.LimitReader(response.Body, expectedSize+1), make([]byte, 64*1024))
	if err != nil {
		return nil, err
	}
	if written != expectedSize {
		return nil, fmt.Errorf("release asset length differs from metadata")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	complete = true
	return &UIHTTPDownloadResult{Status: int32(response.StatusCode), Size: written, Digest: hex.EncodeToString(digest.Sum(nil))}, nil
}

type uiHTTPDownloadWriter struct {
	writer   io.Writer
	limit    int64
	written  int64
	progress UIHTTPDownloadProgress
}

func (w *uiHTTPDownloadWriter) Write(content []byte) (int, error) {
	if int64(len(content)) > w.limit-w.written {
		return 0, fmt.Errorf("release asset exceeds metadata size")
	}
	n, err := w.writer.Write(content)
	w.written += int64(n)
	if n > 0 && w.progress != nil {
		w.progress.Update(w.written, w.limit)
	}
	return n, err
}
