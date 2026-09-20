package app_test

import (
	"net/http"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// fixtureAdmin upgrades historical operational fixtures to stored credentials.
// Only the test fixture label is translated; every request still goes through
// the production token verifier and Cedar engine. Authorization tests use
// Build directly, or supply their own principal store without a fixture label.
func fixtureAdmin(t *testing.T, a *app.App, label, dsn string) {
	t.Helper()
	if label == "" {
		return
	}
	token, err := app.FixtureAdminCredential(a)
	if err != nil {
		t.Fatal(err)
	}
	handler := a.Handler
	a.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+label {
			r = r.Clone(r.Context())
			r.Header.Set("Authorization", "Bearer "+string(token))
		}
		handler.ServeHTTP(w, r)
	})
}
