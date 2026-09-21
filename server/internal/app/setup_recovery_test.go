package app

import (
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/storagetest"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// TestSetupLabResumesAfterEachRepositoryFailure checks setup lab resumes after each repository
// failure.
func TestSetupLabResumesAfterEachRepositoryFailure(t *testing.T) {
	for _, phase := range []string{"read", "write"} {
		for failAt := 1; failAt <= 18; failAt++ {
			a, _ := memorySetupApp(t)
			backing := a.Certificates.Store
			seen, hit := 0, false
			a.Certificates.Store = &setupStateFault{Store: backing, fail: func(op, _ string) error {
				if op == phase {
					seen++
					if seen == failAt {
						hit = true
						return io.ErrUnexpectedEOF
					}
				}
				return nil
			}}
			_, err := a.ExecuteSetup(
				t.Context(),
				lifecycle.HTTPS,
				"lab",
				setupRequest("https", lifecycle.HTTPS),
			)
			if hit {
				setupRequire(t, err, io.ErrUnexpectedEOF)
			} else {
				setupRequire(t, err, nil)
			}
			a.Certificates.Store = backing
			resumed := setupExecute(
				t,
				a,
				lifecycle.HTTPS,
				"lab",
				setupRequest("https", lifecycle.HTTPS),
			)
			if resumed.Identity.Pending != "1" || len(resumed.Identity.Revisions) != 1 ||
				resumed.Identity.Revisions[0].Phase != "ready" {
				t.Fatal("retry changed workflow identity", resumed.Identity)
			}
			if !hit {
				break
			}
		}
	}
}

// TestSetupRolloverOperationsAndManagedCertificateSource checks setup rollover operations and
// managed certificate source.
func TestSetupRolloverOperationsAndManagedCertificateSource(t *testing.T) {
	a, _, _, _ := renewalFixture(t)
	ctx := t.Context()
	setupExecute(t, a, lifecycle.Issuer, "rollover", SetupRequest{Revision: "2"})
	_, err := a.ExecuteSetup(ctx, lifecycle.Issuer, "retire", SetupRequest{Revision: "1"})
	setupRequire(t, err, lifecycle.ErrConflict)
	v := setupExecute(
		t,
		a,
		lifecycle.Issuer,
		"renew",
		SetupRequest{Request: lifecycle.Request{ID: "https-ca"}, Force: true},
	)
	_, err = a.Certificates.CreateIssuer(ctx, "https-ca", v.Identity.Pending, 20*365*24*time.Hour)
	setupRequire(t, err, nil)
	setupExecute(
		t,
		a,
		lifecycle.Issuer,
		"rollover",
		SetupRequest{Request: lifecycle.Request{ID: "https-ca"}, Revision: "2"},
	)
	_, err = a.ExecuteSetup(
		ctx,
		lifecycle.Issuer,
		"retire",
		SetupRequest{Request: lifecycle.Request{ID: "https-ca"}, Revision: "1"},
	)
	setupRequire(t, err, lifecycle.ErrNotFound)
	called := false
	handler := a.certSource()(
		http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(204) },
		),
	)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "/mdm", nil))
	if !called || w.Code != 204 {
		t.Fatal("managed source did not delegate", w.Code)
	}
	a.Certificates.Store = issuanceStateFault{
		Store:     a.protocol,
		readErr:   io.ErrUnexpectedEOF,
		txReadErr: io.ErrUnexpectedEOF,
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "/mdm", nil))
	if w.Code != 503 {
		t.Fatal("unavailable certificate roots accepted", w.Code)
	}
	setupRequire(t, a.enroll.loadCA(ctx, a), io.ErrUnexpectedEOF)
	a.Certificates.Store = a.protocol
	editSetupIdentity(t, a.protocol, "issuer", func(r map[string]any) {
		requireType[map[string]any](t, requireType[[]any](t, r["Revisions"])[0])["Key"] = base64.StdEncoding.EncodeToString(
			[]byte("corrupt"),
		)
	})
	if err := a.enroll.loadCA(ctx, a); err == nil {
		t.Fatal("corrupt issuer loaded")
	}
}

// TestCertificateNoticeAndIssuerFailuresRemainRetryable checks certificate notice and issuer
// failures remain retryable.
func TestCertificateNoticeAndIssuerFailuresRemainRetryable(t *testing.T) {
	for _, phase := range []string{"new request", "notice", "rollover read", "rollover progress", "issuer PEM", "issuer DER", "issuer missing"} {
		t.Run(phase, func(t *testing.T) {
			a, _, _, job := renewalFixture(t)
			if phase == "issuer PEM" || phase == "issuer DER" || phase == "issuer missing" {
				v, err := a.Certificates.Get(t.Context(), "issuer")
				setupRequire(t, err, nil)
				v.Pending = "next"
				if phase == "issuer missing" {
					v.Active = "missing"
				} else {
					bad := []byte("corrupt")
					if phase == "issuer DER" {
						bad = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: bad})
					}
					editSetupIdentity(t, a.protocol, "issuer", func(r map[string]any) {
						requireType[map[string]any](t, requireType[[]any](t, r["Revisions"])[1])["Certificate"] = base64.StdEncoding.EncodeToString(
							bad,
						)
					})
				}
				if err := a.advanceCertificate(t.Context(), v); err == nil {
					t.Fatal("unusable issuer renewed")
				}
				return
			}
			if phase == "rollover read" || phase == "rollover progress" {
				job.IssuerID, job.Phase = "https-ca", "prepared"
				key := "pki/lifecycle/rollover/https-ca/1"
				if phase == "rollover read" {
					setupJSON(t, a.protocol, key, "invalid job")
				} else {
					setupJSON(t, a.protocol, key, job)
					a.Store = &storagetest.Failing{
						Store: a.Store,
						Fail:  map[string]error{"List": io.ErrUnexpectedEOF},
					}
				}
				if err := a.certificateRenewalPass(t.Context()); err == nil {
					t.Fatal("broken rollover scan accepted")
				}
				return
			}
			c := clock.NewFake(a.cfg.Clock.Now().Add(10*365*24*time.Hour - time.Hour))
			a.cfg.Clock, requireType[*state.Memory](t, a.protocol).Now = c, c.Now
			if phase == "notice" {
				item, err := a.Certificates.Get(t.Context(), "https-ca")
				setupRequire(t, err, nil)
				_, err = a.Certificates.Begin(t.Context(), item.Request)
				setupRequire(t, err, nil)
			}
			backing := a.protocol
			a.Certificates.Store = &setupStateFault{
				Store: backing,
				fail: func(op, key string) error {
					if phase == "new request" && op == "write" &&
						key == "pki/lifecycle/identity/https-ca" {
						return io.ErrUnexpectedEOF
					}
					if phase == "notice" && op == "write" &&
						key != "pki/lifecycle/identity/https-ca" {
						return io.ErrUnexpectedEOF
					}
					return nil
				},
			}
			if err := a.certificateRenewalPass(t.Context()); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal("failed renewal metadata ignored", err)
			}
		})
	}
}

// TestMigrationPersistsDisabledBlockedAndRetryStates checks migration persists disabled blocked
// and retry states.
func TestMigrationPersistsDisabledBlockedAndRetryStates(t *testing.T) {
	for _, mode := range []string{"disabled", "lookup failure", "replacement failure", "unsupported trust", "enqueue failure"} {
		t.Run(mode, func(t *testing.T) {
			a, e, target, job := renewalFixture(t)
			ctx := t.Context()
			switch mode {
			case "disabled":
				setupRequire(t, a.Store.Disable(ctx, e.ID, a.cfg.Clock.Now()), nil)
			case "lookup failure":
				a.Store = &storagetest.Failing{
					Store: a.Store,
					Fail:  map[string]error{"Get": io.ErrUnexpectedEOF},
				}
			case "replacement failure":
				a.Store = renewalReplacementStore{Store: a.Store, readErr: io.ErrUnexpectedEOF}
			case "unsupported trust":
				e.Device.OSVersion, e.Device.ProductName = "1.0", "Unknown"
				setupRequire(t, a.Store.Import(ctx, storage.EnrollmentExport{Enrollment: *e}), nil)
			case "enqueue failure":
				core, err := service.New(
					service.Config{
						Store: &storagetest.Failing{
							Store: a.Store,
							Fail:  map[string]error{"Enqueue": io.ErrUnexpectedEOF},
						},
						Clock: a.cfg.Clock,
					},
				)
				setupRequire(t, err, nil)
				a.Core = core
			}
			if mode == "replacement failure" {
				m := lifecycle.Migration{Device: e.ID.ID, Phase: "queued"}
				setupRequire(t, a.progressMigration(ctx, target, e, &m, false), io.ErrUnexpectedEOF)
				setupRequire(t, a.renewOneIdentity(ctx, *e), nil)
				status, err := a.deviceRenewalStatus(ctx, e.ID.ID)
				setupRequire(t, err, nil)
				if status.Reason != "identity renewal will retry" {
					t.Fatal(status)
				}
				return
			}
			err := a.advanceRollover(ctx, job)
			if mode == "lookup failure" {
				setupRequire(t, err, io.ErrUnexpectedEOF)
				return
			}
			setupRequire(t, err, nil)
			rows, _, err := a.Certificates.Migrations(ctx, "issuer", job.To, "", 100)
			setupRequire(t, err, nil)
			if mode == "disabled" && rows[0].Phase != "disabled" {
				t.Fatal("disabled device retained as pending", rows)
			}
			if mode == "enqueue failure" && rows[0].Reason != "migration will retry" {
				t.Fatal("enqueue failure lost", rows)
			}
			if mode == "unsupported trust" && rows[0].Phase != "blocked" {
				t.Fatal("unsupported device not blocked", rows)
			}
		})
	}
}
