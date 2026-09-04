package jose_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/pki/acme/jose"
)

func TestAlgorithms(t *testing.T) {
	t.Parallel()
	got := jose.Algorithms()
	want := []string{jose.ES256, jose.ES384, jose.ES512, jose.RS256, jose.RS384, jose.RS512}
	if !slices.Equal(got, want) {
		t.Fatalf("Algorithms() = %v, want %v", got, want)
	}
	got[0] = "tampered"
	if jose.Algorithms()[0] != jose.ES256 {
		t.Fatal("Algorithms returned a slice aliasing package state")
	}
}

func TestSignParseVerifyRoundTrip(t *testing.T) {
	t.Parallel()
	rsaKey := testRSA(t)
	cases := []struct {
		name string
		alg  string
		key  crypto.Signer
	}{
		{"ES256 derived", "", mustEC(t, elliptic.P256())},
		{"ES256", jose.ES256, mustEC(t, elliptic.P256())},
		{"ES384", jose.ES384, mustEC(t, elliptic.P384())},
		{"ES512", jose.ES512, mustEC(t, elliptic.P521())},
		{"RS256 derived", "", rsaKey},
		{"RS256", jose.RS256, rsaKey},
		{"RS384", jose.RS384, rsaKey},
		{"RS512", jose.RS512, rsaKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			jwk, err := jose.JWKFromPublic(tc.key.Public())
			if err != nil {
				t.Fatalf("JWKFromPublic: %v", err)
			}
			header := jose.Header{
				Algorithm: tc.alg,
				JWK:       jwk,
				Nonce:     "oFvnlFP1wIhRlYS2jTaXbA",
				URL:       "https://acme.example/acme/new-order",
			}
			body, err := jose.Sign(tc.key, header, []byte(`{"identifiers":[]}`))
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			parsed, err := jose.Parse(body)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if tc.alg != "" && parsed.Header.Algorithm != tc.alg {
				t.Fatalf("alg %q, want %q", parsed.Header.Algorithm, tc.alg)
			}
			if parsed.Header.URL != header.URL || parsed.Header.Nonce != header.Nonce {
				t.Fatalf("header did not survive: %+v", parsed.Header)
			}
			if parsed.Header.JWK == nil || *parsed.Header.JWK != *jwk {
				t.Fatalf("jwk did not survive: %+v", parsed.Header.JWK)
			}
			if string(parsed.Payload) != `{"identifiers":[]}` {
				t.Fatalf("payload %q", parsed.Payload)
			}
			if parsed.PayloadIsEmpty() {
				t.Fatal("PayloadIsEmpty on a request with a payload")
			}
			if err := parsed.Verify(tc.key.Public()); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

func TestSignWithKeyID(t *testing.T) {
	t.Parallel()
	key := testP256(t)
	body, err := jose.Sign(key, jose.Header{
		KeyID: "https://acme.example/acme/acct/1",
		Nonce: "bm9uY2U",
		URL:   "https://acme.example/acme/order/1",
	}, nil)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	parsed, err := jose.Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Header.KeyID != "https://acme.example/acme/acct/1" || parsed.Header.JWK != nil {
		t.Fatalf("header %+v", parsed.Header)
	}
	// A nil payload is how a client writes POST-as-GET.
	if !parsed.PayloadIsEmpty() {
		t.Fatal("PayloadIsEmpty is false for an empty payload")
	}
	if err := parsed.Verify(key.Public()); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestSignRejects(t *testing.T) {
	t.Parallel()
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating an Ed25519 key: %v", err)
	}
	short, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generating a short RSA key: %v", err)
	}
	p256 := testP256(t)
	cases := []struct {
		name string
		key  crypto.Signer
		alg  string
		want error
	}{
		{"nil signer", nil, "", jose.ErrKey},
		{"unsupported key type", edKey, "", jose.ErrKey},
		{"unsupported curve", mustEC(t, elliptic.P224()), "", jose.ErrKey},
		{"short RSA key", short, "", jose.ErrKey},
		{"unknown alg", p256, "HS256", jose.ErrAlgorithm},
		{"alg for the wrong curve", p256, jose.ES384, jose.ErrAlgorithm},
		{"RSA alg with an EC key", p256, jose.RS256, jose.ErrAlgorithm},
		{"EC alg with an RSA key", testRSA(t), jose.ES256, jose.ErrAlgorithm},
		{"signer that fails", brokenSigner{pub: &p256.PublicKey, err: errAlwaysFails}, "", jose.ErrKey},
		{"signer returning junk", brokenSigner{pub: &p256.PublicKey, sig: []byte("not asn.1")}, "", jose.ErrSignature},
		{"signer returning trailing bytes", brokenSigner{
			pub: &p256.PublicKey,
			sig: append(asn1ECDSA(t, big.NewInt(1), big.NewInt(2)), 0x00),
		}, "", jose.ErrSignature},
		{"signer returning an oversized r", brokenSigner{
			pub: &p256.PublicKey,
			sig: asn1ECDSA(t, new(big.Int).Lsh(big.NewInt(1), 300), big.NewInt(2)),
		}, "", jose.ErrSignature},
		{"signer returning an oversized s", brokenSigner{
			pub: &p256.PublicKey,
			sig: asn1ECDSA(t, big.NewInt(1), new(big.Int).Lsh(big.NewInt(1), 300)),
		}, "", jose.ErrSignature},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := jose.Sign(tc.key, jose.Header{Algorithm: tc.alg, KeyID: "k", Nonce: "n", URL: "u"}, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

var errAlwaysFails = errors.New("test: the signer refuses")

// asn1ECDSA builds the SEQUENCE a crypto.Signer returns for ECDSA.
func asn1ECDSA(t testing.TB, r, s *big.Int) []byte {
	t.Helper()
	der, err := asn1Marshal(r, s)
	if err != nil {
		t.Fatalf("encoding an ASN.1 signature: %v", err)
	}
	return der
}

func TestParseRejectsBodyShape(t *testing.T) {
	t.Parallel()
	valid := signedFor(t, testP256(t), []byte(`{}`))
	var members map[string]string
	if err := json.Unmarshal(valid, &members); err != nil {
		t.Fatalf("decoding a valid body: %v", err)
	}

	cases := []struct {
		name string
		body []byte
		want error
	}{
		{"not JSON", []byte("{"), jose.ErrParse},
		{"not an object", []byte(`"a string"`), jose.ErrParse},
		{"general serialisation", jwsBody(t, map[string]any{
			"payload":    members["payload"],
			"signatures": []any{map[string]any{"protected": members["protected"], "signature": members["signature"]}},
		}), jose.ErrParse},
		{"unprotected header", jwsBody(t, map[string]any{
			"protected": members["protected"],
			"payload":   members["payload"],
			"signature": members["signature"],
			"header":    map[string]any{"kid": "sneaky"},
		}), jose.ErrParse},
		{"unknown member", jwsBody(t, map[string]any{
			"protected": members["protected"],
			"payload":   members["payload"],
			"signature": members["signature"],
			"extra":     "no",
		}), jose.ErrParse},
		{"missing protected", jwsBody(t, map[string]any{
			"payload":   members["payload"],
			"signature": members["signature"],
		}), jose.ErrParse},
		{"empty protected", jwsBody(t, map[string]any{
			"protected": "",
			"payload":   members["payload"],
			"signature": members["signature"],
		}), jose.ErrParse},
		{"detached payload", jwsBody(t, map[string]any{
			"protected": members["protected"],
			"signature": members["signature"],
		}), jose.ErrParse},
		{"missing signature", jwsBody(t, map[string]any{
			"protected": members["protected"],
			"payload":   members["payload"],
		}), jose.ErrParse},
		{"empty signature", jwsBody(t, map[string]any{
			"protected": members["protected"],
			"payload":   members["payload"],
			"signature": "",
		}), jose.ErrParse},
		{"protected is not base64url", jwsBody(t, map[string]any{
			"protected": "not base64!",
			"payload":   members["payload"],
			"signature": members["signature"],
		}), jose.ErrParse},
		{"padded protected", jwsBody(t, map[string]any{
			"protected": base64.URLEncoding.EncodeToString([]byte(`{"alg":"ES256","kid":"k"}`)),
			"payload":   members["payload"],
			"signature": members["signature"],
		}), jose.ErrParse},
		{"payload is not base64url", jwsBody(t, map[string]any{
			"protected": members["protected"],
			"payload":   "not base64!",
			"signature": members["signature"],
		}), jose.ErrParse},
		{"padded payload", jwsBody(t, map[string]any{
			"protected": members["protected"],
			"payload":   base64.URLEncoding.EncodeToString([]byte("abcde")),
			"signature": members["signature"],
		}), jose.ErrParse},
		{"signature is not base64url", jwsBody(t, map[string]any{
			"protected": members["protected"],
			"payload":   members["payload"],
			"signature": "not base64!",
		}), jose.ErrParse},
		{"padded signature", jwsBody(t, map[string]any{
			"protected": members["protected"],
			"payload":   members["payload"],
			"signature": base64.URLEncoding.EncodeToString([]byte("abcde")),
		}), jose.ErrParse},
		{"duplicate member", []byte(`{"protected":"e30","protected":"e30","payload":"","signature":"AA"}`), jose.ErrParse},
		{"protected is not JSON", jwsBody(t, map[string]any{
			"protected": b64("{"),
			"payload":   members["payload"],
			"signature": members["signature"],
		}), jose.ErrHeader},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := jose.Parse(tc.body); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseRejectsProtectedHeader(t *testing.T) {
	t.Parallel()
	jwk, err := jose.JWKFromPublic(&testP256(t).PublicKey)
	if err != nil {
		t.Fatalf("JWKFromPublic: %v", err)
	}
	base := map[string]any{
		"alg":   jose.ES256,
		"jwk":   jwk,
		"nonce": "bm9uY2U",
		"url":   "https://acme.example/acme/new-account",
	}
	// with copies base with the given members replaced; a nil value deletes.
	with := func(changes map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		for k, v := range changes {
			if v == nil {
				delete(out, k)
				continue
			}
			out[k] = v
		}
		return out
	}

	cases := []struct {
		name   string
		header map[string]any
		want   error
	}{
		{"alg none", with(map[string]any{"alg": "none"}), jose.ErrAlgorithm},
		{"MAC alg", with(map[string]any{"alg": "HS256"}), jose.ErrAlgorithm},
		{"PSS alg", with(map[string]any{"alg": "PS256"}), jose.ErrAlgorithm},
		{"missing alg", with(map[string]any{"alg": nil}), jose.ErrHeader},
		{"b64 false", with(map[string]any{"b64": false}), jose.ErrHeader},
		{"crit present", with(map[string]any{"crit": []string{"b64"}}), jose.ErrHeader},
		{"crit empty", with(map[string]any{"crit": []string{}}), jose.ErrHeader},
		{"jwk and kid", with(map[string]any{"kid": "https://acme.example/acct/1"}), jose.ErrHeader},
		{"neither jwk nor kid", with(map[string]any{"jwk": nil}), jose.ErrHeader},
		{"empty kid", with(map[string]any{"jwk": nil, "kid": ""}), jose.ErrHeader},
		{"missing url", with(map[string]any{"url": nil}), jose.ErrHeader},
		{"empty url", with(map[string]any{"url": ""}), jose.ErrHeader},
		{"missing nonce", with(map[string]any{"nonce": nil}), jose.ErrHeader},
		{"empty nonce", with(map[string]any{"nonce": ""}), jose.ErrHeader},
		{"malformed jwk", with(map[string]any{"jwk": map[string]any{"kty": "EC", "crv": "P-256", "x": "!", "y": "!"}}), jose.ErrKey},
		{"jwk of the wrong type", with(map[string]any{"jwk": "a string"}), jose.ErrHeader},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := json.Marshal(tc.header)
			if err != nil {
				t.Fatalf("encoding the header: %v", err)
			}
			body := jwsBody(t, map[string]any{
				"protected": base64.RawURLEncoding.EncodeToString(encoded),
				"payload":   "",
				"signature": "AAAA",
			})
			if _, err := jose.Parse(body); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

// TestParseAcceptsB64True and unknown header members: RFC 7515 says a
// receiver ignores header parameters it does not understand unless crit
// names them, and crit is refused outright.
func TestParseAcceptsUnknownHeaderMembers(t *testing.T) {
	t.Parallel()
	header, err := json.Marshal(map[string]any{
		"alg":   jose.ES256,
		"kid":   "https://acme.example/acme/acct/1",
		"nonce": "bm9uY2U",
		"url":   "https://acme.example/acme/order/1",
		"b64":   true,
		"typ":   "JOSE+JSON",
		"cty":   "application/jose+json",
	})
	if err != nil {
		t.Fatalf("encoding the header: %v", err)
	}
	body := jwsBody(t, map[string]any{
		"protected": base64.RawURLEncoding.EncodeToString(header),
		"payload":   "",
		"signature": "AAAA",
	})
	if _, err := jose.Parse(body); err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

func TestParseRejectsOversizedBody(t *testing.T) {
	t.Parallel()
	body := append(make([]byte, jose.MaxBody), '{')
	if _, err := jose.Parse(body); !errors.Is(err, jose.ErrParse) {
		t.Fatalf("got %v, want ErrParse", err)
	}
	if !strings.Contains(errString(jose.Parse(body)), "exceeds") {
		t.Fatal("the error did not mention the size limit")
	}
}

func errString(_ *jose.JWS, err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestParseKeepsRawProtected checks the property Verify depends on: the
// protected member is kept as received, not re-encoded from Header.
func TestParseKeepsRawProtected(t *testing.T) {
	t.Parallel()
	// Member order and spacing here are not what Sign would produce.
	header := `{ "url":"https://acme.example/x", "alg":"ES256", "nonce":"n", "kid":"k" }`
	encoded := base64.RawURLEncoding.EncodeToString([]byte(header))
	body := jwsBody(t, map[string]any{"protected": encoded, "payload": "", "signature": "AAAA"})
	parsed, err := jose.Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if string(parsed.Protected) != encoded {
		t.Fatalf("Protected = %q, want the raw member %q", parsed.Protected, encoded)
	}
}

func TestVerifyFailures(t *testing.T) {
	t.Parallel()
	key := testP256(t)
	body := signedFor(t, key, []byte(`{"a":1}`))
	parsed, err := jose.Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	other := mustEC(t, elliptic.P256())
	shortRSA, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generating a short RSA key: %v", err)
	}
	edPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating an Ed25519 key: %v", err)
	}

	t.Run("wrong key", func(t *testing.T) {
		t.Parallel()
		if err := parsed.Verify(&other.PublicKey); !errors.Is(err, jose.ErrSignature) {
			t.Fatalf("got %v, want ErrSignature", err)
		}
	})
	t.Run("key of the wrong family", func(t *testing.T) {
		t.Parallel()
		if err := parsed.Verify(&testRSA(t).PublicKey); !errors.Is(err, jose.ErrAlgorithm) {
			t.Fatalf("got %v, want ErrAlgorithm", err)
		}
	})
	t.Run("key of an unknown type", func(t *testing.T) {
		t.Parallel()
		if err := parsed.Verify(edPub); !errors.Is(err, jose.ErrAlgorithm) {
			t.Fatalf("got %v, want ErrAlgorithm", err)
		}
	})
	t.Run("key on the wrong curve", func(t *testing.T) {
		t.Parallel()
		p384 := mustEC(t, elliptic.P384())
		if err := parsed.Verify(&p384.PublicKey); !errors.Is(err, jose.ErrAlgorithm) {
			t.Fatalf("got %v, want ErrAlgorithm", err)
		}
	})
	t.Run("key on an unsupported curve", func(t *testing.T) {
		t.Parallel()
		p224 := mustEC(t, elliptic.P224())
		if err := parsed.Verify(&p224.PublicKey); !errors.Is(err, jose.ErrKey) {
			t.Fatalf("got %v, want ErrKey", err)
		}
	})
	t.Run("zero value", func(t *testing.T) {
		t.Parallel()
		if err := (&jose.JWS{}).Verify(&key.PublicKey); !errors.Is(err, jose.ErrParse) {
			t.Fatalf("got %v, want ErrParse", err)
		}
		var nilJWS *jose.JWS
		if err := nilJWS.Verify(&key.PublicKey); !errors.Is(err, jose.ErrParse) {
			t.Fatalf("got %v, want ErrParse", err)
		}
	})
	t.Run("alg changed after parsing", func(t *testing.T) {
		t.Parallel()
		mutated := *parsed
		mutated.Header.Algorithm = "HS256"
		if err := mutated.Verify(&key.PublicKey); !errors.Is(err, jose.ErrAlgorithm) {
			t.Fatalf("got %v, want ErrAlgorithm", err)
		}
	})
	t.Run("tampered payload", func(t *testing.T) {
		t.Parallel()
		tampered := reSign(t, parsed, parsed.Signature, b64(`{"a":2}`))
		reparsed, err := jose.Parse(tampered)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := reparsed.Verify(&key.PublicKey); !errors.Is(err, jose.ErrSignature) {
			t.Fatalf("got %v, want ErrSignature", err)
		}
	})
	t.Run("short RSA key", func(t *testing.T) {
		t.Parallel()
		rsaBody := signedFor(t, testRSA(t), []byte(`{}`))
		rsaParsed, err := jose.Parse(rsaBody)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := rsaParsed.Verify(&shortRSA.PublicKey); !errors.Is(err, jose.ErrKey) {
			t.Fatalf("got %v, want ErrKey", err)
		}
	})
	t.Run("RSA alg with an EC key", func(t *testing.T) {
		t.Parallel()
		rsaBody := signedFor(t, testRSA(t), []byte(`{}`))
		rsaParsed, err := jose.Parse(rsaBody)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		if err := rsaParsed.Verify(&key.PublicKey); !errors.Is(err, jose.ErrAlgorithm) {
			t.Fatalf("got %v, want ErrAlgorithm", err)
		}
	})
	t.Run("wrong RSA key", func(t *testing.T) {
		t.Parallel()
		rsaBody := signedFor(t, testRSA(t), []byte(`{}`))
		rsaParsed, err := jose.Parse(rsaBody)
		if err != nil {
			t.Fatalf("Parse: %v", err)
		}
		otherRSA, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Fatalf("generating an RSA key: %v", err)
		}
		if err := rsaParsed.Verify(&otherRSA.PublicKey); !errors.Is(err, jose.ErrSignature) {
			t.Fatalf("got %v, want ErrSignature", err)
		}
	})
}

// ecdsaJWS builds a P-256 JWS whose r and s each have at least the given
// number of leading zero bytes, then returns a body carrying the signature
// with exactly those bytes removed: the shape some Apple ACME clients emit.
func ecdsaJWS(t testing.TB, key *ecdsa.PrivateKey, trimR, trimS int) []byte {
	t.Helper()
	jwk, err := jose.JWKFromPublic(&key.PublicKey)
	if err != nil {
		t.Fatalf("JWKFromPublic: %v", err)
	}
	header, err := json.Marshal(map[string]any{
		"alg":   jose.ES256,
		"jwk":   jwk,
		"nonce": "bm9uY2U",
		"url":   "https://acme.example/acme/new-account",
	})
	if err != nil {
		t.Fatalf("encoding the header: %v", err)
	}
	protected := base64.RawURLEncoding.EncodeToString(header)
	payload := b64(`{"termsOfServiceAgreed":true}`)
	digest := sha256.Sum256([]byte(protected + "." + payload))

	const budget = 4_000_000
	for range budget {
		r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
		if err != nil {
			t.Fatalf("ecdsa.Sign: %v", err)
		}
		rb := r.FillBytes(make([]byte, 32))
		sb := s.FillBytes(make([]byte, 32))
		if !leadingZeros(rb, trimR) || !leadingZeros(sb, trimS) {
			continue
		}
		sig := slices.Concat(rb[trimR:], sb[trimS:])
		return jwsBody(t, map[string]any{
			"protected": protected,
			"payload":   payload,
			"signature": base64.RawURLEncoding.EncodeToString(sig),
		})
	}
	t.Fatalf("no signature with %d and %d leading zero bytes within %d attempts", trimR, trimS, budget)
	return nil
}

func leadingZeros(b []byte, n int) bool {
	for i := range n {
		if b[i] != 0 {
			return false
		}
	}
	return true
}

// TestVerifyShortECDSASignature covers the interoperability case described
// on candidateSplits: a client that encodes r and s as minimal big-endian
// integers rather than at the fixed width RFC 7515 section 3.4 requires.
func TestVerifyShortECDSASignature(t *testing.T) {
	t.Parallel()
	key := testP256(t)
	cases := []struct {
		name         string
		trimR, trimS int
		wantLen      int
	}{
		{"r missing a leading zero", 1, 0, 63},
		{"s missing a leading zero", 0, 1, 63},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parsed, err := jose.Parse(ecdsaJWS(t, key, tc.trimR, tc.trimS))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(parsed.Signature) != tc.wantLen {
				t.Fatalf("signature is %d bytes, want %d", len(parsed.Signature), tc.wantLen)
			}
			if err := parsed.Verify(&key.PublicKey); err != nil {
				t.Fatalf("Verify: %v", err)
			}
		})
	}
}

// TestVerifyShortECDSASignatureBothTrimmed is the two-byte deficit, the case
// step-ca guesses at explicitly: one leading zero dropped from each of r and
// s. Finding such a signature takes about 65536 attempts.
func TestVerifyShortECDSASignatureBothTrimmed(t *testing.T) {
	t.Parallel()
	key := testP256(t)
	parsed, err := jose.Parse(ecdsaJWS(t, key, 1, 1))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(parsed.Signature) != 62 {
		t.Fatalf("signature is %d bytes, want 62", len(parsed.Signature))
	}
	if err := parsed.Verify(&key.PublicKey); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerifyRejectsMalformedECDSASignatures(t *testing.T) {
	t.Parallel()
	key := testP256(t)
	body := signedFor(t, key, []byte(`{"a":1}`))
	parsed, err := jose.Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	payload := payloadOf(t, body)

	cases := []struct {
		name string
		sig  []byte
	}{
		{"a byte too long", append(slices.Clone(parsed.Signature), 0x00)},
		{"truncated by one, not a dropped zero", parsed.Signature[:63]},
		{"random and short", randomBytes(t, 63)},
		{"far too short", randomBytes(t, 32)},
		{"empty after decoding is rejected earlier, so one byte", randomBytes(t, 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reparsed, err := jose.Parse(reSign(t, parsed, tc.sig, payload))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if err := reparsed.Verify(&key.PublicKey); !errors.Is(err, jose.ErrSignature) {
				t.Fatalf("got %v, want ErrSignature", err)
			}
		})
	}
}

func randomBytes(t testing.TB, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("reading random bytes: %v", err)
	}
	return b
}
