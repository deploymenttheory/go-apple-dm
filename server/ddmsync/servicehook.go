package ddmsync

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/dmhook"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// ServiceHook clears DDM state after successful checkout or initial/changed-identity
// authentication, including the device's user channels. Same-certificate retries
// preserve state. Controlled profile replacement uses a separate operation and
// does not trigger this cleanup. Complete returns cleanup failures before success
// reaches the device. Shared SQL compositions roll back the encompassing check-in;
// independently committing stores do not provide cross-store rollback. Legacy
// enrollment stores without an authentication result use the core's pre-read
// classification and require external serialization for concurrent check-ins.
type ServiceHook struct {
	engine      *ddm.Engine
	enrollments storage.EnrollmentStore
	log         *slog.Logger
}

var _ dmhook.Completer = (*ServiceHook)(nil)

// NewServiceHook builds the hook; enrollments is used to find a device's
// user channels.
func NewServiceHook(e *ddm.Engine, enrollments storage.EnrollmentStore, log *slog.Logger) *ServiceHook {
	if log == nil {
		log = e.Logger()
	}
	return &ServiceHook{engine: e, enrollments: enrollments, log: log}
}

// Before passes through the context; the core reports the actual authentication
// outcome after storage rather than classifying it through a separate lookup.
func (h *ServiceHook) Before(ctx context.Context, _ *dmhook.Call) (context.Context, error) {
	return ctx, nil
}

// After observes no additional state; required cleanup runs in Complete.
func (h *ServiceHook) After(context.Context, *dmhook.Call, error) {}

// Complete clears successful lifecycle transitions and propagates failures to the
// check-in's transaction and response. Repeating a completed clear is harmless.
func (h *ServiceHook) Complete(ctx context.Context, c *dmhook.Call) error {
	if c == nil || c.Request == nil {
		return nil
	}
	if c.Op != "checkin:CheckOut" && c.Op != "checkin:Authenticate" {
		return nil
	}
	if c.Op == "checkin:Authenticate" && c.Authenticate != nil && !c.Authenticate.Reset {
		return nil
	}
	if err := h.clear(ctx, c.Request.ID); err != nil {
		h.log.WarnContext(ctx, "ddm: lifecycle cleanup", "enrollment", c.Request.ID.ID, "error", err)
		return err
	}
	return nil
}

// clear clears declarative state for the enrollment and any dependent user channels.
func (h *ServiceHook) clear(ctx context.Context, id mdm.EnrollmentID) error {
	if !id.Channel.IsUser() && h.enrollments != nil {
		cursor := ""
		for {
			res, err := h.enrollments.List(ctx, storage.EnrollmentQuery{ParentID: id.ID}, paging.Page{Cursor: cursor})
			if err != nil {
				return fmt.Errorf("ddm: list user channels for %s: %w", id.ID, err)
			}
			for _, child := range res.Items {
				if err := h.one(ctx, child.ID); err != nil {
					return err
				}
			}
			if res.NextCursor == "" {
				break
			}
			cursor = res.NextCursor
		}
	}
	return h.one(ctx, id)
}

// one clears one enrollment's declarative state through the engine.
func (h *ServiceHook) one(ctx context.Context, id mdm.EnrollmentID) error {
	if err := h.engine.ClearEnrollment(ctx, id); err != nil {
		return fmt.Errorf("ddm: clear enrollment %s: %w", id.ID, err)
	}
	return nil
}
