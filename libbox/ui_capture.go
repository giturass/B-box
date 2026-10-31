package libbox

import (
	"bytes"
	"encoding/json"
	"os"
	"sync"

	"github.com/sagernet/sing-box/experimental/httpdebug"
	"github.com/sagernet/sing-box/option"
	corejson "github.com/sagernet/sing/common/json"
)

// Serialize capture start with core reload/stop. All read methods are bounded
// and available with the local service stopped; capture itself never auto-starts.
var uiCaptureLifecycle sync.Mutex

type UICaptureCA struct {
	Certificate string
	PrivateKey  string
}

func GenerateUICaptureCA() (*UICaptureCA, error) {
	certificate, key, err := httpdebug.GenerateCA()
	if err != nil {
		return nil, err
	}
	return &UICaptureCA{Certificate: certificate, PrivateKey: key}, nil
}

func ValidateUICaptureConfig(configJSON string) (string, error) {
	return httpdebug.Validate(configJSON)
}

// An empty result distinguishes a profile without HTTP settings from an
// explicitly configured empty object, for migration of the old local settings.
func ReadUICaptureConfig(configContent string) (string, error) {
	options, err := parseConfig(baseContext(nil), configContent)
	if err != nil {
		return "", err
	}
	if options.Experimental == nil || options.Experimental.HTTPDebug == nil {
		return "", nil
	}
	return captureJSON(options.Experimental.HTTPDebug)
}

func SetUICaptureConfig(configContent, configJSON string) (*StringBox, error) {
	options, err := parseConfig(baseContext(nil), configContent)
	if err != nil {
		return nil, err
	}
	var config httpdebug.Config
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return nil, err
	}
	if options.Experimental == nil {
		options.Experimental = &option.ExperimentalOptions{}
	}
	options.Experimental.HTTPDebug = (*option.HTTPDebugOptions)(&config)
	var buffer bytes.Buffer
	encoder := corejson.NewEncoder(&buffer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(options); err != nil {
		return nil, err
	}
	return wrapString(buffer.String()), nil
}

func OpenUICapture(directory string) error { return httpdebug.Open(directory) }

func (s *CommandServer) StartUICapture(directory, configJSON, certificate, privateKey string) error {
	uiCaptureLifecycle.Lock()
	defer uiCaptureLifecycle.Unlock()
	if s.Instance() == nil {
		return os.ErrClosed
	}
	return httpdebug.Start(directory, configJSON, certificate, privateKey)
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

func captureJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}
