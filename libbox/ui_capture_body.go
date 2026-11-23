package libbox

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/sagernet/sing-box/experimental/httpdebug"
)

// UICaptureBodyTask belongs to one visible body reader. Cancel interrupts an IO
// call immediately; Close releases decoder state and temporary derived files.
type UICaptureBodyTask struct {
	ctx        context.Context
	cancel     context.CancelFunc
	reader     *httpdebug.BodyReader
	viewMu     sync.Mutex
	viewPath   string
	viewFormat string
	viewStore  *httpdebug.Store
	viewID     string
}

func NewUICaptureBodyTask() *UICaptureBodyTask {
	ctx, cancel := context.WithCancel(context.Background())
	return &UICaptureBodyTask{ctx: ctx, cancel: cancel, reader: httpdebug.NewBodyReader()}
}

func (t *UICaptureBodyTask) Cancel() { t.cancel() }

func (t *UICaptureBodyTask) Close() {
	t.cancel()
	t.reader.Close()
	t.viewMu.Lock()
	defer t.viewMu.Unlock()
	if t.viewPath != "" {
		os.Remove(t.viewPath)
		t.viewPath = ""
	}
}

func (t *UICaptureBodyTask) Read(id string, response bool, offset int64, raw bool) (string, error) {
	if err := t.ctx.Err(); err != nil {
		return "", err
	}
	store, err := httpdebug.Saved()
	if err != nil {
		return "", err
	}
	page, err := t.reader.Read(t.ctx, store, id, response, offset, raw)
	if err != nil {
		return "", err
	}
	return captureJSON(page)
}

func (t *UICaptureBodyTask) PrepareView(path, id string, response bool, format string) (string, error) {
	t.viewMu.Lock()
	defer t.viewMu.Unlock()
	if err := t.ctx.Err(); err != nil {
		return "", err
	}
	store, err := httpdebug.Saved()
	if err != nil {
		return "", err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "body-view-prepare-*.tmp")
	if err != nil {
		return "", err
	}
	defer func() { file.Close(); os.Remove(file.Name()) }()
	message, err := store.WriteBodyView(t.ctx, file, id, response, format)
	if err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	if err = t.ctx.Err(); err != nil {
		return "", err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return "", err
	}
	if t.viewPath != "" && t.viewPath != path {
		os.Remove(t.viewPath)
	}
	t.viewPath, t.viewFormat = path, format
	t.viewStore, t.viewID = store, id
	if err = t.ctx.Err(); err != nil {
		os.Remove(path)
		t.viewPath = ""
		return "", err
	}
	if err = store.CheckBodyView(t.ctx, id); err != nil {
		os.Remove(path)
		t.viewPath = ""
		return "", err
	}
	result := map[string]any{"format": format, "message": message}
	if format == "image" {
		result["path"] = path
	} else {
		page, readErr := httpdebug.ReadBodyViewPage(t.ctx, path, 0)
		if readErr != nil {
			return "", readErr
		}
		result["page"] = page
	}
	value, err := captureJSON(result)
	if err == nil {
		err = store.CheckBodyView(t.ctx, id)
	}
	if err != nil {
		os.Remove(path)
		t.viewPath = ""
		return "", err
	}
	return value, nil
}

func (t *UICaptureBodyTask) ReadView(offset int64) (string, error) {
	t.viewMu.Lock()
	defer t.viewMu.Unlock()
	if err := t.ctx.Err(); err != nil {
		return "", err
	}
	if t.viewPath == "" || t.viewFormat == "image" {
		return "", errors.New("no derived text body is open")
	}
	if err := t.viewStore.CheckBodyView(t.ctx, t.viewID); err != nil {
		os.Remove(t.viewPath)
		t.viewPath = ""
		return "", err
	}
	page, err := httpdebug.ReadBodyViewPage(t.ctx, t.viewPath, offset)
	if err != nil {
		return "", err
	}
	if err := t.viewStore.CheckBodyView(t.ctx, t.viewID); err != nil {
		os.Remove(t.viewPath)
		t.viewPath = ""
		return "", err
	}
	return captureJSON(page)
}
