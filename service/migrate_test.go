package service_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/service"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// TestImportExportPublishes proves decision record 0017 claim 5: the Core
// methods run the hook chain, map storage errors, and publish
// EnrollmentImported.
func TestImportExportPublishes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	src := newHarness(t, service.Config{})
	enroll(t, src, "D1")
	if _, err := src.core.Checkin(ctx, req(src.cert), simple(t, "SetBootstrapToken", "D1", map[string]any{"BootstrapToken": []byte("bst")})); err != nil {
		t.Fatal(err)
	}
	if _, err := src.core.Checkin(ctx, req(src.cert), tokenUpdate(t, "D1", map[string]any{"UserID": "U1", "UserShortName": "alice"})); err != nil {
		t.Fatal(err)
	}
	exported, err := src.core.ExportEnrollments(ctx, storage.Page{})
	if err != nil || len(exported.Items) != 2 || exported.NextCursor != "" {
		t.Fatalf("export: %+v %v", exported, err)
	}
	dev := exported.Items[0]
	if dev.ID != deviceID("D1") || string(dev.BootstrapToken) != "bst" || len(dev.CertHistory) != 1 || dev.CertHistory[0].Hash != dev.CertHash || !dev.Enabled {
		t.Fatalf("device export = %+v", dev)
	}
	if exported.Items[1].ID.ParentID != "D1" || exported.Items[1].UserShortName != "alice" {
		t.Fatalf("user export = %+v", exported.Items[1])
	}
	// Import into a second Core; a hook that vetoes export proves the
	// chain runs on both methods.
	hook := &recordingHook{veto: "export"}
	dst := newHarness(t, service.Config{Hooks: []service.Hook{hook}})
	for _, rec := range exported.Items {
		if err := dst.core.ImportEnrollment(ctx, rec); err != nil {
			t.Fatalf("import %s: %v", rec.ID.ID, err)
		}
	}
	if got := dst.eventTypes(); strings.Join(got, ",") != "enrollment-imported,enrollment-imported" {
		t.Fatalf("events = %v", got)
	}
	if ev := dst.events[0]; ev.Actor != "admin" || ev.Enrollment != deviceID("D1") || ev.Data != deviceID("D1") {
		t.Fatalf("event = %+v", ev)
	}
	got, err := dst.store.Get(ctx, deviceID("D1"))
	want, _ := src.store.Get(ctx, deviceID("D1"))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("imported record\n got %+v\nwant %+v\nerr %v", got, want, err)
	}
	if tok, err := dst.store.BootstrapToken(ctx, deviceID("D1")); err != nil || string(tok) != "bst" {
		t.Fatalf("bootstrap token after import: %q %v", tok, err)
	}
	if hist, err := dst.store.CertHistory(ctx, deviceID("D1")); err != nil || !reflect.DeepEqual(hist, dev.CertHistory) {
		t.Fatalf("history after import: %+v %v", hist, err)
	}
	// The imported identity is live on the target.
	if _, err := dst.core.Connect(ctx, req(src.cert), response("D1", "", mdm.StatusIdle)); err != nil {
		t.Fatalf("connect after import: %v", err)
	}
	if _, err := dst.core.ExportEnrollments(ctx, storage.Page{}); !errors.Is(err, service.ErrHookVeto) || service.CodeOf(err) != service.CodeForbidden {
		t.Fatalf("export veto: %v", err)
	}
	if hook.before != hook.after+1 {
		t.Fatalf("hook calls before=%d after=%d", hook.before, hook.after)
	}
	// Storage errors are mapped: invalid record, bad cursor, and a
	// certificate pinned elsewhere.
	if err := dst.core.ImportEnrollment(ctx, storage.EnrollmentExport{}); service.CodeOf(err) != service.CodeBadRequest || !errors.Is(err, storage.ErrInvalid) {
		t.Fatalf("invalid record: %v", err)
	}
	if _, err := src.core.ExportEnrollments(ctx, storage.Page{Cursor: "garbage"}); service.CodeOf(err) != service.CodeBadRequest {
		t.Fatalf("bad cursor: %v", err)
	}
	clash := dev
	clash.ID = deviceID("D2")
	clash.CertHistory = nil
	if err := dst.core.ImportEnrollment(ctx, clash); service.CodeOf(err) != service.CodeForbidden || !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("pinned elsewhere: %v", err)
	}
	// Import veto.
	vetoed := newHarness(t, service.Config{Hooks: []service.Hook{&recordingHook{veto: "import"}}})
	if err := vetoed.core.ImportEnrollment(ctx, dev); !errors.Is(err, service.ErrHookVeto) {
		t.Fatalf("import veto: %v", err)
	}
	if len(vetoed.events) != 0 {
		t.Fatalf("events after veto = %v", vetoed.eventTypes())
	}
}
