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

func (t *tx) AssignmentState(_ context.Context, account string) (dep.AssignmentState, error) {
	if account == "" {
		return dep.AssignmentState{}, dep.ErrInvalid
	}
	return t.st.assignmentStates[account], nil
}

func (t *tx) PutAssignmentState(_ context.Context, account string, state dep.AssignmentState) error {
	if account == "" || state.Failures < 0 {
		return dep.ErrInvalid
	}
	t.st.assignmentStates[account] = state
	return nil
}

func (s *Store) MarkFetched(ctx context.Context, account, generation string, serials []string) error {
	return s.Update(ctx, func(tx dep.Tx) error { return tx.MarkFetched(ctx, account, generation, serials) })
}

func (s *Store) AssignmentState(ctx context.Context, account string) (dep.AssignmentState, error) {
	t, done := s.view()
	defer done()
	return t.AssignmentState(ctx, account)
}

func (s *Store) PutAssignmentState(ctx context.Context, account string, state dep.AssignmentState) error {
	return s.Update(ctx, func(tx dep.Tx) error { return tx.PutAssignmentState(ctx, account, state) })
}
