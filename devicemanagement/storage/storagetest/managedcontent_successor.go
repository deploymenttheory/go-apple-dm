package storagetest

import (
	"encoding/json"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
)

// completedContent records real workflow progression and separate terminal device
// evidence before any explicit successor is requested.
func completedContent(t *testing.T, factory AppsBooksFactory, method managedcontent.Method) *contentFixture {
	t.Helper()
	f := newContentFixture(t, factory)
	f.plan.Method = method
	_, err := f.workflow.Install(t.Context(), "predecessor", f.plan)
	requireContent(t, err == nil, err)
	_, err = f.workflow.Step(t.Context(), "predecessor", "initial", time.Minute)
	requireContent(t, err == nil, err)
	f.delivery.terminal = true
	f.delivery.installed = true
	f.delivery.version = "1.0"
	f.now.Add(int64(time.Second))
	op, err := f.workflow.Step(t.Context(), "predecessor", "device-result", time.Minute)
	requireContent(t, err == nil && op.Observation.Terminal && op.Observation.Installed, op, err)
	f.plan.PredecessorID = "predecessor"
	return f
}

// contentSuccessorContracts verifies explicit terminal handoff, restart safety and monotonic cancellation fences.
func contentSuccessorContracts(t *testing.T, factory AppsBooksFactory) {
	t.Helper()

	t.Run("PrivacySuccessorDoesNotRequireHardware", func(t *testing.T) {
		f := newContentFixture(t, factory)
		contentUser(f, support.IOS, true)
		_, err := f.workflow.Install(t.Context(), "predecessor", f.plan)
		requireContent(t, err == nil, err)
		_, err = f.workflow.Step(t.Context(), "predecessor", "initial", time.Minute)
		requireContent(t, err == nil, err)
		f.delivery.terminal = true
		f.delivery.installed = true
		f.now.Add(int64(time.Second))
		_, err = f.workflow.Step(t.Context(), "predecessor", "status", time.Minute)
		requireContent(t, err == nil, err)
		f.plan.PredecessorID = "predecessor"
		_, err = f.workflow.Install(t.Context(), "child", f.plan)
		requireContent(t, err == nil, err)
		op, err := f.workflow.Step(t.Context(), "child", "next", time.Minute)
		requireContent(t, err == nil && op.Plan.Target.Serial == "" && f.delivery.posts == 2, op, err)
	})
	t.Run("MissingPredecessorCannotClaimScope", func(t *testing.T) {
		f := newContentFixture(t, factory)
		f.plan.PredecessorID = "missing"
		_, err := f.workflow.Install(t.Context(), "child", f.plan)
		requireContent(t, errors.Is(err, managedcontent.ErrConflict) && f.delivery.posts == 0, err)
	})
	for _, method := range []managedcontent.Method{managedcontent.MDM, managedcontent.DDM} {
		t.Run("ExplicitSuccessor/"+string(method), func(t *testing.T) {
			f := completedContent(t, factory, method)
			before, oldEvidence, err := f.workflow.LoadEvidence(t.Context(), "predecessor")
			requireContent(t, err == nil, err)
			f.delivery.installed = false
			f.now.Add(int64(time.Second))
			_, err = f.workflow.Install(t.Context(), "successor", f.plan)
			requireContent(t, err == nil, err)
			op, err := f.restart(t).Step(t.Context(), "successor", "advance", time.Minute)
			requireContent(t, err == nil && op.Phase == managedcontent.Dispatched && !op.Observation.Installed && !op.Observation.Terminal && f.delivery.posts == 2, op, err)
			old, err := f.workflow.Load(t.Context(), "predecessor")
			requireContent(t, err == nil && old.SupersededBy == op.ID && old.Fence > before.Fence && old.Observation.Terminal && !old.Observation.Installed, old, err)
			_, err = f.workflow.Step(t.Context(), "predecessor", "stale", time.Minute)
			requireContent(t, errors.Is(err, managedcontent.ErrConflict), err)
			_, proof, err := f.workflow.LoadEvidence(t.Context(), op.ID)
			requireContent(t, err == nil && proof.Dispatch.Predecessor.ID == oldEvidence.Dispatch.ID, proof, err)
			if method == managedcontent.MDM {
				requireContent(t, op.Dispatch.ID != before.Dispatch.ID, op, before)
			} else {
				requireContent(t, len(f.delivery.documents) == 2 && proof.Dispatch.Declarations[0].Identifier == oldEvidence.Dispatch.Declarations[0].Identifier && proof.Dispatch.Declarations[1].Identifier == oldEvidence.Dispatch.Declarations[1].Identifier, proof)
			}
			_, err = f.restart(t).Step(t.Context(), op.ID, "restart", time.Minute)
			requireContent(t, err == nil && f.delivery.posts == 2, err)
		})
	}
	t.Run("PendingUnknownAndMethodSwitchDoNotPromote", func(t *testing.T) {
		f := newContentFixture(t, factory)
		_, err := f.workflow.Install(t.Context(), "predecessor", f.plan)
		requireContent(t, err == nil, err)
		f.plan.PredecessorID = "predecessor"
		_, err = f.workflow.Install(t.Context(), "queued-child", f.plan)
		requireContent(t, err != nil, err)
		_, err = f.workflow.Step(t.Context(), "predecessor", "queue", time.Minute)
		requireContent(t, err == nil, err)
		_, err = f.workflow.Install(t.Context(), "pending-child", f.plan)
		requireContent(t, errors.Is(err, managedcontent.ErrBusy), err)
		f.delivery.terminal = true
		f.delivery.installed = true
		_, err = f.workflow.Step(t.Context(), "predecessor", "complete", time.Minute)
		requireContent(t, err == nil, err)
		f.plan.Method = managedcontent.DDM
		_, err = f.workflow.Install(t.Context(), "method-switch", f.plan)
		requireContent(t, errors.Is(err, managedcontent.ErrConflict), err)
	})
	for _, kind := range []string{"unknown", "missing", "pending", "foreign", "transport", "no-device-evidence"} {
		t.Run("FreshPredecessorProof/"+kind, func(t *testing.T) {
			f := completedContent(t, factory, managedcontent.MDM)
			_, err := f.workflow.Install(t.Context(), "successor", f.plan)
			requireContent(t, err == nil, err)
			switch kind {
			case "unknown":
				f.delivery.unknown = true
			case "missing":
				f.delivery.hide = true
			case "pending":
				f.delivery.terminal = false
			case "foreign":
				f.delivery.foreign = true
			case "transport":
				f.delivery.inspectErr = errors.New("read unavailable")
			case "no-device-evidence":
				f.delivery.modify = func(o *managedcontent.Observation) { o.Raw = nil }
			}
			_, err = f.workflow.Step(t.Context(), "successor", "advance", time.Minute)
			requireContent(t, err != nil && f.delivery.posts == 1, kind, err)
			old, err := f.workflow.Load(t.Context(), "predecessor")
			requireContent(t, err == nil && old.SupersededBy == "", old, err)
		})
	}
	t.Run("ConcurrentSuccessorsHaveOneWinner", func(t *testing.T) {
		f := completedContent(t, factory, managedcontent.MDM)
		for _, id := range []string{"a", "b"} {
			_, err := f.workflow.Install(t.Context(), id, f.plan)
			requireContent(t, err == nil, err)
		}
		results := make(chan error, 2)
		var group sync.WaitGroup
		for _, id := range []string{"a", "b"} {
			group.Go(func() { _, err := f.restart(t).Step(t.Context(), id, id, time.Minute); results <- err })
		}
		group.Wait()
		close(results)
		passed, denied := 0, 0
		for err := range results {
			if err == nil {
				passed++
			} else if errors.Is(err, managedcontent.ErrConflict) {
				denied++
			} else {
				t.Fatal(err)
			}
		}
		requireContent(t, passed == 1 && denied == 1 && f.delivery.posts == 2, passed, denied, f.delivery.posts)
		for _, id := range []string{"a", "b"} {
			op, err := f.workflow.Load(t.Context(), id)
			requireContent(t, err == nil, err)
			if op.Phase != managedcontent.Dispatched {
				// A worker that lost ownership may be unable to publish lease release.
				// Cancellation must still respect that durable lease until expiry.
				_, err = f.workflow.Cancel(t.Context(), id)
				if op.LeaseUntil.After(time.Unix(0, f.now.Load())) {
					requireContent(t, errors.Is(err, managedcontent.ErrBusy), err)
					f.now.Add(int64(2 * time.Minute))
					_, err = f.workflow.Cancel(t.Context(), id)
				}
				requireContent(t, err == nil, err)
			}
		}
	})
	t.Run("RestoreAfterCancelNeverRevivesOldWorker", func(t *testing.T) {
		f := completedContent(t, factory, managedcontent.MDM)
		before, err := f.workflow.Load(t.Context(), "predecessor")
		requireContent(t, err == nil, err)
		entered, release := make(chan struct{}), make(chan struct{})
		f.delivery.inspectEntered = entered
		f.delivery.inspectRelease = release
		result := make(chan error, 1)
		go func() { _, err := f.workflow.Step(t.Context(), "predecessor", "old-poll", time.Minute); result <- err }()
		<-entered
		_, err = f.workflow.Install(t.Context(), "successor", f.plan)
		requireContent(t, err == nil, err)
		f.delivery.failSuccessorInspect = true
		_, err = f.workflow.Step(t.Context(), "successor", "prepare", time.Minute)
		requireContent(t, err != nil && f.delivery.posts == 1, err)
		fault := &contentFaultStore{Store: f.a}
		fault.fail.Store(true)
		w, err := managedcontent.New(fault, f.licenses, f.resolver, f.delivery)
		requireContent(t, err == nil, err)
		_, err = w.Cancel(t.Context(), "successor")
		requireContent(t, err != nil, err)
		still, err := f.workflow.Load(t.Context(), "predecessor")
		requireContent(t, err == nil && still.SupersededBy == "successor", still, err)
		_, err = f.workflow.Cancel(t.Context(), "successor")
		requireContent(t, err == nil, err)
		restored, err := f.workflow.Load(t.Context(), "predecessor")
		requireContent(t, err == nil && restored.SupersededBy == "" && restored.Fence >= before.Fence+2, restored, err)
		close(release)
		err = <-result
		requireContent(t, errors.Is(err, managedcontent.ErrFence), err)
		f.delivery.failSuccessorInspect = false
		_, err = f.workflow.Install(t.Context(), "replacement", f.plan)
		requireContent(t, err == nil, err)
		_, err = f.workflow.Step(t.Context(), "replacement", "new", time.Minute)
		requireContent(t, err == nil && f.delivery.posts == 2, err)
	})
	t.Run("HandoffCaptureFailureRollsBackBothOperations", func(t *testing.T) {
		f := completedContent(t, factory, managedcontent.MDM)
		before, err := f.workflow.Load(t.Context(), "predecessor")
		requireContent(t, err == nil, err)
		fault := &contentFaultStore{Store: f.a}
		w, err := managedcontent.New(fault, f.licenses, f.resolver, f.delivery)
		requireContent(t, err == nil, err)
		_, err = w.Install(t.Context(), "successor", f.plan)
		requireContent(t, err == nil, err)
		f.delivery.inspectAfter = func(v managedcontent.Dispatch) {
			if v.ID == before.Dispatch.ID {
				fault.fail.Store(true)
			}
		}
		_, err = w.Step(t.Context(), "successor", "advance", time.Minute)
		requireContent(t, err != nil && f.delivery.posts == 1, err)
		old, err := f.workflow.Load(t.Context(), "predecessor")
		requireContent(t, err == nil && old.SupersededBy == "" && old.Fence == before.Fence, old, err)
		f.delivery.inspectAfter = nil
		_, err = f.restart(t).Step(t.Context(), "successor", "retry", time.Minute)
		requireContent(t, err == nil && f.delivery.posts == 2, err)
	})

	for _, kind := range []string{"read", "write"} {
		t.Run("RestorePersistenceBoundary/"+kind, func(t *testing.T) {
			for at := 1; at <= 30; at++ {
				f := completedContent(t, factory, managedcontent.MDM)
				_, err := f.workflow.Install(t.Context(), "child", f.plan)
				requireContent(t, err == nil, err)
				f.delivery.failSuccessorInspect = true
				_, err = f.workflow.Step(t.Context(), "child", "prepare", time.Minute)
				requireContent(t, err != nil, err)
				fault := &contentIOStore{Store: f.a, kind: kind, at: at}
				w, err := managedcontent.New(fault, f.licenses, f.resolver, f.delivery)
				requireContent(t, err == nil, err)
				_, first := w.Cancel(t.Context(), "child")
				if !fault.hit {
					requireContent(t, first == nil, first)
					break
				}
				requireContent(t, first != nil, at)
				_, err = f.workflow.Cancel(t.Context(), "child")
				requireContent(t, err == nil, err)
				old, err := f.workflow.Load(t.Context(), "predecessor")
				requireContent(t, err == nil && old.SupersededBy == "", old, err)
			}
		})
	}
	t.Run("ExpiredHandoffWorkerCannotPromoteAfterCancel", func(t *testing.T) {
		f := completedContent(t, factory, managedcontent.MDM)
		_, err := f.workflow.Install(t.Context(), "child", f.plan)
		requireContent(t, err == nil, err)
		entered, release := make(chan struct{}), make(chan struct{})
		f.delivery.inspectEntered = entered
		f.delivery.inspectRelease = release
		result := make(chan error, 1)
		go func() { _, err := f.workflow.Step(t.Context(), "child", "old", time.Minute); result <- err }()
		<-entered
		f.now.Add(int64(2 * time.Minute))
		_, err = f.workflow.Cancel(t.Context(), "child")
		requireContent(t, err == nil, err)
		close(release)
		err = <-result
		requireContent(t, errors.Is(err, managedcontent.ErrFence) && f.delivery.posts == 1, err)
	})
	for _, kind := range []string{"missing-private-dispatch", "changed-mdm", "fence-exhaustion", "restore-fence-exhaustion", "lane-epoch-exhaustion"} {
		t.Run("PredecessorIntegrity/"+kind, func(t *testing.T) {
			f := completedContent(t, factory, managedcontent.MDM)
			if kind == "missing-private-dispatch" {
				editContentRecord(t, f.a, "managedcontent/evidence/", func(r *state.Record) {
					var e managedcontent.Evidence
					err := json.Unmarshal(r.Value, &e)
					requireContent(t, err == nil, err)
					e.Dispatch = nil
					r.Value, err = json.Marshal(e)
					requireContent(t, err == nil, err)
				})
			}
			if kind == "changed-mdm" || kind == "fence-exhaustion" {
				editContentRecord(t, f.a, "managedcontent/op/", func(r *state.Record) {
					var o managedcontent.Operation
					err := json.Unmarshal(r.Value, &o)
					requireContent(t, err == nil, err)
					if kind == "changed-mdm" {
						o.MDMID = "foreign"
					} else {
						o.Fence = math.MaxUint64
					}
					r.Value, err = json.Marshal(o)
					requireContent(t, err == nil, err)
				})
			}
			if kind == "lane-epoch-exhaustion" {
				editContentRecord(t, f.a, "managedcontent/lane/", func(r *state.Record) {
					var o map[string]any
					err := json.Unmarshal(r.Value, &o)
					requireContent(t, err == nil, err)
					o["epoch"] = uint64(math.MaxUint64)
					r.Value, err = json.Marshal(o)
					requireContent(t, err == nil, err)
				})
			}
			_, err := f.workflow.Install(t.Context(), "child", f.plan)
			requireContent(t, err == nil, err)
			if kind == "restore-fence-exhaustion" {
				f.delivery.failSuccessorInspect = true
				_, err = f.workflow.Step(t.Context(), "child", "prepare", time.Minute)
				requireContent(t, err != nil, err)
				rows, err := f.a.List(t.Context(), "managedcontent/op/", "", 10)
				requireContent(t, err == nil, err)
				for _, r := range rows {
					var o managedcontent.Operation
					err := json.Unmarshal(r.Value, &o)
					requireContent(t, err == nil, err)
					if o.ID == "predecessor" {
						o.Fence = math.MaxUint64
						r.Value, err = json.Marshal(o)
						requireContent(t, err == nil, err)
						err = f.a.Update(t.Context(), []string{r.Key}, func(tx state.Tx) error { return tx.Put(t.Context(), r) })
						requireContent(t, err == nil, err)
					}
				}
				_, err = f.workflow.Cancel(t.Context(), "child")
				requireContent(t, errors.Is(err, managedcontent.ErrFence), err)
			} else {
				_, err = f.workflow.Step(t.Context(), "child", "advance", time.Minute)
				requireContent(t, err != nil && f.delivery.posts == 1, kind, err)
			}
		})
	}
	for _, kind := range []string{"read", "write"} {
		t.Run("HandoffPersistenceBoundary/"+kind, func(t *testing.T) {
			for at := 1; at <= 70; at++ {
				f := completedContent(t, factory, managedcontent.MDM)
				_, err := f.workflow.Install(t.Context(), "successor", f.plan)
				requireContent(t, err == nil, err)
				fault := &contentIOStore{Store: f.a, kind: kind, at: at}
				w, err := managedcontent.New(fault, f.licenses, f.resolver, f.delivery)
				requireContent(t, err == nil, err)
				_, first := w.Step(t.Context(), "successor", "first", time.Minute)
				if !fault.hit {
					requireContent(t, first == nil, kind, at, first)
					break
				}
				requireContent(t, first != nil, kind, at)
				f.now.Add(int64(2 * time.Minute))
				op, err := f.workflow.Load(t.Context(), "successor")
				requireContent(t, err == nil, err)
				if op.Phase == managedcontent.Submitting || op.Phase == managedcontent.Uncertain {
					_, err = f.restart(t).Reconcile(t.Context(), op.ID, "recover", time.Minute, "original worker stopped", false)
				} else {
					_, err = f.restart(t).Step(t.Context(), op.ID, "retry", time.Minute)
				}
				requireContent(t, err == nil && f.delivery.posts == 2, "successor recovery duplicated/lost dispatch", kind, at, err, f.delivery.posts)
			}
		})
	}
}
