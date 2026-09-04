package jose_test

import (
	"encoding/base64"
	"slices"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/pki/acme/jose"
)

// FuzzParse feeds arbitrary bytes to Parse. Parse is the first thing an ACME
// server runs over an unauthenticated request body, so the contract it has
// to keep is that it never panics, and that whatever it does accept carries
// the two things every later stage assumes: the raw protected header the
// signature is computed over, and an algorithm we know how to verify.
func FuzzParse(f *testing.F) {
	key := testP256(f)
	f.Add(signedFor(f, key, []byte(`{"termsOfServiceAgreed":true}`)))
	f.Add(signedFor(f, key, nil))
	f.Add([]byte(`{"protected":"","payload":"","signature":""}`))
	f.Add([]byte(`{"payload":"","signatures":[]}`))
	f.Add([]byte(`{"protected":"e30","payload":"","signature":"AA","header":{}}`))
	f.Add([]byte(`{"protected":"` + b64(`{"alg":"none","kid":"k","nonce":"n","url":"u"}`) + `","payload":"","signature":"AA"}`))
	f.Add([]byte("{"))
	f.Add([]byte(""))

	f.Fuzz(func(t *testing.T, body []byte) {
		parsed, err := jose.Parse(body)
		if err != nil {
			if parsed != nil {
				t.Fatalf("Parse returned both a JWS and the error %v", err)
			}
			return
		}
		if len(parsed.Protected) == 0 {
			t.Fatal("accepted a JWS with an empty protected header")
		}
		if _, err := base64.RawURLEncoding.DecodeString(string(parsed.Protected)); err != nil {
			t.Fatalf("accepted a protected header that is not unpadded base64url: %v", err)
		}
		if !slices.Contains(jose.Algorithms(), parsed.Header.Algorithm) {
			t.Fatalf("accepted the unknown algorithm %q", parsed.Header.Algorithm)
		}
		if parsed.Header.URL == "" || parsed.Header.Nonce == "" {
			t.Fatalf("accepted a header without a url or nonce: %+v", parsed.Header)
		}
		if (parsed.Header.KeyID == "") == (parsed.Header.JWK == nil) {
			t.Fatalf("accepted a header with both or neither of jwk and kid: %+v", parsed.Header)
		}
		if len(parsed.Signature) == 0 {
			t.Fatal("accepted a JWS with an empty signature")
		}
		// Verification runs on whatever shape the signature arrived in. The
		// result is not asserted on, because a body the fuzzer happened to
		// make verify would be a discovery rather than a failure; what is
		// asserted is that it returns instead of panicking.
		_ = parsed.Verify(&key.PublicKey)
	})
}
