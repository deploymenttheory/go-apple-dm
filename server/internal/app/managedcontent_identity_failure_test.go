package app

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage/ddm/ddmtest"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// contentIdentityNativeStore keeps the same SQL pool and row-lock implementation,
// recording native lock order and optionally reporting a missing parent or an
// unavailable support lookup through the actual native provider interface.
type contentIdentityNativeStore struct {
	storage.Store
	lock          func(context.Context, mdm.EnrollmentID) (*storage.Enrollment, error)
	parent        mdm.EnrollmentID
	lockFailure   error
	parentMissing bool
	listFailure   error
	locks         []mdm.EnrollmentID
}

// Get delegates all rows except a parent reported missing by the native provider.
func (s *contentIdentityNativeStore) Get(ctx context.Context, id mdm.EnrollmentID) (*storage.Enrollment, error) {
	if s.parentMissing && id == s.parent {
		return nil, storage.ErrNotFound
	}
	return s.Store.Get(ctx, id)
}

// GetForUpdate records ordering and forwards successful locks into the existing
// SQL unit of work. A failed parent lock must prevent acquiring the user lock.
func (s *contentIdentityNativeStore) GetForUpdate(ctx context.Context, id mdm.EnrollmentID) (*storage.Enrollment, error) {
	s.locks = append(s.locks, id)
	if id == s.parent && s.lockFailure != nil {
		return nil, s.lockFailure
	}
	return s.lock(ctx, id)
}

// List injects an unavailable native support query without altering enrolled
// identities, certificate state, or any successfully read underlying rows.
func (s *contentIdentityNativeStore) List(ctx context.Context, q storage.EnrollmentQuery, p paging.Page) (paging.Result[storage.Enrollment], error) {
	if s.listFailure != nil {
		return paging.Result[storage.Enrollment]{}, s.listFailure
	}
	return s.Store.List(ctx, q, p)
}

// TestManagedContentNativeUserLockBoundary admits an explicitly bound native
// user without serial or ABM, then exercises the required parent-before-user
// lock boundary and recovery while preserving the original principal admission.
func TestManagedContentNativeUserLockBoundary(t *testing.T) {
	for _, scenario := range []string{"parent-lock-unavailable", "parent-reported-missing", "locking-capability-missing"} {
		t.Run(scenario, func(t *testing.T) {
			a, token, f, _, _, plan := contentBoundaryBench(t)
			parent := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "native-user-parent"}
			seedContentEnrollment(t, a, parent, "native-authenticated-pin", "")
			record, err := a.Store.Get(t.Context(), parent)
			if err != nil {
				t.Fatal(err)
			}
			record.Device.ProductName = "MacBookPro15,1"
			record.Device.OSVersion = "10.15.7"
			if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: *record}); err != nil {
				t.Fatal(err)
			}
			user := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "native-user-reader", ParentID: parent.ID}
			if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: user, Enabled: true, EnrollmentUserID: "native-reader-id", UserShortName: "reader", EnrolledAt: time.Now(), TokenUpdatedAt: time.Now(), LastSeenAt: time.Now()}}); err != nil {
				t.Fatal(err)
			}
			contentFailureReply(t, a, token, "PUT", "/managed-content/users", map[string]any{"enrollment": user, "clientUserId": "reader"}, 200)
			plan.Target = managedcontent.Selection{Enrollment: user, ClientUserID: "reader"}
			path := "/managed-content/operations/native-user-bound"
			contentFailureReply(t, a, token, "PUT", path, map[string]any{"mode": "install", "plan": plan}, 200)
			admission, err := a.protocol.Get(t.Context(), contentServerKey("admission", "native-user-bound"))
			if err != nil {
				t.Fatal(err)
			}
			original := a.Store
			locker, ok := original.(interface {
				GetForUpdate(context.Context, mdm.EnrollmentID) (*storage.Enrollment, error)
			})
			if !ok {
				t.Fatal("SQL native lock unavailable")
			}
			observed := &contentIdentityNativeStore{Store: original, lock: locker.GetForUpdate, parent: parent}
			want := 503
			switch scenario {
			case "parent-lock-unavailable":
				observed.lockFailure = errors.New("fixture-provider-secret parent lock unavailable")
				a.Store = observed
			case "parent-reported-missing":
				observed.parentMissing = true
				a.Store = observed
				want = 404
			case "locking-capability-missing":
				a.Store = struct{ storage.Store }{Store: original}
			}
			contentFailureReply(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, want)
			if scenario == "parent-lock-unavailable" && !reflect.DeepEqual(observed.locks, []mdm.EnrollmentID{parent}) {
				t.Fatal("user lock acquired after parent failure", observed.locks)
			}
			a.Store = original
			queue, err := a.Store.Commands(t.Context(), user, storage.CommandQuery{}, paging.Page{Limit: 100})
			if err != nil || len(queue.Items) != 0 || f.posts.Load() != 0 {
				t.Fatal("identity failure admitted native or Apple effects", err)
			}
			current, err := a.protocol.Get(t.Context(), admission.Key)
			if err != nil || !reflect.DeepEqual(current.Value, admission.Value) {
				t.Fatal("failed parent boundary changed admitted principal", err)
			}
			// A transparent healthy wrapper must acquire every selected user lock only
			// after the actual certificate-bearing parent, on the original SQL pool.
			healthy := &contentIdentityNativeStore{Store: original, lock: locker.GetForUpdate, parent: parent}
			a.Store = healthy
			contentFailureReply(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, 200)
			a.Store = original
			if len(healthy.locks) < 2 {
				t.Fatal("native user dispatch did not use row locking", healthy.locks)
			}
			userLocks := 0
			for i, id := range healthy.locks {
				if id == user {
					userLocks++
					if i == 0 || healthy.locks[i-1] != parent {
						t.Fatal("native user lock preceded its parent", healthy.locks)
					}
				}
			}
			if userLocks == 0 {
				t.Fatal("dispatch omitted the native user lock", healthy.locks)
			}
			queue, err = a.Store.Commands(t.Context(), user, storage.CommandQuery{}, paging.Page{Limit: 100})
			if err != nil || len(queue.Items) != 1 || f.posts.Load() != 0 {
				t.Fatal("healthy user dispatch missing or licensed content", err)
			}
			target, err := (contentResolver{a}).Resolve(t.Context(), plan.Target)
			if err != nil || len(target) != 1 || target[0].ClientUserID != "reader" || target[0].Serial != "" {
				t.Fatal("user licensing acquired hardware dependency", target, err)
			}
		})
	}
}

// TestManagedContentResolverSupportFailure rejects unavailable native support,
// capabilities, and an explicitly wrong serial before any Apple preflight or
// native dispatch. Recovery retains the exact admitted selection.
func TestManagedContentResolverSupportFailure(t *testing.T) {
	for _, scenario := range []string{"native-support-unavailable", "capabilities-unavailable", "wrong-selected-serial"} {
		t.Run(scenario, func(t *testing.T) {
			a, token, f, id, _, plan := contentBoundaryBench(t)
			original := a.Store
			engine := a.Engine
			if scenario == "wrong-selected-serial" {
				plan.Target.Serial = "wrong-explicit-serial"
			}
			path := "/managed-content/operations/support-failure"
			contentFailureReply(t, a, token, "PUT", path, map[string]any{"mode": "install", "plan": plan}, 200)
			if scenario == "native-support-unavailable" {
				locker, ok := original.(interface {
					GetForUpdate(context.Context, mdm.EnrollmentID) (*storage.Enrollment, error)
				})
				if !ok {
					t.Fatal("SQL native lock unavailable")
				}
				a.Store = &contentIdentityNativeStore{Store: original, lock: locker.GetForUpdate, listFailure: errors.New("fixture-provider-secret native support unavailable")}
			} else if scenario == "capabilities-unavailable" {
				replacement, err := ddm.New(ddm.Config{Store: &ddmtest.Failing{Store: engine.Store(), Fail: map[string]error{"StatusValues": errors.New("fixture-provider-secret capabilities unavailable")}}})
				if err != nil {
					t.Fatal(err)
				}
				a.Engine = replacement
			}
			requests := f.requests.Load()
			contentFailureReply(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, 503)
			a.Store = original
			a.Engine = engine
			queue, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
			if err != nil || len(queue.Items) != 0 || f.requests.Load() != requests {
				t.Fatal("resolver failure contacted Apple or native queue", err)
			}
			if scenario == "wrong-selected-serial" {
				// An immutable wrong selection cannot be repaired by silently redirecting
				// its admission; cancel it and name a fresh correctly selected operation.
				contentFailureReply(t, a, token, "POST", path+"/cancel", nil, 200)
				plan.Target.Serial = "S-BOUNDARY"
				contentBoundaryStep(t, a, token, "correctly-selected", plan)
			} else {
				contentFailureReply(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60}, 200)
			}
			if f.posts.Load() != 0 {
				t.Fatal("support recovery mutated Apple licensing")
			}
		})
	}
}

// contentQueryFailureStore delegates the first real query enqueue to sealed SQL,
// then fails the second member of the explicit bounded observation batch.
type contentQueryFailureStore struct {
	storage.Store
	enqueues int
}

// Enqueue models loss of the native queue provider during a two-command query
// batch, preserving the original shared SQL transaction for the first command.
func (s *contentQueryFailureStore) Enqueue(ctx context.Context, ids []mdm.EnrollmentID, cmd *mdm.Command, options storage.EnqueueOptions) (storage.EnqueueResult, error) {
	s.enqueues++
	if s.enqueues == 2 {
		return storage.EnqueueResult{}, errors.New("fixture-provider-secret second query enqueue unavailable")
	}
	return s.Store.Enqueue(ctx, ids, cmd, options)
}

// TestManagedContentStatusQueryProviderRollback proves an actual mid-batch native
// enqueue failure rolls back the earlier query and idempotency record; an explicit
// retry can then commit exactly the original bounded batch without replaying install.
func TestManagedContentStatusQueryProviderRollback(t *testing.T) {
	a, token, f, id, device, plan := contentBoundaryBench(t)
	contentBoundaryStep(t, a, token, "query-provider", plan)
	if _, err := device.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := contentProviderSnapshot(t, a, "query-provider")
	requests := f.requests.Load()
	original := a.Core
	failing := &contentQueryFailureStore{Store: a.Store}
	replacement, err := service.New(service.Config{Store: failing, Bus: a.cfg.publisher(), Clock: a.cfg.Clock, Logger: a.cfg.Logger})
	if err != nil {
		t.Fatal(err)
	}
	a.Core = replacement
	path := "/managed-content/operations/query-provider/status-refresh"
	contentFailureReply(t, a, token, "POST", path, map[string]any{"requestId": "provider-retry"}, 503)
	a.Core = original
	if failing.enqueues != 2 {
		t.Fatal("provider failure did not occur after first query", failing.enqueues)
	}
	rows, err := a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil || len(rows.Items) != 1 {
		t.Fatal("first query escaped failed batch", err)
	}
	key := contentServerKey("query", "query-provider\x00provider-retry")
	if _, err := a.protocol.Get(t.Context(), key); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("failed query retained idempotency receipt", err)
	}
	var records int
	if err := a.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM event_records WHERE type = ?", "managed-content-status-query").Scan(&records); err != nil || records != 0 {
		t.Fatal("failed query emitted completion work", records, err)
	}
	contentFailureReply(t, a, token, "POST", path, map[string]any{"requestId": "provider-retry"}, 200)
	rows, err = a.Store.Commands(t.Context(), id, storage.CommandQuery{}, paging.Page{Limit: 100})
	if err != nil || len(rows.Items) != 3 || f.requests.Load() != requests {
		t.Fatal("query provider recovery replayed install or contacted Apple", err)
	}
	if !reflect.DeepEqual(before, contentProviderSnapshot(t, a, "query-provider")) {
		t.Fatal("query provider failure changed SDK intent/evidence")
	}
}
