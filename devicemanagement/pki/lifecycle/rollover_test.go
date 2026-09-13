package lifecycle_test

import (
	"crypto/x509/pkix"
	"errors"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

func TestRolloverRetainsOfflineDevicesAndRejectsStaleWorkers(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	store := state.NewMemory()
	store.Now = func() time.Time { return now }
	m := &lifecycle.Manager{Store: store}
	req := lifecycle.Request{ID: "issuer", Kind: lifecycle.Issuer, Subject: pkix.Name{CommonName: "Enrollment CA"}}
	item, err := m.Begin(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.CreateIssuer(ctx, req.ID, item.Pending, 0); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Activate(ctx, req.ID, item.Pending); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	item, err = m.Begin(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.CreateIssuer(ctx, req.ID, item.Pending, 0); err != nil {
		t.Fatal(err)
	}
	job, err := m.PrepareRollover(ctx, req.ID, item.Pending, []string{"online", "offline"})
	if err != nil {
		t.Fatal(err)
	}
	if job.Phase != "prepared" {
		t.Fatal(job)
	}
	active, _ := m.Get(ctx, req.ID)
	if active.Active != "1" {
		t.Fatal("preparation switched issuer")
	}
	if _, err = m.ActivateRollover(ctx, req.ID, "2"); err != nil {
		t.Fatal(err)
	}
	rows, _, err := m.Migrations(ctx, req.ID, "2", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rows {
		if v.Device == "online" {
			v.Phase = "confirmed"
			if err = m.SaveMigration(ctx, req.ID, "2", v); err != nil {
				t.Fatal(err)
			}
			if err = m.SaveMigration(ctx, req.ID, "2", v); !errors.Is(err, lifecycle.ErrConflict) {
				t.Fatal("stale worker accepted", err)
			}
		}
	}
	if err = m.CompleteRollover(ctx, req.ID, "2"); !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatal("offline device silently excluded", err)
	}
	if _, err = m.RetireIssuer(ctx, req.ID, "1"); !errors.Is(err, lifecycle.ErrConflict) {
		t.Fatal("old issuer retired early", err)
	}
	m = &lifecycle.Manager{Store: store}
	rows, _, err = m.Migrations(ctx, req.ID, "2", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rows {
		if v.Device == "offline" {
			v.Phase = "confirmed"
			if err = m.SaveMigration(ctx, req.ID, "2", v); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err = m.CompleteRollover(ctx, req.ID, "2"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.RetireIssuer(ctx, req.ID, "1"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.LoadMaterial(ctx, req.ID, "1"); err != nil {
		t.Fatal("retirement discarded issuer status signing key", err)
	}
}
