package eventstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
)

// Sender delivers a projected record to one destination. A receiver may have
// accepted an attempt even if its reply was lost; deduplicate using EventID.
type Sender func(context.Context, eventsink.Record) error

// Worker leases records independently across replicas. Destination identifiers
// must identify immutable receiver configurations, including endpoint changes.
type Worker struct {
	Store        *Store
	Destinations map[string]Sender
	// TransactionalDestinations write only to Store's SQL pool through the
	// supplied context. Their write and delivery acknowledgement commit together.
	// Never register a network sender or a store backed by another pool here.
	TransactionalDestinations map[string]Sender
	Timeout                   time.Duration
	PollInterval              time.Duration
}

// Step handles at most one destination. Empty queues are reported as ErrEmpty;
// transport failures are recorded for retry and are not worker health failures.
func (w *Worker) Step(ctx context.Context) error {
	if w.Store == nil {
		return ErrInvalid
	}
	for destination := range w.TransactionalDestinations {
		if _, duplicate := w.Destinations[destination]; duplicate {
			return ErrInvalid
		}
	}
	timeout := w.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if timeout > 20*time.Minute {
		return ErrInvalid
	}
	d, err := w.Store.Claim(ctx, timeout*2+time.Second)
	if err != nil {
		return err
	}
	if send := w.TransactionalDestinations[d.Destination]; send != nil {
		attempt, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return w.Store.Run(attempt, func(ctx context.Context) error {
			if err := send(ctx, d.Record); err != nil {
				return err
			}
			return w.Store.Finish(ctx, d, "", 0)
		})
	}
	code := "destination-unavailable"
	var delay time.Duration
	if send := w.Destinations[d.Destination]; send != nil {
		attempt, cancel := context.WithTimeout(ctx, timeout)
		err = send(attempt, d.Record)
		cancel()
		code, delay = classify(err, d.Attempts)
	}
	// When the process is stopping, leave the lease for a later worker rather
	// than acknowledge a cancelled attempt whose remote outcome is unknown.
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return w.Store.Finish(ctx, d, code, delay)
}

// Run continues until cancellation. A database error stops the worker so the
// process supervisor and readiness endpoint can expose recording/delivery loss.
func (w *Worker) Run(ctx context.Context) error {
	interval := w.PollInterval
	if interval <= 0 {
		interval = time.Second
	}
	for {
		err := w.Step(ctx)
		if err == nil {
			continue
		}
		if !errors.Is(err, ErrEmpty) && !errors.Is(err, ErrLease) {
			return fmt.Errorf("eventstore: worker: %w", err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func classify(err error, attempt int) (string, time.Duration) {
	if err == nil {
		return "", 0
	}
	delay := time.Second
	for i := 1; i < attempt && delay < time.Hour; i++ {
		delay *= 2
	}
	if delay > time.Hour {
		delay = time.Hour
	}
	var httpErr *eventsink.HTTPError
	if errors.As(err, &httpErr) {
		if httpErr.RetryAfter > delay {
			delay = min(httpErr.RetryAfter, 24*time.Hour)
		}
		switch {
		case httpErr.Status == 408:
			return "http-408", delay
		case httpErr.Status == 429:
			return "http-429", delay
		case httpErr.Status >= 500 && httpErr.Status < 600:
			return "http-5xx", delay
		default:
			return "http-rejected", 0
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout", delay
	}
	return "transport", delay
}
