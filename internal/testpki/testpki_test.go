package testpki

import (
	"crypto/x509"
	"testing"
	"time"
)

func TestIssue(t *testing.T) {
	t.Parallel()
	ca, err := NewCA("ca")
	if err != nil {
		t.Fatal(err)
	}
	a, err := ca.Issue("a", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	b, err := ca.Issue("b", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if a.Cert.SerialNumber.Cmp(b.Cert.SerialNumber) == 0 {
		t.Fatal("serials must differ")
	}
	if _, err := a.Cert.Verify(x509.VerifyOptions{Roots: ca.Pool(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		t.Fatalf("chain: %v", err)
	}
	if _, err := ca.IssueWithKey("c", time.Now(), nil); err == nil {
		t.Fatal("nil key should fail")
	}
}
