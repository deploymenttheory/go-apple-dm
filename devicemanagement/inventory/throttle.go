package inventory

import (
	"context"
	"errors"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
)

// accountCooldown survives worker replacement and applies only to the exact
// account incarnation/revision that received the provider rate limit.
type accountCooldown struct {
	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	RetryAt   time.Time `json:"retry_at"`
}

// readyJob checks durable timing before a worker opens credentials or contacts Apple.
func readyJob(ctx context.Context, tx Tx, j Job, now time.Time) (bool, error) {
	if j.RetryAt.After(now) {
		return false, nil
	}
	cooldown, err := get[accountCooldown](ctx, tx, "axm-cooldown/"+j.AccountID)
	if errors.Is(err, ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	account, err := get[Account](ctx, tx, "account/"+j.AccountID)
	if errors.Is(err, ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	matches := cooldown.Revision == account.Revision && cooldown.CreatedAt.Equal(account.CreatedAt) && j.Revision == account.Revision
	return !matches || !cooldown.RetryAt.After(now), nil
}

// deferThrottled preserves work and progress under the existing lease/account
// fence, then releases the worker. Later callbacks can still coalesce or retain
// their already-created successor generation. No remote mutation is resubmitted.
func (r *Repository) deferThrottled(ctx context.Context, j *Job, cause error) error {
	now := time.Now().UTC()
	retry := now.Add(time.Second)
	if limit, ok := errors.AsType[*axm.RateLimitError](cause); ok && limit.RetryAt.After(now) {
		retry = limit.RetryAt
	} else if api, ok := errors.AsType[*axm.Error](cause); ok && api.RetryAfter > 0 {
		retry = now.Add(api.RetryAfter)
	} else if auth, ok := errors.AsType[*axm.AuthError](cause); ok && auth.RetryAfter > 0 {
		retry = now.Add(auth.RetryAfter)
	}
	j.State = "deferred"
	j.RetryAt = retry
	j.Error = "apple_rate_limited"
	j.FinishedAt = nil
	return r.commitJob(ctx, j, func(tx Tx) error {
		account, err := get[Account](ctx, tx, "account/"+j.AccountID)
		if err != nil {
			return err
		}
		deadline := accountCooldown{Revision: account.Revision, CreatedAt: account.CreatedAt, RetryAt: retry}
		previous, err := get[accountCooldown](ctx, tx, "axm-cooldown/"+j.AccountID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil && previous.Revision == deadline.Revision && previous.CreatedAt.Equal(deadline.CreatedAt) && previous.RetryAt.After(deadline.RetryAt) {
			deadline.RetryAt = previous.RetryAt
			j.RetryAt = deadline.RetryAt
		}
		return put(ctx, tx, "axm-cooldown/"+j.AccountID, deadline)
	})
}
