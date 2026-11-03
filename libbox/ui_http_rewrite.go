package libbox

import (
	"crypto"
	"fmt"

	"github.com/sagernet/sing-box/experimental/httpdebug"
)

func NeedsUIHTTPRewriteCA(configContent string) (bool, error) {
	options, err := parseConfig(baseContext(nil), configContent)
	if err != nil {
		return false, err
	}
	return options.Experimental != nil && options.Experimental.HTTPRewrite != nil &&
		options.Experimental.HTTPRewrite.Enabled && options.Experimental.HTTPRewrite.MITM.Enabled, nil
}

func (s *CommandServer) prepareUIHTTPRewrite(configContent string, overrides *OverrideOptions) (*httpdebug.PreparedRewrite, error) {
	options, err := parseConfig(s.ctx, configContent)
	if err != nil {
		return nil, err
	}
	if options.Experimental == nil || options.Experimental.HTTPRewrite == nil || !options.Experimental.HTTPRewrite.Enabled {
		return nil, nil
	}
	config := (*httpdebug.RewriteConfig)(options.Experimental.HTTPRewrite)
	var signer crypto.Signer
	if config.MITM.Enabled {
		ca, err := httpdebug.ParseCA(overrides.RewriteCertificate)
		if err != nil {
			return nil, fmt.Errorf("HTTP rewrite needs a generated MITM CA: %w", err)
		}
		signer = &uiCaptureSigner{ca.PublicKey, overrides.RewriteSigner}
	}
	return httpdebug.PrepareRewrite(overrides.RewriteDirectory, config, overrides.RewriteCertificate, signer)
}

func activateUIHTTPRewrite(prepared *httpdebug.PreparedRewrite) { httpdebug.ActivateRewrite(prepared) }
func stopUIHTTPProcessing()                                     { httpdebug.StopService() }
