package httpdebug

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

var (
	managerMu        sync.Mutex
	store            *Store
	active           atomic.Pointer[Engine]
	lastCaptureError string
)

// Open initializes the private store even when capture is disabled, so saved
// transactions remain readable after a process restart. Capture never auto-starts.
func Open(directory string) error {
	managerMu.Lock()
	defer managerMu.Unlock()
	return openLocked(directory)
}

func openLocked(directory string) error {
	if store != nil {
		if store.directory != directory {
			return errors.New("capture storage has already been initialized at another path")
		}
		return nil
	}
	var err error
	store, err = OpenStore(directory)
	return err
}

func Start(directory, configJSON string, reader io.ReadCloser, closeSource func()) error {
	var config Config
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return err
	}
	if config.HTTPS && reader == nil {
		return errors.New("HTTPS capture requires a running Root/eBPF collector")
	}
	managerMu.Lock()
	defer managerMu.Unlock()
	if active.Load() != nil {
		return errors.New("stop capture before changing its settings")
	}
	if err := openLocked(directory); err != nil {
		return err
	}
	engine := newEngine(config, store)
	engine.reader, engine.closeSource = reader, closeSource
	lastCaptureError = ""
	active.Store(engine)
	if reader != nil {
		AppendCaptureLog("info", fmt.Sprintf("eBPF capture connected to the local service; ports=%v; body limit=%d bytes", config.Ports, config.BodyLimit))
		go engine.readPlaintext(reader)
	}
	return nil
}

// Validate normalizes local capture settings without modifying a profile.
func Validate(configJSON string) (string, error) {
	var config Config
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return "", err
	}
	data, err := json.Marshal(config)
	return string(data), err
}

func Stop() {
	managerMu.Lock()
	defer managerMu.Unlock()
	if engine := active.Swap(nil); engine != nil {
		if engine.config.HTTPS {
			AppendCaptureLog("info", "Stopping eBPF capture and releasing the collector")
		}
		engine.Close()
		if engine.config.HTTPS {
			AppendCaptureLog("info", "eBPF capture stopped")
		}
	}
}

func Current() *Engine { return active.Load() }

func Saved() (*Store, error) {
	managerMu.Lock()
	defer managerMu.Unlock()
	if store == nil {
		return nil, errors.New("capture storage is not initialized")
	}
	return store, nil
}

type State struct {
	Backend     string `json:"backend"`
	Error       string `json:"error,omitempty"`
	Libraries   int64  `json:"libraries"`
	Scanning    bool   `json:"scanning"`
	Active      bool   `json:"active"`
	Connections int    `json:"connections"`
	Skipped     int64  `json:"skipped"`
}

func Status() State {
	managerMu.Lock()
	defer managerMu.Unlock()
	engine := Current()
	if engine == nil {
		return State{Error: lastCaptureError}
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	backend := "HTTP"
	if engine.config.HTTPS {
		backend = "eBPF"
	}
	warning := lastCaptureError
	if warning == "" {
		if value := engine.warning.Load(); value != nil {
			warning = *value
		}
	}
	return State{Active: !engine.closed, Backend: backend, Error: warning,
		Connections: len(engine.connections) + engine.passive.count(),
		Skipped:     engine.skipped.Load(), Libraries: engine.libraries.Load(), Scanning: engine.scanning.Load()}
}

func failCapture(engine *Engine, err error) {
	managerMu.Lock()
	defer managerMu.Unlock()
	if active.CompareAndSwap(engine, nil) {
		lastCaptureError = err.Error()
		AppendCaptureLog("error", lastCaptureError)
		engine.Close()
	}
}
