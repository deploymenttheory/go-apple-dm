package service_test

import (
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

func TestReenrollmentAfterCheckoutRequiresApprovedNewIdentity(t *testing.T) {
	for _, allow := range []bool{false, true} {
		name, policy := "denied", service.DenyReenroll
		if allow {
			name, policy = "allowed", service.AllowReenroll
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, service.Config{Reenroll: policy})
			ctx := t.Context()
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
			enroll(t, h, id.ID)
			if _, err := h.core.Checkin(ctx, req(h.cert), simple(t, "CheckOut", id.ID, nil)); err != nil {
				t.Fatal(err)
			}
			if _, err := h.core.Checkin(ctx, req(h.cert), authenticate(t, id.ID)); !errors.Is(err, storage.ErrDisabled) {
				t.Fatalf("old identity reactivated enrollment: %v", err)
			}
			_, err := h.core.Checkin(ctx, req(h.cert2), authenticate(t, id.ID))
			if !allow {
				if !errors.Is(err, service.ErrReenrollDenied) {
					t.Fatalf("missing policy refusal: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("approved re-enrollment rejected: %v", err)
			}
			e, err := h.store.Get(ctx, id)
			if err != nil || e.Enabled || !e.DisabledAt.IsZero() || e.CertHash != cms.Fingerprint(h.cert2) || e.Push.Valid() {
				t.Fatalf("new enrollment did not await fresh tokens: %+v %v", e, err)
			}
			if _, err = h.core.Checkin(ctx, req(h.cert), tokenUpdate(t, id.ID, nil)); !errors.Is(err, service.ErrCertMismatch) {
				t.Fatalf("old identity authorized: %v", err)
			}
			if _, err = h.core.Checkin(ctx, req(h.cert2), tokenUpdate(t, id.ID, nil)); err != nil {
				t.Fatal(err)
			}
			e, err = h.store.Get(ctx, id)
			if err != nil || !e.Enabled || e.TokenUpdatedAt.IsZero() {
				t.Fatalf("new identity did not complete enrollment: %+v %v", e, err)
			}
		})
	}
}
