package managedcontent

import (
	"context"
	"crypto/sha256"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// Workflow coordinates explicit content operations over caller-owned atomic
// state. Adapters own native enrollment, command queue and declaration storage;
// they need not adopt an inventory model. Apple and delivery calls run outside
// state transactions. Callers schedule Step and protect private source evidence.
type Workflow struct {
	store    state.Store
	licenses Licensing
	resolver Resolver
	delivery Delivery
}

type execution struct {
	Operation `json:"operation"`
	Evidence  `json:"evidence"`
}
type laneOwner struct {
	ID     string `json:"id"`
	Method Method `json:"method"`
	Epoch  uint64 `json:"epoch"`
}

// New accepts independent source/target/delivery adapters and existing atomic
// state. The state must protect the privileged Evidence records as well as intent.
func New(store state.Store, licenses Licensing, resolver Resolver, delivery Delivery) (*Workflow, error) {
	if store == nil || licenses == nil || resolver == nil || delivery == nil {
		return nil, ErrInput
	}
	return &Workflow{store: store, licenses: licenses, resolver: resolver, delivery: delivery}, nil
}

// key hashes caller identifiers into bounded state keys, including under scoped stores.
func key(kind, id string) string {
	return fmt.Sprintf("managedcontent/%s/%x", kind, sha256.Sum256([]byte(id)))
}

// operationKey addresses ordinary operation state independently of privileged evidence.
func operationKey(id string) string { return key("op", id) }

// evidenceKey addresses the privileged evidence retained for reconciliation.
func evidenceKey(id string) string { return key("evidence", id) }

// laneKey reserves the explicit native installation scope, independent of
// optional hardware disclosure, location, and Apple licensing-user aliases.
func laneKey(p Plan) string {
	raw, _ := json.Marshal([]any{p.Target.Enrollment, p.Content.Asset.AdamID})
	return key("lane", string(raw))
}

// physicalLane is bound only after authenticated resolution. ScopeID is the
// caller's native user principal, not a location's Apple clientUserId alias.
func physicalLane(p Plan, target Target) string {
	if target.Serial == "" {
		return ""
	}
	scope := "system"
	if target.Enrollment.Channel.IsUser() {
		scope = "user:" + target.ScopeID
	}
	raw, _ := json.Marshal([]string{target.Serial, scope, p.Content.Asset.AdamID})
	return key("physical", string(raw))
}

// executionLocks includes the authenticated physical lane after it has been bound.
func executionLocks(e execution) []string {
	keys := locks(e.Operation)
	if e.Evidence.PhysicalLane != "" {
		keys = append(keys, e.Evidence.PhysicalLane)
	}
	return keys
}

// read decodes one persisted record and rejects malformed state.
func read(ctx context.Context, r state.Reader, k string, v any) error {
	record, err := r.Get(ctx, k)
	if err != nil {
		return err
	}
	if json.Unmarshal(record.Value, v) != nil {
		return ErrProtocol
	}
	return nil
}

// put encodes one participating write inside the caller transaction.
func put(ctx context.Context, tx state.Tx, k string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return tx.Put(ctx, state.Record{Key: k, Value: raw})
}

// locks selects operation and native-scope locks before physical correlation.
func locks(o Operation) []string { return []string{operationKey(o.ID), laneKey(o.Plan)} }

// Load reads ordinary intent and safe status, without network calls or raw source
// pages, wire payloads, adapter errors or device evidence bodies.
func (w *Workflow) Load(ctx context.Context, id string) (Operation, error) {
	if id == "" {
		return Operation{}, ErrInput
	}
	var out Operation
	err := read(ctx, w.store, operationKey(id), &out)
	if err == nil && out.ID != id {
		return Operation{}, ErrProtocol
	}
	return out, err
}

// LoadEvidence is an explicitly privileged consistent observation. Callers must
// authorize it separately from ordinary operation reads.
func (w *Workflow) LoadEvidence(ctx context.Context, id string) (Operation, Evidence, error) {
	if id == "" {
		return Operation{}, Evidence{}, ErrInput
	}
	var e execution
	err := w.store.Update(ctx, []string{operationKey(id)}, func(tx state.Tx) error {
		if err := read(ctx, tx, operationKey(id), &e.Operation); err != nil {
			return err
		}
		if e.ID != id {
			return ErrProtocol
		}
		return read(ctx, tx, evidenceKey(id), &e.Evidence)
	})
	return e.Operation, e.Evidence, err
}

// Operations lists ordinary records by opaque state-key cursor. It makes no
// source or delivery calls. The returned cursor is exclusively a paging locator.
func (w *Workflow) Operations(ctx context.Context, after string, limit int) ([]Operation, string, error) {
	records, err := w.store.List(ctx, "managedcontent/op/", after, limit)
	if err != nil {
		return nil, "", err
	}
	out := make([]Operation, 0, len(records))
	cursor := after
	for _, record := range records {
		var op Operation
		if json.Unmarshal(record.Value, &op) != nil {
			return nil, "", ErrProtocol
		}
		out = append(out, op)
		cursor = record.Key
	}
	return out, cursor, nil
}

// Assign queues explicit licensing intent only. Start is local and atomic; no
// licensing job, Apple request or delivery is created until Step runs.
func (w *Workflow) Assign(ctx context.Context, id string, p Plan) (Operation, error) {
	return w.create(ctx, id, AssignOnly, p)
}

// Install queues installation using an existing applicable license. An optional
// LicensingJobID adds event-completion checks; fresh current assignment evidence
// is always required, including when a license predates this workflow.
func (w *Workflow) Install(ctx context.Context, id string, p Plan) (Operation, error) {
	return w.create(ctx, id, InstallOnly, p)
}

// AssignAndInstall queues licensing followed by an explicitly selected delivery
// method. A successful assignment never implicitly establishes installation.
func (w *Workflow) AssignAndInstall(ctx context.Context, id string, p Plan) (Operation, error) {
	return w.create(ctx, id, AssignAndInstall, p)
}

// validatePlan rejects inconsistent caller intent before reserving any state.
func validatePlan(mode Mode, p Plan) error {
	if mode != AssignOnly && mode != InstallOnly && mode != AssignAndInstall {
		return ErrInput
	}
	numeric, err := strconv.ParseInt(p.Content.Asset.AdamID, 10, 64)
	if err != nil || numeric <= 0 || strconv.FormatInt(numeric, 10) != p.Content.Asset.AdamID || p.LocationID == "" || p.MaxPages <= 0 ||
		(p.Content.Asset.PricingParam != "STDQ" && p.Content.Asset.PricingParam != "PLUS") || (p.Content.Kind != "App" && p.Content.Kind != "Book") || p.Target.Enrollment.Validate() != nil ||
		(p.Target.ClientUserID == "" && p.Target.Serial == "") || (p.Content.Kind == "Book" && p.Target.ClientUserID == "") {
		return ErrInput
	}
	if mode == AssignOnly {
		if p.Method != "" || p.LicensingJobID != "" || p.PredecessorID != "" {
			return ErrInput
		}
	} else if p.Method != MDM && p.Method != DDM {
		return ErrInput
	}
	if mode == AssignAndInstall && p.LicensingJobID != "" {
		return ErrInput
	}
	return nil
}

// create atomically reserves local intent and ownership without contacting adapters.
func (w *Workflow) create(ctx context.Context, id string, mode Mode, p Plan) (Operation, error) {
	if id == "" {
		return Operation{}, ErrInput
	}
	if err := validatePlan(mode, p); err != nil {
		return Operation{}, err
	}
	out := Operation{ID: id, Mode: mode, Plan: p, Phase: Queued, LicensingJobID: p.LicensingJobID}
	err := w.store.Update(ctx, locks(out), func(tx state.Tx) error {
		var prior Operation
		err := read(ctx, tx, operationKey(id), &prior)
		if err == nil {
			if prior.Mode != mode || !reflect.DeepEqual(prior.Plan, p) {
				return ErrConflict
			}
			out = prior
			return nil
		}
		if !errors.Is(err, state.ErrNotFound) {
			return err
		}
		if mode != AssignOnly {
			var owner laneOwner
			err := read(ctx, tx, laneKey(p), &owner)

			if err == nil {
				if owner.ID != p.PredecessorID || owner.Method != p.Method || p.PredecessorID == "" {
					return ErrConflict
				}
				var predecessor Operation
				if err := read(ctx, tx, operationKey(p.PredecessorID), &predecessor); err != nil {
					return err
				}
				if err := eligiblePredecessor(predecessor, out); err != nil {
					return err
				}
			} else {
				if !errors.Is(err, state.ErrNotFound) {
					return err
				}
				if p.PredecessorID != "" {
					return ErrConflict
				}
				if err := put(ctx, tx, laneKey(p), laneOwner{ID: id, Method: p.Method, Epoch: 1}); err != nil {
					return err
				}
			}
		}
		if mode != InstallOnly {
			out.LicensingJobID = fmt.Sprintf("content-%x", sha256.Sum256([]byte(id)))
		}
		out.CreatedAt = tx.Now()
		out.UpdatedAt = tx.Now()
		if err := put(ctx, tx, evidenceKey(id), Evidence{}); err != nil {
			return err
		}
		return put(ctx, tx, operationKey(id), out)
	})
	return out, err
}

// claim acquires an expiring worker fence after checking current lane ownership.
func (w *Workflow) claim(ctx context.Context, id, worker string, ttl time.Duration, recovery bool) (execution, error) {
	if id == "" || worker == "" || ttl <= 0 || ttl > time.Hour {
		return execution{}, ErrInput
	}
	initial, initialEvidence, err := w.LoadEvidence(ctx, id)
	if err != nil {
		return execution{}, err
	}
	var out execution
	err = w.store.Update(ctx, executionLocks(execution{Operation: initial, Evidence: initialEvidence}), func(tx state.Tx) error {
		if err := read(ctx, tx, operationKey(id), &out.Operation); err != nil {
			return err
		}
		if err := read(ctx, tx, evidenceKey(id), &out.Evidence); err != nil {
			return err
		}
		if out.Evidence.PhysicalLane != initialEvidence.PhysicalLane {
			return ErrFence
		}
		if out.ID != id || validatePlan(out.Mode, out.Plan) != nil {
			return ErrProtocol
		}
		if out.Phase != Queued && out.Phase != WaitingLicense && out.Phase != Prepared && out.Phase != Submitting && out.Phase != Uncertain && out.Phase != Dispatched && out.Phase != Cancelled && out.Phase != Licensed {
			return ErrProtocol
		}
		if out.SupersededBy != "" {
			return ErrConflict
		}
		if out.Phase == Cancelled || out.Phase == Licensed {
			return ErrConflict
		}
		if tx.Now().Before(out.LeaseUntil) {
			return ErrBusy
		}
		if !recovery && (out.Phase == Submitting || out.Phase == Uncertain) {
			return ErrReconciliation
		}
		if recovery && out.Phase != Submitting && out.Phase != Uncertain && out.Phase != Dispatched {
			return ErrInput
		}
		if out.Fence == math.MaxUint64 {
			return ErrFence
		}
		if out.Mode != AssignOnly {
			var owner laneOwner
			if err := read(ctx, tx, laneKey(out.Plan), &owner); err != nil {
				return err
			}
			if !ownsLane(owner, out) {
				return ErrConflict
			}
		}
		if out.Evidence.PhysicalLane != "" {
			var owner laneOwner
			if err := read(ctx, tx, out.Evidence.PhysicalLane, &owner); err != nil {
				return err
			}
			if !ownsLane(owner, out) {
				return ErrConflict
			}
		}
		out.Fence++
		out.Worker = worker
		out.LeaseUntil = tx.Now().Add(ttl)
		out.UpdatedAt = tx.Now()
		return put(ctx, tx, operationKey(id), out.Operation)
	})
	return out, err
}

// save fences every publication, including before Submit. A stale worker cannot
// transition intent into remote work after cancellation or recovery.
func (w *Workflow) save(ctx context.Context, token execution, done bool, change func(*execution) error) (execution, error) {
	var out execution
	err := w.store.Update(ctx, executionLocks(token), func(tx state.Tx) error {
		if err := read(ctx, tx, operationKey(token.ID), &out.Operation); err != nil {
			return err
		}
		if err := read(ctx, tx, evidenceKey(token.ID), &out.Evidence); err != nil {
			return err
		}
		if out.Evidence.PhysicalLane != token.Evidence.PhysicalLane || out.Fence != token.Fence || out.Worker != token.Worker || !tx.Now().Before(out.LeaseUntil) {
			return ErrFence
		}
		if err := change(&out); err != nil {
			return err
		}
		out.UpdatedAt = tx.Now()
		if done {
			out.Worker = ""
			out.LeaseUntil = time.Time{}
		}
		if err := put(ctx, tx, evidenceKey(out.ID), out.Evidence); err != nil {
			return err
		}
		return put(ctx, tx, operationKey(out.ID), out.Operation)
	})
	return out, err
}

// bind serializes native and authenticated physical ownership before licensing
// or delivery. Unverified serials in queued requests cannot claim physical lanes.
func (w *Workflow) bind(ctx context.Context, token execution, target Target, location appsbooks.Location) (execution, error) {
	var out execution
	physical := ""
	if token.Mode != AssignOnly {
		physical = physicalLane(token.Plan, target)
	}
	keys := executionLocks(token)
	if physical != "" && !slices.Contains(keys, physical) {
		keys = append(keys, physical)
	}
	err := w.store.Update(ctx, keys, func(tx state.Tx) error {
		if err := read(ctx, tx, operationKey(token.ID), &out.Operation); err != nil {
			return err
		}
		if err := read(ctx, tx, evidenceKey(token.ID), &out.Evidence); err != nil {
			return err
		}
		if out.Fence != token.Fence || out.Worker != token.Worker || !tx.Now().Before(out.LeaseUntil) {
			return ErrFence
		}
		if out.Evidence.PhysicalLane != "" && out.Evidence.PhysicalLane != physical {
			return ErrConflict
		}
		if physical != "" {
			var owner laneOwner
			err := read(ctx, tx, physical, &owner)
			if err == nil {
				if !ownsLane(owner, out) {
					return ErrConflict
				}
			} else if errors.Is(err, state.ErrNotFound) {
				if err := put(ctx, tx, physical, laneOwner{ID: out.ID, Method: out.Plan.Method, Epoch: 1}); err != nil {
					return err
				}
			} else {
				return err
			}
		}
		out.Evidence.PhysicalLane = physical
		out.LocationUID = location.UID
		out.MDMID = location.MDMID
		out.Target = target
		out.Blocked = ""
		out.UpdatedAt = tx.Now()
		if err := put(ctx, tx, evidenceKey(out.ID), out.Evidence); err != nil {
			return err
		}
		return put(ctx, tx, operationKey(out.ID), out.Operation)
	})
	return out, err
}

// blocked publishes a safe failure category without persisting arbitrary adapter error text.
func (w *Workflow) blocked(ctx context.Context, e execution, code string, cause error) (Operation, error) {
	out, err := w.save(ctx, e, true, func(current *execution) error { current.Blocked = code; return nil })
	return out.Operation, errors.Join(cause, err)
}

// Step performs one queued licensing/delivery advance. It never repeats an
// uncertain submission. Poll licensing through the existing Apps and Books
// Workflow, then call Step again; delivery status is inspected without POSTs.
func (w *Workflow) Step(ctx context.Context, id, worker string, ttl time.Duration) (Operation, error) {
	prior, err := w.Load(ctx, id)
	if err != nil {
		return Operation{}, err
	}
	if prior.Phase == Licensed || prior.Phase == Cancelled {
		return prior, nil
	}
	e, err := w.claim(ctx, id, worker, ttl, false)
	if err != nil {
		return prior, err
	}
	if e.Phase == Dispatched {
		return w.inspect(ctx, e, false, "")
	}
	target, err := w.resolve(ctx, e.Plan, e.Mode)
	if err != nil {
		return w.blocked(ctx, e, "target", err)
	}
	location, err := w.licenses.Load(ctx, e.Plan.LocationID)
	if err != nil {
		return w.blocked(ctx, e, "location", err)
	}
	if location.UID == "" || location.MDMID == "" || (e.LocationUID != "" && (e.LocationUID != location.UID || e.MDMID != location.MDMID)) {
		return w.blocked(ctx, e, "ownership", ErrConflict)
	}
	e, err = w.bind(ctx, e, target, location)
	if err != nil {
		return e.Operation, err
	}
	asset, err := w.licenses.ReadAsset(ctx, e.Plan.LocationID, e.Plan.Content.Asset, e.Plan.MaxPages)
	if err != nil {
		return w.blocked(ctx, e, "capability", err)
	}
	if asset.LocationID != e.Plan.LocationID || asset.UID != e.LocationUID || asset.MDMID != e.MDMID || asset.LocationRevision == 0 || asset.Source.ObservedAt.IsZero() || len(asset.Source.Pages) == 0 {
		return w.blocked(ctx, e, "ownership", ErrConflict)
	}
	if err := checkAsset(e.Plan, target, asset.Asset); err != nil {
		return w.blocked(ctx, e, "capability", err)
	}
	e.Evidence.Asset = &asset
	if e.LicensingJobID != "" {
		job, err := w.licensingJob(ctx, e, location, worker, ttl)
		if err != nil {
			return w.blocked(ctx, e, "licensing", err)
		}
		if job.State != appsbooks.JobSucceeded {
			out, err := w.save(ctx, e, true, func(current *execution) error {
				current.Phase = WaitingLicense
				current.Blocked = "licensing"
				current.Evidence.Asset = &asset
				return nil
			})
			return out.Operation, errors.Join(ErrLicense, err)
		}
		if job.Status == nil || !job.Status.Successful() || job.NotificationFailure || job.Status.UID != e.LocationUID || job.Status.EventType != "ASSOCIATE" || job.Status.NumRequested != int64(len(job.Request.Assets.Assets)*(len(job.Request.Assets.SerialNumbers)+len(job.Request.Assets.ClientUserIDs))) {
			return w.blocked(ctx, e, "licensing", ErrLicense)
		}
	}
	serial := e.Plan.Target.Serial
	if e.Plan.Target.ClientUserID != "" {
		serial = ""
	}
	proof, err := w.licenses.ConfirmAssignment(ctx, e.Plan.LocationID, e.Plan.Content.Asset, serial, e.Plan.Target.ClientUserID, e.Plan.MaxPages)
	if err != nil {
		return w.blocked(ctx, e, "assignment", err)
	}
	if err := checkAssignment(e, proof, target); err != nil {
		return w.blocked(ctx, e, "assignment", err)
	}
	if e.Plan.PredecessorID != "" && e.Evidence.Handoff == nil {
		e, err = w.handoff(ctx, e)
		if err != nil {
			return w.blocked(ctx, e, "predecessor", err)
		}
	}
	e, err = w.save(ctx, e, e.Mode == AssignOnly, func(current *execution) error {
		current.Evidence.Asset = &asset
		current.Evidence.Assignment = &proof
		current.Operation.License = &LicenseStatus{Confirmed: true, ObservedAt: proof.Assignments.ObservedAt}
		current.Blocked = ""
		if current.Mode == AssignOnly {
			current.Phase = Licensed
			return nil
		}

		dispatch := current.Evidence.Dispatch
		id := dispatchID(current.ID)
		if dispatch == nil {
			var err error
			declarationID := id
			if current.Evidence.DeclarationID != "" {
				declarationID = current.Evidence.DeclarationID
			}
			dispatch, err = buildDispatchFor(id, declarationID, current.Plan, target)
			if err == nil {
				dispatch.Predecessor = predecessorReference(current.Evidence.Handoff)
				dispatch.Digest, err = dispatchDigest(*dispatch)
			}
			if err != nil {
				return err
			}
		} else if err := validateDispatch(*dispatch, id, current.Evidence.DeclarationID, current.Evidence.Handoff, current.Plan, target); err != nil {
			return err
		}
		current.Evidence.Dispatch = dispatch
		current.Operation.Dispatch = &DispatchStatus{ID: dispatch.ID, Method: dispatch.Method, Digest: dispatch.Digest}

		current.Phase = Prepared
		return nil
	})
	if err != nil || e.Mode == AssignOnly {
		return e.Operation, err
	}
	return w.dispatch(ctx, e)
}

// checkAssignment requires the current per-target proof to match the operation authority and asset.
func checkAssignment(e execution, p appsbooks.AssignmentEvidence, target Target) error {
	if p.LocationID != e.Plan.LocationID || p.UID != e.LocationUID || p.MDMID != e.MDMID || p.LocationRevision == 0 || len(p.Configuration) == 0 || p.Assignments.ObservedAt.IsZero() || len(p.Assignments.Pages) == 0 || p.Assets.ObservedAt.IsZero() || len(p.Assets.Pages) == 0 {
		return ErrLicense
	}
	if err := checkAsset(e.Plan, target, p.Asset); err != nil {
		return err
	}
	a := p.Assignment
	serial := e.Plan.Target.Serial
	if e.Plan.Target.ClientUserID != "" {
		serial = ""
	}
	if a.Asset != e.Plan.Content.Asset || a.SerialNumber != serial || a.ClientUserID != e.Plan.Target.ClientUserID || (a.ClientUserID != "" && a.UserStatus != "Associated") {
		return ErrLicense
	}
	return nil
}

// licensingJob loads or starts the explicitly bound licensing intent without inferring target success.
func (w *Workflow) licensingJob(ctx context.Context, e execution, l appsbooks.Location, worker string, ttl time.Duration) (appsbooks.LicensingJob, error) {
	job, err := w.licenses.LoadJob(ctx, e.Plan.LocationID, e.LicensingJobID)
	if errors.Is(err, state.ErrNotFound) && e.Mode != InstallOnly {
		request := appsbooks.JobRequest{Operation: appsbooks.JobAssociate, MaxPages: e.Plan.MaxPages, Assets: &appsbooks.ManageAssetsRequest{Assets: []appsbooks.Asset{e.Plan.Content.Asset}}}
		if e.Plan.Target.ClientUserID != "" {
			request.Assets.ClientUserIDs = []string{e.Plan.Target.ClientUserID}
		} else {
			request.Assets.SerialNumbers = []string{e.Plan.Target.Serial}
		}
		job, err = w.licenses.CreateJob(ctx, l.ID, l.Revision, e.LicensingJobID, request)
	}
	if err != nil {
		return job, err
	}
	if job.ID != e.LicensingJobID || job.LocationID != e.Plan.LocationID || job.Request.Operation != appsbooks.JobAssociate || job.Request.Assets == nil || !slices.Contains(job.Request.Assets.Assets, e.Plan.Content.Asset) {
		return job, ErrLicense
	}
	targets := job.Request.Assets.SerialNumbers
	target := e.Plan.Target.Serial
	if e.Plan.Target.ClientUserID != "" {
		targets = job.Request.Assets.ClientUserIDs
		target = e.Plan.Target.ClientUserID
	}
	if !slices.Contains(targets, target) {
		return job, ErrLicense
	}
	if job.State == appsbooks.JobQueued && e.Mode != InstallOnly {
		return w.licenses.SubmitJob(ctx, e.Plan.LocationID, job.ID, worker, ttl)
	}
	return job, nil
}

// observation checks stable identity, ownership and device-evidence requirements on readback.
func observation(d Dispatch, o Observation) error {
	if !o.Authoritative {
		return ErrReconciliation
	}
	if !o.OwnsIdentity || o.ID != d.ID || o.Enrollment != d.Enrollment || o.Method != d.Method || o.Digest != d.Digest || (o.ManagementMethod != "" && o.ManagementMethod != d.Method) {
		return ErrConflict
	}
	if o.Found {
		if o.ObservedAt.IsZero() || ((o.Installed || o.Terminal) && len(o.Raw) == 0) || (d.Method == MDM && o.DeclarationPublished) || (d.Method == DDM && o.CommandAcknowledged) {
			return ErrProtocol
		}
	} else if o.CommandAcknowledged || o.DeclarationPublished || o.Installed || o.Terminal {
		return ErrProtocol
	}
	return nil
}

// applyObservation records separate safe status and privileged proof without accepting older observations.
func applyObservation(e *execution, o Observation) error {
	if e.Evidence.Observation != nil && o.ObservedAt.Before(e.Evidence.Observation.ObservedAt) {
		return ErrProtocol
	}
	e.Evidence.Observation = &o
	e.Operation.Observation = &DeliveryStatus{Terminal: o.Terminal, CommandAcknowledged: o.CommandAcknowledged, DeclarationPublished: o.DeclarationPublished, Installed: o.Installed, AppVersion: o.AppVersion, ObservedAt: o.ObservedAt}
	e.Phase = Dispatched
	e.Blocked = ""
	return nil
}

// dispatch reads native state before submitting the previously persisted immutable dispatch.
func (w *Workflow) dispatch(ctx context.Context, e execution) (Operation, error) {
	if e.Evidence.Dispatch == nil {
		return w.blocked(ctx, e, "dispatch", ErrProtocol)
	}
	d := *e.Evidence.Dispatch
	observed, err := w.delivery.Inspect(ctx, d)
	if err != nil {
		return w.blocked(ctx, e, "inspection", err)
	}
	if err := observation(d, observed); err != nil {
		return w.blocked(ctx, e, "inspection", err)
	}
	if observed.Found {
		out, err := w.save(ctx, e, true, func(current *execution) error { return applyObservation(current, observed) })
		return out.Operation, err
	}
	e, err = w.save(ctx, e, false, func(current *execution) error { current.Phase = Submitting; return nil })
	if err != nil {
		return e.Operation, err
	}
	observed, err = w.delivery.Submit(ctx, d)
	if err == nil {
		err = observation(d, observed)
		if err == nil && !observed.Found {
			err = ErrProtocol
		}
	}
	if err != nil {
		out, persistErr := w.save(ctx, e, true, func(current *execution) error {
			current.Phase = Uncertain
			current.Blocked = "reconciliation"
			return nil
		})
		return out.Operation, errors.Join(ErrReconciliation, err, persistErr)
	}
	out, err := w.save(ctx, e, true, func(current *execution) error { return applyObservation(current, observed) })
	return out.Operation, err
}

// Reconcile inspects uncertain work after the original worker has stopped and its
// lease expired. A note records that operator decision. retryAbsent additionally
// requires the adapter's authoritative ownership and atomically idempotent
// RetrySafe guarantee; absence alone never re-enables submission. An explicit
// subsequent Step revalidates target and licensing before the same stable dispatch.
func (w *Workflow) Reconcile(ctx context.Context, id, worker string, ttl time.Duration, note string, retryAbsent bool) (Operation, error) {
	if note == "" {
		return Operation{}, ErrInput
	}
	e, err := w.claim(ctx, id, worker, ttl, true)
	if err != nil {
		return Operation{}, err
	}
	return w.inspect(ctx, e, retryAbsent, note)
}

// inspect recovers an uncertain dispatch using authoritative readback and explicit retry permission.
func (w *Workflow) inspect(ctx context.Context, e execution, retryAbsent bool, note string) (Operation, error) {
	if e.Evidence.Dispatch == nil {
		return w.blocked(ctx, e, "inspection", ErrProtocol)
	}
	o, err := w.delivery.Inspect(ctx, *e.Evidence.Dispatch)
	if err == nil {
		err = observation(*e.Evidence.Dispatch, o)
	}
	if err != nil {
		return w.blocked(ctx, e, "inspection", err)
	}
	if o.Found {
		out, err := w.save(ctx, e, true, func(current *execution) error {
			if note != "" {
				current.RecoveryNote = note
			}
			return applyObservation(current, o)
		})
		return out.Operation, err
	}
	out, err := w.save(ctx, e, true, func(current *execution) error {
		current.Phase = Uncertain
		current.Blocked = "reconciliation"
		current.RecoveryNote = note
		if retryAbsent && o.RetrySafe && current.Evidence.Observation == nil {
			current.Phase = Prepared
			current.Blocked = ""
		}
		return nil
	})
	if retryAbsent && o.RetrySafe && e.Evidence.Observation == nil {
		return out.Operation, err
	}
	return out.Operation, errors.Join(ErrReconciliation, err)
}

// Cancel releases an operation that has never entered remote delivery. It cannot
// erase a submitted/uncertain dispatch or switch its management path. A licensing
// job may still complete; cancellation never revokes licenses or reclaims books.
func (w *Workflow) Cancel(ctx context.Context, id string) (Operation, error) {
	initial, initialEvidence, err := w.LoadEvidence(ctx, id)
	if err != nil {
		return Operation{}, err
	}
	var out Operation
	keys := executionLocks(execution{Operation: initial, Evidence: initialEvidence})
	if initial.Plan.PredecessorID != "" {
		keys = append(keys, operationKey(initial.Plan.PredecessorID))
	}
	err = w.store.Update(ctx, keys, func(tx state.Tx) error {
		if err := read(ctx, tx, operationKey(id), &out); err != nil {
			return err
		}
		if out.Phase == Cancelled {
			return nil
		}
		if tx.Now().Before(out.LeaseUntil) {
			return ErrBusy
		}
		if out.Phase != Queued && out.Phase != WaitingLicense && out.Phase != Prepared {
			return ErrReconciliation
		}
		if out.Fence == math.MaxUint64 {
			return ErrFence
		}

		var evidence Evidence
		if err := read(ctx, tx, evidenceKey(id), &evidence); err != nil {
			return err
		}
		if evidence.PhysicalLane != initialEvidence.PhysicalLane {
			return ErrFence
		}
		if err := releaseLanes(ctx, tx, out, &evidence); err != nil {
			return err
		}
		if err := put(ctx, tx, evidenceKey(id), evidence); err != nil {
			return err
		}

		out.Fence++
		out.Worker = ""
		out.LeaseUntil = time.Time{}
		out.Phase = Cancelled
		out.UpdatedAt = tx.Now()
		return put(ctx, tx, operationKey(id), out)
	})
	return out, err
}
