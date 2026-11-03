package httpdebug

import (
	"crypto"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
)

var (
	managerMu        sync.Mutex
	store            *Store
	active           atomic.Pointer[Engine]
	lastCaptureState State
	profileRewrite   *PreparedRewrite
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

func Start(directory, configJSON, certificate string, signer crypto.Signer) error {
	var config Config
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		return err
	}
	var ca *authority
	if config.HTTPS {
		var err error
		ca, err = loadAuthority(certificate, signer)
		if err != nil {
			return fmt.Errorf("load MITM CA: %w", err)
		}
	}
	managerMu.Lock()
	defer managerMu.Unlock()
	if current := active.Load(); current != nil && current.captureEnabled {
		return errors.New("stop capture before changing its settings")
	}
	if err := openLocked(directory); err != nil {
		return err
	}
	engine := newEngine(config, store)
	engine.ca = ca
	if profileRewrite != nil {
		engine.rewrite = profileRewrite.program
		if engine.ca == nil {
			engine.ca = profileRewrite.ca
		}
	}
	lastCaptureState = State{}
	if previous := active.Swap(engine); previous != nil {
		previous.Close()
	}
	AppendCaptureLog("info", fmt.Sprintf("%s capture started; ports=%v; body limit=%d bytes; reconnect target applications", engine.backend(), config.Ports, config.BodyLimit))
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
	current := active.Load()
	if current == nil || !current.captureEnabled {
		return
	}
	var replacement *Engine
	if profileRewrite != nil {
		replacement = newRewriteEngine(profileRewrite)
	}
	if engine := active.Swap(replacement); engine != nil {
		engine.Close()
		lastCaptureState = engine.captureState()
		AppendCaptureLog("info", "Capture recording stopped; profile rewrite rules remain active; reconnect intercepted connections")
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
	Backend              string `json:"backend"`
	Error                string `json:"error,omitempty"`
	Active               bool   `json:"active"`
	RewriteActive        bool   `json:"rewriteActive"`
	RewriteRules         int    `json:"rewriteRules"`
	Connections          int    `json:"connections"`
	Skipped              int64  `json:"skipped"`
	TLSOpened            int64  `json:"tlsOpened"`
	TLSFailed            int64  `json:"tlsFailed"`
	BlockedQUIC          int64  `json:"blockedQUIC"`
	PlaintextConnections int64  `json:"plaintextConnections"`
	PlaintextBytes       int64  `json:"plaintextBytes"`
	ParsedRequests       int64  `json:"parsedRequests"`
	FilteredRequests     int64  `json:"filteredRequests"`
	RecordedRequests     int64  `json:"recordedRequests"`
}

func Status() State {
	managerMu.Lock()
	defer managerMu.Unlock()
	engine := Current()
	if engine == nil {
		return lastCaptureState
	}
	return engine.captureState()
}

func (engine *Engine) captureState() State {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	warning := ""
	if value := engine.warning.Load(); value != nil {
		warning = *value
	}
	rules := 0
	if engine.rewrite != nil {
		rules = engine.rewrite.count
	}
	return State{Active: !engine.closed && engine.captureEnabled, RewriteActive: !engine.closed && engine.rewrite != nil, RewriteRules: rules, Backend: engine.backend(), Error: warning,
		Connections: len(engine.connections) + engine.preparing,
		Skipped:     engine.skipped.Load(), TLSFailed: engine.tlsFailed.Load(), BlockedQUIC: engine.blockedQUIC.Load(),
		TLSOpened:            engine.tlsOpened.Load(),
		PlaintextConnections: engine.plaintextConnections.Load(), PlaintextBytes: engine.plaintextBytes.Load(),
		ParsedRequests: engine.parsedRequests.Load(), FilteredRequests: engine.filteredRequests.Load(), RecordedRequests: engine.recordedRequests.Load()}
}

func (engine *Engine) backend() string {
	if engine.config.HTTPS || engine.rewrite != nil && engine.rewrite.scope.HTTPS {
		return "MITM"
	}
	return "HTTP"
}
