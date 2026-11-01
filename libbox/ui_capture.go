package libbox

import (
	"encoding/json"
	"golang.org/x/sys/unix"
	"os"
	"sync"

	"github.com/sagernet/sing-box/experimental/httpdebug"
)

// Serialize capture start with core reload/stop. All read methods are bounded
// and available with the local service stopped; capture itself never auto-starts.
var uiCaptureLifecycle sync.Mutex

// The source is owned by the active core session, including reload and shutdown.
type UICaptureSource interface{ Close() }

func ValidateUICaptureConfig(configJSON string) (string, error) {
	return httpdebug.Validate(configJSON)
}

func OpenUICapture(directory string) error { return httpdebug.Open(directory) }

func (s *CommandServer) StartUICapture(directory, configJSON string, fd int32, source UICaptureSource) error {
	uiCaptureLifecycle.Lock()
	defer uiCaptureLifecycle.Unlock()
	if s.Instance() == nil {
		return os.ErrClosed
	}
	var reader *os.File
	var closeSource func()
	if fd >= 0 {
		duplicate, err := unix.Dup(int(fd))
		if err != nil {
			return err
		}
		unix.CloseOnExec(duplicate)
		reader = os.NewFile(uintptr(duplicate), "ebpf-capture")
	}
	if source != nil {
		closeSource = source.Close
	}
	if reader == nil {
		return httpdebug.Start(directory, configJSON, nil, closeSource)
	}
	if err := httpdebug.Start(directory, configJSON, reader, closeSource); err != nil {
		reader.Close()
		return err
	}
	return nil
}

func StopUICapture() { httpdebug.Stop() }

func ReadUICaptureStatus() (string, error) { return captureJSON(httpdebug.Status()) }

func ReadUICaptureLogs() (string, error) {
	return captureJSON(struct {
		State httpdebug.State        `json:"state"`
		Logs  []httpdebug.CaptureLog `json:"logs"`
	}{httpdebug.Status(), httpdebug.CaptureLogs()})
}

func AppendUICaptureLog(level, message string) { httpdebug.AppendCaptureLog(level, message) }
func ClearUICaptureLogs()                      { httpdebug.ClearCaptureLogs() }

func ListUICapture(limit int32, query string) (string, error) {
	store, err := httpdebug.Saved()
	if err != nil {
		return "", err
	}
	return captureJSON(store.Page(int(limit), query))
}

func ExtractUICaptureHosts(query string) (string, error) {
	store, err := httpdebug.Saved()
	if err != nil {
		return "", err
	}
	return captureJSON(store.Domains(query))
}

func ReadUICapture(id string) (string, error) {
	store, err := httpdebug.Saved()
	if err != nil {
		return "", err
	}
	detail, err := store.Detail(id)
	if err != nil {
		return "", err
	}
	// The UI needs bounded previews; raw captured bodies go into file exports.
	detail.RequestBody, detail.ResponseBody = nil, nil
	return captureJSON(detail)
}

func ClearUICapture() error {
	store, err := httpdebug.Saved()
	if err != nil {
		return err
	}
	store.Clear()
	return nil
}

func ExportUICaptureHAR(path, query string) error {
	store, err := httpdebug.Saved()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	err = store.WriteHAR(file, query)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func ExportUICaptureCurl(id string) (string, error) {
	store, err := httpdebug.Saved()
	if err != nil {
		return "", err
	}
	return store.Curl(id)
}

func ExportUICaptureWire(path, format, query string) error {
	store, err := httpdebug.Saved()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	err = store.WriteWire(file, format, query)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func captureJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}
