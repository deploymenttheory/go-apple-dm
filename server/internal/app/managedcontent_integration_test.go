//go:build integration

package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
)

// TestManagedContentSealedSQLCapture exercises the real runtime's callback and
// SDK mutation/event boundary on each persistent backend, without resetting any
// shared table. Each SQL backend owns a separate schema/database because runtime
// startup scans sealed records, including records outside the selected location.
func TestManagedContentSealedSQLCapture(t *testing.T) {
	for _, tc := range []struct{ backend, env string }{{"sqlite", ""}, {"postgres", "TEST_POSTGRES_DSN"}, {"mysql", "TEST_MYSQL_DSN"}} {
		t.Run(tc.backend, func(t *testing.T) {
			f := contentFixtureService(t)
			cfg := contentConfig(t, f)
			cfg.Storage = tc.backend
			if tc.env != "" {
				dsn := os.Getenv(tc.env)
				if dsn == "" {
					t.Skip(tc.env + " not set")
				}
				cfg.DSN = contentIsolatedSQLDSN(t, tc.backend, dsn)
			}
			a, token := contentBuild(t, cfg)
			location := "content323-" + rand.Text()
			base := "/appsbooks/locations/" + location
			w := contentRequest(t, a, token, "PUT", base, map[string]any{"revision": 0, "sToken": contentToken("sql-secret")})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			w = contentRequest(t, a, token, "PUT", base+"/receiver", map[string]any{"token": "sql-receiver"})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			callback := func(id string) int {
				r := httptest.NewRequestWithContext(t.Context(), "POST", "/appsbooks/notifications/"+location, strings.NewReader(`{"notificationId":"`+id+`","notificationType":"TEST_NOTIFICATION","uId":"`+f.uid+`","notification":{}}`))
				r.Header.Set("Authorization", "Bearer sql-receiver")
				reply := httptest.NewRecorder()
				a.Handler.ServeHTTP(reply, r)
				return reply.Code
			}
			a.eventPublisher.CaptureAdditional = func(_ context.Context, _ event.Event) error { return errors.New("capture unavailable") }
			if code := callback("rollback"); code != 503 {
				t.Fatal("callback accepted failed SQL capture", code)
			}
			if _, err := a.AppsBooks.LoadNotification(t.Context(), location, "rollback"); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("callback receipt escaped rollback", err)
			}
			a.eventPublisher.CaptureAdditional = nil
			if code := callback("retained"); code != 204 {
				t.Fatal("callback not committed", code)
			}
			a.eventPublisher.CaptureAdditional = func(_ context.Context, _ event.Event) error { return errors.New("replay capture unavailable") }
			if code := callback("retained"); code != 503 {
				t.Fatal("callback replay escaped required capture", code)
			}
			if _, err := a.AppsBooks.LoadNotification(t.Context(), location, "retained"); err != nil {
				t.Fatal("failed replay erased prior durable receipt", err)
			}
			a.eventPublisher.CaptureAdditional = nil
			if err := a.Close(); err != nil {
				t.Fatal(err)
			}
			a, token = contentBuild(t, cfg)
			w = contentRequest(t, a, token, "GET", base+"/notifications/retained/evidence", nil)
			if w.Code != 200 {
				t.Fatal("callback lost on SQL restart", w.Code, w.Body.String())
			}
			before, err := a.AppsBooks.Load(t.Context(), location)
			if err != nil {
				t.Fatal(err)
			}
			a.eventPublisher.CaptureAdditional = func(_ context.Context, _ event.Event) error { return errors.New("capture unavailable") }
			if _, err := a.AppsBooks.Register(t.Context(), location, before.Revision, contentToken("replacement-secret")); !errors.Is(err, event.ErrCapture) {
				t.Fatal("token replacement accepted failed capture", err)
			}
			after, err := a.AppsBooks.Load(t.Context(), location)
			if err != nil || after.CredentialRevision != before.CredentialRevision || after.Revision != before.Revision {
				t.Fatal("token escaped SQL rollback", after, err)
			}
			a.eventPublisher.CaptureAdditional = nil
			// Exercise the complete SDK Step -> native queue/receipt -> required event
			// transaction on this actual backend, after fresh independent Apple readback.
			f.assets.Store(true)
			f.assignments.Store(true)
			nativeID := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "content-native-" + rand.Text()}
			serial := "serial-" + rand.Text()
			seedContentEnrollment(t, a, nativeID, "pin-"+rand.Text(), serial)
			operation := "native-operation-" + rand.Text()
			path := "/managed-content/operations/" + operation
			plan := managedcontent.Plan{LocationID: location, Content: managedcontent.Content{Asset: appsbooks.Asset{AdamID: "1", PricingParam: "STDQ"}, Kind: "App"}, Target: managedcontent.Selection{Enrollment: nativeID, Serial: serial}, Method: managedcontent.MDM, MaxPages: 2}
			w = contentRequest(t, a, token, "PUT", path, map[string]any{"mode": "install", "plan": plan})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			a.eventPublisher.CaptureAdditional = func(_ context.Context, e event.Event) error {
				if e.Type == "managed-content-dispatch" {
					return errors.New("native capture unavailable")
				}
				return nil
			}
			w = contentRequest(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
			if w.Code != 503 {
				t.Fatal("native dispatch accepted failed capture", w.Code, w.Body.String())
			}
			queue, err := a.Store.Commands(t.Context(), nativeID, storage.CommandQuery{}, paging.Page{Limit: 10})
			if err != nil || len(queue.Items) != 0 {
				t.Fatal("native queue escaped SQL capture rollback", len(queue.Items), err)
			}
			if _, err := a.protocol.Get(t.Context(), deliveryKey(operation)); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("native receipt escaped SQL capture rollback", err)
			}
			if _, err := a.protocol.Get(t.Context(), contentAssetKey(nativeID, "1")); !errors.Is(err, state.ErrNotFound) {
				t.Fatal("native asset owner escaped SQL capture rollback", err)
			}
			a.eventPublisher.CaptureAdditional = nil
			w = contentRequest(t, a, token, "POST", path+"/reconcile", contentWorkerInput{Worker: "operator", LeaseSeconds: 60, Note: "original worker stopped; atomically absent native dispatch", RetryAbsent: true})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			w = contentRequest(t, a, token, "POST", path+"/step", contentWorkerInput{Worker: "operator", LeaseSeconds: 60})
			if w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			op, err := a.ManagedContent.Load(t.Context(), operation)
			if err != nil || op.Phase != managedcontent.Dispatched || op.Observation == nil || op.Observation.Installed {
				t.Fatal("native recovery inferred installation", op, err)
			}
		})
	}
}

// contentIsolatedSQLDSN gives this sealed fixture its own persistent namespace.
// Its cleanup removes only the random resource created here, after app pools close.
func contentIsolatedSQLDSN(t *testing.T, backend, dsn string) string {
	t.Helper()
	name := "inventory_test_content_capture_" + strings.ToLower(rand.Text())
	var control *sql.DB
	var drop string
	switch backend {
	case "postgres":
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil {
			t.Fatal(err)
		}
		control = stdlib.OpenDB(*cfg)
		t.Cleanup(func() { _ = control.Close() })
		if _, err := control.ExecContext(t.Context(), "CREATE SCHEMA "+name); err != nil {
			t.Fatal(err)
		}
		drop = "DROP SCHEMA " + name + " CASCADE"
		// pgx.ConnString retains the original text rather than serializing changed
		// RuntimeParams; pass search_path in the DSN actually opened by the runtime.
		if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
			u, err := url.Parse(dsn)
			if err != nil {
				t.Fatal(err)
			}
			q := u.Query()
			q.Set("search_path", name)
			u.RawQuery = q.Encode()
			dsn = u.String()
		} else {
			dsn += " search_path=" + name
		}
	case "mysql":
		cfg, err := mysqldriver.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		control, err = sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = control.Close() })
		if _, err := control.ExecContext(t.Context(), "CREATE DATABASE "+name); err != nil {
			t.Fatal(err)
		}
		drop = "DROP DATABASE " + name
		cfg.DBName = name
		dsn = cfg.FormatDSN()
	default:
		t.Fatalf("unsupported isolated SQL backend %q", backend)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := control.ExecContext(ctx, drop); err != nil {
			t.Errorf("remove owned SQL fixture: %v", err)
		}
	})
	return dsn
}
