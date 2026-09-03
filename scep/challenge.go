package scep

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/internal/clock"
)

// Challenge verifies the challenge password a device presents in its CSR.
type Challenge interface {
	Verify(ctx context.Context, password string, csr *x509.CertificateRequest) error
}

// ErrChallenge is returned for a wrong, missing, expired, or reused challenge.
var ErrChallenge = errors.New("scep: challenge rejected")

// StaticChallenge accepts one shared secret. Adequate only when the
// enrollment profile is delivered over an authenticated channel.
type StaticChallenge string

// Verify implements Challenge with a constant-time comparison.
func (s StaticChallenge) Verify(_ context.Context, password string, _ *x509.CertificateRequest) error {
	if s == "" || subtle.ConstantTimeCompare([]byte(s), []byte(password)) != 1 {
		return fmt.Errorf("%w: static challenge mismatch", ErrChallenge)
	}
	return nil
}

// NoChallenge accepts everything; for renewal-only endpoints or labs.
type NoChallenge struct{}

// Verify implements Challenge.
func (NoChallenge) Verify(context.Context, string, *x509.CertificateRequest) error { return nil }

// OneTimeChallenges issues random single-use challenges with a lifetime;
// each is consumed by the first successful verification.
type OneTimeChallenges struct {
	ttl   time.Duration
	clock clock.Clock
	mu    sync.Mutex
	live  map[string]time.Time
}

// NewOneTimeChallenges creates the issuer.
func NewOneTimeChallenges(ttl time.Duration, c clock.Clock) *OneTimeChallenges {
	if c == nil {
		c = clock.Real{}
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &OneTimeChallenges{ttl: ttl, clock: c, live: map[string]time.Time{}}
}

// Issue returns a new challenge valid for the lifetime. The error is
// reserved for issuers backed by external stores; this one cannot fail
// (crypto/rand.Read never returns an error).
func (o *OneTimeChallenges) Issue(_ context.Context) (string, error) {
	var b [24]byte
	_, _ = rand.Read(b[:])
	c := base64.RawURLEncoding.EncodeToString(b[:])
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.clock.Now()
	for k, exp := range o.live {
		if now.After(exp) {
			delete(o.live, k)
		}
	}
	o.live[c] = now.Add(o.ttl)
	return c, nil
}

// Verify implements Challenge, consuming the challenge.
func (o *OneTimeChallenges) Verify(_ context.Context, password string, _ *x509.CertificateRequest) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	exp, ok := o.live[password]
	if !ok {
		return fmt.Errorf("%w: unknown or already used", ErrChallenge)
	}
	delete(o.live, password)
	if o.clock.Now().After(exp) {
		return fmt.Errorf("%w: expired", ErrChallenge)
	}
	return nil
}

// Live returns how many unexpired challenges are outstanding.
func (o *OneTimeChallenges) Live() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.live)
}

// HMACChallenge derives challenges from a secret, the CSR subject common
// name, and an expiry, so the server needs no state and a challenge issued
// for one device cannot be replayed by another. The challenge is
// "<unix expiry>.<base64url(HMAC-SHA256(key, expiry || cn))>".
type HMACChallenge struct {
	key   []byte
	ttl   time.Duration
	clock clock.Clock
}

// NewHMACChallenge creates the issuer. The key must be at least 16 bytes.
func NewHMACChallenge(key []byte, ttl time.Duration, c clock.Clock) (*HMACChallenge, error) {
	if len(key) < 16 {
		return nil, errors.New("scep: HMAC challenge key must be at least 16 bytes")
	}
	if c == nil {
		c = clock.Real{}
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	return &HMACChallenge{key: append([]byte(nil), key...), ttl: ttl, clock: c}, nil
}

// Issue returns the challenge for the subject common name a device will
// put in its CSR (its UDID, typically).
func (h *HMACChallenge) Issue(commonName string) string {
	exp := h.clock.Now().Add(h.ttl).Unix()
	return fmt.Sprintf("%d.%s", exp, base64.RawURLEncoding.EncodeToString(h.mac(exp, commonName)))
}

func (h *HMACChallenge) mac(exp int64, cn string) []byte {
	m := hmac.New(sha256.New, h.key)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(exp)) // #nosec G115 -- unix seconds are positive
	m.Write(b[:])
	m.Write([]byte(cn))
	return m.Sum(nil)
}

// Verify implements Challenge.
func (h *HMACChallenge) Verify(_ context.Context, password string, csr *x509.CertificateRequest) error {
	expStr, macStr, ok := strings.Cut(password, ".")
	if !ok || csr == nil {
		return fmt.Errorf("%w: malformed", ErrChallenge)
	}
	var exp int64
	if _, err := fmt.Sscanf(expStr, "%d", &exp); err != nil {
		return fmt.Errorf("%w: malformed expiry", ErrChallenge)
	}
	got, err := base64.RawURLEncoding.DecodeString(macStr)
	if err != nil {
		return fmt.Errorf("%w: malformed mac", ErrChallenge)
	}
	if !hmac.Equal(got, h.mac(exp, csr.Subject.CommonName)) {
		return fmt.Errorf("%w: mac mismatch for subject %q", ErrChallenge, csr.Subject.CommonName)
	}
	if h.clock.Now().Unix() > exp {
		return fmt.Errorf("%w: expired", ErrChallenge)
	}
	return nil
}
