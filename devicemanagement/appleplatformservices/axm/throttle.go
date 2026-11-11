package axm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultThrottleWait bounds cumulative rate-limit waiting in one Token or API call.
// A longer server cooldown is retained and returned to the caller without an early retry.
const DefaultThrottleWait = 30 * time.Second

// RateLimitError describes throttling independently from OAuth credential rejection.
// Endpoint is "token", "api", or "activity_download". RetryAt is the earliest known
// retry time; RetryAfter is its remaining delay when the error was returned. Err
// preserves the original typed provider failure and, when applicable, cancellation.
// Error omits provider diagnostics, URLs, assertions and tokens.
type RateLimitError struct {
	Endpoint   string
	RetryAfter time.Duration
	RetryAt    time.Time
	Err        error
}

// Error returns a safe description of the rate limit.
func (e *RateLimitError) Error() string {
	return fmt.Sprintf("axm: %s rate limited; retry after %s", e.Endpoint, e.RetryAfter)
}

// Unwrap preserves the provider error and cancellation for errors.Is/As.
func (e *RateLimitError) Unwrap() error { return e.Err }

const (
	tokenThrottle = iota
	apiThrottle
	storageThrottle
)

type cooldown struct {
	until    time.Time
	cause    error
	sequence uint64
	failures int
}

type tokenExchange struct {
	done  chan struct{}
	value string
	err   error
}

type throttleBudget struct{ remaining time.Duration }

// throttleBudget creates an independent wait allowance for one public call.
func (c *Client) throttleBudget() *throttleBudget {
	return &throttleBudget{remaining: c.cfg.Retry.MaxWait}
}

// throttleEndpoint names the isolated provider surface for safe diagnostics.
func throttleEndpoint(bucket int) string {
	switch bucket {
	case tokenThrottle:
		return "token"
	case apiThrottle:
		return "api"
	default:
		return "activity_download"
	}
}

// throttleError snapshots cooldown metadata while the caller holds c.mu.
func (c *Client) throttleError(bucket int) *RateLimitError {
	state := c.cooldowns[bucket]
	delay := state.until.Sub(c.clock.Now())
	if delay < 0 {
		delay = 0
	}
	return &RateLimitError{Endpoint: throttleEndpoint(bucket), RetryAfter: delay, RetryAt: state.until, Err: state.cause}
}

// limit remembers provider guidance even when this request cannot be retried.
func (c *Client) limit(bucket int, after time.Duration, cause error) *RateLimitError {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := &c.cooldowns[bucket]
	if state.failures < 64 {
		state.failures++
	}
	if after <= 0 {
		after = c.backoff(state.failures)
	}
	until := c.clock.Now().Add(after)
	if until.After(state.until) {
		state.until = until
	}
	state.cause = cause
	state.sequence++
	return c.throttleError(bucket)
}

// clearLimit resets fallback escalation after a successful exchange. A successful
// API request already in flight must not erase a newer concurrent rate limit.
func (c *Client) clearLimit(bucket int, observed uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := &c.cooldowns[bucket]
	if state.sequence == observed {
		state.until = time.Time{}
		state.failures = 0
		state.cause = nil
	}
}

// limitSequence snapshots the cooldown generation before a network exchange.
func (c *Client) limitSequence(bucket int) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cooldowns[bucket].sequence
}

// awaitLimit coordinates known cooldowns without holding a lock across a timer.
// A concurrent extension is rechecked before sending. Timer completion is trusted,
// allowing injected clocks to control waiting independently from wall-clock time.
func (c *Client) awaitLimit(ctx context.Context, bucket int, budget *throttleBudget) error {
	for {
		c.mu.Lock()
		state := c.cooldowns[bucket]
		limited := c.throttleError(bucket)
		c.mu.Unlock()
		if err := ctx.Err(); err != nil {
			if state.cause != nil {
				limited.Err = errors.Join(limited.Err, err)
				return limited
			}
			return fmt.Errorf("%w: %w", ErrTransport, err)
		}
		if limited.RetryAfter <= 0 {
			return nil
		}
		if limited.RetryAfter > budget.remaining {
			return limited
		}
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < limited.RetryAfter {
			return limited
		}
		budget.remaining -= limited.RetryAfter
		c.log.DebugContext(ctx, "axm: throttled", "endpoint", limited.Endpoint, "after", limited.RetryAfter)
		select {
		case <-ctx.Done():
			c.mu.Lock()
			limited = c.throttleError(bucket)
			c.mu.Unlock()
			limited.Err = errors.Join(limited.Err, ctx.Err())
			return limited
		case <-c.clock.After(limited.RetryAfter):
		}
		c.mu.Lock()
		current := &c.cooldowns[bucket]
		if current.sequence == state.sequence {
			current.until = time.Time{}
		}
		unchanged := current.sequence == state.sequence
		c.mu.Unlock()
		if unchanged {
			return nil
		}
	}
}

var errRetryBudget = errors.New("axm: retry wait budget exhausted")

// tokenWaitError preserves known throttling if a shared exchange waiter departs.
func (c *Client) tokenWaitError(cause error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cooldowns[tokenThrottle].cause != nil {
		limited := c.throttleError(tokenThrottle)
		limited.Err = errors.Join(limited.Err, cause)
		return limited
	}
	return &AuthError{Err: fmt.Errorf("%w: %w", ErrTransport, cause)}
}
