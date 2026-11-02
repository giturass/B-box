package libbox

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"testing"

	"github.com/sagernet/sing-box/experimental/httpdebug"
)

type testUICaptureSigner struct {
	key  *ecdsa.PrivateKey
	fail bool
}

func (s testUICaptureSigner) Sign(digest []byte) ([]byte, error) {
	if s.fail {
		return nil, errors.New("platform key unavailable")
	}
	return ecdsa.SignASN1(rand.Reader, s.key, digest)
}

func TestUICapturePlatformSignerGeneratesCAAndRejectsInvalidSignatures(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	platform := testUICaptureSigner{key: key}
	certificate, err := GenerateUICaptureCA(public, platform)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := httpdebug.ParseCA(certificate)
	if err != nil {
		t.Fatal(err)
	}
	if !ca.PublicKey.(*ecdsa.PublicKey).Equal(&key.PublicKey) {
		t.Fatal("platform public key changed")
	}
	wrapped := &uiCaptureSigner{public: &key.PublicKey, platform: platform}
	digest := sha256.Sum256([]byte("binding challenge"))
	signature, err := wrapped.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil || !ecdsa.VerifyASN1(&key.PublicKey, digest[:], signature) {
		t.Fatal("digest signed incorrectly", err)
	}
	if _, err = wrapped.Sign(rand.Reader, digest[:], crypto.SHA384); err == nil {
		t.Fatal("unsupported digest accepted")
	}
	if _, err = wrapped.Sign(rand.Reader, digest[:31], crypto.SHA256); err == nil {
		t.Fatal("short digest accepted")
	}
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	wrapped.platform = testUICaptureSigner{key: other}
	if _, err = wrapped.Sign(rand.Reader, digest[:], crypto.SHA256); err == nil {
		t.Fatal("wrong signing key accepted")
	}
	if _, err = GenerateUICaptureCA(public, testUICaptureSigner{fail: true}); err == nil {
		t.Fatal("platform failure hidden")
	}
	if _, err = GenerateUICaptureCA(public, nil); err == nil {
		t.Fatal("missing platform accepted")
	}
	if _, err = GenerateUICaptureCA([]byte("invalid public key"), platform); err == nil {
		t.Fatal("invalid public key accepted")
	}
}
