package pushnotify

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/v3/clock"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
)

// Notifier pushes enrollments by id: it looks push info up in storage,
// sends through the Pusher, and publishes PushTokenInvalid for tokens APNs
// rejected.
type Notifier struct {
	Store  storage.PushStore
	Pusher push.Pusher
	Bus    *event.Bus
	Clock  clock.Clock
}

// Notify pushes the given enrollments. Enrollments without push info
// (disabled or unknown) are skipped and reported with Err set.
func (n *Notifier) Notify(ctx context.Context, ids []mdm.EnrollmentID) (map[mdm.EnrollmentID]push.Result, error) {
	if n.Store == nil || n.Pusher == nil {
		return nil, errors.New("push: Notifier needs Store and push.Pusher")
	}
	info, err := n.Store.PushInfo(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("pushnotify: %w", err)
	}
	out := map[mdm.EnrollmentID]push.Result{}
	targets := make([]push.Target, 0, len(info))
	for _, id := range ids {
		p, ok := info[id]
		if !ok {
			out[id] = push.Result{Outcome: push.OutcomeSkipped, Err: fmt.Errorf("%w: no push info for %s", storage.ErrNotFound, id.ID)}
			continue
		}
		targets = append(targets, push.Target{ID: id, Push: p})
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
		// A dead token and a refused request are different operational
		// facts, so they are different events: one enrollment is gone, or
		// one deployment is misconfigured for all of them.
		var tp event.Type
		switch r.Outcome {
		case push.OutcomeInvalidToken:
			tp = event.PushTokenInvalid
		case push.OutcomeRejected:
			tp = event.PushRejected
		case push.OutcomeSent, push.OutcomeRateLimited, push.OutcomeUnavailable, push.OutcomeSkipped:
		}
		if tp != "" && n.Bus != nil {
			at := time.Now()
			if n.Clock != nil {
				at = n.Clock.Now()
			}
			_ = n.Bus.Publish(ctx, event.Event{Type: tp, At: at, Enrollment: id, Actor: "apns", Data: r})
		}
	}
	return out, nil
}
