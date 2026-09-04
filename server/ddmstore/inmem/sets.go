package inmem

import (
	"context"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/paging"
)

// PutSet implements ddm.SetStore. An existing set is left untouched.
func (t *tx) PutSet(_ context.Context, name string, at time.Time) (bool, error) {
	if err := validName("set name", name); err != nil {
		return false, err
	}
	if _, ok := t.st.sets[name]; ok {
		return false, nil
	}
	t.st.sets[name] = ddm.Set{Name: name, CreatedAt: at, UpdatedAt: at}
	return true, nil
}

// DeleteSet implements ddm.SetStore.
func (t *tx) DeleteSet(_ context.Context, name string) error {
	if err := validName("set name", name); err != nil {
		return err
	}
	if _, ok := t.st.sets[name]; !ok {
		return notFound("set", name)
	}
	delete(t.st.sets, name)
	delete(t.st.members, name)
	removeEverywhere(t.st.enrollSets, name)
	return nil
}

// GetSet implements ddm.SetStore.
func (t *tx) GetSet(_ context.Context, name string) (*ddm.Set, error) {
	if err := validName("set name", name); err != nil {
		return nil, err
	}
	s, ok := t.st.sets[name]
	if !ok {
		return nil, notFound("set", name)
	}
	return &s, nil
}

// ListSets implements ddm.SetStore. The cursor is the last name of the
// previous page.
func (t *tx) ListSets(_ context.Context, p paging.Page) (paging.Result[ddm.Set], error) {
	keys := make([]string, 0, len(t.st.sets))
	for name := range t.st.sets {
		keys = append(keys, name)
	}
	return pageByKey(keys, p, func(name string) ddm.Set { return t.st.sets[name] }), nil
}

// AddSetDeclaration implements ddm.SetStore. A membership change bumps the
// set's UpdatedAt to at.
func (t *tx) AddSetDeclaration(_ context.Context, set, identifier string, at time.Time) (bool, error) {
	if err := validName("set name", set); err != nil {
		return false, err
	}
	if err := validName("identifier", identifier); err != nil {
		return false, err
	}
	s, ok := t.st.sets[set]
	if !ok {
		return false, notFound("set", set)
	}
	if _, ok := t.st.decls[identifier]; !ok {
		return false, notFound("declaration", identifier)
	}
	if !add(t.st.members, set, identifier) {
		return false, nil
	}
	s.UpdatedAt = at
	t.st.sets[set] = s
	return true, nil
}

// RemoveSetDeclaration implements ddm.SetStore.
func (t *tx) RemoveSetDeclaration(_ context.Context, set, identifier string) (bool, error) {
	if err := validName("set name", set); err != nil {
		return false, err
	}
	if err := validName("identifier", identifier); err != nil {
		return false, err
	}
	if _, ok := t.st.sets[set]; !ok {
		return false, notFound("set", set)
	}
	return remove(t.st.members, set, identifier), nil
}

// SetDeclarations implements ddm.SetStore.
func (t *tx) SetDeclarations(_ context.Context, set string) ([]string, error) {
	if err := validName("set name", set); err != nil {
		return nil, err
	}
	if _, ok := t.st.sets[set]; !ok {
		return nil, notFound("set", set)
	}
	return sortedKeys(t.st.members[set]), nil
}

// DeclarationSets implements ddm.SetStore. An unknown identifier is in no
// set.
func (t *tx) DeclarationSets(_ context.Context, identifier string) ([]string, error) {
	if err := validName("identifier", identifier); err != nil {
		return nil, err
	}
	return sortedKeys(t.setsContaining(identifier)), nil
}

// setsContaining returns the names of the sets that hold identifier.
func (t *tx) setsContaining(identifier string) map[string]struct{} {
	out := map[string]struct{}{}
	for name, members := range t.st.members {
		if _, ok := members[identifier]; ok {
			out[name] = struct{}{}
		}
	}
	return out
}

// PutSet implements ddm.SetStore.
func (s *Store) PutSet(ctx context.Context, name string, at time.Time) (bool, error) {
	v, done := s.view()
	defer done()
	return v.PutSet(ctx, name, at)
}

// DeleteSet implements ddm.SetStore.
func (s *Store) DeleteSet(ctx context.Context, name string) error {
	v, done := s.view()
	defer done()
	return v.DeleteSet(ctx, name)
}

// GetSet implements ddm.SetStore.
func (s *Store) GetSet(ctx context.Context, name string) (*ddm.Set, error) {
	v, done := s.view()
	defer done()
	return v.GetSet(ctx, name)
}

// ListSets implements ddm.SetStore.
func (s *Store) ListSets(ctx context.Context, p paging.Page) (paging.Result[ddm.Set], error) {
	v, done := s.view()
	defer done()
	return v.ListSets(ctx, p)
}

// AddSetDeclaration implements ddm.SetStore.
func (s *Store) AddSetDeclaration(ctx context.Context, set, identifier string, at time.Time) (bool, error) {
	v, done := s.view()
	defer done()
	return v.AddSetDeclaration(ctx, set, identifier, at)
}

// RemoveSetDeclaration implements ddm.SetStore.
func (s *Store) RemoveSetDeclaration(ctx context.Context, set, identifier string) (bool, error) {
	v, done := s.view()
	defer done()
	return v.RemoveSetDeclaration(ctx, set, identifier)
}

// SetDeclarations implements ddm.SetStore.
func (s *Store) SetDeclarations(ctx context.Context, set string) ([]string, error) {
	v, done := s.view()
	defer done()
	return v.SetDeclarations(ctx, set)
}

// DeclarationSets implements ddm.SetStore.
func (s *Store) DeclarationSets(ctx context.Context, identifier string) ([]string, error) {
	v, done := s.view()
	defer done()
	return v.DeclarationSets(ctx, identifier)
}
