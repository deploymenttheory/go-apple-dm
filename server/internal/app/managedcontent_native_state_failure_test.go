package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// contentNativeStateFailure decorates the actual sealed protocol store, retaining
// its SQL pool, shared unit of work, transaction clock and all unaffected methods.
// The SDK's own captured store and the administrative auth store stay unchanged.
type contentNativeStateFailure struct {
	state.Store
	fail func(method, key string) error
}

// Get delegates retained reads unless the addressed native metadata is unavailable.
func (s contentNativeStateFailure) Get(ctx context.Context, key string) (state.Record, error) {
	if err := s.fail("get", key); err != nil {
		return state.Record{}, err
	}
	return s.Store.Get(ctx, key)
}

// Update preserves the original shared transaction and wraps only its callback's
// read/write methods; it never opens a second pool or acknowledges an early commit.
func (s contentNativeStateFailure) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	for _, key := range keys {
		if err := s.fail("update", key); err != nil {
			return err
		}
	}
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(contentNativeTxFailure{Tx: tx, fail: s.fail}) })
}

type contentNativeTxFailure struct {
	state.Tx
	fail func(method, key string) error
}

// Get preserves real transaction reads while injecting selected metadata outages.
func (s contentNativeTxFailure) Get(ctx context.Context, key string) (state.Record, error) {
	if err := s.fail("tx-get", key); err != nil {
		return state.Record{}, err
	}
	return s.Tx.Get(ctx, key)
}

// Put fails an actual native reservation after preceding SQL work, leaving the
// underlying transaction responsible for rolling back every participating write.
func (s contentNativeTxFailure) Put(ctx context.Context, record state.Record) error {
	if err := s.fail("put", record.Key); err != nil {
		return err
	}
	return s.Tx.Put(ctx, record)
}

// contentNativeMutationCounts counts native delivery occurrences and their
// actual durable outbox rows independently of ordinary SDK admission events.
func contentNativeMutationCounts(t *testing.T, a *App) (int, int) {
	t.Helper()
	var occurrences, deliveries int
	if err := a.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM event_records WHERE type = ?", "managed-content-dispatch").Scan(&occurrences); err != nil {
		t.Fatal(err)
	}
	if err := a.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM event_deliveries d JOIN event_records r ON r.event_id = d.event_id WHERE r.type = ?", "managed-content-dispatch").Scan(&deliveries); err != nil {
		t.Fatal(err)
	}
	return occurrences, deliveries
}

// assertContentNativeRollback verifies a failed first native submission left no
// receipt, scope owner, declaration publication, queued command or dispatch outbox.
// It returns the exact reservation keys for the caller's successful retry proof.
func assertContentNativeRollback(t *testing.T, a *App, fixture *contentFixture, dispatch managedcontent.Dispatch) []string {
	t.Helper()
	keys := []string{deliveryKey(dispatch.ID), contentAssetKey(dispatch.Enrollment, dispatch.Content.Asset.AdamID)}
	for _, doc := range dispatch.Declarations {
		keys = append(keys, contentDeclarationKey(doc.Identifier))
		if _, err := a.Engine.GetDeclaration(t.Context(), doc.Identifier); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatal("native declaration escaped transaction rollback", err)
		}
	}
	for _, key := range keys {
		if _, err := a.protocol.Get(t.Context(), key); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("native reservation escaped transaction rollback", key, err)
		}
	}
	if len(dispatch.Declarations) > 0 {
		if _, err := a.Engine.GetSet(t.Context(), "managedcontent:"+dispatch.Declarations[0].Identifier); !errors.Is(err, ddm.ErrNotFound) {
			t.Fatal("publication set escaped transaction rollback", err)
		}
	}
	rows, err := a.Store.Commands(t.Context(), dispatch.Enrollment, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil || len(rows.Items) != 0 {
		t.Fatal("native queue escaped transaction rollback", err)
	}
	if occurrences, deliveries := contentNativeMutationCounts(t, a); occurrences != 0 || deliveries != 0 || fixture.posts.Load() != 0 {
		t.Fatal("failed native transaction emitted outbox or Apple mutation", occurrences, deliveries, fixture.posts.Load())
	}
	return keys
}

// TestManagedContentNativeReservationFailure exercises native admission at
// distinct transaction stages, with an actual SDK-prepared immutable dispatch.
// Queue/publication and every reservation must roll back before operator retry.
func TestManagedContentNativeReservationFailure(t *testing.T) {
	for _, scenario := range []string{"mdm-receipt-read", "mdm-asset-read", "mdm-receipt-write", "mdm-asset-write", "ddm-receipt-write", "ddm-declaration-write"} {
		t.Run(scenario, func(t *testing.T) {
			a, token, f, id, device, plan := contentBoundaryBench(t)
			declarative := strings.HasPrefix(scenario, "ddm-")
			if declarative {
				if _, err := device.SyncDDM(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := device.PostDDMStatus(t.Context(), true); err != nil {
					t.Fatal(err)
				}
				plan.Method = managedcontent.DDM
			}
			path := "/managed-content/operations/reservation-failure"
			contentFailureReply(t, a, token, "PUT", path, map[string]any{"mode": "install", "plan": plan}, 200)
			original := a.protocol
			var hits atomic.Int32
			method := "put"
			if strings.HasSuffix(scenario, "-read") {
				method = "tx-get"
			}
			match := func(key string) bool {
				switch {
				case strings.Contains(scenario, "asset"):
					return key == contentAssetKey(id, "1")
				case strings.Contains(scenario, "declaration"):
					return strings.HasPrefix(key, "managedcontent/server/declaration/")
				default:
					return strings.HasPrefix(key, "managedcontent/native/")
				}
			}
			a.protocol = contentNativeStateFailure{Store: original, fail: func(op, key string) error {
				if op == method && match(key) {
					hits.Add(1)
					return errors.New("fixture-provider-secret native reservation unavailable")
				}
				return nil
			}}
			contentFailureReply(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, 409)
			a.protocol = original
			if hits.Load() == 0 {
				t.Fatal("test did not reach native reservation failure")
			}
			op, evidence, err := a.ManagedContent.LoadEvidence(t.Context(), "reservation-failure")
			if err != nil || op.Phase != managedcontent.Uncertain || evidence.Dispatch == nil {
				t.Fatal("SDK did not retain reconcilable prepared intent", op.Phase, err)
			}
			dispatch := *evidence.Dispatch
			keys := assertContentNativeRollback(t, a, f, dispatch)
			// Recovery is explicit and follows actual absence; restoring storage alone
			// cannot replay the uncertain operation or change its immutable dispatch.
			contentFailureReply(t, a, token, "POST", path+"/reconcile", contentWorkerInput{Worker: "operator", LeaseSeconds: 60, RetryAbsent: true, Note: "previous worker stopped; native SQL transaction absent"}, 200)
			contentFailureReply(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, 200)
			recovered, private, err := a.ManagedContent.LoadEvidence(t.Context(), "reservation-failure")
			if err != nil || recovered.Phase != managedcontent.Dispatched || private.Dispatch == nil || private.Dispatch.ID != dispatch.ID || private.Dispatch.Digest != dispatch.Digest {
				t.Fatal("recovery changed native intent", err)
			}
			for _, key := range keys {
				if _, err := a.protocol.Get(t.Context(), key); err != nil {
					t.Fatal("explicit retry did not commit native reservation", key, err)
				}
			}
			if occurrences, deliveries := contentNativeMutationCounts(t, a); occurrences != 1 || deliveries == 0 || f.posts.Load() != 0 {
				t.Fatal("explicit retry lacked durable delivery, duplicated admission or licensed content", occurrences, deliveries, f.posts.Load())
			}
		})
	}
}

// TestManagedContentNativeMetadataReadFailure introduces outages only after
// genuine SDK/signed native positive evidence, then checks the actual read-only
// HTTP and generic declaration guard boundaries with healthy authentication.
func TestManagedContentNativeMetadataReadFailure(t *testing.T) {
	a, token, f, _, _, dispatch := contentProviderDDMBench(t)
	original := a.protocol
	before := contentProviderSnapshot(t, a, "ddm-provider")
	requests := f.requests.Load()
	occurrences, deliveries := contentNativeMutationCounts(t, a)
	for _, scenario := range []string{"receipt", "asset-owner", "declaration-owner"} {
		t.Run(scenario, func(t *testing.T) {
			key := deliveryKey(dispatch.ID)
			if scenario == "asset-owner" {
				key = contentAssetKey(dispatch.Enrollment, "1")
			}
			if scenario == "declaration-owner" {
				key = contentDeclarationKey(dispatch.Declarations[0].Identifier)
			}
			var hits atomic.Int32
			a.protocol = contentNativeStateFailure{Store: original, fail: func(method, address string) error {
				if method == "get" && address == key {
					hits.Add(1)
					return errors.New("fixture-provider-secret retained ownership unavailable")
				}
				return nil
			}}
			if scenario == "declaration-owner" {
				contentFailureReply(t, a, token, "DELETE", "/declarations/"+dispatch.Declarations[0].Identifier, nil, 503)
			} else {
				contentFailureReply(t, a, token, "GET", "/managed-content/operations/ddm-provider/native-observation", nil, 503)
			}
			a.protocol = original
			if hits.Load() == 0 {
				t.Fatal("native metadata outage was not exercised")
			}
			if !bytes.Equal(before, contentProviderSnapshot(t, a, "ddm-provider")) {
				t.Fatal("failed ownership read changed SDK state")
			}
		})
	}
	contentFailureReply(t, a, token, "GET", "/managed-content/operations/ddm-provider/native-observation", nil, 200)
	if currentEvents, currentDeliveries := contentNativeMutationCounts(t, a); currentEvents != occurrences || currentDeliveries != deliveries || f.requests.Load() != requests {
		t.Fatal("read outage caused delivery or Apple effects")
	}
}

// TestManagedContentPredecessorMetadataLoss retains an actually terminal native
// predecessor but loses or corrupts its receipt before an explicit successor.
// The asset owner cannot transfer merely because historical ACK evidence exists.
func TestManagedContentPredecessorMetadataLoss(t *testing.T) {
	for _, scenario := range []string{"missing", "stale", "read-unavailable", "nonterminal", "pruned-missing", "pruned-read-unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			a, token, f, id, device, plan := contentBoundaryBench(t)
			first := contentBoundaryStep(t, a, token, "predecessor", plan)
			if scenario != "nonterminal" {
				if _, err := device.Connect(t.Context()); err != nil {
					t.Fatal(err)
				}
				contentFailureReply(t, a, token, "POST", "/managed-content/operations/predecessor/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, 200)
			}
			receipt, err := a.protocol.Get(t.Context(), deliveryKey(first.ID))
			if err != nil {
				t.Fatal(err)
			}
			owner, err := a.protocol.Get(t.Context(), contentAssetKey(id, "1"))
			if err != nil {
				t.Fatal(err)
			}
			original := a.protocol
			pruned := strings.HasPrefix(scenario, "pruned-")
			if pruned {
				// Clear intentionally retains terminal history. Model loss of this
				// single fixture journal row directly; no shared rows are removed.
				result, err := a.db.ExecContext(t.Context(), "DELETE FROM commands WHERE enrollment_id = ? AND command_uuid = ? AND state = ?", id.ID, first.ID, storage.StateAcknowledged)
				if err != nil {
					t.Fatal(err)
				}
				removed, err := result.RowsAffected()
				if err != nil || removed != 1 {
					t.Fatal("owned terminal journal row was not removed", removed, err)
				}
			}
			if strings.HasSuffix(scenario, "missing") {
				if err := original.Update(t.Context(), []string{receipt.Key}, func(tx state.Tx) error { return tx.Delete(t.Context(), receipt.Key) }); err != nil {
					t.Fatal(err)
				}
			} else if scenario == "stale" {
				var saved contentDispatchRecord
				if err := json.Unmarshal(receipt.Value, &saved); err != nil {
					t.Fatal(err)
				}
				saved.Dispatch.Digest = "old-native-generation"
				raw, err := json.Marshal(saved)
				if err != nil {
					t.Fatal(err)
				}
				contentFailurePut(t, a, receipt.Key, raw)
			} else if strings.HasSuffix(scenario, "read-unavailable") {
				a.protocol = contentNativeStateFailure{Store: original, fail: func(method, key string) error {
					if (method == "get" || method == "tx-get") && key == receipt.Key {
						return errors.New("fixture-provider-secret predecessor unavailable")
					}
					return nil
				}}
			}
			// This is the actual native adapter contract after SDK preparation. Its
			// predecessor reference comes from the genuine retained SDK dispatch.
			next := contentBoundaryDispatch(t, id, "next-native-generation", 1)
			next.Predecessor = &managedcontent.DispatchReference{ID: first.ID, Digest: first.Digest, Enrollment: first.Enrollment}
			_, err = (contentDelivery{a}).Submit(t.Context(), next)
			a.protocol = original
			if err == nil {
				t.Fatal("unproven predecessor allowed native handoff")
			}
			current, err := a.protocol.Get(t.Context(), owner.Key)
			if err != nil || !bytes.Equal(owner.Value, current.Value) {
				t.Fatal("failed handoff changed current native owner", err)
			}
			if _, err := a.protocol.Get(t.Context(), deliveryKey(next.ID)); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("failed handoff retained new receipt", err)
			}
			rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
			wantRows := 1
			if pruned {
				wantRows = 0
			}
			if err != nil || len(rows.Items) != wantRows || f.posts.Load() != 0 {
				t.Fatal("failed handoff enqueued or licensed content", err)
			}
			// Restore the original metadata, then confirm existing acceptance remains
			// inspectable. No automatic successor is created on storage recovery.
			contentFailurePut(t, a, receipt.Key, receipt.Value)
			out, err := (contentDelivery{a}).Inspect(t.Context(), first)
			if err != nil || !out.Found || !out.OwnsIdentity {
				t.Fatal("metadata recovery lost original owner", out, err)
			}
			if pruned && out.Terminal {
				t.Fatal("missing journal invented terminal native processing")
			}
		})
	}
}
