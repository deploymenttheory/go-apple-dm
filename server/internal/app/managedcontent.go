package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

// AppsBooksConfig enables durable content operations with a stable MDM identity.
// Transport overrides support private test services and embedding; credentials are
// registered per location through separately authorized operations.
type AppsBooksConfig struct {
	MDMID      string
	BaseURL    string
	HTTPClient *http.Client
	Resolver   managedcontent.Resolver
	Delivery   managedcontent.Delivery
}

// wireManagedContent composes SDK workflows over the existing sealed state store.
func (a *App) wireManagedContent(ctx context.Context) error {
	cfg := a.cfg.AppsBooks
	if cfg.MDMID == "" {
		return nil
	}
	if a.db == nil || a.keyring == nil {
		return fmt.Errorf("%w: Apps and Books requires sealed SQL storage", ErrConfig)
	}
	st, err := a.protocolState(ctx)
	if err != nil {
		return err
	}
	st = contentState{Store: st, a: a}
	a.AppsBooks, err = appsbooks.NewWorkflow(st, appsbooks.Config{MDMID: cfg.MDMID, BaseURL: cfg.BaseURL, HTTPClient: cfg.HTTPClient, Clock: a.cfg.Clock})
	if err != nil {
		return err
	}
	resolver := cfg.Resolver
	if resolver == nil {
		resolver = contentResolver{a}
	}
	delivery := cfg.Delivery
	if delivery == nil {
		delivery = contentDelivery{a}
	}
	a.ManagedContent, err = managedcontent.New(st, a.AppsBooks, resolver, delivery)
	if err != nil {
		return err
	}
	a.addWorker("managed-content", a.runManagedContent)
	return nil
}

// receiverKey keeps per-location callback credentials separate from safe metadata.
func receiverKey(location string) string {
	sum := sha256.Sum256([]byte(location))
	return "appsbooks/receiver/" + hex.EncodeToString(sum[:])
}

// appsBooksNotification acknowledges only an authenticated, durably committed receipt.
func (a *App) appsBooksNotification(w http.ResponseWriter, r *http.Request) {
	err := event.Run(r.Context(), a.cfg.publisher(), func(ctx context.Context) error {
		var token string
		key := receiverKey(r.PathValue("location"))
		if err := a.protocol.Update(ctx, []string{key}, func(tx state.Tx) error {
			record, err := tx.Get(ctx, key)
			if errors.Is(err, state.ErrNotFound) {
				return appsbooks.ErrNotificationAuth
			}
			if err != nil {
				return err
			}
			token = string(record.Value)
			return nil
		}); err != nil {
			return err
		}
		if _, err := a.AppsBooks.ReceiveNotification(ctx, r.PathValue("location"), token, r); err != nil {
			return err
		}
		// Replays still require a durable access occurrence before acknowledgment.
		return a.cfg.publisher().Publish(ctx, event.Event{Type: "appsbooks-notification", At: a.cfg.Clock.Now(), Actor: "appsbooks-receiver", Data: map[string]any{"Location": key}})
	})
	if err != nil {
		contentError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// runManagedContent resumes only persisted local intent; uncertain submissions
// remain fenced for explicit reconciliation. The SDK owns all remote retry rules.
func (a *App) runManagedContent(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			after := ""
			for {
				operations, next, err := a.ManagedContent.Operations(ctx, after, 100)
				if err != nil {
					return err
				}
				for _, op := range operations {
					if op.Phase == managedcontent.WaitingLicense && op.LicensingJobID != "" {
						_, pollErr := a.AppsBooks.PollJob(ctx, op.Plan.LocationID, op.LicensingJobID, "reference-server", time.Minute)
						if pollErr != nil && !errors.Is(pollErr, appsbooks.ErrPollEarly) && !errors.Is(pollErr, appsbooks.ErrJobBusy) {
							a.cfg.Logger.DebugContext(ctx, "content license polling deferred", "operation", op.ID)
						}
					}
					if op.Phase != managedcontent.Queued && op.Phase != managedcontent.WaitingLicense && op.Phase != managedcontent.Prepared {
						continue
					}
					executionCtx, identityErr := a.contentExecutionContext(ctx, op.ID)
					if identityErr != nil {
						continue
					}
					_, err = a.ManagedContent.Step(executionCtx, op.ID, "reference-server", time.Minute)
					if err != nil && !errors.Is(err, context.Canceled) {
						a.cfg.Logger.DebugContext(ctx, "managed content awaits operator or service", "operation", op.ID)
					}
				}
				if next == "" {
					break
				}
				after = next
			}
		}
	}
}

// saveReceiver explicitly rotates the callback token before a configuration POST,
// so Apple's synchronous TEST_NOTIFICATION can authenticate the proposed receiver.
func (a *App) saveReceiver(ctx context.Context, id, token string) error {
	if token == "" {
		return appsbooks.ErrInput
	}
	key := receiverKey(id)
	return a.protocol.Update(ctx, []string{key}, func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: key, Value: []byte(token)}) })
}

// safeJob excludes raw Apple responses, preflight pages and arbitrary service errors.
func safeJob(j appsbooks.LicensingJob) any {
	return struct {
		ID                  string             `json:"id"`
		LocationID          string             `json:"locationId"`
		State               appsbooks.JobState `json:"state"`
		EventID             string             `json:"eventId"`
		Fence               uint64             `json:"fence"`
		LeaseUntil          time.Time          `json:"leaseUntil"`
		NextPollAt          time.Time          `json:"nextPollAt"`
		NotificationFailure bool               `json:"notificationFailure"`
	}{j.ID, j.LocationID, j.State, j.EventID, j.Fence, j.LeaseUntil, j.NextPollAt, j.NotificationFailure}
}

// decodeContentBody rejects unknown fields and multiple bounded JSON objects.
func decodeContentBody(r *http.Request, v any) error {
	if err := packageJSON(r, v); err != nil {
		return fmt.Errorf("%w: %v", appsbooks.ErrInput, err)
	}
	return nil
}

// contentState joins SDK writes and required event capture on the existing pool.
// SDK reads and remote I/O remain outside this boundary. Only actual Put/Delete
// calls emit an occurrence; an Update used for a consistent read emits none.
type contentState struct {
	state.Store
	a *App
}
type contentTx struct {
	state.Tx
	changed bool
}

// Put records an actual participating SDK write after storage accepts it.
func (t *contentTx) Put(ctx context.Context, r state.Record) error {
	if err := t.Tx.Put(ctx, r); err != nil {
		return err
	}
	t.changed = true
	return nil
}

// Delete records an actual participating SDK removal after storage accepts it.
func (t *contentTx) Delete(ctx context.Context, key string) error {
	if err := t.Tx.Delete(ctx, key); err != nil {
		return err
	}
	t.changed = true
	return nil
}

// Update commits SDK mutations and required safe event capture together.
func (s contentState) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	return event.Run(ctx, s.a.cfg.publisher(), func(ctx context.Context) error {
		return s.Store.Update(ctx, keys, func(tx state.Tx) error {
			tracked := &contentTx{Tx: tx}
			if err := fn(tracked); err != nil {
				return err
			}
			if !tracked.changed {
				return nil
			}
			actor := "managed-content"
			if p, ok := ctx.Value(actorKey{}).(adminauth.Principal); ok {
				actor = p.Name
			}
			return s.a.cfg.publisher().Publish(ctx, event.Event{Type: "managed-content-state", At: tx.Now(), Actor: actor, Data: map[string]any{"Objects": slices.Clone(keys)}})
		})
	})
}
