package accountdriven

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/state"
	"time"
)

// Token errors.
var (
	ErrTokenNotFound = errors.New("accountdriven: token not found")
	ErrTokenExpired  = errors.New("accountdriven: token expired")
	ErrTokenUsed     = errors.New("accountdriven: token already used")
)

// Defaults for token lifetimes.
const (
	DefaultAccessTTL     = 10 * time.Minute
	DefaultEnrollmentTTL = 365 * 24 * time.Hour
	DefaultRefreshTTL    = 30 * 24 * time.Hour
	DefaultCodeTTL       = 5 * time.Minute
)

// Identity is who authenticated.
//
// ManagedAppleAccount is required: it becomes AssignedManagedAppleID in the
// enrollment profile. The names differ because Apple renamed the concept to
// Managed Apple Account while the profile key kept its original spelling, and
// a key on the wire is not ours to rename.
type Identity struct {
	// UserIdentifier is what the person typed (user@domain).
	UserIdentifier string
	// ManagedAppleAccount is the account the organisation manages.
	ManagedAppleAccount string
	// Subject is the identity provider's stable id, when known.
	Subject string
	// Claims carries anything else the authenticator learned.
	Claims map[string]any
}

// Kind distinguishes the token tiers.
type Kind string

// Token kinds.
const (
	// KindAccess is the reusable bearer for profile retrieval and ongoing MDM requests.
	KindAccess Kind = "access"
	// KindEnrollment is retained for migration tooling only.
	// Deprecated: query credentials do not authorize account-driven requests.
	KindEnrollment Kind = "enrollment"
	// KindRefresh is the OAuth 2 refresh token; rotated on use.
	KindRefresh Kind = "refresh"
	// KindCode is the OAuth 2 authorization code; single use.
	KindCode Kind = "code"
)

// Record is a stored token.
type Record struct {
	Kind      Kind
	Identity  Identity
	IssuedAt  time.Time
	ExpiresAt time.Time
	UsedAt    time.Time
	// Meta binds a code to its request (redirect URI, state, client id).
	Meta map[string]string
}

// TokenStore keeps tokens by the SHA-256 of their value so a dumped table
// reveals nothing usable.
type TokenStore interface {
	// Put stores rec under hash.
	Put(ctx context.Context, hash string, rec Record) error
	// Get returns the record or ErrTokenNotFound.
	Get(ctx context.Context, hash string) (Record, error)
	// MarkUsed records first use; ErrTokenUsed when already used.
	MarkUsed(ctx context.Context, hash string, at time.Time) error
	// Delete removes a token; absent is not an error.
	Delete(ctx context.Context, hash string) error
}

// Hash is the store key of a token value.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewToken returns 32 random bytes as URL-safe base64.
func NewToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("accountdriven: random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}

// Tokens issues and checks the tiers over a TokenStore.
type Tokens struct {
	Store TokenStore
	Now   func() time.Time
	// TTLs; zero means the default.
	AccessTTL, EnrollmentTTL, RefreshTTL, CodeTTL time.Duration
}

func (t *Tokens) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Tokens) ttl(k Kind) time.Duration {
	switch k {
	case KindAccess:
		return or(t.AccessTTL, DefaultAccessTTL)
	case KindEnrollment:
		return or(t.EnrollmentTTL, DefaultEnrollmentTTL)
	case KindRefresh:
		return or(t.RefreshTTL, DefaultRefreshTTL)
	default:
		return or(t.CodeTTL, DefaultCodeTTL)
	}
}

func or(d, def time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return def
}

// Issue mints a token of kind k for id.
func (t *Tokens) Issue(ctx context.Context, k Kind, id Identity, meta map[string]string) (string, error) {
	tok, err := NewToken()
	if err != nil {
		return "", err
	}
	now := t.now()
	rec := Record{Kind: k, Identity: id, IssuedAt: now, ExpiresAt: now.Add(t.ttl(k)), Meta: meta}
	if err := t.Store.Put(ctx, Hash(tok), rec); err != nil {
		return "", fmt.Errorf("accountdriven: store token: %w", err)
	}
	return tok, nil
}

// Check looks a token up, verifying its kind and expiry. It does not
// consume it.
func (t *Tokens) Check(ctx context.Context, k Kind, tok string) (Record, error) {
	if tok == "" {
		return Record{}, ErrTokenNotFound
	}
	rec, err := t.Store.Get(ctx, Hash(tok))
	if err != nil {
		return Record{}, err
	}
	if rec.Kind != k {
		return Record{}, ErrTokenNotFound
	}
	if !t.now().Before(rec.ExpiresAt) {
		return Record{}, ErrTokenExpired
	}
	if !rec.UsedAt.IsZero() {
		return Record{}, ErrTokenUsed
	}
	return rec, nil
}

// Consume checks a single-use token and marks it used; a second call is
// ErrTokenUsed.
func (t *Tokens) Consume(ctx context.Context, k Kind, tok string) (Record, error) {
	rec, err := t.Check(ctx, k, tok)
	if err != nil {
		return Record{}, err
	}
	if !rec.UsedAt.IsZero() {
		return Record{}, ErrTokenUsed
	}
	if err := t.Store.MarkUsed(ctx, Hash(tok), t.now()); err != nil {
		return Record{}, err
	}
	return rec, nil
}

// MemStore is an in-memory transactional TokenStore.
type MemStore struct{ *StateTokenStore }

// NewMemStore returns an empty store.
func NewMemStore() *MemStore { return &MemStore{&StateTokenStore{Backend: state.NewMemory()}} }

// Verify implements Verifier using a reusable access token.
func (t *Tokens) Verify(ctx context.Context, bearer secrets.Secret) (Identity, error) {
	rec, err := t.Check(ctx, KindAccess, string(bearer.Bytes()))
	return rec.Identity, err
}
