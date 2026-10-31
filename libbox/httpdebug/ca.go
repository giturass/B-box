package httpdebug

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"sync"
	"time"
)

// GenerateCA returns a public certificate and its PKCS#8 private key. Android
// encrypts the private key with Android Keystore before persisting it.
func GenerateCA() (certificate, privateKey string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := randomSerial()
	if err != nil {
		return "", "", err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "B-box HTTP Debug CA", Organization: []string{"B-box local debugging"}},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.AddDate(10, 0, 0),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})), nil
}

type authority struct {
	mu     sync.Mutex
	issuer *x509.Certificate
	key    any
	cache  map[string]*tls.Certificate
	order  []string
}

func loadAuthority(certificate, privateKey string) (*authority, error) {
	pair, err := tls.X509KeyPair([]byte(certificate), []byte(privateKey))
	if err != nil {
		return nil, err
	}
	issuer, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	if !issuer.IsCA || issuer.KeyUsage&x509.KeyUsageCertSign == 0 ||
		time.Now().Before(issuer.NotBefore) || time.Now().After(issuer.NotAfter) {
		return nil, errors.New("the debugging CA is not a currently valid signing CA")
	}
	return &authority{issuer: issuer, key: pair.PrivateKey, cache: make(map[string]*tls.Certificate)}, nil
}

func (a *authority) certificate(host string) (*tls.Certificate, error) {
	host = normalizeHost(host)
	if host == "" || len(host) > 253 {
		return nil, errors.New("missing or invalid TLS server name")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if cached := a.cache[host]; cached != nil && time.Now().Add(time.Hour).Before(cached.Leaf.NotAfter) {
		return cached, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	notAfter := time.Now().Add(24 * time.Hour)
	if notAfter.After(a.issuer.NotAfter) {
		notAfter = a.issuer.NotAfter
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter,
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, a.issuer, &key.PublicKey, a.key)
	if err != nil {
		return nil, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, err
	}
	certificate := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
	if a.cache[host] == nil {
		if len(a.order) >= 128 {
			delete(a.cache, a.order[0])
			a.order = a.order[1:]
		}
		a.order = append(a.order, host)
	}
	a.cache[host] = certificate
	return certificate, nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err == nil && serial.Sign() == 0 {
		serial.SetInt64(1)
	}
	return serial, err
}
