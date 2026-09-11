package adminauth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cedar-policy/cedar-go/types"

	"github.com/deploymenttheory/go-apple-dm/clock"
)

// Manager authenticates admin callers, answers authorization decisions from
// the stored Cedar policies, and administers principals and policies.
//
// Principal, credential and policy mutations require Principal.Root independently
// of Cedar. Callers authenticate the actor and authorize the route action before
// invoking these methods. A role-subset comparison cannot bound arbitrary Cedar
// authority. Stores atomically protect the last active root from removal,
// demotion, revocation and immediate expiry; later expiry and policy lockout
// remain operational responsibilities.
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
// actor must be Root. The caller additionally authorizes its administrative
// action before entering this manager.
func (m *Manager) CreatePrincipal(ctx context.Context, actor Principal, p Principal, expires time.Time) (Principal, Token, error) {
	if err := m.canAdminister(actor); err != nil {
		return Principal{}, "", err
	}
	if !ValidName(p.Name) {
		return Principal{}, "", fmt.Errorf("%w: principal name %q", ErrInvalid, p.Name)
	}
	for _, r := range p.Roles {
		if !ValidName(r) {
			return Principal{}, "", fmt.Errorf("%w: role %q", ErrInvalid, r)
		}
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
	out, err := m.store.ApplyPrincipal(ctx, target.Name, PrincipalChange{Op: "rotate", Digest: Digest(tok), TokenID: id, ExpiresAt: expires}, m.clock.Now())
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
	for _, role := range roles {
		if !ValidName(role) {
			return Principal{}, fmt.Errorf("%w: role name", ErrInvalid)
		}
	}
	return m.store.ApplyPrincipal(ctx, target.Name, PrincipalChange{Op: "update", Roles: roles, Root: root}, m.clock.Now())
}

// Revoke clears a principal's token, leaving the principal in place so its
// name still resolves in audit history.
func (m *Manager) Revoke(ctx context.Context, actor Principal, name string) error {
	target, err := m.mayIssueFor(ctx, actor, name)
	if err != nil {
		return err
	}
	_, err = m.store.ApplyPrincipal(ctx, target.Name, PrincipalChange{Op: "revoke"}, m.clock.Now())
	return err
}

// DeletePrincipal removes a principal.
func (m *Manager) DeletePrincipal(ctx context.Context, actor Principal, name string) error {
	target, err := m.mayIssueFor(ctx, actor, name)
	if err != nil {
		return err
	}
	_, err = m.store.ApplyPrincipal(ctx, target.Name, PrincipalChange{Op: "delete"}, m.clock.Now())
	return err
}

// Principal returns one principal.
func (m *Manager) Principal(ctx context.Context, name string) (Principal, error) {
	return m.store.Principal(ctx, name)
}

// Principals pages principals by name.
func (m *Manager) Principals(ctx context.Context, p Page) (Result[Principal], error) {
	return m.store.Principals(ctx, p)
}

// PutPolicy parses and validates the policy, rejects unknown action references,
// and stores it only after validation succeeds.
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

// canAdminister keeps credential and policy mutations outside policy delegation.
func (m *Manager) canAdminister(actor Principal) error {
	if !actor.Root {
		return fmt.Errorf("%w: %s is not a root principal", ErrDenied, actor.Name)
	}
	return nil
}

// mayIssueFor requires Root before reading or changing another credential.
func (m *Manager) mayIssueFor(ctx context.Context, actor Principal, name string) (Principal, error) {
	if err := m.canAdminister(actor); err != nil {
		return Principal{}, err
	}
	return m.store.Principal(ctx, name)
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
