package httpdebug

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"fmt"
	"testing"
	"time"
)

func TestMITMAuthorityIdentityKeyMismatchAndBoundedSingleHostLeaves(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	pem, err := GenerateCA(key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := loadAuthority(pem, key)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca.issuer)
	for _, host := range []string{"origin.test", "127.0.0.1", "::1"} {
		leaf, err := ca.leaf(host)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = leaf.Leaf.Verify(x509.VerifyOptions{DNSName: host, Roots: pool}); err != nil {
			t.Fatal(err)
		}
		if leaf.Leaf.IsCA || leaf.Leaf.NotAfter.After(ca.issuer.NotAfter) || leaf.Leaf.NotAfter.After(time.Now().Add(13*time.Hour)) {
			t.Fatal("invalid leaf constraints")
		}
		if err = leaf.Leaf.VerifyHostname("unrelated.test"); err == nil {
			t.Fatal("leaf covers another origin")
		}
	}
	first, _ := ca.leaf("origin.test")
	second, _ := ca.leaf("ORIGIN.test.")
	if first != second {
		t.Fatal("leaf cache not reused")
	}
	for i := 0; i < maxLeafCertificates+1; i++ {
		if _, err = ca.leaf(fmt.Sprintf("host%d.test", i)); err != nil {
			t.Fatal(err)
		}
	}
	if len(ca.leaves) != maxLeafCertificates || len(ca.order) != maxLeafCertificates {
		t.Fatal("leaf cache is unbounded")
	}
	otherKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	otherPEM, err := GenerateCA(otherKey)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := ParseCA(otherPEM)
	if ca.issuer.Subject.String() == other.Subject.String() {
		t.Fatal("independent installations have the same CA identity")
	}
	if _, err = loadAuthority(pem, otherKey); err == nil {
		t.Fatal("mismatched signing key accepted")
	}
	if _, err = ParseCA(pem + otherPEM); err == nil {
		t.Fatal("multiple public certificates accepted")
	}
	if _, err = GenerateCA(nil); err == nil {
		t.Fatal("missing platform key accepted")
	}
}
