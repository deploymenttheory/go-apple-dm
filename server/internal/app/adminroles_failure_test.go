package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth/adminauthtest"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth/inmem"
)

type unavailableRoles struct {
	adminauth.Store
	fail string
}

func (s unavailableRoles) Roles(ctx context.Context, p adminauth.Page) (adminauth.Result[adminauth.Role], error) {
	if s.fail == "Roles" {
		return adminauth.Result[adminauth.Role]{}, adminauthtest.ErrFailing
	}
	return s.Store.Roles(ctx, p)
}

func (s unavailableRoles) Role(ctx context.Context, name string) (adminauth.Role, error) {
	if s.fail == "Role" {
		return adminauth.Role{}, adminauthtest.ErrFailing
	}
	return s.Store.Role(ctx, name)
}

func TestRoleHandlersRejectInvalidInputAndStorageFailures(t *testing.T) {
	for _, tc := range []struct {
		method, path, body, fail string
		status                   int
	}{
		{"GET", "/roles?limit=invalid", "", "", 400},
		{"GET", "/roles", "", "Roles", 500},
		{"GET", "/roles/existing", "", "Role", 500},
		{"GET", "/roles/missing", "", "", 404},
		{"PUT", "/roles/new", "{", "", 400},
		{"PUT", "/roles/invalid!", `{}`, "", 400},
		{"DELETE", "/roles/missing", "", "", 404},
		{"DELETE", "/roles/existing", "", "", 409},
		{"POST", "/policies/validate", "{", "", 400},
		{"POST", "/policies/validate", `{"Name":"candidate","Source":"invalid policy"}`, "", 400},
		{"POST", "/policies/existing/activation", "{", "", 400},
		{"POST", "/policies/existing/activation", `{}`, "", 400},
		{"POST", "/policies/missing/activation", `{"Active":false}`, "", 404},
		{"POST", "/policies/existing/activation", `{"Active":false}`, "GetPolicy", 500},
		{"POST", "/policies/existing/activation", `{"Active":false}`, "PutPolicy", 500},
	} {
		t.Run(tc.method+tc.path+tc.fail, func(t *testing.T) {
			ctx, now := t.Context(), time.Now()
			base := inmem.New()
			if _, err := base.PutRole(ctx, adminauth.Role{Name: "existing"}, now); err != nil {
				t.Fatal(err)
			}
			if _, err := base.PutPolicy(ctx, adminauth.Policy{Name: "existing", Source: `permit(principal in MDM::Role::"existing",action,resource);`}, now); err != nil {
				t.Fatal(err)
			}
			store := unavailableRoles{Store: &adminauthtest.Failing{Store: base, Fail: tc.fail}, fail: tc.fail}
			reg, err := adminauth.NewRegistry(AdminActions()...)
			if err != nil {
				t.Fatal(err)
			}
			manager, err := adminauth.New(store, reg)
			if err != nil {
				t.Fatal(err)
			}
			a := &App{admin: manager}
			mux := http.NewServeMux()
			for _, route := range a.roleRoutes() {
				mux.Handle(route.Pattern, route.Handler)
			}
			req := httptest.NewRequestWithContext(context.WithValue(ctx, actorKey{}, adminauth.Root), tc.method, tc.path, strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, req)
			if w.Code != tc.status || strings.Contains(w.Body.String(), adminauthtest.ErrFailing.Error()) {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
			if got, err := base.GetPolicy(ctx, "existing"); err != nil || !got.Enabled() {
				t.Fatalf("failed request changed policy: %+v %v", got, err)
			}
			if _, err := base.Role(ctx, "existing"); err != nil {
				t.Fatalf("failed request deleted role: %v", err)
			}
			if _, err := base.Role(ctx, "new"); !errors.Is(err, adminauth.ErrNotFound) {
				t.Fatalf("invalid input created role: %v", err)
			}
		})
	}
}
