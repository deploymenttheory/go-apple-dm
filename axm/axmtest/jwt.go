package axmtest

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"
)

// Errors of the assertion checks.
var (
	ErrAssertion = errors.New("axmtest: invalid client assertion")
)

// AssertionHeader is a decoded JWS header.
type AssertionHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ,omitempty"`
}

// AssertionClaims are the decoded claims of a client assertion.
type AssertionClaims struct {
	Iss string `json:"iss"`
	Sub string `json:"sub"`
	Aud string `json:"aud"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
	Jti string `json:"jti"`
}

// DecodeAssertion splits a compact JWS into its header and claims without
// verifying the signature.
func DecodeAssertion(token string) (AssertionHeader, AssertionClaims, error) {
	var h AssertionHeader
	var c AssertionClaims
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return h, c, fmt.Errorf("%w: want 3 segments, got %d", ErrAssertion, len(parts))
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return h, c, fmt.Errorf("%w: header: %w", ErrAssertion, err)
	}
	if err := json.Unmarshal(hb, &h); err != nil {
		return h, c, fmt.Errorf("%w: header: %w", ErrAssertion, err)
	}
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return h, c, fmt.Errorf("%w: claims: %w", ErrAssertion, err)
	}
	if err := json.Unmarshal(cb, &c); err != nil {
		return h, c, fmt.Errorf("%w: claims: %w", ErrAssertion, err)
	}
	return h, c, nil
}

// VerifyAssertion checks the ES256 signature of token with pub.
func VerifyAssertion(token string, pub *ecdsa.PublicKey) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return fmt.Errorf("%w: want 3 segments", ErrAssertion)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("%w: signature: %w", ErrAssertion, err)
	}
	if len(sig) != 64 {
		return fmt.Errorf("%w: ES256 signature must be 64 bytes, got %d", ErrAssertion, len(sig))
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	if !ecdsa.Verify(pub, digest[:], r, s) {
		return fmt.Errorf("%w: signature does not verify", ErrAssertion)
	}
	return nil
}

// checkClaims applies Apple's documented rules to the claims.
func checkClaims(c AssertionClaims, clientID string, now time.Time, skew time.Duration) error {
	if c.Iss != clientID || c.Sub != clientID {
		return fmt.Errorf("%w: iss/sub must be the client id", ErrAssertion)
	}
	if c.Aud != audience {
		return fmt.Errorf("%w: aud must be %s", ErrAssertion, audience)
	}
	if c.Jti == "" {
		return fmt.Errorf("%w: jti is required", ErrAssertion)
	}
	iat, exp := time.Unix(c.Iat, 0), time.Unix(c.Exp, 0)
	if c.Iat == 0 || iat.After(now.Add(skew)) {
		return fmt.Errorf("%w: iat %d is in the future", ErrAssertion, c.Iat)
	}
	if c.Exp == 0 || !exp.After(now.Add(-skew)) {
		return fmt.Errorf("%w: exp %d has passed", ErrAssertion, c.Exp)
	}
	if exp.Sub(iat) > maxAssertionLife {
		return fmt.Errorf("%w: exp is more than 180 days after iat", ErrAssertion)
	}
	return nil
}
