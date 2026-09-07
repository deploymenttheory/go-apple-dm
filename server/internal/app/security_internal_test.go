package app

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/state"
)

var errSecurityStorage = errors.New("security storage unavailable")

type unavailableDepot struct{ ca.Depot }

func (unavailableDepot) Put(context.Context, *x509.Certificate) error { return errSecurityStorage }

type unavailableChallenge struct{}

func (unavailableChallenge) Verify(context.Context, string, *x509.CertificateRequest) error {
	return errSecurityStorage
}

func TestIssuanceAndChallengeFailuresPropagate(t *testing.T) {
	ctx := t.Context()
	a := &accountdriven.Associations{Store: state.NewMemory()}
	d := &enrollmentDepot{Depot: unavailableDepot{}, associations: a}
	if err := d.Put(ctx, &x509.Certificate{}); !errors.Is(err, errSecurityStorage) {
		t.Fatal(err)
	}
	cert := &x509.Certificate{
		Subject: pkix.Name{CommonName: accountdriven.CertificateSubjectPrefix + "missing"},
	}
	if err := d.Put(ctx, cert); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("unregistered association was issued", err)
	}
	root, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := revocation.New(
		state.NewMemory(),
		revocation.Issuer{
			Certificate: root,
			Signer:      key,
			CRLTTL:      time.Hour,
			CRLRefresh:  time.Minute,
			OCSPTTL:     time.Minute,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	d.registry, d.issuer = reg, cms.Fingerprint(root)
	if err := d.Put(ctx, cert); !errors.Is(err, revocation.ErrInvalid) {
		t.Fatal("unregistrable certificate was issued", err)
	}
	challenge := enrollmentChallenge{base: unavailableChallenge{}, associations: a}
	if err := challenge.Verify(ctx, "password", nil); !errors.Is(err, errSecurityStorage) {
		t.Fatal(err)
	}
	csr := &x509.CertificateRequest{Subject: cert.Subject}
	if err := challenge.Verify(
		ctx,
		"password",
		csr,
	); !errors.Is(
		err,
		accountdriven.ErrAssociation,
	) {
		t.Fatal("account challenge fell through to shared password", err)
	}
}

func TestProtocolStoreOutageAndUnconfiguredRoute(t *testing.T) {
	ctx := t.Context()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, dialect: sqlite.Dialect}
	if _, err := a.protocolState(ctx); err == nil {
		t.Fatal("closed state database accepted")
	}
	a.cfg.RateLimits.Routes = map[string]RouteQuota{"auth": {}}
	next := http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	)
	if _, err := a.withRateLimits(ctx, next); err == nil {
		t.Fatal("limiter initialized without storage")
	}
	a.protocol = state.NewMemory()
	if got, err := a.protocolState(ctx); err != nil || got != a.protocol {
		t.Fatal(got, err)
	}
	h, err := a.withRateLimits(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/unconfigured", nil))
	if w.Code != http.StatusNoContent {
		t.Fatal(w.Code)
	}
}
