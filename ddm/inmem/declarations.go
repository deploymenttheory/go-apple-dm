package inmem

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/deploymenttheory/go-apple-mdm/ddm"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

func copyDeclaration(d ddm.Declaration) ddm.Declaration {
	d.Canonical = bytes.Clone(d.Canonical)
	return d
}

func copyVersion(v ddm.DeclarationVersion) ddm.DeclarationVersion {
	v.Canonical = bytes.Clone(v.Canonical)
	return v
}

// PutDeclaration implements ddm.DeclarationStore. A create stores every
// field as given; an update keeps the stored CreatedAt and takes
// d.UpdatedAt as the time the token changed. Each accepted change records
// the (identifier, token) revision unless it already exists.
func (t *tx) PutDeclaration(_ context.Context, d *ddm.Declaration) (bool, error) {
	if d == nil {
		return false, fmt.Errorf("%w: nil declaration", ddm.ErrInvalid)
	}
	if err := validName("identifier", d.Identifier); err != nil {
		return false, err
	}
	if err := validName("server token", d.ServerToken); err != nil {
		return false, err
	}
	rec := copyDeclaration(*d)
	if cur, ok := t.st.decls[d.Identifier]; ok {
		if cur.Kind != d.Kind {
			return false, fmt.Errorf("%w: declaration %q is kind %q, not %q", ddm.ErrConflict, d.Identifier, cur.Kind, d.Kind)
		}
		if cur.ServerToken == d.ServerToken {
			return false, nil
		}
		rec.CreatedAt = cur.CreatedAt
	}
	t.st.decls[d.Identifier] = rec
	key := versionKey{identifier: d.Identifier, token: d.ServerToken}
	if _, ok := t.st.versions[key]; !ok {
		t.st.versions[key] = ddm.DeclarationVersion{
			Identifier: rec.Identifier, Type: rec.Type, ServerToken: rec.ServerToken,
			Canonical: rec.Canonical, CreatedAt: rec.UpdatedAt,
		}
	}
	return true, nil
}

// GetDeclaration implements ddm.DeclarationStore.
func (t *tx) GetDeclaration(_ context.Context, identifier string) (*ddm.Declaration, error) {
	if err := validName("identifier", identifier); err != nil {
		return nil, err
	}
	d, ok := t.st.decls[identifier]
	if !ok {
		return nil, notFound("declaration", identifier)
	}
	out := copyDeclaration(d)
	return &out, nil
}

// GetDeclarationVersion implements ddm.DeclarationStore.
func (t *tx) GetDeclarationVersion(_ context.Context, identifier, serverToken string) (*ddm.DeclarationVersion, error) {
	if err := validName("identifier", identifier); err != nil {
		return nil, err
	}
	if err := validName("server token", serverToken); err != nil {
		return nil, err
	}
	v, ok := t.st.versions[versionKey{identifier: identifier, token: serverToken}]
	if !ok {
		return nil, fmt.Errorf("%w: declaration %q version %q", ddm.ErrNotFound, identifier, serverToken)
	}
	out := copyVersion(v)
	return &out, nil
}

// DeleteDeclaration implements ddm.DeclarationStore.
func (t *tx) DeleteDeclaration(_ context.Context, identifier string) error {
	if err := validName("identifier", identifier); err != nil {
		return err
	}
	if _, ok := t.st.decls[identifier]; !ok {
		return notFound("declaration", identifier)
	}
	delete(t.st.decls, identifier)
	maps.DeleteFunc(t.st.versions, func(k versionKey, _ ddm.DeclarationVersion) bool { return k.identifier == identifier })
	removeEverywhere(t.st.members, identifier)
	removeEverywhere(t.st.enrollDecls, identifier)
	return nil
}

// ListDeclarations implements ddm.DeclarationStore. The cursor is the last
// identifier of the previous page. An unknown InSet yields an empty page.
func (t *tx) ListDeclarations(_ context.Context, q ddm.DeclarationQuery, p storage.Page) (storage.Result[ddm.Declaration], error) {
	var inSet map[string]struct{}
	if q.InSet != "" {
		if _, ok := t.st.sets[q.InSet]; !ok {
			return storage.Result[ddm.Declaration]{}, nil
		}
		inSet = t.st.members[q.InSet]
	}
	keys := make([]string, 0, len(t.st.decls))
	for id, d := range t.st.decls {
		if q.Kind != "" && d.Kind != q.Kind {
			continue
		}
		if q.Type != "" && d.Type != q.Type {
			continue
		}
		if q.InSet != "" {
			if _, ok := inSet[id]; !ok {
				continue
			}
		}
		keys = append(keys, id)
	}
	return pageByKey(keys, p, func(id string) ddm.Declaration { return copyDeclaration(t.st.decls[id]) }), nil
}

// PruneVersions implements ddm.DeclarationStore.
func (t *tx) PruneVersions(context.Context) (int64, error) {
	keep := map[versionKey]struct{}{}
	for id, d := range t.st.decls {
		keep[versionKey{identifier: id, token: d.ServerToken}] = struct{}{}
	}
	for _, s := range t.st.snapshots {
		for _, it := range s.Items {
			keep[versionKey{identifier: it.Identifier, token: it.BaseToken}] = struct{}{}
		}
	}
	var n int64
	for k := range t.st.versions {
		if _, ok := keep[k]; !ok {
			delete(t.st.versions, k)
			n++
		}
	}
	return n, nil
}

// PutDeclaration implements ddm.DeclarationStore.
func (s *Store) PutDeclaration(ctx context.Context, d *ddm.Declaration) (bool, error) {
	v, done := s.view()
	defer done()
	return v.PutDeclaration(ctx, d)
}

// GetDeclaration implements ddm.DeclarationStore.
func (s *Store) GetDeclaration(ctx context.Context, identifier string) (*ddm.Declaration, error) {
	v, done := s.view()
	defer done()
	return v.GetDeclaration(ctx, identifier)
}

// GetDeclarationVersion implements ddm.DeclarationStore.
func (s *Store) GetDeclarationVersion(ctx context.Context, identifier, serverToken string) (*ddm.DeclarationVersion, error) {
	v, done := s.view()
	defer done()
	return v.GetDeclarationVersion(ctx, identifier, serverToken)
}

// DeleteDeclaration implements ddm.DeclarationStore.
func (s *Store) DeleteDeclaration(ctx context.Context, identifier string) error {
	v, done := s.view()
	defer done()
	return v.DeleteDeclaration(ctx, identifier)
}

// ListDeclarations implements ddm.DeclarationStore.
func (s *Store) ListDeclarations(ctx context.Context, q ddm.DeclarationQuery, p storage.Page) (storage.Result[ddm.Declaration], error) {
	v, done := s.view()
	defer done()
	return v.ListDeclarations(ctx, q, p)
}

// PruneVersions implements ddm.DeclarationStore.
func (s *Store) PruneVersions(ctx context.Context) (int64, error) {
	v, done := s.view()
	defer done()
	return v.PruneVersions(ctx)
}

// declarationsByIdentifier returns copies of the named declarations that
// exist, sorted by identifier.
func (t *tx) declarationsByIdentifier(ids map[string]struct{}) []ddm.Declaration {
	out := make([]ddm.Declaration, 0, len(ids))
	for _, id := range slices.Sorted(maps.Keys(ids)) {
		if d, ok := t.st.decls[id]; ok {
			out = append(out, copyDeclaration(d))
		}
	}
	return out
}
