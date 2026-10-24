package dep

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
)

// AssignmentPending marks a remote mutation whose result must be read back before
// resubmission. It is committed before the request, including for new targets.
const AssignmentPending = "PENDING"

// markPending durably records uncertainty before making a remote mutation.
func (a *Assigner) markPending(ctx context.Context, run *assignmentRun, uuid string, serials []string) error {
	return a.cfg.Store.Update(ctx, func(tx Tx) error {
		if _, err := run.lock(ctx, tx); err != nil {
			return err
		}
		for _, serial := range serials {
			asg, err := tx.GetAssignment(ctx, a.cfg.Account, serial)
			if errors.Is(err, ErrNotFound) {
				asg = &Assignment{Account: a.cfg.Account, SerialNumber: serial}
			} else if err != nil {
				return err
			}
			asg.ProfileUUID, asg.Status, asg.AttemptedAt = uuid, AssignmentPending, a.cfg.Clock.Now()
			if err := tx.PutAssignment(ctx, asg); err != nil {
				return err
			}
		}
		return nil
	})
}

// reconcilePending inspects uncertain requests before choosing further mutations.
// Missing or inaccessible detail cannot establish a safe retry and fails closed.
func (a *Assigner) reconcilePending(ctx context.Context, run *assignmentRun) error {
	page := paging.Page{Limit: a.cfg.PageSize}
	for {
		result, err := a.cfg.Store.ListAssignments(ctx, a.cfg.Account, AssignmentQuery{Status: AssignmentPending}, page)
		if err != nil {
			return err
		}
		serials := make([]string, 0, len(result.Items))
		for _, asg := range result.Items {
			serials = append(serials, asg.SerialNumber)
		}
		for batch := range slices.Chunk(serials, a.cfg.BatchSize) {
			if err := run.renew(ctx); err != nil {
				return err
			}
			request, cancel := context.WithTimeout(ctx, assignmentRequestTimeout)
			details, err := a.cfg.Client.DeviceDetails(request, a.cfg.Account, batch)
			cancel()
			if err != nil {
				return err
			}
			err = event.Run(ctx, a.cfg.Bus, func(ctx context.Context) error {
				var events []event.Event
				err := a.cfg.Store.Update(ctx, func(tx Tx) error {
					if _, err := run.lock(ctx, tx); err != nil {
						return err
					}
					for _, serial := range batch {
						detail, ok := details[serial]
						if !ok || detail.SerialNumber != serial || (detail.ResponseStatus != "" && detail.ResponseStatus != StatusSuccess) {
							return fmt.Errorf("%w: uncertain assignment readback unavailable", ErrConflict)
						}
						existing, err := tx.GetDevice(ctx, a.cfg.Account, serial)
						if err != nil && !errors.Is(err, ErrNotFound) {
							return err
						}
						if existing != nil && !existing.Deleted {
							detail.OpType, detail.OpDate, detail.ResponseStatus = existing.OpType, existing.OpDate, ""
							if err := tx.PutDevices(ctx, a.cfg.Account, []Device{detail}, a.cfg.Clock.Now()); err != nil {
								return err
							}
						}
						asg, err := tx.GetAssignment(ctx, a.cfg.Account, serial)
						if err != nil {
							return err
						}
						asg.Status, asg.NextAttemptAt = StatusFailed, time.Time{}
						if existing != nil && !existing.Deleted && asg.ProfileUUID == run.account.DesiredProfile(serial) && !NeedsAssignment(detail, asg.ProfileUUID) {
							asg.Status, asg.Attempts, asg.LastError = StatusSuccess, 0, ""
							events = append(events, event.Event{Type: EventDeviceAssigned, At: a.cfg.Clock.Now(), Actor: Actor, Data: AssignmentEvent{Account: a.cfg.Account, Assignment: *asg}})
						}
						if err := tx.PutAssignment(ctx, asg); err != nil {
							return err
						}
					}
					return nil
				})
				if err != nil {
					return err
				}
				for _, ev := range events {
					if a.cfg.Bus != nil {
						if err := a.cfg.Bus.Publish(ctx, ev); err != nil {
							a.cfg.Logger.WarnContext(ctx, "dep: publish", "type", string(ev.Type), "error", err)
						}
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		if result.NextCursor == "" {
			return nil
		}
		page.Cursor = result.NextCursor
	}
}
