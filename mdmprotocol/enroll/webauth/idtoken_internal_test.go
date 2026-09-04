package webauth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"
)

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func ecJWK(t *testing.T, kid string, key *ecdsa.PrivateKey) map[string]any {
	t.Helper()
	return map[string]any{"kty": "EC", "crv": "P-256", "kid": kid, "x": b64(key.X.FillBytes(make([]byte, 32))), "y": b64(key.Y.FillBytes(make([]byte, 32)))}
}

func signES256(t *testing.T, key *ecdsa.PrivateKey, header, claims map[string]any) string {
	t.Helper()
	input := b64(mustJSON(t, header)) + "." + b64(mustJSON(t, claims))
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + b64(append(r.FillBytes(make([]byte, 32)), s.FillBytes(make([]byte, 32))...))
}

func TestParseJWKS(t *testing.T) {
	t.Parallel()
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	short, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	rsaJWK := func(key *rsa.PrivateKey, e []byte) map[string]any {
		return map[string]any{"kty": "RSA", "kid": "r", "n": b64(key.N.Bytes()), "e": b64(e)}
	}
	good := mustJSON(t, map[string]any{"keys": []map[string]any{
		ecJWK(t, "e", ec), rsaJWK(rs, big.NewInt(int64(rs.E)).Bytes()),
		{"kty": "EC", "crv": "P-256", "kid": "enc", "use": "enc", "x": "AA", "y": "AA"},
		{"kty": "EC", "crv": "P-384", "kid": "p384"},
		{"kty": "RSA", "kid": "ps", "alg": "PS256", "n": "AA", "e": "AQAB"},
		{"kty": "oct", "kid": "hmac"},
	}})
	keys, err := parseJWKS(good)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].alg != algES256 || keys[0].kid != "e" || keys[1].alg != algRS256 || keys[1].kid != "r" {
		t.Fatalf("keys %+v", keys)
	}
	if got := selectKeys(keys, "", algRS256); len(got) != 1 || got[0].kid != "r" {
		t.Fatalf("select without kid: %+v", got)
	}
	if got := selectKeys(keys, "e", algRS256); len(got) != 0 {
		t.Fatalf("select wrong alg: %+v", got)
	}
	x, y := b64(ec.X.FillBytes(make([]byte, 32))), b64(ec.Y.FillBytes(make([]byte, 32)))
	for name, set := range map[string]any{
		"notJSON":     "nope",
		"empty":       map[string]any{"keys": []any{}},
		"badX":        map[string]any{"keys": []map[string]any{{"kty": "EC", "crv": "P-256", "x": "!", "y": y}}},
		"badY":        map[string]any{"keys": []map[string]any{{"kty": "EC", "crv": "P-256", "x": x, "y": "!"}}},
		"offCurve":    map[string]any{"keys": []map[string]any{{"kty": "EC", "crv": "P-256", "x": x, "y": x}}},
		"badN":        map[string]any{"keys": []map[string]any{{"kty": "RSA", "n": "!", "e": "AQAB"}}},
		"badE":        map[string]any{"keys": []map[string]any{{"kty": "RSA", "n": "AQAB", "e": "!"}}},
		"longE":       map[string]any{"keys": []map[string]any{{"kty": "RSA", "n": "AQAB", "e": b64([]byte{1, 2, 3, 4, 5})}}},
		"emptyE":      map[string]any{"keys": []map[string]any{{"kty": "RSA", "n": "AQAB", "e": ""}}},
		"shortRSA":    map[string]any{"keys": []map[string]any{rsaJWK(short, []byte{1, 0, 1})}},
		"evenE":       map[string]any{"keys": []map[string]any{rsaJWK(rs, []byte{1, 0, 0})}},
		"tinyOddE":    map[string]any{"keys": []map[string]any{rsaJWK(rs, []byte{1})}},
		"onlySkipped": map[string]any{"keys": []map[string]any{{"kty": "oct"}}},
	} {
		var body []byte
		if s, ok := set.(string); ok {
			body = []byte(s)
		} else {
			body = mustJSON(t, set)
		}
		if _, err := parseJWKS(body); !errors.Is(err, ErrJWK) {
			t.Fatalf("%s: err %v", name, err)
		}
	}
}

func TestVerifyIDToken(t *testing.T) {
	t.Parallel()
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rs, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keys := []verificationKey{{kid: "e", alg: algES256, key: &ec.PublicKey}, {kid: "r", alg: algRS256, key: &rs.PublicKey}}
	lookup := func(kid, alg string) []verificationKey { return selectKeys(keys, kid, alg) }
	now := time.Unix(1_700_000_000, 0)
	checks := idTokenChecks{issuer: "https://idp.example.com", clientID: "client", nonce: "n1", now: now, skew: time.Minute}
	base := func() map[string]any {
		return map[string]any{"iss": "https://idp.example.com", "sub": "s", "aud": "client", "exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nonce": "n1"}
	}
	header := map[string]any{"alg": algES256, "kid": "e", "typ": "JWT"}

	claims, err := verifyIDToken(signES256(t, ec, header, base()), lookup, checks)
	if err != nil || claims.Subject != "s" {
		t.Fatalf("valid: %+v %v", claims, err)
	}
	// aud as an array and iat within skew; groups with a non-string entry.
	c := base()
	c["aud"] = []any{"other", "client"}
	c["iat"] = now.Add(30 * time.Second).Unix()
	c["groups"] = []any{"a", 1, "b"}
	c["email_verified"] = "yes"
	claims, err = verifyIDToken(signES256(t, ec, header, c), lookup, checks)
	if err != nil || len(claims.Groups) != 2 || claims.EmailVerified {
		t.Fatalf("aud array: %+v %v", claims, err)
	}
	// A token without a kid matches any key of its algorithm.
	if _, err := verifyIDToken(signES256(t, ec, map[string]any{"alg": algES256}, base()), lookup, checks); err != nil {
		t.Fatalf("no kid: %v", err)
	}

	mutate := func(fn func(map[string]any)) string {
		c := base()
		fn(c)
		return signES256(t, ec, header, c)
	}
	valid := signES256(t, ec, header, base())
	parts := strings.Split(valid, ".")
	cases := map[string]string{
		"twoSegments":   parts[0] + "." + parts[1],
		"badHeaderB64":  "!." + parts[1] + "." + parts[2],
		"badHeaderJSON": b64([]byte("{")) + "." + parts[1] + "." + parts[2],
		"badPayloadB64": parts[0] + ".!." + parts[2],
		"badSigB64":     parts[0] + "." + parts[1] + ".!",
		"algNone":       b64(mustJSON(t, map[string]any{"alg": "none"})) + "." + parts[1] + ".",
		"algHS256":      b64(mustJSON(t, map[string]any{"alg": "HS256", "kid": "e"})) + "." + parts[1] + "." + parts[2],
		"unknownKid":    signES256(t, ec, map[string]any{"alg": algES256, "kid": "zz"}, base()),
		"wrongKey":      signES256(t, other, header, base()),
		"algMismatch":   signES256(t, ec, map[string]any{"alg": algRS256, "kid": "r"}, base()),
		"shortSig":      parts[0] + "." + parts[1] + "." + b64([]byte{1, 2, 3}),
		"badClaims":     parts[0] + "." + b64([]byte("[]")) + "." + parts[2],
		"wrongIss":      mutate(func(c map[string]any) { c["iss"] = "https://evil.example.com" }),
		"wrongAud":      mutate(func(c map[string]any) { c["aud"] = "other" }),
		"audArrayMiss":  mutate(func(c map[string]any) { c["aud"] = []any{"other", 1} }),
		"audNumber":     mutate(func(c map[string]any) { c["aud"] = 1 }),
		"noExp":         mutate(func(c map[string]any) { delete(c, "exp") }),
		"expired":       mutate(func(c map[string]any) { c["exp"] = now.Add(-2 * time.Minute).Unix() }),
		"noIat":         mutate(func(c map[string]any) { delete(c, "iat") }),
		"futureIat":     mutate(func(c map[string]any) { c["iat"] = now.Add(2 * time.Minute).Unix() }),
		"noSub":         mutate(func(c map[string]any) { delete(c, "sub") }),
		"noNonce":       mutate(func(c map[string]any) { delete(c, "nonce") }),
		"wrongNonce":    mutate(func(c map[string]any) { c["nonce"] = "n2" }),
	}
	for name, raw := range cases {
		if _, err := verifyIDToken(raw, lookup, checks); !errors.Is(err, ErrIDToken) {
			t.Fatalf("%s: err %v", name, err)
		}
	}
	// Within skew the token is still accepted.
	c = base()
	c["exp"] = now.Add(-30 * time.Second).Unix()
	if _, err := verifyIDToken(signES256(t, ec, header, c), lookup, checks); err != nil {
		t.Fatalf("within skew: %v", err)
	}
	// The wrong signature under the RS256 key must not verify either.
	input := parts[0] + "." + parts[1]
	if err := verifySignature(algRS256, input, make([]byte, 256), keys); !errors.Is(err, ErrIDToken) {
		t.Fatalf("rs256 garbage: %v", err)
	}
}
