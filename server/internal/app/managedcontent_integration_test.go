//go:build integration

package app

import (
	"context"
	"crypto/rand"
	"errors"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// TestManagedContentSealedSQLCapture exercises the real runtime's callback and
// SDK mutation/event boundary on each persistent backend, without resetting any
// shared table. Random location UIDs isolate repeated runs and independent pools.
func TestManagedContentSealedSQLCapture(t *testing.T) {
	for _, tc := range []struct{ backend, env string }{{"sqlite", ""}, {"postgres", "TEST_POSTGRES_DSN"}, {"mysql", "TEST_MYSQL_DSN"}} {
		t.Run(tc.backend, func(t *testing.T) {
			f := contentFixtureService(t)
			cfg := contentConfig(t, f)
			cfg.Storage = tc.backend
			if tc.env != "" {
				cfg.DSN = os.Getenv(tc.env)
				if cfg.DSN == "" {
					t.Skip(tc.env + " not set")
				}
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
		})
	}
}
