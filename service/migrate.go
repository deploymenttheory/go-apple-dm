package service

import (
	"context"
	"fmt"

	"github.com/deploymenttheory/go-apple-dm/event"
	"github.com/deploymenttheory/go-apple-dm/paging"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// ExportEnrollments pages through enrollment records for migration. Device
// channels precede the user channels that belong to them, so a page can be
// replayed into ImportEnrollment in order (decision record 0017).
func (c *Core) ExportEnrollments(ctx context.Context, p paging.Page) (paging.Result[storage.EnrollmentExport], error) {
	ctx, after, err := c.runHooks(ctx, &Call{Op: "export"})
	if err != nil {
		return paging.Result[storage.EnrollmentExport]{}, err
	}
	res, err := c.store.Export(ctx, p)
	if err != nil {
		err = wrapCode(codeForStorage(err), fmt.Errorf("export enrollments: %w", err))
	}
	after(err)
	return res, err
}

// ImportEnrollment writes one exported record and publishes
// EnrollmentImported with actor "admin". The record is written exactly as
// given: a disabled enrollment stays disabled and the command queue is not
// touched.
func (c *Core) ImportEnrollment(ctx context.Context, rec storage.EnrollmentExport) error {
	ctx, after, err := c.runHooks(ctx, &Call{Op: "import"})
	if err != nil {
		return err
	}
	if err := c.store.Import(ctx, rec); err != nil {
		err = wrapCode(codeForStorage(err), fmt.Errorf("import enrollment %s: %w", rec.ID.ID, err))
		after(err)
		return err
	}
	c.publish(ctx, event.EnrollmentImported, rec.ID, "admin", rec.ID)
	after(nil)
	return nil
}
