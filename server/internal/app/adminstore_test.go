package app_test

import (
	"context"
	"io"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth/inmem"
	adminsql "github.com/deploymenttheory/go-apple-dm/server/adminauth/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/server/sqlstore/sqlite"
)

// Build from a database DSN and DM_ADMIN_STORE, without an injected store, then
// authenticate with a principal from the store opened by the application.
func TestAdminStoreOnTheProcessDatabase(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "admin.db")
	a := build(t, app.Config{
		Storage: "sqlite", DSN: dsn, Listen: ":0",
	})
	srv := serve(t, a)

	// Reach the same rows the server is using, through a second handle on
	// the same file, and mint a principal there.
	db, err := sqlite.Open(context.Background(), dsn, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := adminsql.Open(context.Background(), db.DB(), sqlite.Dialect, adminsql.Options{})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := adminauth.NewRegistry(app.AdminActions()...)
	if err != nil {
		t.Fatal(err)
	}
	m, err := adminauth.New(st, reg)
	if err != nil {
		t.Fatal(err)
	}
	tok := mintPrincipal(t, m, adminauth.Principal{Name: "ops", Root: true})

	resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/config", tok, "")
	defer func(body io.Closer) { _ = body.Close() }(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: a principal from the server's own store was refused", resp.StatusCode)
	}

	// A token that was never issued is still refused.
	bad := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/config", "nonsense", "")
	defer func(body io.Closer) { _ = body.Close() }(bad.Body)
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", bad.StatusCode)
	}
}

// Without DM_ADMIN_STORE and without a token the admin API is not mounted,
// so turning the store on is an explicit act rather than a side effect of
// choosing a SQL backend.
func TestAdminStoreRequiresAuthenticationByDefault(t *testing.T) {
	a := build(t, app.Config{
		Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "off.db"), Listen: ":0",
	})
	srv := serve(t, a)
	resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/config", "anything", "")
	defer func(body io.Closer) { _ = body.Close() }(resp.Body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: the admin API must require a stored credential", resp.StatusCode)
	}
}

// An in-memory deployment has nowhere persistent for principals, but the admin
// API must still behave the same way.
func TestAdminStoreWithoutADatabase(t *testing.T) {
	a := build(t, app.Config{
		Storage: "inmem", Listen: ":0",
	})
	srv := serve(t, a)
	resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/config", "nope", "")
	defer func(body io.Closer) { _ = body.Close() }(resp.Body)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// An injected store wins over DM_ADMIN_STORE, which is how the other tests
// and an integrator supply their own.
func TestAdminStoreInjectionWins(t *testing.T) {
	st := inmem.New()
	a := build(t, app.Config{
		Storage: "inmem", Listen: ":0",
		AdminStore: st,
	})
	srv := serve(t, a)
	reg, err := adminauth.NewRegistry(app.AdminActions()...)
	if err != nil {
		t.Fatal(err)
	}
	m, err := adminauth.New(st, reg)
	if err != nil {
		t.Fatal(err)
	}
	tok := mintPrincipal(t, m, adminauth.Principal{Name: "injected", Root: true})
	resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/config", tok, "")
	defer func(body io.Closer) { _ = body.Close() }(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: the injected store was not used", resp.StatusCode)
	}
}
