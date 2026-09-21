package app

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// TestCertificateWorkerRenewsOnlyManagedLeavesAndPersistsNotices checks certificate worker renews
// only managed leaves and persists notices.
func TestCertificateWorkerRenewsOnlyManagedLeavesAndPersistsNotices(t *testing.T) {
	a, clock := memorySetupApp(t)
	ctx := t.Context()
	v := setupExecute(t, a, lifecycle.HTTPS, "lab", setupRequest("https", lifecycle.HTTPS))
	setupExecute(t, a, lifecycle.HTTPS, "activate", SetupRequest{Revision: v.Identity.Pending})
	clock.Advance(340 * 24 * time.Hour)
	setupRequire(t, a.certificateRenewalPass(ctx), nil)
	current, err := a.Certificates.Get(ctx, "https")
	setupRequire(t, err, nil)
	if current.Active != "2" || current.Pending != "" {
		t.Fatal("scheduled HTTPS renewal did not activate", current)
	}
	setupRequire(t, a.certificateRenewalPass(ctx), nil)
	v = setupExecute(t, a, lifecycle.Push, "request", setupRequest("push", lifecycle.Push))
	if err := a.advanceCertificate(ctx, *v.Identity); err == nil {
		t.Fatal("missing vendor not reported")
	}
	a.cfg.Setup.Role = "customer"
	setupRequire(t, a.advanceCertificate(ctx, *v.Identity), nil)
	a.cfg.Setup.Role = "combined"
	editSetupIdentity(t, a.protocol, "push", func(r map[string]any) {
		requireType[map[string]any](t, requireType[[]any](t, r["Revisions"])[0])["SignedRequest"] = base64.StdEncoding.EncodeToString(
			[]byte("already signed"),
		)
	})
	setupRequire(t, a.advanceCertificate(ctx, *v.Identity), nil)
	v = setupExecute(t, a, lifecycle.HTTPS, "request", setupRequest("imported", lifecycle.HTTPS))
	setupRequire(t, a.advanceCertificate(ctx, *v.Identity), nil)
	setupRequire(
		t,
		a.advanceCertificate(
			ctx,
			lifecycle.Identity{Request: lifecycle.Request{Kind: lifecycle.Vendor}},
		),
		nil,
	)
	setupRequire(
		t,
		a.advanceCertificate(
			ctx,
			lifecycle.Identity{Request: lifecycle.Request{Kind: lifecycle.Issuer}},
		),
		nil,
	)
	ca := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }),
	)
	defer ca.Close()
	req := setupRequest("public", lifecycle.HTTPS)
	req.PublicACME = &lifecycle.PublicACMEOptions{
		Directory:   ca.URL,
		Contact:     "operator@example.com",
		AcceptTerms: true,
	}
	v = setupExecute(t, a, lifecycle.HTTPS, "acme", req)
	if err := a.advanceCertificate(ctx, *v.Identity); err == nil {
		t.Fatal("CA rejection ignored")
	}
}

// TestCertificateWorkerRejectsBrokenAndForeignMaterial checks that certificate worker rejects
// broken and foreign material.
func TestCertificateWorkerRejectsBrokenAndForeignMaterial(t *testing.T) {
	for _, mode := range []string{"leaf PEM", "leaf DER", "CA PEM", "CA DER", "different CA", "missing CA", "missing material", "ACME read failure", "CA read failure", "issuance conflict"} {
		t.Run(mode, func(t *testing.T) {
			a, clock := memorySetupApp(t)
			v := setupExecute(t, a, lifecycle.HTTPS, "lab", setupRequest("https", lifecycle.HTTPS))
			setupExecute(
				t,
				a,
				lifecycle.HTTPS,
				"activate",
				SetupRequest{Revision: v.Identity.Pending},
			)
			clock.Advance(time.Hour)
			v = setupExecute(t, a, lifecycle.HTTPS, "renew", SetupRequest{Force: true})
			if mode == "leaf PEM" || mode == "leaf DER" || mode == "CA PEM" || mode == "CA DER" {
				id := "https"
				if mode == "CA PEM" || mode == "CA DER" {
					id = "https-ca"
				}
				bad := []byte("broken")
				if mode == "leaf DER" || mode == "CA DER" {
					bad = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: bad})
				}
				editSetupIdentity(t, a.protocol, id, func(r map[string]any) {
					requireType[map[string]any](t, requireType[[]any](t, r["Revisions"])[0])["Certificate"] = base64.StdEncoding.EncodeToString(
						bad,
					)
				})
			}
			switch mode {
			case "different CA":
				other := setupExecute(
					t,
					a,
					lifecycle.Issuer,
					"create",
					setupRequest("other", lifecycle.Issuer),
				)
				setupExecute(
					t,
					a,
					lifecycle.Issuer,
					"activate",
					SetupRequest{
						Request:  lifecycle.Request{ID: "other"},
						Revision: other.Identity.Pending,
					},
				)
				a.cfg.Setup.HTTPSCAID = "other"
			case "missing CA":
				a.cfg.Setup.HTTPSCAID = "missing"
			case "missing material":
				v.Identity.Active = "missing"
			case "ACME read failure":
				a.Certificates.Store = issuanceStateFault{
					Store:   a.protocol,
					readErr: io.ErrUnexpectedEOF,
				}
			case "CA read failure":
				a.cfg.Setup.HTTPSCAID = "../invalid"
			case "issuance conflict":
				v.Identity.Pending = "missing"
			}
			err := a.advanceCertificate(t.Context(), *v.Identity)
			if mode == "different CA" || mode == "missing CA" {
				setupRequire(t, err, nil)
			} else if err == nil {
				t.Fatal("invalid material renewed")
			}
		})
	}
}

// TestManagedIssuerWorkerRoutesAndDynamicTrust checks managed issuer worker routes and dynamic
// trust.
func TestManagedIssuerWorkerRoutesAndDynamicTrust(t *testing.T) {
	a, _, _, _ := renewalFixture(t)
	ctx := t.Context()
	if _, err := a.certificateRegistry(ctx); err != nil {
		t.Fatal(err)
	}
	for _, isACME := range []bool{false, true} {
		base := PathSCEP
		if isACME {
			base = PathACME
		}
		for _, rev := range []string{"2", "missing"} {
			w := httptest.NewRecorder()
			a.managedIssuanceHandler(isACME).
				ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", base+"/issuers/"+rev+"/directory", nil))
			if rev == "missing" && w.Code != 404 {
				t.Fatal("unknown issuer route accepted")
			}
		}
	}
	called := false
	a.legacyIssuance(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).
		ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), "GET", PathSCEP, nil))
	if !called {
		t.Fatal("retained legacy route unavailable")
	}
	config, err := captureManagedTLS(t, a)
	setupRequire(t, err, nil)
	if config.ClientAuth != tls.VerifyClientCertIfGiven || config.GetCertificate == nil {
		t.Fatal("managed TLS policy missing")
	}
	mux := http.NewServeMux()
	setupRequire(t, a.wireServiceConfig(ctx, a.enroll, mux), nil)
	for _, path := range []string{PathServiceConfig, PathTrustAnchors, PathTrustProfile} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", path, nil))
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	a.Certificates.Store = issuanceStateFault{
		Store:     a.protocol,
		readErr:   io.ErrUnexpectedEOF,
		txReadErr: io.ErrUnexpectedEOF,
	}
	for _, path := range []string{PathTrustAnchors, PathTrustProfile} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", path, nil))
		if w.Code != 503 {
			t.Fatal("unavailable trust not reported", path, w.Code)
		}
	}
	_, err = captureManagedTLS(t, a)
	setupRequire(t, err, io.ErrUnexpectedEOF)
	_, _, err = a.profileSigningIdentity(a.enroll)(ctx)
	setupRequire(t, err, io.ErrUnexpectedEOF)
	_, err = a.managedIssuer(ctx, "2")
	setupRequire(t, err, io.ErrUnexpectedEOF)
	_, err = a.managedRegistry(ctx)
	setupRequire(t, err, io.ErrUnexpectedEOF)
	setupRequire(t, a.wireServiceConfig(ctx, a.enroll, http.NewServeMux()), io.ErrUnexpectedEOF)
	a.enroll = nil
	_, err = captureManagedTLS(t, a)
	setupRequire(t, err, nil)
	_, err = a.managedIssuer(ctx, "2")
	setupRequire(t, err, lifecycle.ErrConflict)
}

// OTA and revocation routes must re-read managed trust after startup. An issuer
// store outage must fail closed instead of serving stale trust or a stale CRL.
func TestManagedOTAAndRevocationRoutesRequireCurrentTrust(t *testing.T) {
	a, _, _, _ := renewalFixture(t)
	anchor := filepath.Join(t.TempDir(), "bootstrap.pem")
	setupRequire(t, os.WriteFile(anchor, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: a.enroll.caCert.Raw}), 0o600), nil)
	a.enroll.cfg.Identity = IdentitySCEP
	a.enroll.cfg.OTAAnchorFile = anchor
	a.enroll.cfg.OTAChallenge = "disposable-challenge"
	a.cfg.PKI.Enabled = true
	mux := http.NewServeMux()
	setupRequire(t, a.wireOTA(mux), nil)
	setupRequire(t, a.wirePKI(t.Context(), a.enroll, mux), nil)
	crlPath := "/pki/crl/" + cms.Fingerprint(a.enroll.caCert)
	for _, unavailable := range []bool{false, true} {
		if unavailable {
			a.Certificates.Store = issuanceStateFault{Store: a.protocol, readErr: io.ErrUnexpectedEOF, txReadErr: io.ErrUnexpectedEOF}
		}
		for _, path := range []string{"/ota", crlPath} {
			w := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil)
			if path == crlPath {
				r.Method = http.MethodGet
			}
			mux.ServeHTTP(w, r)
			want := http.StatusForbidden // An unsigned OTA request has no trusted signer.
			if path == crlPath {
				want = http.StatusOK
			}
			if unavailable {
				want = http.StatusServiceUnavailable
			}
			if w.Code != want {
				t.Fatalf("%s (trust unavailable=%t): %d %s", path, unavailable, w.Code, w.Body.String())
			}
		}
	}
	a.enroll.cfg.OTAChallenge = ""
	if err := a.wireOTA(http.NewServeMux()); err == nil {
		t.Fatal("OTA configured without an admission challenge")
	}
	a.enroll.cfg.OTAChallenge = "disposable-challenge"
	a.enroll.cfg.OTAAnchorFile = filepath.Join(t.TempDir(), "missing.pem")
	if err := a.wireOTA(http.NewServeMux()); err == nil {
		t.Fatal("OTA configured without bootstrap trust")
	}
}

// TestIssuerRenewalWorkerPreparesSuccessorAndRestartsPreparedRollover checks issuer renewal worker
// prepares successor and restarts prepared rollover.
func TestIssuerRenewalWorkerPreparesSuccessorAndRestartsPreparedRollover(t *testing.T) {
	for _, id := range []string{"issuer", "https-ca"} {
		t.Run(id, func(t *testing.T) {
			a, _, _, job := renewalFixture(t)
			// A completed previous transition allows the next worker pass to inspect
			// the active root; the HTTPS authority has no previous transition.
			if id == "issuer" {
				v, err := a.Certificates.Get(t.Context(), id)
				setupRequire(t, err, nil)
				v.Pending = "missing"
				if err = a.advanceCertificate(t.Context(), v); err == nil {
					t.Fatal("missing successor ignored")
				}
				job.Phase = "prepared"
				setupJSON(t, a.protocol, "pki/lifecycle/rollover/issuer/2", job)
				if err = a.identityRenewalPass(t.Context()); err == nil {
					t.Fatal("stale prepared job ignored")
				}
			} else {
				future := clock.NewFake(a.cfg.Clock.Now().Add(time.Hour))
				a.cfg.Clock = future
				requireType[*state.Memory](t, a.protocol).Now = future.Now
				v := setupExecute(
					t,
					a,
					lifecycle.Issuer,
					"renew",
					SetupRequest{Request: lifecycle.Request{ID: id}, Force: true},
				)
				setupRequire(t, a.advanceCertificate(t.Context(), *v.Identity), nil)
				job, err := a.Certificates.Rollover(t.Context(), id, v.Identity.Pending)
				setupRequire(t, err, nil)
				if job.Phase != "prepared" {
					t.Fatal("HTTPS authority switched before trust", job)
				}
			}
		})
	}
}

// TestIdleIdentityWorkerHandlesEvidenceAndStorageFailures checks idle identity worker handles
// evidence and storage failures.
func TestIdleIdentityWorkerHandlesEvidenceAndStorageFailures(t *testing.T) {
	a, e, _, job := renewalFixture(t)
	job.Phase = "complete"
	setupJSON(t, a.protocol, "pki/lifecycle/rollover/issuer/2", job)
	setupRequire(t, a.identityRenewalPass(t.Context()), nil)
	setupJSON(
		t,
		a.protocol,
		"issued-identity:"+e.CertHash,
		identityEvidence{
			Issuer:   "issuer",
			Method:   "scep",
			NotAfter: a.cfg.Clock.Now().Add(365 * 24 * time.Hour),
		},
	)
	setupRequire(t, a.identityRenewalPass(t.Context()), nil)
	setupRequire(t, a.Store.Import(t.Context(), storageExportWithoutEvidence(e.ID)), nil)
	setupRequire(t, a.identityRenewalPass(t.Context()), nil)
	a.enroll = nil
	setupRequire(t, a.identityRenewalPass(t.Context()), nil)
}

// storageExportWithoutEvidence builds an enabled enrollment export with an unknown certificate pin
// and no issuance evidence.
func storageExportWithoutEvidence(id mdm.EnrollmentID) storage.EnrollmentExport {
	return storage.EnrollmentExport{
		Enrollment: storage.Enrollment{ID: id, Enabled: true, CertHash: "unknown"},
	}
}

// captureManagedTLS captures the managed TLS configuration or error during a test client
// handshake.
func captureManagedTLS(t *testing.T, a *App) (*tls.Config, error) {
	t.Helper()
	type result struct {
		config *tls.Config
		err    error
	}
	results := make(chan result, 1)
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	server.TLS = &tls.Config{
		MinVersion: tls.VersionTLS12,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			config, err := a.ManagedTLSConfig(hello)
			results <- result{config, err}
			return nil, err
		},
	}
	server.StartTLS()
	defer server.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	setupRequire(t, err, nil)
	response, err := server.Client().Do(request)
	if err == nil {
		_ = response.Body.Close()
	}
	out := <-results
	return out.config, out.err
}
