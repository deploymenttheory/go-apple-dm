package acme_test

import (
	"context"
	"crypto/x509/pkix"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
)

// Software-key macOS enrollment can answer device-attest-01 without attObj.
// This must use the same explicit policy as an empty attestation statement.
func TestChallengeWithoutAttestationObject(t *testing.T) {
	for _, payload := range []map[string]string{{}, {"attObj": ""}} {
		t.Run("Allowed", func(t *testing.T) {
			f := newFixture(t, func(c *acme.Config) { c.AllowUnattested = true })
			fl := f.begin(testIdentifier)
			if got := fl.challenge().Status; got != acme.StatusPending {
				t.Fatalf("POST-as-GET changed status: %s", got)
			}
			requireStatus(t, fl.acct.post(fl.chalURL, payload), http.StatusOK)
			if got := fl.order().Status; got != acme.StatusReady {
				t.Fatalf("order status = %s", got)
			}
			requireStatus(t, fl.finalizeWith(fl.key, pkix.Name{}), http.StatusOK)
			if got := fl.order().Status; got != acme.StatusValid {
				t.Fatalf("order status = %s", got)
			}
		})
		t.Run("DefaultRefuses", func(t *testing.T) {
			fl := newFixture(t).begin(testIdentifier)
			requireProblem(t, fl.acct.post(fl.chalURL, payload), acme.ProblemMalformed)
			if got := fl.order().Status; got != acme.StatusInvalid {
				t.Fatalf("order status = %s", got)
			}
		})
		t.Run("RequiredAttestationCannotDowngrade", func(t *testing.T) {
			f := newFixture(t, func(c *acme.Config) { c.AllowUnattested = true })
			binding := f.ids[testIdentifier]
			binding.RequireAttestation = true
			f.ids[testIdentifier] = binding
			fl := f.begin(testIdentifier)
			requireProblem(t, fl.acct.post(fl.chalURL, payload), acme.ProblemBadAttestationStatement)
		})
	}
	t.Run("ScopedPolicyRecheckedAtFinalize", func(t *testing.T) {
		var allowed atomic.Bool
		allowed.Store(true)
		var calls atomic.Int32
		f := newFixture(t, func(c *acme.Config) {
			c.AuthorizeUnattested = acme.PolicyFunc(func(_ context.Context, d *acme.Decision) error {
				calls.Add(1)
				if d.Attestation != nil {
					t.Error("unattested decision carries an attestation")
				}
				if !allowed.Load() {
					return acme.NewProblem(acme.ProblemUnauthorized, "permission revoked")
				}
				return nil
			})
		})
		fl := f.begin(testIdentifier)
		requireStatus(t, fl.acct.post(fl.chalURL, map[string]string{}), http.StatusOK)
		allowed.Store(false)
		requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemUnauthorized)
		if calls.Load() < 2 {
			t.Fatalf("policy called %d times", calls.Load())
		}
	})
	t.Run("MalformedEvidenceNeverDowngrades", func(t *testing.T) {
		for _, payload := range []any{json.RawMessage("null"), []string{}, map[string]any{"attObj": 42}, map[string]string{"attObj": "!"}, map[string]string{"attObj": "bm90LWNib3I"}} {
			f := newFixture(t, func(c *acme.Config) { c.AllowUnattested = true })
			fl := f.begin(testIdentifier)
			response := fl.acct.post(fl.chalURL, payload)
			if response.status == http.StatusOK {
				t.Fatalf("malformed response accepted: %#v", payload)
			}
		}
	})
}
