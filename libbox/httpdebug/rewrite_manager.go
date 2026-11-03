package httpdebug

import (
	"crypto"
	"fmt"
)

// PreparedRewrite validates everything that can fail before replacing a running
// service. Activation only publishes a prepared immutable program. The caller
// serializes preparation, service reload and activation with its lifecycle lock.
type PreparedRewrite struct {
	program *RewriteProgram
	ca      *authority
}

func PrepareRewrite(directory string, config *RewriteConfig, certificate string, signer crypto.Signer) (*PreparedRewrite, error) {
	if config == nil || !config.Enabled {
		return nil, nil
	}
	program, err := CompileRewrite(config)
	if err != nil {
		return nil, err
	}
	prepared := &PreparedRewrite{program: program}
	if config.MITM.Enabled {
		prepared.ca, err = loadAuthority(certificate, signer)
		if err != nil {
			return nil, fmt.Errorf("HTTP rewrite MITM CA: %w", err)
		}
	}
	managerMu.Lock()
	defer managerMu.Unlock()
	if err = openLocked(directory); err != nil {
		return nil, err
	}
	return prepared, nil
}

func newRewriteEngine(prepared *PreparedRewrite) *Engine {
	engine := newEngine(Config{}, store)
	engine.captureEnabled = false
	engine.rewrite, engine.ca = prepared.program, prepared.ca
	return engine
}

func ActivateRewrite(prepared *PreparedRewrite) {
	managerMu.Lock()
	defer managerMu.Unlock()
	profileRewrite = prepared
	var next *Engine
	if prepared != nil {
		next = newRewriteEngine(prepared)
	}
	if previous := active.Swap(next); previous != nil {
		previous.Close()
	}
	if prepared != nil {
		AppendCaptureLog("info", fmt.Sprintf("Profile HTTP rewriting active: %d rules; capture recording is off", prepared.program.count))
	}
}

// StopService clears both profile processing and optional manual recording.
// Stop only controls recording and intentionally keeps profile rules alive.
func StopService() {
	managerMu.Lock()
	defer managerMu.Unlock()
	profileRewrite = nil
	if engine := active.Swap(nil); engine != nil {
		engine.Close()
		lastCaptureState = engine.captureState()
		AppendCaptureLog("info", "Service HTTP processing stopped; intercepted connections closed")
	}
}
