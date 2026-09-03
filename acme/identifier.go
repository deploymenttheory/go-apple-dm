package acme

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/internal/clock"
)

// MinIdentifierKey is the shortest HMAC key accepted for minting client
// identifiers.
const MinIdentifierKey = 16

// ErrIdentifierKey is a key too short to mint identifiers with.
var ErrIdentifierKey = errors.New("acme: identifier key must be at least 16 bytes")

// Identifiers turns the client identifier an order asks for into what the
// server knows about the device it was issued to.
//
// This is the seam that decides who may enroll. Apple describes the
// ClientIdentifier as evidence "that the device has access to a valid
// client identifier issued by the enterprise infrastructure", while warning
// that it is "a relatively weak indication because of the risk that an
// attacker can intercept the client identifier". So it is treated as one
// factor: it says which device the server expects, and the attestation says
// which device actually turned up. Both must agree.
type Identifiers interface {
	// Verify returns the binding for an identifier, or a problem. A
	// rejection should wrap ErrRejected so the order fails cleanly rather
	// than looking like a server fault.
	Verify(ctx context.Context, identifier string) (Binding, error)
}

// IdentifiersFunc adapts a function to Identifiers.
type IdentifiersFunc func(ctx context.Context, identifier string) (Binding, error)

// Verify implements Identifiers.
func (f IdentifiersFunc) Verify(ctx context.Context, identifier string) (Binding, error) {
	return f(ctx, identifier)
}

// HMACIdentifiers mints client identifiers that carry their own binding and
// expiry under a message authentication code, so the server needs no state
// between writing an enrollment profile and seeing the order that uses it.
// It is the same stateless approach the SCEP HMAC challenge uses, and it
// suits the same situation: a profile is composed at one moment and
// presented at another, possibly by a different process.
//
// Statelessness bounds replay by time, not by use. The one-time property
// comes from the store: the first order claims the identifier and a second
// is refused.
type HMACIdentifiers struct {
	key   []byte
	ttl   time.Duration
	clock clock.Clock
}

// NewHMACIdentifiers builds a minter. The key must be at least
// MinIdentifierKey bytes; ttl is how long a minted identifier stays usable,
// and should cover the gap between handing a device its profile and the
// device acting on it.
func NewHMACIdentifiers(key []byte, ttl time.Duration, c clock.Clock) (*HMACIdentifiers, error) {
	if len(key) < MinIdentifierKey {
		return nil, fmt.Errorf("%w: got %d", ErrIdentifierKey, len(key))
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if c == nil {
		c = clock.Real{}
	}
	return &HMACIdentifiers{key: append([]byte(nil), key...), ttl: ttl, clock: c}, nil
}

// sealed is what an identifier carries.
type sealed struct {
	Binding Binding `json:"b"`
	Expires int64   `json:"e"`
	// Unique makes every minted identifier different even for the same
	// device and the same expiry second. Without it two identifiers issued
	// in quick succession would be the same string, and since an identifier
	// may be claimed only once, the second would be dead on arrival.
	Unique []byte `json:"u"`
}

// Issue mints an identifier for a device. The result goes into the ACME
// payload's ClientIdentifier.
func (h *HMACIdentifiers) Issue(b Binding) (string, error) {
	unique := make([]byte, 12)
	if _, err := rand.Read(unique); err != nil {
		return "", fmt.Errorf("acme: seal identifier: %w", err)
	}
	payload, err := json.Marshal(sealed{
		Binding: b, Expires: h.clock.Now().Add(h.ttl).Unix(), Unique: unique,
	})
	if err != nil {
		return "", fmt.Errorf("acme: seal identifier: %w", err)
	}
	enc := base64.RawURLEncoding.EncodeToString(payload)
	return enc + "." + base64.RawURLEncoding.EncodeToString(h.mac(payload)), nil
}

// Verify implements Identifiers.
func (h *HMACIdentifiers) Verify(_ context.Context, identifier string) (Binding, error) {
	enc, sig, ok := strings.Cut(identifier, ".")
	if !ok {
		return Binding{}, NewProblem(ProblemRejectedIdentifier, "the identifier is not recognised")
	}
	payload, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return Binding{}, WrapProblem(
			ProblemRejectedIdentifier, err, "the identifier is not recognised",
		)
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return Binding{}, WrapProblem(
			ProblemRejectedIdentifier, err, "the identifier is not recognised",
		)
	}
	// Compare before decoding, so nothing an unauthenticated caller wrote
	// is parsed.
	if !hmac.Equal(got, h.mac(payload)) {
		return Binding{}, NewProblem(ProblemRejectedIdentifier, "the identifier is not recognised")
	}
	var s sealed
	if err := json.Unmarshal(payload, &s); err != nil {
		return Binding{}, WrapProblem(
			ProblemRejectedIdentifier, err, "the identifier is not recognised",
		)
	}
	if h.clock.Now().After(time.Unix(s.Expires, 0)) {
		return Binding{}, NewProblem(ProblemRejectedIdentifier, "the identifier has expired")
	}
	return s.Binding, nil
}

func (h *HMACIdentifiers) mac(payload []byte) []byte {
	m := hmac.New(sha256.New, h.key)
	m.Write(payload)
	return m.Sum(nil)
}

// StaticIdentifiers accepts a fixed set, for development and tests. The
// zero value accepts nothing.
type StaticIdentifiers map[string]Binding

// Verify implements Identifiers.
func (s StaticIdentifiers) Verify(_ context.Context, identifier string) (Binding, error) {
	b, ok := s[identifier]
	if !ok {
		return Binding{}, NewProblem(ProblemRejectedIdentifier, "the identifier is not recognised")
	}
	return b, nil
}
