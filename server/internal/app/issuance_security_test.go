package app

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/webauth"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/scep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

func TestAdmissionPolicyDeniesUnlistedAndUnverifiedIdentities(t *testing.T) {
	a := &App{cfg: Config{Clock: clock.Real{}}}
	e := &enrollment{now: time.Now}
	policy, err := a.enrollmentAdmission(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = policy(
		t.Context(),
		AdmissionRequest{UDID: "mac"},
	); !errors.Is(
		err,
		webauth.ErrDenied,
	) {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "admission.json")
	raw := []byte(
		`{"Devices":[{"Serial":"SER","UDID":"mac"}],"Accounts":[{"Issuer":"https://idp.example","Email":"alice@example.com","ManagedAppleAccount":"managed@example.com","Groups":["enrollers"]}]}`,
	)
	if err = os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	e.cfg.AdmissionFile = file
	policy, err = a.enrollmentAdmission(e)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []AdmissionRequest{{Serial: "SER", UDID: "other"}, {Serial: "other", UDID: "mac"}, {Issuer: "https://idp.example", Email: "alice@example.com", Groups: []string{"enrollers"}}, {Issuer: "https://idp.example", Email: "alice@example.com", EmailVerified: true}} {
		if _, err = policy(t.Context(), r); !errors.Is(err, webauth.ErrDenied) {
			t.Fatal("unauthorized admission", r, err)
		}
	}
	grant, err := policy(
		t.Context(),
		AdmissionRequest{
			Issuer:        "https://idp.example",
			Subject:       "alice",
			Email:         "alice@example.com",
			EmailVerified: true,
			Groups:        []string{"enrollers"},
		},
	)
	if err != nil || grant.Account != "managed@example.com" || !grant.ExpiresAt.After(time.Now()) {
		t.Fatal(grant, err)
	}
	if _, err = policy(t.Context(), AdmissionRequest{Serial: "SER", UDID: "mac"}); err != nil {
		t.Fatal(err)
	}
}

func TestAdmissionPolicyRejectsUnknownConstraints(t *testing.T) {
	a := &App{cfg: Config{Clock: clock.Real{}}}
	file := filepath.Join(t.TempDir(), "policy.json")
	for _, raw := range []string{`{"accountz":[]}`, `{"accounts":[{"issuer":"https://idp","subject":"user","managedAppleAccount":"user@example.com","groupz":["restricted"]}]}`, `{} {}`, `null garbage`} {
		if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := a.enrollmentAdmission(
			&enrollment{cfg: EnrollConfig{AdmissionFile: file}},
		); err == nil {
			t.Fatal("invalid policy accepted", raw)
		}
	}
}

func TestIssuanceGrantConcurrentClaimsRetryAndReadmission(t *testing.T) {
	ctx := t.Context()
	st := state.NewMemory()
	a := &App{cfg: Config{Clock: clock.Real{}}, protocol: st}
	cert, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var denied atomic.Bool
	e := &enrollment{
		state:  st,
		app:    a,
		now:    time.Now,
		caCert: cert,
		caKey:  key,
		admission: func(context.Context, AdmissionRequest) (AdmissionGrant, error) {
			if denied.Load() {
				return AdmissionGrant{}, webauth.ErrDenied
			}
			return AdmissionGrant{ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
	}
	a.enroll = e
	e.depot = &enrollmentDepot{app: a, Depot: ca.NewMemoryDepot()}
	makeCSR := func() *x509.CertificateRequest {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		der, err := x509.CreateCertificateRequest(
			rand.Reader,
			&x509.CertificateRequest{Subject: pkix.Name{CommonName: "allowed"}},
			k,
		)
		if err != nil {
			t.Fatal(err)
		}
		csr, err := x509.ParseCertificateRequest(der)
		if err != nil {
			t.Fatal(err)
		}
		return csr
	}
	requests := []*x509.CertificateRequest{makeCSR(), makeCSR()}
	password, err := e.issueSCEPGrant(
		ctx,
		acme.Binding{CommonName: "allowed", UDID: "mac"},
		AdmissionGrant{ExpiresAt: time.Now().Add(time.Hour)},
	)
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int64
	var winner atomic.Int64
	var wg sync.WaitGroup
	for i := range requests {
		wg.Go(func() {
			if err := e.verifySCEPGrant(ctx, password, requests[i]); err == nil {
				winners.Add(1)
				winner.Store(int64(i))
			}
		})
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("competing keys admitted", winners.Load())
	}
	csr := requests[winner.Load()]
	first, err := e.issueSCEP(ctx, csr, ca.Policy{}, password, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.verifySCEPGrant(ctx, password, csr); err != nil {
		t.Fatal(err)
	}
	retry, err := e.issueSCEP(ctx, csr, ca.Policy{}, password, false)
	if err != nil || !bytes.Equal(first.Raw, retry.Raw) {
		t.Fatal("retry changed certificate", err)
	}

	fault := errors.New("issuance state write failed")
	e.state = issuanceStateFault{Store: st, txReadErr: fault}
	if _, err = e.issueSCEP(ctx, csr, ca.Policy{}, password, false); !errors.Is(err, fault) {
		t.Fatal(err)
	}
	e.state = st
	a.protocol = issuanceStateFault{Store: st, writeErr: fault}
	if _, err = e.issueSCEP(ctx, csr, ca.Policy{}, password, false); !errors.Is(err, fault) {
		t.Fatal("unregistered certificate returned", err)
	}
	a.protocol = st
	cacheKey := "scep/certificate/" + issuanceHash([]byte(password)) + "/" + issuanceHash(csr.Raw)
	cached, err := st.Get(ctx, cacheKey)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.Update(ctx, []string{cacheKey}, func(tx state.Tx) error {
		return tx.Put(ctx, state.Record{Key: cacheKey, Value: []byte("invalid certificate")})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = e.issueSCEP(ctx, csr, ca.Policy{}, password, false); err == nil {
		t.Fatal("corrupt cached certificate returned")
	}
	if err = st.Update(
		ctx,
		[]string{cacheKey},
		func(tx state.Tx) error { return tx.Put(ctx, cached) },
	); err != nil {
		t.Fatal(err)
	}
	denied.Store(true)
	if _, err = e.issueSCEP(
		ctx,
		csr,
		ca.Policy{},
		password,
		false,
	); !errors.Is(
		err,
		webauth.ErrDenied,
	) {
		t.Fatal("issuance ignored removed ownership", err)
	}
	if err = e.verifySCEPGrant(ctx, password, csr); !errors.Is(err, webauth.ErrDenied) {
		t.Fatal("removed eligibility accepted", err)
	}
	denied.Store(false)
	k := scepGrantKey(password)
	if err = st.Update(ctx, []string{k}, func(tx state.Tx) error {
		r, err := tx.Get(ctx, k)
		if err != nil {
			return err
		}
		var g scepGrant
		if err = json.Unmarshal(r.Value, &g); err != nil {
			return err
		}
		g.ExpiresAt = tx.Now().Add(-time.Second)
		r.Value, err = json.Marshal(g)
		if err != nil {
			return err
		}
		return tx.Put(ctx, r)
	}); err != nil {
		t.Fatal(err)
	}
	if err = e.verifySCEPGrant(ctx, password, csr); err == nil {
		t.Fatal("expired grant accepted")
	}
}

type admissionDEPStore struct {
	dep.Store
	calls  int
	device *dep.StoredDevice
	fail   error
}

func (s *admissionDEPStore) ListAccounts(
	_ context.Context,
	page paging.Page,
) (paging.Result[dep.Account], error) {
	s.calls++
	if page.Cursor == "" {
		return paging.Result[dep.Account]{
			Items:      []dep.Account{{Name: "first", ProfileUUID: "approved"}},
			NextCursor: "second",
		}, nil
	}
	return paging.Result[dep.Account]{
		Items: []dep.Account{{Name: "second", ProfileUUID: "approved"}},
	}, nil
}

func (s *admissionDEPStore) GetDevice(
	_ context.Context,
	account, _ string,
) (*dep.StoredDevice, error) {
	if s.fail != nil {
		return nil, s.fail
	}
	if account == "first" {
		return nil, dep.ErrNotFound
	}
	return s.device, nil
}

func TestDEPAdmissionRequiresCurrentAssignmentAndTraversesPages(t *testing.T) {
	store := &admissionDEPStore{
		device: &dep.StoredDevice{
			Device: dep.Device{
				SerialNumber:  "serial",
				ProfileUUID:   "approved",
				ProfileStatus: dep.ProfileStatusAssigned,
			},
		},
	}
	a := &App{dep: &depService{store: store}}
	service := &acmeService{app: a}
	allowed, err := service.depLookup(t.Context(), "serial")
	if err != nil || !allowed || store.calls != 2 {
		t.Fatal(allowed, err, store.calls)
	}
	for _, status := range []string{"", dep.ProfileStatusEmpty, dep.ProfileStatusRemoved, "unknown"} {
		store.device.ProfileStatus = status
		if allowed, err = service.depLookup(t.Context(), "serial"); err != nil || allowed {
			t.Fatal("invalid assignment admitted", status, err)
		}
	}
	store.device.ProfileStatus = dep.ProfileStatusPushed
	store.device.Deleted = true
	if allowed, err = service.depLookup(t.Context(), "serial"); err != nil || allowed {
		t.Fatal("tombstone admitted", err)
	}
	store.device.Deleted = false
	store.device.ProfileUUID = "different-profile"
	if allowed, err = service.depLookup(t.Context(), "serial"); err != nil || allowed {
		t.Fatal("wrong profile admitted", err)
	}
	store.fail = errors.New("inventory unavailable")
	if _, err = service.depLookup(t.Context(), "serial"); !errors.Is(err, store.fail) {
		t.Fatal("inventory failure hidden", err)
	}
	a.dep = nil
	if _, err = service.depLookup(t.Context(), "serial"); err == nil {
		t.Fatal("missing store accepted")
	}
}

func TestSCEPRenewalPreservesAdmissionAndRejectsRemovedOwnership(t *testing.T) {
	st := state.NewMemory()
	a := &App{cfg: Config{Clock: clock.Real{}}, protocol: st}
	cert, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var denied atomic.Bool
	e := &enrollment{
		state:  st,
		app:    a,
		now:    time.Now,
		caCert: cert,
		caKey:  key,
		admission: func(_ context.Context, r AdmissionRequest) (AdmissionGrant, error) {
			if denied.Load() || r.UDID != "mac" {
				return AdmissionGrant{}, webauth.ErrDenied
			}
			return AdmissionGrant{ExpiresAt: time.Now().Add(time.Hour)}, nil
		},
	}
	a.enroll = e
	e.depot = &enrollmentDepot{app: a, Depot: ca.NewMemoryDepot()}
	e.local, err = ca.NewLocal(cert, key, ca.WithDepot(e.depot))
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := scep.NewServer(
		e.local,
		cert,
		key,
		scep.WithChallenge(enrollmentChallenge{app: a}),
		scep.WithIssuance(e.issueSCEP),
	)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewTLSServer(endpoint.Handler())
	defer srv.Close()
	client := scep.NewClient(srv.URL, srv.Client())
	password, err := e.issueSCEPGrant(
		t.Context(),
		acme.Binding{CommonName: "allowed", UDID: "mac"},
		AdmissionGrant{ExpiresAt: time.Now().Add(time.Hour)},
	)
	if err != nil {
		t.Fatal(err)
	}
	deviceKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.Enroll(
		t.Context(),
		deviceKey,
		scep.EnrollOptions{Subject: pkix.Name{CommonName: "allowed"}, Challenge: password},
	)
	if err != nil {
		t.Fatal(err)
	}
	options := scep.EnrollOptions{
		Subject: pkix.Name{CommonName: "allowed"},
		Renew:   &scep.Identity{Cert: first, Key: deviceKey},
	}
	renewed, err := client.Enroll(t.Context(), deviceKey, options)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first.Raw, renewed.Raw) {
		t.Fatal("renewal did not issue a new certificate")
	}
	record, err := st.Get(t.Context(), "issued-identity:"+cms.Fingerprint(renewed))
	if err != nil {
		t.Fatal(err)
	}
	var evidence identityEvidence
	if err = json.Unmarshal(record.Value, &evidence); err != nil || evidence.UDID != "mac" {
		t.Fatal(evidence, err)
	}
	denied.Store(true)
	if _, err = client.Enroll(t.Context(), deviceKey, options); err == nil {
		t.Fatal("renewed after ownership removal")
	}
	denied.Store(false)
	k := "issued-identity:" + cms.Fingerprint(first)
	if err = st.Update(
		t.Context(),
		[]string{k},
		func(tx state.Tx) error { return tx.Delete(t.Context(), k) },
	); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Enroll(t.Context(), deviceKey, options); err == nil {
		t.Fatal("renewed without issuance provenance")
	}
}
