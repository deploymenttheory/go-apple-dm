package app_test

import (
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/internal/app"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/v3/server/adminauth/inmem"
	adminsql "github.com/deploymenttheory/go-apple-dm/v3/server/adminauth/sqlstore"
	"github.com/deploymenttheory/go-apple-dm/v3/server/sqlstore/sqlite"
)

// The debt this closes: adminauth/sqlstore was imported only by its own
// tests. cmd/dmserver never set AdminStore, so principals, Cedar policies
// and revocable tokens were unreachable from the shipped binary and every
// phase 8 claim rested on a store an in-process caller injected.
//
// This builds the server the way the binary does -- a database DSN and
// DM_ADMIN_STORE, nothing injected -- then authenticates with a principal
// created through the store the server opened for itself.
func TestAdminStoreOnTheProcessDatabase(t *testing.T) {
	dsn := filepath.Join(t.TempDir(), "admin.db")
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "sqlite", DSN: dsn, Listen: ":0",
		AdminStoreEnabled: true,
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: a principal from the server's own store was refused", resp.StatusCode)
	}

	// A token that was never issued is still refused.
	bad := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/config", "nonsense", "")
	defer bad.Body.Close()
	if bad.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", bad.StatusCode)
	}
}

// Without DM_ADMIN_STORE and without a token the admin API is not mounted,
// so turning the store on is an explicit act rather than a side effect of
// choosing a SQL backend.
func TestAdminStoreOffByDefault(t *testing.T) {
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "sqlite", DSN: filepath.Join(t.TempDir(), "off.db"), Listen: ":0",
	})
	srv := serve(t, a)
	resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/config", "anything", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: the admin API was mounted without being asked for", resp.StatusCode)
	}
}

// An in-memory deployment has nowhere persistent for principals, but the admin
// API must still behave the same way.
func TestAdminStoreWithoutADatabase(t *testing.T) {
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", Listen: ":0", AdminStoreEnabled: true,
	})
	srv := serve(t, a)
	resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/config", "nope", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// An injected store wins over DM_ADMIN_STORE, which is how the other tests
// and an integrator supply their own.
func TestAdminStoreInjectionWins(t *testing.T) {
	st := inmem.New()
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", Listen: ":0",
		AdminStore: st, AdminStoreEnabled: true,
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
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: the injected store was not used", resp.StatusCode)
	}
}

// Break-glass: the static token keeps working beside a principal store,
// because an empty store authenticates nobody and the route that creates the
// first principal is itself authorized. Before this, configuring a store made
// DM_ADMIN_TOKEN silently stop working.
func TestBreakGlassAlongsideThePrincipalStore(t *testing.T) {
	bus := event.New()
	rec := &recorder{}
	bus.Subscribe(event.All, rec.handle)

	st := inmem.New()
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", Listen: ":0",
		AdminStore: st, AdminToken: "break-glass-secret", Bus: bus,
	})
	srv := serve(t, a)

	t.Run("BootstrapsAnEmptyStore", func(t *testing.T) {
		resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/config", "break-glass-secret", "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200: the break-glass token was refused with a store configured", resp.StatusCode)
		}
	})

	t.Run("ReportedByConfig", func(t *testing.T) {
		resp := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/config", "break-glass-secret", "")
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		var cfg struct{ Policy, BreakGlass bool }
		if err := json.Unmarshal(body, &cfg); err != nil {
			t.Fatal(err)
		}
		if !cfg.Policy || !cfg.BreakGlass {
			t.Fatalf("config = %+v, want both policy and break-glass reported", cfg)
		}
	})

	// The point of the distinct actor: an operator can alert on it once
	// bootstrap is over.
	t.Run("AuditedUnderItsOwnActor", func(t *testing.T) {
		rec.reset()
		resp := adminReq(t, srv.URL, http.MethodPut, "/admin/v1/declarations", "break-glass-secret", `{}`)
		defer resp.Body.Close()
		actions := rec.ofType(event.AdminAction)
		if len(actions) == 0 {
			t.Fatal("a break-glass request published no AdminAction event")
		}
		if got := actions[0].Actor; got != app.BreakGlassActor {
			t.Fatalf("actor = %q, want %q", got, app.BreakGlassActor)
		}
	})

	// Break-glass bypasses policy; an ordinary principal does not. Both
	// credentials are live at once and they are graded differently.
	t.Run("BypassesPolicyWhileStoredPrincipalsDoNot", func(t *testing.T) {
		reg, err := adminauth.NewRegistry(app.AdminActions()...)
		if err != nil {
			t.Fatal(err)
		}
		m, err := adminauth.New(st, reg)
		if err != nil {
			t.Fatal(err)
		}
		_, tok, err := m.CreatePrincipal(context.Background(),
			adminauth.Root, adminauth.Principal{Name: "reader"}, time.Time{})
		if err != nil {
			t.Fatal(err)
		}
		denied := adminReq(t, srv.URL, http.MethodPut, "/admin/v1/declarations", string(tok), `{}`)
		defer denied.Body.Close()
		if denied.StatusCode != http.StatusForbidden {
			t.Fatalf("stored principal status = %d, want 403: policy was not enforced", denied.StatusCode)
		}
		allowed := adminReq(t, srv.URL, http.MethodGet, "/admin/v1/config", "break-glass-secret", "")
		defer allowed.Body.Close()
		if allowed.StatusCode != http.StatusOK {
			t.Fatalf("break-glass status = %d, want 200", allowed.StatusCode)
		}
	})
}
