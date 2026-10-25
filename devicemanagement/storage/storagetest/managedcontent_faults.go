package storagetest

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// contentIOStore injects one failure at a caller-selected persistence boundary.
// Counting is instrumentation, not an expected internal call sequence: the
// property checks recovery and at-most-once remote effects for every encountered
// read/write/delete boundary, stopping when that operation no longer occurs.
type contentIOStore struct {
	state.Store
	kind  string
	at, n int
	hit   bool
}
type contentIOTx struct {
	state.Tx
	owner *contentIOStore
}

// fail injects one configured persistence boundary failure.
func (s *contentIOStore) fail(kind string) error {
	if s.kind == kind {
		s.n++
		if s.n == s.at {
			s.hit = true
			return errors.New("injected persistence failure")
		}
	}
	return nil
}

// Get reads the underlying record while preserving the injected failure or namespace boundary.
func (s *contentIOStore) Get(ctx context.Context, k string) (state.Record, error) {
	if err := s.fail("read"); err != nil {
		return state.Record{}, err
	}
	return s.Store.Get(ctx, k)
}

// List reads the underlying page and restores caller-visible keys.
func (s *contentIOStore) List(ctx context.Context, p, a string, n int) ([]state.Record, error) {
	if err := s.fail("list"); err != nil {
		return nil, err
	}
	return s.Store.List(ctx, p, a, n)
}

// Update joins the underlying transaction while preserving fault injection and atomic rollback.
func (s *contentIOStore) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(contentIOTx{Tx: tx, owner: s}) })
}

// Get reads the underlying record while preserving the injected failure or namespace boundary.
func (s contentIOTx) Get(ctx context.Context, k string) (state.Record, error) {
	if err := s.owner.fail("read"); err != nil {
		return state.Record{}, err
	}
	return s.Tx.Get(ctx, k)
}

// Put writes the record through the caller namespace.
func (s contentIOTx) Put(ctx context.Context, r state.Record) error {
	if err := s.owner.fail("write"); err != nil {
		return err
	}
	return s.Tx.Put(ctx, r)
}

// Delete deletes the record through the caller namespace.
func (s contentIOTx) Delete(ctx context.Context, k string) error {
	if err := s.owner.fail("delete"); err != nil {
		return err
	}
	return s.Tx.Delete(ctx, k)
}

type contentAuthority struct {
	managedcontent.Licensing
	location   func(*appsbooks.Location) error
	asset      func(*appsbooks.AssetEvidence) error
	job        func(*appsbooks.LicensingJob) error
	assignment func(*appsbooks.AssignmentEvidence) error
}

// Load loads location metadata with the configured authority fault.
func (s contentAuthority) Load(ctx context.Context, id string) (appsbooks.Location, error) {
	out, err := s.Licensing.Load(ctx, id)
	if err == nil && s.location != nil {
		err = s.location(&out)
	}
	return out, err
}

// ReadAsset reads native asset evidence with the configured capability fault.
func (s contentAuthority) ReadAsset(ctx context.Context, id string, a appsbooks.Asset, n int) (appsbooks.AssetEvidence, error) {
	out, err := s.Licensing.ReadAsset(ctx, id, a, n)
	if err == nil && s.asset != nil {
		err = s.asset(&out)
	}
	return out, err
}

// LoadJob loads licensing results with the configured per-target fault.
func (s contentAuthority) LoadJob(ctx context.Context, id, job string) (appsbooks.LicensingJob, error) {
	out, err := s.Licensing.LoadJob(ctx, id, job)
	if err == nil && s.job != nil {
		err = s.job(&out)
	}
	return out, err
}

// ConfirmAssignment returns current assignment evidence with the configured association fault.
func (s contentAuthority) ConfirmAssignment(ctx context.Context, id string, a appsbooks.Asset, serial, user string, n int) (appsbooks.AssignmentEvidence, error) {
	out, err := s.Licensing.ConfirmAssignment(ctx, id, a, serial, user, n)
	if err == nil && s.assignment != nil {
		err = s.assignment(&out)
	}
	return out, err
}

// contentFailureContracts verifies rollback and recovery at actual read and write boundaries.
func contentFailureContracts(t *testing.T, factory AppsBooksFactory) {
	t.Helper()
	for _, kind := range []string{"read", "write"} {
		t.Run("EveryPersistenceBoundary/"+kind, func(t *testing.T) {
			for at := 1; at <= 50; at++ {
				f := newContentFixture(t, factory)
				fault := &contentIOStore{Store: f.a, kind: kind, at: at}
				w, err := managedcontent.New(fault, f.licenses, f.resolver, f.delivery)
				requireContent(t, err == nil, err)
				_, err = f.workflow.Install(t.Context(), "io", f.plan)
				requireContent(t, err == nil, err)
				_, firstErr := w.Step(t.Context(), "io", "first", time.Minute)
				if fault.hit {
					requireContent(t, firstErr != nil, kind, at)
				} else {
					requireContent(t, firstErr == nil, kind, at, firstErr)
					break
				}
				fault.kind = ""
				f.now.Add(int64(2 * time.Minute))
				op, err := f.workflow.Load(t.Context(), "io")
				requireContent(t, err == nil, err)
				if op.Phase == managedcontent.Submitting || op.Phase == managedcontent.Uncertain {
					_, err = f.restart(t).Reconcile(t.Context(), op.ID, "recover", time.Minute, "original worker stopped", false)
				} else {
					_, err = f.restart(t).Step(t.Context(), op.ID, "retry", time.Minute)
				}
				requireContent(t, err == nil && f.delivery.posts == 1, "persistence recovery duplicated or lost dispatch", kind, at, err, f.delivery.posts)
			}
		})
	}
	t.Run("CancelRollbackAtEveryDeletion", func(t *testing.T) {
		for at := 1; at <= 3; at++ {
			f := newContentFixture(t, factory)
			f.delivery.inspectErr = errors.New("offline")
			_, err := f.workflow.Install(t.Context(), "cancel", f.plan)
			requireContent(t, err == nil, err)
			_, err = f.workflow.Step(t.Context(), "cancel", "prepare", time.Minute)
			requireContent(t, err != nil, err)
			fault := &contentIOStore{Store: f.a, kind: "delete", at: at}
			w, err := managedcontent.New(fault, f.licenses, f.resolver, f.delivery)
			requireContent(t, err == nil, err)
			_, err = w.Cancel(t.Context(), "cancel")
			if fault.hit {
				requireContent(t, err != nil, at, err)
			} else {
				requireContent(t, err == nil, at, err)
			}
			fault.kind = ""
			op, err := f.workflow.Cancel(t.Context(), "cancel")
			requireContent(t, err == nil && op.Phase == managedcontent.Cancelled, op, err)
			_, err = f.workflow.Install(t.Context(), "replacement", f.plan)
			requireContent(t, err == nil, err)
		}
	})
	t.Run("LocalStartRollbackAtEveryWrite", func(t *testing.T) {
		for at := 1; at <= 4; at++ {
			f := newContentFixture(t, factory)
			fault := &contentIOStore{Store: f.a, kind: "write", at: at}
			w, err := managedcontent.New(fault, f.licenses, f.resolver, f.delivery)
			requireContent(t, err == nil, err)
			_, err = w.Install(t.Context(), "start", f.plan)
			if fault.hit {
				requireContent(t, err != nil, at, err)
				_, err = f.workflow.Load(t.Context(), "start")
				requireContent(t, errors.Is(err, state.ErrNotFound), err)
			} else {
				requireContent(t, err == nil, at, err)
			}
			fault.kind = ""
			_, err = f.workflow.Install(t.Context(), "start", f.plan)
			requireContent(t, err == nil && f.resolver.calls.Load() == 0 && f.delivery.posts == 0, at, err)
		}
	})
	t.Run("InvalidPublicInputsAndTerminalOperations", func(t *testing.T) {
		f := newContentFixture(t, factory)
		_, err := managedcontent.New(nil, f.licenses, f.resolver, f.delivery)
		requireContent(t, errors.Is(err, managedcontent.ErrInput), err)
		for _, change := range []func(*managedcontent.Plan){func(p *managedcontent.Plan) { p.LocationID = "" }, func(p *managedcontent.Plan) { p.MaxPages = 0 }, func(p *managedcontent.Plan) { p.Method = "automatic" }, func(p *managedcontent.Plan) { p.Content.Asset.AdamID = "01" }, func(p *managedcontent.Plan) { p.Content.Kind = "Subscription" }, func(p *managedcontent.Plan) { p.Target.Serial = "" }, func(p *managedcontent.Plan) { p.Target.Enrollment.ID = "" }} {
			p := f.plan
			change(&p)
			_, err = f.workflow.Install(t.Context(), "invalid", p)
			requireContent(t, errors.Is(err, managedcontent.ErrInput), err)
		}
		_, err = f.workflow.Install(t.Context(), "", f.plan)
		requireContent(t, errors.Is(err, managedcontent.ErrInput), err)
		_, err = f.workflow.Assign(t.Context(), "invalid", f.plan)
		requireContent(t, errors.Is(err, managedcontent.ErrInput), err)
		p := f.plan
		p.LicensingJobID = "existing"
		_, err = f.workflow.AssignAndInstall(t.Context(), "invalid", p)
		requireContent(t, errors.Is(err, managedcontent.ErrInput), err)
		_, err = f.workflow.Install(t.Context(), "valid", f.plan)
		requireContent(t, err == nil, err)
		for _, v := range []struct {
			worker string
			ttl    time.Duration
		}{{"", time.Minute}, {"worker", 0}, {"worker", 2 * time.Hour}} {
			_, err = f.workflow.Step(t.Context(), "valid", v.worker, v.ttl)
			requireContent(t, errors.Is(err, managedcontent.ErrInput), err)
		}
		_, err = f.workflow.Load(t.Context(), "")
		requireContent(t, errors.Is(err, managedcontent.ErrInput), err)
		_, _, err = f.workflow.LoadEvidence(t.Context(), "")
		requireContent(t, errors.Is(err, managedcontent.ErrInput), err)
		_, err = f.workflow.Step(t.Context(), "missing", "worker", time.Minute)
		requireContent(t, errors.Is(err, state.ErrNotFound), err)
		_, err = f.workflow.Cancel(t.Context(), "missing")
		requireContent(t, errors.Is(err, state.ErrNotFound), err)
		_, err = f.workflow.Reconcile(t.Context(), "valid", "worker", time.Minute, "", false)
		requireContent(t, errors.Is(err, managedcontent.ErrInput), err)
		_, err = f.workflow.Reconcile(t.Context(), "valid", "worker", time.Minute, "stopped", false)
		requireContent(t, errors.Is(err, managedcontent.ErrInput), err)
		_, err = f.workflow.Cancel(t.Context(), "valid")
		requireContent(t, err == nil, err)
		op, err := f.workflow.Step(t.Context(), "valid", "worker", time.Minute)
		requireContent(t, err == nil && op.Phase == managedcontent.Cancelled, op, err)
		_, err = f.workflow.Reconcile(t.Context(), "valid", "worker", time.Minute, "stopped", false)
		requireContent(t, errors.Is(err, managedcontent.ErrConflict), err)
		fault := &contentIOStore{Store: f.a, kind: "list", at: 1}
		w, err := managedcontent.New(fault, f.licenses, f.resolver, f.delivery)
		requireContent(t, err == nil, err)
		_, _, err = w.Operations(t.Context(), "", 1)
		requireContent(t, err != nil, err)
	})

	t.Run("CancelledSnapshotCannotClaimReleasedPhysicalScope", func(t *testing.T) {
		f := newContentFixture(t, factory)
		f.delivery.inspectErr = errors.New("offline")
		_, err := f.workflow.Install(t.Context(), "snapshot", f.plan)
		requireContent(t, err == nil, err)
		_, err = f.workflow.Step(t.Context(), "snapshot", "prepare", time.Minute)
		requireContent(t, err != nil, err)
		barrier := &contentSnapshotBarrier{Store: f.a, entered: make(chan struct{}), release: make(chan struct{})}
		barrier.armed.Store(true)
		w, err := managedcontent.New(barrier, f.licenses, f.resolver, f.delivery)
		requireContent(t, err == nil, err)
		result := make(chan error, 1)
		go func() { _, err := w.Step(t.Context(), "snapshot", "old", time.Minute); result <- err }()
		<-barrier.entered
		_, err = f.workflow.Cancel(t.Context(), "snapshot")
		requireContent(t, err == nil, err)
		close(barrier.release)
		err = <-result
		requireContent(t, errors.Is(err, managedcontent.ErrFence) && f.delivery.posts == 0, err)
	})
	t.Run("OlderDeviceObservationCannotEraseNewerFacts", func(t *testing.T) {
		f := newContentFixture(t, factory)
		_, err := f.workflow.Install(t.Context(), "status", f.plan)
		requireContent(t, err == nil, err)
		f.delivery.installed = true
		f.delivery.version = "2.0"
		_, err = f.workflow.Step(t.Context(), "status", "first", time.Minute)
		requireContent(t, err == nil, err)
		f.delivery.modify = func(o *managedcontent.Observation) {
			o.ObservedAt = o.ObservedAt.Add(-time.Hour)
			o.Installed = false
			o.AppVersion = "1.0"
		}
		_, err = f.workflow.Step(t.Context(), "status", "older", time.Minute)
		requireContent(t, errors.Is(err, managedcontent.ErrProtocol), err)
		op, err := f.workflow.Load(t.Context(), "status")
		requireContent(t, err == nil && op.Observation.Installed && op.Observation.AppVersion == "2.0", op, err)
	})

	for _, kind := range []string{"read", "write"} {
		t.Run("CancelPersistenceBoundary/"+kind, func(t *testing.T) {
			for at := 1; at <= 20; at++ {
				f := newContentFixture(t, factory)
				_, err := f.workflow.Install(t.Context(), "cancel", f.plan)
				requireContent(t, err == nil, err)
				f.delivery.inspectErr = errors.New("offline")
				_, err = f.workflow.Step(t.Context(), "cancel", "prepare", time.Minute)
				requireContent(t, err != nil, err)
				fault := &contentIOStore{Store: f.a, kind: kind, at: at}
				w, err := managedcontent.New(fault, f.licenses, f.resolver, f.delivery)
				requireContent(t, err == nil, err)
				_, err = w.Cancel(t.Context(), "cancel")
				if fault.hit {
					requireContent(t, err != nil, kind, at, err)
				} else {
					requireContent(t, err == nil, kind, at, err)
					break
				}
				_, err = f.workflow.Cancel(t.Context(), "cancel")
				requireContent(t, err == nil, err)
				_, err = f.workflow.Install(t.Context(), "replacement", f.plan)
				requireContent(t, err == nil, err)
			}
		})
	}
	t.Run("InvalidAuthoritativeClockRollsBackStart", func(t *testing.T) {
		f := newContentFixture(t, factory)
		memory := state.NewMemory()
		memory.Now = func() time.Time { return time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }
		w, err := managedcontent.New(memory, f.licenses, f.resolver, f.delivery)
		requireContent(t, err == nil, err)
		_, err = w.Install(t.Context(), "invalid-clock", f.plan)
		requireContent(t, err != nil, err)
		rows, err := memory.List(t.Context(), "managedcontent/", "", 10)
		requireContent(t, err == nil && len(rows) == 0, rows, err)
	})

	t.Run("ExistingDurableQueueReadbackAvoidsSubmission", func(t *testing.T) {
		f := newContentFixture(t, factory)
		_, err := f.workflow.Install(t.Context(), "readback", f.plan)
		requireContent(t, err == nil, err)
		f.delivery.inspectErr = errors.New("offline")
		_, err = f.workflow.Step(t.Context(), "readback", "prepare", time.Minute)
		requireContent(t, err != nil, err)
		_, private, err := f.workflow.LoadEvidence(t.Context(), "readback")
		requireContent(t, err == nil, err)
		f.delivery.inspectErr = nil
		_, err = f.delivery.Submit(t.Context(), *private.Dispatch)
		requireContent(t, err == nil, err)
		op, err := f.restart(t).Step(t.Context(), "readback", "inspect-existing", time.Minute)
		requireContent(t, err == nil && op.Phase == managedcontent.Dispatched && f.delivery.posts == 1, op, err)
	})
	t.Run("CancelBookIntentNeverReclaimsLicense", func(t *testing.T) {
		f := newContentFixture(t, factory)
		contentUser(f, support.IOS, true)
		f.plan.Method = ""
		f.plan.Content.Kind = "Book"
		f.service.mu.Lock()
		f.service.product = "Book"
		f.service.mu.Unlock()
		_, err := f.workflow.Assign(t.Context(), "book", f.plan)
		requireContent(t, err == nil, err)
		_, err = f.workflow.Cancel(t.Context(), "book")
		requireContent(t, err == nil && f.delivery.posts == 0, err)
		f.service.mu.Lock()
		posts := f.service.posts
		f.service.mu.Unlock()
		requireContent(t, posts == 0, "cancellation reclaimed book", posts)
	})
	contentCorruptionContracts(t, factory)
	contentAdapterContracts(t, factory)
	contentWireBindingContracts(t, factory)
}

// editContentRecord changes persisted state to exercise integrity checks at the public workflow boundary.
func editContentRecord(t *testing.T, s state.Store, prefix string, edit func(*state.Record)) {
	t.Helper()
	rows, err := s.List(t.Context(), prefix, "", 10)
	requireContent(t, err == nil && len(rows) == 1, rows, err)
	record := rows[0]
	edit(&record)
	op, err := s.List(t.Context(), "managedcontent/op/", "", 10)
	requireContent(t, err == nil && len(op) == 1, op, err)
	err = s.Update(t.Context(), []string{op[0].Key}, func(tx state.Tx) error { return tx.Put(t.Context(), record) })
	requireContent(t, err == nil, err)
}

// contentCorruptionContracts rejects malformed or foreign durable intent and evidence.
func contentCorruptionContracts(t *testing.T, factory AppsBooksFactory) {
	t.Helper()
	for _, kind := range []string{"id", "mode", "phase", "fence", "malformed"} {
		t.Run("CorruptIntent/"+kind, func(t *testing.T) {
			f := newContentFixture(t, factory)
			_, err := f.workflow.Install(t.Context(), "intent", f.plan)
			requireContent(t, err == nil, err)
			editContentRecord(t, f.a, "managedcontent/op/", func(r *state.Record) {
				if kind == "malformed" {
					r.Value = []byte("{")
					return
				}
				var o managedcontent.Operation
				err := json.Unmarshal(r.Value, &o)
				requireContent(t, err == nil, err)
				switch kind {
				case "id":
					o.ID = "other"
				case "mode":
					o.Mode = "unknown"
				case "phase":
					o.Phase = "unknown"
				case "fence":
					o.Fence = math.MaxUint64
				}
				r.Value, err = json.Marshal(o)
				requireContent(t, err == nil, err)
			})
			_, err = f.workflow.Step(t.Context(), "intent", "worker", time.Minute)
			requireContent(t, err != nil && f.delivery.posts == 0, kind, err)
			if kind == "id" {
				_, _, err = f.workflow.LoadEvidence(t.Context(), "intent")
				requireContent(t, errors.Is(err, managedcontent.ErrProtocol), err)
			}
			if kind == "malformed" {
				_, _, err = f.workflow.Operations(t.Context(), "", 2)
				requireContent(t, errors.Is(err, managedcontent.ErrProtocol), err)
			}
			if kind == "fence" {
				_, err = f.workflow.Cancel(t.Context(), "intent")
				requireContent(t, errors.Is(err, managedcontent.ErrFence), err)
			}
		})
	}
	for _, scope := range []string{"lane", "physical"} {
		for _, kind := range []string{"foreign", "missing"} {
			t.Run("OwnershipRecord/"+scope+"/"+kind, func(t *testing.T) {
				f := newContentFixture(t, factory)
				_, err := f.workflow.Install(t.Context(), "intent", f.plan)
				requireContent(t, err == nil, err)
				f.delivery.inspectErr = errors.New("offline")
				_, err = f.workflow.Step(t.Context(), "intent", "prepare", time.Minute)
				requireContent(t, err != nil, err)
				rows, err := f.a.List(t.Context(), "managedcontent/"+scope+"/", "", 5)
				requireContent(t, err == nil && len(rows) == 1, rows, err)
				ops, err := f.a.List(t.Context(), "managedcontent/op/", "", 5)
				requireContent(t, err == nil, err)
				err = f.a.Update(t.Context(), []string{ops[0].Key, rows[0].Key}, func(tx state.Tx) error {
					if kind == "missing" {
						return tx.Delete(t.Context(), rows[0].Key)
					}
					r := rows[0]
					r.Value = []byte(`{"id":"foreign","method":"ddm"}`)
					return tx.Put(t.Context(), r)
				})
				requireContent(t, err == nil, err)
				_, err = f.workflow.Step(t.Context(), "intent", "worker", time.Minute)
				requireContent(t, err != nil && f.delivery.posts == 0, scope, kind, err)
				_, err = f.workflow.Cancel(t.Context(), "intent")
				requireContent(t, err != nil, scope, kind, err)
			})
		}
	}
}

// contentAdapterContracts verifies target, licensing and delivery adapter authority failures.
func contentAdapterContracts(t *testing.T, factory AppsBooksFactory) {
	t.Helper()
	for _, kind := range []string{"source-error", "missing-os", "user-scope", "unassignable", "platform", "user-mdm-channel", "mac-license-channel", "ddm-disabled", "ddm-book"} {
		t.Run("TargetAndCapability/"+kind, func(t *testing.T) {
			f := newContentFixture(t, factory)
			switch kind {
			case "source-error":
				_, f.resolver.err = http.NewRequestWithContext(t.Context(), http.MethodGet, "https://private.invalid/%zz?token=PRIVATE-ERROR-MARKER", nil)
			case "missing-os":
				f.resolver.targets[0].Support.OS = ""
			case "user-scope":
				contentUser(f, support.MacOS, false)
				f.resolver.targets[0].Serial = "s1"
			case "unassignable":
				f.service.mu.Lock()
				f.service.notDeviceAssignable = true
				f.service.mu.Unlock()
			case "platform":
				f.service.mu.Lock()
				f.service.platforms = []string{"macOS"}
				f.service.mu.Unlock()
			case "user-mdm-channel":
				contentUser(f, support.IOS, false)
				id := mdm.EnrollmentID{Channel: mdm.ChannelSharedIPadUser, ID: "device:user", ParentID: "device"}
				f.plan.Target.Enrollment = id
				f.resolver.targets[0].Enrollment = id
				f.resolver.targets[0].Support.Channel = support.ChannelUser
				f.resolver.targets[0].Support.SharedIPad = true
			case "mac-license-channel":
				id := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "device:user", ParentID: "device"}
				f.plan.Target.Enrollment = id
				f.resolver.targets[0].Enrollment = id
				f.resolver.targets[0].Support.OS = support.MacOS
				f.resolver.targets[0].Support.Channel = support.ChannelUser
				f.resolver.targets[0].ScopeID = "native-user"
			case "ddm-disabled":
				f.plan.Method = managedcontent.DDM
				f.resolver.targets[0].DDMEnabled = false
			case "ddm-book":
				contentUser(f, support.IOS, true)
				f.plan.Method = managedcontent.DDM
				f.plan.Content.Kind = "Book"
				f.service.mu.Lock()
				f.service.product = "Book"
				f.service.mu.Unlock()
			}
			_, err := f.workflow.Install(t.Context(), "invalid", f.plan)
			requireContent(t, err == nil, err)
			_, err = f.workflow.Step(t.Context(), "invalid", "worker", time.Minute)
			requireContent(t, err != nil && f.delivery.posts == 0, kind, err)
			op, err := f.workflow.Load(t.Context(), "invalid")
			requireContent(t, err == nil, err)
			raw, err := json.Marshal(op)
			requireContent(t, err == nil && !strings.Contains(string(raw), "PRIVATE-"), string(raw), err)
		})
	}
	for _, kind := range []string{"location-error", "uid", "asset-error", "asset-uid", "proof-error", "proof-uid", "proof-time", "proof-target", "proof-kind"} {
		t.Run("SourceAuthority/"+kind, func(t *testing.T) {
			f := newContentFixture(t, factory)
			authority := contentAuthority{Licensing: f.licenses}
			switch kind {
			case "location-error":
				authority.location = func(*appsbooks.Location) error { return errors.New("source unavailable") }
			case "uid":
				authority.location = func(l *appsbooks.Location) error { l.UID = ""; return nil }
			case "asset-error":
				authority.asset = func(*appsbooks.AssetEvidence) error { return errors.New("capability unavailable") }
			case "asset-uid":
				authority.asset = func(e *appsbooks.AssetEvidence) error { e.UID = "other"; return nil }
			case "proof-error":
				authority.assignment = func(*appsbooks.AssignmentEvidence) error { return errors.New("assignment unavailable") }
			case "proof-uid":
				authority.assignment = func(e *appsbooks.AssignmentEvidence) error { e.UID = "other"; return nil }
			case "proof-time":
				authority.assignment = func(e *appsbooks.AssignmentEvidence) error { e.Assignments.ObservedAt = time.Time{}; return nil }
			case "proof-target":
				authority.assignment = func(e *appsbooks.AssignmentEvidence) error { e.Assignment.SerialNumber = "other"; return nil }
			case "proof-kind":
				authority.assignment = func(e *appsbooks.AssignmentEvidence) error { e.Asset.ProductType = "Book"; return nil }
			}
			w, err := managedcontent.New(f.a, authority, f.resolver, f.delivery)
			requireContent(t, err == nil, err)
			_, err = w.Install(t.Context(), "invalid", f.plan)
			requireContent(t, err == nil, err)
			_, err = w.Step(t.Context(), "invalid", "worker", time.Minute)
			requireContent(t, err != nil && f.delivery.posts == 0, kind, err)
		})
	}
	t.Run("AggregateCompletionWithoutTargetAssociation", func(t *testing.T) {
		f := newContentFixture(t, factory)
		_, err := f.workflow.AssignAndInstall(t.Context(), "aggregate", f.plan)
		requireContent(t, err == nil, err)
		_, err = f.workflow.Step(t.Context(), "aggregate", "submit", time.Minute)
		requireContent(t, errors.Is(err, managedcontent.ErrLicense), err)
		f.complete(t, "aggregate")
		f.service.mu.Lock()
		f.service.missingAssignment = true
		f.service.mu.Unlock()
		_, err = f.workflow.Step(t.Context(), "aggregate", "install", time.Minute)
		requireContent(t, errors.Is(err, appsbooks.ErrAssignmentUnavailable) && f.delivery.posts == 0, err)
	})
	t.Run("UserAssignmentAndInstallRetainsAppleIdentity", func(t *testing.T) {
		f := newContentFixture(t, factory)
		contentUser(f, support.IOS, true)
		_, err := f.workflow.AssignAndInstall(t.Context(), "user", f.plan)
		requireContent(t, err == nil, err)
		_, err = f.workflow.Step(t.Context(), "user", "submit", time.Minute)
		requireContent(t, errors.Is(err, managedcontent.ErrLicense), err)
		f.complete(t, "user")
		op, err := f.workflow.Step(t.Context(), "user", "install", time.Minute)
		requireContent(t, err == nil && op.Phase == managedcontent.Dispatched && op.Plan.Target.Serial == "" && op.Plan.Target.ClientUserID == "u", op, err)
		job, err := f.licenses.LoadJob(t.Context(), "location", op.LicensingJobID)
		requireContent(t, err == nil && len(job.Request.Assets.SerialNumbers) == 0 && len(job.Request.Assets.ClientUserIDs) == 1 && job.Request.Assets.ClientUserIDs[0] == "u", job, err)
	})

	for _, kind := range []string{"unavailable", "job-error", "job-location", "non-associate", "wrong-asset", "wrong-target", "missing-status", "partial", "bad-count", "status-uid", "notification-failure", "pending"} {
		t.Run("ExplicitLicensingJob/"+kind, func(t *testing.T) {
			f := newContentFixture(t, factory)
			p := f.plan
			p.Method = ""
			op, err := f.workflow.Assign(t.Context(), "assign", p)
			requireContent(t, err == nil, err)
			_, err = f.workflow.Step(t.Context(), op.ID, "submit", time.Minute)
			requireContent(t, errors.Is(err, managedcontent.ErrLicense), err)
			f.complete(t, op.ID)
			op, err = f.workflow.Step(t.Context(), op.ID, "complete", time.Minute)
			requireContent(t, err == nil, err)
			p = f.plan
			p.LicensingJobID = op.LicensingJobID
			if kind == "unavailable" {
				p.LicensingJobID = "unavailable"
			}
			authority := contentAuthority{Licensing: f.licenses, job: func(j *appsbooks.LicensingJob) error {
				switch kind {
				case "job-error":
					return errors.New("unavailable")
				case "job-location":
					j.LocationID = "other"
				case "non-associate":
					j.Request.Operation = appsbooks.JobDisassociate
				case "wrong-asset":
					j.Request.Assets.Assets = []appsbooks.Asset{{AdamID: "2", PricingParam: "STDQ"}}
				case "wrong-target":
					j.Request.Assets.SerialNumbers = []string{"other"}
				case "missing-status":
					j.Status = nil
				case "partial":
					j.Status.NumCompleted = 0
				case "bad-count":
					j.Status.NumRequested = 2
					j.Status.NumCompleted = 2
				case "status-uid":
					j.Status.UID = "other"
				case "notification-failure":
					j.NotificationFailure = true
				case "pending":
					j.State = appsbooks.JobAccepted
				}
				return nil
			}}
			w, err := managedcontent.New(f.a, authority, f.resolver, f.delivery)
			requireContent(t, err == nil, err)
			_, err = w.Install(t.Context(), "install", p)
			requireContent(t, err == nil, err)
			_, err = w.Step(t.Context(), "install", "worker", time.Minute)
			requireContent(t, err != nil && f.delivery.posts == 0, kind, err)
		})
	}
	for _, kind := range []string{"foreign", "bad-digest", "invalid-device-evidence", "mdm-publication", "missing-after-submit"} {
		t.Run("DeliveryAuthority/"+kind, func(t *testing.T) {
			f := newContentFixture(t, factory)
			switch kind {
			case "foreign":
				f.delivery.foreign = true
			case "bad-digest":
				f.delivery.modify = func(o *managedcontent.Observation) { o.Digest = "wrong" }
			case "invalid-device-evidence":
				f.delivery.modify = func(o *managedcontent.Observation) {
					if o.Found {
						o.Installed = true
						o.Raw = nil
					}
				}
			case "mdm-publication":
				f.delivery.modify = func(o *managedcontent.Observation) {
					if o.Found {
						o.DeclarationPublished = true
					}
				}
			case "missing-after-submit":
				f.delivery.modify = func(o *managedcontent.Observation) {
					if o.Found {
						o.Found = false
						o.Raw = nil
					}
				}
			}
			_, err := f.workflow.Install(t.Context(), "invalid", f.plan)
			requireContent(t, err == nil, err)
			_, err = f.workflow.Step(t.Context(), "invalid", "worker", time.Minute)
			requireContent(t, err != nil, kind, err)
		})
	}
	t.Run("CurrentNativeUserScopeAndDDMRestart", func(t *testing.T) {
		f := newContentFixture(t, factory)
		contentUser(f, support.MacOS, false)
		f.plan.Method = managedcontent.DDM
		f.resolver.targets[0].Serial = "s1"
		f.resolver.targets[0].ScopeID = "native-principal"
		_, err := f.workflow.Install(t.Context(), "user-scope", f.plan)
		requireContent(t, err == nil, err)
		f.delivery.inspectErr = errors.New("offline")
		_, err = f.workflow.Step(t.Context(), "user-scope", "prepare", time.Minute)
		requireContent(t, err != nil, err)
		f.delivery.inspectErr = nil
		op, err := f.restart(t).Step(t.Context(), "user-scope", "resume", time.Minute)
		requireContent(t, err == nil && f.delivery.posts == 1, op, err)
		f.now.Add(int64(time.Second))
		f.delivery.published = true
		op, err = f.restart(t).Step(t.Context(), "user-scope", "status", time.Minute)
		requireContent(t, err == nil && op.Observation.DeclarationPublished && !op.Observation.Installed, op, err)
	})
	t.Run("CancellationFencesPausedResolver", func(t *testing.T) {
		f := newContentFixture(t, factory)
		_, err := f.workflow.Install(t.Context(), "cancel-race", f.plan)
		requireContent(t, err == nil, err)
		entered, release := make(chan struct{}), make(chan struct{})
		f.resolver.entered = entered
		f.resolver.release = release
		result := make(chan error, 1)
		go func() { _, err := f.workflow.Step(t.Context(), "cancel-race", "old", time.Minute); result <- err }()
		<-entered
		_, err = f.workflow.Cancel(t.Context(), "cancel-race")
		requireContent(t, errors.Is(err, managedcontent.ErrBusy), err)
		f.now.Add(int64(2 * time.Minute))
		_, err = f.workflow.Cancel(t.Context(), "cancel-race")
		requireContent(t, err == nil, err)
		close(release)
		err = <-result
		requireContent(t, errors.Is(err, managedcontent.ErrFence) && f.delivery.posts == 0, err)
	})
}

// contentSnapshotBarrier pauses only after a consistent snapshot transaction has
// released its locks, permitting an actual competing cancellation to commit.
type contentSnapshotBarrier struct {
	state.Store
	armed            atomic.Bool
	entered, release chan struct{}
}

// Update pauses after committed snapshot reads so a competing cancellation can finish.
func (s *contentSnapshotBarrier) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	err := s.Store.Update(ctx, keys, fn)
	if err == nil && s.armed.Swap(false) {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

// contentWireBindingContracts exercise a persisted, self-consistent digest with
// wire contents that violate the original intent. Hash equality alone must not
// authorize a different command selector/identity or declaration relationship.
func contentWireBindingContracts(t *testing.T, factory AppsBooksFactory) {
	t.Helper()
	for _, kind := range []string{"bad-plist", "command-uuid", "command-asset", "declaration-count", "declaration-token"} {
		t.Run("WireBinding/"+kind, func(t *testing.T) {
			f := newContentFixture(t, factory)
			if strings.HasPrefix(kind, "declaration") {
				f.plan.Method = managedcontent.DDM
			}
			f.delivery.inspectErr = errors.New("offline")
			_, err := f.workflow.Install(t.Context(), "wire", f.plan)
			requireContent(t, err == nil, err)
			_, err = f.workflow.Step(t.Context(), "wire", "prepare", time.Minute)
			requireContent(t, err != nil, err)
			editContentRecord(t, f.a, "managedcontent/evidence/", func(record *state.Record) {
				var e managedcontent.Evidence
				err := json.Unmarshal(record.Value, &e)
				requireContent(t, err == nil, err)
				switch kind {
				case "bad-plist":
					e.Dispatch.MDM = []byte("invalid plist")
				case "command-uuid", "command-asset":
					id := e.Dispatch.ID
					asset := int64(1)
					if kind == "command-uuid" {
						id = "unowned-id"
					} else {
						asset = 2
					}
					command, err := mdm.NewCommand(&commands.InstallApplication{ITunesStoreID: &asset, Options: &commands.InstallApplicationOptions{PurchaseMethod: new(int64(1))}}, mdm.WithUUID(id))
					requireContent(t, err == nil, err)
					e.Dispatch.MDM = command.Raw
				case "declaration-count":
					e.Dispatch.Declarations = e.Dispatch.Declarations[:1]
				case "declaration-token":
					e.Dispatch.Declarations[0].ServerToken = "unowned-token"
				}
				e.Dispatch.Digest = ""
				raw, err := jsonv2.Marshal(e.Dispatch)
				requireContent(t, err == nil, err)
				e.Dispatch.Digest = fmt.Sprintf("%x", sha256.Sum256(raw))
				record.Value, err = json.Marshal(e)
				requireContent(t, err == nil, err)
			})
			f.delivery.inspectErr = nil
			_, err = f.restart(t).Step(t.Context(), "wire", "resume", time.Minute)
			requireContent(t, errors.Is(err, managedcontent.ErrConflict) && f.delivery.posts == 0, kind, err)
		})
	}
}
