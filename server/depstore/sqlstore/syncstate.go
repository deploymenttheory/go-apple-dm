package sqlstore

import (
	"context"
	"database/sql"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
)

// LockAccount serializes a name before decision reads, including before account
// creation. ErrNotFound leaves that name locked until the transaction finishes.
func (t *txStore) LockAccount(ctx context.Context, account string) error {
	if err := t.lockAccountName(ctx, account); err != nil {
		return err
	}
	_, err := t.GetAccount(ctx, account)
	return err
}

// lockAccountName acquires a stable transaction lock shared by account and token
// keypair mutations. Lock rows survive account deletion and identity replacement.
func (t *txStore) lockAccountName(ctx context.Context, account string) error {
	if err := validName("account name", account); err != nil {
		return err
	}
	cols := []string{"account"}
	if _, err := t.exec(ctx, "create account lock", t.s.d.InsertIgnore("dep_account_locks", cols, cols), account); err != nil {
		return err
	}
	_, err := t.exec(ctx, "lock account name", "UPDATE dep_account_locks SET account = account WHERE account = ?", account)
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
