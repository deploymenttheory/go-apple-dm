package adminauth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cedar-policy/cedar-go/types"

	"github.com/deploymenttheory/go-apple-dm/internal/clock"
)

// Manager authenticates admin callers, answers authorization decisions from
// the stored Cedar policies, and administers principals and policies.
//
// Two things sit outside Cedar on purpose. Policy administration is gated by
// Principal.Root, because a policy that can edit policies can grant itself
// anything, so it cannot be the thing that bounds itself. And the last root
// principal cannot be removed, demoted, or revoked, so an operator cannot
// lock themselves out. Credential administration is an ordinary action a
// policy may grant, bounded by Covers.
type Manager struct {
	store Store
	reg   *Registry
	clock clock.Clock

	mu       sync.RWMutex
	compiled *PolicySet
}

// Option configures a Manager.
type Option func(*Manager)

// WithClock injects a clock, so token expiry is deterministic in tests.
func WithClock(c clock.Clock) Option {
	return func(m *Manager) {
		if c != nil {
			m.clock = c
		}
	}
}

// New returns a Manager over store, serving the actions in reg.
func New(store Store, reg *Registry, opts ...Option) (*Manager, error) {
	if store == nil {
		return nil, fmt.Errorf("%w: nil store", ErrInvalid)
	}
	if reg == nil {
		return nil, fmt.Errorf("%w: nil action registry", ErrInvalid)
	}
	m := &Manager{store: store, reg: reg, clock: clock.Real{}}
	for _, o := range opts {
		o(m)
	}
	return m, nil
}

// Registry returns the action registry this manager serves.
func (m *Manager) Registry() *Registry { return m.reg }

// Authenticate resolves a plaintext token to its principal.
//
// A malformed token is rejected on its checksum before any query runs, so a
// scanner spraying the endpoint costs no database round trips. The principal
// carries TokenID, so an audit line names the credential that acted without
// naming the secret.
func (m *Manager) Authenticate(ctx context.Context, t Token) (Principal, error) {
	if !Valid(t) {
		return Principal{}, fmt.Errorf("%w: malformed token", ErrInvalid)
	}
	p, err := m.store.PrincipalByDigest(ctx, Digest(t))
	if err != nil {
		return Principal{}, err
	}
	if err := p.Active(m.clock.Now()); err != nil {
		return Principal{}, err
	}
	return p, nil
}

// Authorize answers one request against the current policies, recompiling
// them when the store's policy version has moved.
//
// The default is deny: an unknown action, an empty policy set, and a policy
// that errors all produce a denial rather than an allow.
func (m *Manager) Authorize(ctx context.Context, p Principal, action string, resource types.EntityUID, reqCtx map[string]types.Value) (Decision, error) {
	if _, ok := m.reg.Lookup(action); !ok {
		return Decision{}, fmt.Errorf("%w: %q", ErrUnknownAction, action)
	}
	set, err := m.policySet(ctx)
	if err != nil {
		return Decision{}, err
	}
	return set.Authorize(p, action, resource, reqCtx), nil
}

// policySet returns the compiled policies, recompiling when the store version
// has changed. A compile failure is an error rather than a silent empty set,
// because an empty set denies everything and would look like a policy bug.
func (m *Manager) policySet(ctx context.Context) (*PolicySet, error) {
	v, err := m.store.PolicyVersion(ctx)
	if err != nil {
		return nil, err
	}
	m.mu.RLock()
	cur := m.compiled
	m.mu.RUnlock()
	if cur != nil && cur.Version() == v {
		return cur, nil
	}
	docs, err := m.store.Policies(ctx)
	if err != nil {
		return nil, err
	}
	set, err := Compile(m.reg, v, docs)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.compiled = set
	m.mu.Unlock()
	return set, nil
}

// CreatePrincipal adds a principal and mints its first token.
//
// actor must already hold every role it grants, so a credential can never
// mint one more privileged than itself. Whether the caller may administer
// principals at all is the ActionManagePrincipals decision, made by the
// caller before it gets here.
func (m *Manager) CreatePrincipal(ctx context.Context, actor Principal, p Principal, expires time.Time) (Principal, Token, error) {
	if !ValidName(p.Name) {
		return Principal{}, "", fmt.Errorf("%w: principal name %q", ErrInvalid, p.Name)
	}
	for _, r := range p.Roles {
		if !ValidName(r) {
			return Principal{}, "", fmt.Errorf("%w: role %q", ErrInvalid, r)
		}
	}
	if !actor.Covers(p) {
		return Principal{}, "", fmt.Errorf("%w: %s cannot grant %v", ErrEscalation, actor.Name, p.Roles)
	}
	tok, id, err := mint()
	if err != nil {
		return Principal{}, "", err
	}
	p.TokenID = id
	p.ExpiresAt = expires
	out, err := m.store.CreatePrincipal(ctx, p, Digest(tok), m.clock.Now())
	if err != nil {
		return Principal{}, "", err
	}
	return out, tok, nil
}

// Rotate mints a replacement token, invalidating the previous one at once.
func (m *Manager) Rotate(ctx context.Context, actor Principal, name string, expires time.Time) (Principal, Token, error) {
	target, err := m.mayIssueFor(ctx, actor, name)
	if err != nil {
		return Principal{}, "", err
	}
	tok, id, err := mint()
	if err != nil {
		return Principal{}, "", err
	}
	out, err := m.store.SetToken(ctx, target.Name, Digest(tok), id, expires, m.clock.Now())
	if err != nil {
		return Principal{}, "", err
	}
	return out, tok, nil
}

// UpdatePrincipal replaces a principal's roles and root flag.
func (m *Manager) UpdatePrincipal(ctx context.Context, actor Principal, name string, roles []string, root bool) (Principal, error) {
	target, err := m.mayIssueFor(ctx, actor, name)
	if err != nil {
		return Principal{}, err
	}
	if !actor.Covers(Principal{Roles: roles, Root: root}) {
		return Principal{}, fmt.Errorf("%w: %s cannot grant %v", ErrEscalation, actor.Name, roles)
	}
	if target.Root && !root {
		if err := m.guardLastRoot(ctx, name); err != nil {
			return Principal{}, err
		}
	}
	return m.store.UpdatePrincipal(ctx, name, roles, root, m.clock.Now())
}

// Revoke clears a principal's token, leaving the principal in place so its
// name still resolves in audit history.
func (m *Manager) Revoke(ctx context.Context, actor Principal, name string) error {
	target, err := m.mayIssueFor(ctx, actor, name)
	if err != nil {
		return err
	}
	// Revoking the last root credential locks everyone out of policy
	// administration just as deleting it would.
	if target.Root {
		if err := m.guardLastRoot(ctx, name); err != nil {
			return err
		}
	}
	return m.store.RevokeToken(ctx, name, m.clock.Now())
}

// DeletePrincipal removes a principal.
func (m *Manager) DeletePrincipal(ctx context.Context, actor Principal, name string) error {
	target, err := m.mayIssueFor(ctx, actor, name)
	if err != nil {
		return err
	}
	if target.Root {
		if err := m.guardLastRoot(ctx, name); err != nil {
			return err
		}
	}
	return m.store.DeletePrincipal(ctx, name)
}

// Principal returns one principal.
func (m *Manager) Principal(ctx context.Context, name string) (Principal, error) {
	return m.store.Principal(ctx, name)
}

// Principals pages principals by name.
func (m *Manager) Principals(ctx context.Context, p Page) (Result[Principal], error) {
	return m.store.Principals(ctx, p)
}

// PutPolicy validates and stores a policy document. Validation is at write
// time on purpose: a policy naming an action nobody serves parses cleanly and
// then silently never grants, so refusing it here is the difference between an
// error an operator sees and a rule that quietly does nothing.
func (m *Manager) PutPolicy(ctx context.Context, actor Principal, doc Policy) (Policy, error) {
	if err := m.canAdminister(actor); err != nil {
		return Policy{}, err
	}
	if err := Validate(m.reg, doc); err != nil {
		return Policy{}, err
	}
	return m.store.PutPolicy(ctx, doc, m.clock.Now())
}

// GetPolicy returns one policy document.
func (m *Manager) GetPolicy(ctx context.Context, actor Principal, name string) (Policy, error) {
	if err := m.canAdminister(actor); err != nil {
		return Policy{}, err
	}
	return m.store.GetPolicy(ctx, name)
}

// Policies returns every policy document, ordered by name.
func (m *Manager) Policies(ctx context.Context, actor Principal) ([]Policy, error) {
	if err := m.canAdminister(actor); err != nil {
		return nil, err
	}
	return m.store.Policies(ctx)
}

// DeletePolicy removes a policy document.
func (m *Manager) DeletePolicy(ctx context.Context, actor Principal, name string) error {
	if err := m.canAdminister(actor); err != nil {
		return err
	}
	return m.store.DeletePolicy(ctx, name)
}

// canAdminister gates *policy* administration on Root rather than on a
// policy, because a principal that can edit policies can grant itself
// anything, so the capability cannot be the thing the policies bound.
//
// Credential administration is deliberately not gated here: it is a normal
// action a policy may grant, bounded by Covers so a caller can never issue a
// credential more privileged than its own. Zentral draws the line in the same
// place.
func (m *Manager) canAdminister(actor Principal) error {
	if !actor.Root {
		return fmt.Errorf("%w: %s is not a root principal", ErrDenied, actor.Name)
	}
	return nil
}

// mayIssueFor reports whether actor may act on the credential of name.
//
// Two conditions, both from Zentral's can_issue_credentials_for: the actor
// must hold every role the target holds, and the target must not be named
// directly by a policy. A principal a policy names by name no longer derives
// its authority from its roles, so the role subset test would not bound it.
func (m *Manager) mayIssueFor(ctx context.Context, actor Principal, name string) (Principal, error) {
	target, err := m.store.Principal(ctx, name)
	if err != nil {
		return Principal{}, err
	}
	if !actor.Covers(target) {
		return Principal{}, fmt.Errorf("%w: %s does not hold every role of %s", ErrEscalation, actor.Name, name)
	}
	if actor.Name != name {
		named, err := m.namedByPolicy(ctx, name)
		if err != nil {
			return Principal{}, err
		}
		if named {
			return Principal{}, fmt.Errorf("%w: %s is named directly by a policy", ErrEscalation, name)
		}
	}
	return target, nil
}

// namedByPolicy reports whether any stored policy references the principal by
// name rather than through a role.
func (m *Manager) namedByPolicy(ctx context.Context, name string) (bool, error) {
	docs, err := m.store.Policies(ctx)
	if err != nil {
		return false, err
	}
	needle := string(EntityPrincipal) + `::"` + name + `"`
	for _, d := range docs {
		if strings.Contains(d.Source, needle) {
			return true, nil
		}
	}
	return false, nil
}

// guardLastRoot refuses when name is the only root principal.
func (m *Manager) guardLastRoot(ctx context.Context, name string) error {
	n, err := m.store.CountRoot(ctx)
	if err != nil {
		return err
	}
	if n <= 1 {
		return fmt.Errorf("%w: %s", ErrLastRoot, name)
	}
	return nil
}

// Root is the implicit actor for bootstrap, before any principal exists.
var Root = Principal{Name: "root", Root: true}

// mint returns a token and the public id that names it in audit lines. The id
// is a fragment of the body: enough to identify the credential, never enough
// to use it.
func mint() (Token, string, error) {
	t, err := Mint()
	if err != nil {
		return "", "", err
	}
	return t, string(t)[len(Prefix) : len(Prefix)+8], nil
}
