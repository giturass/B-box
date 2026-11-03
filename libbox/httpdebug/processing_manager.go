package httpdebug

import (
	"context"
	"crypto"
	"crypto/x509"
	"fmt"
	"slices"
	"time"
)

// Preparation cannot execute scripts or intercept traffic. Activation happens
// only after the core has started successfully under the service lifecycle lock.
type PreparedProcessing struct {
	rewrite *RewriteProgram
	mitm    *MITMConfig
	scripts *ScriptProgram
	ca      *authority
	cron    *cronRunner
}

func PrepareProcessing(directory string, mitm *MITMConfig, rewrite *RewriteConfig, scripts *ScriptsConfig,
	certificate string, signer crypto.Signer, timezone string) (*PreparedProcessing, error) {
	prepared := &PreparedProcessing{}
	var err error
	if mitm != nil {
		copy := *mitm
		copy.Hosts, copy.Ports, copy.Packages = slices.Clone(mitm.Hosts), slices.Clone(mitm.Ports), slices.Clone(mitm.Packages)
		if err = copy.validate(); err != nil {
			return nil, err
		}
		if copy.active() {
			prepared.mitm = &copy
			prepared.ca, err = loadAuthority(certificate, signer)
			if err != nil {
				return nil, fmt.Errorf("MITM CA: %w", err)
			}
		}
	}
	if rewrite != nil && rewrite.Enabled {
		prepared.rewrite, err = CompileRewrite(rewrite)
		if err != nil {
			return nil, err
		}
	}
	if scripts != nil && scripts.Enabled {
		location := time.UTC
		if timezone != "" {
			location, err = time.LoadLocation(timezone)
			if err != nil {
				return nil, fmt.Errorf("device script timezone: %w", err)
			}
		}
		prepared.scripts, err = CompileScripts(scripts, location)
		if err != nil {
			return nil, err
		}
	}
	if prepared.rewrite == nil && prepared.mitm == nil && prepared.scripts == nil {
		return nil, nil
	}
	managerMu.Lock()
	defer managerMu.Unlock()
	if err = openLocked(directory); err != nil {
		return nil, err
	}
	return prepared, nil
}

func newProcessingEngine(prepared *PreparedProcessing) *Engine {
	engine := newEngine(Config{}, store)
	engine.captureEnabled = false
	engine.rewrite, engine.mitm, engine.scripts, engine.ca = prepared.rewrite, prepared.mitm, prepared.scripts, prepared.ca
	return engine
}

func ActivateProcessing(prepared *PreparedProcessing, dial RewriteDialFunc, roots *x509.CertPool) {
	managerMu.Lock()
	defer managerMu.Unlock()
	if profileProcessing != nil && profileProcessing.cron != nil {
		profileProcessing.cron.Close()
	}
	profileProcessing = prepared
	var next *Engine
	if prepared != nil {
		next = newProcessingEngine(prepared)
	}
	if previous := active.Swap(next); previous != nil {
		previous.Close()
	}
	if prepared != nil {
		if prepared.scripts != nil && len(prepared.scripts.cron) > 0 {
			prepared.cron = startCron(prepared.scripts, dial, roots)
		}
		writeLog("info", "Profile MITM, HTTP rewrite and script settings applied; capture recording is off")
	}
}

// StopService ends all profile processing, scheduled jobs and manual recording.
// Stop controls recording only; it never restarts or duplicates cron schedules.
func StopService() {
	managerMu.Lock()
	defer managerMu.Unlock()
	defer SetLogger(nil)
	if profileProcessing != nil && profileProcessing.cron != nil {
		profileProcessing.cron.Close()
	}
	profileProcessing = nil
	if engine := active.Swap(nil); engine != nil {
		engine.Close()
		lastCaptureState = engine.captureState()
		writeLog("info", "Service processing stopped; intercepted connections and cron jobs closed")
	}
}

// Avoid recursive interception of HTTP requests explicitly issued by scripts.
// DNS and outbound rule evaluation still happen in the active sing-box router.
type processingContextKey struct{}

func WithoutProcessing(ctx context.Context) context.Context {
	return context.WithValue(ctx, processingContextKey{}, true)
}
func ProcessingDisabled(ctx context.Context) bool { return ctx.Value(processingContextKey{}) != nil }
