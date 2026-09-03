package acme_test

import (
	"context"
	"crypto/x509/pkix"
	"encoding/base64"

	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/acme"
	"github.com/deploymenttheory/go-apple-dm/acme/attest"
	"github.com/deploymenttheory/go-apple-dm/acme/attest/attesttest"
	"github.com/deploymenttheory/go-apple-dm/acme/jose"
	"github.com/deploymenttheory/go-apple-dm/event"
)

// TestChallenge covers the device-attest-01 challenge: the whole of the
// trust decision, and what happens to the order when it goes wrong.
func TestChallenge(t *testing.T) {
	t.Run("Succeeds", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier)
		res := fl.answer(fl.attestation(deviceProperties()))
		requireStatus(t, res, http.StatusOK)
		body := decode[challengeJSON](t, res)
		if body.Status != acme.StatusValid {
			t.Fatalf("challenge status = %q, want valid", body.Status)
		}
		if body.Type != acme.ChallengeDeviceAttest {
			t.Errorf("challenge type = %q, want %q", body.Type, acme.ChallengeDeviceAttest)
		}
		if body.Validated == "" {
			t.Error("a valid challenge carries no validated time")
		}
		if got := fl.authorization().Status; got != acme.StatusValid {
			t.Errorf("authorization status = %q, want valid", got)
		}
		if got := fl.order().Status; got != acme.StatusReady {
			t.Errorf("order status = %q, want ready", got)
		}
		if n := f.events.count(event.ACMEChallengeValid); n != 1 {
			t.Errorf("published %d challenge events, want 1", n)
		}
	})

	// PolicyFailureLeavesTheChallengePending is the distinction between a
	// refusal and a fault. A directory that was briefly unreachable must
	// not reject a device for good, so the challenge stays pending and the
	// retry that follows succeeds. step-ca has no policy seam at all, so
	// every refusal there is terminal whether or not it was the device's
	// fault.
	t.Run("PolicyFailureLeavesTheChallengePending", func(t *testing.T) {
		var calls atomic.Int32
		f := newFixture(t, func(c *acme.Config) {
			c.Authorize = acme.PolicyFunc(func(context.Context, *acme.Decision) error {
				if calls.Add(1) == 1 {
					return acme.WrapProblem(
						acme.ProblemServerInternal, errStore, "the device could not be looked up",
					)
				}
				return nil
			})
		})
		fl := f.begin(testIdentifier)
		object := fl.attestation(deviceProperties())

		first := fl.answer(object)
		if first.status != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500; body %s", first.status, first.body)
		}
		requireProblem(t, first, acme.ProblemServerInternal)
		if got := fl.challenge().Status; got != acme.StatusPending {
			t.Fatalf("challenge status = %q after a server fault, want pending", got)
		}
		if got := fl.order().Status; got != acme.StatusPending {
			t.Fatalf("order status = %q after a server fault, want pending", got)
		}

		requireStatus(t, fl.answer(object), http.StatusOK)
		if got := fl.challenge().Status; got != acme.StatusValid {
			t.Fatalf("challenge status = %q on the retry, want valid", got)
		}
	})

	// PolicyRefusalSettlesInvalid is the other half: a device the policy
	// turned away would be turned away again, so the challenge, its
	// authorization and its order are all settled in one transaction.
	t.Run("PolicyRefusalSettlesInvalid", func(t *testing.T) {
		f := newFixture(t, func(c *acme.Config) {
			c.Authorize = acme.AllowSerials("SOME-OTHER-MAC")
		})
		fl := f.begin(testIdentifier)
		res := fl.answer(fl.attestation(deviceProperties()))
		requireProblem(t, res, acme.ProblemUnauthorized)

		challenge := fl.challenge()
		if challenge.Status != acme.StatusInvalid {
			t.Errorf("challenge status = %q, want invalid", challenge.Status)
		}
		if challenge.Error == nil {
			t.Error("the settled challenge carries no error")
		}
		if got := fl.authorization().Status; got != acme.StatusInvalid {
			t.Errorf("authorization status = %q, want invalid", got)
		}
		order := fl.order()
		if order.Status != acme.StatusInvalid {
			t.Errorf("order status = %q, want invalid", order.Status)
		}
		if order.Error == nil {
			t.Error("the settled order carries no error")
		}
		if n := f.events.count(event.AttestationRejected); n != 1 {
			t.Errorf("published %d rejection events, want 1", n)
		}
	})

	// BindingMismatchRejected is the second factor. Apple warns the client
	// identifier is "a relatively weak indication" because it can be
	// intercepted, so the identifier says which device the server expects
	// and the attestation says which turned up: both must agree. step-ca
	// instead requires the ordered identifier to equal the device's serial
	// number, which is printed on the case and in every inventory.
	t.Run("BindingMismatchRejected", func(t *testing.T) {
		cases := map[string]attest.Properties{
			"Serial": {SerialNumber: "C02NOTTHISONE", UDID: testUDID},
			"UDID":   {SerialNumber: testSerial, UDID: "00008030-DEADBEEFDEADBEEF"},
		}
		for name, props := range cases {
			t.Run(name, func(t *testing.T) {
				f := newFixture(t)
				fl := f.begin(testIdentifier)
				res := fl.answer(fl.attestation(props))
				p := requireProblem(t, res, acme.ProblemBadAttestationStatement)
				if len(p.Subproblems) != 1 || p.Subproblems[0].Identifier == nil {
					t.Fatalf("subproblems = %+v, want one naming the identifier", p.Subproblems)
				}
				if got := p.Subproblems[0].Identifier.Value; got != testIdentifier {
					t.Errorf("subproblem identifier = %q, want %q", got, testIdentifier)
				}
				if want := acme.ProblemPrefix + acme.ProblemRejectedIdentifier; p.Subproblems[0].Type != want {
					t.Errorf("subproblem type = %q, want %q", p.Subproblems[0].Type, want)
				}
				if got := fl.order().Status; got != acme.StatusInvalid {
					t.Errorf("order status = %q, want invalid", got)
				}
			})
		}
	})

	t.Run("UnidentifiedAttestationNeedsOptIn", func(t *testing.T) {
		// A user enrollment attests to genuine hardware without naming it.
		// Accepting one has to be deliberate, because there is then nothing
		// tying the certificate to a particular device.
		const identifier = "an-identifier-for-a-user-enrollment"
		refused := newFixture(t)
		refused.ids[identifier] = acme.Binding{CommonName: "A User Enrollment"}
		fl := refused.begin(identifier)
		res := fl.answer(fl.attestation(attest.Properties{OSVersion: "26.0"}))
		p := requireProblem(t, res, acme.ProblemBadAttestationStatement)
		if !strings.Contains(p.Detail, "names no device") {
			t.Errorf("detail = %q, want it to say the attestation names no device", p.Detail)
		}

		allowed := newFixture(t)
		allowed.ids[identifier] = acme.Binding{
			CommonName: "A User Enrollment", AllowUnidentified: true,
		}
		fl = allowed.begin(identifier)
		requireStatus(t, fl.answer(fl.attestation(attest.Properties{OSVersion: "26.0"})), http.StatusOK)
	})

	// NoAttestationRequiresOptIn: Apple sends a statement with no chain
	// when the profile did not ask for an attestation or the hardware
	// cannot produce one. Whether that is acceptable is a deployment's
	// decision, and the safe default is to refuse: Apple hardware that
	// cannot attest is old enough to be worth knowing about.
	t.Run("NoAttestationRequiresOptIn", func(t *testing.T) {
		empty, err := attesttest.Object(nil)
		if err != nil {
			t.Fatal(err)
		}

		t.Run("RefusedByDefault", func(t *testing.T) {
			f := newFixture(t)
			fl := f.begin(testIdentifier)
			p := requireProblem(t, fl.answer(empty), acme.ProblemBadAttestationStatement)
			if !strings.Contains(p.Detail, "requires an attestation") {
				t.Errorf("detail = %q, want it to say an attestation is required", p.Detail)
			}
		})

		t.Run("AcceptedWhenAllowed", func(t *testing.T) {
			f := newFixture(t, func(c *acme.Config) { c.AllowUnattested = true })
			fl := f.begin(testIdentifier)
			requireStatus(t, fl.answer(empty), http.StatusOK)
			if got := fl.order().Status; got != acme.StatusReady {
				t.Fatalf("order status = %q, want ready", got)
			}
		})

		t.Run("FinalizeAlsoWorks", func(t *testing.T) {
			// The challenge handler stores the attestation object exactly as
			// it arrived, and a well formed statement carrying no chain is
			// not empty, so finalize has to read attest.ErrNoAttestation as
			// the case the challenge deliberately accepted rather than as a
			// corrupt record. Nothing attested to a key here, so nothing
			// binds the certificate request's key: that is the cost of the
			// setting, and it is why the default is to refuse.
			f := newFixture(t, func(c *acme.Config) { c.AllowUnattested = true })
			fl := f.begin(testIdentifier)
			requireStatus(t, fl.answer(empty), http.StatusOK)
			requireStatus(t, fl.finalizeWith(fl.key, pkix.Name{}), http.StatusOK)
			if got := fl.order().Status; got != acme.StatusValid {
				t.Fatalf("order status = %q, want valid", got)
			}
		})
	})

	t.Run("ForeignAuthority", func(t *testing.T) {
		// An attestation from an authority this server does not trust is
		// refused before anything is read out of its leaf.
		f := newFixture(t)
		foreign, err := attesttest.NewCA()
		if err != nil {
			t.Fatal(err)
		}
		fl := f.begin(testIdentifier)
		object, err := foreign.ObjectForToken(fl.token, deviceProperties(), fl.key.Public())
		if err != nil {
			t.Fatal(err)
		}
		requireProblem(t, fl.answer(object), acme.ProblemBadAttestationStatement)
		if n := f.events.count(event.AttestationRejected); n != 1 {
			t.Errorf("published %d rejection events, want 1", n)
		}
	})

	t.Run("StaleFreshness", func(t *testing.T) {
		// The freshness code is the SHA-256 of this challenge's token, so an
		// attestation minted for an earlier order cannot be replayed.
		f := newFixture(t)
		fl := f.begin(testIdentifier)
		object, err := f.attest.ObjectForToken(
			fl.token+"-from-an-earlier-order", deviceProperties(), fl.key.Public(),
		)
		if err != nil {
			t.Fatal(err)
		}
		requireProblem(t, fl.answer(object), acme.ProblemBadAttestationStatement)
	})

	t.Run("MalformedResponses", func(t *testing.T) {
		cases := map[string]any{
			"NoAttObj":         map[string]string{},
			"NotBase64":        map[string]string{"attObj": "!!! not base64url !!!"},
			"NotAnObject":      []string{"not an object at all"},
			"PayloadIsNotJSON": nil,
		}
		for name, payload := range cases {
			t.Run(name, func(t *testing.T) {
				f := newFixture(t)
				fl := f.begin(testIdentifier)
				var res *response
				if payload == nil {
					res = f.signed(
						fl.chalURL, fl.acct.key,
						jose.Header{KeyID: fl.acct.url}, []byte("not json at all"),
					)
				} else {
					res = fl.acct.post(fl.chalURL, payload)
				}
				requireProblem(t, res, acme.ProblemMalformed)
				// A malformed response is the client's fault, so the
				// challenge is settled rather than left open.
				if got := fl.challenge().Status; got != acme.StatusInvalid {
					t.Errorf("challenge status = %q, want invalid", got)
				}
			})
		}
	})

	t.Run("NotAnAttestationObject", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier)
		requireProblem(
			t, fl.answer([]byte("this is not CBOR")), acme.ProblemBadAttestationStatement,
		)
	})

	t.Run("AnsweredTwiceReturnsTheCurrentState", func(t *testing.T) {
		// A repeated post reports where things stand rather than validating
		// again, which is the idempotency nanoca buys with a lease.
		f := newFixture(t)
		fl := f.begin(testIdentifier)
		object := fl.attestation(deviceProperties())
		first := decode[challengeJSON](t, fl.answer(object))
		second := fl.answer(object)
		requireStatus(t, second, http.StatusOK)
		body := decode[challengeJSON](t, second)
		if body.Status != acme.StatusValid || body.Validated != first.Validated {
			t.Fatalf("second answer = %+v, want the first one back %+v", body, first)
		}
		if n := f.events.count(event.ACMEChallengeValid); n != 1 {
			t.Errorf("published %d challenge events, want 1: it revalidated", n)
		}
	})

	t.Run("PostAsGetReportsState", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier)
		if got := fl.challenge().Status; got != acme.StatusPending {
			t.Fatalf("status = %q, want pending", got)
		}
	})

	t.Run("AuthorizationExpired", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier)
		object := fl.attestation(deviceProperties())
		f.clock.Advance(acme.DefaultOrderTTL + time.Minute)
		p := requireProblem(t, fl.answer(object), acme.ProblemMalformed)
		if !strings.Contains(p.Detail, "expired") {
			t.Errorf("detail = %q, want it to say the authorization expired", p.Detail)
		}
	})

	t.Run("StoreFailures", func(t *testing.T) {
		cases := map[string]map[string]error{
			"Authorization": {"GetAuthorization": errStore},
			"Order":         {"GetOrder": errStore},
			"Settle":        {"PutChallenge": errStore},
		}
		for name, fail := range cases {
			t.Run(name, func(t *testing.T) {
				f := newFixture(t)
				fl := f.begin(testIdentifier)
				object := fl.attestation(deviceProperties())
				broken := newFixtureSharing(t, f, fail)
				res := broken.signed(
					broken.url("/challenge/"+idOf(fl.chalURL)), fl.acct.key,
					jose.Header{KeyID: broken.url("/account/" + fl.acct.id)},
					mustJSON(t, map[string]string{
						"attObj": base64.RawURLEncoding.EncodeToString(object),
					}),
				)
				requireProblem(t, res, acme.ProblemServerInternal)
			})
		}
	})

	t.Run("SettleFailure", func(t *testing.T) {
		// A challenge that cannot be settled is reported as our fault, not
		// as the refusal that caused it.
		f := newFixture(t)
		fl := f.begin(testIdentifier)
		object := fl.attestation(attest.Properties{SerialNumber: "C02NOTTHISONE"})
		broken := newFixtureSharing(t, f, map[string]error{"PutAuthorization": errStore})
		res := broken.signed(
			broken.url("/challenge/"+idOf(fl.chalURL)), fl.acct.key,
			jose.Header{KeyID: broken.url("/account/" + fl.acct.id)},
			mustJSON(t, map[string]string{
				"attObj": base64.RawURLEncoding.EncodeToString(object),
			}),
		)
		requireProblem(t, res, acme.ProblemServerInternal)
	})
}

// TestDecisionProperties: a policy that runs where no attestation was
// produced still gets a usable value rather than a nil dereference.
func TestDecisionProperties(t *testing.T) {
	var got attest.Properties
	seen := false
	f := newFixture(t, func(c *acme.Config) {
		c.AllowUnattested = true
		c.Authorize = acme.PolicyFunc(func(_ context.Context, d *acme.Decision) error {
			seen, got = true, d.Properties()
			if d.Attestation != nil {
				t.Error("a decision without an attestation carried one")
			}
			if d.Account == nil || d.Order == nil {
				t.Error("the decision is missing the account or the order")
			}
			if d.Identifier.Value != testIdentifier {
				t.Errorf("decision identifier = %q, want %q", d.Identifier.Value, testIdentifier)
			}
			if d.Binding.Serial != testSerial {
				t.Errorf("decision binding serial = %q, want %q", d.Binding.Serial, testSerial)
			}
			return nil
		})
	})
	empty, err := attesttest.Object(nil)
	if err != nil {
		t.Fatal(err)
	}
	fl := f.begin(testIdentifier)
	requireStatus(t, fl.answer(empty), http.StatusOK)
	if !seen {
		t.Fatal("the policy never ran")
	}
	if got.Identified() || got.SIPEnabled != nil {
		t.Fatalf("properties = %+v, want the zero value", got)
	}
}

// TestEventsAreOptional: publishing is a side effect, not what the request
// is for, so a deployment with no bus and one whose bus has stopped
// accepting events both still issue.
func TestEventsAreOptional(t *testing.T) {
	t.Run("NoBus", func(t *testing.T) {
		f := newFixture(t, func(c *acme.Config) { c.Bus = nil })
		fl := f.begin(testIdentifier).pass()
		requireStatus(t, fl.finalizeWith(fl.key, pkix.Name{}), http.StatusOK)
	})

	t.Run("ClosedBus", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier)
		if err := f.events.bus.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		requireStatus(t, fl.answer(fl.attestation(deviceProperties())), http.StatusOK)
		requireStatus(t, fl.finalizeWith(fl.key, pkix.Name{}), http.StatusOK)
	})
}
