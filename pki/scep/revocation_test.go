package scep_test

import (
	"crypto/x509/pkix"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/pki/scep"
	"github.com/deploymenttheory/go-apple-dm/state"
)

func TestRevokedRenewalCannotUseChallengeFallback(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	registry, err := revocation.New(state.NewMemory(), revocation.Issuer{Certificate: f.caCert, Signer: f.caKey, CRLTTL: time.Hour, CRLRefresh: time.Minute, OCSPTTL: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	s, err := newTestServer(f.signer, f.caCert, f.caKey, scep.WithChallenge(scep.StaticChallenge("known-challenge")), scep.WithCertificateStatus(registry.Check))
	if err != nil {
		t.Fatal(err)
	}
	client := serve(t, s)
	key := rsaKey(t)
	subject := pkix.Name{CommonName: "device"}
	cert, err := client.Enroll(ctx, key, scep.EnrollOptions{Subject: subject, Challenge: "known-challenge"})
	if err != nil {
		t.Fatal(err)
	}
	issuer := cms.Fingerprint(f.caCert)
	if err := registry.Register(ctx, issuer, cert, revocation.Provenance{Source: "scep"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Enroll(ctx, rsaKey(t), scep.EnrollOptions{Subject: subject, Renew: &scep.Identity{Cert: cert, Key: key}}); err != nil {
		t.Fatal("valid renewal", err)
	}
	if err := registry.Revoke(ctx, issuer, cert.SerialNumber, 1); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"device", "changed-subject"} {
		if _, err := client.Enroll(ctx, rsaKey(t), scep.EnrollOptions{Subject: pkix.Name{CommonName: name}, Challenge: "known-challenge", Renew: &scep.Identity{Cert: cert, Key: key}}); !errors.Is(err, scep.ErrRejected) {
			t.Fatal("revoked renewal accepted", err)
		}
	}
}
