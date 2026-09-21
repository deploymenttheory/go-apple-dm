package dep

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"
)

const (
	assignmentLease          = 2 * time.Minute
	assignmentRequestTimeout = time.Minute
)

// assignmentRun is private to one invocation, even when an Assigner is shared.
// Network requests are bounded below the lease duration and no SQL transaction
// is kept open across a request. Every state write rechecks ownership.
type assignmentRun struct {
	a     *Assigner
	owner string
}

// claim acquires the account assignment lease unless another live owner or the persisted
// retry deadline prevents a run.
func (a *Assigner) claim(ctx context.Context) (*assignmentRun, time.Time, error) {
	r := &assignmentRun{a: a, owner: rand.Text()}
	var deadline time.Time
	err := a.cfg.Store.Update(ctx, func(tx Tx) error {
		if err := tx.LockAccount(ctx, a.cfg.Account); err != nil {
			return err
		}
		state, err := tx.AssignmentState(ctx, a.cfg.Account)
		if err != nil {
			return err
		}
		now := a.cfg.Clock.Now()
		deadline = state.NotBefore
		if state.Owner != "" && state.LeaseUntil.After(deadline) {
			deadline = state.LeaseUntil
		}
		if now.Before(deadline) {
			return fmt.Errorf("%w: until %s", ErrBackoff, deadline.UTC().Format(time.RFC3339))
		}
		state.Owner = r.owner
		state.LeaseUntil = now.Add(assignmentLease)
		return tx.PutAssignmentState(ctx, a.cfg.Account, state)
	})
	return r, deadline, err
}

// lock locks the account and verifies that this run still owns an unexpired assignment
// lease.
func (r *assignmentRun) lock(ctx context.Context, tx Tx) (AssignmentState, error) {
	if err := tx.LockAccount(ctx, r.a.cfg.Account); err != nil {
		return AssignmentState{}, err
	}
	state, err := tx.AssignmentState(ctx, r.a.cfg.Account)
	if err != nil {
		return state, err
	}
	if state.Owner != r.owner || !r.a.cfg.Clock.Now().Before(state.LeaseUntil) {
		return state, fmt.Errorf("%w: assignment lease lost", ErrConflict)
	}
	return state, nil
}

// renew extends this run's persisted lease while retaining its ownership fence.
func (r *assignmentRun) renew(ctx context.Context) error {
	return r.a.cfg.Store.Update(ctx, func(tx Tx) error {
		state, err := r.lock(ctx, tx)
		if err != nil {
			return err
		}
		state.LeaseUntil = r.a.cfg.Clock.Now().Add(assignmentLease)
		return tx.PutAssignmentState(ctx, r.a.cfg.Account, state)
	})
}

// throttle persists account-wide assignment backoff while this run owns the lease.
func (r *assignmentRun) throttle(ctx context.Context, retry time.Duration) (time.Time, error) {
	var deadline time.Time
	err := r.a.cfg.Store.Update(ctx, func(tx Tx) error {
		state, err := r.lock(ctx, tx)
		if err != nil {
			return err
		}
		state.Failures++
		if retry <= 0 {
			retry = r.a.cfg.AccountBackoff.Delay(state.Failures)
		}
		deadline = r.a.cfg.Clock.Now().Add(retry)
		state.NotBefore = deadline
		return tx.PutAssignmentState(ctx, r.a.cfg.Account, state)
	})
	return deadline, err
}

// release releases this run's assignment lease without overwriting a replacement owner.
func (r *assignmentRun) release(ctx context.Context, success bool) error {
	return r.a.cfg.Store.Update(ctx, func(tx Tx) error {
		if err := tx.LockAccount(ctx, r.a.cfg.Account); err != nil {
			return err
		}
		state, err := tx.AssignmentState(ctx, r.a.cfg.Account)
		if err != nil {
			return err
		}
		if state.Owner != r.owner {
			return fmt.Errorf("%w: assignment lease lost", ErrConflict)
		}
		state.Owner = ""
		state.LeaseUntil = time.Time{}
		if success {
			state.Failures = 0
			state.NotBefore = time.Time{}
		}
		return tx.PutAssignmentState(ctx, r.a.cfg.Account, state)
	})
}
