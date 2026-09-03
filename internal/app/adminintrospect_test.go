package app_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-mdm/adminauth"
	"github.com/deploymenttheory/go-apple-mdm/internal/app"
)

// GET /routes is generated from the same slice the mux is built from, so it
// cannot drift from what is served. No reference server publishes its route
// table at all.
func TestAdminIntrospection(t *testing.T) {
	a, m, _ := policyApp(t, nil)
	srv := serve(t, a).URL
	// A principal with no policy at all: introspection is authenticated but
	// not policy-gated, because a caller needs it to interpret a 404 that is
	// really a role split, and a policy that had to grant it first would make
	// the explanation unreachable exactly when it is needed.
	tok := mintPrincipal(t, m, adminauth.Principal{Name: "nobody"})

	t.Run("ConfigNeedsNoGrant", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/config", tok, "")
		var got struct {
			Role     string
			Version  string
			Families []string
			Policy   bool
		}
		decodeBody(t, resp, &got)
		if got.Role != string(app.RoleAll) {
			t.Fatalf("role = %q", got.Role)
		}
		if got.Version == "" {
			t.Fatal("no version")
		}
		if !got.Policy {
			t.Fatal("Policy is false though a principal store is configured")
		}
		if len(got.Families) == 0 {
			t.Fatal("no families listed")
		}
	})

	t.Run("RoutesMatchTheTable", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/routes", tok, "")
		var got struct {
			Routes []struct{ Method, Pattern, Action, Family string }
		}
		decodeBody(t, resp, &got)
		if len(got.Routes) != len(a.AdminRoutes()) {
			t.Fatalf("published %d routes, mounted %d", len(got.Routes), len(a.AdminRoutes()))
		}
		published := make(map[string]string, len(got.Routes))
		for _, r := range got.Routes {
			if r.Action == "" || r.Family == "" {
				t.Fatalf("published route %+v has no action or family", r)
			}
			published[r.Method+" "+r.Pattern] = r.Action
		}
		for _, rt := range a.AdminRoutes() {
			// A sub-tree mount carries no method, exactly as the handler
			// splits it, so the key is built the same way on both sides.
			method, pattern, ok := strings.Cut(rt.RoutePattern(), " ")
			if !ok {
				method, pattern = "", rt.RoutePattern()
			}
			action, ok := published[method+" "+pattern]
			if !ok {
				t.Fatalf("mounted route %q is not published", rt.RoutePattern())
			}
			if action != rt.RouteAction() {
				t.Fatalf("route %q published action %q, mounted %q", rt.RoutePattern(), action, rt.RouteAction())
			}
		}
	})

	// Introspection still needs a credential: it is not public.
	t.Run("StillNeedsAuthentication", func(t *testing.T) {
		for _, path := range []string{"/admin/v1/config", "/admin/v1/routes"} {
			resp := adminReq(t, srv, http.MethodGet, path, "", "")
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s without a token = %d, want 401", path, resp.StatusCode)
			}
		}
	})

	// Only the two introspection routes skip the policy check; everything
	// else is gated, so the exemption cannot quietly spread.
	t.Run("OnlyIntrospectionSkipsPolicy", func(t *testing.T) {
		for _, rt := range a.AdminRoutes() {
			if rt.RouteAction() != app.ActionReadConfig {
				continue
			}
			if !strings.Contains(rt.RoutePattern(), "/config") && !strings.Contains(rt.RoutePattern(), "/routes") {
				t.Fatalf("route %q claims the introspection action", rt.RoutePattern())
			}
		}
		// A non-introspection route is refused for the same ungranted caller.
		resp := adminReq(t, srv, http.MethodPost, "/admin/v1/notify", tok, "")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("an ungranted action = %d, want 403", resp.StatusCode)
		}
	})
}
