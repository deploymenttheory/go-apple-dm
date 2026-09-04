package app_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth/inmem"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/event"
)

// policyApp builds a server whose admin API authenticates against a principal
// store and authorizes by policy, which is what a deployment runs. The static
// token path is the development opt-out and is covered elsewhere.
func policyApp(t *testing.T, bus *event.Bus) (*app.App, *adminauth.Manager, adminauth.Store) {
	t.Helper()
	st := inmem.New()
	a := build(t, app.Config{
		Role: app.RoleAll, Storage: "inmem", Listen: ":0",
		AdminStore: st, Bus: bus,
	})
	reg, err := adminauth.NewRegistry(app.AdminActions()...)
	if err != nil {
		t.Fatal(err)
	}
	m, err := adminauth.New(st, reg)
	if err != nil {
		t.Fatal(err)
	}
	return a, m, st
}

// mint creates a principal and returns its token.
func mintPrincipal(t *testing.T, m *adminauth.Manager, p adminauth.Principal) string {
	t.Helper()
	_, tok, err := m.CreatePrincipal(context.Background(), adminauth.Root, p, time.Time{})
	if err != nil {
		t.Fatalf("CreatePrincipal %s: %v", p.Name, err)
	}
	return string(tok)
}

func adminReq(t *testing.T, srv string, method, path, token string, body string) *http.Response {
	t.Helper()
	var rdr *strings.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	} else {
		rdr = strings.NewReader("")
	}
	req, err := http.NewRequestWithContext(context.Background(), method, srv+path, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// The rule a scope string cannot express: one action, narrowed by request
// context, so a credential can read declarations without publishing them.
func TestAdminPolicy(t *testing.T) {
	ctx := context.Background()
	bus := event.New()
	rec := &recorder{}
	bus.Subscribe(event.All, rec.handle)
	a, m, _ := policyApp(t, bus)
	srv := serve(t, a).URL

	reader := mintPrincipal(t, m, adminauth.Principal{Name: "reader", Roles: []string{"reader"}})
	writer := mintPrincipal(t, m, adminauth.Principal{Name: "writer", Roles: []string{"writer"}})

	if _, err := m.PutPolicy(ctx, adminauth.Root, adminauth.Policy{
		Name: "roles",
		Source: `
			permit (principal in MDM::Role::"reader", action == MDM::Action::"getDeclaration", resource);
			permit (principal in MDM::Role::"writer", action == MDM::Action::"putDeclaration", resource);
			permit (principal in MDM::Role::"writer", action == MDM::Action::"getDeclaration", resource);
		`,
	}); err != nil {
		t.Fatalf("PutPolicy: %v", err)
	}

	decl := `{"Type":"com.apple.configuration.management.test","Identifier":"com.example.a","Payload":{"Echo":"hi"}}`

	t.Run("GrantedActionIsAllowed", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodPut, "/admin/v1/declarations", writer, decl)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("writer PUT = %d, want 200", resp.StatusCode)
		}
	})

	t.Run("UngrantedActionIsForbidden", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodPut, "/admin/v1/declarations", reader, decl)
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("reader PUT = %d, want 403", resp.StatusCode)
		}
	})

	// A principal that can read but not write is exactly the CI credential
	// that no single shared secret can express.
	t.Run("ReadStillWorksForTheReader", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/declarations/com.example.a", reader, "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("reader GET = %d, want 200", resp.StatusCode)
		}
	})

	// An action no policy names is denied by default, not allowed.
	t.Run("UnnamedActionIsDeniedByDefault", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodPost, "/admin/v1/notify", writer, "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("notify with no policy = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("UnknownTokenIsUnauthorized", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/declarations/com.example.a", "mdmt_totally-not-a-real-token", "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("bad token = %d, want 401", resp.StatusCode)
		}
	})

	// A revoked credential stops working without a restart, which is the
	// operational property a configured token cannot offer.
	t.Run("RevokedTokenStopsWorking", func(t *testing.T) {
		tok := mintPrincipal(t, m, adminauth.Principal{Name: "temp", Roles: []string{"reader"}})
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/declarations/com.example.a", tok, "")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("before revoke = %d, want 200", resp.StatusCode)
		}
		if err := m.Revoke(ctx, adminauth.Root, "temp"); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		resp = adminReq(t, srv, http.MethodGet, "/admin/v1/declarations/com.example.a", tok, "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("after revoke = %d, want 401", resp.StatusCode)
		}
	})

	// A rotated credential invalidates the previous value immediately.
	t.Run("RotatedTokenReplacesTheOld", func(t *testing.T) {
		old := mintPrincipal(t, m, adminauth.Principal{Name: "rot", Roles: []string{"reader"}})
		_, fresh, err := m.Rotate(ctx, adminauth.Root, "rot", time.Time{})
		if err != nil {
			t.Fatalf("Rotate: %v", err)
		}
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/declarations/com.example.a", old, "")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("old token after rotate = %d, want 401", resp.StatusCode)
		}
		resp = adminReq(t, srv, http.MethodGet, "/admin/v1/declarations/com.example.a", string(fresh), "")
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("new token = %d, want 200", resp.StatusCode)
		}
	})

	// The request's resource and context reach the policy, so a rule can name
	// one enrollment or one set rather than the deployment as a whole. This
	// is what a coarse credential scope cannot express at all.
	t.Run("ResourceAndContextReachThePolicy", func(t *testing.T) {
		ops := mintPrincipal(t, m, adminauth.Principal{Name: "ops", Roles: []string{"ops"}})
		if _, err := m.PutPolicy(ctx, adminauth.Root, adminauth.Policy{
			Name: "ops",
			Source: `permit (
				principal in MDM::Role::"ops",
				action == MDM::Action::"assignSet",
				resource == MDM::Enrollment::"device/UDID-OK"
			) when { context.set == "allowed" && context.channel == "device" };`,
		}); err != nil {
			t.Fatal(err)
		}
		// The assertion is the authorization outcome, not the handler's: an
		// allowed request reaches the engine and may still 404 on a set that
		// does not exist, which is a different answer from being refused.
		for _, tc := range []struct {
			name, path string
			forbidden  bool
		}{
			{"the named enrollment and set", "/admin/v1/enrollments/device/UDID-OK/sets/allowed", false},
			{"another set on the same enrollment", "/admin/v1/enrollments/device/UDID-OK/sets/other", true},
			{"the same set on another enrollment", "/admin/v1/enrollments/device/UDID-NO/sets/allowed", true},
			{"a user channel rather than a device", "/admin/v1/enrollments/user/UDID-OK/sets/allowed?parent=UDID-OK", true},
		} {
			resp := adminReq(t, srv, http.MethodPut, tc.path, ops, "")
			_ = resp.Body.Close()
			got := resp.StatusCode == http.StatusForbidden
			if got != tc.forbidden {
				t.Fatalf("%s: %s = %d, forbidden=%v want forbidden=%v", tc.name, tc.path, resp.StatusCode, got, tc.forbidden)
			}
		}
	})

	// A refusal names the principal, so an operator can see who was denied
	// what. Neither Fleet nor Zentral records this.
	t.Run("DenialIsAuditedWithThePrincipal", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodPut, "/admin/v1/declarations", reader, decl)
		_ = resp.Body.Close()
		denied := rec.ofType(event.AdminDenied)
		if len(denied) == 0 {
			t.Fatal("no AdminDenied event")
		}
		var found bool
		for _, e := range denied {
			if e.Actor == "reader" {
				found = true
				data := e.Data.(map[string]any)
				if data["Action"] != app.ActionPutDeclaration {
					t.Fatalf("denied action = %v", data["Action"])
				}
				if data["TokenID"] == "" {
					t.Fatal("the denial does not name the credential")
				}
			}
		}
		if !found {
			t.Fatal("no denial names the reader principal")
		}
	})
}

// Policy administration is not policy-grantable: a permit-all policy still
// does not let a non-root principal edit policies, because a policy that can
// edit policies can grant itself anything.
func TestAdminPolicyAdministrationNeedsRoot(t *testing.T) {
	ctx := context.Background()
	a, m, _ := policyApp(t, nil)
	srv := serve(t, a).URL
	tok := mintPrincipal(t, m, adminauth.Principal{Name: "everything", Roles: []string{"all"}})
	if _, err := m.PutPolicy(ctx, adminauth.Root, adminauth.Policy{
		Name:   "all",
		Source: `permit (principal in MDM::Role::"all", action, resource);`,
	}); err != nil {
		t.Fatal(err)
	}
	// The permit-all policy grants every ordinary action.
	resp := adminReq(t, srv, http.MethodPost, "/admin/v1/notify", tok, "")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("notify with permit-all = %d, want 200", resp.StatusCode)
	}
	// Root remains a separate capability the policy cannot confer.
	p, err := m.Principal(ctx, "everything")
	if err != nil {
		t.Fatal(err)
	}
	if p.Root {
		t.Fatal("a policy made a principal root")
	}
	if _, err := m.PutPolicy(ctx, p, adminauth.Policy{Name: "x", Source: `permit (principal, action, resource);`}); err == nil {
		t.Fatal("a permit-all policy granted policy administration")
	}
}
