package jose_test

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"encoding/asn1"
	"encoding/base64"
	json "encoding/json/v2"
	"io"
	"math/big"
	"sync"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/acme/jose"
)

// Shared keys. RSA generation is slow enough that the tests share one key
// rather than paying for it in every case.
var (
	p256Once sync.Once
	p256Key  *ecdsa.PrivateKey
	rsaOnce  sync.Once
	rsaKey   *rsa.PrivateKey
)

func testP256(t testing.TB) *ecdsa.PrivateKey {
	t.Helper()
	p256Once.Do(func() {
		k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic(err)
		}
		p256Key = k
	})
	return p256Key
}

func testRSA(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	rsaOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		rsaKey = k
	})
	return rsaKey
}

func mustEC(t testing.TB, curve elliptic.Curve) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(curve, rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	return k
}

// b64 is unpadded base64url, the only encoding this package accepts.
func b64(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

// jwsBody assembles a flattened JWS from already-encoded members, so that a
// test can produce a body no honest signer would.
func jwsBody(t testing.TB, members map[string]any) []byte {
	t.Helper()
	body, err := json.Marshal(members)
	if err != nil {
		t.Fatalf("encoding the test body: %v", err)
	}
	return body
}

// signedFor produces a valid JWS with a jwk header, for tests that only
// need something well formed.
func signedFor(t testing.TB, key crypto.Signer, payload []byte) []byte {
	t.Helper()
	jwk, err := jose.JWKFromPublic(key.Public())
	if err != nil {
		t.Fatalf("JWKFromPublic: %v", err)
	}
	body, err := jose.Sign(key, jose.Header{
		JWK:   jwk,
		Nonce: "bm9uY2U",
		URL:   "https://acme.example/acme/new-account",
	}, payload)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	return body
}

// reSign rebuilds a flattened body from a parsed JWS with a replacement
// signature, keeping the protected header and payload byte for byte.
func reSign(t testing.TB, parsed *jose.JWS, signature []byte, payload string) []byte {
	t.Helper()
	return jwsBody(t, map[string]any{
		"protected": string(parsed.Protected),
		"payload":   payload,
		"signature": base64.RawURLEncoding.EncodeToString(signature),
	})
}

// payloadOf recovers the encoded payload member from a body Sign produced.
func payloadOf(t testing.TB, body []byte) string {
	t.Helper()
	var members struct {
		Payload string `json:"payload"`
	}
	if err := json.Unmarshal(body, &members); err != nil {
		t.Fatalf("decoding the payload member: %v", err)
	}
	return members.Payload
}

// brokenSigner has a usable public key but a Sign that misbehaves, which is
// the only way to reach Sign's post-signature failure paths.
type brokenSigner struct {
	pub crypto.PublicKey
	sig []byte
	err error
}

func (b brokenSigner) Public() crypto.PublicKey { return b.pub }

func (b brokenSigner) Sign(io.Reader, []byte, crypto.SignerOpts) ([]byte, error) {
	return b.sig, b.err
}

// asn1Marshal writes the ECDSA SEQUENCE a crypto.Signer is expected to
// return, so that a test can hand Sign a well-formed but useless one.
func asn1Marshal(r, s *big.Int) ([]byte, error) {
	return asn1.Marshal(struct{ R, S *big.Int }{R: r, S: s}) //nolint:wrapcheck // test helper
}
