package adminauth

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/cedar-policy/cedar-go"
)

// Role groups administrative principals. Only Cedar policies grant authority.
type Role struct {
	Name        string
	Description string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Enabled reports whether a policy participates in authorization.
func (p Policy) Enabled() bool { return p.Active == nil || *p.Active }

// References extracts literal entity references from Cedar's parsed JSON form.
// String values and comments cannot be mistaken for entity references.
func References(source string, entityType string) ([]string, error) {
	set, err := cedar.NewPolicySetFromBytes("references", []byte(source))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var out []string
	var visit func(any)
	visit = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if v["type"] == entityType {
				if id, ok := v["id"].(string); ok && !slices.Contains(out, id) {
					out = append(out, id)
				}
			}
			for _, child := range v {
				visit(child)
			}
		case []any:
			for _, child := range v {
				visit(child)
			}
		}
	}
	for _, policy := range set.All() {
		data, err := policy.MarshalJSON()
		if err != nil {
			return nil, err
		}
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, err
		}
		visit(v)
	}
	slices.Sort(out)
	return out, nil
}

// ValidateReferences refuses dangling references, even in inactive policies.
func ValidateReferences(ctx context.Context, store Store, p Policy) error {
	for _, kind := range []string{string(EntityRole), string(EntityPrincipal)} {
		names, err := References(p.Source, kind)
		if err != nil {
			return err
		}
		for _, name := range names {
			if kind == string(EntityRole) {
				_, err = store.Role(ctx, name)
			} else {
				_, err = store.Principal(ctx, name)
			}
			if err != nil {
				return fmt.Errorf("%w: unresolved %s %q: %w", ErrInvalid, kind, name, err)
			}
		}
	}
	return nil
}

// PutRole requires root authority and creates or updates a named role, preserving its
// creation time and advancing the policy version. Role membership alone grants no
// operational permission.
func (m *Manager) PutRole(ctx context.Context, actor Principal, role Role) (Role, error) {
	if err := m.canAdminister(actor); err != nil {
		return Role{}, err
	}
	if !ValidName(role.Name) {
		return Role{}, fmt.Errorf("%w: role name", ErrInvalid)
	}
	return m.store.PutRole(ctx, role, m.clock.Now())
}

// Role returns the named managed role, or ErrNotFound when no such role exists.
func (m *Manager) Role(ctx context.Context, name string) (Role, error) {
	return m.store.Role(ctx, name)
}

// Roles returns a page of managed roles in name order using an exclusive cursor.
func (m *Manager) Roles(ctx context.Context, page Page) (Result[Role], error) {
	return m.store.Roles(ctx, page)
}

// DeleteRole requires root authority and removes an unreferenced role. Principal membership
// or a policy reference returns ErrConflict; an absent role returns ErrNotFound.
func (m *Manager) DeleteRole(ctx context.Context, actor Principal, name string) error {
	if err := m.canAdminister(actor); err != nil {
		return err
	}
	return m.store.DeleteRole(ctx, name)
}

// Bootstrap creates the first root exactly once, with no operational grants.
func (m *Manager) Bootstrap(ctx context.Context, name string, expires time.Time) (Principal, Token, error) {
	if !ValidName(name) || (!expires.IsZero() && !expires.After(m.clock.Now())) {
		return Principal{}, "", ErrInvalid
	}
	token, id, err := mint()
	if err != nil {
		return Principal{}, "", err
	}
	p, err := m.store.BootstrapPrincipal(ctx, Principal{Name: name, Root: true, TokenID: id, ExpiresAt: expires}, Digest(token), m.clock.Now())
	if err != nil {
		return Principal{}, "", err
	}
	return p, token, nil
}

// Initialized reports whether principal initialization has permanently consumed the
// bootstrap opportunity.
func (m *Manager) Initialized(ctx context.Context) (bool, error) { return m.store.Initialized(ctx) }

// ValidatePolicy checks without persisting or changing the active policy set.
func (m *Manager) ValidatePolicy(ctx context.Context, p Policy) error {
	if err := Validate(m.reg, p); err != nil {
		return err
	}
	return ValidateReferences(ctx, m.store, p)
}
