package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

func TestAuthorizedReplacementPreservesEnrollment(t *testing.T) {
	for _, outcome := range []string{"commit", "rollback", "expired", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			h := newHarness(t, service.Config{EnableReplacements: true, Reenroll: service.DenyReenroll})
			ctx := context.Background()
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
			enroll(t, h, id.ID)
			_ = h.store.StoreBootstrapToken(ctx, id, []byte("escrow"), t0)
			cmd, _ := mdm.NewCommand(&commands.DeviceInformation{}, mdm.WithUUID("inventory"))
			_, _ = h.core.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{})
			install, _ := mdm.NewCommand(&commands.InstallProfile{Payload: []byte("p")}, mdm.WithUUID("update"))
			_, err := h.store.TransitionReplacement(ctx, id, storage.ReplacementChange{Op: "begin", At: t0, Begin: &storage.Replacement{ID: "update", Method: "acme", OldHash: cms.Fingerprint(h.cert), ExpiresAt: t0.Add(time.Minute), Command: *install}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = h.store.TransitionReplacement(ctx, id, storage.ReplacementChange{Op: "issue", ID: "update", Method: "acme", Hash: cms.Fingerprint(h.cert2), At: t0})
			if err != nil {
				t.Fatal(err)
			}
			// Candidate cannot obtain the credential-bearing command or use other check-ins.
			if cmd, err := h.core.Connect(ctx, req(h.cert2), response(id.ID, "", mdm.StatusIdle)); err != nil || cmd != nil {
				t.Fatal(cmd, err)
			}
			if _, err := h.core.Checkin(ctx, req(h.cert2), tokenUpdate(t, id.ID, nil)); err == nil {
				t.Fatal("TokenUpdate before Authenticate accepted")
			}
			if _, err := h.core.Checkin(ctx, req(h.cert2), checkinPlist(t, map[string]any{"MessageType": "CheckOut", "UDID": id.ID})); err == nil {
				t.Fatal("candidate checkout accepted")
			}
			got, err := h.core.Connect(ctx, req(h.cert), response(id.ID, "", mdm.StatusIdle))
			if err != nil || got == nil || got.UUID != "update" {
				t.Fatal(got, err)
			}
			if _, err := h.core.Checkin(ctx, req(h.cert), authenticate(t, id.ID)); err != nil {
				t.Fatal(err)
			}
			if _, err := h.core.Checkin(ctx, req(h.cert2), authenticate(t, id.ID)); err != nil {
				t.Fatal(err)
			}
			if _, err := h.core.Checkin(ctx, req(h.cert2), tokenUpdate(t, id.ID, map[string]any{"PushMagic": "candidate"})); err != nil {
				t.Fatal(err)
			}
			old, _ := h.store.Get(ctx, id)
			if old.CertHash != cms.Fingerprint(h.cert) || !old.Enabled {
				t.Fatal("premature switch")
			}
			switch outcome {
			case "commit":
				_, err = h.core.Connect(ctx, req(h.cert2), response(id.ID, "update", mdm.StatusAcknowledged))
			case "rollback":
				_, err = h.core.Connect(ctx, req(h.cert), response(id.ID, "update", mdm.StatusError))
			case "expired":
				h.clock.Advance(time.Minute)
			case "cancelled":
				_, err = h.store.TransitionReplacement(ctx, id, storage.ReplacementChange{Op: "cancel", ID: "update", At: t0})
			}
			if err != nil {
				t.Fatal(err)
			}
			cert := h.cert
			if outcome == "commit" {
				cert = h.cert2
				if _, err := h.core.Checkin(ctx, req(cert), authenticate(t, id.ID)); err != nil {
					t.Fatal("duplicate Authenticate", err)
				}
			}
			got, err = h.core.Connect(ctx, req(cert), response(id.ID, "", mdm.StatusIdle))
			if err != nil || got == nil || got.UUID != "inventory" {
				t.Fatal("queue lost", got, err)
			}
			escrow, err := h.store.BootstrapToken(ctx, id)
			if err != nil || string(escrow) != "escrow" {
				t.Fatal("escrow lost", err)
			}
			if outcome != "commit" {
				if _, err := h.core.Checkin(ctx, req(h.cert2), authenticate(t, id.ID)); !errors.Is(err, service.ErrCertMismatch) {
					t.Fatal("discarded identity accepted", err)
				}
			}
		})
	}
}
