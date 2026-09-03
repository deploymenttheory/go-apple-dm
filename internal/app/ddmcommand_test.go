package app_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/audit"
	auditinmem "github.com/deploymenttheory/go-apple-mdm/audit/inmem"
	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/app"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

var t0app = time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)

// enrolled writes an enabled device enrollment so the notifier has something
// commandable to target.
func enrolled(t *testing.T, a *app.App, udid string) mdm.EnrollmentID {
	t.Helper()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: udid}
	err := a.Core.ImportEnrollment(context.Background(), storage.EnrollmentExport{
		Enrollment: storage.Enrollment{
			ID: id, Enabled: true,
			Push: mdm.Push{Topic: "com.apple.mgmt.External.test", Token: []byte("tok"), Magic: "magic"},
		},
	})
	if err != nil {
		t.Fatalf("ImportEnrollment: %v", err)
	}
	return id
}

// declare publishes a declaration and assigns it, which records the change
// rows the notifier drains.
func declare(t *testing.T, a *app.App, id mdm.EnrollmentID) {
	t.Helper()
	ctx := context.Background()
	body := []byte(`{"Type":"com.apple.configuration.management.test","Identifier":"com.example.cfg","Payload":{"Echo":"hi"}}`)
	if _, _, err := a.Engine.PutDeclaration(ctx, body); err != nil {
		t.Fatalf("PutDeclaration: %v", err)
	}
	if _, err := a.Engine.AssignDeclaration(ctx, id, "com.example.cfg"); err != nil {
		t.Fatalf("AssignDeclaration: %v", err)
	}
}

// DeclarativeManagement is an MDM command, so it must travel the MDM command
// path. The notifier used to enqueue straight into storage, which skipped
// Core.Enqueue's hook chain, skipped the schema/support target screening, and
// never published CommandQueued -- so every DDM-driven command was missing
// from the event bus, and therefore from the audit trail.
func TestDDMCommandTravelsTheCommandPath(t *testing.T) {
	t.Run("PublishesCommandQueued", func(t *testing.T) {
		bus := event.New()
		rec := &recorder{}
		bus.Subscribe(event.All, rec.handle)
		fake := clock.NewFake(t0app)
		a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem", AdminToken: "t", Bus: bus, Clock: fake})

		id := enrolled(t, a, "UDID-DDM-1")
		declare(t, a, id)
		// The notifier defers an enrollment whose newest change is younger
		// than its coalescing window, so a burst of edits becomes one
		// command. Move past it before draining.
		fake.Advance(time.Minute)
		if _, err := a.Notifier.DrainOnce(context.Background()); err != nil {
			t.Fatalf("DrainOnce: %v", err)
		}

		queued := rec.ofType(event.CommandQueued)
		if len(queued) == 0 {
			t.Fatal("a DDM wake published no CommandQueued event")
		}
		if got := queued[0].Enrollment.ID; got != "UDID-DDM-1" {
			t.Fatalf("enrollment = %q", got)
		}
	})

	t.Run("LandsInTheAuditTrail", func(t *testing.T) {
		st := auditinmem.New()
		fake := clock.NewFake(t0app)
		a := build(t, app.Config{
			Role: app.RoleAll, Storage: "inmem", AdminToken: "t", Clock: fake,
			Sinks: app.SinkConfig{AuditStore: st},
		})
		id := enrolled(t, a, "UDID-DDM-2")
		declare(t, a, id)
		fake.Advance(time.Minute)
		if _, err := a.Notifier.DrainOnce(context.Background()); err != nil {
			t.Fatalf("DrainOnce: %v", err)
		}
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		res, err := st.List(context.Background(), audit.Query{Type: string(event.CommandQueued)}, audit.Page{})
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Items) == 0 {
			t.Fatal("the DDM wake command is absent from the audit trail")
		}
		if got := res.Items[0].Fields["request_type"]; got != "DeclarativeManagement" {
			t.Fatalf("request_type = %v", got)
		}
	})

}

// The engine no longer calls back into the notifier, so the admin route
// wrapper is what shortens the wait after a declarative change. It lives in
// one place so a route added later cannot forget it.
func TestAdminWriteKicksTheNotifier(t *testing.T) {
	// With a fake clock the notifier's poll never fires, so the only thing
	// that can start another drain is a kick. Each loop iteration parks on
	// Clock.After, so the count of pending waiters rising is the evidence
	// that an extra iteration ran without time moving.
	fake := clock.NewFake(t0app)
	a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem", AdminToken: "t", Clock: fake})
	srv := serve(t, a)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, func() bool { return fake.Pending() >= 1 })
	before := fake.Pending()

	body := `{"Type":"com.apple.configuration.management.test","Identifier":"com.example.cfg","Payload":{"Echo":"hi"}}`
	resp := adminReq(t, srv.URL, http.MethodPut, "/admin/v1/declarations", "t", body)
	_ = resp.Body.Close()
	if resp.StatusCode >= 300 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	waitFor(t, func() bool { return fake.Pending() > before })
}

// A read must not kick, so the notifier is not woken by every dashboard poll.
func TestAdminReadDoesNotKick(t *testing.T) {
	fake := clock.NewFake(t0app)
	a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem", AdminToken: "t", Clock: fake})
	srv := serve(t, a)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, func() bool { return fake.Pending() >= 1 })
	before := fake.Pending()

	resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/config", "t", "")
	_ = resp.Body.Close()
	// Give a wrongly-placed kick time to land.
	time.Sleep(50 * time.Millisecond)
	if fake.Pending() != before {
		t.Fatal("a read woke the notifier")
	}
}

// Suppression applies only where a caller asks for it. An operator sending
// the same command twice in quick succession gets two commands, because
// nothing on the admin path sets a dedupe key -- a rerun is a legitimate
// thing to want, and the library must not decide otherwise.
func TestOperatorCommandsAreNeverSuppressed(t *testing.T) {
	a, srv, _ := mdmAdminApp(t)
	id := seed(t, a, "UDID-RERUN")

	cmd := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Command</key><dict><key>RequestType</key><string>DeviceInformation</string></dict>
<key>CommandUUID</key><string>%s</string>
</dict></plist>`

	for _, uuid := range []string{"RERUN-1", "RERUN-2"} {
		resp := adminReq(t, srv, http.MethodPost,
			"/admin/v1/enrollments/device/UDID-RERUN/commands", "t", fmt.Sprintf(cmd, uuid))
		body := jsonBody(t, resp)
		_ = resp.Body.Close()
		if got := body["Queued"]; got != float64(1) {
			t.Fatalf("%s: Queued = %v, want 1", uuid, got)
		}
		if _, suppressed := body["Skipped"]; suppressed {
			t.Fatalf("%s was suppressed: %v", uuid, body)
		}
	}

	res, err := a.Store.Commands(context.Background(), id, storage.CommandQuery{}, storage.Page{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("queue holds %d commands, want both reruns", len(res.Items))
	}
}
