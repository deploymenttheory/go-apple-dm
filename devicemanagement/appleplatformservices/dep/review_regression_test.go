package dep_test

import (
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
)

func TestRegressionExpiredCursorRetainsRemovedDevice(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	s := newSyncer(t, f)
	f.srv.AddDevices(dep.Device{SerialNumber: "REVIEW-REMOVED"}, dep.Device{SerialNumber: "REVIEW-KEPT"})
	if _, err := s.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if !f.srv.DeleteDevice("REVIEW-REMOVED") {
		t.Fatal("missing fixture device")
	}
	cur, err := f.store.Cursor(ctx, acct)
	if err != nil {
		t.Fatal(err)
	}
	cur.UpdatedAt = f.clk.Now().Add(-8 * 24 * time.Hour)
	if err := f.store.SetCursor(ctx, acct, cur); err != nil {
		t.Fatal(err)
	}
	res, err := s.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.store.GetDevice(ctx, acct, "REVIEW-REMOVED")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("refetched=%v, removed device still live=%v", res.Restarted, !got.Deleted)
	if !got.Deleted {
		t.Fatal("full refetch did not reconcile a removed device")
	}
}
