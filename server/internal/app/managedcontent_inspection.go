package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// ManagedContentNativeObservation binds a fresh read of retained native facts to
// the actual immutable operation. Raw is privileged source evidence, not a
// client-supplied observation or a request to perform another installation.
type ManagedContentNativeObservation struct {
	OperationID string                     `json:"operationId"`
	Observation managedcontent.Observation `json:"observation"`
}

// ManagedContentStatusQuery identifies explicitly queued native observation work.
// Queue acceptance does not establish query completion or device installation.
type ManagedContentStatusQuery struct {
	OperationID string    `json:"operationId"`
	Commands    []string  `json:"commands"`
	QueuedAt    time.Time `json:"queuedAt"`
}

// inspectContent reads the admitted operation's actual configured native adapter
// without stepping the SDK, claiming a lease, contacting Apple or queueing work.
func (a *App) inspectContent(ctx context.Context, id string) (ManagedContentNativeObservation, error) {
	out := ManagedContentNativeObservation{OperationID: id}
	ctx, err := a.contentExecutionContext(ctx, id)
	if err != nil {
		return out, err
	}
	op, evidence, err := a.ManagedContent.LoadEvidence(ctx, id)
	if err != nil {
		return out, err
	}
	if evidence.Dispatch == nil {
		return out, managedcontent.ErrInput
	}
	if op.SupersededBy != "" {
		return out, managedcontent.ErrConflict
	}
	delivery := a.cfg.AppsBooks.Delivery
	if delivery == nil {
		delivery = contentDelivery{a}
	}
	observation, err := delivery.Inspect(ctx, *evidence.Dispatch)
	if err != nil {
		return out, err
	}
	d := evidence.Dispatch
	if observation.ID != d.ID || observation.Enrollment != d.Enrollment || observation.Method != d.Method || observation.Digest != d.Digest || !observation.Authoritative || !observation.OwnsIdentity || (observation.Found && observation.ObservedAt.IsZero()) {
		return out, managedcontent.ErrProtocol
	}
	out.Observation = observation
	return out, nil
}

// refreshContentStatus atomically queues a bounded, explicitly named native
// query batch over the existing queue. It never licenses, installs, replays a
// dispatch or accepts a caller's replacement target/bundle/payload.
func (a *App) refreshContentStatus(ctx context.Context, id, requestID string) (ManagedContentStatusQuery, error) {
	var out ManagedContentStatusQuery
	if requestID == "" || len(requestID) > 256 || a.cfg.AppsBooks.Delivery != nil {
		return out, managedcontent.ErrInput
	}
	ctx, err := a.contentExecutionContext(ctx, id)
	if err != nil {
		return out, err
	}
	op, evidence, err := a.ManagedContent.LoadEvidence(ctx, id)
	if err != nil {
		return out, err
	}
	if op.Phase != managedcontent.Dispatched || evidence.Dispatch == nil || evidence.Dispatch.Method != managedcontent.MDM || op.SupersededBy != "" {
		return out, managedcontent.ErrReconciliation
	}
	d := *evidence.Dispatch
	if err := a.lockNativeContent(ctx); err != nil {
		return out, err
	}
	if err := a.lockContentEnrollment(ctx, d.Enrollment); err != nil {
		return out, err
	}
	if _, err := (contentResolver{a}).Resolve(ctx, managedcontent.Selection{Enrollment: d.Enrollment, Serial: d.Serial, ClientUserID: d.ClientUserID}); err != nil {
		return out, err
	}
	owned, err := a.protocol.Get(ctx, contentAssetKey(d.Enrollment, d.Content.Asset.AdamID))
	if err != nil {
		return out, err
	}
	var owner nativeContentOwner
	if json.Unmarshal(owned.Value, &owner) != nil || owner.ID != d.ID || owner.Digest != d.Digest {
		return out, managedcontent.ErrConflict
	}
	key := contentServerKey("query", id+"\x00"+requestID)
	err = a.protocol.Update(ctx, []string{key}, func(tx state.Tx) error {
		record, err := tx.Get(ctx, key)
		if err == nil {
			if json.Unmarshal(record.Value, &out) != nil || out.OperationID != id {
				return managedcontent.ErrProtocol
			}
			return nil
		}
		if !errors.Is(err, state.ErrNotFound) {
			return err
		}
		var payloads []commands.Command
		if d.Content.Kind == "Book" {
			payloads = []commands.Command{&commands.ManagedMediaList{}}
		} else {
			rows, err := (contentDelivery{a}).nativeCommands(ctx, d.Enrollment)
			if err != nil {
				return err
			}
			bundle := ""
			for _, q := range rows {
				if q.Command.UUID != d.ID || q.Result == nil {
					continue
				}
				decoded, err := mdm.DecodeResponse(q.Result.Raw, q.Command.RequestType)
				if err != nil {
					return err
				}
				payload, ok := decoded.Payload.(*commands.InstallApplicationResponse)
				if !ok {
					return managedcontent.ErrProtocol
				}
				if payload.Identifier != nil {
					bundle = *payload.Identifier
				}
			}
			if bundle == "" {
				return managedcontent.ErrReconciliation
			}
			payloads = []commands.Command{&commands.InstalledApplicationList{Identifiers: []string{bundle}}, &commands.ManagedApplicationList{Identifiers: []string{bundle}}}
		}
		out = ManagedContentStatusQuery{OperationID: id, QueuedAt: tx.Now()}
		for index, payload := range payloads {
			sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%d", id, requestID, index)))
			uuid := fmt.Sprintf("managed-content-status-%x", sum[:])
			cmd, err := mdm.NewCommand(payload, mdm.WithUUID(uuid))
			if err != nil {
				return err
			}
			result, err := a.Core.Enqueue(ctx, []mdm.EnrollmentID{d.Enrollment}, cmd, storage.EnqueueOptions{DedupeKey: uuid})
			if err != nil {
				return err
			}
			if skipped := result.Skipped[d.Enrollment]; skipped != nil {
				return skipped
			}
			out.Commands = append(out.Commands, uuid)
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if err := tx.Put(ctx, state.Record{Key: key, Value: raw}); err != nil {
			return err
		}
		return a.cfg.publisher().Publish(ctx, event.Event{Type: "managed-content-status-query", At: tx.Now(), Actor: "managed-content", Enrollment: d.Enrollment, Data: map[string]any{"Operation": id, "Count": len(out.Commands)}})
	})
	return out, err
}
