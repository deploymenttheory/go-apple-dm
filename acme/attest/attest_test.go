package attest_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/acme/attest"
	"github.com/deploymenttheory/go-apple-dm/acme/attest/attesttest"
	"github.com/deploymenttheory/go-apple-dm/internal/cbor"
)

const token = "9tXmyH1t3fFQ0zPzr3aUqKq0Q7RmAcXvIcQ5cdKZ3wA"

func key(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newCA(t *testing.T) *attesttest.CA {
	t.Helper()
	ca, err := attesttest.NewCA()
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func boolp(b bool) *bool { return &b }

// macProperties is a full set as a Mac on macOS 14.2 or later reports it.
func macProperties() attest.Properties {
	return attest.Properties{
		SerialNumber:           "TMWFWWWP67",
		UDID:                   "00006030-000651690A84001C",
		SoftwareUpdateDeviceID: "J514sAP",
		OSVersion:              "15.5",
		SEPOSVersion:           "15.5",
		LLBVersion:             "15.5",
		SecureBoot:             attest.SecureBootFull,
		SIPEnabled:             boolp(true),
		KextsAllowed:           boolp(false),
	}
}

func TestParseObjectReadsEveryDocumentedProperty(t *testing.T) {
	ca := newCA(t)
	k := key(t)
	raw, err := ca.ObjectForToken(token, macProperties(), k.Public())
	if err != nil {
		t.Fatal(err)
	}
	a, err := attest.ParseObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	if a.Format != attest.FormatApple {
		t.Fatalf("format = %q", a.Format)
	}
	if len(a.Chain) != 2 {
		t.Fatalf("chain of %d", len(a.Chain))
	}
	p := a.Properties
	want := macProperties()
	if p.SerialNumber != want.SerialNumber || p.UDID != want.UDID ||
		p.SoftwareUpdateDeviceID != want.SoftwareUpdateDeviceID ||
		p.OSVersion != want.OSVersion || p.SEPOSVersion != want.SEPOSVersion ||
		p.LLBVersion != want.LLBVersion || p.SecureBoot != want.SecureBoot {
		t.Fatalf("properties = %+v", p)
	}
	// The two macOS statuses are DER integers, not strings: reading them
	// as raw bytes would give a value that looks like data but is not.
	if p.SIPEnabled == nil || !*p.SIPEnabled {
		t.Fatalf("SIP = %v", p.SIPEnabled)
	}
	if p.KextsAllowed == nil || *p.KextsAllowed {
		t.Fatalf("kexts = %v", p.KextsAllowed)
	}
	if !p.Identified() {
		t.Fatal("attestation names no device")
	}
	// The object is kept verbatim so finalize can verify it again.
	if string(a.Raw) != string(raw) {
		t.Fatal("raw object not preserved")
	}
	if err := a.Verify(attest.VerifyOptions{
		Anchors:   ca.Anchors(),
		Freshness: attest.FreshnessForToken(token),
		PublicKey: k.Public(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestParseObjectRejects(t *testing.T) {
	ca := newCA(t)
	k := key(t)
	good, err := ca.ObjectForToken(token, macProperties(), k.Public())
	if err != nil {
		t.Fatal(err)
	}
	wrongFormat, err := cbor.Marshal(map[string]any{
		"fmt":     "tpm",
		"attStmt": map[string]any{"x5c": [][]byte{{1}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	badCert, err := attesttest.Object([][]byte{{1, 2, 3}})
	if err != nil {
		t.Fatal(err)
	}
	long := make([][]byte, attest.MaxChain+1)
	for i := range long {
		long[i] = ca.Intermediate.Raw
	}
	tooLong, err := attesttest.Object(long)
	if err != nil {
		t.Fatal(err)
	}
	badStmt, err := cbor.Marshal(map[string]any{"fmt": "apple", "attStmt": "not a map"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   []byte
		want error
	}{
		{"not cbor", []byte{0xff, 0xff}, attest.ErrFormat},
		{"truncated", good[:len(good)-1], attest.ErrFormat},
		{"wrong statement format", wrongFormat, attest.ErrFormat},
		{"statement not a map", badStmt, attest.ErrFormat},
		{"unparseable certificate", badCert, attest.ErrFormat},
		{"chain too long", tooLong, attest.ErrFormat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := attest.ParseObject(tc.in); !errors.Is(err, tc.want) {
				t.Fatalf("ParseObject = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseObjectWithoutAttestation(t *testing.T) {
	// A device whose profile did not ask for an attestation, or whose
	// hardware cannot produce one, still answers the challenge. That is a
	// policy question, not a malformed message, so it has its own error.
	for _, stmt := range []any{
		map[string]any{},
		map[string]any{"x5c": [][]byte{}},
	} {
		raw, err := cbor.Marshal(map[string]any{"fmt": "apple", "attStmt": stmt})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := attest.ParseObject(raw); !errors.Is(err, attest.ErrNoAttestation) {
			t.Fatalf("ParseObject = %v, want ErrNoAttestation", err)
		}
	}
}

func TestParseChain(t *testing.T) {
	ca := newCA(t)
	k := key(t)
	// The DeviceInformation response carries the same chain as DER.
	chain, err := ca.Chain(attesttest.LeafOptions{
		Properties: attest.Properties{
			SerialNumber: "C02X1234",
			Freshness:    []byte("a 32 byte nonce chosen by us...."),
		},
		PublicKey: k.Public(),
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := attest.ParseChain(chain)
	if err != nil {
		t.Fatal(err)
	}
	if a.Properties.SerialNumber != "C02X1234" {
		t.Fatalf("serial = %q", a.Properties.SerialNumber)
	}
	if len(a.Raw) != 0 {
		t.Fatal("a chain has no attestation object")
	}
	// A DeviceInformation reader has no key to bind.
	if err := a.Verify(attest.VerifyOptions{
		Anchors:   ca.Anchors(),
		Freshness: []byte("a 32 byte nonce chosen by us...."),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := attest.ParseChain(nil); !errors.Is(err, attest.ErrNoAttestation) {
		t.Fatalf("empty chain = %v", err)
	}
	if _, err := attest.ParseChain([][]byte{{1, 2}}); !errors.Is(err, attest.ErrFormat) {
		t.Fatalf("bad DER = %v", err)
	}
}

func TestVerifyFreshness(t *testing.T) {
	ca := newCA(t)
	k := key(t)
	t.Run("MissingExtensionFails", func(t *testing.T) {
		// step-ca skips the comparison when the extension is absent, so an
		// attestation carrying no freshness code passes its check. An
		// attestation that says nothing about which challenge it answers
		// must fail here exactly like one carrying the wrong code.
		raw, err := ca.Object(attesttest.LeafOptions{
			Properties: attest.Properties{SerialNumber: "NOFRESH"},
			PublicKey:  k.Public(),
		})
		if err != nil {
			t.Fatal(err)
		}
		a, err := attest.ParseObject(raw)
		if err != nil {
			t.Fatal(err)
		}
		if len(a.Properties.Freshness) != 0 {
			t.Fatal("expected no freshness extension")
		}
		err = a.Verify(attest.VerifyOptions{
			Anchors:   ca.Anchors(),
			Freshness: attest.FreshnessForToken(token),
			PublicKey: k.Public(),
		})
		if !errors.Is(err, attest.ErrFreshness) {
			t.Fatalf("Verify = %v, want ErrFreshness", err)
		}
	})
	t.Run("AnotherChallengeFails", func(t *testing.T) {
		// An attestation minted for one order must not be replayable into
		// another.
		raw, err := ca.ObjectForToken("a-different-token", macProperties(), k.Public())
		if err != nil {
			t.Fatal(err)
		}
		a, err := attest.ParseObject(raw)
		if err != nil {
			t.Fatal(err)
		}
		err = a.Verify(attest.VerifyOptions{
			Anchors:   ca.Anchors(),
			Freshness: attest.FreshnessForToken(token),
			PublicKey: k.Public(),
		})
		if !errors.Is(err, attest.ErrFreshness) {
			t.Fatalf("Verify = %v, want ErrFreshness", err)
		}
	})
	t.Run("RequiredByOptions", func(t *testing.T) {
		raw, err := ca.ObjectForToken(token, macProperties(), k.Public())
		if err != nil {
			t.Fatal(err)
		}
		a, err := attest.ParseObject(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Verify(attest.VerifyOptions{Anchors: ca.Anchors()}); !errors.Is(err, attest.ErrOptions) {
			t.Fatalf("Verify = %v, want ErrOptions", err)
		}
	})
	t.Run("MatchesAppleRule", func(t *testing.T) {
		sum := sha256.Sum256([]byte(token))
		if got := attest.FreshnessForToken(token); string(got) != string(sum[:]) {
			t.Fatalf("FreshnessForToken = %x", got)
		}
	})
}

func TestVerifyBindsTheAttestedKey(t *testing.T) {
	// Apple's guidance is to retain the public key in the attestation leaf
	// for a later validation. Without this check anyone who observes a
	// valid attestation could have an unrelated key certified.
	ca := newCA(t)
	attested := key(t)
	other := key(t)
	raw, err := ca.ObjectForToken(token, macProperties(), attested.Public())
	if err != nil {
		t.Fatal(err)
	}
	a, err := attest.ParseObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	err = a.Verify(attest.VerifyOptions{
		Anchors:   ca.Anchors(),
		Freshness: attest.FreshnessForToken(token),
		PublicKey: other.Public(),
	})
	if !errors.Is(err, attest.ErrKeyMismatch) {
		t.Fatalf("Verify = %v, want ErrKeyMismatch", err)
	}
	// A key that cannot be marshalled is a caller fault, not a panic.
	err = a.Verify(attest.VerifyOptions{
		Anchors:   ca.Anchors(),
		Freshness: attest.FreshnessForToken(token),
		PublicKey: struct{ crypto.PublicKey }{},
	})
	if !errors.Is(err, attest.ErrKeyMismatch) {
		t.Fatalf("Verify = %v, want ErrKeyMismatch", err)
	}
}

func TestVerifyChain(t *testing.T) {
	ca := newCA(t)
	k := key(t)
	t.Run("ForeignAuthority", func(t *testing.T) {
		// A chain that verifies against its own root must not verify
		// against ours.
		other := newCA(t)
		raw, err := other.ObjectForToken(token, macProperties(), k.Public())
		if err != nil {
			t.Fatal(err)
		}
		a, err := attest.ParseObject(raw)
		if err != nil {
			t.Fatal(err)
		}
		err = a.Verify(attest.VerifyOptions{
			Anchors:   ca.Anchors(),
			Freshness: attest.FreshnessForToken(token),
			PublicKey: k.Public(),
		})
		if !errors.Is(err, attest.ErrChain) {
			t.Fatalf("Verify = %v, want ErrChain", err)
		}
	})
	t.Run("Expired", func(t *testing.T) {
		start := time.Now().Add(-200 * 24 * time.Hour)
		raw, err := ca.Object(attesttest.LeafOptions{
			Properties: attest.Properties{Freshness: attest.FreshnessForToken(token)},
			PublicKey:  k.Public(),
			NotBefore:  start,
			NotAfter:   start.Add(90 * 24 * time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		a, err := attest.ParseObject(raw)
		if err != nil {
			t.Fatal(err)
		}
		opts := attest.VerifyOptions{
			Anchors:   ca.Anchors(),
			Freshness: attest.FreshnessForToken(token),
			PublicKey: k.Public(),
		}
		if err := a.Verify(opts); !errors.Is(err, attest.ErrChain) {
			t.Fatalf("Verify = %v, want ErrChain", err)
		}
		// The same attestation verifies at a time inside its window, so
		// the rejection was the clock and nothing else.
		opts.Now = func() time.Time { return start.Add(24 * time.Hour) }
		if err := a.Verify(opts); err != nil {
			t.Fatalf("Verify inside the window = %v", err)
		}
	})
	t.Run("MissingIntermediate", func(t *testing.T) {
		full, err := ca.Chain(attesttest.LeafOptions{
			Properties: attest.Properties{Freshness: attest.FreshnessForToken(token)},
			PublicKey:  k.Public(),
		})
		if err != nil {
			t.Fatal(err)
		}
		a, err := attest.ParseChain(full[:1])
		if err != nil {
			t.Fatal(err)
		}
		err = a.Verify(attest.VerifyOptions{
			Anchors:   ca.Anchors(),
			Freshness: attest.FreshnessForToken(token),
		})
		if !errors.Is(err, attest.ErrChain) {
			t.Fatalf("Verify = %v, want ErrChain", err)
		}
	})
	t.Run("DefaultAnchorsAreApple", func(t *testing.T) {
		// With no anchors configured the verifier trusts Apple alone, so a
		// test authority is rejected.
		raw, err := ca.ObjectForToken(token, macProperties(), k.Public())
		if err != nil {
			t.Fatal(err)
		}
		a, err := attest.ParseObject(raw)
		if err != nil {
			t.Fatal(err)
		}
		err = a.Verify(attest.VerifyOptions{
			Freshness: attest.FreshnessForToken(token),
			PublicKey: k.Public(),
		})
		if !errors.Is(err, attest.ErrChain) {
			t.Fatalf("Verify = %v, want ErrChain", err)
		}
	})
}

func TestUserEnrollmentHasNoIdentity(t *testing.T) {
	// Apple omits the serial number and the UDID for a user enrollment.
	// That is a genuine attestation of a key on real hardware, so it must
	// verify; whether it is good enough is the caller's policy.
	ca := newCA(t)
	k := key(t)
	raw, err := ca.ObjectForToken(token, attest.Properties{SEPOSVersion: "18.0"}, k.Public())
	if err != nil {
		t.Fatal(err)
	}
	a, err := attest.ParseObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	if a.Properties.Identified() {
		t.Fatalf("properties = %+v", a.Properties)
	}
	if err := a.Verify(attest.VerifyOptions{
		Anchors:   ca.Anchors(),
		Freshness: attest.FreshnessForToken(token),
		PublicKey: k.Public(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedExtensions(t *testing.T) {
	ca := newCA(t)
	k := key(t)
	cases := []struct {
		name string
		ext  pkix.Extension
		want error
	}{
		{
			"truncated integer",
			pkix.Extension{Id: attest.OIDSIPStatus, Value: []byte{0x02, 0x05}},
			attest.ErrExtension,
		},
		{
			"trailing bytes after integer",
			pkix.Extension{Id: attest.OIDKextsAllowed, Value: []byte{0x02, 0x01, 0x00, 0x00}},
			attest.ErrExtension,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := ca.Object(attesttest.LeafOptions{
				Properties: attest.Properties{Freshness: attest.FreshnessForToken(token)},
				PublicKey:  k.Public(),
				Extra:      []pkix.Extension{tc.ext},
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := attest.ParseObject(raw); !errors.Is(err, tc.want) {
				t.Fatalf("ParseObject = %v, want %v", err, tc.want)
			}
		})
	}
	t.Run("BlankIsAbsent", func(t *testing.T) {
		// Apple states that a property its servers cannot verify may come
		// back blank, so an empty value is missing data rather than a
		// fault.
		raw, err := ca.Object(attesttest.LeafOptions{
			Properties: attest.Properties{Freshness: attest.FreshnessForToken(token)},
			PublicKey:  k.Public(),
			Extra: []pkix.Extension{
				{Id: attest.OIDSIPStatus, Value: []byte{}},
				{Id: attest.OIDSerialNumber, Value: []byte{}},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		a, err := attest.ParseObject(raw)
		if err != nil {
			t.Fatal(err)
		}
		if a.Properties.SIPEnabled != nil || a.Properties.SerialNumber != "" {
			t.Fatalf("properties = %+v", a.Properties)
		}
	})
}

func TestAppleAnchors(t *testing.T) {
	got := attest.AppleAnchors()
	if len(got) != 1 {
		t.Fatalf("%d anchors", len(got))
	}
	want := "Apple Enterprise Attestation Root CA"
	if got[0].Subject.CommonName != want {
		t.Fatalf("subject = %q", got[0].Subject.CommonName)
	}
	if !got[0].IsCA {
		t.Fatal("anchor is not a CA")
	}
	// A caller that appends its own anchors must not reach the next caller.
	got = append(got, got[0])
	if len(attest.AppleAnchors()) != 1 {
		t.Fatal("the anchor list is shared")
	}
}

func TestLeafAndPublicKey(t *testing.T) {
	ca := newCA(t)
	k := key(t)
	raw, err := ca.ObjectForToken(token, macProperties(), k.Public())
	if err != nil {
		t.Fatal(err)
	}
	a, err := attest.ParseObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	if a.Leaf() != a.Chain[0] {
		t.Fatal("Leaf is not the first certificate")
	}
	attested, ok := a.PublicKey().(*ecdsa.PublicKey)
	if !ok || !attested.Equal(k.Public()) {
		t.Fatalf("PublicKey = %T", a.PublicKey())
	}
}

func TestAttestTestCAErrors(t *testing.T) {
	ca := newCA(t)
	if _, err := ca.Leaf(attesttest.LeafOptions{}); !errors.Is(err, attesttest.ErrCA) {
		t.Fatalf("no key = %v", err)
	}
	if _, err := ca.Chain(attesttest.LeafOptions{}); !errors.Is(err, attesttest.ErrCA) {
		t.Fatalf("no key = %v", err)
	}
	if _, err := ca.Object(attesttest.LeafOptions{}); !errors.Is(err, attesttest.ErrCA) {
		t.Fatalf("no key = %v", err)
	}
	if _, err := ca.ObjectForToken(token, attest.Properties{}, nil); !errors.Is(err, attesttest.ErrCA) {
		t.Fatalf("no key = %v", err)
	}
	// A chain from another authority, signed by a root we do not trust.
	k := key(t)
	other := newCA(t)
	leaf, err := ca.Leaf(attesttest.LeafOptions{
		Properties: attest.Properties{Freshness: attest.FreshnessForToken(token)},
		PublicKey:  k.Public(),
		Issuer:     other.Root,
		IssuerKey:  nil,
	})
	if err == nil {
		t.Fatalf("signing with no key produced %v", leaf.Subject)
	}
	if ca.Anchors()[0] != ca.Root {
		t.Fatal("Anchors is not the root")
	}
}

func FuzzParseObject(f *testing.F) {
	ca, err := attesttest.NewCA()
	if err != nil {
		f.Fatal(err)
	}
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		f.Fatal(err)
	}
	seed, err := ca.ObjectForToken(token, attest.Properties{SerialNumber: "S"}, k.Public())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte{0xa0})
	f.Add([]byte("not cbor at all"))
	f.Fuzz(func(t *testing.T, data []byte) {
		a, err := attest.ParseObject(data)
		if err != nil {
			return
		}
		// Anything accepted has a chain, a leaf, and a key, and always
		// fails verification against the real Apple anchors.
		if len(a.Chain) == 0 || a.Leaf() == nil {
			t.Fatal("accepted an attestation with no chain")
		}
		if a.Format != attest.FormatApple {
			t.Fatalf("accepted format %q", a.Format)
		}
		if err := a.Verify(attest.VerifyOptions{Freshness: []byte("x")}); err == nil {
			t.Fatal("fuzzed input verified against the Apple anchors")
		}
		_ = x509.MarshalPKIXPublicKey
	})
}
