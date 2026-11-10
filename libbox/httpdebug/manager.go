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
	managerMu         sync.Mutex
	store             *Store
	active            atomic.Pointer[Engine]
	lastCaptureState  State
	profileProcessing *PreparedProcessing
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
	managerMu.Lock()
	defer managerMu.Unlock()
	if current := active.Load(); current != nil && current.recording.Load() != nil {
		return errors.New("stop capture before changing its settings")
	}
	if err := openLocked(directory); err != nil {
		return err
	}
	engine := active.Load()
	if engine == nil {
		engine = newEngine(config, store)
		active.Store(engine)
	} else {
		engine.mu.Lock()
		if engine.closed {
			engine.mu.Unlock()
			return errors.New("local service processing has stopped")
		}
		engine.recording.Store(&config)
		engine.mu.Unlock()
	}
	lastCaptureState = State{}
	writeLog("info", fmt.Sprintf("%s recording started at new request/stream boundaries; ports=%v; body limit=%d bytes; reconnect flows that were not already intercepted", engine.backend(), config.Ports, config.BodyLimit))
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
	if current == nil || current.recording.Load() == nil {
		return
	}
	current.stopRecording()
	lastCaptureState = current.captureState()
	writeLog("info", "Recording stopped; existing connections, profile MITM, rewrites, scripts and cron continue")
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
	MITMActive           bool   `json:"mitmActive"`
	ScriptsActive        bool   `json:"scriptsActive"`
	ScriptRules          int    `json:"scriptRules"`
	CronJobs             int    `json:"cronJobs"`
	Connections          int    `json:"connections"`
	Skipped              int64  `json:"skipped"`
	TLSOpened            int64  `json:"tlsOpened"`
	TLSFailed            int64  `json:"tlsFailed"`
	TLSNoRequest         int64  `json:"tlsNoRequest"`
	BlockedQUIC          int64  `json:"blockedQUIC"`
	PlaintextConnections int64  `json:"plaintextConnections"`
	PlaintextBytes       int64  `json:"plaintextBytes"`
	ParsedRequests       int64  `json:"parsedRequests"`
	FilteredRequests     int64  `json:"filteredRequests"`
	RecordedRequests     int64  `json:"recordedRequests"`
	BodyBudgetReserved   int64  `json:"bodyBudgetReserved"`
	BodyBudgetLimit      int64  `json:"bodyBudgetLimit"`
	HTTPScriptsRunning   int    `json:"httpScriptsRunning"`
	CronScriptsRunning   int    `json:"cronScriptsRunning"`
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
	scriptRules, cronJobs := 0, 0
	if engine.scripts != nil {
		scriptRules, cronJobs = len(engine.scripts.http), len(engine.scripts.cron)
	}
	return State{MITMActive: !engine.closed && engine.mitm.active(), ScriptsActive: !engine.closed && engine.scripts != nil,
		ScriptRules: scriptRules, CronJobs: cronJobs, Active: !engine.closed && engine.recording.Load() != nil, RewriteActive: !engine.closed && engine.rewrite != nil, RewriteRules: rules, Backend: engine.backend(), Error: warning,
		Connections: len(engine.connections) + len(engine.preparing),
		Skipped:     engine.skipped.Load(), TLSFailed: engine.tlsFailed.Load(), BlockedQUIC: engine.blockedQUIC.Load(),
		TLSOpened:            engine.tlsOpened.Load(),
		TLSNoRequest:         engine.tlsNoRequest.Load(),
		PlaintextConnections: engine.plaintextConnections.Load(), PlaintextBytes: engine.plaintextBytes.Load(),
		ParsedRequests: engine.parsedRequests.Load(), FilteredRequests: engine.filteredRequests.Load(), RecordedRequests: engine.recordedRequests.Load(),
		BodyBudgetReserved: processingBodies.used.Load(), BodyBudgetLimit: processingBodies.limit,
		HTTPScriptsRunning: int(httpScriptsRunning.Load()), CronScriptsRunning: int(cronScriptsRunning.Load())}
}

func (engine *Engine) backend() string {
	if engine.mitm.active() {
		return "MITM"
	}
	return "HTTP"
}
