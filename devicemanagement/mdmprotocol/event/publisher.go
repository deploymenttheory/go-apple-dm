package event

import (
	"context"
	"errors"
)

// ErrCapture indicates required recording failed; the associated local mutation
// must roll back and the caller may retry once recording is available.
var ErrCapture = errors.New("event: required capture failed")

// Publisher accepts a typed occurrence. Persistent publishers must return a
// capture error before the associated operation reports success.
type Publisher interface {
	// Publish submits the event to the configured delivery path and reports publication
	// failure.
	Publish(context.Context, Event) error
}

// Coordinator groups local state mutations and required event capture. Nested
// calls join the caller's transaction. Remote requests belong outside Run.
type Coordinator interface {
	// Run commits local state and required event capture together; nested calls join the
	// caller's transaction. The callback must keep remote requests outside this transaction.
	Run(context.Context, func(context.Context) error) error
}

// Run joins a publisher's transaction when it implements Coordinator. A plain
// in-process publisher executes fn directly and provides no persistence promise.
func Run(ctx context.Context, p Publisher, fn func(context.Context) error) error {
	if c, ok := p.(Coordinator); ok {
		return c.Run(ctx, fn)
	}
	return fn(ctx)
}
