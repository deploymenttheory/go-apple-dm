package inmem

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// AssignSet implements ddm.AssignmentStore.
func (t *tx) AssignSet(_ context.Context, id mdm.EnrollmentID, set string, _ time.Time) (bool, error) {
	if err := validID(id); err != nil {
		return false, err
	}
	if err := validName("set name", set); err != nil {
		return false, err
	}
	if _, ok := t.st.sets[set]; !ok {
		return false, notFound("set", set)
	}
	t.st.ids[id.ID] = id
	return add(t.st.enrollSets, id.ID, set), nil
}

// UnassignSet implements ddm.AssignmentStore. An unknown set is simply not
// assigned.
func (t *tx) UnassignSet(_ context.Context, id mdm.EnrollmentID, set string) (bool, error) {
	if err := validID(id); err != nil {
		return false, err
	}
	if err := validName("set name", set); err != nil {
		return false, err
	}
	return remove(t.st.enrollSets, id.ID, set), nil
}

// EnrollmentSets implements ddm.AssignmentStore.
func (t *tx) EnrollmentSets(_ context.Context, id mdm.EnrollmentID) ([]string, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	return sortedKeys(t.st.enrollSets[id.ID]), nil
}

// SetEnrollments implements ddm.AssignmentStore. The cursor is the last
// enrollment id of the previous page.
func (t *tx) SetEnrollments(_ context.Context, set string, p storage.Page) (storage.Result[mdm.EnrollmentID], error) {
	if err := validName("set name", set); err != nil {
		return storage.Result[mdm.EnrollmentID]{}, err
	}
	if _, ok := t.st.sets[set]; !ok {
		return storage.Result[mdm.EnrollmentID]{}, notFound("set", set)
	}
	var keys []string
	for key, sets := range t.st.enrollSets {
		if _, ok := sets[set]; ok {
			keys = append(keys, key)
		}
	}
	return pageByKey(keys, p, func(key string) mdm.EnrollmentID { return t.st.ids[key] }), nil
}

// AssignDeclaration implements ddm.AssignmentStore.
func (t *tx) AssignDeclaration(_ context.Context, id mdm.EnrollmentID, identifier string, _ time.Time) (bool, error) {
	if err := validID(id); err != nil {
		return false, err
	}
	if err := validName("identifier", identifier); err != nil {
		return false, err
	}
	if _, ok := t.st.decls[identifier]; !ok {
		return false, notFound("declaration", identifier)
	}
	t.st.ids[id.ID] = id
	return add(t.st.enrollDecls, id.ID, identifier), nil
}

// UnassignDeclaration implements ddm.AssignmentStore.
func (t *tx) UnassignDeclaration(_ context.Context, id mdm.EnrollmentID, identifier string) (bool, error) {
	if err := validID(id); err != nil {
		return false, err
	}
	if err := validName("identifier", identifier); err != nil {
		return false, err
	}
	return remove(t.st.enrollDecls, id.ID, identifier), nil
}

// EnrollmentDeclarations implements ddm.AssignmentStore.
func (t *tx) EnrollmentDeclarations(_ context.Context, id mdm.EnrollmentID) ([]string, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	return sortedKeys(t.st.enrollDecls[id.ID]), nil
}

// StaticDeclarations implements ddm.AssignmentStore.
func (t *tx) StaticDeclarations(_ context.Context, id mdm.EnrollmentID) ([]ddm.Declaration, error) {
	if err := validID(id); err != nil {
		return nil, err
	}
	return t.declarationsByIdentifier(t.staticIdentifiers(id.ID)), nil
}

// staticIdentifiers is the union of an enrollment's direct assignments and
// the members of its assigned sets.
func (t *tx) staticIdentifiers(key string) map[string]struct{} {
	out := map[string]struct{}{}
	for identifier := range t.st.enrollDecls[key] {
		out[identifier] = struct{}{}
	}
	for set := range t.st.enrollSets[key] {
		for identifier := range t.st.members[set] {
			out[identifier] = struct{}{}
		}
	}
	return out
}

// AffectedEnrollments implements ddm.AssignmentStore.
func (t *tx) AffectedEnrollments(_ context.Context, identifiers, sets []string) ([]mdm.EnrollmentID, error) {
	for _, identifier := range identifiers {
		if err := validName("identifier", identifier); err != nil {
			return nil, err
		}
	}
	for _, set := range sets {
		if err := validName("set name", set); err != nil {
			return nil, err
		}
	}
	// Any set named directly or holding one of the identifiers affects its
	// enrollments; a direct assignment of an identifier does too.
	setNames := map[string]struct{}{}
	for _, set := range sets {
		setNames[set] = struct{}{}
	}
	for _, identifier := range identifiers {
		for name := range t.setsContaining(identifier) {
			setNames[name] = struct{}{}
		}
	}
	out := make([]mdm.EnrollmentID, 0)
	for key, id := range t.st.ids {
		if intersects(t.st.enrollSets[key], setNames) || hasAny(t.st.enrollDecls[key], identifiers) {
			out = append(out, id)
		}
	}
	slices.SortFunc(out, compareEnrollmentIDs)
	return out, nil
}

func compareEnrollmentIDs(a, b mdm.EnrollmentID) int {
	return cmp.Or(cmp.Compare(a.ParentID, b.ParentID), cmp.Compare(a.ID, b.ID))
}

func intersects(a, b map[string]struct{}) bool {
	for k := range a {
		if _, ok := b[k]; ok {
			return true
		}
	}
	return false
}

func hasAny(set map[string]struct{}, keys []string) bool {
	for _, k := range keys {
		if _, ok := set[k]; ok {
			return true
		}
	}
	return false
}

// AssignSet implements ddm.AssignmentStore.
func (s *Store) AssignSet(ctx context.Context, id mdm.EnrollmentID, set string, at time.Time) (bool, error) {
	v, done := s.view()
	defer done()
	return v.AssignSet(ctx, id, set, at)
}

// UnassignSet implements ddm.AssignmentStore.
func (s *Store) UnassignSet(ctx context.Context, id mdm.EnrollmentID, set string) (bool, error) {
	v, done := s.view()
	defer done()
	return v.UnassignSet(ctx, id, set)
}

// EnrollmentSets implements ddm.AssignmentStore.
func (s *Store) EnrollmentSets(ctx context.Context, id mdm.EnrollmentID) ([]string, error) {
	v, done := s.view()
	defer done()
	return v.EnrollmentSets(ctx, id)
}

// SetEnrollments implements ddm.AssignmentStore.
func (s *Store) SetEnrollments(ctx context.Context, set string, p storage.Page) (storage.Result[mdm.EnrollmentID], error) {
	v, done := s.view()
	defer done()
	return v.SetEnrollments(ctx, set, p)
}

// AssignDeclaration implements ddm.AssignmentStore.
func (s *Store) AssignDeclaration(ctx context.Context, id mdm.EnrollmentID, identifier string, at time.Time) (bool, error) {
	v, done := s.view()
	defer done()
	return v.AssignDeclaration(ctx, id, identifier, at)
}

// UnassignDeclaration implements ddm.AssignmentStore.
func (s *Store) UnassignDeclaration(ctx context.Context, id mdm.EnrollmentID, identifier string) (bool, error) {
	v, done := s.view()
	defer done()
	return v.UnassignDeclaration(ctx, id, identifier)
}

// EnrollmentDeclarations implements ddm.AssignmentStore.
func (s *Store) EnrollmentDeclarations(ctx context.Context, id mdm.EnrollmentID) ([]string, error) {
	v, done := s.view()
	defer done()
	return v.EnrollmentDeclarations(ctx, id)
}

// StaticDeclarations implements ddm.AssignmentStore.
func (s *Store) StaticDeclarations(ctx context.Context, id mdm.EnrollmentID) ([]ddm.Declaration, error) {
	v, done := s.view()
	defer done()
	return v.StaticDeclarations(ctx, id)
}

// AffectedEnrollments implements ddm.AssignmentStore.
func (s *Store) AffectedEnrollments(ctx context.Context, identifiers, sets []string) ([]mdm.EnrollmentID, error) {
	v, done := s.view()
	defer done()
	return v.AffectedEnrollments(ctx, identifiers, sets)
}
