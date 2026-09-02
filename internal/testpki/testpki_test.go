package testpki

import (
	"bytes"
	"crypto/tls"
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

func TestIssuePushAndPEM(t *testing.T) {
	t.Parallel()
	ca, err := NewCA("ca")
	if err != nil {
		t.Fatal(err)
	}
	const topic = "com.apple.mgmt.External.test"
	id, err := ca.IssuePush(topic, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range id.Cert.Subject.Names {
		if n.Type.Equal(oidUserID) && n.Value == topic {
			found = true
		}
	}
	if !found {
		t.Fatalf("subject UID not set: %+v", id.Cert.Subject)
	}
	certPEM, keyPEM, err := id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("round trip: %v", err)
	}
	if !bytes.Equal(pair.Certificate[0], id.Cert.Raw) {
		t.Fatal("PEM certificate does not match")
	}
	if _, err := ca.IssuePushWithKey(topic, time.Now(), nil); err == nil {
		t.Fatal("nil key should fail")
	}
	if _, _, err := (&Identity{}).PEM(); err == nil {
		t.Fatal("empty identity should fail")
	}
}
