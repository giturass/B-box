package httpdebug

import (
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
)

var (
	managerMu sync.Mutex
	store     *Store
	active    atomic.Pointer[Engine]
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

func Start(directory, configJSON, certificate, privateKey string) error {
	var config Config
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return err
	}
	if err := config.validate(); err != nil {
		return err
	}
	var ca *authority
	if config.HTTPS {
		var err error
		ca, err = loadAuthority(certificate, privateKey)
		if err != nil {
			return err
		}
	}
	managerMu.Lock()
	defer managerMu.Unlock()
	if active.Load() != nil {
		return errors.New("stop capture before changing its settings")
	}
	if err := openLocked(directory); err != nil {
		return err
	}
	active.Store(newEngine(config, ca, store))
	return nil
}

// Validate returns normalized settings without enabling capture or generating a CA.
func Validate(configJSON string) (string, error) {
	var config Config
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return "", err
	}
	if err := config.validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(config)
	return string(data), err
}

func Stop() {
	managerMu.Lock()
	defer managerMu.Unlock()
	if engine := active.Swap(nil); engine != nil {
		engine.Close()
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
	Active      bool  `json:"active"`
	Connections int   `json:"connections"`
	Skipped     int64 `json:"skipped"`
	BlockedQUIC int64 `json:"blockedQUIC"`
}

func Status() State {
	engine := Current()
	if engine == nil {
		return State{}
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	return State{Active: !engine.closed, Connections: len(engine.connections),
		Skipped: engine.skipped.Load(), BlockedQUIC: engine.blockedQUIC.Load()}
}
