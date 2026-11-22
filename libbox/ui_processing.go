package libbox

import (
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/experimental/httpdebug"
	"github.com/sagernet/sing/common/control"
	M "github.com/sagernet/sing/common/metadata"
)

type uiProcessingState struct {
	prepared   *httpdebug.PreparedProcessing
	unregister func()
}

// UIScriptTask is tied to the processing instance whose profile the Android
// host admitted. Run waits on IO; Close only signals cancellation.
type UIScriptTask struct {
	task *httpdebug.ScriptTask
}

func (s *CommandServer) NewUIScriptTask(kind string, index int32) (*UIScriptTask, error) {
	// A reload owns the writer through preparation and activation. Never wait
	// through it and resolve this index against its replacement profile. Ordinary
	// probe and recording admission are readers and keep the same instance stable.
	if !uiCaptureLifecycle.TryRLock() {
		return nil, errors.New("local service is changing during script execution")
	}
	defer uiCaptureLifecycle.RUnlock()
	if s.Instance() == nil || s.uiProcessingState == nil || s.uiProcessingRoutes == nil {
		return nil, errors.New("local profile scripts are not running")
	}
	task, err := s.uiProcessingState.prepared.RunScript(s.uiProcessingRoutes.ctx, kind, int(index))
	if err != nil {
		return nil, err
	}
	return &UIScriptTask{task: task}, nil
}

func (t *UIScriptTask) Close() { t.task.Cancel() }
func (t *UIScriptTask) Join()  { t.task.Join() }

func (t *UIScriptTask) Run() (string, error) {
	result := t.task.Wait()
	if result.Logs == nil {
		result.Logs = []string{}
	}
	encoded, err := json.Marshal(struct {
		Result string   `json:"result"`
		Error  string   `json:"error"`
		Logs   []string `json:"logs"`
	}{result.Result, result.Error, result.Logs})
	return string(encoded), err
}

func NeedsUIMITMCA(configContent string) (bool, error) {
	options, err := parseConfig(baseContext(nil), configContent)
	if err != nil {
		return false, err
	}
	return options.Experimental != nil && options.Experimental.MITM != nil &&
		options.Experimental.MITM.Enabled && len(options.Experimental.MITM.Hosts) > 0, nil
}

func (s *CommandServer) prepareUIProcessing(configContent string, overrides *OverrideOptions) (*httpdebug.PreparedProcessing, error) {
	options, err := parseConfig(s.ctx, configContent)
	if err != nil {
		return nil, err
	}
	if options.Experimental == nil {
		return nil, nil
	}
	config := options.Experimental
	var signer crypto.Signer
	if config.MITM != nil && config.MITM.Enabled && len(config.MITM.Hosts) > 0 {
		ca, err := httpdebug.ParseCA(overrides.MitmCertificate)
		if err != nil {
			return nil, fmt.Errorf("MITM needs a generated CA: %w", err)
		}
		signer = &uiCaptureSigner{ca.PublicKey, overrides.MitmSigner}
	}
	prepared, err := httpdebug.PrepareProcessing(overrides.ProcessingDirectory, (*httpdebug.MITMConfig)(config.MITM),
		(*httpdebug.RewriteConfig)(config.HTTPRewrite), (*httpdebug.ScriptsConfig)(config.Scripts),
		overrides.MitmCertificate, signer, overrides.ScriptTimezone)
	if err != nil {
		return nil, err
	}
	prepared.SetProfileDirectory(overrides.ProcessingProfileDirectory)
	return prepared, nil
}

func (s *CommandServer) activateUIProcessing(prepared *httpdebug.PreparedProcessing, reloading bool) {
	instance := s.Instance()
	if instance == nil {
		httpdebug.StopService()
		return
	}
	logger := instance.Box().LogFactory().NewLogger("processing")
	httpdebug.SetLogger(func(level, message string) {
		switch level {
		case "debug":
			logger.Debug(message)
		case "warn":
			logger.Warn(message)
		case "error":
			logger.Error(message)
		default:
			logger.Info(message)
		}
	})
	if prepared == nil {
		httpdebug.ActivateProcessing(nil, nil, nil)
		return
	}
	routes := newUIRouteLifecycle(context.Background())
	s.uiProcessingRoutes = routes
	dial := func(ctx context.Context, address string) (net.Conn, error) {
		if instance == nil || s.Instance() != instance {
			return nil, errors.New("local service changed during script execution")
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		destination := M.ParseSocksaddr(address)
		if !destination.IsValid() || destination.Port == 0 {
			return nil, errors.New("invalid script destination")
		}
		metadata := adapter.InboundContext{Inbound: "b-box-scripts", Network: "tcp", Source: M.ParseSocksaddr("127.0.0.1:0"), Destination: destination}
		routeCtx := adapter.WithContext(httpdebug.WithoutProcessing(ctx), &metadata)
		if policy := httpdebug.ScriptPolicy(ctx); policy != "" {
			return routes.dial(routeCtx, func(ctx context.Context) (net.Conn, error) {
				outbound, found := instance.Box().Outbound().Outbound(policy)
				if !found {
					return nil, fmt.Errorf("script policy %q is not a configured sing-box outbound", policy)
				}
				return outbound.DialContext(ctx, "tcp", destination)
			})
		}
		return routes.route(routeCtx, func(ctx context.Context, conn net.Conn, onClose func(error)) {
			instance.Box().Router().RouteConnectionEx(ctx, conn, metadata, onClose)
		})
	}
	prepared.SetGatewayResolver(func() string {
		if s.Instance() != instance {
			return ""
		}
		current := instance.Box().Network().DefaultNetworkInterface()
		if current == nil {
			return ""
		}
		for _, gateway := range current.Gateways {
			if gateway.Is4() && !gateway.IsUnspecified() {
				return gateway.String()
			}
		}
		for _, gateway := range current.Gateways {
			if gateway.IsValid() && !gateway.IsUnspecified() {
				return gateway.WithZone("").String()
			}
		}
		return ""
	})
	event := httpdebug.ScriptEventEngineStarted
	if reloading {
		event = httpdebug.ScriptEventProfileReloaded
	}
	httpdebug.ActivateProcessing(prepared, dial, instance.UIRootPool(), event)
	state := &uiProcessingState{prepared: prepared}
	s.uiProcessingState = state
	if monitor := instance.Box().Network().InterfaceMonitor(); monitor != nil {
		callback := uiProcessingNetworkCallback(routes.ctx, prepared.NotifyScriptEvent, instance.Box().Network().DefaultNetworkInterface() == nil)
		element := monitor.RegisterCallback(callback)
		state.unregister = func() { monitor.UnregisterCallback(element) }
	}
}

// The core monitor publishes after updating its real default interface and
// DNS snapshot. Initial publication precedes registration; repeated absence
// is not a new network event. The captured prepared instance rejects a late
// callback after reload instead of targeting the new global processor.
func uiProcessingNetworkCallback(ctx context.Context, notify func(string) error, missing bool) func(*control.Interface, int) {
	var access sync.Mutex
	return func(networkInterface *control.Interface, _ int) {
		access.Lock()
		defer access.Unlock()
		if ctx.Err() != nil {
			return
		}
		if networkInterface == nil && missing {
			return
		}
		missing = networkInterface == nil
		_ = notify(httpdebug.ScriptEventNetworkChanged)
	}
}

func (s *CommandServer) stopUIProcessing() {
	s.uiProcessingRoutes.close()
	if s.uiProcessingState != nil && s.uiProcessingState.unregister != nil {
		s.uiProcessingState.unregister()
	}
	httpdebug.StopService()
	s.uiProcessingRoutes.wait()
	s.uiProcessingRoutes = nil
	s.uiProcessingState = nil
}
