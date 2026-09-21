package app

import (
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

// principalRoutes administer stored credentials and the Cedar policies that
// bound their access. Authority mutations require a root principal.
//
//	GET    /principals
//	POST   /principals                       body: {Name, Roles, Root, ExpiresAt}
//	GET    /principals/{name}
//	PATCH  /principals/{name}                body: {Roles, Root}
//	DELETE /principals/{name}
//	POST   /principals/{name}/rotate         body: {ExpiresAt}
//	POST   /principals/{name}/revoke
//	GET    /policies
//	GET    /policies/{name}
//	PUT    /policies/{name}                  body: {Source, Description}
//	DELETE /policies/{name}
//	GET    /actions
func (a *App) principalRoutes() []adminRoute {
	var routes []adminRoute
	add := func(action, pattern string, fn http.HandlerFunc) {
		routes = append(
			routes,
			adminRoute{
				Pattern:       pattern,
				Action:        action,
				Family:        "principals",
				LocalMutation: true,
				Handler:       fn,
			},
		)
	}

	add(ActionReadPrincipals, "GET /principals", func(w http.ResponseWriter, r *http.Request) {
		res, err := a.admin.Principals(
			r.Context(),
			adminauth.Page{Cursor: r.URL.Query().Get("cursor")},
		)
		if err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		items := make([]principalView, 0, len(res.Items))
		for _, p := range res.Items {
			items = append(items, viewOf(p))
		}
		writeJSON(w, http.StatusOK, map[string]any{"Items": items, "NextCursor": res.NextCursor})
	})

	add(
		ActionReadPrincipals,
		"GET /principals/{name}",
		func(w http.ResponseWriter, r *http.Request) {
			p, err := a.admin.Principal(r.Context(), r.PathValue("name"))
			if err != nil {
				writeError(w, adminStatus(err), err)
				return
			}
			writeJSON(w, http.StatusOK, viewOf(p))
		},
	)

	add(ActionManagePrincipals, "POST /principals", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name      string
			Roles     []string
			Root      bool
			ExpiresAt time.Time
		}
		if !decodeAdmin(w, r, &body) {
			return
		}
		actor := a.actor(r)
		p, tok, err := a.admin.CreatePrincipal(
			r.Context(),
			actor,
			adminauth.Principal{
				Name:  body.Name,
				Roles: body.Roles,
				Root:  body.Root,
			},
			body.ExpiresAt,
		)
		if err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		// The only time the token is ever readable. It is not stored, and no
		// later route can return it.
		writeJSON(
			w,
			http.StatusCreated,
			map[string]any{"Principal": viewOf(p), "Token": string(tok)},
		)
	})

	add(
		ActionManagePrincipals,
		"PATCH /principals/{name}",
		func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Roles []string
				Root  bool
			}
			if !decodeAdmin(w, r, &body) {
				return
			}
			p, err := a.admin.UpdatePrincipal(
				r.Context(),
				a.actor(r),
				r.PathValue("name"),
				body.Roles,
				body.Root,
			)
			if err != nil {
				writeError(w, adminStatus(err), err)
				return
			}
			writeJSON(w, http.StatusOK, viewOf(p))
		},
	)

	add(
		ActionManagePrincipals,
		"DELETE /principals/{name}",
		func(w http.ResponseWriter, r *http.Request) {
			if err := a.admin.DeletePrincipal(
				r.Context(),
				a.actor(r),
				r.PathValue("name"),
			); err != nil {
				writeError(w, adminStatus(err), err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		},
	)

	add(
		ActionManagePrincipals,
		"POST /principals/{name}/rotate",
		func(w http.ResponseWriter, r *http.Request) {
			var body struct{ ExpiresAt time.Time }
			if r.ContentLength > 0 && !decodeAdmin(w, r, &body) {
				return
			}
			p, tok, err := a.admin.Rotate(
				r.Context(),
				a.actor(r),
				r.PathValue("name"),
				body.ExpiresAt,
			)
			if err != nil {
				writeError(w, adminStatus(err), err)
				return
			}
			writeJSON(
				w,
				http.StatusOK,
				map[string]any{"Principal": viewOf(p), "Token": string(tok)},
			)
		},
	)

	add(
		ActionManagePrincipals,
		"POST /principals/{name}/revoke",
		func(w http.ResponseWriter, r *http.Request) {
			if err := a.admin.Revoke(r.Context(), a.actor(r), r.PathValue("name")); err != nil {
				writeError(w, adminStatus(err), err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		},
	)

	add(ActionReadPolicies, "GET /policies", func(w http.ResponseWriter, r *http.Request) {
		docs, err := a.admin.Policies(r.Context(), a.actor(r))
		if err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"Items": docs})
	})

	add(ActionReadPolicies, "GET /policies/{name}", func(w http.ResponseWriter, r *http.Request) {
		doc, err := a.admin.GetPolicy(r.Context(), a.actor(r), r.PathValue("name"))
		if err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, doc)
	})

	add(ActionManagePolicies, "PUT /policies/{name}", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Source, Description string
			Active              *bool
		}
		if !decodeAdmin(w, r, &body) {
			return
		}
		doc, err := a.admin.PutPolicy(r.Context(), a.actor(r), adminauth.Policy{
			Name: r.PathValue("name"), Source: body.Source, Description: body.Description, Active: body.Active,
		})
		if err != nil {
			writeError(w, adminStatus(err), err)
			return
		}
		writeJSON(w, http.StatusOK, doc)
	})

	add(
		ActionManagePolicies,
		"DELETE /policies/{name}",
		func(w http.ResponseWriter, r *http.Request) {
			if err := a.admin.DeletePolicy(
				r.Context(),
				a.actor(r),
				r.PathValue("name"),
			); err != nil {
				writeError(w, adminStatus(err), err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		},
	)

	// The action catalogue an operator writes policies against, with the
	// prose that says what granting each one means.
	routes = append(routes, adminRoute{Pattern: "GET /actions", Action: ActionReadConfig, Family: "authorization", Introspection: true, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"Items": AdminActions()})
	})})

	return routes
}

// principalView is what a principal looks like over the wire. There is no
// token and no digest: the value is readable once, at creation and rotation,
// and nothing can read it back.
type principalView struct {
	Name      string
	Roles     []string
	Root      bool
	TokenID   string
	HasToken  bool
	TokenAt   time.Time
	ExpiresAt time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// viewOf projects a principal's roles and credential metadata without its stored token
// digest.
func viewOf(p adminauth.Principal) principalView {
	return principalView{
		Name: p.Name, Roles: p.Roles, Root: p.Root,
		TokenID: p.TokenID, HasToken: p.TokenID != "",
		TokenAt: p.TokenAt, ExpiresAt: p.ExpiresAt,
		CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// actor is the principal making the request, resolved again from the header.
// The authorization wrapper has already accepted it.
func (a *App) actor(r *http.Request) adminauth.Principal {
	if p, ok := r.Context().Value(actorKey{}).(adminauth.Principal); ok {
		return p
	}
	p, err := a.principal(r)
	if err != nil {
		return adminauth.Principal{}
	}
	return p
}

// decodeAdmin reads a bounded JSON body, reporting whether the handler should
// continue.
func decodeAdmin(w http.ResponseWriter, r *http.Request, v any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
	if err != nil || len(body) > MaxAdminBody {
		writeError(w, http.StatusRequestEntityTooLarge, ErrBodyTooLarge)
		return false
	}
	if err := json.Unmarshal(body, v); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

// adminStatus maps an adminauth error to a status code.
func adminStatus(err error) int {
	switch {
	case errors.Is(err, adminauth.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, adminauth.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, adminauth.ErrInvalid), errors.Is(err, adminauth.ErrUnknownAction):
		return http.StatusBadRequest
	case errors.Is(err, adminauth.ErrDenied),
		errors.Is(err, adminauth.ErrEscalation),
		errors.Is(err, adminauth.ErrLastRoot):
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}
