//go:build go1.24

package tls

import "context"

// QUIC uses crypto/tls. Convert TCP uTLS and kTLS configurations without
// weakening certificate verification, and keep dynamic ECH lookup available.
// Adapted from MiChongs/sing-box common/tls/quic_client.go; see manifest.json.
type xhttpSTDClientConvertible interface {
	stdClientConfig() *STDClientConfig
}

func QUICClientConfig(config Config) (Config, error) {
	switch config := config.(type) {
	case nil:
		return nil, nil
	case xhttpSTDClientConvertible:
		return config.stdClientConfig(), nil
	case *KTLSClientConfig:
		return QUICClientConfig(config.Config)
	case *ECHClientConfig:
		inner, err := QUICClientConfig(config.ECHCapableConfig)
		if err != nil {
			return nil, err
		}
		return &ECHClientConfig{
			ECHCapableConfig: inner.(ECHCapableConfig),
			dnsRouter:        config.dnsRouter,
			queryServerName:  config.queryServerName,
		}, nil
	}
	_, err := config.STDConfig()
	if err != nil {
		return nil, err
	}
	return config, nil
}

// QUIC bypasses ClientHandshake, so perform the ECH DNS refresh before each
// dial and give that dial an independent snapshot of the refreshed config.
func QUICDialConfig(ctx context.Context, config Config) (Config, error) {
	ech, ok := config.(*ECHClientConfig)
	if !ok {
		return config, nil
	}
	ech.access.Lock()
	defer ech.access.Unlock()
	if err := ech.fetchECHConfigList(ctx); err != nil {
		return nil, err
	}
	return ech.ECHCapableConfig.Clone(), nil
}
