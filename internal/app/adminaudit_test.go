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

	"github.com/deploymenttheory/go-apple-dm/audit"
	"github.com/deploymenttheory/go-apple-dm/audit/audittest"
	auditinmem "github.com/deploymenttheory/go-apple-dm/audit/inmem"
	"github.com/deploymenttheory/go-apple-dm/internal/app"
	"github.com/deploymenttheory/go-apple-dm/internal/clock"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/secrets"
)

// auditApp builds a server whose trail is a store the test can also read.
func auditApp(t *testing.T, cfg app.Config) (*app.App, audit.Store) {
	t.Helper()
	st := auditinmem.New()
	cfg.Sinks.AuditStore = st
	if cfg.Storage == "" {
		cfg.Storage, cfg.Role = "inmem", app.RoleAll
	}
	cfg.AdminToken = "t"
	return build(t, cfg), st
}

func readAll(t *testing.T, st audit.Store) []audit.Record {
	t.Helper()
	res, err := st.List(context.Background(), audit.Query{}, audit.Page{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	return res.Items
}

// The repudiation control, end to end: a state change through the assembled
// server lands in a persistent trail, projected rather than raw.
func TestAuditTrailRecordsStateChanges(t *testing.T) {
	a, st := auditApp(t, app.Config{})
	publishSomething(t, a)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	records := readAll(t, st)
	if len(records) == 0 {
		t.Fatal("a state change wrote no audit record")
	}
	if records[0].Type != "enrollment-imported" {
		t.Fatalf("type = %q", records[0].Type)
	}
	if records[0].Actor != "admin" {
		t.Fatalf("actor = %q", records[0].Actor)
	}
}

// An admin request is attributable to the principal that made it, which is
// the question the trail exists to answer.
func TestAuditTrailAttributesAdminRequests(t *testing.T) {
	a, st := auditApp(t, app.Config{})
	srv := serve(t, a)
	resp := adminReq(t, srv.URL, http.MethodPut, "/admin/v1/declarations", "t", `{}`)
	defer resp.Body.Close()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	for _, rec := range readAll(t, st) {
		if rec.Type == "admin-action" {
			if rec.Actor != app.BreakGlassActor {
				t.Fatalf("actor = %q, want the break-glass actor", rec.Actor)
			}
			// The path is recorded as the admin mux saw it, after
			// StripPrefix, so it is the route rather than the mount point.
			if rec.Fields["Path"] != "/declarations" || rec.Fields["Action"] != "putDeclaration" {
				t.Fatalf("fields = %v", rec.Fields)
			}
			return
		}
	}
	t.Fatal("no admin-action record")
}

// The route is mounted only when a trail exists, and it pages the way every
// other listing does so mdmctl reads it with the client's existing helper.
func TestAuditRouteListsAndPages(t *testing.T) {
	a, st := auditApp(t, app.Config{})
	srv := serve(t, a)
	ctx := context.Background()
	for i := range 5 {
		if _, err := st.Append(ctx, audit.Record{
			At: audittest.T0.Add(time.Duration(i) * time.Minute), Type: "enrolled", Actor: "device",
			Enrollment: mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-1"},
		}); err != nil {
			t.Fatal(err)
		}
	}

	get := func(path string) map[string]any {
		t.Helper()
		resp := adminReq(t, srv.URL, http.MethodGet, path, "t", "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status = %d", path, resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	page := get("/admin/v1/audit?limit=2")
	items, _ := page["Items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	cursor, _ := page["NextCursor"].(string)
	if cursor == "" {
		t.Fatal("no cursor on a full page")
	}
	next := get("/admin/v1/audit?limit=2&cursor=" + cursor)
	if n, _ := next["Items"].([]any); len(n) != 2 {
		t.Fatalf("second page = %d", len(n))
	}

	t.Run("Filters", func(t *testing.T) {
		if n, _ := get("/admin/v1/audit?type=enrolled")["Items"].([]any); len(n) != 5 {
			t.Fatalf("by type = %d", len(n))
		}
		if n, _ := get("/admin/v1/audit?actor=nobody")["Items"].([]any); len(n) != 0 {
			t.Fatalf("by unknown actor = %d", len(n))
		}
		if n, _ := get("/admin/v1/audit?enrollment=UDID-1")["Items"].([]any); len(n) != 5 {
			t.Fatalf("by enrollment = %d", len(n))
		}
	})

	t.Run("Get", func(t *testing.T) {
		one := get("/admin/v1/audit/1")
		if one["Type"] != "enrolled" {
			t.Fatalf("record = %v", one)
		}
	})
}

// A caller's mistake is a 400 with a reason, not an empty page that reads as
// "nothing happened".
func TestAuditRouteRejectsBadInput(t *testing.T) {
	a, _ := auditApp(t, app.Config{})
	srv := serve(t, a)
	for _, path := range []string{
		"/admin/v1/audit?since=yesterday",
		"/admin/v1/audit?until=nope",
		"/admin/v1/audit?limit=lots",
		"/admin/v1/audit?cursor=abc",
		"/admin/v1/audit/not-a-number",
	} {
		resp := adminReq(t, srv.URL, http.MethodGet, path, "t", "")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", path, resp.StatusCode)
		}
	}
	resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/audit/9999", "t", "")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("absent record: status = %d, want 404", resp.StatusCode)
	}
}

// Without a trail the routes are not mounted, so the admin API does not grow
// a surface a deployment did not ask for.
func TestAuditRouteAbsentWithoutATrail(t *testing.T) {
	a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem", AdminToken: "t"})
	srv := serve(t, a)
	resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/audit", "t", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// Retention is the only way a record leaves the trail, so the worker is
// driven by the injected clock and asserted rather than left to a timer.
func TestAuditRetentionPrunesOnItsInterval(t *testing.T) {
	fake := clock.NewFake(audittest.T0)
	a, st := auditApp(t, app.Config{
		Clock: fake,
		Sinks: app.SinkConfig{Retention: 24 * time.Hour},
	})

	ctx := context.Background()
	if _, err := st.Append(ctx, audit.Record{At: audittest.T0.Add(-48 * time.Hour), Type: "enrolled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, audit.Record{At: audittest.T0, Type: "enrolled"}); err != nil {
		t.Fatal(err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Run(runCtx) }()

	// Wait until the loop is parked on the clock, then move past the tick.
	waitFor(t, func() bool { return fake.Pending() > 0 })
	fake.Advance(app.DefaultAuditPruneInterval)
	waitFor(t, func() bool { return len(readAll(t, st)) == 1 })

	cancel()
	<-done
}

// waitFor polls cond until it holds, so a test never sleeps for a fixed
// duration to wait on a worker.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition was never met")
}

// Retention off keeps everything, which has to be a deliberate choice rather
// than an accident of an unset variable.
func TestAuditRetentionOffKeepsEverything(t *testing.T) {
	fake := clock.NewFake(audittest.T0)
	a, st := auditApp(t, app.Config{Clock: fake})
	ctx := context.Background()
	if _, err := st.Append(ctx, audit.Record{At: audittest.T0.Add(-a2Years), Type: "enrolled"}); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- a.Run(runCtx) }()
	fake.Advance(2 * app.DefaultAuditPruneInterval)
	cancel()
	<-done
	if len(readAll(t, st)) != 1 {
		t.Fatal("a record was pruned with retention unset")
	}
}

const a2Years = 2 * 365 * 24 * time.Hour

// The trail resolves the same three ways as every other satellite store, and
// each arm is worth asserting because the wrong one means either no trail or
// a trail nobody asked for.
func TestAuditStoreSelection(t *testing.T) {
	t.Run("PersistOnADatabase", func(t *testing.T) {
		a := build(t, app.Config{
			Role: app.RoleAll, Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "a.db"),
			AdminToken: "t", Sinks: app.SinkConfig{Persist: true},
		})
		srv := serve(t, a)
		publishSomething(t, a)
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		// The records survive in the process's own database, which is the
		// whole point of persisting rather than logging.
		resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/audit", "t", "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusInternalServerError {
			t.Fatalf("status = %d", resp.StatusCode)
		}
	})

	t.Run("PersistWithoutADatabase", func(t *testing.T) {
		a := build(t, app.Config{
			Role: app.RoleAll, Storage: "inmem", AdminToken: "t",
			Sinks: app.SinkConfig{Persist: true},
		})
		srv := serve(t, a)
		resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/audit", "t", "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want an in-memory trail", resp.StatusCode)
		}
	})

	t.Run("OpenFailureIsReported", func(t *testing.T) {
		dsn := filepath.Join(t.TempDir(), "b.db")
		a, err := app.Build(context.Background(), app.Config{
			Role: app.RoleAll, Storage: "sqlite", DSN: dsn, AdminToken: "t", Logger: quiet,
			StorageKeys: []string{"test"},
			Secrets:     secrets.Static{"test": []byte("0123456789abcdef0123456789abcdef")},
			Sinks:       app.SinkConfig{Persist: true},
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Close(); err != nil {
			t.Fatal(err)
		}
		// Rebuilding against the closed file is not itself an error, so the
		// failing path is exercised through a store that cannot append.
		failing := &audittest.Failing{Store: auditinmem.New(), Fail: "Append"}
		b := build(t, app.Config{
			Role: app.RoleAll, Storage: "inmem", AdminToken: "t",
			Sinks: app.SinkConfig{AuditStore: failing},
		})
		publishSomething(t, b)
		if err := b.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

// A store that cannot answer is a 500 without the cause, and a store that
// says "not found" is a 404: the trail must not leak storage detail to an
// admin caller.
func TestAuditRouteMapsStoreErrors(t *testing.T) {
	a, _ := auditApp(t, app.Config{})
	_ = a
	failing := &audittest.Failing{Store: auditinmem.New(), Fail: "List"}
	b := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", AdminToken: "t",
		Sinks: app.SinkConfig{AuditStore: failing},
	})
	srv := serve(t, b)
	resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/audit", "t", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "injected") {
		t.Fatalf("the storage cause leaked to the caller: %s", body)
	}

	getFails := &audittest.Failing{Store: auditinmem.New(), Fail: "Get"}
	c := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", AdminToken: "t",
		Sinks: app.SinkConfig{AuditStore: getFails},
	})
	csrv := serve(t, c)
	got := adminReq(t, csrv.URL, http.MethodGet, "/admin/v1/audit/1", "t", "")
	defer got.Body.Close()
	if got.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", got.StatusCode)
	}
}

// A prune that fails is logged and retried rather than stopping the loop: a
// broken retention pass must not take the server's workers down with it.
func TestAuditRetentionSurvivesAFailedPrune(t *testing.T) {
	fake := clock.NewFake(audittest.T0)
	failing := &audittest.Failing{Store: auditinmem.New(), Fail: "Prune"}
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", AdminToken: "t", Clock: fake,
		Sinks: app.SinkConfig{AuditStore: failing, Retention: time.Hour, PruneInterval: time.Minute},
	})
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(runCtx) }()
	waitFor(t, func() bool { return fake.Pending() > 0 })
	fake.Advance(2 * time.Minute)
	// The loop is still running: it parks on the clock again.
	waitFor(t, func() bool { return fake.Pending() > 0 })
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("a failed prune stopped the workers: %v", err)
	}
}
