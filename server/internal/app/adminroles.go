package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

func (a *App) roleRoutes() []adminRoute {
	var routes []adminRoute
	add := func(action, pattern string, handler http.HandlerFunc) {
		routes = append(routes, adminRoute{Action: action, Pattern: pattern, Family: "authorization", LocalMutation: true, Handler: handler})
	}
	add(ActionReadRoles, "GET /roles", func(w http.ResponseWriter, r *http.Request) {
		p, err := page(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		roles, err := a.admin.Roles(r.Context(), adminauth.Page{Cursor: p.Cursor, Limit: p.Limit})
		if err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, roles)
	})
	add(ActionReadRoles, "GET /roles/{name}", func(w http.ResponseWriter, r *http.Request) {
		role, err := a.admin.Role(r.Context(), r.PathValue("name"))
		if err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, role)
	})
	add(ActionManageRoles, "PUT /roles/{name}", func(w http.ResponseWriter, r *http.Request) {
		var input struct{ Description string }
		if !decodeAdmin(w, r, &input) {
			return
		}
		role, err := a.admin.PutRole(r.Context(), a.actor(r), adminauth.Role{Name: r.PathValue("name"), Description: input.Description})
		if err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, role)
	})
	add(ActionManageRoles, "DELETE /roles/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.admin.DeleteRole(r.Context(), a.actor(r), r.PathValue("name")); err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	add(ActionManagePolicies, "POST /policies/validate", func(w http.ResponseWriter, r *http.Request) {
		var input adminauth.Policy
		if !decodeAdmin(w, r, &input) {
			return
		}
		if err := a.admin.ValidatePolicy(r.Context(), input); err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"Valid": true})
	})
	add(ActionManagePolicies, "POST /policies/{name}/activation", func(w http.ResponseWriter, r *http.Request) {
		var input struct{ Active *bool }
		if !decodeAdmin(w, r, &input) {
			return
		}
		if input.Active == nil {
			writeError(w, http.StatusBadRequest, adminauth.ErrInvalid)
			return
		}
		policy, err := a.admin.GetPolicy(r.Context(), a.actor(r), r.PathValue("name"))
		if err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		policy.Active = input.Active
		policy, err = a.admin.PutPolicy(r.Context(), a.actor(r), policy)
		if err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, policy)
	})
	for _, rt := range []adminRoute{
		{Pattern: "GET /auth/me", Action: ActionReadConfig, Family: "authorization", Introspection: true, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, viewOf(a.actor(r))) })},
		{Pattern: "GET /schema", Action: ActionReadConfig, Family: "authorization", Introspection: true, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, http.StatusOK, a.admin.Registry().Schema()) })},
	} {
		routes = append(routes, rt)
	}
	return routes
}

// bootstrapAdmin is the sole endpoint accepting the one-time bootstrap secret.
// The store atomically consumes bootstrap and creates the first credential.
func (a *App) bootstrapAdmin(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	r = r.WithContext(context.WithValue(r.Context(), authorizationKey{}, &authorizationRequest{Resource: adminauth.SystemResource}))

	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || a.cfg.BootstrapToken == "" || !constantTimeEqual(token, a.cfg.BootstrapToken) {
		a.auditDenied(r, adminauth.Principal{}, adminRoute{Action: ActionManagePrincipals}, ErrUnauthorized)
		writeError(w, http.StatusUnauthorized, ErrUnauthorized)
		return
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Name      string
			ExpiresAt time.Time
		}
		if !decodeAdmin(w, r, &input) {
			return
		}
		p, token, err := a.admin.Bootstrap(r.Context(), input.Name, input.ExpiresAt)
		if err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"Principal": viewOf(p), "Token": string(token)})
	})
	rt := adminRoute{Pattern: "POST /auth/bootstrap", Action: ActionManagePrincipals, Family: "authorization", LocalMutation: true, Handler: handler}
	actor := adminauth.Principal{Name: "bootstrap"}
	if a.eventPublisher != nil {
		a.localAdmin(w, r, actor, rt)
		return
	}
	a.sensitiveAdminRead(w, r, actor, rt)
}
