// Package event is the in-process event bus every state change in the
// service layer publishes to. Webhooks, audit logs, metrics, and DDM
// reconcilers are subscribers rather than special cases (decision record
// 0001).
package event

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/mdm"
)

// Type names an event.
type Type string

// Event types published by the service layer.
const (
	Enrolled          Type = "enrolled"       // Authenticate accepted for a new enrollment
	Reenrolled        Type = "reenrolled"     // Authenticate accepted for an existing enrollment
	TokenUpdated      Type = "token-updated"  // TokenUpdate stored
	CheckedOut        Type = "checked-out"    // CheckOut received
	CertRotated       Type = "cert-rotated"   // enrollment identity certificate changed
	CommandQueued     Type = "command-queued" // command enqueued for an enrollment
	CommandSent       Type = "command-sent"   // command delivered to the device
	CommandResult     Type = "command-result" // Acknowledged, Error, CommandFormatError, or NotNow
	BootstrapTokenSet Type = "bootstrap-token-set"
	PushTokenInvalid  Type = "push-token-invalid"
	DDMChanged        Type = "ddm-changed"
	DDMStatusReceived Type = "ddm-status-received"
	// All subscribes to every type.
	All Type = "*"
)

// Event is one occurrence.
type Event struct {
	Type       Type
	At         time.Time
	Enrollment mdm.EnrollmentID
	// Actor is who caused the event: "device", "admin", or a system component.
	Actor string
	// Data carries type-specific detail, for example *mdm.Response for CommandResult.
	Data any
}

// Handler receives events. Returning an error is reported through the bus
// error handler but does not stop other handlers.
type Handler func(ctx context.Context, e Event) error

// Bus dispatches events to subscribers. It is safe for concurrent use.
type Bus struct {
	mu      sync.RWMutex
	subs    map[Type][]*subscription
	async   bool
	onError func(Event, error)
	wg      sync.WaitGroup
	nextID  int
	closed  bool
	closeMu sync.Mutex
}

type subscription struct {
	id int
	h  Handler
}

// Option configures a Bus.
type Option func(*Bus)

// WithAsync dispatches each Publish on its own goroutine; Close waits for
// in-flight deliveries. The default is synchronous delivery in
// subscription order, which keeps tests and audit ordering deterministic.
func WithAsync() Option { return func(b *Bus) { b.async = true } }

// WithErrorHandler receives handler errors. The default drops them.
func WithErrorHandler(f func(Event, error)) Option { return func(b *Bus) { b.onError = f } }

// New creates a bus.
func New(opts ...Option) *Bus {
	b := &Bus{subs: map[Type][]*subscription{}}
	for _, o := range opts {
		o(b)
	}
	return b
}

// ErrClosed is returned by Publish after Close.
var ErrClosed = errors.New("event: bus closed")

// Subscribe registers h for events of type t (or All). The returned
// function removes the subscription.
func (b *Bus) Subscribe(t Type, h Handler) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nextID++
	s := &subscription{id: b.nextID, h: h}
	b.subs[t] = append(b.subs[t], s)
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		list := b.subs[t]
		for i, x := range list {
			if x == s {
				b.subs[t] = append(list[:i:i], list[i+1:]...)
				return
			}
		}
	}
}

// Publish delivers e to subscribers of e.Type and of All. In synchronous
// mode it returns the joined handler errors; in asynchronous mode it
// returns immediately and errors go to the error handler.
func (b *Bus) Publish(ctx context.Context, e Event) error {
	b.closeMu.Lock()
	if b.closed {
		b.closeMu.Unlock()
		return ErrClosed
	}
	if b.async {
		b.wg.Add(1)
		b.closeMu.Unlock()
		go func() {
			defer b.wg.Done()
			_ = b.deliver(ctx, e)
		}()
		return nil
	}
	b.closeMu.Unlock()
	return b.deliver(ctx, e)
}

func (b *Bus) deliver(ctx context.Context, e Event) error {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b.mu.RLock()
	handlers := make([]Handler, 0, len(b.subs[e.Type])+len(b.subs[All]))
	for _, s := range b.subs[e.Type] {
		handlers = append(handlers, s.h)
	}
	for _, s := range b.subs[All] {
		handlers = append(handlers, s.h)
	}
	b.mu.RUnlock()
	var errs []error
	for _, h := range handlers {
		if err := h(ctx, e); err != nil {
			errs = append(errs, err)
			if b.onError != nil {
				b.onError(e, err)
			}
		}
	}
	return errors.Join(errs...)
}

// Close stops accepting events and waits for asynchronous deliveries, or
// until ctx is done.
func (b *Bus) Close(ctx context.Context) error {
	b.closeMu.Lock()
	b.closed = true
	b.closeMu.Unlock()
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
