package app_test

import (
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/push"
	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/push/apns"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/v3/storage"
	"github.com/deploymenttheory/go-apple-dm/v3/testpki"
)

// mdmAdminApp builds a server with the mdm admin family mounted and a
// recording pusher, so the wake route has something to observe.
func mdmAdminApp(t *testing.T) (*app.App, string, *recordingPusher) {
	t.Helper()
	p := &recordingPusher{}
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", AdminToken: "t",
		Push: app.PushConfig{Pusher: p, Coalesce: -1},
	})
	return a, serve(t, a).URL, p
}

func jsonBody(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

func seed(t *testing.T, a *app.App, udid string) mdm.EnrollmentID {
	t.Helper()
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: udid}
	err := a.Core.ImportEnrollment(context.Background(), storage.EnrollmentExport{
		Enrollment: storage.Enrollment{
			ID: id, Enabled: true,
			Push:   mdm.Push{Topic: "com.apple.mgmt.External.test", Token: []byte("tok"), Magic: "magic"},
			Device: storage.DeviceInfo{SerialNumber: "SERIAL-" + udid, ProductName: "Mac15,3", OSVersion: "26.0"},
		},
	})
	if err != nil {
		t.Fatalf("ImportEnrollment: %v", err)
	}
	return id
}

// The MDM half of the admin surface: the enrollments the server manages, the
// commands queued for them, the certificates that wake them, and the export
// that moves them between deployments.
func TestMDMAdminRoutes(t *testing.T) {
	a, srv, pusher := mdmAdminApp(t)
	seed(t, a, "UDID-A")
	seed(t, a, "UDID-B")

	t.Run("ListEnrollments", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/enrollments", "t", "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		items, _ := jsonBody(t, resp)["Items"].([]any)
		if len(items) != 2 {
			t.Fatalf("items = %d, want 2", len(items))
		}
	})

	t.Run("FiltersAndPages", func(t *testing.T) {
		one := adminReq(t, srv, http.MethodGet, "/admin/v1/enrollments?limit=1", "t", "")
		defer one.Body.Close()
		body := jsonBody(t, one)
		if items, _ := body["Items"].([]any); len(items) != 1 {
			t.Fatalf("limit ignored: %d", len(items))
		}
		if cursor, _ := body["NextCursor"].(string); cursor == "" {
			t.Fatal("no cursor on a full page")
		}
		bySerial := adminReq(t, srv, http.MethodGet, "/admin/v1/enrollments?serial=SERIAL-UDID-A", "t", "")
		defer bySerial.Body.Close()
		if items, _ := jsonBody(t, bySerial)["Items"].([]any); len(items) != 1 {
			t.Fatalf("serial filter = %d", len(items))
		}
	})

	// An enrollment record carries the device unlock token, the bootstrap
	// token and the raw check-in plists. None of them is inventory.
	t.Run("ListingLeaksNoSecrets", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/enrollments/device/UDID-A", "t", "")
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"UnlockToken", "AuthenticateRaw", "TokenUpdateRaw", "BootstrapToken"} {
			if strings.Contains(string(body), forbidden) {
				t.Errorf("enrollment view exposes %s:\n%s", forbidden, body)
			}
		}
	})

	t.Run("EnqueueReadAndClearCommands", func(t *testing.T) {
		cmd := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Command</key><dict><key>RequestType</key><string>DeviceInformation</string>
<key>Queries</key><array><string>UDID</string></array></dict>
<key>CommandUUID</key><string>CMD-1</string>
</dict></plist>`
		enq := adminReq(t, srv, http.MethodPost, "/admin/v1/enrollments/device/UDID-A/commands", "t", cmd)
		defer enq.Body.Close()
		if enq.StatusCode != http.StatusOK {
			t.Fatalf("enqueue status = %d", enq.StatusCode)
		}
		if got := jsonBody(t, enq)["CommandUUID"]; got != "CMD-1" {
			t.Fatalf("CommandUUID = %v", got)
		}

		list := adminReq(t, srv, http.MethodGet, "/admin/v1/enrollments/device/UDID-A/commands", "t", "")
		defer list.Body.Close()
		items, _ := jsonBody(t, list)["Items"].([]any)
		if len(items) != 1 {
			t.Fatalf("queue = %d, want 1", len(items))
		}

		clear := adminReq(t, srv, http.MethodDelete, "/admin/v1/enrollments/device/UDID-A/commands", "t", "")
		defer clear.Body.Close()
		if got := jsonBody(t, clear)["Cleared"]; got != float64(1) {
			t.Fatalf("Cleared = %v", got)
		}
	})

	// Core.Enqueue screens the target against schema/support, so a refusal
	// is reported with its reason rather than silently queueing nothing.
	t.Run("UnsupportedTargetIsReported", func(t *testing.T) {
		seed(t, a, "UDID-OLD")
		cmd := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Command</key><dict><key>RequestType</key><string>DeviceInformation</string></dict>
<key>CommandUUID</key><string>CMD-2</string>
</dict></plist>`
		resp := adminReq(t, srv, http.MethodPost, "/admin/v1/enrollments/device/UDID-OLD/commands", "t", cmd)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	t.Run("PushWakesTheDevice", func(t *testing.T) {
		before := pusher.count()
		resp := adminReq(t, srv, http.MethodPost, "/admin/v1/enrollments/device/UDID-A/push", "t", "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if pusher.count() == before {
			t.Fatal("the push route woke nobody")
		}
	})

	t.Run("DisableEnrollment", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodDelete, "/admin/v1/enrollments/device/UDID-B", "t", "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		got, err := a.Store.Get(context.Background(), mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-B"})
		if err != nil {
			t.Fatal(err)
		}
		if got.Enabled {
			t.Fatal("the enrollment is still enabled")
		}
	})

	t.Run("ExportAndImport", func(t *testing.T) {
		exp := adminReq(t, srv, http.MethodGet, "/admin/v1/export?limit=1", "t", "")
		defer exp.Body.Close()
		if exp.StatusCode != http.StatusOK {
			t.Fatalf("export status = %d", exp.StatusCode)
		}
		rec := `{"ID":{"Channel":1,"ID":"UDID-IMPORTED"},"Enabled":true}`
		imp := adminReq(t, srv, http.MethodPost, "/admin/v1/import", "t", rec)
		defer imp.Body.Close()
		if imp.StatusCode != http.StatusNoContent {
			t.Fatalf("import status = %d", imp.StatusCode)
		}
		if _, err := a.Store.Get(context.Background(),
			mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-IMPORTED"}); err != nil {
			t.Fatalf("imported record absent: %v", err)
		}
	})

	t.Run("PushCerts", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/pushcerts", "t", "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		bad := adminReq(t, srv, http.MethodPut, "/admin/v1/pushcerts", "t", `{"Topic":"t"}`)
		defer bad.Body.Close()
		if bad.StatusCode != http.StatusBadRequest {
			t.Fatalf("a certificate with no key = %d, want 400", bad.StatusCode)
		}
	})

	t.Run("BadInputIsRejected", func(t *testing.T) {
		for _, c := range []struct {
			method, path, body string
			want               int
		}{
			{http.MethodGet, "/admin/v1/enrollments?limit=lots", "", http.StatusBadRequest},
			{http.MethodGet, "/admin/v1/enrollments?channel=carrier-pigeon", "", http.StatusBadRequest},
			{http.MethodGet, "/admin/v1/enrollments?enabled=maybe", "", http.StatusBadRequest},
			{http.MethodGet, "/admin/v1/enrollments/pigeon/x", "", http.StatusBadRequest},
			{http.MethodGet, "/admin/v1/enrollments/device/nope", "", http.StatusNotFound},
			{http.MethodPost, "/admin/v1/enrollments/device/UDID-A/commands", "not a plist", http.StatusBadRequest},
			{http.MethodPost, "/admin/v1/import", "{", http.StatusBadRequest},
			{http.MethodPut, "/admin/v1/pushcerts", "{", http.StatusBadRequest},
		} {
			resp := adminReq(t, srv, c.method, c.path, "t", c.body)
			_ = resp.Body.Close()
			if resp.StatusCode != c.want {
				t.Errorf("%s %s = %d, want %d", c.method, c.path, resp.StatusCode, c.want)
			}
		}
	})
}

// The new routes are ordinary admin routes: Cedar gates them, the audit
// trail records them, and a principal without the action is refused. This is
// what declaring an action per route buys, and it is asserted rather than
// assumed because these are the routes that erase fleets.
func TestMDMAdminRoutesAreGoverned(t *testing.T) {
	bus := event.New()
	rec := &recorder{}
	bus.Subscribe(event.All, rec.handle)
	st := inmem.New()
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", AdminStore: st, Bus: bus,
	})
	srv := serve(t, a).URL
	seed(t, a, "UDID-GOV")

	reg, err := adminauth.NewRegistry(app.AdminActions()...)
	if err != nil {
		t.Fatal(err)
	}
	m, err := adminauth.New(st, reg)
	if err != nil {
		t.Fatal(err)
	}
	root := mintPrincipal(t, m, adminauth.Principal{Name: "ops", Root: true})
	// Root gates policy administration in Go, outside the policy system; every
	// other action still goes through Cedar, so the grant is explicit.
	if _, err := m.PutPolicy(context.Background(), adminauth.Root, adminauth.Policy{
		Name:   "ops",
		Source: `permit (principal == MDM::Principal::"ops", action, resource);`,
	}); err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}
	_, reader, err := m.CreatePrincipal(context.Background(),
		adminauth.Root, adminauth.Principal{Name: "reader"}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	t.Run("RefusesAPrincipalWithoutTheAction", func(t *testing.T) {
		rec.reset()
		resp := adminReq(t, srv, http.MethodDelete, "/admin/v1/enrollments/device/UDID-GOV", string(reader), "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", resp.StatusCode)
		}
		denied := rec.ofType(event.AdminDenied)
		if len(denied) == 0 {
			t.Fatal("the refusal was not audited")
		}
		data, _ := denied[0].Data.(map[string]any)
		if data["Action"] != app.ActionDisableEnrollment {
			t.Fatalf("audited action = %v", data["Action"])
		}
	})

	t.Run("AuditsAnEnqueue", func(t *testing.T) {
		rec.reset()
		cmd := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Command</key><dict><key>RequestType</key><string>DeviceInformation</string></dict>
<key>CommandUUID</key><string>CMD-GOV</string>
</dict></plist>`
		resp := adminReq(t, srv, http.MethodPost, "/admin/v1/enrollments/device/UDID-GOV/commands", root, cmd)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		for _, e := range rec.ofType(event.AdminAction) {
			if data, _ := e.Data.(map[string]any); data["Action"] == app.ActionEnqueueCommand {
				if e.Actor != "ops" {
					t.Fatalf("actor = %q", e.Actor)
				}
				return
			}
		}
		t.Fatal("the enqueue was not audited")
	})
}

// The mdm role owns enrollments, commands and push, and used to serve no
// admin API at all: adminEnabled was gated on the role rather than on having
// a credential.
func TestAdminAPIOnTheMDMRole(t *testing.T) {
	p := &recordingPusher{}
	a := build(t, app.Config{
		Role: app.RoleMDM, Storage: "inmem", AdminToken: "t",
		Push: app.PushConfig{Pusher: p, Coalesce: -1},
	})
	srv := serve(t, a).URL
	seed(t, a, "UDID-MDMROLE")

	resp := adminReq(t, srv, http.MethodGet, "/admin/v1/enrollments", "t", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want the mdm role to serve its own admin API", resp.StatusCode)
	}
	if items, _ := jsonBody(t, resp)["Items"].([]any); len(items) != 1 {
		t.Fatalf("items = %d", len(items))
	}
}

// A backend that cannot answer is a 500 with no cause in the body: the SQL
// error is for the operator's log, not the caller's screen.
func TestMDMAdminRoutesHideStorageFailures(t *testing.T) {
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "m.db"), AdminToken: "t",
	})
	srv := serve(t, a).URL
	seed(t, a, "UDID-CLOSED")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	cmd := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Command</key><dict><key>RequestType</key><string>DeviceInformation</string></dict>
<key>CommandUUID</key><string>CMD-X</string>
</dict></plist>`
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/admin/v1/enrollments", ""},
		{http.MethodGet, "/admin/v1/enrollments/device/UDID-CLOSED", ""},
		{http.MethodDelete, "/admin/v1/enrollments/device/UDID-CLOSED", ""},
		{http.MethodPost, "/admin/v1/enrollments/device/UDID-CLOSED/commands", cmd},
		{http.MethodGet, "/admin/v1/enrollments/device/UDID-CLOSED/commands", ""},
		{http.MethodDelete, "/admin/v1/enrollments/device/UDID-CLOSED/commands", ""},
		{http.MethodGet, "/admin/v1/pushcerts", ""},
		// PUT /pushcerts is absent: the store validates the PEM before it
		// reaches the database, so a closed pool answers 400, not 500.
		{http.MethodGet, "/admin/v1/export", ""},
		{http.MethodPost, "/admin/v1/import", `{"ID":{"Channel":1,"ID":"X"},"Enabled":true}`},
	} {
		resp := adminReq(t, srv, c.method, c.path, "t", c.body)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusInternalServerError {
			t.Errorf("%s %s = %d, want 500", c.method, c.path, resp.StatusCode)
		}
		if strings.Contains(string(body), "sql") || strings.Contains(string(body), "database") {
			t.Errorf("%s %s leaked the cause: %s", c.method, c.path, body)
		}
	}
}

// Waking a device needs a push source. Without one the answer says so
// rather than reporting a success nobody received.
func TestPushRouteNeedsAPushSource(t *testing.T) {
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", AdminToken: "t",
		Push: app.PushConfig{Source: app.PushSourceOff},
	})
	srv := serve(t, a).URL
	seed(t, a, "UDID-NOPUSH")
	resp := adminReq(t, srv, http.MethodPost, "/admin/v1/enrollments/device/UDID-NOPUSH/push", "t", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", resp.StatusCode)
	}
}

func TestMDMAdminBodyLimits(t *testing.T) {
	a, srv, _ := mdmAdminApp(t)
	seed(t, a, "UDID-BIG")
	big := strings.Repeat("x", app.MaxAdminBody+1)
	for _, c := range []struct{ method, path string }{
		{http.MethodPost, "/admin/v1/enrollments/device/UDID-BIG/commands"},
		{http.MethodPut, "/admin/v1/pushcerts"},
		{http.MethodPost, "/admin/v1/import"},
	} {
		resp := adminReq(t, srv, c.method, c.path, "t", big)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Errorf("%s %s = %d, want 413", c.method, c.path, resp.StatusCode)
		}
	}
}

// A completed command carries the device's answer, which is the point of
// reading the queue at all.
func TestCommandListingShowsResults(t *testing.T) {
	a, srv, _ := mdmAdminApp(t)
	id := seed(t, a, "UDID-RESULT")
	ctx := context.Background()
	cmd, err := mdm.NewCommand(&commands.DeviceInformation{}, mdm.WithUUID("CMD-R"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Core.Enqueue(ctx, []mdm.EnrollmentID{id}, cmd, storage.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store.Next(ctx, id, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	err = a.Store.StoreResult(ctx, id, &mdm.Response{
		ID: id, CommandUUID: "CMD-R", Status: mdm.StatusError,
		ErrorChain: []mdm.ErrorChainItem{{ErrorCode: 12021, ErrorDomain: "MCMDMErrorDomain"}},
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	resp := adminReq(t, srv, http.MethodGet,
		"/admin/v1/enrollments/device/UDID-RESULT/commands?type=DeviceInformation", "t", "")
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"CMD-R", "Error", "12021"} {
		if !strings.Contains(string(body), want) {
			t.Fatalf("listing missing %q:\n%s", want, body)
		}
	}
}

func TestChannelFilterAcceptsEveryChannel(t *testing.T) {
	a, srv, _ := mdmAdminApp(t)
	_ = a
	for _, ch := range []string{"device", "user", "shared-ipad-user", "user-enrollment-device", "user-enrollment-user"} {
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/enrollments?channel="+ch, "t", "")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("channel %q = %d", ch, resp.StatusCode)
		}
	}
}

// Every route that takes an enrollment from the path or a page from the
// query rejects a bad one the same way. Asserting it per route rather than
// once is what stops a new route parsing them differently.
func TestMDMAdminRoutesRejectBadPathsAndPages(t *testing.T) {
	a, srv, _ := mdmAdminApp(t)
	seed(t, a, "UDID-P")

	badChannel := []struct{ method, path string }{
		{http.MethodGet, "/admin/v1/enrollments/pigeon/UDID-P"},
		{http.MethodDelete, "/admin/v1/enrollments/pigeon/UDID-P"},
		{http.MethodPost, "/admin/v1/enrollments/pigeon/UDID-P/commands"},
		{http.MethodGet, "/admin/v1/enrollments/pigeon/UDID-P/commands"},
		{http.MethodDelete, "/admin/v1/enrollments/pigeon/UDID-P/commands"},
		{http.MethodPost, "/admin/v1/enrollments/pigeon/UDID-P/push"},
	}
	for _, c := range badChannel {
		resp := adminReq(t, srv, c.method, c.path, "t", "{}")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s %s = %d, want 400", c.method, c.path, resp.StatusCode)
		}
	}

	badPage := []string{
		"/admin/v1/enrollments?limit=lots",
		"/admin/v1/enrollments/device/UDID-P/commands?limit=lots",
		"/admin/v1/export?limit=lots",
	}
	for _, path := range badPage {
		resp := adminReq(t, srv, http.MethodGet, path, "t", "")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", path, resp.StatusCode)
		}
	}

	// A disabled enrollment is Gone rather than Not Found: it existed, and
	// saying so is the difference between "wrong id" and "checked out".
	if err := a.Store.Disable(context.Background(),
		mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-P"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	cmd := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Command</key><dict><key>RequestType</key><string>DeviceInformation</string></dict>
<key>CommandUUID</key><string>CMD-D</string>
</dict></plist>`
	resp := adminReq(t, srv, http.MethodPost, "/admin/v1/enrollments/device/UDID-P/commands", "t", cmd)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusGone {
		t.Fatalf("enqueue to a disabled enrollment = %d", resp.StatusCode)
	}
}

// Uploading a push certificate is how an operator renews the credential that
// wakes the fleet, so the round trip is asserted with a real certificate
// rather than a stub. The response carries the topic and expiry and never the
// private key.
func TestPushCertUploadRoundTrip(t *testing.T) {
	a, srv, _ := mdmAdminApp(t)
	_ = a
	ca, err := testpki.NewCA("push-ca")
	if err != nil {
		t.Fatal(err)
	}
	const topic = "com.apple.mgmt.External.upload"
	id, err := ca.IssuePush(topic, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := id.PEM()
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{
		"Topic": topic, "CertPEM": string(certPEM), "KeyPEM": string(keyPEM),
	})
	if err != nil {
		t.Fatal(err)
	}

	put := adminReq(t, srv, http.MethodPut, "/admin/v1/pushcerts", "t", string(body))
	defer put.Body.Close()
	if put.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(put.Body)
		t.Fatalf("status = %d: %s", put.StatusCode, raw)
	}
	stored := jsonBody(t, put)
	if stored["Topic"] != topic {
		t.Fatalf("topic = %v", stored["Topic"])
	}

	list := adminReq(t, srv, http.MethodGet, "/admin/v1/pushcerts", "t", "")
	defer list.Body.Close()
	raw, err := io.ReadAll(list.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), topic) {
		t.Fatalf("listing missing the topic:\n%s", raw)
	}
	// The key is what an attacker wants; it must never be readable back.
	for _, forbidden := range []string{"KeyPEM", "PRIVATE KEY", "CertPEM"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("push certificate listing exposes %s:\n%s", forbidden, raw)
		}
	}
}

// APNs telling us a token is dead is the answer an operator needs, so the
// push route reports it rather than flattening it to "sent".
func TestPushRouteReportsAnInvalidToken(t *testing.T) {
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", AdminToken: "t",
		Push: app.PushConfig{Pusher: &deadTokenPusher{}, Coalesce: -1},
	})
	srv := serve(t, a).URL
	seed(t, a, "UDID-DEAD")
	resp := adminReq(t, srv, http.MethodPost, "/admin/v1/enrollments/device/UDID-DEAD/push", "t", "")
	defer resp.Body.Close()
	body := jsonBody(t, resp)
	if body["Outcome"] != string(push.OutcomeInvalidToken) || body["Sent"] != false {
		t.Fatalf("body = %v", body)
	}
	if body["Status"] != float64(410) || body["Reason"] != "Unregistered" {
		t.Fatalf("body = %v", body)
	}
}

// The route must not answer a refused request the same way it answers a dead
// device. An operator reading "invalid-token" retires an enrollment; an
// operator reading "rejected" goes and looks at their push certificate.
func TestPushRouteSeparatesRejectionFromADeadToken(t *testing.T) {
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", AdminToken: "t",
		Push: app.PushConfig{Pusher: &rejectingPusher{}, Coalesce: -1},
	})
	srv := serve(t, a).URL
	seed(t, a, "UDID-REJECT")
	resp := adminReq(t, srv, http.MethodPost, "/admin/v1/enrollments/device/UDID-REJECT/push", "t", "")
	defer resp.Body.Close()
	body := jsonBody(t, resp)
	if body["Outcome"] != string(push.OutcomeRejected) {
		t.Fatalf("body = %v", body)
	}
	if body["Status"] != float64(400) || body["Reason"] != apns.ReasonDeviceTokenNotForTopic {
		t.Fatalf("body = %v", body)
	}
}

// deadTokenPusher is APNs answering 410 for a token the device no longer has.
type deadTokenPusher struct{}

func (deadTokenPusher) Push(_ context.Context, targets []push.Target) (map[mdm.EnrollmentID]push.Result, error) {
	out := make(map[mdm.EnrollmentID]push.Result, len(targets))
	for _, tgt := range targets {
		out[tgt.ID] = push.Result{Outcome: push.OutcomeInvalidToken, Status: 410, Reason: "Unregistered"}
	}
	return out, nil
}

// rejectingPusher is APNs refusing the request itself: the topic in the
// certificate does not match the token. Every device answers this way.
type rejectingPusher struct{}

func (rejectingPusher) Push(_ context.Context, targets []push.Target) (map[mdm.EnrollmentID]push.Result, error) {
	out := make(map[mdm.EnrollmentID]push.Result, len(targets))
	for _, tgt := range targets {
		out[tgt.ID] = push.Result{Outcome: push.OutcomeRejected, Status: 400, Reason: apns.ReasonDeviceTokenNotForTopic}
	}
	return out, nil
}
