package statestore_test

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/scep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

func exerciseSCEPGrants(t *testing.T, a, b state.Store) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(
		rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: "device"}},
		key,
	)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.ParseCertificateRequest(der)
	if err != nil {
		t.Fatal(err)
	}
	grants := []scep.Grants{
		{
			Store:     a,
			Authorize: func(context.Context, scep.Grant, *x509.CertificateRequest) error { return nil },
		},
		{
			Store:     b,
			Authorize: func(context.Context, scep.Grant, *x509.CertificateRequest) error { return nil },
		},
	}
	password, err := grants[0].Issue(
		t.Context(),
		scep.Grant{ExpiresAt: time.Now().Add(time.Minute)},
	)
	if err != nil {
		t.Fatal(err)
	}
	root, signer, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	local, err := ca.NewLocal(root, signer)
	if err != nil {
		t.Fatal(err)
	}
	var signatures atomic.Int64
	var wg sync.WaitGroup
	certs := make(chan *x509.Certificate, 12)
	for n := range 12 {
		wg.Go(func() {
			g := grants[n%2]
			i := scep.CertificateIssuer{
				Store:     g.Store,
				Authorize: g.Verify,
				Signer:    sqlReceiptSigner{Signer: local, count: &signatures},
				Register:  func(context.Context, *x509.Certificate) error { return nil },
			}
			c, err := i.Issue(t.Context(), password, csr, ca.Policy{})
			if err != nil {
				t.Error(err)
			}
			certs <- c
		})
	}
	wg.Wait()
	close(certs)
	var first *x509.Certificate
	for c := range certs {
		if c == nil {
			t.Fatal("missing certificate")
		}
		if first == nil {
			first = c
		}
		if !bytes.Equal(c.Raw, first.Raw) {
			t.Fatal("replicas returned different certificates")
		}
	}
	if signatures.Load() != 1 {
		t.Fatal("duplicate signing", signatures.Load())
	}
	other, err := x509.CreateCertificateRequest(
		rand.Reader,
		&x509.CertificateRequest{Subject: pkix.Name{CommonName: "another device"}},
		key,
	)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := x509.ParseCertificateRequest(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := grants[1].Verify(t.Context(), password, changed); err == nil {
		t.Fatal("another CSR reused a grant")
	}
}

type sqlReceiptSigner struct {
	ca.Signer
	count *atomic.Int64
}

func (s sqlReceiptSigner) Sign(
	ctx context.Context,
	csr *x509.CertificateRequest,
	p ca.Policy,
) (*x509.Certificate, error) {
	s.count.Add(1)
	return s.Signer.Sign(ctx, csr, p)
}
