package libbox

import (
	"context"
	"crypto"
	"errors"
	"fmt"
	"net"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/experimental/httpdebug"
	M "github.com/sagernet/sing/common/metadata"
)

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

func (s *CommandServer) activateUIProcessing(prepared *httpdebug.PreparedProcessing) {
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
			outbound, found := instance.Box().Outbound().Outbound(policy)
			if !found {
				return nil, fmt.Errorf("script policy %q is not a configured sing-box outbound", policy)
			}
			return outbound.DialContext(routeCtx, "tcp", destination)
		}
		local, routed := net.Pipe()
		stop := context.AfterFunc(routeCtx, func() { local.Close(); routed.Close() })
		go instance.Box().Router().RouteConnectionEx(routeCtx, routed, metadata, func(error) { stop(); routed.Close() })
		return local, nil
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
	httpdebug.ActivateProcessing(prepared, dial, instance.UIRootPool())
}

func stopUIProcessing() { httpdebug.StopService() }
