//go:build with_utls && go1.24

package tls

import (
	"crypto/tls"
	"slices"
)

// Adapted from MiChongs/sing-box's uTLS conversion for the pinned SagerNet
// fields. Fingerprinting, fragmentation and spoofing only affect TCP.
func (c *UTLSClientConfig) stdClientConfig() *STDClientConfig {
	config := &tls.Config{
		Time:                           c.config.Time,
		RootCAs:                        c.config.RootCAs,
		NextProtos:                     slices.Clone(c.config.NextProtos),
		InsecureSkipVerify:             c.config.InsecureSkipVerify || c.verifyServerName,
		VerifyPeerCertificate:          c.config.VerifyPeerCertificate,
		CipherSuites:                   slices.Clone(c.config.CipherSuites),
		MinVersion:                     c.config.MinVersion,
		MaxVersion:                     c.config.MaxVersion,
		EncryptedClientHelloConfigList: slices.Clone(c.config.EncryptedClientHelloConfigList),
	}
	for _, curve := range c.config.CurvePreferences {
		config.CurvePreferences = append(config.CurvePreferences, tls.CurveID(curve))
	}
	for _, certificate := range c.config.Certificates {
		config.Certificates = append(config.Certificates, tls.Certificate{
			Certificate: certificate.Certificate,
			PrivateKey:  certificate.PrivateKey,
			Leaf:        certificate.Leaf,
		})
	}
	client := &STDClientConfig{
		ctx:        c.ctx,
		config:     config,
		serverName: c.serverName,
		disableSNI: c.disableSNI,
		// The upstream pin callback supplies certificate authentication while
		// InsecureSkipVerify bypasses chain/name checks. Do not add a second
		// system-chain check when disable_sni is combined with that callback.
		verifyServerName: c.verifyServerName && c.config.VerifyPeerCertificate == nil,
		handshakeTimeout: c.handshakeTimeout,
	}
	client.SetServerName(c.serverName)
	return client
}
