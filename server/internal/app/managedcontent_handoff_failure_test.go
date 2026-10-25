package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/ddmsync"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// TestManagedContentDDMNativeSuccessor exercises the SDK's same-method handoff
// against signed owned status and the actual declaration publication store.
func TestManagedContentDDMNativeSuccessor(t *testing.T) {
	for _, competing := range []bool{false, true} {
		name := "owned-handoff"
		if competing {
			name = "foreign-declaration"
		}
		t.Run(name, func(t *testing.T) {
			a, token, f, id, _, first := contentProviderDDMBench(t)
			path := "/managed-content/operations/ddm-provider"
			contentFailureReply(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, 200)
			old, err := a.ManagedContent.Load(t.Context(), "ddm-provider")
			if err != nil || old.Observation == nil || !old.Observation.Terminal {
				t.Fatal("signed predecessor processing missing", old, err)
			}
			ownerKey := contentAssetKey(id, "1")
			ownerBefore, err := a.protocol.Get(t.Context(), ownerKey)
			if err != nil {
				t.Fatal(err)
			}
			if competing {
				// This models independently embedded publication, outside the HTTP
				// administration guard. Native admission must still reject it.
				raw := []byte(`{"Identifier":"foreign-competing-app","Type":"com.apple.configuration.app.managed","Payload":{"AppStoreID":"1","InstallBehavior":{"Install":"Required","License":{"Assignment":"Device"}}}}`)
				if _, _, err := a.Engine.PutDeclaration(t.Context(), raw); err != nil {
					t.Fatal(err)
				}
				if _, err := a.Engine.AssignDeclaration(t.Context(), id, "foreign-competing-app"); err != nil {
					t.Fatal(err)
				}
			}
			plan := old.Plan
			plan.PredecessorID = old.ID
			contentFailureReply(t, a, token, "PUT", "/managed-content/operations/ddm-next", map[string]any{"mode": "install", "plan": plan}, 200)
			want := 200
			if competing {
				want = 409
			}
			contentFailureReply(t, a, token, "POST", "/managed-content/operations/ddm-next/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, want)
			next, evidence, err := a.ManagedContent.LoadEvidence(t.Context(), "ddm-next")
			if err != nil || evidence.Dispatch == nil {
				t.Fatal("successor lost immutable dispatch", err)
			}
			dispatch := *evidence.Dispatch
			ownerAfter, err := a.protocol.Get(t.Context(), ownerKey)
			if err != nil {
				t.Fatal(err)
			}
			if competing {
				if next.Phase != managedcontent.Uncertain || !bytes.Equal(ownerBefore.Value, ownerAfter.Value) {
					t.Fatal("foreign declaration did not fence native ownership", next.Phase)
				}
				if _, err := a.protocol.Get(t.Context(), deliveryKey(dispatch.ID)); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("denied successor retained a dispatch receipt", err)
				}
			} else {
				if next.Phase != managedcontent.Dispatched || dispatch.ID == first.ID || dispatch.Digest == first.Digest || dispatch.Predecessor == nil || dispatch.Predecessor.ID != first.ID || dispatch.Predecessor.Digest != first.Digest {
					t.Fatal("native successor identity did not advance", next.Phase, dispatch.Predecessor)
				}
				var owner nativeContentOwner
				if err := json.Unmarshal(ownerAfter.Value, &owner); err != nil || owner.ID != dispatch.ID || owner.Digest != dispatch.Digest {
					t.Fatal("native owner did not transfer", owner, err)
				}
				current, err := a.ManagedContent.Load(t.Context(), old.ID)
				if err != nil || current.SupersededBy != next.ID {
					t.Fatal("predecessor was not superseded", current, err)
				}
				if _, err := (contentDelivery{a}).Inspect(t.Context(), first); !errors.Is(err, managedcontent.ErrConflict) {
					t.Fatal("predecessor retained native ownership", err)
				}
			}
			if len(dispatch.Declarations) != len(first.Declarations) {
				t.Fatal("successor changed declaration count")
			}
			for i, doc := range first.Declarations {
				// Same-plan reinstall has identical content-addressed tokens; a new
				// owner must not manufacture token changes for unchanged bytes.
				if dispatch.Declarations[i].Identifier != doc.Identifier || dispatch.Declarations[i].ServerToken != doc.ServerToken || !bytes.Equal(dispatch.Declarations[i].Raw, doc.Raw) {
					t.Fatal("same-plan handoff replaced owned declaration identity")
				}
				if !competing {
					record, err := a.protocol.Get(t.Context(), contentDeclarationKey(doc.Identifier))
					var owner nativeContentOwner
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal(record.Value, &owner); err != nil || owner.ID != dispatch.ID || owner.Digest != dispatch.Digest {
						t.Fatal("declaration owner did not transfer", owner, err)
					}
				}
				decl, err := a.Engine.GetDeclaration(t.Context(), doc.Identifier)
				if err != nil || decl.ServerToken != doc.ServerToken {
					t.Fatal("native publication lost the owned declaration", err)
				}
			}
			if f.posts.Load() != 0 {
				t.Fatal("DDM handoff licensed content")
			}
		})
	}
}

// contentMutationQueue fails only the actual native enqueue call. Reads and
// transaction participation retain the original application's SQL store.
type contentMutationQueue struct {
	storage.Store
	hits *atomic.Int32
}

func (s contentMutationQueue) Enqueue(context.Context, []mdm.EnrollmentID, *mdm.Command, storage.EnqueueOptions) (storage.EnqueueResult, error) {
	s.hits.Add(1)
	return storage.EnqueueResult{}, errors.New("fixture-provider-secret enqueue unavailable")
}

// contentMutationDDM wraps the existing transaction, including its publication
// lock capability, so injected writes cannot escape into a separate pool.
type contentMutationDDM struct {
	ddm.Store
	method string
	hits   *atomic.Int32
}

func (s contentMutationDDM) Update(ctx context.Context, fn func(ddm.Tx) error) error {
	return s.Store.Update(ctx, func(tx ddm.Tx) error {
		return fn(contentMutationDDMTx{Tx: tx, method: s.method, hits: s.hits})
	})
}

type contentMutationDDMTx struct {
	ddm.Tx
	method string
	hits   *atomic.Int32
}

func (s contentMutationDDMTx) LockPublication(ctx context.Context, name string) error {
	locker, ok := s.Tx.(ddm.PublicationLocker)
	if !ok {
		return ddm.ErrInvalid
	}
	return locker.LockPublication(ctx, name)
}

func (s contentMutationDDMTx) PutDeclaration(ctx context.Context, d *ddm.Declaration) (bool, error) {
	if s.method == "PutDeclaration" {
		s.hits.Add(1)
		return false, errors.New("fixture-provider-secret publication unavailable")
	}
	return s.Tx.PutDeclaration(ctx, d)
}

func (s contentMutationDDMTx) AssignSet(ctx context.Context, id mdm.EnrollmentID, set string, at time.Time) (bool, error) {
	if s.method == "AssignSet" {
		s.hits.Add(1)
		return false, errors.New("fixture-provider-secret assignment unavailable")
	}
	return s.Tx.AssignSet(ctx, id, set, at)
}

// TestManagedContentNativeMutationProviderFailure proves native queue,
// publication and assignment errors roll back before explicit same-ID recovery.
func TestManagedContentNativeMutationProviderFailure(t *testing.T) {
	for _, method := range []string{"Enqueue", "PutDeclaration", "AssignSet"} {
		t.Run(method, func(t *testing.T) {
			a, token, f, id, device, plan := contentBoundaryBench(t)
			if method != "Enqueue" {
				if _, err := device.SyncDDM(t.Context()); err != nil {
					t.Fatal(err)
				}
				if err := device.PostDDMStatus(t.Context(), true); err != nil {
					t.Fatal(err)
				}
				plan.Method = managedcontent.DDM
			}
			path := "/managed-content/operations/mutation-failure"
			contentFailureReply(t, a, token, "PUT", path, map[string]any{"mode": "install", "plan": plan}, 200)
			core, engine := a.Core, a.Engine
			var hits atomic.Int32
			if method == "Enqueue" {
				var err error
				// Enqueue uses the supported Core storage seam, with the same
				// target validation, event publisher, clock and SQL pool.
				a.Core, err = service.New(service.Config{Store: contentMutationQueue{Store: a.Store, hits: &hits}, Bus: a.cfg.publisher(), Clock: a.cfg.Clock, Logger: a.cfg.Logger, Hooks: []service.Hook{ddmsync.NewServiceHook(engine, a.Store, a.cfg.Logger)}})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				var err error
				a.Engine, err = ddm.New(ddm.Config{Store: contentMutationDDM{Store: engine.Store(), method: method, hits: &hits}, Clock: a.cfg.Clock})
				if err != nil {
					t.Fatal(err)
				}
			}
			contentFailureReply(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, 409)
			a.Core, a.Engine = core, engine
			if hits.Load() != 1 {
				t.Fatal("native provider seam was not reached exactly once", hits.Load())
			}
			op, evidence, err := a.ManagedContent.LoadEvidence(t.Context(), "mutation-failure")
			if err != nil || op.Phase != managedcontent.Uncertain || evidence.Dispatch == nil {
				t.Fatal("failed native mutation lost reconcilable intent", op.Phase, err)
			}
			dispatch := *evidence.Dispatch
			keys := []string{deliveryKey(dispatch.ID), contentAssetKey(id, "1")}
			for _, doc := range dispatch.Declarations {
				keys = append(keys, contentDeclarationKey(doc.Identifier))
				if _, err := a.Engine.GetDeclaration(t.Context(), doc.Identifier); !errors.Is(err, ddm.ErrNotFound) {
					t.Fatal("publication escaped failed native transaction", err)
				}
			}
			for _, key := range keys {
				if _, err := a.protocol.Get(t.Context(), key); !errors.Is(err, state.ErrNotFound) {
					t.Fatal("native reservation escaped failed mutation", key, err)
				}
			}
			if len(dispatch.Declarations) > 0 {
				set := "managedcontent:" + dispatch.Declarations[0].Identifier
				if _, err := a.Engine.GetSet(t.Context(), set); !errors.Is(err, ddm.ErrNotFound) {
					t.Fatal("failed assignment retained publication set", err)
				}
				sets, err := a.Engine.EnrollmentSets(t.Context(), id)
				if err != nil || len(sets) != 0 {
					t.Fatal("failed assignment retained enrollment membership", sets, err)
				}
			}
			rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
			if err != nil || len(rows.Items) != 0 {
				t.Fatal("failed native mutation retained a command", err)
			}
			if captures, deliveries := contentNativeMutationCounts(t, a); captures != 0 || deliveries != 0 || f.posts.Load() != 0 {
				t.Fatal("failed mutation emitted native capture or remote effects", captures, deliveries)
			}
			contentFailureReply(t, a, token, "POST", path+"/reconcile", contentWorkerInput{Worker: "operator", LeaseSeconds: 60, RetryAbsent: true, Note: "original worker stopped; native transaction rolled back"}, 200)
			contentFailureReply(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, 200)
			current, private, err := a.ManagedContent.LoadEvidence(t.Context(), "mutation-failure")
			if err != nil || current.Phase != managedcontent.Dispatched || private.Dispatch == nil || !sameContentDispatch(dispatch, *private.Dispatch) {
				t.Fatal("explicit recovery changed immutable native dispatch", err)
			}
			for _, key := range keys {
				if _, err := a.protocol.Get(t.Context(), key); err != nil {
					t.Fatal("explicit recovery missing native reservation", key, err)
				}
			}
			if captures, _ := contentNativeMutationCounts(t, a); captures != 1 || f.posts.Load() != 0 {
				t.Fatal("explicit recovery duplicated admission or licensed content", captures)
			}
		})
	}
}
