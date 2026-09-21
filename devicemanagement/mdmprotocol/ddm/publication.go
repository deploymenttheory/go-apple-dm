package ddm

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"slices"
)

// SetPublication is the complete desired membership of a server declaration set.
// Publication is a local storage operation, not an Apple protocol object.
// Removing a member never deletes its declaration or retained versions.
type SetPublication struct {
	Name         string
	Declarations []jsontext.Value
}

// PublishResult describes a committed publication, not device installation.
type PublishResult struct {
	Name         string
	Changed      bool
	Declarations []DeclarationRef
}

// PublicationLocker is implemented by transaction views supporting atomic
// publication. The lock must serialize publishers of name until transaction end,
// including the first publication when the set does not yet exist.
type PublicationLocker interface {
	// LockPublication serializes publication of the named set within the current
	// transaction.
	LockPublication(context.Context, string) error
}

// PublishSet validates and atomically replaces a set's contents. Assignments
// survive replacement. Identical publications produce no notification work.
func (e *Engine) PublishSet(ctx context.Context, publication SetPublication) (PublishResult, error) {
	var result PublishResult
	err := e.store.Update(ctx, func(tx Tx) error {
		var err error
		result, err = e.PublishSetTx(ctx, tx, publication)
		return err
	})
	if err != nil {
		return PublishResult{}, err
	}
	return result, nil
}

// PublishSetTx joins the caller's transaction. The caller must roll back on
// error and must not expose the result until commit. This permits administrative
// metadata and declarations to share one unit of work without nested updates.
func (e *Engine) PublishSetTx(ctx context.Context, tx Tx, publication SetPublication) (PublishResult, error) {
	result := PublishResult{Name: publication.Name, Declarations: []DeclarationRef{}}
	if publication.Name == "" {
		return result, fmt.Errorf("%w: empty set name", ErrInvalid)
	}
	decls := make([]*Declaration, 0, len(publication.Declarations))
	seen := map[string]bool{}
	for _, raw := range publication.Declarations {
		d, err := ParseDeclaration(raw, e.target(ctx))
		if err != nil {
			return result, err
		}
		if err := e.validatePredicate(d); err != nil {
			return result, err
		}
		if seen[d.Identifier] {
			return result, fmt.Errorf("%w: duplicate declaration identifier", ErrInvalidDeclaration)
		}
		seen[d.Identifier] = true
		decls = append(decls, d)
	}
	slices.SortFunc(decls, func(a, b *Declaration) int {
		return compareRefs(DeclarationRef{Identifier: a.Identifier}, DeclarationRef{Identifier: b.Identifier})
	})
	lock, ok := tx.(PublicationLocker)
	if !ok {
		return result, fmt.Errorf("%w: store transaction does not support publication locking", ErrInvalid)
	}
	if err := lock.LockPublication(ctx, publication.Name); err != nil {
		return result, err
	}
	now := e.clock.Now()
	created, err := tx.PutSet(ctx, publication.Name, now)
	if err != nil {
		return result, err
	}
	result.Changed = created
	previous, err := tx.SetDeclarations(ctx, publication.Name)
	if err != nil {
		return result, err
	}
	var changedIDs []string
	for _, d := range decls {
		d.CreatedAt, d.UpdatedAt = now, now
		old, err := tx.GetDeclaration(ctx, d.Identifier)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return result, err
		}
		if old != nil {
			d.CreatedAt = old.CreatedAt
		}
		changed, err := tx.PutDeclaration(ctx, d)
		if err != nil {
			return result, err
		}
		if changed {
			changedIDs = append(changedIDs, d.Identifier)
			result.Changed = true
		}
		added, err := tx.AddSetDeclaration(ctx, publication.Name, d.Identifier, now)
		if err != nil {
			return result, err
		}
		result.Changed = result.Changed || added
		result.Declarations = append(result.Declarations, DeclarationRef{Kind: d.Kind, Identifier: d.Identifier, ServerToken: d.ServerToken})
	}
	for _, id := range previous {
		if seen[id] {
			continue
		}
		if _, err := tx.RemoveSetDeclaration(ctx, publication.Name, id); err != nil {
			return result, err
		}
		result.Changed = true
	}
	if result.Changed {
		if err := e.recordAffected(ctx, tx, changedIDs, []string{publication.Name}, ReasonSet, now); err != nil {
			return PublishResult{}, err
		}
	}
	result.Declarations = SortRefs(result.Declarations)
	return result, nil
}
