package eventstore

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlcommon"
)

// ErrCapture requires the associated local mutation to fail and be retried.
var ErrCapture = event.ErrCapture

// Publisher records events before notifying optional in-process subscribers.
// Configure it before use and keep Destinations stable for its lifetime.
type Publisher struct {
	// CaptureAdditional records another JSON-serializable representation in the same transaction.
	// It runs before after-commit notifications and must perform no network I/O.
	CaptureAdditional func(context.Context, event.Event) error
	Store             *Store
	Registry          *eventsink.Registry
	Destinations      []string
	Subscribers       event.Publisher
	Report            func(error)
	mu                sync.Mutex
	health            CaptureHealth
}

// CaptureHealth exposes recording failure without disclosing event contents.
type CaptureHealth struct {
	LastSuccess   time.Time `json:"last_success,omitempty"`
	LastFailure   time.Time `json:"last_failure,omitempty"`
	FailedDenials uint64    `json:"failed_denials"`
}

var (
	_ event.Publisher   = (*Publisher)(nil)
	_ event.Coordinator = (*Publisher)(nil)
)

// Run groups required capture and the caller's local database mutations.
func (p *Publisher) Run(ctx context.Context, fn func(context.Context) error) error {
	if p.Store == nil {
		return sqlcommon.Fail(ctx, ErrCapture)
	}
	return p.Store.Run(ctx, fn)
}

// Health returns a concurrency-safe snapshot of recording health.
func (p *Publisher) Health() CaptureHealth { p.mu.Lock(); defer p.mu.Unlock(); return p.health }

// Publish captures only an allowlisted projection. Security denials are recorded
// in a separate transaction after any failed operation has rolled back. Failure
// to record a denial never changes the authorization decision.
func (p *Publisher) Publish(ctx context.Context, e event.Event) error {
	if p.Store == nil {
		return sqlcommon.Fail(ctx, ErrCapture)
	}
	if e.ID == "" {
		e.ID = rand.Text()
	}
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	reg := p.Registry
	if reg == nil {
		reg = eventsink.Default()
	}
	// Copy the projection now: a caller can release its input after Publish.
	raw, err := json.Marshal(reg.Project(e))
	var rec eventsink.Record
	if err == nil {
		err = json.Unmarshal(raw, &rec)
	}
	if err != nil {
		return sqlcommon.Fail(ctx, fmt.Errorf("%w: projection invalid", ErrCapture))
	}
	if denial(e.Type) {
		if p.CaptureAdditional != nil && e.Data != nil {
			// Denials are deferred until rollback. Preserve the concrete data
			// type and its contents before the publisher releases its input.
			data, err := json.Marshal(e.Data)
			copy := reflect.New(reflect.TypeOf(e.Data))
			if err == nil {
				err = json.Unmarshal(data, copy.Interface())
			}
			if err != nil {
				return sqlcommon.Fail(ctx, fmt.Errorf("%w: additional data invalid", ErrCapture))
			}
			e.Data = copy.Elem().Interface()
		}
		var captureErr error
		sqlcommon.AfterCompletion(ctx, func(outside context.Context, _ bool) {
			bounded, cancel := context.WithTimeout(outside, 10*time.Second)
			defer cancel()
			captureErr = p.capture(bounded, e, rec)
			if captureErr != nil {
				p.mu.Lock()
				p.health.FailedDenials++
				p.mu.Unlock()
			}
		})
		return captureErr
	}
	return p.capture(ctx, e, rec)
}

func (p *Publisher) capture(ctx context.Context, e event.Event, rec eventsink.Record) error {
	err := p.Store.Run(ctx, func(ctx context.Context) error {
		if err := p.Store.Capture(ctx, rec, p.Destinations); err != nil {
			return err
		}
		if p.CaptureAdditional != nil {
			return p.CaptureAdditional(ctx, e)
		}
		return nil
	})
	p.mu.Lock()
	if err != nil {
		p.health.LastFailure = time.Now().UTC()
	}
	p.mu.Unlock()
	if err != nil {
		wrapped := fmt.Errorf("%w: %w", ErrCapture, err)
		if p.Report != nil {
			p.Report(wrapped)
		}
		return sqlcommon.Fail(ctx, wrapped)
	}
	sqlcommon.AfterCommit(ctx, func(context.Context) {
		p.mu.Lock()
		p.health.LastSuccess = time.Now().UTC()
		p.mu.Unlock()
	})
	if p.Subscribers != nil {
		sqlcommon.AfterCommit(ctx, func(outside context.Context) {
			bounded, cancel := context.WithTimeout(outside, 10*time.Second)
			defer cancel()
			if err := p.Subscribers.Publish(bounded, e); err != nil && p.Report != nil {
				p.Report(err)
			}
		})
	}
	return nil
}

func denial(t event.Type) bool {
	switch t {
	case event.EnrollmentDenied, event.IdentityRejected, event.CertificateStatusRejected, event.PrivateHopRejected, event.CertReuseDenied, event.UserAuthFailed, event.AttestationRejected, event.AdminDenied:
		return true
	default:
		return false
	}
}
