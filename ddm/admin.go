package ddm

import (
	"context"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// Change reasons recorded on ddm_changes rows.
const (
	ReasonDeclaration = "declaration"
	ReasonSet         = "set"
	ReasonAssignment  = "assignment"
	ReasonTouch       = "touch"
)

// PutDeclaration validates and stores a declaration. changed is false when
// an equivalent declaration was already stored, in which case no enrollment
// is notified. Every affected enrollment is queued for notification inside
// the same transaction.
func (e *Engine) PutDeclaration(ctx context.Context, raw []byte) (*Declaration, bool, error) {
	d, err := ParseDeclaration(raw, e.target(ctx))
	if err != nil {
		return nil, false, err
	}
	if err := e.validatePredicate(d); err != nil {
		return nil, false, err
	}
	now := e.clock.Now()
	d.CreatedAt, d.UpdatedAt = now, now
	var changed bool
	err = e.store.Update(ctx, func(tx Tx) error {
		if prev, err := tx.GetDeclaration(ctx, d.Identifier); err == nil {
			d.CreatedAt = prev.CreatedAt
		}
		var err error
		if changed, err = tx.PutDeclaration(ctx, d); err != nil {
			return err
		}
		if !changed {
			return nil
		}
		return e.recordAffected(ctx, tx, []string{d.Identifier}, nil, ReasonDeclaration, now)
	})
	if err != nil {
		return nil, false, err
	}
	return d, changed, nil
}

// GetDeclaration returns the current revision.
func (e *Engine) GetDeclaration(ctx context.Context, identifier string) (*Declaration, error) {
	return e.store.GetDeclaration(ctx, identifier)
}

// ListDeclarations pages through declarations.
func (e *Engine) ListDeclarations(ctx context.Context, q DeclarationQuery, p storage.Page) (storage.Result[Declaration], error) {
	return e.store.ListDeclarations(ctx, q, p)
}

// DeleteDeclaration removes a declaration and notifies every enrollment that
// had it, so devices receive 404 on their next fetch and drop it.
func (e *Engine) DeleteDeclaration(ctx context.Context, identifier string) error {
	now := e.clock.Now()
	err := e.store.Update(ctx, func(tx Tx) error {
		if err := e.recordAffected(ctx, tx, []string{identifier}, nil, ReasonDeclaration, now); err != nil {
			return err
		}
		return tx.DeleteDeclaration(ctx, identifier)
	})
	if err != nil {
		return err
	}
	return nil
}

// PutSet creates a set.
func (e *Engine) PutSet(ctx context.Context, name string) (bool, error) {
	return e.store.PutSet(ctx, name, e.clock.Now())
}

// DeleteSet removes a set and notifies its enrollments.
func (e *Engine) DeleteSet(ctx context.Context, name string) error {
	now := e.clock.Now()
	err := e.store.Update(ctx, func(tx Tx) error {
		if err := e.recordAffected(ctx, tx, nil, []string{name}, ReasonSet, now); err != nil {
			return err
		}
		return tx.DeleteSet(ctx, name)
	})
	if err != nil {
		return err
	}
	return nil
}

// GetSet returns a set or ErrNotFound.
func (e *Engine) GetSet(ctx context.Context, name string) (*Set, error) {
	return e.store.GetSet(ctx, name)
}

// ListSets pages through sets.
func (e *Engine) ListSets(ctx context.Context, p storage.Page) (storage.Result[Set], error) {
	return e.store.ListSets(ctx, p)
}

// AddToSet adds a declaration to a set and notifies the set's enrollments.
func (e *Engine) AddToSet(ctx context.Context, set, identifier string) (bool, error) {
	return e.setMembership(ctx, set, func(tx Tx) (bool, error) {
		return tx.AddSetDeclaration(ctx, set, identifier, e.clock.Now())
	})
}

// RemoveFromSet removes a declaration from a set and notifies the set's
// enrollments.
func (e *Engine) RemoveFromSet(ctx context.Context, set, identifier string) (bool, error) {
	return e.setMembership(ctx, set, func(tx Tx) (bool, error) {
		return tx.RemoveSetDeclaration(ctx, set, identifier)
	})
}

func (e *Engine) setMembership(ctx context.Context, set string, op func(Tx) (bool, error)) (bool, error) {
	var changed bool
	now := e.clock.Now()
	err := e.store.Update(ctx, func(tx Tx) error {
		var err error
		if changed, err = op(tx); err != nil || !changed {
			return err
		}
		return e.recordAffected(ctx, tx, nil, []string{set}, ReasonSet, now)
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

// SetDeclarations lists a set's members.
func (e *Engine) SetDeclarations(ctx context.Context, set string) ([]string, error) {
	return e.store.SetDeclarations(ctx, set)
}

// DeclarationSets lists the sets containing a declaration.
func (e *Engine) DeclarationSets(ctx context.Context, identifier string) ([]string, error) {
	return e.store.DeclarationSets(ctx, identifier)
}

// AssignSet binds an enrollment to a set and notifies it.
func (e *Engine) AssignSet(ctx context.Context, id mdm.EnrollmentID, set string) (bool, error) {
	return e.assignment(ctx, id, func(tx Tx) (bool, error) { return tx.AssignSet(ctx, id, set, e.clock.Now()) })
}

// UnassignSet removes a set binding and notifies the enrollment.
func (e *Engine) UnassignSet(ctx context.Context, id mdm.EnrollmentID, set string) (bool, error) {
	return e.assignment(ctx, id, func(tx Tx) (bool, error) { return tx.UnassignSet(ctx, id, set) })
}

// AssignDeclaration binds one declaration directly to an enrollment.
func (e *Engine) AssignDeclaration(ctx context.Context, id mdm.EnrollmentID, identifier string) (bool, error) {
	return e.assignment(ctx, id, func(tx Tx) (bool, error) { return tx.AssignDeclaration(ctx, id, identifier, e.clock.Now()) })
}

// UnassignDeclaration removes a direct binding.
func (e *Engine) UnassignDeclaration(ctx context.Context, id mdm.EnrollmentID, identifier string) (bool, error) {
	return e.assignment(ctx, id, func(tx Tx) (bool, error) { return tx.UnassignDeclaration(ctx, id, identifier) })
}

func (e *Engine) assignment(ctx context.Context, id mdm.EnrollmentID, op func(Tx) (bool, error)) (bool, error) {
	if err := id.Validate(); err != nil {
		return false, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	var changed bool
	now := e.clock.Now()
	err := e.store.Update(ctx, func(tx Tx) error {
		var err error
		if changed, err = op(tx); err != nil || !changed {
			return err
		}
		return tx.RecordChanges(ctx, []mdm.EnrollmentID{id}, ReasonAssignment, now)
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

// EnrollmentSets lists an enrollment's sets.
func (e *Engine) EnrollmentSets(ctx context.Context, id mdm.EnrollmentID) ([]string, error) {
	return e.store.EnrollmentSets(ctx, id)
}

// SetEnrollments pages through a set's enrollments.
func (e *Engine) SetEnrollments(ctx context.Context, set string, p storage.Page) (storage.Result[mdm.EnrollmentID], error) {
	return e.store.SetEnrollments(ctx, set, p)
}

// EnrollmentDeclarations lists an enrollment's direct assignments.
func (e *Engine) EnrollmentDeclarations(ctx context.Context, id mdm.EnrollmentID) ([]string, error) {
	return e.store.EnrollmentDeclarations(ctx, id)
}

// Touch queues a notification for enrollments without changing their
// declarations: the first DeclarativeManagement command that enables the
// engine on a device, or a resolver-driven change.
func (e *Engine) Touch(ctx context.Context, ids []mdm.EnrollmentID, reason string) error {
	for _, id := range ids {
		if err := id.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if reason == "" {
		reason = ReasonTouch
	}
	if err := e.store.RecordChanges(ctx, ids, reason, e.clock.Now()); err != nil {
		return err
	}
	return nil
}

// PruneVersions deletes declaration revisions nothing references.
func (e *Engine) PruneVersions(ctx context.Context) (int64, error) { return e.store.PruneVersions(ctx) }

// recordAffected queues every enrollment whose membership includes the
// identifiers or sets.
func (e *Engine) recordAffected(ctx context.Context, tx Tx, identifiers, sets []string, reason string, now time.Time) error {
	ids, err := tx.AffectedEnrollments(ctx, identifiers, sets)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return tx.RecordChanges(ctx, ids, reason, now)
}
