package dep

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// accountBinding identifies the credentials and Apple principal behind a request.
// It deliberately excludes mutable local policy and display metadata.
type accountBinding struct {
	consumerKey, consumerSecret, accessToken, accessSecret string
	serverUUID, orgID                                      string
}

// assignmentCooldown carries only a vendor retry instruction from a rejected response.
// It cannot expose obsolete successes to callers as a valid assignment result.
type assignmentCooldown struct{ delay time.Duration }

// Error identifies the retained retry instruction without including response content.
func (*assignmentCooldown) Error() string { return "dep: stale assignment response requires cooldown" }

// staleResponse preserves throttling instructions while rejecting obsolete business data.
// The worker must independently verify account identity and lease ownership before saving them.
func (c *Client) staleResponse(cause error, status int, header http.Header, body []byte, out any) error {
	if !errors.Is(cause, ErrConflict) {
		return cause
	}
	if status == http.StatusTooManyRequests {
		return errors.Join(cause, newError(status, body, header.Get("Retry-After"), c.cfg.Clock.Now()))
	}
	if _, assignment := out.(*AssignResponse); assignment && status >= 200 && status < 300 {
		var response AssignResponse
		if Unmarshal(body, &response) == nil {
			for _, status := range response.Devices {
				if status == StatusThrottled {
					return errors.Join(cause, &assignmentCooldown{delay: time.Duration(response.RetryAfterSeconds) * time.Second})
				}
			}
		}
	}
	return cause
}

// binding returns a comparable, private snapshot; it must never be logged.
func binding(a *Account) accountBinding {
	return accountBinding{a.ConsumerKey, a.ConsumerSecret, a.AccessToken, a.AccessSecret, a.ServerUUID, a.OrgID}
}

type accountFenceKey struct{}

// withAccountFence binds all remote calls and commits in a worker run to its account.
func withAccountFence(ctx context.Context, a *Account) context.Context {
	return context.WithValue(ctx, accountFenceKey{}, a.Clone())
}

// checkAccountFence prevents a worker from switching identities between requests.
func checkAccountFence(ctx context.Context, a *Account) error {
	if expected, ok := ctx.Value(accountFenceKey{}).(*Account); ok &&
		(expected.Name != a.Name || binding(expected) != binding(a)) {
		return fmt.Errorf("%w: account credentials or identity changed", ErrConflict)
	}
	return nil
}

// lockedAccount serializes a credential-dependent mutation with token replacement.
func lockedAccount(ctx context.Context, tx Tx, expected *Account) (*Account, error) {
	if err := tx.LockAccount(ctx, expected.Name); err != nil {
		return nil, err
	}
	current, err := tx.GetAccount(ctx, expected.Name)
	if err != nil {
		return nil, err
	}
	if binding(current) != binding(expected) {
		return nil, fmt.Errorf("%w: account credentials or identity changed", ErrConflict)
	}
	return current, nil
}

// accountUpdate applies a response only while its credentials still belong to the account.
func (c *Client) accountUpdate(ctx context.Context, expected *Account, fn func(Tx, *Account) error) error {
	return c.cfg.Store.Update(ctx, func(tx Tx) error {
		current, err := lockedAccount(ctx, tx, expected)
		if err != nil {
			return err
		}
		return fn(tx, current)
	})
}
