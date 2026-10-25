package managedcontent

import (
	"context"
	"math"
	"slices"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// ownsLane permits a queued successor to inspect its explicitly named predecessor
// without transferring ownership. Promotion occurs only after terminal readback.
func ownsLane(owner laneOwner, e execution) bool {
	return owner.Method == e.Plan.Method && (owner.ID == e.ID || (e.Plan.PredecessorID != "" && owner.ID == e.Plan.PredecessorID && e.Evidence.Handoff == nil))
}

// eligiblePredecessor restricts succession to an explicit native installation
// scope and the same delivery method; it is not a management migration engine.
func eligiblePredecessor(old, next Operation) error {
	if old.ID == next.ID || old.SupersededBy != "" || old.Phase != Dispatched || old.Plan.Method != next.Plan.Method || old.Plan.Content.Asset.AdamID != next.Plan.Content.Asset.AdamID || old.Plan.Content.Kind != next.Plan.Content.Kind || old.Plan.Target.Enrollment != next.Plan.Target.Enrollment {
		return ErrConflict
	}
	if old.Observation == nil || (!old.Observation.Terminal && !old.Observation.Installed) {
		return ErrBusy
	}
	return nil
}

// handoff uses fresh owned device-processing evidence. ACK or declaration
// publication alone cannot fence a predecessor and authorize another operation.
func (w *Workflow) handoff(ctx context.Context, token execution) (execution, error) {
	old, private, err := w.LoadEvidence(ctx, token.Plan.PredecessorID)
	if err != nil {
		return token, err
	}
	if err := eligiblePredecessor(old, token.Operation); err != nil {
		return token, err
	}
	if private.Dispatch == nil {
		return token, ErrProtocol
	}
	digest, err := dispatchDigest(*private.Dispatch)
	if err != nil {
		return token, err
	}
	if digest != private.Dispatch.Digest {
		return token, ErrConflict
	}
	observed, err := w.delivery.Inspect(ctx, *private.Dispatch)
	if err != nil {
		return token, err
	}
	if err := observation(*private.Dispatch, observed); err != nil {
		return token, err
	}
	if !observed.Found || !observed.Terminal {
		return token, ErrBusy
	}
	keys := executionLocks(token)
	for _, key := range executionLocks(execution{Operation: old, Evidence: private}) {
		if !slices.Contains(keys, key) {
			keys = append(keys, key)
		}
	}
	var out execution
	err = w.store.Update(ctx, keys, func(tx state.Tx) error {
		if err := read(ctx, tx, operationKey(token.ID), &out.Operation); err != nil {
			return err
		}
		if err := read(ctx, tx, evidenceKey(token.ID), &out.Evidence); err != nil {
			return err
		}
		if out.Fence != token.Fence || out.Worker != token.Worker || !tx.Now().Before(out.LeaseUntil) {
			return ErrFence
		}
		var predecessor execution
		if err := read(ctx, tx, operationKey(old.ID), &predecessor.Operation); err != nil {
			return err
		}
		if err := read(ctx, tx, evidenceKey(old.ID), &predecessor.Evidence); err != nil {
			return err
		}
		if err := eligiblePredecessor(predecessor.Operation, out.Operation); err != nil {
			return err
		}
		if predecessor.Evidence.Dispatch == nil || predecessor.Evidence.Dispatch.Digest != private.Dispatch.Digest || predecessor.MDMID != out.MDMID {
			return ErrConflict
		}
		if predecessor.Evidence.PhysicalLane != "" && predecessor.Evidence.PhysicalLane != out.Evidence.PhysicalLane {
			return ErrConflict
		}
		if predecessor.Fence == math.MaxUint64 {
			return ErrFence
		}
		for _, key := range []string{laneKey(out.Plan), out.Evidence.PhysicalLane} {
			if key == "" {
				continue
			}
			var owner laneOwner
			if err := read(ctx, tx, key, &owner); err != nil {
				return err
			}
			if !ownsLane(owner, out) || owner.Epoch == math.MaxUint64 {
				return ErrConflict
			}
			owner.ID = out.ID
			owner.Epoch++
			if err := put(ctx, tx, key, owner); err != nil {
				return err
			}
		}
		if err := applyObservation(&predecessor, observed); err != nil {
			return err
		}
		predecessor.Fence++
		predecessor.SupersededBy = out.ID
		predecessor.Worker = ""
		predecessor.LeaseUntil = time.Time{}
		predecessor.UpdatedAt = tx.Now()
		out.Evidence.Handoff = &HandoffEvidence{ID: old.ID, PhysicalLane: predecessor.Evidence.PhysicalLane, Dispatch: *private.Dispatch, Observation: observed}
		if out.Plan.Method == DDM {
			if len(private.Dispatch.Declarations) != 2 {
				return ErrProtocol
			}
			out.Evidence.DeclarationID = private.Dispatch.Declarations[0].Identifier
		}
		if err := put(ctx, tx, operationKey(old.ID), predecessor.Operation); err != nil {
			return err
		}
		if err := put(ctx, tx, evidenceKey(old.ID), predecessor.Evidence); err != nil {
			return err
		}
		if err := put(ctx, tx, evidenceKey(out.ID), out.Evidence); err != nil {
			return err
		}
		return put(ctx, tx, operationKey(out.ID), out.Operation)
	})
	if err != nil {
		return token, err
	}
	return out, nil
}

// releaseLanes restores a completed predecessor only for never-submitted work.
// Fences and lane epochs advance rather than reverting, so ownership restoration
// cannot revive an old worker. Unknown or completed successor dispatch cannot cancel.
func releaseLanes(ctx context.Context, tx state.Tx, o Operation, e *Evidence) error {
	if o.Mode == AssignOnly {
		return nil
	}
	for _, key := range []string{laneKey(o.Plan), e.PhysicalLane} {
		if key == "" {
			continue
		}
		var owner laneOwner
		if err := read(ctx, tx, key, &owner); err != nil {
			return err
		}
		if e.Handoff == nil && o.Plan.PredecessorID != "" && owner.ID != o.ID {
			continue
		}
		if owner.Method != o.Plan.Method {
			return ErrConflict
		}
		if owner.ID == o.Plan.PredecessorID && o.Plan.PredecessorID != "" && e.Handoff == nil {
			continue
		}
		if owner.ID != o.ID {
			return ErrConflict
		}
		if e.Handoff != nil && (key == laneKey(o.Plan) || key == e.Handoff.PhysicalLane) {
			if owner.Epoch == math.MaxUint64 {
				return ErrFence
			}
			owner.ID = e.Handoff.ID
			owner.Epoch++
			if err := put(ctx, tx, key, owner); err != nil {
				return err
			}
		} else if err := tx.Delete(ctx, key); err != nil {
			return err
		}
	}
	if e.Handoff != nil {
		var predecessor Operation
		if err := read(ctx, tx, operationKey(e.Handoff.ID), &predecessor); err != nil {
			return err
		}
		if predecessor.SupersededBy != o.ID {
			return ErrConflict
		}
		if predecessor.Fence == math.MaxUint64 {
			return ErrFence
		}
		predecessor.Fence++
		predecessor.SupersededBy = ""
		predecessor.Worker = ""
		predecessor.LeaseUntil = time.Time{}
		predecessor.UpdatedAt = tx.Now()
		if err := put(ctx, tx, operationKey(predecessor.ID), predecessor); err != nil {
			return err
		}
	}
	e.PhysicalLane = ""
	return nil
}

// predecessorReference binds adapter ownership transfer to the exact prior dispatch identity.
func predecessorReference(e *HandoffEvidence) *DispatchReference {
	if e == nil {
		return nil
	}
	return &DispatchReference{ID: e.Dispatch.ID, Digest: e.Dispatch.Digest, Enrollment: e.Dispatch.Enrollment}
}
