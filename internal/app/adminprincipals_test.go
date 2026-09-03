package app_test

import (
	"context"
	json "encoding/json/v2"
	"io"
	"net/http"
	"strings"
	"testing"

	"time"

	"github.com/deploymenttheory/go-apple-mdm/adminauth"
	"github.com/deploymenttheory/go-apple-mdm/adminauth/adminauthtest"
	"github.com/deploymenttheory/go-apple-mdm/adminauth/inmem"
	"github.com/deploymenttheory/go-apple-mdm/internal/app"
)

func decodeBody(t *testing.T, resp *http.Response, v any) {
	t.Helper()
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
}

// The credential lifecycle an operator drives: create, use, rotate, revoke.
func TestAdminPrincipalRoutes(t *testing.T) {
	ctx := context.Background()
	a, m, _ := policyApp(t, nil)
	srv := serve(t, a).URL
	rootTok := mintPrincipal(t, m, adminauth.Principal{Name: "root", Root: true})
	if _, err := m.PutPolicy(ctx, adminauth.Root, adminauth.Policy{
		Name:   "admins",
		Source: `permit (principal == MDM::Principal::"root", action, resource);`,
	}); err != nil {
		t.Fatal(err)
	}

	var created struct {
		Principal struct {
			Name     string
			Roles    []string
			HasToken bool
			TokenID  string
		}
		Token string
	}

	t.Run("Create", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodPost, "/admin/v1/principals", rootTok,
			`{"Name":"ci","Roles":["reader"]}`)
		if resp.StatusCode != http.StatusCreated {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("create = %d: %s", resp.StatusCode, body)
		}
		decodeBody(t, resp, &created)
		if created.Principal.Name != "ci" || !created.Principal.HasToken {
			t.Fatalf("created = %+v", created.Principal)
		}
		if !adminauth.Valid(adminauth.Token(created.Token)) {
			t.Fatalf("the returned token is not well formed: %q", adminauth.Redact(adminauth.Token(created.Token)))
		}
	})

	// The token is readable exactly once. No later read returns it, and the
	// listing never carries a credential.
	t.Run("TokenIsNeverReadableAgain", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/principals/ci", rootTok, "")
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if strings.Contains(string(body), created.Token) {
			t.Fatalf("a read returned the token: %s", body)
		}
		if strings.Contains(string(body), "Digest") {
			t.Fatalf("a read exposed the digest: %s", body)
		}
		resp = adminReq(t, srv, http.MethodGet, "/admin/v1/principals", rootTok, "")
		body, _ = io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if strings.Contains(string(body), created.Token) {
			t.Fatalf("the listing returned the token: %s", body)
		}
	})

	t.Run("RotateThenRevoke", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodPost, "/admin/v1/principals/ci/rotate", rootTok, "")
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			t.Fatalf("rotate = %d", resp.StatusCode)
		}
		var rotated struct{ Token string }
		decodeBody(t, resp, &rotated)
		if rotated.Token == created.Token {
			t.Fatal("rotate returned the same token")
		}
		if _, err := m.Authenticate(ctx, adminauth.Token(created.Token)); err == nil {
			t.Fatal("the pre-rotation token still authenticates")
		}
		resp = adminReq(t, srv, http.MethodPost, "/admin/v1/principals/ci/revoke", rootTok, "")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("revoke = %d, want 204", resp.StatusCode)
		}
		if _, err := m.Authenticate(ctx, adminauth.Token(rotated.Token)); err == nil {
			t.Fatal("a revoked token still authenticates")
		}
	})

	t.Run("Patch", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodPatch, "/admin/v1/principals/ci", rootTok, `{"Roles":["ops"]}`)
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			t.Fatalf("patch = %d", resp.StatusCode)
		}
		var got struct{ Roles []string }
		decodeBody(t, resp, &got)
		if len(got.Roles) != 1 || got.Roles[0] != "ops" {
			t.Fatalf("roles = %v", got.Roles)
		}
	})

	t.Run("Delete", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodDelete, "/admin/v1/principals/ci", rootTok, "")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete = %d, want 204", resp.StatusCode)
		}
		if _, err := m.Principal(ctx, "ci"); err == nil {
			t.Fatal("the principal survived a delete")
		}
	})

	// The last root principal cannot be removed through the API either.
	t.Run("LastRootIsProtected", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodDelete, "/admin/v1/principals/root", rootTok, "")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("deleting the last root = %d, want 403", resp.StatusCode)
		}
	})

	t.Run("BadInput", func(t *testing.T) {
		for name, tc := range map[string]struct {
			method, path, body string
			want               int
		}{
			"malformed json":  {http.MethodPost, "/admin/v1/principals", `{`, http.StatusBadRequest},
			"bad name":        {http.MethodPost, "/admin/v1/principals", `{"Name":"not a name!"}`, http.StatusBadRequest},
			"unknown read":    {http.MethodGet, "/admin/v1/principals/nobody", "", http.StatusNotFound},
			"unknown rotate":  {http.MethodPost, "/admin/v1/principals/nobody/rotate", "", http.StatusNotFound},
			"duplicate":       {http.MethodPost, "/admin/v1/principals", `{"Name":"root"}`, http.StatusConflict},
			"unknown patch":   {http.MethodPatch, "/admin/v1/principals/nobody", `{"Roles":["a"]}`, http.StatusNotFound},
			"unknown delete":  {http.MethodDelete, "/admin/v1/principals/nobody", "", http.StatusNotFound},
			"unknown revoke":  {http.MethodPost, "/admin/v1/principals/nobody/revoke", "", http.StatusNotFound},
			"malformed patch": {http.MethodPatch, "/admin/v1/principals/root", `{`, http.StatusBadRequest},
		} {
			resp := adminReq(t, srv, tc.method, tc.path, rootTok, tc.body)
			_ = resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("%s: %d, want %d", name, resp.StatusCode, tc.want)
			}
		}
	})
}

func TestAdminPolicyRoutes(t *testing.T) {
	ctx := context.Background()
	a, m, _ := policyApp(t, nil)
	srv := serve(t, a).URL
	rootTok := mintPrincipal(t, m, adminauth.Principal{Name: "root", Root: true})
	if _, err := m.PutPolicy(ctx, adminauth.Root, adminauth.Policy{
		Name:   "admins",
		Source: `permit (principal == MDM::Principal::"root", action, resource);`,
	}); err != nil {
		t.Fatal(err)
	}
	good := `permit (principal, action == MDM::Action::"` + app.ActionNotify + `", resource);`

	t.Run("PutGetDelete", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodPut, "/admin/v1/policies/ops", rootTok,
			`{"Source":`+quote(good)+`,"Description":"lets anyone notify"}`)
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			t.Fatalf("put = %d: %s", resp.StatusCode, body)
		}
		_ = resp.Body.Close()
		// The source comes back exactly as written, so an operator sees what
		// they wrote rather than a reformatting.
		resp = adminReq(t, srv, http.MethodGet, "/admin/v1/policies/ops", rootTok, "")
		var got struct{ Source, Description string }
		decodeBody(t, resp, &got)
		if got.Source != good {
			t.Fatalf("source = %q, want it byte for byte", got.Source)
		}
		resp = adminReq(t, srv, http.MethodDelete, "/admin/v1/policies/ops", rootTok, "")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Fatalf("delete = %d", resp.StatusCode)
		}
	})

	// A policy naming an action nobody serves is refused when it is written,
	// with the known actions in the message. Cedar alone would accept it and
	// it would then silently never grant.
	t.Run("UnknownActionIsRefusedAtWriteTime", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodPut, "/admin/v1/policies/typo", rootTok,
			`{"Source":`+quote(`permit (principal, action == MDM::Action::"lstEnrollments", resource);`)+`}`)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("typo policy = %d, want 400: %s", resp.StatusCode, body)
		}
		if !strings.Contains(string(body), app.ActionNotify) {
			t.Fatalf("the error does not name the known actions: %s", body)
		}
	})

	t.Run("ActionCatalogue", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/actions", rootTok, "")
		var got struct {
			Items []struct{ ID, Help string }
		}
		decodeBody(t, resp, &got)
		if len(got.Items) != len(app.AdminActions()) {
			t.Fatalf("actions = %d, want %d", len(got.Items), len(app.AdminActions()))
		}
		for _, it := range got.Items {
			if it.Help == "" {
				t.Fatalf("action %q has no help text for an operator", it.ID)
			}
		}
	})

	t.Run("Listing", func(t *testing.T) {
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/policies", rootTok, "")
		var got struct {
			Items []struct{ Name string }
		}
		decodeBody(t, resp, &got)
		if len(got.Items) == 0 {
			t.Fatal("no policies listed")
		}
	})

	t.Run("BadInput", func(t *testing.T) {
		for name, tc := range map[string]struct {
			method, path, body string
			want               int
		}{
			"malformed json": {http.MethodPut, "/admin/v1/policies/x", `{`, http.StatusBadRequest},
			"bad cedar":      {http.MethodPut, "/admin/v1/policies/x", `{"Source":"not cedar"}`, http.StatusBadRequest},
			"unknown get":    {http.MethodGet, "/admin/v1/policies/nope", "", http.StatusNotFound},
			"unknown delete": {http.MethodDelete, "/admin/v1/policies/nope", "", http.StatusNotFound},
		} {
			resp := adminReq(t, srv, tc.method, tc.path, rootTok, tc.body)
			_ = resp.Body.Close()
			if resp.StatusCode != tc.want {
				t.Errorf("%s: %d, want %d", name, resp.StatusCode, tc.want)
			}
		}
	})

	// Policy administration is not policy-grantable, so a principal that is
	// not root is refused even when a policy permits everything.
	t.Run("NonRootIsForbidden", func(t *testing.T) {
		tok := mintPrincipal(t, m, adminauth.Principal{Name: "plain"})
		if _, err := m.PutPolicy(ctx, adminauth.Root, adminauth.Policy{
			Name:   "everything",
			Source: `permit (principal == MDM::Principal::"plain", action, resource);`,
		}); err != nil {
			t.Fatal(err)
		}
		resp := adminReq(t, srv, http.MethodGet, "/admin/v1/policies", tok, "")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("non-root policy read = %d, want 403", resp.StatusCode)
		}
	})
}

// quote renders s as a JSON string.
func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// The principal routes are absent with the static token: there is nothing to
// administer, and mounting them would imply otherwise.
func TestPrincipalRoutesNeedAStore(t *testing.T) {
	a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem", Listen: ":0", AdminToken: "secret"})
	srv := serve(t, a).URL
	resp := adminReq(t, srv, http.MethodGet, "/admin/v1/principals", "secret", "")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("principals with a static token = %d, want 404", resp.StatusCode)
	}
	for _, rt := range a.AdminRoutes() {
		if strings.Contains(rt.RoutePattern(), "/principals") {
			t.Fatalf("principal route %q was registered without a store", rt.RoutePattern())
		}
	}
}

// An oversized body is refused before it is parsed, on every route that
// takes one.
func TestAdminBodyLimit(t *testing.T) {
	a, m, _ := policyApp(t, nil)
	srv := serve(t, a).URL
	rootTok := mintPrincipal(t, m, adminauth.Principal{Name: "root", Root: true})
	if _, err := m.PutPolicy(context.Background(), adminauth.Root, adminauth.Policy{
		Name:   "admins",
		Source: `permit (principal == MDM::Principal::"root", action, resource);`,
	}); err != nil {
		t.Fatal(err)
	}
	huge := `{"Name":"` + strings.Repeat("a", app.MaxAdminBody) + `"}`
	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/admin/v1/principals"},
		{http.MethodPatch, "/admin/v1/principals/root"},
		{http.MethodPut, "/admin/v1/policies/x"},
	} {
		resp := adminReq(t, srv, tc.method, tc.path, rootTok, huge)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusRequestEntityTooLarge {
			t.Errorf("%s %s = %d, want 413", tc.method, tc.path, resp.StatusCode)
		}
	}
}

// A storage failure is an internal error, never a denial: reporting a
// database outage as "forbidden" would send an operator hunting a policy bug.
func TestAdminStoreFailureIsInternal(t *testing.T) {
	st := &adminauthtest.Failing{Store: inmem.New(), Fail: "Principals"}
	base := inmem.New()
	seed, err := adminauth.New(base, mustRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	_, tok, err := seed.CreatePrincipal(context.Background(), adminauth.Root,
		adminauth.Principal{Name: "root", Root: true}, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seed.PutPolicy(context.Background(), adminauth.Root, adminauth.Policy{
		Name:   "admins",
		Source: `permit (principal == MDM::Principal::"root", action, resource);`,
	}); err != nil {
		t.Fatal(err)
	}
	st.Store = base
	a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem", Listen: ":0", AdminStore: st})
	srv := serve(t, a).URL
	resp := adminReq(t, srv, http.MethodGet, "/admin/v1/principals", string(tok), "")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("a failing store returned %d, want 500", resp.StatusCode)
	}
}

func mustRegistry(t *testing.T) *adminauth.Registry {
	t.Helper()
	reg, err := adminauth.NewRegistry(app.AdminActions()...)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}
