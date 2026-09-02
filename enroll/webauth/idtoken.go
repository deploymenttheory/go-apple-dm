package webauth

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Claims is what the id_token said about the person, typed for the
// common OIDC claims with the full payload in Raw.
type Claims struct {
	Subject           string
	Email             string
	EmailVerified     bool
	Name              string
	PreferredUsername string
	Groups            []string
	// Raw holds every claim as decoded from the payload.
	Raw map[string]any
}

// ErrIDToken is wrapped by every id_token verification failure.
var ErrIDToken = errors.New("webauth: id_token")

// Signing algorithms accepted.
const (
	algES256 = "ES256"
	algRS256 = "RS256"
)

// minRSABits rejects RSA keys shorter than 2048 bits.
const minRSABits = 2048

// jwk is a JSON Web Key as served by the provider.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

// verificationKey is one usable public key.
type verificationKey struct {
	kid string
	alg string // ES256 or RS256, derived from the key type
	key crypto.PublicKey
}

// ErrJWK is wrapped by JWKS parsing failures.
var ErrJWK = errors.New("webauth: jwks")

// parseJWKS keeps the keys this package can use and skips the rest.
func parseJWKS(body []byte) ([]verificationKey, error) {
	var set jwkSet
	if err := json.Unmarshal(body, &set); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrJWK, err)
	}
	var keys []verificationKey
	for _, k := range set.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue
		}
		switch {
		case k.Kty == "EC" && k.Crv == "P-256" && (k.Alg == "" || k.Alg == algES256):
			pub, err := ecKey(k)
			if err != nil {
				return nil, err
			}
			keys = append(keys, verificationKey{kid: k.Kid, alg: algES256, key: pub})
		case k.Kty == "RSA" && (k.Alg == "" || k.Alg == algRS256):
			pub, err := rsaKey(k)
			if err != nil {
				return nil, err
			}
			keys = append(keys, verificationKey{kid: k.Kid, alg: algRS256, key: pub})
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w: no ES256 or RS256 keys", ErrJWK)
	}
	return keys, nil
}

func ecKey(k jwk) (*ecdsa.PublicKey, error) {
	x, err := base64.RawURLEncoding.DecodeString(k.X)
	if err != nil {
		return nil, fmt.Errorf("%w: key %q: x: %w", ErrJWK, k.Kid, err)
	}
	y, err := base64.RawURLEncoding.DecodeString(k.Y)
	if err != nil {
		return nil, fmt.Errorf("%w: key %q: y: %w", ErrJWK, k.Kid, err)
	}
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	if _, err := pub.ECDH(); err != nil {
		return nil, fmt.Errorf("%w: key %q: not on P-256: %w", ErrJWK, k.Kid, err)
	}
	return pub, nil
}

func rsaKey(k jwk) (*rsa.PublicKey, error) {
	n, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("%w: key %q: n: %w", ErrJWK, k.Kid, err)
	}
	e, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("%w: key %q: e: %w", ErrJWK, k.Kid, err)
	}
	if len(e) == 0 || len(e) > 4 {
		return nil, fmt.Errorf("%w: key %q: exponent length %d", ErrJWK, k.Kid, len(e))
	}
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	if pub.N.BitLen() < minRSABits {
		return nil, fmt.Errorf("%w: key %q: %d-bit RSA key is too short", ErrJWK, k.Kid, pub.N.BitLen())
	}
	if pub.E < 3 || pub.E%2 == 0 {
		return nil, fmt.Errorf("%w: key %q: exponent %d", ErrJWK, k.Kid, pub.E)
	}
	return pub, nil
}

// joseHeader is the JWS protected header.
type joseHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// idTokenChecks are the expectations verifyIDToken enforces.
type idTokenChecks struct {
	issuer   string
	clientID string
	nonce    string
	now      time.Time
	skew     time.Duration
}

// keyLookup returns the candidate keys for a key id and algorithm; the
// second call is made when the first found nothing and a refresh is
// allowed.
type keyLookup func(kid, alg string) []verificationKey

// splitJWS separates a compact JWS into its header, payload, and signature.
func splitJWS(raw string) (header joseHeader, signingInput string, payload, sig []byte, err error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return header, "", nil, nil, fmt.Errorf("%w: expected three segments, got %d", ErrIDToken, len(parts))
	}
	hdr, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return header, "", nil, nil, fmt.Errorf("%w: header: %w", ErrIDToken, err)
	}
	if err := json.Unmarshal(hdr, &header); err != nil {
		return header, "", nil, nil, fmt.Errorf("%w: header: %w", ErrIDToken, err)
	}
	payload, err = base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return header, "", nil, nil, fmt.Errorf("%w: payload: %w", ErrIDToken, err)
	}
	sig, err = base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return header, "", nil, nil, fmt.Errorf("%w: signature: %w", ErrIDToken, err)
	}
	return header, parts[0] + "." + parts[1], payload, sig, nil
}

// verifySignature checks sig over signingInput with any of keys.
func verifySignature(alg, signingInput string, sig []byte, keys []verificationKey) error {
	digest := sha256.Sum256([]byte(signingInput))
	for _, k := range keys {
		if k.alg != alg {
			continue
		}
		switch pub := k.key.(type) {
		case *ecdsa.PublicKey:
			if len(sig) != 64 {
				continue
			}
			r, s := new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])
			if ecdsa.Verify(pub, digest[:], r, s) {
				return nil
			}
		case *rsa.PublicKey:
			if rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig) == nil {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: signature does not verify", ErrIDToken)
}

// verifyIDToken parses and verifies a compact JWS id_token.
func verifyIDToken(raw string, lookup keyLookup, checks idTokenChecks) (Claims, error) {
	header, signingInput, payload, sig, err := splitJWS(raw)
	if err != nil {
		return Claims{}, err
	}
	if header.Alg != algES256 && header.Alg != algRS256 {
		return Claims{}, fmt.Errorf("%w: alg %q not accepted", ErrIDToken, header.Alg)
	}
	keys := lookup(header.Kid, header.Alg)
	if len(keys) == 0 {
		return Claims{}, fmt.Errorf("%w: no key for kid %q", ErrIDToken, header.Kid)
	}
	if err := verifySignature(header.Alg, signingInput, sig, keys); err != nil {
		return Claims{}, err
	}
	var rawClaims map[string]any
	if err := json.Unmarshal(payload, &rawClaims); err != nil {
		return Claims{}, fmt.Errorf("%w: claims: %w", ErrIDToken, err)
	}
	if err := checkClaims(rawClaims, checks); err != nil {
		return Claims{}, err
	}
	return typedClaims(rawClaims), nil
}

func checkClaims(c map[string]any, checks idTokenChecks) error {
	if iss, _ := c["iss"].(string); iss != checks.issuer {
		return fmt.Errorf("%w: iss %q, want %q", ErrIDToken, iss, checks.issuer)
	}
	if !audienceContains(c["aud"], checks.clientID) {
		return fmt.Errorf("%w: aud does not contain the client id", ErrIDToken)
	}
	exp, ok := numericDate(c["exp"])
	if !ok {
		return fmt.Errorf("%w: exp missing", ErrIDToken)
	}
	if !checks.now.Before(exp.Add(checks.skew)) {
		return fmt.Errorf("%w: expired at %s", ErrIDToken, exp.UTC().Format(time.RFC3339))
	}
	iat, ok := numericDate(c["iat"])
	if !ok {
		return fmt.Errorf("%w: iat missing", ErrIDToken)
	}
	if iat.After(checks.now.Add(checks.skew)) {
		return fmt.Errorf("%w: issued in the future at %s", ErrIDToken, iat.UTC().Format(time.RFC3339))
	}
	if sub, _ := c["sub"].(string); sub == "" {
		return fmt.Errorf("%w: sub missing", ErrIDToken)
	}
	if nonce, _ := c["nonce"].(string); nonce == "" || nonce != checks.nonce {
		return fmt.Errorf("%w: nonce mismatch", ErrIDToken)
	}
	return nil
}

// audienceContains handles aud as a string or an array of strings.
func audienceContains(aud any, clientID string) bool {
	switch v := aud.(type) {
	case string:
		return v == clientID
	case []any:
		for _, a := range v {
			if s, ok := a.(string); ok && s == clientID {
				return true
			}
		}
	}
	return false
}

func numericDate(v any) (time.Time, bool) {
	f, ok := v.(float64)
	if !ok {
		return time.Time{}, false
	}
	return time.Unix(int64(f), 0), true
}

func typedClaims(c map[string]any) Claims {
	cl := Claims{Raw: c}
	cl.Subject, _ = c["sub"].(string)
	cl.Email, _ = c["email"].(string)
	cl.EmailVerified, _ = c["email_verified"].(bool)
	cl.Name, _ = c["name"].(string)
	cl.PreferredUsername, _ = c["preferred_username"].(string)
	if groups, ok := c["groups"].([]any); ok {
		for _, g := range groups {
			if s, ok := g.(string); ok {
				cl.Groups = append(cl.Groups, s)
			}
		}
	}
	return cl
}
