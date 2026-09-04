package push

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
)

// Errors returned by this package.
var (
	ErrNoCertificate = errors.New("push: no push certificate for topic")
	ErrCertExpired   = errors.New("push: push certificate expired")
	ErrInvalidToken  = errors.New("push: device token invalid")
	ErrRejected      = errors.New("push: APNs rejected the request")
	ErrRateLimited   = errors.New("push: rate limited")
	ErrUpstream      = errors.New("push: APNs error")
)

// Outcome classifies what happened to one push. It is a closed set, so a
// caller may switch on it exhaustively and use it as a metric label.
//
// The distinction that matters is between OutcomeInvalidToken and
// OutcomeRejected. The first says this device will never receive a push
// again; the second says this request was wrong, which is usually a
// property of the topic, the certificate, or the environment rather than of
// the device, and so is usually true of every device at once. Collapsing
// them lets one misconfiguration read as a fleet that has gone silent.
type Outcome string

// Push outcomes.
const (
	// OutcomeSent: APNs accepted the notification.
	OutcomeSent Outcome = "sent"
	// OutcomeInvalidToken: this token will not work again. Apple states this
	// only for status 410 ("there is no need to send further pushes to the
	// same device token"), so only 410 produces it.
	OutcomeInvalidToken Outcome = "invalid-token"
	// OutcomeRejected: APNs refused the request and will refuse an identical
	// one, but said nothing about the device. A wrong topic, an expired or
	// mismatched push certificate, the sandbox environment, or a malformed
	// request all land here. It needs an operator, not a retry, and it is
	// not grounds for treating the enrollment as gone.
	OutcomeRejected Outcome = "rejected"
	// OutcomeRateLimited: APNs asked for a pause. RetryAfter carries what it
	// asked for, when it said.
	OutcomeRateLimited Outcome = "rate-limited"
	// OutcomeUnavailable: APNs or the network failed in a way that may
	// succeed on retry.
	OutcomeUnavailable Outcome = "unavailable"
	// OutcomeSkipped: nothing was sent to APNs, because the enrollment has
	// no usable push info or because a Coalescer dropped the push as a
	// duplicate. Err says which.
	OutcomeSkipped Outcome = "skipped"
)

// Outcomes lists every outcome, for exhaustiveness tests and label sets.
var Outcomes = []Outcome{
	OutcomeSent, OutcomeInvalidToken, OutcomeRejected,
	OutcomeRateLimited, OutcomeUnavailable, OutcomeSkipped,
}

// Target is one push destination.
type Target struct {
	ID   mdm.EnrollmentID
	Push mdm.Push
}

// Result is the outcome for one target.
type Result struct {
	// Outcome classifies the result. The zero value is not a valid outcome;
	// a Pusher always sets it.
	Outcome Outcome
	// RetryAfter is what APNs asked to wait, and zero when it asked for
	// nothing. It is not a recommendation this package invents: a caller
	// that wants a floor applies its own, or apns.DefaultRetryAfter.
	RetryAfter time.Duration
	// Status and Reason are the APNs HTTP status and reason string. Reason
	// is one of the values in apns.Reasons when APNs sent one.
	Status int
	Reason string
	// APNSID is the apns-id header of an accepted push.
	APNSID string
	Err    error
}

// Sent reports whether APNs accepted the notification.
func (r Result) Sent() bool { return r.Outcome == OutcomeSent }

// TokenInvalid reports whether APNs said this token will never work again,
// which is the only outcome that justifies giving up on an enrollment.
func (r Result) TokenInvalid() bool { return r.Outcome == OutcomeInvalidToken }

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
			out[t.ID] = Result{Outcome: OutcomeSkipped, Err: ErrCoalesced}
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
	maps.Copy(out, results)
	return out, err
}
