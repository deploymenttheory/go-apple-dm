package app_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/maintenance"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
	"github.com/deploymenttheory/go-apple-dm/server/webhook"
)

// TestMaintenanceRejectsWithoutWebhookWrites checks the assembled HTTP stack does
// not persist observations after maintenance acknowledges, and resumes capture.
func TestMaintenanceRejectsWithoutWebhookWrites(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "maintenance.sqlite")
	a := build(t, app.Config{Storage: "sqlite", DSN: dsn, BootstrapToken: "t", Webhooks: webhook.Config{Enabled: true}})
	subscription := eventRequest(a, "POST", "/webhooks", "t", `{"name":"maintenance","url":"https://receiver.example.test/webhook","events":["protocol.mdm.exchange"]}`)
	if subscription.Code != http.StatusCreated {
		t.Fatal(subscription.Code, subscription.Body.String())
	}
	db, err := sql.Open("sqlite", sqlite.DSN(dsn, sqlite.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	control, err := maintenance.Open(t.Context(), db, sqlite.Dialect, false)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorkers := context.WithCancel(t.Context())
	workers := make(chan error, 1)
	go func() { workers <- a.Run(workerCtx) }()
	t.Cleanup(func() {
		stopWorkers()
		select {
		case err := <-workers:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("workers did not stop")
		}
	})
	ticket := rand.Text()
	if err := control.Request(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	deadline, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := control.WaitDrained(deadline, ticket); err != nil {
		t.Fatal(err)
	}
	counts := func() [2]int {
		t.Helper()
		var counts [2]int
		if err := db.QueryRowContext(t.Context(), `SELECT (SELECT COUNT(*) FROM webhook_messages), (SELECT COUNT(*) FROM event_deliveries)`).Scan(&counts[0], &counts[1]); err != nil {
			t.Fatal(err)
		}
		return counts
	}
	before := counts()
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/mdm", nil))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	if after := counts(); after != before {
		t.Fatalf("maintenance-refused request persisted webhook data: before=%v after=%v", before, after)
	}
	if status, err := control.Status(t.Context()); err != nil || !status.Ready() {
		t.Fatal("maintenance acknowledgement changed", status, err)
	}
	if err := control.Resume(t.Context(), ticket); err != nil {
		t.Fatal(err)
	}
	for {
		ready := httptest.NewRecorder()
		a.Handler.ServeHTTP(ready, httptest.NewRequestWithContext(deadline, http.MethodGet, "/healthz", nil))
		if ready.Code == http.StatusOK {
			break
		}
		select {
		case <-deadline.Done():
			t.Fatal("admission did not resume")
		case <-time.After(10 * time.Millisecond):
		}
	}
	w = httptest.NewRecorder()
	a.Handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/mdm", nil))
	if w.Code == http.StatusServiceUnavailable {
		t.Fatal("resumed request refused by maintenance")
	}
	if after := counts(); after != [2]int{before[0] + 1, before[1] + 1} {
		t.Fatalf("resumed request observation missing: before=%v after=%v", before, after)
	}
}
