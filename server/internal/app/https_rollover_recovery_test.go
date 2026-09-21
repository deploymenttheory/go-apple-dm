package app

import (
	"encoding/base64"
	"io"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// TestHTTPSTrustRecoveryResumesAfterEveryRepositoryFailure checks HTTPS trust recovery resumes
// after every repository failure.
func TestHTTPSTrustRecoveryResumesAfterEveryRepositoryFailure(t *testing.T) {
	for _, phase := range []string{"read", "write"} {
		for failAt := 1; failAt <= 40; failAt++ {
			a, job := httpsRecoveryFixture(t, "old")
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
			err := a.activateHTTPSTrust(t.Context(), job)
			if hit {
				setupRequire(t, err, io.ErrUnexpectedEOF)
			} else {
				setupRequire(t, err, nil)
			}
			a.Certificates.Store = backing
			setupRequire(t, a.activateHTTPSTrust(t.Context(), job), nil)
			current, err := a.Certificates.Rollover(t.Context(), job.IssuerID, job.To)
			setupRequire(t, err, nil)
			if current.Phase != "complete" {
				t.Fatal("interrupted rollover did not recover", current)
			}
			if !hit {
				break
			}
		}
	}
}

// httpsRecoveryFixture builds an HTTPS issuer-rollover fixture with the requested
// pending-certificate state.
func httpsRecoveryFixture(t *testing.T, pending string) (*App, lifecycle.Rollover) {
	t.Helper()
	a, _, _, _ := renewalFixture(t)
	v := setupExecute(t, a, lifecycle.HTTPS, "lab", setupRequest("https", lifecycle.HTTPS))
	setupExecute(t, a, lifecycle.HTTPS, "activate", SetupRequest{Revision: v.Identity.Pending})
	c := clock.NewFake(a.cfg.Clock.Now().Add(time.Hour))
	a.cfg.Clock = c
	requireType[*state.Memory](t, a.protocol).Now = c.Now
	ca := setupExecute(
		t,
		a,
		lifecycle.Issuer,
		"renew",
		SetupRequest{Request: lifecycle.Request{ID: "https-ca"}, Force: true},
	)
	_, err := a.Certificates.CreateIssuer(
		t.Context(),
		"https-ca",
		ca.Identity.Pending,
		20*365*24*time.Hour,
	)
	setupRequire(t, err, nil)
	job, err := a.Certificates.PrepareRollover(t.Context(), "https-ca", "2", nil)
	setupRequire(t, err, nil)
	if pending != "" {
		v = setupExecute(t, a, lifecycle.HTTPS, "renew", SetupRequest{Force: true})
		issuer := "https-ca"
		if pending == "new" {
			_, err = a.Certificates.ActivateRollover(t.Context(), "https-ca", "2")
			setupRequire(t, err, nil)
		} else if pending == "foreign" {
			other := setupExecute(
				t,
				a,
				lifecycle.Issuer,
				"create",
				setupRequest("foreign", lifecycle.Issuer),
			)
			setupExecute(
				t,
				a,
				lifecycle.Issuer,
				"activate",
				SetupRequest{
					Request:  lifecycle.Request{ID: "foreign"},
					Revision: other.Identity.Pending,
				},
			)
			issuer = "foreign"
		}
		if pending != "empty" {
			_, err = a.Certificates.IssueHTTPS(t.Context(), "https", v.Identity.Pending, issuer)
			setupRequire(t, err, nil)
		}
	}
	return a, job
}

// TestHTTPSTrustActivationRecoversExistingPendingLeaves checks that HTTPS trust activation
// recovers existing pending leaves.
func TestHTTPSTrustActivationRecoversExistingPendingLeaves(t *testing.T) {
	for _, pending := range []string{"old", "new", "empty", "foreign"} {
		t.Run(pending, func(t *testing.T) {
			a, job := httpsRecoveryFixture(t, pending)
			before, err := a.Certificates.LoadMaterial(t.Context(), "https", "")
			setupRequire(t, err, nil)
			err = a.activateHTTPSTrust(t.Context(), job)
			if pending == "foreign" {
				setupRequire(t, err, lifecycle.ErrConflict)
				current, err := a.Certificates.Get(t.Context(), "https")
				setupRequire(t, err, nil)
				if current.Active != "1" || current.Pending != "2" {
					t.Fatal("external pending certificate overwritten", current)
				}
				return
			}
			setupRequire(t, err, nil)
			current, err := a.Certificates.Get(t.Context(), "https")
			setupRequire(t, err, nil)
			if current.Active == "1" || current.Pending != "" {
				t.Fatal("TLS was not replaced", current)
			}
			if pending == "old" && current.Revisions[1].Phase != "cancelled" {
				t.Fatal("old-authority candidate retained")
			}
			if len(before.Key) == 0 {
				t.Fatal("missing original identity")
			}
			setupRequire(t, a.activateHTTPSTrust(t.Context(), job), nil)
			_, err = a.retireHTTPSTrust(t.Context(), "https-ca", "1")
			setupRequire(t, err, nil)
		})
	}
}

// TestHTTPSTrustRecoveryRejectsDamagedMaterial checks that HTTPS trust recovery rejects damaged
// material.
func TestHTTPSTrustRecoveryRejectsDamagedMaterial(t *testing.T) {
	for _, damaged := range []string{"active leaf", "pending leaf", "new CA", "old CA", "missing active", "missing identity", "missing successor"} {
		t.Run(damaged, func(t *testing.T) {
			a, job := httpsRecoveryFixture(t, "old")
			// Model a process restart after the CA activation transaction committed.
			_, err := a.Certificates.ActivateRollover(t.Context(), "https-ca", "2")
			setupRequire(t, err, nil)
			id, index := "https", 0
			switch damaged {
			case "pending leaf":
				index = 1
			case "new CA":
				id, index = "https-ca", 1
			case "old CA":
				id = "https-ca"
			case "missing active":
				editSetupIdentity(
					t,
					a.protocol,
					"https",
					func(r map[string]any) { r["Active"] = "missing" },
				)
			case "missing identity":
				a.cfg.Setup.HTTPSID = "missing"
			case "missing successor":
				job.To = "missing"
			}
			if damaged == "active leaf" || damaged == "pending leaf" || damaged == "new CA" ||
				damaged == "old CA" {
				editSetupIdentity(t, a.protocol, id, func(r map[string]any) {
					requireType[map[string]any](t, requireType[[]any](t, r["Revisions"])[index])["Key"] = base64.StdEncoding.EncodeToString(
						[]byte("corrupt key"),
					)
				})
			}
			if err := a.activateHTTPSTrust(t.Context(), job); err == nil {
				t.Fatal("corrupt trust transition completed")
			}
			if damaged == "active leaf" || damaged == "old CA" {
				if _, err := a.retireHTTPSTrust(t.Context(), "https-ca", "1"); err == nil {
					t.Fatal("damaged authority retired")
				}
			}
		})
	}
}
