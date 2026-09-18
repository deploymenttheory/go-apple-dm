package service

import (
	"context"
	"crypto/subtle"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// AuthorizeResource authenticates a file request using the enrolled MDM identity.
// Unlike compatibility pinning modes for check-in, resource delivery always
// requires the exact pin, a known enabled channel and valid certificate status.
func (c *Core) AuthorizeResource(ctx context.Context, r *mdm.Request) error {
	if r == nil || r.Certificate == nil || r.ID.Validate() != nil {
		return ErrInvalidMessage
	}
	if c.certificateStatus != nil {
		if err := c.certificateStatus(ctx, r.Certificate); err != nil {
			return err
		}
	}
	now := c.clock.Now()
	if now.Before(r.Certificate.NotBefore) || !now.Before(r.Certificate.NotAfter) {
		return ErrInvalidMessage
	}
	for _, id := range []mdm.EnrollmentID{r.ID.Device(), r.ID} {
		enrollment, err := c.store.Get(ctx, id)
		if err != nil {
			return err
		}
		if !enrollment.Enabled || !enrollment.DisabledAt.IsZero() {
			return storage.ErrDisabled
		}
	}
	pin, err := c.store.CertHash(ctx, r.ID)
	if err != nil {
		return err
	}
	if pin == "" || subtle.ConstantTimeCompare([]byte(pin), []byte(cms.Fingerprint(r.Certificate))) != 1 {
		return ErrInvalidMessage
	}
	return nil
}
