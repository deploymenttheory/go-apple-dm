package storagetest

import (
	"bytes"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

type contentResolver struct {
	entered, release chan struct{}
	targets          []managedcontent.Target
	calls            atomic.Int64
	err              error
}

// Resolve returns the explicitly configured authenticated candidates and records resolution attempts.
func (r *contentResolver) Resolve(ctx context.Context, _ managedcontent.Selection) ([]managedcontent.Target, error) {
	if r.entered != nil {
		close(r.entered)
		r.entered = nil
		select {
		case <-r.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	r.calls.Add(1)
	return r.targets, r.err
}

type contentDelivery struct {
	failSuccessorInspect           bool
	inspectAfter                   func(managedcontent.Dispatch)
	terminal                       bool
	documents                      map[string][]byte
	nativeOwners                   map[string]string
	pending                        map[string]bool
	inspectEntered, inspectRelease chan struct{}
	modify                         func(*managedcontent.Observation)
	mu                             sync.Mutex
	items                          map[string]managedcontent.Dispatch
	posts                          int
	err                            error
	inspectErr                     error
	unknown                        bool
	foreign                        bool
	retrySafe                      bool
	hide                           bool
	ack                            bool
	published                      bool
	installed                      bool
	version                        string
	entered                        chan struct{}
	release                        chan struct{}
	after                          func()
	now                            *atomic.Int64
}

// observation constructs native readback from the adapter fixture state.
func (d *contentDelivery) observation(v managedcontent.Dispatch) managedcontent.Observation {
	o := managedcontent.Observation{ID: v.ID, Enrollment: v.Enrollment, Method: v.Method, Digest: v.Digest, Authoritative: !d.unknown, OwnsIdentity: !d.foreign, RetrySafe: d.retrySafe, ObservedAt: time.Unix(0, d.now.Load()).UTC()}
	if old, ok := d.items[v.ID]; ok && !d.hide {
		o.Found = true
		o.Terminal = d.terminal
		o.Digest = old.Digest
		o.Method = old.Method
		o.Enrollment = old.Enrollment
		o.ManagementMethod = old.Method
		o.CommandAcknowledged = d.ack
		o.DeclarationPublished = d.published
		o.Installed = d.installed
		o.AppVersion = d.version
		o.Raw = []byte("PRIVATE-DEVICE-EVIDENCE")
		if d.pending[v.ID] {
			o.Terminal = false
			o.Installed = false
			o.CommandAcknowledged = false
		}
	}
	if d.modify != nil {
		d.modify(&o)
	}
	return o
}

// Inspect reads stable native identity and ownership without dispatching another installation.
func (d *contentDelivery) Inspect(ctx context.Context, v managedcontent.Dispatch) (managedcontent.Observation, error) {
	d.mu.Lock()
	entered, release := d.inspectEntered, d.inspectRelease
	d.inspectEntered = nil
	d.inspectRelease = nil
	d.mu.Unlock()
	if entered != nil {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return managedcontent.Observation{}, ctx.Err()
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.inspectAfter != nil {
		d.inspectAfter(v)
	}
	if d.failSuccessorInspect && v.Predecessor != nil {
		return managedcontent.Observation{}, errors.New("successor read unavailable")
	}
	return d.observation(v), d.inspectErr
}

// Submit atomically accepts an owned stable dispatch or rejects a conflicting native scope.
func (d *contentDelivery) Submit(ctx context.Context, v managedcontent.Dispatch) (managedcontent.Observation, error) {
	d.mu.Lock()
	entered, release := d.entered, d.release
	d.entered = nil
	d.release = nil
	d.mu.Unlock()
	if entered != nil {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return managedcontent.Observation{}, ctx.Err()
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if old, ok := d.items[v.ID]; ok {
		left, le := jsonv2.Marshal(old)
		right, re := jsonv2.Marshal(v)
		if le != nil || re != nil || !bytes.Equal(left, right) {
			return managedcontent.Observation{}, managedcontent.ErrConflict
		}
	} else {
		scopeRaw, err := json.Marshal([]any{v.Enrollment, v.Content.Asset.AdamID})
		if err != nil {
			return managedcontent.Observation{}, err
		}
		scope := string(scopeRaw)
		if owner := d.nativeOwners[scope]; owner != "" {
			predecessor, ok := d.items[owner]
			if !ok || v.Predecessor == nil || v.Predecessor.ID != owner || v.Predecessor.Digest != predecessor.Digest || v.Predecessor.Enrollment != predecessor.Enrollment || predecessor.Method != v.Method || !d.terminal || d.pending[owner] {
				return managedcontent.Observation{}, managedcontent.ErrConflict
			}
		} else if v.Predecessor != nil {
			return managedcontent.Observation{}, managedcontent.ErrConflict
		}
		for _, doc := range v.Declarations {
			d.documents[doc.Identifier] = append([]byte(nil), doc.Raw...)
		}
		d.nativeOwners[scope] = v.ID
		d.pending[v.ID] = v.Predecessor != nil
		d.items[v.ID] = v
		d.posts++
	}
	if d.after != nil {
		d.after()
		d.after = nil
	}
	return d.observation(v), d.err
}

// contentFaultStore injects a required local capture/commit failure after all
// participating writes. The actual backend still supplies locks and rollback.
type contentFaultStore struct {
	state.Store
	fail atomic.Bool
}

// Update joins the underlying transaction while preserving fault injection and atomic rollback.
func (s *contentFaultStore) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	return s.Store.Update(ctx, keys, func(tx state.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if s.fail.Swap(false) {
			return errors.New("required capture failed PRIVATE-ERROR-MARKER")
		}
		return nil
	})
}

type contentFixture struct {
	a, b     state.Store
	now      *atomic.Int64
	service  *licensingService
	licenses *appsbooks.Workflow
	resolver *contentResolver
	delivery *contentDelivery
	workflow *managedcontent.Workflow
	plan     managedcontent.Plan
}

// newContentFixture creates real licensing HTTP and atomic state adapters for delivery contracts.
func newContentFixture(t *testing.T, factory AppsBooksFactory) *contentFixture {
	t.Helper()
	a, b := factory(t)
	now := &atomic.Int64{}
	now.Store(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	a = &jobTimeStore{Store: a, now: now}
	b = &jobTimeStore{Store: b, now: now}
	f, cfg, token := licensingFixture(t)
	f.mu.Lock()
	f.requested = 1
	f.completed = 0
	f.mu.Unlock()
	l := newAppsBooksWorkflow(t, a, cfg)
	if _, err := l.Register(t.Context(), "location", 0, token); err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "selected"}
	r := &contentResolver{targets: []managedcontent.Target{{Enrollment: id, Serial: "s1", Authenticated: true, AllowAppInstall: true, DDMEnabled: true, Support: support.Target{OS: support.IOS, Version: osversion.New(27, 0, 0), Channel: support.ChannelDevice, Supervised: true}}}}
	d := &contentDelivery{items: map[string]managedcontent.Dispatch{}, documents: map[string][]byte{}, nativeOwners: map[string]string{}, pending: map[string]bool{}, now: now}
	w, err := managedcontent.New(a, l, r, d)
	if err != nil {
		t.Fatal(err)
	}
	return &contentFixture{a: a, b: b, now: now, service: f, licenses: l, resolver: r, delivery: d, workflow: w, plan: managedcontent.Plan{LocationID: "location", Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}, Target: managedcontent.Selection{Enrollment: id, Serial: "s1"}, Method: managedcontent.MDM, MaxPages: 4}}
}

// restart reopens the workflow through the second backend handle.
func (f *contentFixture) restart(t *testing.T) *managedcontent.Workflow {
	t.Helper()
	w, err := managedcontent.New(f.b, f.licenses, f.resolver, f.delivery)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// complete polls explicit licensing work to its confirmed per-target completion.
func (f *contentFixture) complete(t *testing.T, id string) {
	t.Helper()
	op, err := f.workflow.Load(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	f.service.mu.Lock()
	f.service.status = "COMPLETE"
	f.service.completed = 1
	f.service.mu.Unlock()
	f.now.Add(int64(6 * time.Minute))
	if _, err := f.licenses.PollJob(t.Context(), "location", op.LicensingJobID, "poll", time.Minute); err != nil {
		t.Fatal(err)
	}
}

// requireContent reports a failed delivery invariant at the calling contract.
func requireContent(t *testing.T, ok bool, args ...any) {
	t.Helper()
	if !ok {
		t.Fatal(args...)
	}
}

// RunManagedContent exercises the same durable content workflow against memory
// and each sealed SQL backend. Apple calls go only to a real local TLS fixture;
// the delivery adapter models durable identity/readback, not physical installation.
func RunManagedContent(t *testing.T, factory AppsBooksFactory) {
	t.Helper()
	t.Run("QueuedStartAndConfirmedAssignment", func(t *testing.T) {
		f := newContentFixture(t, factory)
		op, err := f.workflow.AssignAndInstall(t.Context(), "operation", f.plan)
		requireContent(t, err == nil && op.Phase == managedcontent.Queued && f.resolver.calls.Load() == 0 && f.delivery.posts == 0, op, err)
		jobs, _, err := f.licenses.Jobs(t.Context(), "location", "", 10)
		requireContent(t, err == nil && len(jobs) == 0, jobs, err)
		op, err = f.workflow.Step(t.Context(), op.ID, "worker", time.Minute)
		requireContent(t, errors.Is(err, managedcontent.ErrLicense) && op.Phase == managedcontent.WaitingLicense && f.delivery.posts == 0, op, err)
		f.complete(t, op.ID)
		op, err = f.restart(t).Step(t.Context(), op.ID, "second", time.Minute)
		requireContent(t, err == nil && op.Phase == managedcontent.Dispatched && op.License.Confirmed && f.delivery.posts == 1 && !op.Observation.Installed && !op.Observation.CommandAcknowledged, op, err)
		f.now.Add(int64(time.Second))
		f.delivery.ack = true
		op, err = f.restart(t).Step(t.Context(), op.ID, "ack", time.Minute)
		requireContent(t, err == nil && op.Observation.CommandAcknowledged && !op.Observation.Installed && f.delivery.posts == 1, op, err)
		f.now.Add(int64(time.Second))
		f.delivery.installed = true
		f.delivery.version = "2.0"
		op, err = f.restart(t).Step(t.Context(), op.ID, "status", time.Minute)
		requireContent(t, err == nil && op.Observation.Installed && op.Observation.AppVersion == "2.0" && f.delivery.posts == 1, op, err)
		_, proof, err := f.workflow.LoadEvidence(t.Context(), op.ID)
		requireContent(t, err == nil && len(proof.Assignment.Assignments.Pages) > 0 && len(proof.Dispatch.MDM) > 0 && string(proof.Observation.Raw) == "PRIVATE-DEVICE-EVIDENCE", proof, err)
		raw, err := json.Marshal(op)
		requireContent(t, err == nil && !strings.Contains(string(raw), "PRIVATE-") && !strings.Contains(string(raw), "configuration"), string(raw), err)
		_, err = f.workflow.AssignAndInstall(t.Context(), "other", func() managedcontent.Plan { p := f.plan; p.Method = managedcontent.DDM; return p }())
		requireContent(t, errors.Is(err, managedcontent.ErrConflict), err)
	})
	t.Run("ExistingLicenseWithoutJobAndImmutableIdentity", func(t *testing.T) {
		f := newContentFixture(t, factory)
		op, err := f.workflow.Install(t.Context(), "existing", f.plan)
		requireContent(t, err == nil, err)
		again, err := f.restart(t).Install(t.Context(), op.ID, f.plan)
		requireContent(t, err == nil && again.CreatedAt == op.CreatedAt, again, err)
		p := f.plan
		p.Method = managedcontent.DDM
		_, err = f.workflow.Install(t.Context(), op.ID, p)
		requireContent(t, errors.Is(err, managedcontent.ErrConflict), err)
		op, err = f.workflow.Step(t.Context(), op.ID, "worker", time.Minute)
		requireContent(t, err == nil && op.Phase == managedcontent.Dispatched && f.delivery.posts == 1, op, err)
		f.service.mu.Lock()
		posts := f.service.posts
		f.service.mu.Unlock()
		requireContent(t, posts == 0, "install unexpectedly submitted licensing", posts)
	})
	t.Run("IntentRollbackIncludesLaneAndEvidence", func(t *testing.T) {
		f := newContentFixture(t, factory)
		fault := &contentFaultStore{Store: f.a}
		fault.fail.Store(true)
		w, err := managedcontent.New(fault, f.licenses, f.resolver, f.delivery)
		requireContent(t, err == nil, err)
		_, err = w.AssignAndInstall(t.Context(), "rolled-back", f.plan)
		requireContent(t, err != nil, err)
		_, err = f.workflow.Load(t.Context(), "rolled-back")
		requireContent(t, errors.Is(err, state.ErrNotFound), err)
		_, _, err = f.workflow.LoadEvidence(t.Context(), "rolled-back")
		requireContent(t, errors.Is(err, state.ErrNotFound), err)
		_, err = f.workflow.AssignAndInstall(t.Context(), "replacement", f.plan)
		requireContent(t, err == nil, err)
		jobs, _, err := f.licenses.Jobs(t.Context(), "location", "", 10)
		requireContent(t, err == nil && len(jobs) == 0 && f.delivery.posts == 0 && f.resolver.calls.Load() == 0, jobs, err)
	})
	t.Run("AcceptedBeforeLocalFailureReadback", func(t *testing.T) {
		f := newContentFixture(t, factory)
		fault := &contentFaultStore{Store: f.a}
		w, err := managedcontent.New(fault, f.licenses, f.resolver, f.delivery)
		requireContent(t, err == nil, err)
		_, err = w.Install(t.Context(), "crashed", f.plan)
		requireContent(t, err == nil, err)
		f.delivery.after = func() { fault.fail.Store(true) }
		_, err = w.Step(t.Context(), "crashed", "old", time.Minute)
		requireContent(t, err != nil && f.delivery.posts == 1, err)
		persisted, err := f.workflow.Load(t.Context(), "crashed")
		requireContent(t, err == nil && persisted.Phase == managedcontent.Submitting, persisted, err)
		_, err = f.restart(t).Step(t.Context(), "crashed", "no-replay", time.Minute)
		requireContent(t, errors.Is(err, managedcontent.ErrBusy) || errors.Is(err, managedcontent.ErrReconciliation), err)
		f.now.Add(int64(2 * time.Minute))
		op, err := f.restart(t).Reconcile(t.Context(), "crashed", "recover", time.Minute, "old process stopped", false)
		requireContent(t, err == nil && op.Phase == managedcontent.Dispatched && f.delivery.posts == 1, op, err)
	})
	t.Run("UncertaintyNeverAuthorizesBlindReplay", func(t *testing.T) {
		f := newContentFixture(t, factory)
		_, err := f.workflow.Install(t.Context(), "uncertain", f.plan)
		requireContent(t, err == nil, err)
		f.delivery.err = errors.New("transport https://private.invalid/?token=PRIVATE-ERROR-MARKER")
		op, err := f.workflow.Step(t.Context(), "uncertain", "worker", time.Minute)
		requireContent(t, errors.Is(err, managedcontent.ErrReconciliation) && op.Phase == managedcontent.Uncertain, op, err)
		f.delivery.hide = true
		f.delivery.unknown = true
		_, err = f.restart(t).Reconcile(t.Context(), op.ID, "read", time.Minute, "original stopped", true)
		requireContent(t, errors.Is(err, managedcontent.ErrReconciliation) && f.delivery.posts == 1, err)
		f.delivery.unknown = false
		_, err = f.restart(t).Reconcile(t.Context(), op.ID, "read2", time.Minute, "original stopped", true)
		requireContent(t, errors.Is(err, managedcontent.ErrReconciliation) && f.delivery.posts == 1, err)
		public, err := f.workflow.Load(t.Context(), op.ID)
		requireContent(t, err == nil, err)
		raw, err := json.Marshal(public)
		requireContent(t, err == nil && !strings.Contains(string(raw), "PRIVATE-") && !strings.Contains(string(raw), "private.invalid"), string(raw), err)
		f.delivery.hide = false
		f.delivery.err = nil
		op, err = f.restart(t).Reconcile(t.Context(), op.ID, "read3", time.Minute, "original stopped", false)
		requireContent(t, err == nil && op.Phase == managedcontent.Dispatched && f.delivery.posts == 1, op, err)
	})
	t.Run("CompletedDispatchCannotBeRepeatedAfterPruning", func(t *testing.T) {
		f := newContentFixture(t, factory)
		_, err := f.workflow.Install(t.Context(), "completed", f.plan)
		requireContent(t, err == nil, err)
		_, err = f.workflow.Step(t.Context(), "completed", "worker", time.Minute)
		requireContent(t, err == nil, err)
		_, err = f.workflow.Cancel(t.Context(), "completed")
		requireContent(t, errors.Is(err, managedcontent.ErrReconciliation), err)
		f.delivery.hide = true
		f.delivery.retrySafe = true
		_, err = f.restart(t).Reconcile(t.Context(), "completed", "recover", time.Minute, "old process stopped", true)
		requireContent(t, errors.Is(err, managedcontent.ErrReconciliation) && f.delivery.posts == 1, err)
		_, err = f.restart(t).Step(t.Context(), "completed", "no-replay", time.Minute)
		requireContent(t, errors.Is(err, managedcontent.ErrReconciliation) && f.delivery.posts == 1, err)
	})
	t.Run("ExplicitRetryFencesOldWorkerAndUsesSameIdentity", func(t *testing.T) {
		f := newContentFixture(t, factory)
		_, err := f.workflow.Install(t.Context(), "fence", f.plan)
		requireContent(t, err == nil, err)
		entered, release := make(chan struct{}), make(chan struct{})
		f.delivery.entered = entered
		f.delivery.release = release
		f.delivery.retrySafe = true
		result := make(chan error, 1)
		go func() { _, err := f.workflow.Step(t.Context(), "fence", "old", time.Minute); result <- err }()
		<-entered
		f.now.Add(int64(2 * time.Minute))
		op, err := f.restart(t).Reconcile(t.Context(), "fence", "recover", time.Minute, "original worker fenced; exact identity is idempotent", true)
		requireContent(t, err == nil && op.Phase == managedcontent.Prepared, op, err)
		op, err = f.restart(t).Step(t.Context(), "fence", "new", time.Minute)
		requireContent(t, err == nil && op.Phase == managedcontent.Dispatched, op, err)
		close(release)
		err = <-result
		requireContent(t, errors.Is(err, managedcontent.ErrFence) && f.delivery.posts == 1, err, f.delivery.posts)
	})
	t.Run("SeparateAssignAndCancelBeforeDispatch", func(t *testing.T) {
		f := newContentFixture(t, factory)
		p := f.plan
		p.Method = ""
		op, err := f.workflow.Assign(t.Context(), "assign", p)
		requireContent(t, err == nil, err)
		_, err = f.workflow.Step(t.Context(), op.ID, "first", time.Minute)
		requireContent(t, errors.Is(err, managedcontent.ErrLicense), err)
		f.complete(t, op.ID)
		op, err = f.workflow.Step(t.Context(), op.ID, "second", time.Minute)
		requireContent(t, err == nil && op.Phase == managedcontent.Licensed && op.Dispatch == nil && f.delivery.posts == 0, op, err)
		_, err = f.workflow.Install(t.Context(), "cancel", f.plan)
		requireContent(t, err == nil, err)
		op, err = f.workflow.Cancel(t.Context(), "cancel")
		requireContent(t, err == nil && op.Phase == managedcontent.Cancelled, op, err)
		_, err = f.workflow.Install(t.Context(), "new-owner", f.plan)
		requireContent(t, err == nil, err)
	})

	t.Run("OptionalSerialAndLicensingAliasShareNativeLane", func(t *testing.T) {
		f := newContentFixture(t, factory)
		contentUser(f, support.IOS, false)
		f.resolver.targets[0].Serial = "s1"
		_, err := f.workflow.Install(t.Context(), "first", f.plan)
		requireContent(t, err == nil, err)
		p := f.plan
		p.Target.Serial = "s1"
		p.Target.ClientUserID = "location-alias"
		p.Method = managedcontent.DDM
		_, err = f.workflow.Install(t.Context(), "alias", p)
		requireContent(t, errors.Is(err, managedcontent.ErrConflict), err)
		_, err = f.workflow.Step(t.Context(), "first", "worker", time.Minute)
		requireContent(t, err == nil && f.delivery.posts == 1, err)
	})
	t.Run("AuthenticatedPhysicalBindingAcrossEnrollments", func(t *testing.T) {
		f := newContentFixture(t, factory)
		_, err := f.workflow.Install(t.Context(), "first", f.plan)
		requireContent(t, err == nil, err)
		second := f.plan
		second.Target.Enrollment.ID = "reenrolled"
		second.Method = managedcontent.DDM
		candidate := f.resolver.targets[0]
		candidate.Enrollment = second.Target.Enrollment
		f.resolver.targets = append(f.resolver.targets, candidate)
		_, err = f.workflow.Install(t.Context(), "second", second)
		requireContent(t, err == nil, "queued intents must not trust unverified physical serial", err)
		_, err = f.workflow.Step(t.Context(), "first", "first", time.Minute)
		requireContent(t, err == nil, err)
		_, err = f.workflow.Step(t.Context(), "second", "second", time.Minute)
		requireContent(t, errors.Is(err, managedcontent.ErrConflict) && f.delivery.posts == 1, err)
	})
	t.Run("CancelReleasesNeverSubmittedPhysicalBinding", func(t *testing.T) {
		f := newContentFixture(t, factory)
		f.delivery.inspectErr = errors.New("offline")
		_, err := f.workflow.Install(t.Context(), "first", f.plan)
		requireContent(t, err == nil, err)
		_, err = f.workflow.Step(t.Context(), "first", "worker", time.Minute)
		requireContent(t, err != nil && f.delivery.posts == 0, err)
		_, err = f.workflow.Cancel(t.Context(), "first")
		requireContent(t, err == nil, err)
		p := f.plan
		p.Method = managedcontent.DDM
		f.delivery.inspectErr = nil
		_, err = f.workflow.Install(t.Context(), "second", p)
		requireContent(t, err == nil, err)
		_, err = f.workflow.Step(t.Context(), "second", "worker", time.Minute)
		requireContent(t, err == nil && f.delivery.posts == 1, err)
	})
	t.Run("ScopedStore35Characters", func(t *testing.T) {
		f := newContentFixture(t, func(t *testing.T) (state.Store, state.Store) {
			t.Helper()
			a, b := factory(t)
			prefix := strings.Repeat("x", 34) + "/"
			return contentPrefixStore{Store: a, prefix: prefix}, contentPrefixStore{Store: b, prefix: prefix}
		})
		_, err := f.workflow.Install(t.Context(), "scope", f.plan)
		requireContent(t, err == nil, err)
		op, err := f.restart(t).Step(t.Context(), "scope", "worker", time.Minute)
		requireContent(t, err == nil && op.Phase == managedcontent.Dispatched && f.delivery.posts == 1, op, err)
		items, _, err := f.workflow.Operations(t.Context(), "", 1)
		requireContent(t, err == nil && len(items) == 1 && items[0].ID == op.ID, items, err)
	})
	for _, kind := range []string{"payload-byte", "digest", "target", "declaration-token"} {
		t.Run("PersistedMismatch/"+kind, func(t *testing.T) {
			f := newContentFixture(t, factory)
			if kind == "declaration-token" {
				f.plan.Method = managedcontent.DDM
			}
			f.delivery.inspectErr = errors.New("offline")
			_, err := f.workflow.Install(t.Context(), "corrupt", f.plan)
			requireContent(t, err == nil, err)
			_, err = f.workflow.Step(t.Context(), "corrupt", "prepare", time.Minute)
			requireContent(t, err != nil && f.delivery.posts == 0, err)
			records, err := f.a.List(t.Context(), "managedcontent/evidence/", "", 10)
			requireContent(t, err == nil && len(records) == 1, records, err)
			ops, err := f.a.List(t.Context(), "managedcontent/op/", "", 10)
			requireContent(t, err == nil && len(ops) == 1, ops, err)
			record := records[0]
			var evidence managedcontent.Evidence
			err = json.Unmarshal(record.Value, &evidence)
			requireContent(t, err == nil, err)
			switch kind {
			case "payload-byte":
				evidence.Dispatch.MDM[0] ^= 1
			case "digest":
				evidence.Dispatch.Digest = "wrong"
			case "target":
				evidence.Dispatch.Enrollment.ID = "other"
			case "declaration-token":
				evidence.Dispatch.Declarations[0].ServerToken = "wrong"
			}
			record.Value, err = json.Marshal(evidence)
			requireContent(t, err == nil, err)
			err = f.a.Update(t.Context(), []string{ops[0].Key}, func(tx state.Tx) error { return tx.Put(t.Context(), record) })
			requireContent(t, err == nil, err)
			f.delivery.inspectErr = nil
			_, err = f.restart(t).Step(t.Context(), "corrupt", "resume", time.Minute)
			requireContent(t, errors.Is(err, managedcontent.ErrConflict) && f.delivery.posts == 0, err)
		})
	}
	contentTargetContracts(t, factory)
	contentFailureContracts(t, factory)
	contentSuccessorContracts(t, factory)
}

// contentTargetContracts exercise platform/channel boundaries and exact selection
// using actual source capabilities and assignment reads, not inferred inventories.
func contentTargetContracts(t *testing.T, factory AppsBooksFactory) {
	t.Helper()
	for _, tc := range []struct {
		name   string
		change func(*contentFixture)
		want   error
	}{
		{"SerialMismatch", func(f *contentFixture) { f.resolver.targets[0].Serial = "different" }, managedcontent.ErrTarget},
		{"AmbiguousSelectedEnrollment", func(f *contentFixture) { f.resolver.targets = append(f.resolver.targets, f.resolver.targets[0]) }, managedcontent.ErrTarget},
		{"Unauthenticated", func(f *contentFixture) { f.resolver.targets[0].Authenticated = false }, managedcontent.ErrTarget},
		{"UnclaimedLocation", func(f *contentFixture) { f.service.mu.Lock(); f.service.owner = ""; f.service.mu.Unlock() }, appsbooks.ErrOwnership},
		{"WrongProduct", func(f *contentFixture) {
			f.service.mu.Lock()
			f.service.product = "Book"
			f.service.mu.Unlock()
		}, managedcontent.ErrLicense},
		{"TVOSAppDDMUnavailable", func(f *contentFixture) {
			f.plan.Method = managedcontent.DDM
			f.resolver.targets[0].Support.OS = support.TvOS
		}, managedcontent.ErrUnsupported},
		{"AppDDMVersionUnavailable", func(f *contentFixture) {
			f.plan.Method = managedcontent.DDM
			f.resolver.targets[0].Support.Version = osversion.New(17, 1, 0)
		}, managedcontent.ErrUnsupported},
		{"ModernMacBookUnavailable", func(f *contentFixture) {
			contentUser(f, support.MacOS, false)
			f.plan.Content.Kind = "Book"
			f.service.mu.Lock()
			f.service.product = "Book"
			f.service.mu.Unlock()
		}, managedcontent.ErrUnsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newContentFixture(t, factory)
			tc.change(f)
			_, err := f.workflow.Install(t.Context(), "invalid", f.plan)
			requireContent(t, err == nil, err)
			_, err = f.workflow.Step(t.Context(), "invalid", "worker", time.Minute)
			requireContent(t, errors.Is(err, tc.want) && f.delivery.posts == 0, tc.name, err)
			f.service.mu.Lock()
			posts := f.service.posts
			f.service.mu.Unlock()
			requireContent(t, posts == 0, "denied target licensed", posts)
		})
	}
	for _, name := range []string{"TVOSMDM", "PrivacyUserEnrollment", "DDMDevice", "DDMMacUser", "IOSBookUser", "LegacyMacBookUser", "MacDeviceMDM", "MacUserMDM", "MacUserEnrollmentMDM", "ExplicitSecondCandidate"} {
		t.Run(name, func(t *testing.T) {
			f := newContentFixture(t, factory)
			switch name {
			case "TVOSMDM":
				f.resolver.targets[0].Support.OS = support.TvOS
			case "PrivacyUserEnrollment":
				contentUser(f, support.IOS, true)
			case "DDMDevice":
				f.plan.Method = managedcontent.DDM
			case "DDMMacUser":
				contentUser(f, support.MacOS, true)
				f.plan.Method = managedcontent.DDM
			case "IOSBookUser":
				contentUser(f, support.IOS, true)
				f.plan.Content.Kind = "Book"
				f.service.mu.Lock()
				f.service.product = "Book"
				f.service.mu.Unlock()
			case "LegacyMacBookUser":
				contentUser(f, support.MacOS, false)
				f.plan.Content.Kind = "Book"
				f.resolver.targets[0].Support.Version = osversion.New(10, 15, 0)
				f.service.mu.Lock()
				f.service.product = "Book"
				f.service.mu.Unlock()
			case "MacDeviceMDM":
				f.resolver.targets[0].Support.OS = support.MacOS
			case "MacUserMDM":
				contentUser(f, support.MacOS, false)
			case "MacUserEnrollmentMDM":
				contentUser(f, support.MacOS, true)
			case "ExplicitSecondCandidate":
				other := f.resolver.targets[0]
				other.Enrollment.ID = "not-selected"
				other.Serial = "not-selected"
				f.resolver.targets = append([]managedcontent.Target{other}, f.resolver.targets...)
			}
			_, err := f.workflow.Install(t.Context(), "valid", f.plan)
			requireContent(t, err == nil, err)
			op, err := f.workflow.Step(t.Context(), "valid", "worker", time.Minute)
			requireContent(t, err == nil && op.Phase == managedcontent.Dispatched && f.delivery.posts == 1, name, op, err)
			_, e, err := f.workflow.LoadEvidence(t.Context(), op.ID)
			requireContent(t, err == nil, err)
			if f.plan.Method == managedcontent.DDM {
				requireContent(t, len(e.Dispatch.Declarations) == 2 && len(e.Dispatch.MDM) == 0 && !op.Observation.Installed, e, op)
			} else {
				command, err := mdm.DecodeCommand(e.Dispatch.MDM)
				requireContent(t, err == nil && command.UUID == op.Dispatch.ID, command, err)
				if f.resolver.targets[0].Support.OS == support.MacOS && f.plan.Content.Kind == "App" {
					app, ok := command.Payload.(*commands.InstallApplication)
					requireContent(t, ok, command.Payload)
					if f.resolver.targets[0].Support.UserEnrollment {
						requireContent(t, app.InstallAsManaged == nil, app)
					} else {
						requireContent(t, app.InstallAsManaged != nil && *app.InstallAsManaged, app)
					}
				}
			}
		})
	}
}

// contentUser selects a native user channel without requiring physical hardware disclosure.
func contentUser(f *contentFixture, os support.OS, privacy bool) {
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "user-device"}
	channel := support.ChannelDevice
	if os == support.MacOS {
		id = mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "device:user", ParentID: "device"}
		channel = support.ChannelUser
	}
	if privacy {
		if channel == support.ChannelDevice {
			id.Channel = mdm.ChannelUserEnrollmentDevice
		} else {
			id.Channel = mdm.ChannelUserEnrollmentUser
		}
	}
	f.plan.Target = managedcontent.Selection{Enrollment: id, ClientUserID: "u"}
	f.resolver.targets = []managedcontent.Target{{Enrollment: id, ClientUserID: "u", Authenticated: true, AllowAppInstall: true, DDMEnabled: true, Support: support.Target{OS: os, Version: osversion.New(27, 0, 0), Channel: channel, UserEnrollment: privacy, Supervised: !privacy}}}
}

// contentPrefixStore exercises actual backend key budgets with caller namespaces.
type contentPrefixStore struct {
	state.Store
	prefix string
}
type contentPrefixTx struct {
	state.Tx
	prefix string
}

// Get reads the underlying record while preserving the injected failure or namespace boundary.
func (s contentPrefixStore) Get(ctx context.Context, key string) (state.Record, error) {
	r, err := s.Store.Get(ctx, s.prefix+key)
	r.Key = strings.TrimPrefix(r.Key, s.prefix)
	return r, err
}

// List reads the underlying page and restores caller-visible keys.
func (s contentPrefixStore) List(ctx context.Context, prefix, after string, limit int) ([]state.Record, error) {
	if after != "" {
		after = s.prefix + after
	}
	rows, err := s.Store.List(ctx, s.prefix+prefix, after, limit)
	for i := range rows {
		rows[i].Key = strings.TrimPrefix(rows[i].Key, s.prefix)
	}
	return rows, err
}

// Update joins the underlying transaction while preserving fault injection and atomic rollback.
func (s contentPrefixStore) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	scoped := make([]string, len(keys))
	for i, key := range keys {
		scoped[i] = s.prefix + key
	}
	return s.Store.Update(ctx, scoped, func(tx state.Tx) error { return fn(contentPrefixTx{Tx: tx, prefix: s.prefix}) })
}

// Get reads the underlying record while preserving the injected failure or namespace boundary.
func (s contentPrefixTx) Get(ctx context.Context, key string) (state.Record, error) {
	r, err := s.Tx.Get(ctx, s.prefix+key)
	r.Key = strings.TrimPrefix(r.Key, s.prefix)
	return r, err
}

// List reads the underlying page and restores caller-visible keys.
func (s contentPrefixTx) List(ctx context.Context, prefix, after string, limit int) ([]state.Record, error) {
	if after != "" {
		after = s.prefix + after
	}
	rows, err := s.Tx.List(ctx, s.prefix+prefix, after, limit)
	for i := range rows {
		rows[i].Key = strings.TrimPrefix(rows[i].Key, s.prefix)
	}
	return rows, err
}

// Put writes the record through the caller namespace.
func (s contentPrefixTx) Put(ctx context.Context, r state.Record) error {
	r.Key = s.prefix + r.Key
	return s.Tx.Put(ctx, r)
}

// Delete deletes the record through the caller namespace.
func (s contentPrefixTx) Delete(ctx context.Context, key string) error {
	return s.Tx.Delete(ctx, s.prefix+key)
}
