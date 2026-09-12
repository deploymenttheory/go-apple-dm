package ddmsync

import (
	"context"
	"log/slog"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/dmhook"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// ServiceHook clears DDM state after successful checkout or authentication,
// including
// the device's user channels. Cleanup failures are logged.
type ServiceHook struct {
	engine      *ddm.Engine
	enrollments storage.EnrollmentStore
	log         *slog.Logger
}

// NewServiceHook builds the hook; enrollments is used to find a device's
// user channels.
func NewServiceHook(e *ddm.Engine, enrollments storage.EnrollmentStore, log *slog.Logger) *ServiceHook {
	if log == nil {
		log = e.Logger()
	}
	return &ServiceHook{engine: e, enrollments: enrollments, log: log}
}

// Before implements dmhook.Hook.
type retryKey struct{}

func (h *ServiceHook) Before(ctx context.Context, c *dmhook.Call) (context.Context, error) {
	if c != nil && c.Op == "checkin:Authenticate" && c.Request != nil &&
		c.Request.Certificate != nil &&
		h.enrollments != nil {
		e, err := h.enrollments.Get(ctx, c.Request.ID)
		if err == nil && e.CertHash == cms.Fingerprint(c.Request.Certificate) {
			ctx = context.WithValue(ctx, retryKey{}, true)
		}
	}
	return ctx, nil
}

// After implements dmhook.Hook.
func (h *ServiceHook) After(ctx context.Context, c *dmhook.Call, err error) {
	if err != nil || c == nil || c.Request == nil {
		return
	}
	if c.Op != "checkin:CheckOut" && c.Op != "checkin:Authenticate" {
		return
	}
	if retry, _ := ctx.Value(retryKey{}).(bool); retry && c.Op == "checkin:Authenticate" {
		return
	}
	h.clear(ctx, c.Request.ID)
}

func (h *ServiceHook) clear(ctx context.Context, id mdm.EnrollmentID) {
	if !id.Channel.IsUser() && h.enrollments != nil {
		cursor := ""
		for {
			res, err := h.enrollments.List(ctx, storage.EnrollmentQuery{ParentID: id.ID}, paging.Page{Cursor: cursor})
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
