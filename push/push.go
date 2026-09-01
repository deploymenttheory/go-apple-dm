// Package push wakes managed devices through APNs (decision record 0007).
// A Pusher sends one MDM push per target; Notifier looks targets up in
// storage, sends, and publishes events; Coalesce collapses bursts.
//
// Apple documentation:
//   - https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers
//   - https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens
package push

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// Errors returned by this package.
var (
	ErrNoCertificate = errors.New("push: no push certificate for topic")
	ErrCertExpired   = errors.New("push: push certificate expired")
	ErrInvalidToken  = errors.New("push: device token invalid")
	ErrRateLimited   = errors.New("push: rate limited")
	ErrUpstream      = errors.New("push: APNs error")
)

// Target is one push destination.
type Target struct {
	ID   mdm.EnrollmentID
	Push mdm.Push
}

// Result is the outcome for one target.
type Result struct {
	// Sent is true when APNs accepted the notification.
	Sent bool
	// Invalid is true when APNs said the token is no longer valid; the
	// enrollment should stop being pushed until it re-registers.
	Invalid bool
	// RetryAfter is set when APNs asked to back off.
	RetryAfter time.Duration
	// Status and Reason are the APNs HTTP status and reason string.
	Status int
	Reason string
	// APNSID is the apns-id header of an accepted push.
	APNSID string
	Err    error
}

// Pusher sends MDM pushes.
type Pusher interface {
	// Push sends to every target and returns a Result per enrollment id.
	// The error is for failures that affect the whole batch (no
	// certificate, context cancelled); per-target failures are in Results.
	Push(ctx context.Context, targets []Target) (map[mdm.EnrollmentID]Result, error)
}

// CertStore provides the APNs push certificate for a topic.
type CertStore interface {
	// PushCertificate returns the certificate with its private key. The
	// certificate must contain the topic as its UID.
	PushCertificate(ctx context.Context, topic string) (tls.Certificate, error)
}

// StaticCertStore serves fixed certificates by topic.
type StaticCertStore map[string]tls.Certificate

// PushCertificate implements CertStore.
func (s StaticCertStore) PushCertificate(_ context.Context, topic string) (tls.Certificate, error) {
	c, ok := s[topic]
	if !ok {
		return tls.Certificate{}, fmt.Errorf("%w: %s", ErrNoCertificate, topic)
	}
	return c, nil
}

// Notifier pushes enrollments by id: it looks push info up in storage,
// sends through the Pusher, and publishes PushTokenInvalid for tokens APNs
// rejected.
type Notifier struct {
	Store  storage.PushStore
	Pusher Pusher
	Bus    *event.Bus
	Clock  clock.Clock
}

// Notify pushes the given enrollments. Enrollments without push info
// (disabled or unknown) are skipped and reported with Err set.
func (n *Notifier) Notify(ctx context.Context, ids []mdm.EnrollmentID) (map[mdm.EnrollmentID]Result, error) {
	if n.Store == nil || n.Pusher == nil {
		return nil, errors.New("push: Notifier needs Store and Pusher")
	}
	info, err := n.Store.PushInfo(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("push: %w", err)
	}
	out := map[mdm.EnrollmentID]Result{}
	targets := make([]Target, 0, len(info))
	for _, id := range ids {
		p, ok := info[id]
		if !ok {
			out[id] = Result{Err: fmt.Errorf("%w: no push info for %s", storage.ErrNotFound, id.ID)}
			continue
		}
		targets = append(targets, Target{ID: id, Push: p})
	}
	if len(targets) == 0 {
		return out, nil
	}
	results, err := n.Pusher.Push(ctx, targets)
	if err != nil {
		return out, err
	}
	for id, r := range results {
		out[id] = r
		if r.Invalid && n.Bus != nil {
			at := time.Now()
			if n.Clock != nil {
				at = n.Clock.Now()
			}
			_ = n.Bus.Publish(ctx, event.Event{Type: event.PushTokenInvalid, At: at, Enrollment: id, Actor: "apns", Data: r})
		}
	}
	return out, nil
}

// Coalescer drops repeated pushes to the same enrollment inside a window:
// a device that was just woken will fetch every queued command anyway.
type Coalescer struct {
	next   Pusher
	window time.Duration
	clock  clock.Clock
	mu     sync.Mutex
	last   map[mdm.EnrollmentID]time.Time
}

// Coalesce wraps a Pusher.
func Coalesce(next Pusher, window time.Duration, c clock.Clock) *Coalescer {
	if c == nil {
		c = clock.Real{}
	}
	return &Coalescer{next: next, window: window, clock: c, last: map[mdm.EnrollmentID]time.Time{}}
}

// ErrCoalesced marks a push that was skipped because one was sent recently.
var ErrCoalesced = errors.New("push: coalesced with a recent push")

// Push implements Pusher.
func (c *Coalescer) Push(ctx context.Context, targets []Target) (map[mdm.EnrollmentID]Result, error) {
	now := c.clock.Now()
	out := map[mdm.EnrollmentID]Result{}
	var send []Target
	c.mu.Lock()
	for _, t := range targets {
		if last, ok := c.last[t.ID]; ok && now.Sub(last) < c.window {
			out[t.ID] = Result{Err: ErrCoalesced}
			continue
		}
		c.last[t.ID] = now
		send = append(send, t)
	}
	// Forget entries older than the window so the map does not grow forever.
	for id, at := range c.last {
		if now.Sub(at) >= c.window {
			delete(c.last, id)
		}
	}
	c.mu.Unlock()
	if len(send) == 0 {
		return out, nil
	}
	results, err := c.next.Push(ctx, send)
	for id, r := range results {
		out[id] = r
	}
	return out, err
}
