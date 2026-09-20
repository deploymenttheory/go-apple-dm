package inmem

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

func (s *Store) validateRoles(names []string) error {
	for _, name := range names {
		if _, ok := s.roles[name]; !ok {
			return fmt.Errorf("%w: unknown role %q", adminauth.ErrInvalid, name)
		}
	}
	return nil
}

func (s *Store) validateReferences(p adminauth.Policy) error {
	names, err := adminauth.References(p.Source, string(adminauth.EntityRole))
	if err != nil {
		return err
	}
	if err := s.validateRoles(names); err != nil {
		return err
	}
	names, err = adminauth.References(p.Source, string(adminauth.EntityPrincipal))
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, ok := s.principals[name]; !ok {
			return fmt.Errorf("%w: unknown principal %q", adminauth.ErrInvalid, name)
		}
	}
	return nil
}

func (s *Store) PutRole(_ context.Context, role adminauth.Role, now time.Time) (adminauth.Role, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !adminauth.ValidName(role.Name) {
		return adminauth.Role{}, adminauth.ErrInvalid
	}
	role.CreatedAt, role.UpdatedAt = now, now
	if old, ok := s.roles[role.Name]; ok {
		role.CreatedAt = old.CreatedAt
	}
	s.roles[role.Name] = role
	s.version++
	return role, nil
}

func (s *Store) Role(_ context.Context, name string) (adminauth.Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	role, ok := s.roles[name]
	if !ok {
		return role, adminauth.ErrNotFound
	}
	return role, nil
}

func (s *Store) Roles(_ context.Context, p adminauth.Page) (adminauth.Result[adminauth.Role], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var names []string
	for name := range s.roles {
		if name > p.Cursor {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if p.Limit <= 0 {
		p.Limit = adminauth.DefaultPageSize
	}
	out := adminauth.Result[adminauth.Role]{Items: []adminauth.Role{}}
	if len(names) > p.Limit {
		names = names[:p.Limit]
		out.NextCursor = names[len(names)-1]
	}
	for _, name := range names {
		out.Items = append(out.Items, s.roles[name])
	}
	return out, nil
}

func (s *Store) DeleteRole(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.roles[name]; !ok {
		return adminauth.ErrNotFound
	}
	for _, r := range s.principals {
		if slices.Contains(r.p.Roles, name) {
			return adminauth.ErrConflict
		}
	}
	for _, p := range s.policies {
		refs, err := adminauth.References(p.Source, string(adminauth.EntityRole))
		if err != nil {
			return err
		}
		if slices.Contains(refs, name) {
			return adminauth.ErrConflict
		}
	}
	delete(s.roles, name)
	s.version++
	return nil
}

func (s *Store) Initialized(_ context.Context) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initialized, nil
}

func (s *Store) BootstrapPrincipal(_ context.Context, p adminauth.Principal, digest string, now time.Time) (adminauth.Principal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initialized || len(s.principals) != 0 {
		return adminauth.Principal{}, adminauth.ErrConflict
	}
	if !p.Root || p.TokenID == "" || digest == "" || p.Active(now) != nil {
		return adminauth.Principal{}, adminauth.ErrInvalid
	}
	return s.createPrincipal(p, digest, now)
}

func clonePolicy(p adminauth.Policy) adminauth.Policy {
	if p.Active != nil {
		active := *p.Active
		p.Active = &active
	}
	return p
}
