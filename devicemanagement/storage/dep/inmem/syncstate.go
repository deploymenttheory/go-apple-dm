package inmem

import (
	"context"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
)

// LockAccount runs under the memory store's transaction lock.
func (t *tx) LockAccount(ctx context.Context, account string) error {
	_, err := t.GetAccount(ctx, account)
	return err
}

// MarkFetched records which serials were observed in an account's full-fetch generation.
// Syncers call the transaction form together with device and cursor writes so partial
// fetches cannot trigger reconciliation.
func (t *tx) MarkFetched(_ context.Context, account, generation string, serials []string) error {
	if account == "" || generation == "" {
		return dep.ErrInvalid
	}
	for _, serial := range serials {
		if _, ok := t.st.devices[deviceKey{account, serial}]; !ok {
			return fmt.Errorf("%w: fetched device %s", dep.ErrNotFound, serial)
		}
	}
	for _, serial := range serials {
		t.st.seen[deviceKey{account, serial}] = generation
	}
	return nil
}

// AssignmentState returns the account's persisted assignment lease and retry schedule, or
// zero state when none is stored.
func (t *tx) AssignmentState(_ context.Context, account string) (dep.AssignmentState, error) {
	if account == "" {
		return dep.AssignmentState{}, dep.ErrInvalid
	}
	return t.st.assignmentStates[account], nil
}

// PutAssignmentState replaces an account's assignment lease and retry schedule. Worker
// transitions call the transaction form after LockAccount to fence overlapping runs.
func (t *tx) PutAssignmentState(_ context.Context, account string, state dep.AssignmentState) error {
	if account == "" || state.Failures < 0 {
		return dep.ErrInvalid
	}
	t.st.assignmentStates[account] = state
	return nil
}

// MarkFetched records which serials were observed in an account's full-fetch generation.
// Syncers call the transaction form together with device and cursor writes so partial
// fetches cannot trigger reconciliation.
func (s *Store) MarkFetched(ctx context.Context, account, generation string, serials []string) error {
	return s.Update(ctx, func(tx dep.Tx) error { return tx.MarkFetched(ctx, account, generation, serials) })
}

// AssignmentState returns the account's persisted assignment lease and retry schedule, or
// zero state when none is stored.
func (s *Store) AssignmentState(ctx context.Context, account string) (dep.AssignmentState, error) {
	t, done := s.view()
	defer done()
	return t.AssignmentState(ctx, account)
}

// PutAssignmentState replaces an account's assignment lease and retry schedule. Worker
// transitions call the transaction form after LockAccount to fence overlapping runs.
func (s *Store) PutAssignmentState(ctx context.Context, account string, state dep.AssignmentState) error {
	return s.Update(ctx, func(tx dep.Tx) error { return tx.PutAssignmentState(ctx, account, state) })
}
