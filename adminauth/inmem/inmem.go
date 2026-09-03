package inmem

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/adminauth"
)

var _ adminauth.Store = (*Store)(nil)

// Store keeps principals and policies in memory. It is safe for concurrent
// use.
type Store struct {
	mu         sync.RWMutex
	principals map[string]record
	policies   map[string]adminauth.Policy
	version    int64
}

// record is a principal plus the digest of its current token, which never
// leaves the store.
type record struct {
	p      adminauth.Principal
	digest string
}

// New returns an empty Store.
func New() *Store {
	return &Store{
		principals: make(map[string]record),
		policies:   make(map[string]adminauth.Policy),
	}
}

// CreatePrincipal implements adminauth.Store.
func (s *Store) CreatePrincipal(_ context.Context, p adminauth.Principal, digest string, now time.Time) (adminauth.Principal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.principals[p.Name]; ok {
		return adminauth.Principal{}, fmt.Errorf("%w: principal %q", adminauth.ErrConflict, p.Name)
	}
	p.Roles = slices.Clone(p.Roles)
	sort.Strings(p.Roles)
	p.CreatedAt, p.UpdatedAt, p.TokenAt = now, now, now
	s.principals[p.Name] = record{p: p, digest: digest}
	return p, nil
}

// Principal implements adminauth.Store.
func (s *Store) Principal(_ context.Context, name string) (adminauth.Principal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.principals[name]
	if !ok {
		return adminauth.Principal{}, fmt.Errorf("%w: principal %q", adminauth.ErrNotFound, name)
	}
	return clone(r.p), nil
}

// PrincipalByDigest implements adminauth.Store.
func (s *Store) PrincipalByDigest(_ context.Context, digest string) (adminauth.Principal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	// A revoked principal has no digest, so an empty digest must never match.
	if digest == "" {
		return adminauth.Principal{}, fmt.Errorf("%w: token", adminauth.ErrNotFound)
	}
	for _, r := range s.principals {
		if r.digest == digest {
			return clone(r.p), nil
		}
	}
	return adminauth.Principal{}, fmt.Errorf("%w: token", adminauth.ErrNotFound)
}

// Principals implements adminauth.Store, paging by name.
func (s *Store) Principals(_ context.Context, p adminauth.Page) (adminauth.Result[adminauth.Principal], error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.principals))
	for name := range s.principals {
		if p.Cursor != "" && name <= p.Cursor {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	limit := p.Limit
	if limit <= 0 {
		limit = adminauth.DefaultPageSize
	}
	var out adminauth.Result[adminauth.Principal]
	for i, name := range names {
		if i == limit {
			out.NextCursor = names[i-1]
			break
		}
		out.Items = append(out.Items, clone(s.principals[name].p))
	}
	return out, nil
}

// UpdatePrincipal implements adminauth.Store.
func (s *Store) UpdatePrincipal(_ context.Context, name string, roles []string, root bool, now time.Time) (adminauth.Principal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.principals[name]
	if !ok {
		return adminauth.Principal{}, fmt.Errorf("%w: principal %q", adminauth.ErrNotFound, name)
	}
	r.p.Roles = slices.Clone(roles)
	sort.Strings(r.p.Roles)
	r.p.Root = root
	r.p.UpdatedAt = now
	s.principals[name] = r
	return clone(r.p), nil
}

// SetToken implements adminauth.Store.
func (s *Store) SetToken(_ context.Context, name, digest, tokenID string, expires, now time.Time) (adminauth.Principal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.principals[name]
	if !ok {
		return adminauth.Principal{}, fmt.Errorf("%w: principal %q", adminauth.ErrNotFound, name)
	}
	r.digest = digest
	r.p.TokenID = tokenID
	r.p.TokenAt = now
	r.p.ExpiresAt = expires
	r.p.UpdatedAt = now
	s.principals[name] = r
	return clone(r.p), nil
}

// RevokeToken implements adminauth.Store.
func (s *Store) RevokeToken(_ context.Context, name string, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.principals[name]
	if !ok {
		return fmt.Errorf("%w: principal %q", adminauth.ErrNotFound, name)
	}
	r.digest = ""
	r.p.TokenID = ""
	r.p.TokenAt = time.Time{}
	r.p.ExpiresAt = time.Time{}
	r.p.UpdatedAt = now
	s.principals[name] = r
	return nil
}

// DeletePrincipal implements adminauth.Store.
func (s *Store) DeletePrincipal(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.principals[name]; !ok {
		return fmt.Errorf("%w: principal %q", adminauth.ErrNotFound, name)
	}
	delete(s.principals, name)
	return nil
}

// CountRoot implements adminauth.Store.
func (s *Store) CountRoot(_ context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, r := range s.principals {
		if r.p.Root {
			n++
		}
	}
	return n, nil
}

// PutPolicy implements adminauth.Store.
func (s *Store) PutPolicy(_ context.Context, p adminauth.Policy, now time.Time) (adminauth.Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.policies[p.Name]; ok {
		p.CreatedAt = old.CreatedAt
	} else {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	s.policies[p.Name] = p
	s.version++
	return p, nil
}

// GetPolicy implements adminauth.Store.
func (s *Store) GetPolicy(_ context.Context, name string) (adminauth.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.policies[name]
	if !ok {
		return adminauth.Policy{}, fmt.Errorf("%w: policy %q", adminauth.ErrNotFound, name)
	}
	return p, nil
}

// Policies implements adminauth.Store, ordered by name.
func (s *Store) Policies(_ context.Context) ([]adminauth.Policy, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	names := make([]string, 0, len(s.policies))
	for name := range s.policies {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]adminauth.Policy, 0, len(names))
	for _, name := range names {
		out = append(out, s.policies[name])
	}
	return out, nil
}

// DeletePolicy implements adminauth.Store.
func (s *Store) DeletePolicy(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.policies[name]; !ok {
		return fmt.Errorf("%w: policy %q", adminauth.ErrNotFound, name)
	}
	delete(s.policies, name)
	s.version++
	return nil
}

// PolicyVersion implements adminauth.Store.
func (s *Store) PolicyVersion(_ context.Context) (int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.version, nil
}

// clone returns a copy whose slice a caller cannot mutate through.
func clone(p adminauth.Principal) adminauth.Principal {
	p.Roles = slices.Clone(p.Roles)
	return p
}
