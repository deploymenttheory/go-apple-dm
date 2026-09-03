package adminauth

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cedar-policy/cedar-go/types"
)

// Cedar entity types. Every admin authorization decision is a Cedar request
// over these: a principal (which may be a member of roles), an action, and a
// resource. Namespacing them under MDM keeps action ids unique, which Cedar
// requires globally.
const (
	// EntityPrincipal is an admin caller, `MDM::Principal::"<name>"`.
	EntityPrincipal types.EntityType = "MDM::Principal"
	// EntityRole is a named group a principal belongs to,
	// `MDM::Role::"<name>"`, expressed as a Cedar entity parent so a policy
	// can say `principal in MDM::Role::"reader"`.
	EntityRole types.EntityType = "MDM::Role"
	// EntityAction is an operation a route declares, `MDM::Action::"<id>"`.
	EntityAction types.EntityType = "MDM::Action"
	// EntitySystem is the resource for routes that act on the deployment
	// rather than on one object: `MDM::System::"any"`.
	EntitySystem types.EntityType = "MDM::System"
	// EntityEnrollment is one enrollment as a resource.
	EntityEnrollment types.EntityType = "MDM::Enrollment"
	// EntityDeclaration is one declaration as a resource.
	EntityDeclaration types.EntityType = "MDM::Declaration"
	// EntityDEPAccount is one device enrollment service account as a resource.
	EntityDEPAccount types.EntityType = "MDM::DEPAccount"
)

// SystemResource is the resource for deployment-wide routes.
var SystemResource = types.NewEntityUID(EntitySystem, "any")

// PrincipalUID is the Cedar entity for a principal name.
func PrincipalUID(name string) types.EntityUID {
	return types.NewEntityUID(EntityPrincipal, types.String(name))
}

// RoleUID is the Cedar entity for a role name.
func RoleUID(name string) types.EntityUID {
	return types.NewEntityUID(EntityRole, types.String(name))
}

// ActionUID is the Cedar entity for an action id.
func ActionUID(id string) types.EntityUID {
	return types.NewEntityUID(EntityAction, types.String(id))
}

// Errors this package and its stores return.
var (
	// ErrNotFound is an unknown principal or policy, or a token matching none.
	ErrNotFound = errors.New("adminauth: not found")
	// ErrConflict is a name that already exists.
	ErrConflict = errors.New("adminauth: conflict")
	// ErrInvalid is a malformed name, token, or policy.
	ErrInvalid = errors.New("adminauth: invalid")
	// ErrRevoked is a principal whose token has been revoked.
	ErrRevoked = errors.New("adminauth: token revoked")
	// ErrExpired is a principal whose token has passed its expiry.
	ErrExpired = errors.New("adminauth: token expired")
	// ErrDenied is an authorization decision of deny.
	ErrDenied = errors.New("adminauth: denied")
	// ErrLastRoot guards the last root principal against deletion, demotion,
	// or revocation, so an operator cannot lock themselves out of policy
	// administration. step-ca protects its last super admin the same way.
	ErrLastRoot = errors.New("adminauth: last root principal")
	// ErrEscalation is an attempt to issue a credential for a principal whose
	// authority the caller does not already hold.
	ErrEscalation = errors.New("adminauth: cannot issue credentials for a more privileged principal")
	// ErrUnknownAction is a policy naming an action no route serves. Cedar
	// parses such a policy happily and it then silently never grants, so the
	// check is ours to make.
	ErrUnknownAction = errors.New("adminauth: unknown action")
)

// Principal is an admin caller.
//
// Authority comes from policies that name the principal or one of its roles,
// evaluated by Cedar. The one exception is Root, which is deliberately outside
// the policy system: a principal that may edit policies can grant itself
// anything, so that capability cannot itself be policy-granted. Zentral draws
// the same line, excluding policy mutation from the permissions it bounds.
type Principal struct {
	Name  string
	Roles []string
	// Root may administer principals and policies. It confers no other
	// authority: a root principal still needs a policy to enqueue a command.
	Root      bool
	CreatedAt time.Time
	UpdatedAt time.Time
	// TokenID names the current credential in audit lines, empty once
	// revoked. It is a fragment of the token, never enough to use.
	TokenID string
	// TokenAt is when the current token was minted (zero when revoked).
	TokenAt time.Time
	// ExpiresAt is when the current token stops being accepted; zero never
	// expires. Fleet makes every API-only token non-expiring with no way to
	// say otherwise, which is the failure this field exists to avoid.
	ExpiresAt time.Time
}

// UID is the principal's Cedar entity.
func (p Principal) UID() types.EntityUID { return PrincipalUID(p.Name) }

// Entity renders the principal as a Cedar entity whose parents are its roles,
// so `principal in MDM::Role::"reader"` resolves.
func (p Principal) Entity() types.Entity {
	parents := make([]types.EntityUID, 0, len(p.Roles))
	for _, r := range p.Roles {
		parents = append(parents, RoleUID(r))
	}
	return types.Entity{UID: p.UID(), Parents: types.NewEntityUIDSet(parents...)}
}

// Covers reports whether p may issue a credential for other: it must hold
// every role other does, and be root if other is. This is the subset test
// that stops a principal issuing a credential more privileged than its own,
// ported from Zentral's can_issue_credentials_for.
//
// A root principal covers everything, as Zentral's superuser does. Requiring
// root to hold every role it grants would make the first grant of a new role
// impossible, since a role exists only by being named on a principal or in a
// policy.
func (p Principal) Covers(other Principal) bool {
	if p.Root {
		return true
	}
	if other.Root {
		return false
	}
	for _, r := range other.Roles {
		if !slices.Contains(p.Roles, r) {
			return false
		}
	}
	return true
}

// Active reports whether p has a credential that is neither revoked nor
// expired at now.
func (p Principal) Active(now time.Time) error {
	if p.TokenID == "" {
		return ErrRevoked
	}
	if !p.ExpiresAt.IsZero() && !now.Before(p.ExpiresAt) {
		return ErrExpired
	}
	return nil
}

// ValidName reports whether a principal or role name is usable: 1 to 64
// characters of letters, digits, hyphen, underscore, or dot. Names appear in
// policies and audit lines, so they stay boring on purpose, and the character
// set keeps them safe to render inside a Cedar entity literal.
func ValidName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}

// ParseRoles validates a comma-separated role list, sorted and deduplicated.
func ParseRoles(s string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(s, ",") {
		name := strings.TrimSpace(part)
		if name == "" {
			continue
		}
		if !ValidName(name) {
			return nil, fmt.Errorf("%w: role %q", ErrInvalid, name)
		}
		if !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Policy is a stored Cedar policy document. One record may hold several
// statements; the whole document is parsed and validated as a unit.
type Policy struct {
	// Name identifies the document for editing and for audit lines.
	Name string
	// Source is the Cedar text, stored and served exactly as written so an
	// operator sees back what they wrote.
	Source string
	// Description is operator prose, never interpreted.
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Page requests one page of principals or policies.
type Page struct {
	Cursor string
	Limit  int
}

// DefaultPageSize applies when Page.Limit is not positive.
const DefaultPageSize = 100

// Result is one page with the cursor for the next ("" at the end).
type Result[T any] struct {
	Items      []T
	NextCursor string
}

// Store persists principals, the digests of their tokens, and the policy
// documents that grant them authority. Implementations are contract-tested by
// adminauth/adminauthtest, so the in-memory and the three SQL backends behave
// identically.
//
// A store never sees a plaintext token: the caller mints one, hands the store
// its digest, and shows the value to the operator once.
type Store interface {
	// CreatePrincipal adds a principal with its first token digest. An
	// existing name is ErrConflict.
	CreatePrincipal(ctx context.Context, p Principal, digest string, now time.Time) (Principal, error)
	// Principal returns one principal by name.
	Principal(ctx context.Context, name string) (Principal, error)
	// PrincipalByDigest returns the principal whose current token digest
	// matches. This is the authentication path and must be one indexed lookup.
	PrincipalByDigest(ctx context.Context, digest string) (Principal, error)
	// Principals pages principals by name.
	Principals(ctx context.Context, p Page) (Result[Principal], error)
	// UpdatePrincipal replaces the roles and root flag of a principal.
	UpdatePrincipal(ctx context.Context, name string, roles []string, root bool, now time.Time) (Principal, error)
	// SetToken replaces the current token digest, invalidating the previous
	// one immediately.
	SetToken(ctx context.Context, name, digest, tokenID string, expires, now time.Time) (Principal, error)
	// RevokeToken clears the current token, leaving the principal in place.
	RevokeToken(ctx context.Context, name string, now time.Time) error
	// DeletePrincipal removes a principal entirely.
	DeletePrincipal(ctx context.Context, name string) error
	// CountRoot returns how many principals carry Root, for the anti-lockout
	// invariant.
	CountRoot(ctx context.Context) (int, error)

	// PutPolicy stores or replaces a policy document.
	PutPolicy(ctx context.Context, p Policy, now time.Time) (Policy, error)
	// GetPolicy returns one policy document by name.
	GetPolicy(ctx context.Context, name string) (Policy, error)
	// Policies returns every stored policy, ordered by name. The whole set is
	// compiled together, so this is not paged.
	Policies(ctx context.Context) ([]Policy, error)
	// DeletePolicy removes a policy document.
	DeletePolicy(ctx context.Context, name string) error
	// PolicyVersion changes whenever any policy changes, so a cached
	// compilation can tell it is stale without recompiling.
	PolicyVersion(ctx context.Context) (int64, error)
}
