package appsbooks

import (
	"bytes"
	"context"
	"crypto/sha256"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// NotificationReceipt preserves authenticated original bytes, including unknown
// fields. EventID may be empty for test, inventory and association notifications.
// AppliedJobID distinguishes retained unsolicited evidence from job observations.
type NotificationReceipt struct {
	Notification Notification `json:"notification"`
	Raw          []byte       `json:"raw"`
	EventID      string       `json:"eventId"`
	AppliedJobID string       `json:"appliedJobId"`
	ReceivedAt   time.Time    `json:"receivedAt"`
}

// receiptKey isolates notification identities using bounded location-scoped keys.
func receiptKey(location, id string) string {
	return locationKey(location, fmt.Sprintf("notifications/%x", sha256.Sum256([]byte(id))))
}

// eventReceiptPrefix groups retained callback identities for one opaque Apple event.
// The short namespace leaves room for caller/store key scopes within the 255-byte limit.
func eventReceiptPrefix(location, event string) string {
	return locationKey(location, fmt.Sprintf("inbox/%x/", sha256.Sum256([]byte(event))))
}

// eventReceiptKey indexes a callback without copying its sensitive original body.
func eventReceiptKey(location, event, id string) string {
	return eventReceiptPrefix(location, event) + fmt.Sprintf("%x", sha256.Sum256([]byte(id)))
}

// ReceiveNotification authenticates before accessing payloads and commits the
// receipt, deduplication and matching job changes atomically BEFORE returning nil.
// The host MUST send 2xx only on nil error; otherwise Apple can retry delivery.
// token is a dedicated per-location receiver credential supplied by the host,
// including during Configure's synchronous TEST_NOTIFICATION. This method never
// reads or exposes sTokens and does not own/close r.Body. Unknown types, inventory
// deltas and user-association callbacks are retained without inferred projections.
func (w *Workflow) ReceiveNotification(ctx context.Context, location, token string, r *http.Request) (NotificationReceipt, error) {
	l, err := w.Load(ctx, location)
	if err != nil {
		return NotificationReceipt{}, err
	}
	n, err := DecodeNotification(r, token, l.UID)
	if err != nil {
		return NotificationReceipt{}, err
	}
	eventID, err := notificationEvent(n)
	if err != nil {
		return NotificationReceipt{}, err
	}
	var out NotificationReceipt
	err = w.store.Update(ctx, []string{locationKey(location, "metadata")}, func(tx state.Tx) error {
		if _, err := w.readLocation(ctx, tx, location); err != nil {
			return err
		}
		err := readState(ctx, tx, receiptKey(location, n.ID), &out)
		if err == nil {
			if !bytes.Equal(out.Raw, n.Raw) {
				return ErrProtocol
			}
			return nil
		}
		if !errors.Is(err, state.ErrNotFound) {
			return err
		}
		out = NotificationReceipt{Notification: n, Raw: n.Raw, EventID: eventID, ReceivedAt: tx.Now()}
		if eventID != "" {
			var jobID string
			err := readState(ctx, tx, eventKey(location, eventID), &jobID)
			if err == nil {
				var j LicensingJob
				if err := readState(ctx, tx, jobKey(location, jobID), &j); err != nil {
					return err
				}
				if err := applyJobNotification(&j, n); err != nil {
					return err
				}
				j.UpdatedAt = tx.Now()
				out.AppliedJobID = j.ID
				if err := putState(ctx, tx, jobKey(location, j.ID), j); err != nil {
					return err
				}
			} else if !errors.Is(err, state.ErrNotFound) {
				return err
			}
			if err := putState(ctx, tx, eventReceiptKey(location, eventID, n.ID), n.ID); err != nil {
				return err
			}
		}
		return putState(ctx, tx, receiptKey(location, n.ID), out)
	})
	return out, err
}

// LoadNotification returns privileged retained evidence for audit or explicit
// reconciliation, including callbacks received before event binding or restart.
func (w *Workflow) LoadNotification(ctx context.Context, location, id string) (NotificationReceipt, error) {
	if _, err := w.Load(ctx, location); err != nil {
		return NotificationReceipt{}, err
	}
	var out NotificationReceipt
	err := readState(ctx, w.store, receiptKey(location, id), &out)
	return out, err
}

// notificationEvent checks management payload shape before indexing an event.
func notificationEvent(n Notification) (string, error) {
	switch n.Type {
	case "ASSET_MANAGEMENT":
		var p AssetManagementNotification
		if err := n.Decode(&p); err != nil {
			return "", err
		}
		if p.EventID == "" || p.Type == "" || p.Result == "" {
			return "", ErrProtocol
		}
		return p.EventID, nil
	case "USER_MANAGEMENT":
		var p UserManagementNotification
		if err := n.Decode(&p); err != nil {
			return "", err
		}
		if p.EventID == "" || p.Type == "" || p.Result == "" {
			return "", ErrProtocol
		}
		return p.EventID, nil
	default:
		return "", nil
	}
}

// applyRetainedNotifications binds pre-response callbacks in the same event/job transaction.
func (w *Workflow) applyRetainedNotifications(ctx context.Context, tx state.Tx, j *LicensingJob) error {
	prefix := eventReceiptPrefix(j.LocationID, j.EventID)
	after := ""
	for {
		records, err := tx.List(ctx, prefix, after, 100)
		if err != nil {
			return err
		}
		for _, r := range records {
			var id string
			if json.Unmarshal(r.Value, &id) != nil {
				return ErrProtocol
			}
			var receipt NotificationReceipt
			if err := readState(ctx, tx, receiptKey(j.LocationID, id), &receipt); err != nil {
				return err
			}
			if err := applyJobNotification(j, receipt.Notification); err != nil {
				return err
			}
			receipt.AppliedJobID = j.ID
			if err := putState(ctx, tx, receiptKey(j.LocationID, id), receipt); err != nil {
				return err
			}
			after = r.Key
		}
		if len(records) < 100 {
			return nil
		}
	}
}

// applyJobNotification correlates targets and keeps failures monotonic across unordered callbacks.
func applyJobNotification(j *LicensingJob, n Notification) error {
	if slices.Contains(j.NotificationIDs, n.ID) {
		return nil
	}
	if j.Targets == nil {
		j.Targets = map[string]TargetResult{}
	}
	switch n.Type {
	case "ASSET_MANAGEMENT":
		var p AssetManagementNotification
		if err := n.Decode(&p); err != nil {
			return err
		}
		if p.EventID != j.EventID || p.Type != string(j.Request.Operation) || (j.Request.Assets == nil && j.Request.Revoke == nil) {
			return ErrProtocol
		}
		for _, a := range p.Assignments {
			if !j.expectsAssignment(a) {
				return ErrProtocol
			}
			a := a
			key := targetKey(a)
			old, exists := j.Targets[key]
			if !exists || old.Result == "SUCCESS" {
				j.Targets[key] = TargetResult{Assignment: &a, Result: p.Result}
			}
		}
		if p.Result != "SUCCESS" || len(p.Failures) > 0 {
			j.NotificationFailure = true
		}
	case "USER_MANAGEMENT":
		var p UserManagementNotification
		if err := n.Decode(&p); err != nil {
			return err
		}
		if p.EventID != j.EventID || p.Type != string(j.Request.Operation) || j.Request.Users == nil {
			return ErrProtocol
		}
		for _, u := range p.Users {
			found := false
			for _, want := range j.Request.Users.Users {
				if want.ClientUserID == u.ClientUserID {
					found = true
				}
			}
			if !found {
				return ErrProtocol
			}
			u := u
			key := "user/" + u.ClientUserID
			old, exists := j.Targets[key]
			if !exists || old.Result == "SUCCESS" {
				j.Targets[key] = TargetResult{User: &u, Result: p.Result}
			}
		}
		if p.Result != "SUCCESS" || len(p.Failures) > 0 {
			j.NotificationFailure = true
		}
	default:
		return ErrProtocol
	}
	j.NotificationIDs = append(j.NotificationIDs, n.ID)
	// Contradictory late failure evidence is visible and can only downgrade success.
	if j.NotificationFailure && j.State == JobSucceeded {
		j.State = JobFailed
	}
	return nil
}

// targetKey identifies an asset/target pair independently of callback batch boundaries.
func targetKey(a Assignment) string {
	data, _ := json.Marshal([]string{a.AdamID, a.PricingParam, a.ClientUserID, a.SerialNumber})
	return fmt.Sprintf("assignment/%x", sha256.Sum256(data))
}

// expectsAssignment prevents unrelated users, devices or assets from changing this job.
func (j LicensingJob) expectsAssignment(a Assignment) bool {
	if (a.ClientUserID == "") == (a.SerialNumber == "") {
		return false
	}
	var users, serials []string
	if j.Request.Assets != nil {
		users, serials = j.Request.Assets.ClientUserIDs, j.Request.Assets.SerialNumbers
		if !slices.Contains(j.Request.Assets.Assets, a.Asset) {
			return false
		}
	} else if j.Request.Revoke != nil {
		users, serials = j.Request.Revoke.ClientUserIDs, j.Request.Revoke.SerialNumbers
	} else {
		return false
	}
	return a.ClientUserID != "" && slices.Contains(users, a.ClientUserID) || a.SerialNumber != "" && slices.Contains(serials, a.SerialNumber)
}
