package libbox

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"io"

	"github.com/sagernet/sing-box/experimental/httpdebug"
)

// Android signs an already hashed ECDSA/SHA-256 message with NONEwithECDSA.
// The platform private key is never serialized or exposed through a binding.
type UICaptureSigner interface {
	Sign(digest []byte) ([]byte, error)
}

type uiCaptureSigner struct {
	public   crypto.PublicKey
	platform UICaptureSigner
}

func (s *uiCaptureSigner) Public() crypto.PublicKey { return s.public }
func (s *uiCaptureSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if s.platform == nil || opts.HashFunc() != crypto.SHA256 || len(digest) != sha256.Size {
		return nil, errors.New("unsupported MITM CA signature operation")
	}
	key, ok := s.public.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("MITM CA key must be ECDSA")
	}
	signature, err := s.platform.Sign(digest)
	if err != nil {
		return nil, err
	}
	if len(signature) > 80 || !ecdsa.VerifyASN1(key, digest, signature) {
		return nil, errors.New("platform returned an invalid MITM CA signature")
	}
	return signature, nil
}

func GenerateUICaptureCA(publicKeyDER []byte, signer UICaptureSigner) (string, error) {
	if len(publicKeyDER) > 1024 {
		return "", errors.New("MITM CA public key is too large")
	}
	public, err := x509.ParsePKIXPublicKey(publicKeyDER)
	if err != nil {
		return "", err
	}
	return httpdebug.GenerateCA(&uiCaptureSigner{public, signer})
}
