package acme_test

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/acme/attest"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/acme/attest/attesttest"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/acme/jose"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/acme/acmetest"
)

// TestNewOrder covers RFC 8555 section 7.4 and Apple's anti-replay rule for
// the client identifier.
func TestNewOrder(t *testing.T) {
	t.Run("Created", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		res := acct.post(f.url("/new-order"), orderRequest(testIdentifier))
		requireStatus(t, res, http.StatusCreated)
		body := decode[orderJSON](t, res)
		if body.Status != acme.StatusPending {
			t.Errorf("status = %q, want pending", body.Status)
		}
		want := acme.Identifier{Type: acme.IdentifierPermanent, Value: testIdentifier}
		if len(body.Identifiers) != 1 || body.Identifiers[0] != want {
			t.Errorf("identifiers = %v, want %v", body.Identifiers, want)
		}
		if len(body.Authorizations) != 1 {
			t.Fatalf("authorizations = %v, want one", body.Authorizations)
		}
		location := res.header.Get("Location")
		if want := f.url("/order/" + idOf(location)); location != want {
			t.Errorf("Location = %q, want an order URL like %q", location, want)
		}
		if body.Finalize != location+"/finalize" {
			t.Errorf("finalize = %q, want %q", body.Finalize, location+"/finalize")
		}
		if body.Certificate != "" {
			t.Errorf("certificate = %q on a pending order", body.Certificate)
		}
		expires, err := time.Parse(time.RFC3339, body.Expires)
		if err != nil {
			t.Fatalf("expires %q: %v", body.Expires, err)
		}
		if got := expires.Sub(f.clock.Now()).Round(time.Minute); got != acme.DefaultOrderTTL {
			t.Errorf("expires in %v, want the default order lifetime %v", got, acme.DefaultOrderTTL)
		}
	})

	// IdentifierIsOneTime is Apple's anti-replay code taken at its word: the
	// ClientIdentifier buys exactly one certificate. Neither nanoca nor
	// step-ca consumes it, so on either of them this order would succeed.
	t.Run("IdentifierIsOneTime", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		requireStatus(t, acct.post(f.url("/new-order"), orderRequest(testIdentifier)), http.StatusCreated)

		second := acct.post(f.url("/new-order"), orderRequest(testIdentifier))
		p := requireProblem(t, second, acme.ProblemRejectedIdentifier)
		if len(p.Subproblems) != 1 || p.Subproblems[0].Identifier == nil {
			t.Fatalf("subproblems = %+v, want one naming the identifier", p.Subproblems)
		}
		if got := p.Subproblems[0].Identifier.Value; got != testIdentifier {
			t.Errorf("subproblem identifier = %q, want %q", got, testIdentifier)
		}

		// A different account does no better: the claim is on the
		// identifier, not on the account that took it.
		other := f.register()
		requireProblem(
			t, other.post(f.url("/new-order"), orderRequest(testIdentifier)),
			acme.ProblemRejectedIdentifier,
		)
	})

	// ConcurrentOrdersClaimOnce is the same rule under a race. The claim is
	// taken in the transaction that creates the order, so a read-then-write
	// cannot be the answer and exactly one caller may win.
	t.Run("ConcurrentOrdersClaimOnce", func(t *testing.T) {
		const racers = 8
		f := newFixture(t)
		acct := f.register()
		target := f.url("/new-order")
		payload := mustJSON(t, orderRequest(testIdentifier))

		// Signing happens here so that the goroutines do nothing but the
		// one request whose outcome is under test.
		bodies := make([][]byte, racers)
		for i := range bodies {
			body, err := jose.Sign(
				acct.key,
				jose.Header{KeyID: acct.url, URL: target, Nonce: f.nonce()},
				payload,
			)
			if err != nil {
				t.Fatal(err)
			}
			bodies[i] = body
		}

		statuses := make([]int, racers)
		var wg sync.WaitGroup
		for i := range racers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				req := f.request(http.MethodPost, target, bodies[i])
				req.Header.Set("Content-Type", acme.ContentTypeJOSE)
				res, err := f.ts.Client().Do(req)
				if err != nil {
					statuses[i] = -1
					return
				}
				defer func() { _ = res.Body.Close() }()
				_, _ = io.Copy(io.Discard, res.Body)
				statuses[i] = res.StatusCode
			}()
		}
		wg.Wait()

		created := 0
		for i, status := range statuses {
			switch status {
			case http.StatusCreated:
				created++
			case http.StatusBadRequest:
			default:
				t.Errorf("racer %d got status %d, want 201 or 400", i, status)
			}
		}
		if created != 1 {
			t.Fatalf("%d of %d orders were created, want exactly one", created, racers)
		}
	})

	t.Run("UnsupportedIdentifierType", func(t *testing.T) {
		// A dns identifier would produce an authorization whose only
		// challenge type nothing could satisfy, so it is refused at the
		// door rather than left to rot.
		f := newFixture(t)
		acct := f.register()
		res := acct.post(f.url("/new-order"), map[string]any{
			"identifiers": []acme.Identifier{{Type: "dns", Value: "device.example"}},
		})
		p := requireProblem(t, res, acme.ProblemUnsupportedIdentifier)
		if len(p.Subproblems) != 1 || p.Subproblems[0].Identifier == nil {
			t.Fatalf("subproblems = %+v, want one naming the identifier", p.Subproblems)
		}
		if got := p.Subproblems[0].Identifier.Type; got != "dns" {
			t.Errorf("subproblem identifier type = %q, want dns", got)
		}
	})

	t.Run("WrongShape", func(t *testing.T) {
		cases := map[string]any{
			"ZeroIdentifiers": map[string]any{"identifiers": []acme.Identifier{}},
			"TwoIdentifiers": map[string]any{"identifiers": []acme.Identifier{
				{Type: acme.IdentifierPermanent, Value: testIdentifier},
				{Type: acme.IdentifierPermanent, Value: "another"},
			}},
			"EmptyValue": map[string]any{"identifiers": []acme.Identifier{
				{Type: acme.IdentifierPermanent, Value: ""},
			}},
			// The validity of a device identity is the server's decision,
			// not something a device may ask for.
			"NotBefore": map[string]any{
				"identifiers": []acme.Identifier{{Type: acme.IdentifierPermanent, Value: testIdentifier}},
				"notBefore":   "2026-01-01T00:00:00Z",
			},
			"NotAfter": map[string]any{
				"identifiers": []acme.Identifier{{Type: acme.IdentifierPermanent, Value: testIdentifier}},
				"notAfter":    "2036-01-01T00:00:00Z",
			},
		}
		for name, payload := range cases {
			t.Run(name, func(t *testing.T) {
				f := newFixture(t)
				acct := f.register()
				requireProblem(
					t, acct.post(f.url("/new-order"), payload), acme.ProblemMalformed,
				)
			})
		}
	})

	t.Run("UnknownIdentifier", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		res := acct.post(f.url("/new-order"), orderRequest("never-minted-by-this-server"))
		requireProblem(t, res, acme.ProblemRejectedIdentifier)
	})

	t.Run("PostAsGetIsRefused", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		requireProblem(t, acct.post(f.url("/new-order"), nil), acme.ProblemMalformed)
	})

	t.Run("PayloadIsNotAnOrder", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		res := f.signed(
			f.url("/new-order"), acct.key, jose.Header{KeyID: acct.url}, []byte("not json"),
		)
		requireProblem(t, res, acme.ProblemMalformed)
	})

	t.Run("StoreFailure", func(t *testing.T) {
		f := newFixture(t, func(c *acme.Config) {
			c.Store = &acmetest.Failing{Store: c.Store, Fail: map[string]error{"PutOrder": errStore}}
		})
		acct := f.register()
		res := acct.post(f.url("/new-order"), orderRequest(testIdentifier))
		requireProblem(t, res, acme.ProblemServerInternal)
	})
}

// TestOrderEndpoint is the POST-as-GET on an order.
func TestOrderEndpoint(t *testing.T) {
	f := newFixture(t)
	fl := f.begin(testIdentifier)

	t.Run("PostAsGet", func(t *testing.T) {
		if got := fl.order().Status; got != acme.StatusPending {
			t.Fatalf("status = %q, want pending", got)
		}
	})

	t.Run("AnotherAccountIsUnauthorized", func(t *testing.T) {
		other := f.register()
		requireProblem(t, other.post(fl.orderURL, nil), acme.ProblemUnauthorized)
		requireProblem(t, other.post(fl.authzURL, nil), acme.ProblemUnauthorized)
		requireProblem(t, other.post(fl.chalURL, nil), acme.ProblemUnauthorized)
	})

	t.Run("NothingOfTheSort", func(t *testing.T) {
		for _, target := range []string{
			f.url("/order/nothing"),
			f.url("/authz/nothing"),
			f.url("/challenge/nothing"),
			f.url("/order/nothing/finalize"),
		} {
			requireProblem(t, fl.acct.post(target, nil), acme.ProblemMalformed)
		}
	})

	t.Run("StoreFailures", func(t *testing.T) {
		cases := map[string]struct {
			path string
			fail map[string]error
		}{
			"Order":         {"/order/" + idOf(fl.orderURL), map[string]error{"GetOrder": errStore}},
			"Authorization": {"/authz/" + idOf(fl.authzURL), map[string]error{"GetAuthorization": errStore}},
			"Challenge":     {"/challenge/" + idOf(fl.chalURL), map[string]error{"GetChallenge": errStore}},
			// The authorization is found but the challenge it names cannot
			// be read, which is our fault and not a missing record.
			"ChallengeOfAuthorization": {
				"/authz/" + idOf(fl.authzURL), map[string]error{"GetChallenge": errStore},
			},
		}
		for name, c := range cases {
			t.Run(name, func(t *testing.T) {
				broken := newFixtureSharing(t, f, c.fail)
				res := broken.signed(
					broken.url(c.path), fl.acct.key,
					jose.Header{KeyID: broken.url("/account/" + fl.acct.id)}, nil,
				)
				requireProblem(t, res, acme.ProblemServerInternal)
			})
		}
	})

	t.Run("ExpiredAuthorizationReportsExpired", func(t *testing.T) {
		// Expiry is reported without waiting for a sweeper to have run, so
		// a client reading a stale authorization is told the truth.
		g := newFixture(t)
		other := g.begin(testIdentifier)
		g.clock.Advance(acme.DefaultOrderTTL + time.Minute)
		if got := other.authorization().Status; got != acme.StatusExpired {
			t.Fatalf("status = %q, want expired", got)
		}
	})
}

// TestFinalize covers RFC 8555 section 7.4: what the server does with a
// certificate request.
func TestFinalize(t *testing.T) {
	t.Run("Issues", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier).pass()
		res := fl.finalizeWith(fl.key, pkix.Name{CommonName: "a name the device chose"})
		requireStatus(t, res, http.StatusOK)
		body := decode[orderJSON](t, res)
		if body.Status != acme.StatusValid {
			t.Fatalf("status = %q, want valid", body.Status)
		}
		if body.Certificate == "" {
			t.Fatal("the order names no certificate")
		}
		if got := res.header.Get("Location"); got != fl.orderURL {
			t.Errorf("Location = %q, want the order %q", got, fl.orderURL)
		}
		if f.events.count(event.ACMEIssued) != 1 {
			t.Errorf("published %d issuance events, want 1", f.events.count(event.ACMEIssued))
		}
	})

	// KeyMustMatchAttestation is the binding Apple's guidance asks for and
	// the one the reference implementations miss: nanoca and Fleet never
	// compare the attested key with the certificate request, and step-ca
	// skips the comparison when the authorization carries no fingerprint.
	t.Run("KeyMustMatchAttestation", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier).pass()
		res := fl.finalizeWith(newKey(t), pkix.Name{})
		p := requireProblem(t, res, acme.ProblemBadCSR)
		if !strings.Contains(p.Detail, "attested") {
			t.Errorf("detail = %q, want it to name the attested key", p.Detail)
		}
		// A refused certificate request is the client's mistake, so the
		// order stays ready and the right key can still be presented.
		if got := fl.order().Status; got != acme.StatusReady {
			t.Fatalf("order status = %q, want ready", got)
		}
		requireStatus(t, fl.finalizeWith(fl.key, pkix.Name{}), http.StatusOK)
	})

	// BadCSRLeavesTheOrderReady is RFC 8555 section 7.4: a rejected
	// certificate request does not spoil the order, so an amended one can
	// still finalize it.
	t.Run("BadCSRLeavesTheOrderReady", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier).pass()

		malformed := map[string]string{
			"NoCSR":     "",
			"NotBase64": "!!! not base64url !!!",
			"NotACSR":   base64.RawURLEncoding.EncodeToString([]byte("nothing like DER")),
			"BadSignature": base64.RawURLEncoding.EncodeToString(
				tamper(csrDER(t, fl.key, pkix.Name{})),
			),
		}
		for name, csr := range malformed {
			t.Run(name, func(t *testing.T) {
				res := fl.acct.post(fl.finalize, map[string]string{"csr": csr})
				requireProblem(t, res, acme.ProblemBadCSR)
				if got := fl.order().Status; got != acme.StatusReady {
					t.Fatalf("order status = %q after a bad CSR, want ready", got)
				}
			})
		}
		requireStatus(t, fl.finalizeWith(fl.key, pkix.Name{}), http.StatusOK)
	})

	t.Run("OrderIsNotReady", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier)
		requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemOrderNotReady)
	})

	t.Run("OrderIsInvalid", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier)
		setOrderStatus(t, f, fl, acme.StatusInvalid)
		requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemOrderNotReady)
	})

	t.Run("OrderIsProcessing", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier)
		setOrderStatus(t, f, fl, acme.StatusProcessing)
		p := requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemOrderNotReady)
		if !strings.Contains(p.Detail, acme.StatusProcessing) {
			t.Errorf("detail = %q, want it to name the status", p.Detail)
		}
	})

	t.Run("AlreadyValidDoesNotIssueTwice", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier).pass()
		first := decode[orderJSON](t, fl.finalizeWith(fl.key, pkix.Name{}))
		second := fl.finalizeWith(fl.key, pkix.Name{})
		requireStatus(t, second, http.StatusOK)
		body := decode[orderJSON](t, second)
		if body.Certificate != first.Certificate {
			t.Fatalf("certificate = %q, want the one already issued %q", body.Certificate, first.Certificate)
		}
		if n := f.events.count(event.ACMEIssued); n != 1 {
			t.Errorf("published %d issuance events, want 1", n)
		}
	})

	t.Run("Expired", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier).pass()
		f.clock.Advance(acme.DefaultOrderTTL + time.Minute)
		p := requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemOrderNotReady)
		if !strings.Contains(p.Detail, "expired") {
			t.Errorf("detail = %q, want it to say the order expired", p.Detail)
		}
	})

	t.Run("AnotherAccountsOrder", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier).pass()
		other := f.register()
		res := other.post(fl.finalize, map[string]string{
			"csr": base64.RawURLEncoding.EncodeToString(csrDER(t, fl.key, pkix.Name{})),
		})
		requireProblem(t, res, acme.ProblemUnauthorized)
	})

	t.Run("PostAsGetIsRefused", func(t *testing.T) {
		f := newFixture(t)
		fl := f.begin(testIdentifier).pass()
		requireProblem(t, fl.acct.post(fl.finalize, nil), acme.ProblemMalformed)
	})

	t.Run("SignerRefusesTheKey", func(t *testing.T) {
		// A CA policy that names the key kinds it will certify is the
		// deployment's, so the refusal is the client's fault and the order
		// stays ready for a device that offers the right key.
		f := newFixture(t, func(c *acme.Config) {
			c.CAPolicy.AllowedKeys = []ca.KeyKind{ca.KeyECP384}
		})
		fl := f.begin(testIdentifier).pass()
		requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemBadCSR)
		if got := fl.order().Status; got != acme.StatusReady {
			t.Fatalf("order status = %q, want ready", got)
		}
	})

	t.Run("SignerFails", func(t *testing.T) {
		// A signer that breaks is our fault, and a terminal one at that:
		// the order is settled invalid rather than left for a retry that
		// would fail the same way.
		f := newFixture(t, func(c *acme.Config) { c.Signer = brokenSigner{c.Signer} })
		fl := f.begin(testIdentifier).pass()
		requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemServerInternal)
		if got := fl.order().Status; got != acme.StatusReady {
			t.Fatalf("order status = %q, want ready: a server fault must not spoil the order", got)
		}
	})

	t.Run("AttestationNoLongerVerifies", func(t *testing.T) {
		// The stored attestation is verified again at finalize, from its
		// original bytes, because the key being certified was not known
		// when the challenge was answered.
		f := newFixture(t, func(c *acme.Config) { c.OrderTTL = 30 * 24 * time.Hour })
		fl := f.begin(testIdentifier)
		props := deviceProperties()
		props.Freshness = attest.FreshnessForToken(fl.token)
		object, err := f.attest.Object(attesttest.LeafOptions{
			Properties: props,
			PublicKey:  fl.key.Public(),
			NotBefore:  time.Now().Add(-time.Minute),
			NotAfter:   time.Now().Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		requireStatus(t, fl.answer(object), http.StatusOK)
		f.clock.Advance(2 * time.Hour)
		requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemBadAttestationStatement)
		if got := fl.order().Status; got != acme.StatusInvalid {
			t.Fatalf("order status = %q, want invalid", got)
		}
	})

	t.Run("SettlingTheOrderFails", func(t *testing.T) {
		// A terminal fault settles the order invalid, and a store that
		// cannot record that is our fault rather than the attestation's, so
		// the client is told to retry rather than that its device is wrong.
		f := newFixture(t, func(c *acme.Config) { c.OrderTTL = 30 * 24 * time.Hour })
		fl := f.begin(testIdentifier)
		props := deviceProperties()
		props.Freshness = attest.FreshnessForToken(fl.token)
		object, err := f.attest.Object(attesttest.LeafOptions{
			Properties: props,
			PublicKey:  fl.key.Public(),
			NotBefore:  time.Now().Add(-time.Minute),
			NotAfter:   time.Now().Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		requireStatus(t, fl.answer(object), http.StatusOK)
		f.clock.Advance(2 * time.Hour)

		broken := newFixtureSharing(t, f, map[string]error{"PutOrder": errStore})
		res := broken.signed(
			broken.url("/order/"+idOf(fl.orderURL)+"/finalize"), fl.acct.key,
			jose.Header{KeyID: broken.url("/account/" + fl.acct.id)},
			mustJSON(t, map[string]string{
				"csr": base64.RawURLEncoding.EncodeToString(csrDER(t, fl.key, pkix.Name{})),
			}),
		)
		requireProblem(t, res, acme.ProblemServerInternal)
	})

	t.Run("StoredAttestationIsUnreadable", func(t *testing.T) {
		// A record the server wrote itself cannot be corrupt, so this is
		// reported as our fault rather than the device's.
		f := newFixture(t)
		fl := f.begin(testIdentifier).pass()
		record := challengeRecord(t, f, fl)
		record.Attestation = []byte("not an attestation object")
		putChallenge(t, f, record)
		requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemServerInternal)
	})

	t.Run("AuthorizedWithoutAnAttestation", func(t *testing.T) {
		// A challenge that carries no attestation at all can only come from
		// a server that was configured to allow one, so finalizing it on a
		// server that requires one is refused.
		f := newFixture(t)
		fl := f.begin(testIdentifier).pass()
		record := challengeRecord(t, f, fl)
		record.Attestation = nil
		putChallenge(t, f, record)
		requireProblem(t, fl.finalizeWith(fl.key, pkix.Name{}), acme.ProblemUnauthorized)
	})

	t.Run("StoreFailures", func(t *testing.T) {
		for name, fail := range map[string]map[string]error{
			"Authorization": {"GetAuthorization": errStore},
			"Challenge":     {"GetChallenge": errStore},
			"Certificate":   {"PutCertificate": errStore},
			"Order":         {"PutOrder": errStore},
		} {
			t.Run(name, func(t *testing.T) {
				f := newFixture(t)
				fl := f.begin(testIdentifier).pass()
				broken := newFixtureSharing(t, f, fail)
				res := f.signed(
					broken.url("/order/"+idOf(fl.orderURL)+"/finalize"), fl.acct.key,
					jose.Header{KeyID: broken.url("/account/" + fl.acct.id)},
					mustJSON(t, map[string]string{
						"csr": base64.RawURLEncoding.EncodeToString(csrDER(t, fl.key, pkix.Name{})),
					}),
				)
				requireProblem(t, res, acme.ProblemServerInternal)
			})
		}
	})
}

// tamper flips the last byte, which leaves the structure intact and the
// signature wrong.
func tamper(der []byte) []byte {
	out := append([]byte(nil), der...)
	out[len(out)-1] ^= 0xff
	return out
}

// brokenSigner is a certificate authority that fails for a reason that is
// neither a policy refusal nor a bad certificate request.
type brokenSigner struct{ ca.Signer }

func (brokenSigner) Sign(
	context.Context,
	*x509.CertificateRequest,
	ca.Policy,
) (*x509.Certificate, error) {
	return nil, errStore
}

// setOrderStatus writes a status the protocol would not reach on its own.
func setOrderStatus(t *testing.T, f *fixture, fl *flow, status string) {
	t.Helper()
	order, err := f.store.GetOrder(t.Context(), idOf(fl.orderURL))
	if err != nil {
		t.Fatal(err)
	}
	order.Status = status
	if err := f.store.Update(t.Context(), func(tx acme.Tx) error {
		return tx.PutOrder(t.Context(), order)
	}); err != nil {
		t.Fatal(err)
	}
}

func challengeRecord(t *testing.T, f *fixture, fl *flow) *acme.Challenge {
	t.Helper()
	record, err := f.store.GetChallenge(t.Context(), idOf(fl.chalURL))
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func putChallenge(t *testing.T, f *fixture, c *acme.Challenge) {
	t.Helper()
	if err := f.store.Update(t.Context(), func(tx acme.Tx) error {
		return tx.PutChallenge(t.Context(), c)
	}); err != nil {
		t.Fatal(err)
	}
}
