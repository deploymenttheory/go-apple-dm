package acme_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/acme"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
)

// identifierSeed is the message authentication key the tests mint with. It
// is longer than MinIdentifierKey so that only the tests that mean to test
// the length limit run into it.
var identifierSeed = []byte("sixteen bytes at the very least, and then some")

// TestHMACIdentifiers proves the client identifier is a bearer token that
// carries its own binding rather than a value anyone can guess. step-ca
// requires the ordered identifier to equal the device's serial number,
// which is printed on the case and appears in every inventory, so knowing
// a serial number is enough to order a certificate there.
func TestHMACIdentifiers(t *testing.T) {
	t.Run("RoundTrip", func(t *testing.T) {
		fake := clock.NewFake(time.Now())
		ids, err := acme.NewHMACIdentifiers(identifierSeed, time.Hour, fake)
		if err != nil {
			t.Fatal(err)
		}
		want := acme.Binding{
			Serial: testSerial, UDID: testUDID, EnrollmentID: "enrollment-1",
			CommonName: testCommonName, Organization: []string{"Deployment Theory"},
			AllowUnidentified: true, NotAfter: fake.Now().Add(24 * time.Hour).UTC().Truncate(time.Second),
		}
		identifier, err := ids.Issue(want)
		if err != nil {
			t.Fatal(err)
		}
		got, err := ids.Verify(t.Context(), identifier)
		if err != nil {
			t.Fatal(err)
		}
		if got.Serial != want.Serial || got.UDID != want.UDID ||
			got.EnrollmentID != want.EnrollmentID || got.CommonName != want.CommonName ||
			got.AllowUnidentified != want.AllowUnidentified ||
			!got.NotAfter.Equal(want.NotAfter) ||
			len(got.Organization) != 1 || got.Organization[0] != want.Organization[0] {
			t.Fatalf("binding = %+v, want %+v", got, want)
		}
	})

	// Tampered is the whole point of the message authentication code: an
	// identifier this server did not mint, or one whose binding was edited
	// to name another device, buys nothing.
	t.Run("Tampered", func(t *testing.T) {
		fake := clock.NewFake(time.Now())
		ids, err := acme.NewHMACIdentifiers(identifierSeed, time.Hour, fake)
		if err != nil {
			t.Fatal(err)
		}
		good, err := ids.Issue(acme.Binding{Serial: testSerial, UDID: testUDID})
		if err != nil {
			t.Fatal(err)
		}
		payload, signature, _ := strings.Cut(good, ".")

		// The edited payload is the attack the code exists to stop: a
		// binding rewritten to name a device the holder does not own.
		edited := decodeSegment(t, payload)
		var sealed map[string]any
		if err := json.Unmarshal(edited, &sealed); err != nil {
			t.Fatal(err)
		}
		binding, ok := sealed["b"].(map[string]any)
		if !ok {
			t.Fatalf("sealed payload = %v, want a binding member", sealed)
		}
		binding["serial"] = "C02SOMEONEELSES"
		editedPayload := base64.RawURLEncoding.EncodeToString(mustJSON(t, sealed))

		// A payload that authenticates but is not the JSON we wrote can only
		// come from someone holding the key, so it has to fail on its own
		// terms rather than reaching the decoder unguarded.
		notJSON := []byte("this authenticates but is not a sealed binding")
		mac := hmac.New(sha256.New, identifierSeed)
		mac.Write(notJSON)
		authenticatedRubbish := base64.RawURLEncoding.EncodeToString(notJSON) +
			"." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

		cases := map[string]string{
			"EditedPayload":               editedPayload + "." + signature,
			"WrongSignature":              payload + "." + base64.RawURLEncoding.EncodeToString([]byte("nope")),
			"PayloadNotBase64":            "!!! not base64url !!!." + signature,
			"SignatureNotBase64":          payload + ".!!! not base64url !!!",
			"NoSeparator":                 payload + signature,
			"Empty":                       "",
			"AuthenticatedButNotABinding": authenticatedRubbish,
		}
		for name, identifier := range cases {
			t.Run(name, func(t *testing.T) {
				_, err := ids.Verify(t.Context(), identifier)
				requireRejected(t, err)
			})
		}

		t.Run("MintedByAnotherServer", func(t *testing.T) {
			other, err := acme.NewHMACIdentifiers(
				[]byte("a different key of ample length indeed"), time.Hour, fake,
			)
			if err != nil {
				t.Fatal(err)
			}
			foreign, err := other.Issue(acme.Binding{Serial: testSerial})
			if err != nil {
				t.Fatal(err)
			}
			_, err = ids.Verify(t.Context(), foreign)
			requireRejected(t, err)
		})

		t.Run("Expired", func(t *testing.T) {
			// Statelessness bounds replay by time; the one-time property
			// comes from the store.
			fake.Advance(time.Hour + time.Second)
			_, err := ids.Verify(t.Context(), good)
			p := requireRejected(t, err)
			if !strings.Contains(p.Detail, "expired") {
				t.Errorf("detail = %q, want it to say the identifier expired", p.Detail)
			}
		})
	})

	t.Run("ShortKeyIsRefused", func(t *testing.T) {
		for _, size := range []int{0, acme.MinIdentifierKey - 1} {
			ids, err := acme.NewHMACIdentifiers(make([]byte, size), time.Hour, nil)
			if !errors.Is(err, acme.ErrIdentifierKey) {
				t.Fatalf("key of %d bytes: error = %v, want ErrIdentifierKey", size, err)
			}
			if ids != nil {
				t.Error("a minter was returned alongside the error")
			}
		}
	})

	t.Run("Defaults", func(t *testing.T) {
		// A zero lifetime and a nil clock are filled in, so a caller that
		// supplies only a key still gets something usable.
		ids, err := acme.NewHMACIdentifiers(identifierSeed, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		identifier, err := ids.Issue(acme.Binding{Serial: testSerial})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ids.Verify(t.Context(), identifier); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("OrderedThroughTheServer", func(t *testing.T) {
		// The server needs no state between composing an enrollment profile
		// and seeing the order that uses it, which is the point of sealing
		// the binding into the identifier.
		var ids *acme.HMACIdentifiers
		f := newFixture(t, func(c *acme.Config) {
			minter, err := acme.NewHMACIdentifiers(identifierSeed, time.Hour, c.Clock)
			if err != nil {
				t.Fatal(err)
			}
			ids, c.Identifiers = minter, minter
		})
		identifier, err := ids.Issue(acme.Binding{
			Serial: testSerial, UDID: testUDID, CommonName: testCommonName,
		})
		if err != nil {
			t.Fatal(err)
		}
		// A second identifier is minted now and ordered after it has gone
		// stale, so the refusal below is expiry rather than the one-time
		// claim the first identifier already took.
		stale, err := ids.Issue(acme.Binding{Serial: testSerial, UDID: testUDID})
		if err != nil {
			t.Fatal(err)
		}
		fl := f.begin(identifier)
		requireStatus(t, fl.answer(fl.attestation(deviceProperties())), http.StatusOK)

		f.clock.Advance(2 * time.Hour)
		acct := f.register()
		p := requireProblem(
			t, acct.post(f.url("/new-order"), orderRequest(stale)),
			acme.ProblemRejectedIdentifier,
		)
		if !strings.Contains(p.Detail, "expired") {
			t.Errorf("detail = %q, want it to say the identifier expired", p.Detail)
		}
	})
}

// TestStaticIdentifiers is the development and test source: a fixed map,
// whose zero value accepts nothing.
func TestStaticIdentifiers(t *testing.T) {
	ids := acme.StaticIdentifiers{testIdentifier: defaultBinding()}
	got, err := ids.Verify(t.Context(), testIdentifier)
	if err != nil {
		t.Fatal(err)
	}
	if got.Serial != testSerial {
		t.Fatalf("binding serial = %q, want %q", got.Serial, testSerial)
	}
	if _, err := ids.Verify(t.Context(), "not in the map"); err == nil {
		t.Fatal("an unknown identifier was accepted")
	} else {
		requireRejected(t, err)
	}
	var empty acme.StaticIdentifiers
	if _, err := empty.Verify(t.Context(), testIdentifier); err == nil {
		t.Fatal("the zero value accepted an identifier")
	}
}

// TestIdentifiersFunc adapts a function, which is how a deployment reaches
// its own directory.
func TestIdentifiersFunc(t *testing.T) {
	seen := ""
	ids := acme.IdentifiersFunc(func(_ context.Context, identifier string) (acme.Binding, error) {
		seen = identifier
		if identifier != testIdentifier {
			return acme.Binding{}, acme.NewProblem(
				acme.ProblemRejectedIdentifier, "the identifier is not recognised",
			)
		}
		return acme.Binding{Serial: testSerial}, nil
	})
	got, err := ids.Verify(t.Context(), testIdentifier)
	if err != nil {
		t.Fatal(err)
	}
	if got.Serial != testSerial || seen != testIdentifier {
		t.Fatalf("binding = %+v, saw %q", got, seen)
	}
	if _, err := ids.Verify(t.Context(), "something else"); err == nil {
		t.Fatal("an unknown identifier was accepted")
	}

	t.Run("AFaultIsNotARefusal", func(t *testing.T) {
		// An identifier source that broke is our fault, so the order fails
		// with a server error rather than telling the device its identifier
		// was no good.
		f := newFixture(t, func(c *acme.Config) {
			c.Identifiers = acme.IdentifiersFunc(
				func(context.Context, string) (acme.Binding, error) {
					return acme.Binding{}, acme.WrapProblem(
						acme.ProblemServerInternal, errStore, "the directory is unavailable",
					)
				},
			)
		})
		acct := f.register()
		res := acct.post(f.url("/new-order"), orderRequest(testIdentifier))
		requireProblem(t, res, acme.ProblemServerInternal)
	})
}

func decodeSegment(t *testing.T, segment string) []byte {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(segment)
	if err != nil {
		t.Fatalf("decode %q: %v", segment, err)
	}
	return raw
}

// requireRejected insists a refusal is a rejectedIdentifier problem, so an
// order fails cleanly rather than looking like a server fault.
func requireRejected(t *testing.T, err error) *acme.Problem {
	t.Helper()
	if err == nil {
		t.Fatal("the identifier was accepted")
	}
	if !errors.Is(err, acme.ErrRejected) {
		t.Fatalf("error = %v, want a rejectedIdentifier problem", err)
	}
	p := acme.AsProblem(err)
	if !p.Terminal() {
		t.Errorf("problem %v is not terminal, so the order would not settle", p)
	}
	return p
}
