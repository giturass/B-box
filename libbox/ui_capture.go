package libbox

import (
	"encoding/json"
	"errors"
	"os"
	"sync"

	"github.com/sagernet/sing-box/experimental/httpdebug"
)

// Serialize capture start with core reload/stop. All read methods are bounded
// and available with the local service stopped; capture itself never auto-starts.
var uiCaptureLifecycle sync.Mutex

func ValidateUICaptureConfig(configJSON string) (string, error) {
	return httpdebug.Validate(configJSON)
}

func OpenUICapture(directory string) error { return httpdebug.Open(directory) }

func (s *CommandServer) StartUICapture(directory, configJSON, certificate string, signer UICaptureSigner) error {
	uiCaptureLifecycle.Lock()
	defer uiCaptureLifecycle.Unlock()
	if s.Instance() == nil {
		return os.ErrClosed
	}
	// Parameters are retained for existing generated callers. The profile's
	// independent MITM settings and CA exclusively control TLS decryption.
	return httpdebug.Start(directory, configJSON, "", nil)
}

func StopUICapture() { httpdebug.Stop() }

func ReadUICaptureStatus() (string, error) { return captureJSON(httpdebug.Status()) }

func ListUICapture(limit int32, query string) (string, error) {
	store, err := httpdebug.Saved()
	if err != nil {
		return "", err
	}
	return captureJSON(store.Page(int(limit), query))
}

func QueryUICapture(optionsJSON string) (string, error) {
	query, err := httpdebug.DecodeCaptureQuery(optionsJSON)
	if err != nil {
		return "", err
	}
	store, err := httpdebug.Saved()
	if err != nil {
		return "", err
	}
	page, err := store.Query(query)
	if err != nil {
		return "", err
	}
	return captureJSON(page)
}

func SelectUICaptureHAR(optionsJSON string) (string, error) {
	query, err := httpdebug.DecodeCaptureQuery(optionsJSON)
	if err != nil {
		return "", err
	}
	store, err := httpdebug.Saved()
	if err != nil {
		return "", err
	}
	ids, err := store.SelectHAR(query)
	if err != nil {
		return "", err
	}
	return captureJSON(ids)
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
	return captureJSON(detail)
}

func ReadUICaptureBody(id string, response bool, offset int64, raw bool) (string, error) {
	store, err := httpdebug.Saved()
	if err != nil {
		return "", err
	}
	page, err := store.BodyPage(id, response, offset, raw)
	if err != nil {
		return "", err
	}
	return captureJSON(page)
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
	return exportUICaptureFile(path, func(file *os.File) error { return store.WriteHAR(file, query) })
}

func ExportUICaptureHARSelection(path, idsJSON string) error {
	var ids []string
	if err := json.Unmarshal([]byte(idsJSON), &ids); err != nil {
		return err
	}
	if ids == nil {
		return errors.New("missing HAR request selection")
	}
	store, err := httpdebug.Saved()
	if err != nil {
		return err
	}
	return exportUICaptureFile(path, func(file *os.File) error { return store.WriteHARSelection(file, ids) })
}

func exportUICaptureFile(path string, write func(*os.File) error) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	err = write(file)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

func ExportUICaptureCurlFile(path, id string) error {
	store, err := httpdebug.Saved()
	if err != nil {
		return err
	}
	return exportUICaptureFile(path, func(file *os.File) error { return store.WriteCurl(file, id) })
}

func ExportUICaptureCurl(id string) (string, error) {
	store, err := httpdebug.Saved()
	if err != nil {
		return "", err
	}
	return store.Curl(id)
}

func captureJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}
