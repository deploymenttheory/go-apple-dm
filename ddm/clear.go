package ddm

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/service"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// ClearEnrollment forgets everything the engine holds for one enrollment:
// set and declaration assignments, the snapshot, status, and pending
// changes. Declarations and sets themselves are untouched.
func (e *Engine) ClearEnrollment(ctx context.Context, id mdm.EnrollmentID) error {
	if err := id.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return e.store.Update(ctx, func(tx Tx) error { return tx.ClearEnrollment(ctx, id) })
}

// ServiceHook clears DDM state when an enrollment checks out or
// re-authenticates (the device's user channels included), so a wiped or
// re-enrolled device never inherits declarations (KMFDDM #41).
type ServiceHook struct {
	engine      *Engine
	enrollments storage.EnrollmentStore
	log         *slog.Logger
}

// NewServiceHook builds the hook; enrollments is used to find a device's
// user channels.
func NewServiceHook(e *Engine, enrollments storage.EnrollmentStore, log *slog.Logger) *ServiceHook {
	if log == nil {
		log = e.log
	}
	return &ServiceHook{engine: e, enrollments: enrollments, log: log}
}

// Before implements service.Hook.
func (h *ServiceHook) Before(ctx context.Context, _ *service.Call) (context.Context, error) {
	return ctx, nil
}

// After implements service.Hook.
func (h *ServiceHook) After(ctx context.Context, c *service.Call, err error) {
	if err != nil || c == nil || c.Request == nil {
		return
	}
	if c.Op != "checkin:CheckOut" && c.Op != "checkin:Authenticate" {
		return
	}
	h.clear(ctx, c.Request.ID)
}

func (h *ServiceHook) clear(ctx context.Context, id mdm.EnrollmentID) {
	if !id.Channel.IsUser() && h.enrollments != nil {
		cursor := ""
		for {
			res, err := h.enrollments.List(ctx, storage.EnrollmentQuery{ParentID: id.ID}, storage.Page{Cursor: cursor})
			if err != nil {
				h.log.WarnContext(ctx, "ddm: list user channels", "enrollment", id.ID, "error", err)
				break
			}
			for _, child := range res.Items {
				h.one(ctx, child.ID)
			}
			if res.NextCursor == "" {
				break
			}
			cursor = res.NextCursor
		}
	}
	h.one(ctx, id)
}

func (h *ServiceHook) one(ctx context.Context, id mdm.EnrollmentID) {
	if err := h.engine.ClearEnrollment(ctx, id); err != nil {
		h.log.WarnContext(ctx, "ddm: clear enrollment", "enrollment", id.ID, "error", err)
	}
}
