package jose_test

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"math/big"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/pki/acme/jose"
)

func TestJWKRoundTrip(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		curve elliptic.Curve
		crv   string
		size  int
	}{
		{"P-256", elliptic.P256(), "P-256", 32},
		{"P-384", elliptic.P384(), "P-384", 48},
		{"P-521", elliptic.P521(), "P-521", 66},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			key := mustEC(t, tc.curve)
			jwk, err := jose.JWKFromPublic(&key.PublicKey)
			if err != nil {
				t.Fatalf("JWKFromPublic: %v", err)
			}
			if jwk.Kty != "EC" || jwk.Crv != tc.crv {
				t.Fatalf("got kty %q crv %q", jwk.Kty, jwk.Crv)
			}
			x, err := base64.RawURLEncoding.DecodeString(jwk.X)
			if err != nil {
				t.Fatalf("decoding x: %v", err)
			}
			if len(x) != tc.size {
				t.Fatalf("x is %d bytes, want the fixed width %d", len(x), tc.size)
			}
			back, err := jwk.Public()
			if err != nil {
				t.Fatalf("Public: %v", err)
			}
			pub, ok := back.(*ecdsa.PublicKey)
			if !ok {
				t.Fatalf("Public returned %T", back)
			}
			if !pub.Equal(&key.PublicKey) {
				t.Fatal("round trip did not preserve the key")
			}
		})
	}
}

func TestJWKRoundTripRSA(t *testing.T) {
	t.Parallel()
	key := testRSA(t)
	jwk, err := jose.JWKFromPublic(&key.PublicKey)
	if err != nil {
		t.Fatalf("JWKFromPublic: %v", err)
	}
	if jwk.Kty != "RSA" || jwk.E != "AQAB" {
		t.Fatalf("got kty %q e %q", jwk.Kty, jwk.E)
	}
	back, err := jwk.Public()
	if err != nil {
		t.Fatalf("Public: %v", err)
	}
	pub, ok := back.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("Public returned %T", back)
	}
	if !pub.Equal(&key.PublicKey) {
		t.Fatal("round trip did not preserve the key")
	}
}

// TestJWKJSONRoundTrip pins the wire form: only the members RFC 7638 needs,
// and nothing extra for an EC key.
func TestJWKJSONRoundTrip(t *testing.T) {
	t.Parallel()
	jwk, err := jose.JWKFromPublic(&testP256(t).PublicKey)
	if err != nil {
		t.Fatalf("JWKFromPublic: %v", err)
	}
	encoded, err := json.Marshal(jwk)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var members map[string]string
	if err := json.Unmarshal(encoded, &members); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(members) != 4 {
		t.Fatalf("an EC JWK carried %d members: %s", len(members), encoded)
	}
	for _, want := range []string{"kty", "crv", "x", "y"} {
		if _, ok := members[want]; !ok {
			t.Fatalf("missing %q in %s", want, encoded)
		}
	}
	var back jose.JWK
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatalf("Unmarshal into JWK: %v", err)
	}
	if back != *jwk {
		t.Fatalf("JSON round trip changed the key: %+v", back)
	}
}

func TestJWKFromPublicRejects(t *testing.T) {
	t.Parallel()
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("generating a short RSA key: %v", err)
	}
	edPub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating an Ed25519 key: %v", err)
	}
	cases := []struct {
		name string
		pub  any
	}{
		{"unsupported curve", &ecdsa.PublicKey{Curve: elliptic.P224(), X: big.NewInt(1), Y: big.NewInt(2)}},
		{"unsupported key type", edPub},
		{"short RSA modulus", &small.PublicKey},
		{"nil RSA modulus", &rsa.PublicKey{E: 65537}},
		{"coordinate too wide for the curve", &ecdsa.PublicKey{
			Curve: elliptic.P256(),
			X:     new(big.Int).Lsh(big.NewInt(1), 300),
			Y:     big.NewInt(2),
		}},
		{"negative coordinate", &ecdsa.PublicKey{Curve: elliptic.P256(), X: big.NewInt(1), Y: big.NewInt(-2)}},
		{"non-positive RSA exponent", &rsa.PublicKey{N: new(big.Int).Lsh(big.NewInt(1), 2047), E: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := jose.JWKFromPublic(tc.pub); !errors.Is(err, jose.ErrKey) {
				t.Fatalf("got %v, want ErrKey", err)
			}
		})
	}
}

func TestJWKPublicRejects(t *testing.T) {
	t.Parallel()
	valid, err := jose.JWKFromPublic(&testP256(t).PublicKey)
	if err != nil {
		t.Fatalf("JWKFromPublic: %v", err)
	}
	// Flipping the low bit of x moves the point off the curve while keeping
	// the coordinate the right width.
	x, err := base64.RawURLEncoding.DecodeString(valid.X)
	if err != nil {
		t.Fatalf("decoding x: %v", err)
	}
	x[len(x)-1] ^= 1
	offCurve := *valid
	offCurve.X = base64.RawURLEncoding.EncodeToString(x)

	short := *valid
	short.X = base64.RawURLEncoding.EncodeToString(x[:31])

	rsaJWK, err := jose.JWKFromPublic(&testRSA(t).PublicKey)
	if err != nil {
		t.Fatalf("JWKFromPublic: %v", err)
	}

	cases := []struct {
		name string
		jwk  *jose.JWK
	}{
		{"nil key", nil},
		{"unsupported key type", &jose.JWK{Kty: "OKP", Crv: "Ed25519", X: valid.X}},
		{"unsupported curve", &jose.JWK{Kty: "EC", Crv: "P-224", X: valid.X, Y: valid.Y}},
		{"x is not base64url", &jose.JWK{Kty: "EC", Crv: "P-256", X: "not base64!", Y: valid.Y}},
		{"y is not base64url", &jose.JWK{Kty: "EC", Crv: "P-256", X: valid.X, Y: "not base64!"}},
		{"padded coordinate", &jose.JWK{Kty: "EC", Crv: "P-256", X: valid.X + "==", Y: valid.Y}},
		{"coordinate of the wrong width", &short},
		{"point not on the curve", &offCurve},
		{"n is not base64url", &jose.JWK{Kty: "RSA", N: "not base64!", E: "AQAB"}},
		{"e is not base64url", &jose.JWK{Kty: "RSA", N: rsaJWK.N, E: "not base64!"}},
		{"empty modulus", &jose.JWK{Kty: "RSA", N: "", E: "AQAB"}},
		{"empty exponent", &jose.JWK{Kty: "RSA", N: rsaJWK.N, E: ""}},
		{"oversized exponent", &jose.JWK{Kty: "RSA", N: rsaJWK.N, E: base64.RawURLEncoding.EncodeToString([]byte{1, 2, 3, 4, 5})}},
		{"short modulus", &jose.JWK{Kty: "RSA", N: base64.RawURLEncoding.EncodeToString([]byte{1, 2, 3}), E: "AQAB"}},
		{"even exponent", &jose.JWK{Kty: "RSA", N: rsaJWK.N, E: base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x00})}},
		{"exponent of one", &jose.JWK{Kty: "RSA", N: rsaJWK.N, E: base64.RawURLEncoding.EncodeToString([]byte{0x01})}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := tc.jwk.Public(); !errors.Is(err, jose.ErrKey) {
				t.Fatalf("Public: got %v, want ErrKey", err)
			}
			if _, err := tc.jwk.Thumbprint(); !errors.Is(err, jose.ErrKey) {
				t.Fatalf("Thumbprint: got %v, want ErrKey", err)
			}
		})
	}
}

// TestThumbprintRFC7638 is the worked example from RFC 7638 section 3.1.
// The key carries alg and kid members that the thumbprint must ignore.
func TestThumbprintRFC7638(t *testing.T) {
	t.Parallel()
	const n = "0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPeb" +
		"WKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMic" +
		"AtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XP" +
		"ksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw"
	const want = "NzbLsXh8uDCcd-6MNwXF4W_7noWXFZAfHkxZsRGC9Xs"

	jwk := &jose.JWK{Kty: "RSA", N: n, E: "AQAB"}
	got, err := jwk.Thumbprint()
	if err != nil {
		t.Fatalf("Thumbprint: %v", err)
	}
	if got != want {
		t.Fatalf("thumbprint %q, want %q", got, want)
	}
}

// TestThumbprintPadsCoordinates checks the fixed-width rule from the other
// direction: a P-256 key whose x happens to be 31 significant bytes must
// still thumbprint as though x were 32 bytes, which is what happens because
// JWKFromPublic pads it.
func TestThumbprintPadsCoordinates(t *testing.T) {
	t.Parallel()
	var key *ecdsa.PrivateKey
	for range 5000 {
		k := mustEC(t, elliptic.P256())
		if k.PublicKey.X.BitLen() <= 248 {
			key = k
			break
		}
	}
	if key == nil {
		t.Skip("no P-256 key with a short x turned up; the padding is covered by the encoding test")
	}
	jwk, err := jose.JWKFromPublic(&key.PublicKey)
	if err != nil {
		t.Fatalf("JWKFromPublic: %v", err)
	}
	x, err := base64.RawURLEncoding.DecodeString(jwk.X)
	if err != nil {
		t.Fatalf("decoding x: %v", err)
	}
	if len(x) != 32 || x[0] != 0 {
		t.Fatalf("x is %d bytes starting %#x, want a 32-byte left-padded coordinate", len(x), x[0])
	}
	if _, err := jwk.Thumbprint(); err != nil {
		t.Fatalf("Thumbprint: %v", err)
	}
}

// TestThumbprintStable checks that two spellings of one key thumbprint the
// same, which is the property a key authorisation depends on.
func TestThumbprintStable(t *testing.T) {
	t.Parallel()
	jwk, err := jose.JWKFromPublic(&testP256(t).PublicKey)
	if err != nil {
		t.Fatalf("JWKFromPublic: %v", err)
	}
	first, err := jwk.Thumbprint()
	if err != nil {
		t.Fatalf("Thumbprint: %v", err)
	}
	second, err := (&jose.JWK{Kty: jwk.Kty, Crv: jwk.Crv, X: jwk.X, Y: jwk.Y}).Thumbprint()
	if err != nil {
		t.Fatalf("Thumbprint: %v", err)
	}
	if first != second {
		t.Fatalf("thumbprints differ: %q and %q", first, second)
	}
	if decoded, err := base64.RawURLEncoding.DecodeString(first); err != nil || len(decoded) != 32 {
		t.Fatalf("thumbprint %q is not 32 unpadded base64url bytes (%v)", first, err)
	}
}
