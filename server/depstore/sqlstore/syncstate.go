package sqlstore

import (
	"context"
	"database/sql"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
)

// LockAccount obtains a write lock before decision reads, including on SQLite.
// Callers invoke it only through the transaction view supplied to Update.
func (t *txStore) LockAccount(ctx context.Context, account string) error {
	if err := validName("account name", account); err != nil {
		return err
	}
	if _, err := t.exec(ctx, "lock account", "UPDATE dep_accounts SET name = name WHERE name = ?", account); err != nil {
		return err
	}
	_, err := t.GetAccount(ctx, account)
	return err
}

// MarkFetched records which serials were observed in an account's full-fetch generation.
// Syncers call the transaction form together with device and cursor writes so partial
// fetches cannot trigger reconciliation.
func (t *txStore) MarkFetched(ctx context.Context, account, generation string, serials []string) error {
	if account == "" || generation == "" {
		return dep.ErrInvalid
	}
	for _, serial := range serials {
		if _, err := t.GetDevice(ctx, account, serial); err != nil {
			return err
		}
		if _, err := t.exec(ctx, "mark fetched", "UPDATE dep_devices SET fetch_generation = ? WHERE account = ? AND serial_number = ?", generation, account, serial); err != nil {
			return err
		}
	}
	return nil
}

// AssignmentState returns the account's persisted assignment lease and retry schedule, or
// zero state when none is stored.
func (t *txStore) AssignmentState(ctx context.Context, account string) (dep.AssignmentState, error) {
	var state dep.AssignmentState
	if err := validName("account name", account); err != nil {
		return state, err
	}
	var lease, retry sql.NullTime
	_, err := t.row(ctx, "assignment state", "SELECT owner, lease_until, not_before, failures FROM dep_assignment_state WHERE account = ?", []any{account}, &state.Owner, &lease, &retry, &state.Failures)
	if lease.Valid {
		state.LeaseUntil = lease.Time.UTC()
	}
	if retry.Valid {
		state.NotBefore = retry.Time.UTC()
	}
	return state, err
}

// PutAssignmentState replaces an account's assignment lease and retry schedule. Worker
// transitions call the transaction form after LockAccount to fence overlapping runs.
func (t *txStore) PutAssignmentState(ctx context.Context, account string, state dep.AssignmentState) error {
	if err := validName("account name", account); err != nil {
		return err
	}
	if state.Failures < 0 {
		return dep.ErrInvalid
	}
	cols := []string{"account", "owner", "lease_until", "not_before", "failures"}
	_, err := t.exec(ctx, "put assignment state", t.upsert("dep_assignment_state", cols, cols[:1], nil), account, state.Owner, nullTime(&state.LeaseUntil), nullTime(&state.NotBefore), state.Failures)
	return err
}

// MarkFetched records which serials were observed in an account's full-fetch generation.
// Syncers call the transaction form together with device and cursor writes so partial
// fetches cannot trigger reconciliation.
func (s *Store) MarkFetched(ctx context.Context, account, generation string, serials []string) error {
	return s.write(ctx, func(t *txStore) error { return t.MarkFetched(ctx, account, generation, serials) })
}

// AssignmentState returns the account's persisted assignment lease and retry schedule, or
// zero state when none is stored.
func (s *Store) AssignmentState(ctx context.Context, account string) (dep.AssignmentState, error) {
	return s.view(ctx).AssignmentState(ctx, account)
}

// PutAssignmentState replaces an account's assignment lease and retry schedule. Worker
// transitions call the transaction form after LockAccount to fence overlapping runs.
func (s *Store) PutAssignmentState(ctx context.Context, account string, state dep.AssignmentState) error {
	return s.write(ctx, func(t *txStore) error { return t.PutAssignmentState(ctx, account, state) })
}
