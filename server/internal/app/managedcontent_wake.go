package app

import (
	"context"
	"errors"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/server/eventsink"
)

const contentWakeDestination = "managed-content-wake"

// contentWakeDestinations selects durable wake delivery only for newly committed
// MDM content dispatches and explicitly queued native query batches. DDM already
// uses the existing declarative change journal/notifier.
func contentWakeDestinations(record eventsink.Record) []string {
	if record.ID == "" {
		return nil
	}
	if record.Type == "managed-content-status-query" || (record.Type == "managed-content-dispatch" && record.Fields["Method"] == "mdm") {
		return []string{contentWakeDestination}
	}
	return nil
}

// sendContentWake is an ordinary external outbox destination. The existing
// leased retry worker invokes it only after native queue/receipt/audit commit,
// without holding a SQL transaction or enrollment row lock across APNs.
func (a *App) sendContentWake(ctx context.Context, record eventsink.Record) error {
	if a.Push == nil {
		return nil
	}
	var channel mdm.Channel
	for candidate := mdm.ChannelDevice; candidate.Valid(); candidate++ {
		if candidate.String() == record.Channel {
			channel = candidate
			break
		}
	}
	id := mdm.EnrollmentID{Channel: channel, ID: record.ID, ParentID: record.Parent}
	if !channel.Valid() || id.ID == "" {
		return &eventsink.HTTPError{Status: 400}
	}
	results, err := a.Push.Notify(ctx, []mdm.EnrollmentID{id})
	if err != nil {
		return errors.New("managed content wake unavailable")
	}
	result, ok := results[id]
	if !ok {
		return errors.New("managed content wake outcome unavailable")
	}
	switch result.Outcome {
	case push.OutcomeSent, push.OutcomeInvalidToken, push.OutcomeSkipped:
		return nil
	case push.OutcomeRejected:
		return &eventsink.HTTPError{Status: 400}
	default:
		return errors.New("managed content wake retry required")
	}
}
