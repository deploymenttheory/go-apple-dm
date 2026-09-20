package app_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/eventstore"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

func eventAdmin(t *testing.T) (*app.App, *sql.DB, *eventstore.Store) {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "events.sqlite")
	a := build(t, app.Config{Storage: "sqlite", DSN: dsn, BootstrapToken: "t", Sinks: app.SinkConfig{Persist: true}})
	s, err := sqlite.Open(t.Context(), dsn, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	e, err := eventstore.Open(t.Context(), s.DB(), sqlite.Dialect)
	if err != nil {
		t.Fatal(err)
	}
	return a, s.DB(), e
}

func eventRequest(a *app.App, method, path, token, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), method, "/admin/v1"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, r)
	return w
}

func TestEventAPIRecordsWithoutWorkersAndRetriesDestinations(t *testing.T) {
	a, _, events := eventAdmin(t)
	seed(t, a, "event-device")
	w := eventRequest(a, "GET", "/events?type=enrollment-imported", "t", "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var list struct {
		Items []struct {
			EventID string `json:"event_id"`
			Type    string `json:"type"`
		} `json:"Items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Items) != 1 || list.Items[0].EventID == "" {
		t.Fatal(list, err)
	}
	id := list.Items[0].EventID
	w = eventRequest(a, "GET", "/events/"+id, "t", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), id) {
		t.Fatal(w.Code, w.Body.String())
	}
	d, err := events.Claim(t.Context(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = events.Finish(t.Context(), d, "destination-unavailable", 0); err != nil {
		t.Fatal(err)
	}
	w = eventRequest(a, "GET", "/events/deliveries?state=blocked", "t", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), id) || strings.Contains(w.Body.String(), d.Token) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = eventRequest(a, "POST", "/events/"+id+"/retry", "t", `{"destination":"audit"}`)
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = eventRequest(a, "GET", "/events/status", "t", "")
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte(`"Capture"`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"records"`)) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		method, path, token, body string
		status                    int
	}{
		{"GET", "/events", "wrong", "", 401},
		{"GET", "/events/missing", "t", "", 404},
		{"GET", "/events?limit=bad", "t", "", 400},
		{"GET", "/events?limit=1001", "t", "", 400},
		{"GET", "/events/deliveries?state=invalid", "t", "", 400},
		{"POST", "/events/" + id + "/retry", "t", `{"destination":"missing"}`, 409},
		{"POST", "/events/" + id + "/retry", "t", `{}`, 400},
		{"POST", "/events/" + id + "/retry", "t", `{"destination":"audit","extra":true}`, 400},
		{"POST", "/events/" + id + "/retry", "t", `{"destination":"audit"}{}`, 400},
	} {
		w := eventRequest(a, tc.method, tc.path, tc.token, tc.body)
		if w.Code != tc.status {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}

func TestLocalAdminCaptureFailureRollsBackMutationAndWithholdsCredential(t *testing.T) {
	a, db, _ := eventAdmin(t)
	id := seed(t, a, "keep-enabled")
	// Fails after the handler changes the enrollment or creates a principal.
	if _, err := db.ExecContext(t.Context(), `CREATE TRIGGER refuse_admin_capture BEFORE INSERT ON event_records WHEN NEW.type = 'admin-action' BEGIN SELECT RAISE(ABORT, 'injected capture failure'); END`); err != nil {
		t.Fatal(err)
	}
	w := eventRequest(a, "DELETE", "/enrollments/device/"+id.ID, "t", "")
	if w.Code != 503 || w.Header().Get("Retry-After") == "" {
		t.Fatal(w.Code, w.Body.String())
	}
	e, err := a.Store.Get(t.Context(), id)
	if err != nil || !e.Enabled || !e.DisabledAt.IsZero() {
		t.Fatal("enrollment changed without recording", e, err)
	}
	w = eventRequest(a, "POST", "/principals", "t", `{"Name":"new-admin","Root":true}`)
	if w.Code != 503 || strings.Contains(w.Body.String(), "Token") || strings.Contains(w.Body.String(), "injected") {
		t.Fatal(w.Code, w.Body.String())
	}
	var n int
	if err = db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM admin_principals WHERE name = 'new-admin'").Scan(&n); err != nil || n != 0 {
		t.Fatal("credential committed without event", n, err)
	}
	w = eventRequest(a, "GET", "/events/status", "t", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "last_failure") {
		t.Fatal("recording failure not observable", w.Code, w.Body.String())
	}
	if _, err = db.ExecContext(t.Context(), "DROP TRIGGER refuse_admin_capture"); err != nil {
		t.Fatal(err)
	}
	w = eventRequest(a, "DELETE", "/enrollments/device/"+id.ID, "t", "")
	if w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	e, err = a.Store.Get(t.Context(), id)
	if err != nil || e.Enabled || e.DisabledAt.IsZero() {
		t.Fatal("retry did not apply", e, err)
	}
}

func TestEnrollmentImportCannotCommitWithoutEvent(t *testing.T) {
	a, db, _ := eventAdmin(t)
	if _, err := db.ExecContext(t.Context(), "DROP TABLE event_records"); err != nil {
		t.Fatal(err)
	}
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "new-device"}
	if err := a.Core.ImportEnrollment(context.Background(), storage.EnrollmentExport{Enrollment: storage.Enrollment{ID: id}}); !errors.Is(err, eventstore.ErrCapture) {
		t.Fatal(err)
	}
	if _, err := a.Store.Get(t.Context(), id); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("unrecorded enrollment survived", err)
	}
}
