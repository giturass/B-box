package httpdebug

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"sync"
	"time"
)

const maxLeafCertificates = 128

// GenerateCA signs with the platform's non-exportable key. The CA private key
// never crosses the Android Keystore boundary or appears in a file/export.
func GenerateCA(signer crypto.Signer) (string, error) {
	if err := checkSigner(signer); err != nil {
		return "", err
	}
	public, err := x509.MarshalPKIXPublicKey(signer.Public())
	if err != nil {
		return "", err
	}
	identity := sha256.Sum256(public)
	serial, err := certificateSerial()
	if err != nil {
		return "", err
	}
	now := time.Now()
	root := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "B-box MITM " + hex.EncodeToString(identity[:6])},
		NotBefore:    now.Add(-5 * time.Minute), NotAfter: now.AddDate(3, 0, 0),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true,
		KeyUsage:           x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	der, err := x509.CreateCertificate(rand.Reader, root, root, signer.Public(), signer)
	if err != nil {
		return "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), nil
}

func ParseCA(certificate string) (*x509.Certificate, error) {
	if len(certificate) > 16*1024 {
		return nil, errors.New("MITM CA certificate exceeds 16 KiB")
	}
	block, rest := pem.Decode([]byte(certificate))
	if block == nil || block.Type != "CERTIFICATE" || len(rest) != 0 {
		return nil, errors.New("expected one public MITM CA certificate")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	if !ca.IsCA || !ca.BasicConstraintsValid || ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		return nil, errors.New("certificate is not a signing CA")
	}
	if err = ca.CheckSignatureFrom(ca); err != nil {
		return nil, errors.New("MITM CA must be self-signed")
	}
	if time.Now().Before(ca.NotBefore) || !time.Now().Before(ca.NotAfter) {
		return nil, errors.New("MITM CA is expired or not yet valid")
	}
	return ca, nil
}

type authority struct {
	mu     sync.Mutex
	issuer *x509.Certificate
	signer crypto.Signer
	leaves map[string]*tls.Certificate
	order  []string
}

func checkSigner(signer crypto.Signer) error {
	if signer == nil {
		return errors.New("generate a MITM CA in certificate settings first")
	}
	key, ok := signer.Public().(*ecdsa.PublicKey)
	if !ok || key.Curve != elliptic.P256() || !key.Curve.IsOnCurve(key.X, key.Y) {
		return errors.New("MITM CA requires an ECDSA P-256 signing key")
	}
	return nil
}

func loadAuthority(certificate string, signer crypto.Signer) (*authority, error) {
	if err := checkSigner(signer); err != nil {
		return nil, err
	}
	ca, err := ParseCA(certificate)
	if err != nil {
		return nil, err
	}
	key, ok := ca.PublicKey.(*ecdsa.PublicKey)
	if !ok || !key.Equal(signer.Public()) {
		return nil, errors.New("MITM CA and platform signing key do not match")
	}
	challenge := make([]byte, sha256.Size)
	if _, err = rand.Read(challenge); err != nil {
		return nil, err
	}
	signature, err := signer.Sign(rand.Reader, challenge, crypto.SHA256)
	if err != nil {
		return nil, err
	}
	if !ecdsa.VerifyASN1(key, challenge, signature) {
		return nil, errors.New("MITM CA signing key verification failed")
	}
	return &authority{issuer: ca, signer: signer, leaves: make(map[string]*tls.Certificate)}, nil
}

func (a *authority) leaf(host string) (*tls.Certificate, error) {
	host = normalizeHost(host)
	if !validHost(host) {
		return nil, errors.New("invalid MITM server name")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	if cached := a.leaves[host]; cached != nil && cached.Leaf.NotAfter.After(now.Add(time.Minute)) {
		return cached, nil
	}
	if !now.Before(a.issuer.NotAfter) {
		return nil, errors.New("MITM CA expired; stop capture and regenerate the CA")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := certificateSerial()
	if err != nil {
		return nil, err
	}
	leaf := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: host},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.Add(12 * time.Hour),
		BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage:        []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	}
	if leaf.NotAfter.After(a.issuer.NotAfter) {
		leaf.NotAfter = a.issuer.NotAfter
	}
	if leaf.NotBefore.Before(a.issuer.NotBefore) {
		leaf.NotBefore = a.issuer.NotBefore
	}
	if ip := net.ParseIP(host); ip != nil {
		leaf.IPAddresses = []net.IP{ip}
	} else {
		// A single SAN prevents clients from coalescing excluded origins onto
		// this TLS connection because the upstream certificate has more SANs.
		leaf.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, a.issuer, &key.PublicKey, a.signer)
	if err != nil {
		return nil, err
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	result := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: parsed}
	if a.leaves[host] == nil {
		if len(a.order) == maxLeafCertificates {
			delete(a.leaves, a.order[0])
			a.order = a.order[1:]
		}
		a.order = append(a.order, host)
	}
	a.leaves[host] = result
	return result, nil
}

func certificateSerial() (*big.Int, error) {
	// Positive 159-bit serial, within RFC 5280's twenty-octet limit.
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 159))
	if err == nil && serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	return serial, err
}
