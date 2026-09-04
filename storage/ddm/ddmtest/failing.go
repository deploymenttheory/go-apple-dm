package ddmtest

import (
	"context"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/paging"
)

// Failing wraps a Store and returns the configured error from any method
// whose name is in Fail, so callers can test their error paths. Update
// hands fn a Failing-wrapped transaction, so injected failures fire inside
// transactions too.
type Failing struct {
	ddm.Store
	Fail map[string]error
}

func (f *Failing) fail(method string) error {
	if f.Fail == nil {
		return nil
	}
	return f.Fail[method]
}

// txStore adapts a Tx that is not itself a Store, so Failing can wrap it.
type txStore struct {
	ddm.Tx
}

// Update on a transaction view is a nested transaction: invalid.
func (txStore) Update(context.Context, func(ddm.Tx) error) error {
	return ddm.ErrInvalid
}

// Update implements ddm.Store.
func (f *Failing) Update(ctx context.Context, fn func(ddm.Tx) error) error {
	if err := f.fail("Update"); err != nil {
		return err
	}
	return f.Store.Update(ctx, func(tx ddm.Tx) error {
		inner, ok := tx.(ddm.Store)
		if !ok {
			inner = txStore{Tx: tx}
		}
		return fn(&Failing{Store: inner, Fail: f.Fail})
	})
}

// PutDeclaration implements ddm.DeclarationStore.
func (f *Failing) PutDeclaration(ctx context.Context, d *ddm.Declaration) (bool, error) {
	if err := f.fail("PutDeclaration"); err != nil {
		return false, err
	}
	return f.Store.PutDeclaration(ctx, d)
}

// GetDeclaration implements ddm.DeclarationStore.
func (f *Failing) GetDeclaration(ctx context.Context, identifier string) (*ddm.Declaration, error) {
	if err := f.fail("GetDeclaration"); err != nil {
		return nil, err
	}
	return f.Store.GetDeclaration(ctx, identifier)
}

// GetDeclarationVersion implements ddm.DeclarationStore.
func (f *Failing) GetDeclarationVersion(ctx context.Context, identifier, serverToken string) (*ddm.DeclarationVersion, error) {
	if err := f.fail("GetDeclarationVersion"); err != nil {
		return nil, err
	}
	return f.Store.GetDeclarationVersion(ctx, identifier, serverToken)
}

// DeleteDeclaration implements ddm.DeclarationStore.
func (f *Failing) DeleteDeclaration(ctx context.Context, identifier string) error {
	if err := f.fail("DeleteDeclaration"); err != nil {
		return err
	}
	return f.Store.DeleteDeclaration(ctx, identifier)
}

// ListDeclarations implements ddm.DeclarationStore.
func (f *Failing) ListDeclarations(ctx context.Context, q ddm.DeclarationQuery, p paging.Page) (paging.Result[ddm.Declaration], error) {
	if err := f.fail("ListDeclarations"); err != nil {
		return paging.Result[ddm.Declaration]{}, err
	}
	return f.Store.ListDeclarations(ctx, q, p)
}

// PruneVersions implements ddm.DeclarationStore.
func (f *Failing) PruneVersions(ctx context.Context) (int64, error) {
	if err := f.fail("PruneVersions"); err != nil {
		return 0, err
	}
	return f.Store.PruneVersions(ctx)
}

// PutSet implements ddm.SetStore.
func (f *Failing) PutSet(ctx context.Context, name string, at time.Time) (bool, error) {
	if err := f.fail("PutSet"); err != nil {
		return false, err
	}
	return f.Store.PutSet(ctx, name, at)
}

// DeleteSet implements ddm.SetStore.
func (f *Failing) DeleteSet(ctx context.Context, name string) error {
	if err := f.fail("DeleteSet"); err != nil {
		return err
	}
	return f.Store.DeleteSet(ctx, name)
}

// GetSet implements ddm.SetStore.
func (f *Failing) GetSet(ctx context.Context, name string) (*ddm.Set, error) {
	if err := f.fail("GetSet"); err != nil {
		return nil, err
	}
	return f.Store.GetSet(ctx, name)
}

// ListSets implements ddm.SetStore.
func (f *Failing) ListSets(ctx context.Context, p paging.Page) (paging.Result[ddm.Set], error) {
	if err := f.fail("ListSets"); err != nil {
		return paging.Result[ddm.Set]{}, err
	}
	return f.Store.ListSets(ctx, p)
}

// AddSetDeclaration implements ddm.SetStore.
func (f *Failing) AddSetDeclaration(ctx context.Context, set, identifier string, at time.Time) (bool, error) {
	if err := f.fail("AddSetDeclaration"); err != nil {
		return false, err
	}
	return f.Store.AddSetDeclaration(ctx, set, identifier, at)
}

// RemoveSetDeclaration implements ddm.SetStore.
func (f *Failing) RemoveSetDeclaration(ctx context.Context, set, identifier string) (bool, error) {
	if err := f.fail("RemoveSetDeclaration"); err != nil {
		return false, err
	}
	return f.Store.RemoveSetDeclaration(ctx, set, identifier)
}

// SetDeclarations implements ddm.SetStore.
func (f *Failing) SetDeclarations(ctx context.Context, set string) ([]string, error) {
	if err := f.fail("SetDeclarations"); err != nil {
		return nil, err
	}
	return f.Store.SetDeclarations(ctx, set)
}

// DeclarationSets implements ddm.SetStore.
func (f *Failing) DeclarationSets(ctx context.Context, identifier string) ([]string, error) {
	if err := f.fail("DeclarationSets"); err != nil {
		return nil, err
	}
	return f.Store.DeclarationSets(ctx, identifier)
}

// AssignSet implements ddm.AssignmentStore.
func (f *Failing) AssignSet(ctx context.Context, id mdm.EnrollmentID, set string, at time.Time) (bool, error) {
	if err := f.fail("AssignSet"); err != nil {
		return false, err
	}
	return f.Store.AssignSet(ctx, id, set, at)
}

// UnassignSet implements ddm.AssignmentStore.
func (f *Failing) UnassignSet(ctx context.Context, id mdm.EnrollmentID, set string) (bool, error) {
	if err := f.fail("UnassignSet"); err != nil {
		return false, err
	}
	return f.Store.UnassignSet(ctx, id, set)
}

// EnrollmentSets implements ddm.AssignmentStore.
func (f *Failing) EnrollmentSets(ctx context.Context, id mdm.EnrollmentID) ([]string, error) {
	if err := f.fail("EnrollmentSets"); err != nil {
		return nil, err
	}
	return f.Store.EnrollmentSets(ctx, id)
}

// SetEnrollments implements ddm.AssignmentStore.
func (f *Failing) SetEnrollments(ctx context.Context, set string, p paging.Page) (paging.Result[mdm.EnrollmentID], error) {
	if err := f.fail("SetEnrollments"); err != nil {
		return paging.Result[mdm.EnrollmentID]{}, err
	}
	return f.Store.SetEnrollments(ctx, set, p)
}

// AssignDeclaration implements ddm.AssignmentStore.
func (f *Failing) AssignDeclaration(ctx context.Context, id mdm.EnrollmentID, identifier string, at time.Time) (bool, error) {
	if err := f.fail("AssignDeclaration"); err != nil {
		return false, err
	}
	return f.Store.AssignDeclaration(ctx, id, identifier, at)
}

// UnassignDeclaration implements ddm.AssignmentStore.
func (f *Failing) UnassignDeclaration(ctx context.Context, id mdm.EnrollmentID, identifier string) (bool, error) {
	if err := f.fail("UnassignDeclaration"); err != nil {
		return false, err
	}
	return f.Store.UnassignDeclaration(ctx, id, identifier)
}

// EnrollmentDeclarations implements ddm.AssignmentStore.
func (f *Failing) EnrollmentDeclarations(ctx context.Context, id mdm.EnrollmentID) ([]string, error) {
	if err := f.fail("EnrollmentDeclarations"); err != nil {
		return nil, err
	}
	return f.Store.EnrollmentDeclarations(ctx, id)
}

// StaticDeclarations implements ddm.AssignmentStore.
func (f *Failing) StaticDeclarations(ctx context.Context, id mdm.EnrollmentID) ([]ddm.Declaration, error) {
	if err := f.fail("StaticDeclarations"); err != nil {
		return nil, err
	}
	return f.Store.StaticDeclarations(ctx, id)
}

// AffectedEnrollments implements ddm.AssignmentStore.
func (f *Failing) AffectedEnrollments(ctx context.Context, identifiers, sets []string) ([]mdm.EnrollmentID, error) {
	if err := f.fail("AffectedEnrollments"); err != nil {
		return nil, err
	}
	return f.Store.AffectedEnrollments(ctx, identifiers, sets)
}

// PutSnapshot implements ddm.SnapshotStore.
func (f *Failing) PutSnapshot(ctx context.Context, s *ddm.Snapshot) error {
	if err := f.fail("PutSnapshot"); err != nil {
		return err
	}
	return f.Store.PutSnapshot(ctx, s)
}

// Snapshot implements ddm.SnapshotStore.
func (f *Failing) Snapshot(ctx context.Context, id mdm.EnrollmentID) (*ddm.Snapshot, error) {
	if err := f.fail("Snapshot"); err != nil {
		return nil, err
	}
	return f.Store.Snapshot(ctx, id)
}

// PutStatus implements ddm.StatusStore.
func (f *Failing) PutStatus(ctx context.Context, id mdm.EnrollmentID, u ddm.StatusUpdate) (ddm.StatusOutcome, error) {
	if err := f.fail("PutStatus"); err != nil {
		return ddm.StatusOutcome{}, err
	}
	return f.Store.PutStatus(ctx, id, u)
}

// DeclarationStatus implements ddm.StatusStore.
func (f *Failing) DeclarationStatus(ctx context.Context, id mdm.EnrollmentID) ([]ddm.DeclarationStatus, error) {
	if err := f.fail("DeclarationStatus"); err != nil {
		return nil, err
	}
	return f.Store.DeclarationStatus(ctx, id)
}

// DeclarationStatusByIdentifier implements ddm.StatusStore.
func (f *Failing) DeclarationStatusByIdentifier(ctx context.Context, identifier string, p paging.Page) (paging.Result[ddm.EnrollmentDeclarationStatus], error) {
	if err := f.fail("DeclarationStatusByIdentifier"); err != nil {
		return paging.Result[ddm.EnrollmentDeclarationStatus]{}, err
	}
	return f.Store.DeclarationStatusByIdentifier(ctx, identifier, p)
}

// StatusValues implements ddm.StatusStore.
func (f *Failing) StatusValues(ctx context.Context, id mdm.EnrollmentID, q ddm.StatusValueQuery, p paging.Page) (paging.Result[ddm.StatusValue], error) {
	if err := f.fail("StatusValues"); err != nil {
		return paging.Result[ddm.StatusValue]{}, err
	}
	return f.Store.StatusValues(ctx, id, q, p)
}

// StatusErrors implements ddm.StatusStore.
func (f *Failing) StatusErrors(ctx context.Context, id mdm.EnrollmentID, p paging.Page) (paging.Result[ddm.StatusError], error) {
	if err := f.fail("StatusErrors"); err != nil {
		return paging.Result[ddm.StatusError]{}, err
	}
	return f.Store.StatusErrors(ctx, id, p)
}

// StatusReports implements ddm.StatusStore.
func (f *Failing) StatusReports(ctx context.Context, id mdm.EnrollmentID, p paging.Page) (paging.Result[ddm.StatusReportRecord], error) {
	if err := f.fail("StatusReports"); err != nil {
		return paging.Result[ddm.StatusReportRecord]{}, err
	}
	return f.Store.StatusReports(ctx, id, p)
}

// RecordChanges implements ddm.ChangeStore.
func (f *Failing) RecordChanges(ctx context.Context, ids []mdm.EnrollmentID, reason string, at time.Time) error {
	if err := f.fail("RecordChanges"); err != nil {
		return err
	}
	return f.Store.RecordChanges(ctx, ids, reason, at)
}

// PendingChanges implements ddm.ChangeStore.
func (f *Failing) PendingChanges(ctx context.Context, now time.Time, limit int) ([]ddm.Change, error) {
	if err := f.fail("PendingChanges"); err != nil {
		return nil, err
	}
	return f.Store.PendingChanges(ctx, now, limit)
}

// CompleteChanges implements ddm.ChangeStore.
func (f *Failing) CompleteChanges(ctx context.Context, seqs []int64) error {
	if err := f.fail("CompleteChanges"); err != nil {
		return err
	}
	return f.Store.CompleteChanges(ctx, seqs)
}

// FailChanges implements ddm.ChangeStore.
func (f *Failing) FailChanges(ctx context.Context, seqs []int64, msg string, nextAttempt time.Time) error {
	if err := f.fail("FailChanges"); err != nil {
		return err
	}
	return f.Store.FailChanges(ctx, seqs, msg, nextAttempt)
}

// ChangeStats implements ddm.ChangeStore.
func (f *Failing) ChangeStats(ctx context.Context, now time.Time) (int64, int64, error) {
	if err := f.fail("ChangeStats"); err != nil {
		return 0, 0, err
	}
	return f.Store.ChangeStats(ctx, now)
}

// ClearEnrollment implements ddm.Tx.
func (f *Failing) ClearEnrollment(ctx context.Context, id mdm.EnrollmentID) error {
	if err := f.fail("ClearEnrollment"); err != nil {
		return err
	}
	return f.Store.ClearEnrollment(ctx, id)
}
