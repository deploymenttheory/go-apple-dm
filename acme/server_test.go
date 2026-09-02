package acme_test

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/acme"
	"github.com/deploymenttheory/go-apple-mdm/acme/acmetest"
	"github.com/deploymenttheory/go-apple-mdm/acme/inmem"
	"github.com/deploymenttheory/go-apple-mdm/acme/jose"
	"github.com/deploymenttheory/go-apple-mdm/ca"
)

// TestDirectory proves the directory names exactly the endpoints that
// answer. An advertised endpoint that returns nothing is one whose faults
// are never found, so revokeCert and keyChange are absent: Apple's client
// never revokes a certificate or rolls an account key.
func TestDirectory(t *testing.T) {
	f := newFixture(t)
	res := f.get(f.url("/directory"))
	requireStatus(t, res, http.StatusOK)
	if ct := res.header.Get("Content-Type"); ct != acme.ContentTypeJSON {
		t.Errorf("content type = %q, want %q", ct, acme.ContentTypeJSON)
	}

	// The member set is asserted from the raw document rather than from a
	// struct, so a member the server started sending would be noticed.
	members := decode[map[string]any](t, res)
	want := map[string]bool{"newNonce": true, "newAccount": true, "newOrder": true, "meta": true}
	for name := range members {
		if !want[name] {
			t.Errorf("the directory advertises %q, which is not implemented", name)
		}
	}
	for name := range want {
		if _, ok := members[name]; !ok {
			t.Errorf("the directory is missing %q", name)
		}
	}
	for _, absent := range []string{"revokeCert", "keyChange", "newAuthz"} {
		if _, ok := members[absent]; ok {
			t.Errorf("the directory advertises %q, which this server does not answer", absent)
		}
	}

	dir := decode[directoryJSON](t, res)
	if got, want := dir.NewNonce, f.url("/new-nonce"); got != want {
		t.Errorf("newNonce = %q, want %q", got, want)
	}
	if got, want := dir.NewAccount, f.url("/new-account"); got != want {
		t.Errorf("newAccount = %q, want %q", got, want)
	}
	if got, want := dir.NewOrder, f.url("/new-order"); got != want {
		t.Errorf("newOrder = %q, want %q", got, want)
	}
	if got := f.server.DirectoryURL(); got != f.url("/directory") {
		t.Errorf("DirectoryURL = %q, want %q", got, f.url("/directory"))
	}

	// Every advertised URL answers. RFC 8555 section 7.2 wants 200 for a
	// HEAD on new-nonce and 204 for a GET, both carrying a nonce.
	if got := f.head(dir.NewNonce); got.status != http.StatusOK {
		t.Errorf("HEAD new-nonce = %d, want 200", got.status)
	}
	nonceRes := f.get(dir.NewNonce)
	if nonceRes.status != http.StatusNoContent {
		t.Errorf("GET new-nonce = %d, want 204", nonceRes.status)
	}
	if nonceRes.header.Get("Replay-Nonce") == "" {
		t.Error("new-nonce carried no Replay-Nonce")
	}
	if cc := nonceRes.header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", cc)
	}
	if link := nonceRes.header.Get("Link"); !strings.Contains(link, `rel="index"`) {
		t.Errorf("Link = %q, want an index link", link)
	}
	acct := f.register()
	requireStatus(t, acct.post(dir.NewOrder, orderRequest(testIdentifier)), http.StatusCreated)
}

// TestUnknownEndpointIsAProblem: anything else under the prefix is answered
// as an ACME problem rather than as the multiplexer's HTML, so a client
// parsing the response finds what it expects.
func TestUnknownEndpointIsAProblem(t *testing.T) {
	f := newFixture(t)
	res := f.get(f.url("/no-such-endpoint"))
	requireProblem(t, res, acme.ProblemMalformed)
}

// TestNew checks the configuration a server refuses to start with.
func TestNew(t *testing.T) {
	root, err := sharedCA()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ca.NewLocal(root.cert, root.key)
	if err != nil {
		t.Fatal(err)
	}
	valid := func() acme.Config {
		return acme.Config{
			BaseURL:     "https://mdm.example",
			Store:       inmem.New(),
			Signer:      signer,
			Identifiers: acme.StaticIdentifiers{},
		}
	}

	cases := map[string]func(*acme.Config){
		"NoBaseURL":     func(c *acme.Config) { c.BaseURL = "" },
		"NoStore":       func(c *acme.Config) { c.Store = nil },
		"NoSigner":      func(c *acme.Config) { c.Signer = nil },
		"NoIdentifiers": func(c *acme.Config) { c.Identifiers = nil },
		"NoScheme":      func(c *acme.Config) { c.BaseURL = "mdm.example" },
	}
	for name, break_ := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := valid()
			break_(&cfg)
			srv, err := acme.New(cfg)
			if !errors.Is(err, acme.ErrConfig) {
				t.Fatalf("error = %v, want ErrConfig", err)
			}
			if srv != nil {
				t.Error("a server was returned alongside the error")
			}
		})
	}

	t.Run("Defaults", func(t *testing.T) {
		cfg := valid()
		// A trailing slash on the base and an unslashed prefix both have to
		// normalise, because every URL the server publishes is built from
		// them and the url header of every request is compared with one.
		cfg.BaseURL = "https://mdm.example/"
		cfg.Prefix = "identity/acme/"
		srv, err := acme.New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := srv.DirectoryURL(), "https://mdm.example/identity/acme/directory"; got != want {
			t.Fatalf("DirectoryURL = %q, want %q", got, want)
		}
	})

	t.Run("HTTPBaseIsAccepted", func(t *testing.T) {
		// A deployment behind a TLS-terminating proxy publishes https, but
		// a lab or a test harness serves plain HTTP, and the scheme check
		// exists to catch a missing one rather than to demand TLS.
		cfg := valid()
		cfg.BaseURL = "http://localhost:8080"
		if _, err := acme.New(cfg); err != nil {
			t.Fatal(err)
		}
	})
}

// TestSignedRequest covers RFC 8555 section 6: everything the server checks
// about a JWS before an endpoint sees it.
func TestSignedRequest(t *testing.T) {
	// URLHeaderIsThePublishedURL proves the expected url is the one the
	// directory published rather than one rebuilt from the request. step-ca
	// builds it from r.Host and the path, so a proxy that rewrites Host
	// breaks it; nanoca additionally requires r.TLS to be set.
	t.Run("URLHeaderIsThePublishedURL", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		target := f.url("/new-order")
		for _, wrong := range []string{
			"https://attacker.example" + acme.DefaultPrefix + "/new-order",
			f.base + acme.DefaultPrefix + "/new-account",
			target + "/",
		} {
			res := f.signed(
				target, acct.key,
				jose.Header{KeyID: acct.url, URL: wrong},
				mustJSON(t, orderRequest(testIdentifier)),
			)
			p := requireProblem(t, res, acme.ProblemMalformed)
			if !strings.Contains(p.Detail, "url header") {
				t.Errorf("detail = %q, want it to name the url header", p.Detail)
			}
		}
	})

	// ProxyHostIsIgnored is the deployment case: TLS is terminated at the
	// edge and the last hop rewrites Host, so nothing about the connection
	// may be trusted. The same request that succeeds here fails on nanoca,
	// which insists on r.TLS, and on step-ca, which compares against
	// r.Host.
	t.Run("ProxyHostIsIgnored", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		res := f.signed(
			f.url("/new-order"), acct.key,
			jose.Header{KeyID: acct.url},
			mustJSON(t, orderRequest(testIdentifier)),
			func(r *http.Request) { r.Host = "mdm.example" },
		)
		requireStatus(t, res, http.StatusCreated)
	})

	t.Run("BodyTooLarge", func(t *testing.T) {
		// The device-attest-01 payload carries a whole attestation object,
		// which is the one ACME body an attacker can make large without
		// looking odd. step-ca reads it with an unbounded io.ReadAll.
		f := newFixture(t, func(c *acme.Config) { c.MaxBody = 1024 })
		acct := f.register()
		res := f.signed(
			f.url("/new-order"), acct.key,
			jose.Header{KeyID: acct.url},
			mustJSON(t, orderRequest(strings.Repeat("A", 4096))),
		)
		p := requireProblem(t, res, acme.ProblemMalformed)
		if !strings.Contains(p.Detail, "larger than") {
			t.Errorf("detail = %q, want it to say the body was too large", p.Detail)
		}
	})

	t.Run("WrongContentType", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		body, err := jose.Sign(
			acct.key,
			jose.Header{KeyID: acct.url, URL: f.url("/new-order"), Nonce: f.nonce()},
			mustJSON(t, orderRequest(testIdentifier)),
		)
		if err != nil {
			t.Fatal(err)
		}
		res := f.postRaw(f.url("/new-order"), "application/json", body)
		requireProblem(t, res, acme.ProblemMalformed)
	})

	t.Run("NotAJWS", func(t *testing.T) {
		f := newFixture(t)
		res := f.postRaw(f.url("/new-order"), acme.ContentTypeJOSE, []byte(`{"nope":1}`))
		requireProblem(t, res, acme.ProblemMalformed)
	})

	t.Run("BodyCannotBeRead", func(t *testing.T) {
		// A body that fails mid-read reaches the same answer as one that was
		// never well formed. It cannot be produced over a real connection,
		// so the handler is driven directly.
		f := newFixture(t)
		req := httptest.NewRequest(http.MethodPost, f.url("/new-order"), errorReader{})
		req.Header.Set("Content-Type", acme.ContentTypeJOSE)
		rec := httptest.NewRecorder()
		f.server.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body)
		}
	})

	t.Run("UnsupportedAlgorithmNamesTheSupportedSet", func(t *testing.T) {
		// RFC 8555 section 6.2 requires the accepted algorithms to be sent
		// with the problem, so a client can pick one without guessing.
		f := newFixture(t)
		acct := f.register()
		body := rawJWS(t, map[string]any{
			"alg":   "HS256",
			"nonce": f.nonce(),
			"url":   f.url("/new-order"),
			"kid":   acct.url,
		}, mustJSON(t, orderRequest(testIdentifier)), []byte("not a signature"))
		res := f.postRaw(f.url("/new-order"), acme.ContentTypeJOSE, body)
		p := requireProblem(t, res, acme.ProblemBadSignatureAlgorithm)
		if len(p.Algorithms) == 0 {
			t.Fatal("the problem carried no algorithms member")
		}
		if !contains(p.Algorithms, jose.ES256) || !contains(p.Algorithms, jose.RS256) {
			t.Errorf("algorithms = %v, want at least ES256 and RS256", p.Algorithms)
		}
	})

	t.Run("NeitherKIDNorJWK", func(t *testing.T) {
		f := newFixture(t)
		body := rawJWS(t, map[string]any{
			"alg":   jose.ES256,
			"nonce": f.nonce(),
			"url":   f.url("/new-order"),
		}, []byte(`{}`), []byte("signature"))
		res := f.postRaw(f.url("/new-order"), acme.ContentTypeJOSE, body)
		requireProblem(t, res, acme.ProblemMalformed)
	})

	t.Run("KIDIsNotAnAccountOfThisServer", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		for _, kid := range []string{
			"https://elsewhere.example" + acme.DefaultPrefix + "/account/" + acct.id,
			f.url("/account/"),
			f.url("/account/" + acct.id + "/orders"),
		} {
			res := f.signed(f.url("/new-order"), acct.key, jose.Header{KeyID: kid}, []byte(`{}`))
			requireProblem(t, res, acme.ProblemAccountDoesNotExist)
		}
	})

	t.Run("KIDNamesAnUnknownAccount", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		res := f.signed(
			f.url("/new-order"), acct.key,
			jose.Header{KeyID: f.url("/account/nobody")}, []byte(`{}`),
		)
		requireProblem(t, res, acme.ProblemAccountDoesNotExist)
	})

	t.Run("JWKOnAnEndpointThatWantsAKID", func(t *testing.T) {
		f := newFixture(t)
		key := newKey(t)
		jwk, err := jose.JWKFromPublic(key.Public())
		if err != nil {
			t.Fatal(err)
		}
		res := f.signed(
			f.url("/new-order"), key,
			jose.Header{JWK: jwk}, mustJSON(t, orderRequest(testIdentifier)),
		)
		p := requireProblem(t, res, acme.ProblemMalformed)
		if !strings.Contains(p.Detail, "account key") {
			t.Errorf("detail = %q, want it to ask for an account key", p.Detail)
		}
	})

	t.Run("KIDOnNewAccount", func(t *testing.T) {
		// new-account is the one endpoint where the key must be embedded,
		// because the server does not know it yet.
		f := newFixture(t)
		acct := f.register()
		res := f.signed(
			f.url("/new-account"), acct.key,
			jose.Header{KeyID: acct.url}, []byte(`{}`),
		)
		requireProblem(t, res, acme.ProblemMalformed)
	})

	t.Run("SignatureDoesNotVerify", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		res := f.signed(
			f.url("/new-order"), newKey(t),
			jose.Header{KeyID: acct.url}, mustJSON(t, orderRequest(testIdentifier)),
		)
		requireProblem(t, res, acme.ProblemUnauthorized)
	})

	t.Run("DeactivatedAccount", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		record, err := f.store.GetAccount(t.Context(), acct.id)
		if err != nil {
			t.Fatal(err)
		}
		record.Status = acme.StatusDeactivated
		if err := f.store.Update(t.Context(), func(tx acme.Tx) error {
			return tx.PutAccount(t.Context(), record)
		}); err != nil {
			t.Fatal(err)
		}
		res := acct.post(f.url("/new-order"), orderRequest(testIdentifier))
		p := requireProblem(t, res, acme.ProblemUnauthorized)
		if !strings.Contains(p.Detail, acme.StatusDeactivated) {
			t.Errorf("detail = %q, want it to name the account status", p.Detail)
		}
	})

	t.Run("StoredAccountKeyIsUnusable", func(t *testing.T) {
		// A record the server itself wrote cannot be malformed, so this is
		// our fault when it happens and must not look like the client's.
		f := newFixture(t)
		acct := f.register()
		record, err := f.store.GetAccount(t.Context(), acct.id)
		if err != nil {
			t.Fatal(err)
		}
		record.Key = &jose.JWK{Kty: "EC", Crv: "P-256"}
		if err := f.store.Update(t.Context(), func(tx acme.Tx) error {
			return tx.PutAccount(t.Context(), record)
		}); err != nil {
			t.Fatal(err)
		}
		res := acct.post(f.url("/new-order"), orderRequest(testIdentifier))
		requireProblem(t, res, acme.ProblemServerInternal)
	})

	t.Run("AccountCouldNotBeRead", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		broken := newFixtureSharing(t, f, map[string]error{"GetAccount": errStore})
		res := broken.signed(
			broken.url("/new-order"), acct.key,
			jose.Header{KeyID: broken.url("/account/" + acct.id)},
			mustJSON(t, orderRequest(testIdentifier)),
		)
		requireProblem(t, res, acme.ProblemServerInternal)
	})
}

// TestNonce covers RFC 8555 section 6.5. A nonce is single use and it
// expires: step-ca's nonces never do, so its table grows for the life of a
// deployment and one minted a year ago still works.
func TestNonce(t *testing.T) {
	t.Run("SingleUse", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		nonce := f.nonce()
		first := f.signed(
			f.url("/new-order"), acct.key,
			jose.Header{KeyID: acct.url, Nonce: nonce},
			mustJSON(t, orderRequest(testIdentifier)),
		)
		requireStatus(t, first, http.StatusCreated)
		second := f.signed(
			f.url("/new-order"), acct.key,
			jose.Header{KeyID: acct.url, Nonce: nonce},
			mustJSON(t, orderRequest(testIdentifier)),
		)
		requireProblem(t, second, acme.ProblemBadNonce)
		// A failed request still carries a fresh nonce, so a client can
		// retry without a round trip to new-nonce.
		if second.header.Get("Replay-Nonce") == "" {
			t.Error("the badNonce response carried no replacement nonce")
		}
	})

	t.Run("Expired", func(t *testing.T) {
		f := newFixture(t, func(c *acme.Config) { c.NonceTTL = 30 * time.Minute })
		acct := f.register()
		nonce := f.nonce()
		f.clock.Advance(31 * time.Minute)
		res := f.signed(
			f.url("/new-order"), acct.key,
			jose.Header{KeyID: acct.url, Nonce: nonce},
			mustJSON(t, orderRequest(testIdentifier)),
		)
		p := requireProblem(t, res, acme.ProblemBadNonce)
		if !strings.Contains(p.Detail, "expired") {
			t.Errorf("detail = %q, want it to say the nonce expired", p.Detail)
		}
	})

	t.Run("NeverIssued", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		res := f.signed(
			f.url("/new-order"), acct.key,
			jose.Header{KeyID: acct.url, Nonce: "AAAAAAAAAAAAAAAAAAAAAA"},
			mustJSON(t, orderRequest(testIdentifier)),
		)
		requireProblem(t, res, acme.ProblemBadNonce)
	})

	t.Run("Absent", func(t *testing.T) {
		// The JWS layer refuses a header with no nonce before the server's
		// own check is reached, so an absent nonce is malformed rather than
		// badNonce. Either way the client learns to fetch one.
		f := newFixture(t)
		acct := f.register()
		body := rawJWS(t, map[string]any{
			"alg": jose.ES256, "url": f.url("/new-order"), "kid": acct.url,
		}, []byte(`{}`), []byte("signature"))
		res := f.postRaw(f.url("/new-order"), acme.ContentTypeJOSE, body)
		requireProblem(t, res, acme.ProblemMalformed)
	})

	t.Run("StoreFailureIsNotTheClientsFault", func(t *testing.T) {
		f := newFixture(t)
		acct := f.register()
		broken := newFixtureSharing(t, f, map[string]error{"TakeNonce": errStore})
		res := broken.signed(
			broken.url("/new-order"), acct.key,
			jose.Header{KeyID: broken.url("/account/" + acct.id), Nonce: "AAAAAAAAAAAAAAAAAAAAAA"},
			mustJSON(t, orderRequest(testIdentifier)),
		)
		requireProblem(t, res, acme.ProblemServerInternal)
	})

	t.Run("MintingFailureStillAnswers", func(t *testing.T) {
		// A client that gets no nonce will fetch one, so failing the request
		// because a nonce could not be minted would be worse than carrying
		// on without one.
		f := newFixture(t, func(c *acme.Config) {
			c.Store = &acmetest.Failing{Store: c.Store, Fail: map[string]error{"PutNonce": errStore}}
		})
		res := f.get(f.url("/directory"))
		requireStatus(t, res, http.StatusOK)
		if got := res.header.Get("Replay-Nonce"); got != "" {
			t.Errorf("Replay-Nonce = %q, want none", got)
		}
	})
}

// TestNewAccount covers RFC 8555 section 7.3: a key is an account.
func TestNewAccount(t *testing.T) {
	t.Run("Created", func(t *testing.T) {
		f := newFixture(t)
		key := newKey(t)
		res := f.newAccountRequest(key, map[string]any{
			"termsOfServiceAgreed": true,
			"contact":              []string{"mailto:admin@example.test"},
		})
		requireStatus(t, res, http.StatusCreated)
		body := decode[accountJSON](t, res)
		if body.Status != acme.StatusValid {
			t.Errorf("status = %q, want valid", body.Status)
		}
		if len(body.Contact) != 1 || body.Contact[0] != "mailto:admin@example.test" {
			t.Errorf("contact = %v, want the one that was sent", body.Contact)
		}
		location := res.header.Get("Location")
		if want := f.url("/account/" + idOf(location)); location != want {
			t.Errorf("Location = %q, want an account URL like %q", location, want)
		}
		if body.Orders != location+"/orders" {
			t.Errorf("orders = %q, want %q", body.Orders, location+"/orders")
		}
	})

	t.Run("SameKeyReturnsTheSameAccount", func(t *testing.T) {
		// A device that lost its state and registers again has to get the
		// account it already has, not a second one.
		f := newFixture(t)
		key := newKey(t)
		first := f.newAccountRequest(key, map[string]any{"termsOfServiceAgreed": true})
		requireStatus(t, first, http.StatusCreated)
		second := f.newAccountRequest(key, map[string]any{"termsOfServiceAgreed": true})
		requireStatus(t, second, http.StatusOK)
		if first.header.Get("Location") != second.header.Get("Location") {
			t.Fatalf(
				"second registration got %q, want %q",
				second.header.Get("Location"), first.header.Get("Location"),
			)
		}
	})

	t.Run("OnlyReturnExistingWithAnUnknownKey", func(t *testing.T) {
		f := newFixture(t)
		res := f.newAccountRequest(newKey(t), map[string]any{"onlyReturnExisting": true})
		requireProblem(t, res, acme.ProblemAccountDoesNotExist)
	})

	t.Run("OnlyReturnExistingWithAKnownKey", func(t *testing.T) {
		f := newFixture(t)
		key := newKey(t)
		created := f.newAccountRequest(key, map[string]any{"termsOfServiceAgreed": true})
		requireStatus(t, created, http.StatusCreated)
		res := f.newAccountRequest(key, map[string]any{"onlyReturnExisting": true})
		requireStatus(t, res, http.StatusOK)
		if res.header.Get("Location") != created.header.Get("Location") {
			t.Error("onlyReturnExisting returned a different account")
		}
	})

	t.Run("PayloadIsNotAnObject", func(t *testing.T) {
		f := newFixture(t)
		key := newKey(t)
		jwk, err := jose.JWKFromPublic(key.Public())
		if err != nil {
			t.Fatal(err)
		}
		res := f.signed(f.url("/new-account"), key, jose.Header{JWK: jwk}, []byte(`["not an object"]`))
		requireProblem(t, res, acme.ProblemMalformed)
	})

	t.Run("PostAsGetIsRefused", func(t *testing.T) {
		f := newFixture(t)
		key := newKey(t)
		jwk, err := jose.JWKFromPublic(key.Public())
		if err != nil {
			t.Fatal(err)
		}
		res := f.signed(f.url("/new-account"), key, jose.Header{JWK: jwk}, nil)
		requireProblem(t, res, acme.ProblemMalformed)
	})

	t.Run("ConcurrentRegistrationsOfOneKeyAgree", func(t *testing.T) {
		// Two registrations of one key can race, and the loser has to
		// answer with the account the winner made rather than an error the
		// client cannot act on.
		f := newFixture(t)
		key := newKey(t)
		jwk, err := jose.JWKFromPublic(key.Public())
		if err != nil {
			t.Fatal(err)
		}
		thumbprint, err := jwk.Thumbprint()
		if err != nil {
			t.Fatal(err)
		}
		// The winner's account is seeded, and the first lookup is made to
		// miss, so the loser's path runs the same way every time.
		const winner = "the-account-that-won-the-race"
		if err := f.store.Update(t.Context(), func(tx acme.Tx) error {
			return tx.PutAccount(t.Context(), &acme.Account{
				ID: winner, Thumbprint: thumbprint, Key: jwk,
				Status: acme.StatusValid, CreatedAt: f.clock.Now(),
			})
		}); err != nil {
			t.Fatal(err)
		}
		blind := &blindFirstLookup{Store: f.store}
		racing := newFixtureWithStore(t, blind)
		res := racing.newAccountRequest(key, map[string]any{"termsOfServiceAgreed": true})
		requireStatus(t, res, http.StatusOK)
		if got, want := res.header.Get("Location"), racing.url("/account/"+winner); got != want {
			t.Fatalf("Location = %q, want the winner's account %q", got, want)
		}
	})

	t.Run("ConflictAndThenNoAccount", func(t *testing.T) {
		f := newFixture(t, func(c *acme.Config) {
			c.Store = &acmetest.Failing{
				Store: c.Store,
				Fail:  map[string]error{"PutAccount": acme.ErrConflict, "AccountByThumbprint": errStore},
				// The first lookup has to miss so the account is created;
				// the one inside the conflict branch is the one that fails.
				After: map[string]int{"AccountByThumbprint": 2},
			}
		})
		res := f.newAccountRequest(newKey(t), map[string]any{"termsOfServiceAgreed": true})
		requireProblem(t, res, acme.ProblemServerInternal)
	})

	t.Run("StoreFailures", func(t *testing.T) {
		for name, fail := range map[string]map[string]error{
			"Lookup": {"AccountByThumbprint": errStore},
			"Write":  {"PutAccount": errStore},
		} {
			t.Run(name, func(t *testing.T) {
				f := newFixture(t, func(c *acme.Config) {
					c.Store = &acmetest.Failing{Store: c.Store, Fail: fail}
				})
				res := f.newAccountRequest(newKey(t), map[string]any{"termsOfServiceAgreed": true})
				requireProblem(t, res, acme.ProblemServerInternal)
			})
		}
	})
}

// TestAccount covers the account endpoint, which answers a POST-as-GET and
// nothing else: Apple's client makes no account updates.
func TestAccount(t *testing.T) {
	f := newFixture(t)
	acct := f.register()

	t.Run("PostAsGet", func(t *testing.T) {
		res := acct.post(acct.url, nil)
		requireStatus(t, res, http.StatusOK)
		body := decode[accountJSON](t, res)
		if body.Status != acme.StatusValid {
			t.Errorf("status = %q, want valid", body.Status)
		}
	})

	t.Run("UpdatesAreRefused", func(t *testing.T) {
		res := acct.post(acct.url, map[string]any{"contact": []string{"mailto:new@example.test"}})
		requireProblem(t, res, acme.ProblemMalformed)
	})

	t.Run("AnotherAccountIsUnauthorized", func(t *testing.T) {
		other := f.register()
		requireProblem(t, other.post(acct.url, nil), acme.ProblemUnauthorized)
		requireProblem(t, other.post(acct.url+"/orders", nil), acme.ProblemUnauthorized)
	})
}

// TestAccountOrders lists an account's orders and pages them.
func TestAccountOrders(t *testing.T) {
	f := newFixture(t)
	acct := f.register()

	t.Run("Empty", func(t *testing.T) {
		body := decode[ordersJSON](t, acct.post(acct.url+"/orders", nil))
		if len(body.Orders) != 0 {
			t.Fatalf("orders = %v, want none", body.Orders)
		}
	})

	t.Run("Paged", func(t *testing.T) {
		// Orders are seeded directly: the point here is the pagination and
		// the next link, not the ordering endpoint.
		const total = 105
		if err := f.store.Update(t.Context(), func(tx acme.Tx) error {
			for i := range total {
				id := "seeded-order-" + string(rune('a'+i/26)) + string(rune('a'+i%26))
				err := tx.PutOrder(t.Context(), &acme.Order{
					ID: id, AccountID: acct.id, Status: acme.StatusPending,
					Identifier: acme.Identifier{Type: acme.IdentifierPermanent, Value: id},
					Expires:    f.clock.Now().Add(time.Hour), CreatedAt: f.clock.Now(),
				})
				if err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		res := acct.post(acct.url+"/orders", nil)
		requireStatus(t, res, http.StatusOK)
		first := decode[ordersJSON](t, res)
		if len(first.Orders) == 0 || len(first.Orders) >= total {
			t.Fatalf("first page has %d orders, want a partial page of %d", len(first.Orders), total)
		}
		next := nextLink(res.header)
		if next == "" {
			t.Fatal("no next link on a partial page")
		}
		rest := decode[ordersJSON](t, acct.post(next, nil))
		if got := len(first.Orders) + len(rest.Orders); got != total {
			t.Fatalf("read %d orders across two pages, want %d", got, total)
		}
		if nextLink(acct.post(next, nil).header) != "" {
			t.Error("the last page still offered a next link")
		}
	})

	t.Run("StoreFailure", func(t *testing.T) {
		broken := newFixtureSharing(t, f, map[string]error{"ListOrders": errStore})
		res := f.signed(
			broken.url("/account/"+acct.id+"/orders"), acct.key,
			jose.Header{KeyID: broken.url("/account/" + acct.id)}, nil,
		)
		requireProblem(t, res, acme.ProblemServerInternal)
	})
}

// TestCertificateEndpoint downloads an issued chain and refuses to hand it
// to anyone else.
func TestCertificateEndpoint(t *testing.T) {
	f := newFixture(t)
	fl := f.begin(testIdentifier).pass()
	requireStatus(t, fl.finalizeWith(fl.key, pkix.Name{}), http.StatusOK)
	certURL := fl.order().Certificate
	if certURL == "" {
		t.Fatal("the order names no certificate")
	}

	t.Run("PostAsGet", func(t *testing.T) {
		res := fl.acct.post(certURL, nil)
		requireStatus(t, res, http.StatusOK)
		if ct := res.header.Get("Content-Type"); ct != acme.ContentTypePEMChain {
			t.Errorf("content type = %q, want %q", ct, acme.ContentTypePEMChain)
		}
		leafOf(t, res.body)
	})

	t.Run("AnotherAccountIsUnauthorized", func(t *testing.T) {
		other := f.register()
		requireProblem(t, other.post(certURL, nil), acme.ProblemUnauthorized)
	})

	t.Run("NoSuchCertificate", func(t *testing.T) {
		requireProblem(t, fl.acct.post(f.url("/cert/nothing"), nil), acme.ProblemMalformed)
	})

	t.Run("StoreFailure", func(t *testing.T) {
		broken := newFixtureSharing(t, f, map[string]error{"GetCertificate": errStore})
		res := f.signed(
			broken.url("/cert/"+idOf(certURL)), fl.acct.key,
			jose.Header{KeyID: broken.url("/account/" + fl.acct.id)}, nil,
		)
		requireProblem(t, res, acme.ProblemServerInternal)
	})
}

// Helpers used only by the server tests.

// errStore stands in for any backend fault.
var errStore = errors.New("the store is unavailable")

// errorReader fails on the first read, which is what a connection dropping
// mid-body looks like to the handler.
type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errStore }

// blindFirstLookup makes the first thumbprint lookup miss, so the account
// creation that follows loses the race deterministically.
type blindFirstLookup struct {
	*inmem.Store
	seen bool
}

func (b *blindFirstLookup) AccountByThumbprint(
	ctx context.Context,
	thumbprint string,
) (*acme.Account, error) {
	if !b.seen {
		b.seen = true
		return nil, acme.ErrNotFound
	}
	return b.Store.AccountByThumbprint(ctx, thumbprint)
}

// rawJWS assembles a flattened JWS from parts, for the headers jose.Sign
// would refuse to write.
func rawJWS(t *testing.T, protected map[string]any, payload, signature []byte) []byte {
	t.Helper()
	return mustJSON(t, map[string]string{
		"protected": base64.RawURLEncoding.EncodeToString(mustJSON(t, protected)),
		"payload":   base64.RawURLEncoding.EncodeToString(payload),
		"signature": base64.RawURLEncoding.EncodeToString(signature),
	})
}

// newFixtureSharing builds a second server over the first one's store, so a
// backend fault can be injected into an exchange the first server set up.
func newFixtureSharing(t *testing.T, f *fixture, fail map[string]error) *fixture {
	t.Helper()
	return newFixtureWithStore(t, &acmetest.Failing{Store: f.store, Fail: fail})
}

func newFixtureWithStore(t *testing.T, store acme.Store) *fixture {
	t.Helper()
	return newFixture(t, func(c *acme.Config) { c.Store = store })
}

// nextLink reads the rel="next" link RFC 8555 section 7.1.2.1 uses for
// paging.
func nextLink(h http.Header) string {
	for _, value := range h.Values("Link") {
		if !strings.Contains(value, `rel="next"`) {
			continue
		}
		if start := strings.Index(value, "<"); start >= 0 {
			if end := strings.Index(value, ">"); end > start {
				return value[start+1 : end]
			}
		}
	}
	return ""
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

var (
	_ = io.Discard
	_ = x509.MarshalPKIXPublicKey
)
