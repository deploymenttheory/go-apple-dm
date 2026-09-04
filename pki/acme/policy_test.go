package acme_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/acme/attest"
)

// decisionFor is what a policy sees once the attestation has been verified
// and matched against the binding.
func decisionFor(props *attest.Properties) *acme.Decision {
	d := &acme.Decision{
		Account:    &acme.Account{ID: "an-account"},
		Order:      &acme.Order{ID: "an-order"},
		Binding:    defaultBinding(),
		Identifier: acme.Identifier{Type: acme.IdentifierPermanent, Value: testIdentifier},
	}
	if props != nil {
		d.Attestation = &attest.Attestation{Properties: *props}
	}
	return d
}

// requireRefusal insists a policy refusal is terminal, so the challenge
// settles invalid rather than inviting a retry that would fail the same
// way.
func requireRefusal(t *testing.T, err error, kind string) *acme.Problem {
	t.Helper()
	if err == nil {
		t.Fatal("the policy allowed issuance")
	}
	p := acme.AsProblem(err)
	if p.Type != kind {
		t.Fatalf("problem type = %q, want %q", p.Type, kind)
	}
	if !p.Terminal() {
		t.Errorf("problem %v is not terminal, so the challenge would stay pending", p)
	}
	return p
}

func TestPolicies(t *testing.T) {
	yes, no := true, false

	t.Run("AllowAll", func(t *testing.T) {
		// The default is only as strong as the identifiers, which is the
		// point: a deployment minting one identifier per device already has
		// its allowlist.
		if err := acme.AllowAll().Authorize(t.Context(), decisionFor(nil)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("AllowSerials", func(t *testing.T) {
		policy := acme.AllowSerials(testSerial, "C02ANOTHERONE")
		if err := policy.Authorize(t.Context(), decisionFor(&attest.Properties{
			SerialNumber: testSerial,
		})); err != nil {
			t.Fatal(err)
		}
		for name, props := range map[string]*attest.Properties{
			"NotOnTheList":  {SerialNumber: "C02NOTONTHELIST"},
			"NoSerial":      {UDID: testUDID},
			"NoAttestation": nil,
		} {
			t.Run(name, func(t *testing.T) {
				err := policy.Authorize(t.Context(), decisionFor(props))
				p := requireRefusal(t, err, acme.ProblemUnauthorized)
				if len(p.Subproblems) != 1 || p.Subproblems[0].Identifier == nil {
					t.Fatalf("subproblems = %+v, want one naming the identifier", p.Subproblems)
				}
			})
		}
	})

	t.Run("RequireSecureBoot", func(t *testing.T) {
		policy := acme.RequireSecureBoot(attest.SecureBootFull)
		if err := policy.Authorize(t.Context(), decisionFor(&attest.Properties{
			SecureBoot: attest.SecureBootFull,
		})); err != nil {
			t.Fatal(err)
		}
		// A device that reports no status is refused too: the absence of
		// the extension is not evidence of a secured device.
		for name, props := range map[string]*attest.Properties{
			"Reduced":    {SecureBoot: attest.SecureBootReduced},
			"Permissive": {SecureBoot: attest.SecureBootPermissive},
			"NotStated":  {SerialNumber: testSerial},
		} {
			t.Run(name, func(t *testing.T) {
				err := policy.Authorize(t.Context(), decisionFor(props))
				requireRefusal(t, err, acme.ProblemUnauthorized)
			})
		}
	})

	t.Run("RequireSIP", func(t *testing.T) {
		policy := acme.RequireSIP()
		if err := policy.Authorize(t.Context(), decisionFor(&attest.Properties{
			SIPEnabled: &yes,
		})); err != nil {
			t.Fatal(err)
		}
		for name, props := range map[string]*attest.Properties{
			"Disabled":  {SIPEnabled: &no},
			"NotStated": {SerialNumber: testSerial},
		} {
			t.Run(name, func(t *testing.T) {
				err := policy.Authorize(t.Context(), decisionFor(props))
				requireRefusal(t, err, acme.ProblemUnauthorized)
			})
		}
	})

	t.Run("Chain", func(t *testing.T) {
		ran := 0
		counting := acme.PolicyFunc(func(context.Context, *acme.Decision) error {
			ran++
			return nil
		})
		if err := acme.Chain().Authorize(t.Context(), decisionFor(nil)); err != nil {
			t.Fatalf("an empty chain refused: %v", err)
		}
		policy := acme.Chain(counting, acme.AllowSerials("C02SOMEONEELSES"), counting)
		err := policy.Authorize(t.Context(), decisionFor(&attest.Properties{
			SerialNumber: testSerial,
		}))
		requireRefusal(t, err, acme.ProblemUnauthorized)
		if ran != 1 {
			t.Fatalf("%d policies ran, want the chain to stop at the first refusal", ran)
		}
		if err := acme.Chain(counting, counting).Authorize(t.Context(), decisionFor(nil)); err != nil {
			t.Fatal(err)
		}
		if ran != 3 {
			t.Fatalf("%d policies ran in total, want 3", ran)
		}
	})

	t.Run("DeviceLookup", func(t *testing.T) {
		t.Run("Found", func(t *testing.T) {
			var asked string
			lookup := acme.DeviceLookup(func(_ context.Context, serial string) (bool, error) {
				asked = serial
				return true, nil
			})
			if err := lookup.Authorize(t.Context(), decisionFor(&attest.Properties{
				SerialNumber: testSerial,
			})); err != nil {
				t.Fatal(err)
			}
			if asked != testSerial {
				t.Fatalf("looked up %q, want %q", asked, testSerial)
			}
		})

		t.Run("NotFound", func(t *testing.T) {
			lookup := acme.DeviceLookup(func(context.Context, string) (bool, error) {
				return false, nil
			})
			err := lookup.Authorize(t.Context(), decisionFor(&attest.Properties{
				SerialNumber: testSerial,
			}))
			requireRefusal(t, err, acme.ProblemUnauthorized)
		})

		t.Run("NoSerial", func(t *testing.T) {
			lookup := acme.DeviceLookup(func(context.Context, string) (bool, error) {
				t.Error("the lookup ran with nothing to look up")
				return true, nil
			})
			err := lookup.Authorize(t.Context(), decisionFor(&attest.Properties{UDID: testUDID}))
			requireRefusal(t, err, acme.ProblemUnauthorized)
		})

		// A lookup that failed is not a device that was refused. Answering
		// terminally would turn a directory outage into a device that can
		// never enroll.
		t.Run("FailureIsNotTerminal", func(t *testing.T) {
			lookup := acme.DeviceLookup(func(context.Context, string) (bool, error) {
				return false, errStore
			})
			err := lookup.Authorize(t.Context(), decisionFor(&attest.Properties{
				SerialNumber: testSerial,
			}))
			p := acme.AsProblem(err)
			if p.Type != acme.ProblemServerInternal {
				t.Fatalf("problem type = %q, want serverInternal", p.Type)
			}
			if p.Terminal() {
				t.Fatal("a lookup failure was terminal, so a directory outage would reject the device")
			}
			if !errors.Is(err, errStore) {
				t.Error("the cause was not kept for the log")
			}
		})

		t.Run("ThroughTheServer", func(t *testing.T) {
			// End to end, a lookup failure leaves the challenge pending so
			// the device can try again.
			f := newFixture(t, func(c *acme.Config) {
				c.Authorize = acme.DeviceLookup(func(context.Context, string) (bool, error) {
					return false, errStore
				})
			})
			fl := f.begin(testIdentifier)
			res := fl.answer(fl.attestation(deviceProperties()))
			if res.status != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", res.status)
			}
			if got := fl.challenge().Status; got != acme.StatusPending {
				t.Fatalf("challenge status = %q, want pending", got)
			}
		})
	})

	t.Run("PropertiesWithoutAnAttestation", func(t *testing.T) {
		// A nil attestation only reaches a policy when the server is
		// configured not to require one, and it must not be a panic.
		d := decisionFor(nil)
		if got := d.Properties(); got.Identified() || got.SIPEnabled != nil || len(got.Freshness) != 0 {
			t.Fatalf("properties = %+v, want the zero value", got)
		}
	})
}

// TestProblem covers the problem document helpers: what a device is told,
// and what a caller can test for.
func TestProblem(t *testing.T) {
	t.Run("AsProblem", func(t *testing.T) {
		if got := acme.AsProblem(nil); got != nil {
			t.Errorf("AsProblem(nil) = %v, want nil", got)
		}
		// An unexpected fault becomes an internal error, so its message
		// never reaches a device.
		plain := acme.AsProblem(errStore)
		if plain.Type != acme.ProblemServerInternal {
			t.Errorf("type = %q, want serverInternal", plain.Type)
		}
		if plain.Detail == errStore.Error() {
			t.Error("the cause leaked into the detail")
		}
		if !errors.Is(plain, errStore) {
			t.Error("the cause was not kept for the log")
		}
		original := acme.NewProblem(acme.ProblemBadCSR, "no good")
		if got := acme.AsProblem(original); got != original {
			t.Errorf("AsProblem returned %v, want the problem unchanged", got)
		}
		// A problem wrapped in something else is still that problem.
		wrapped := fmt.Errorf("issuing: %w", original)
		if got := acme.AsProblem(wrapped); got != original {
			t.Errorf("AsProblem(%v) = %v, want the wrapped problem", wrapped, got)
		}
	})

	t.Run("Terminal", func(t *testing.T) {
		cases := map[string]bool{
			acme.ProblemBadCSR:         true,
			acme.ProblemMalformed:      true,
			acme.ProblemOrderNotReady:  true,
			acme.ProblemUnauthorized:   true,
			acme.ProblemServerInternal: false,
		}
		for kind, want := range cases {
			if got := acme.NewProblem(kind, "").Terminal(); got != want {
				t.Errorf("%s terminal = %v, want %v", kind, got, want)
			}
		}
	})

	t.Run("Statuses", func(t *testing.T) {
		cases := map[string]int{
			acme.ProblemAccountDoesNotExist:     http.StatusBadRequest,
			acme.ProblemBadNonce:                http.StatusBadRequest,
			acme.ProblemBadPublicKey:            http.StatusBadRequest,
			acme.ProblemBadSignatureAlgorithm:   http.StatusBadRequest,
			acme.ProblemBadAttestationStatement: http.StatusBadRequest,
			acme.ProblemRejectedIdentifier:      http.StatusBadRequest,
			acme.ProblemUnsupportedIdentifier:   http.StatusBadRequest,
			acme.ProblemOrderNotReady:           http.StatusForbidden,
			acme.ProblemUnauthorized:            http.StatusUnauthorized,
			acme.ProblemServerInternal:          http.StatusInternalServerError,
			// A type with no entry is still answered, as a bad request.
			"somethingWeNeverDefined": http.StatusBadRequest,
		}
		for kind, want := range cases {
			if got := acme.NewProblem(kind, "").Status; got != want {
				t.Errorf("%s status = %d, want %d", kind, got, want)
			}
		}
	})

	t.Run("Is", func(t *testing.T) {
		p := acme.NewProblem(acme.ProblemBadNonce, "the nonce is not one this server issued")
		if !errors.Is(p, acme.ErrBadNonce) {
			t.Error("a badNonce problem does not match the badNonce sentinel")
		}
		if errors.Is(p, acme.ErrMalformed) {
			t.Error("a badNonce problem matched the malformed sentinel")
		}
		// A target carrying a detail is a specific problem, not a kind, so
		// it matches nothing.
		if errors.Is(p, acme.NewProblem(acme.ProblemBadNonce, "some other detail")) {
			t.Error("a target with a detail matched")
		}
		if errors.Is(p, errStore) {
			t.Error("a problem matched an unrelated error")
		}
		for _, sentinel := range []error{
			acme.ErrMalformed, acme.ErrBadNonce, acme.ErrUnauthorized, acme.ErrBadCSR,
			acme.ErrBadAttestation, acme.ErrRejected, acme.ErrOrderNotReady,
			acme.ErrAccountNotFound, acme.ErrUnsupportedID, acme.ErrInternal,
			acme.ErrBadAlgorithm, acme.ErrBadPublicKeyType,
		} {
			if !errors.Is(sentinel, sentinel) {
				t.Errorf("%v does not match itself", sentinel)
			}
		}
	})

	t.Run("URN", func(t *testing.T) {
		p := acme.NewProblem(acme.ProblemBadCSR, "")
		if got, want := p.URN(), acme.ProblemPrefix+acme.ProblemBadCSR; got != want {
			t.Fatalf("URN = %q, want %q", got, want)
		}
	})

	t.Run("Error", func(t *testing.T) {
		if got := acme.NewProblem(acme.ProblemBadCSR, "").Error(); got != "acme: badCSR" {
			t.Errorf("Error = %q, want %q", got, "acme: badCSR")
		}
		got := acme.NewProblem(acme.ProblemBadCSR, "key %d is wrong", 2).Error()
		if want := "acme: badCSR: key 2 is wrong"; got != want {
			t.Errorf("Error = %q, want %q", got, want)
		}
	})

	t.Run("Unwrap", func(t *testing.T) {
		if got := acme.NewProblem(acme.ProblemBadCSR, "").Unwrap(); got != nil {
			t.Errorf("Unwrap = %v, want nil", got)
		}
		p := acme.WrapProblem(acme.ProblemBadCSR, errStore, "no good")
		if got := p.Unwrap(); !errors.Is(got, errStore) {
			t.Errorf("Unwrap = %v, want the cause", got)
		}
	})

	t.Run("WithSubproblem", func(t *testing.T) {
		id := acme.Identifier{Type: acme.IdentifierPermanent, Value: testIdentifier}
		p := acme.NewProblem(acme.ProblemRejectedIdentifier, "already used").
			WithSubproblem(acme.ProblemRejectedIdentifier, "already used", id).
			WithSubproblem(acme.ProblemUnsupportedIdentifier, "and unsupported", id)
		if len(p.Subproblems) != 2 {
			t.Fatalf("subproblems = %+v, want two", p.Subproblems)
		}
		// The subproblem type is the full URN, unlike the outer type, which
		// the server renders as one on the way out.
		if want := acme.ProblemPrefix + acme.ProblemRejectedIdentifier; p.Subproblems[0].Type != want {
			t.Errorf("subproblem type = %q, want %q", p.Subproblems[0].Type, want)
		}
		if p.Subproblems[0].Identifier == nil || p.Subproblems[0].Identifier.Value != testIdentifier {
			t.Errorf("subproblem identifier = %+v, want %v", p.Subproblems[0].Identifier, id)
		}
	})
}
