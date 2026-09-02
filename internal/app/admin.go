package app

import (
	"context"
	"crypto/subtle"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/deploymenttheory/go-apple-mdm/ddm"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/storage"
)

// MaxAdminBody bounds admin request bodies.
const MaxAdminBody = 1 << 20

// Admin API errors.
var (
	ErrBodyTooLarge = errors.New("app: body too large")
	ErrUnauthorized = errors.New("app: unauthorized")
	ErrBadChannel   = errors.New("app: channel must be device or user")
)

// adminHandler is the minimal JSON admin API of the ddm role (decision
// record 0025): bearer token, declarations, sets, assignments, status,
// and a manual notifier drain. Phase 8 replaces it.
//
//	PUT    /declarations                                   body: declaration JSON
//	GET    /declarations/{id}
//	DELETE /declarations/{id}
//	PUT    /sets/{set}/declarations/{id}
//	DELETE /sets/{set}/declarations/{id}
//	PUT    /enrollments/{channel}/{id}/sets/{set}           channel: device or user
//	DELETE /enrollments/{channel}/{id}/sets/{set}
//	GET    /enrollments/{channel}/{id}/declarations
//	GET    /enrollments/{channel}/{id}/status
//	GET    /enrollments/{channel}/{id}/status/values
//	GET    /enrollments/{channel}/{id}/tokens
//	POST   /notify
func (a *App) adminHandler() http.Handler {
	mux := http.NewServeMux()
	e := a.Engine
	mux.HandleFunc("PUT /declarations", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
		if err != nil || len(body) > MaxAdminBody {
			writeError(w, http.StatusRequestEntityTooLarge, ErrBodyTooLarge)
			return
		}
		d, changed, err := e.PutDeclaration(r.Context(), body)
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(
			w,
			http.StatusOK,
			map[string]any{
				"Identifier":  d.Identifier,
				"Type":        d.Type,
				"ServerToken": d.ServerToken,
				"Changed":     changed,
			},
		)
	})
	mux.HandleFunc("GET /declarations/{id}", func(w http.ResponseWriter, r *http.Request) {
		d, err := e.GetDeclaration(r.Context(), r.PathValue("id"))
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(
			d.Canonical,
		) // #nosec G705 -- canonical JSON produced by the engine, served as JSON with nosniff
	})
	mux.HandleFunc("DELETE /declarations/{id}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, e.DeleteDeclaration(r.Context(), r.PathValue("id")))
	})
	mux.HandleFunc(
		"PUT /sets/{set}/declarations/{id}",
		func(w http.ResponseWriter, r *http.Request) {
			set := r.PathValue("set")
			if _, err := e.PutSet(r.Context(), set); err != nil {
				writeError(w, statusFor(err), err)
				return
			}
			changed, err := e.AddToSet(r.Context(), set, r.PathValue("id"))
			respondChanged(w, changed, err)
		},
	)
	mux.HandleFunc(
		"DELETE /sets/{set}/declarations/{id}",
		func(w http.ResponseWriter, r *http.Request) {
			changed, err := e.RemoveFromSet(r.Context(), r.PathValue("set"), r.PathValue("id"))
			respondChanged(w, changed, err)
		},
	)
	mux.HandleFunc(
		"PUT /enrollments/{channel}/{id}/sets/{set}",
		func(w http.ResponseWriter, r *http.Request) {
			id, err := enrollmentFromPath(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			changed, err := e.AssignSet(r.Context(), id, r.PathValue("set"))
			respondChanged(w, changed, err)
		},
	)
	mux.HandleFunc(
		"DELETE /enrollments/{channel}/{id}/sets/{set}",
		func(w http.ResponseWriter, r *http.Request) {
			id, err := enrollmentFromPath(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			changed, err := e.UnassignSet(r.Context(), id, r.PathValue("set"))
			respondChanged(w, changed, err)
		},
	)
	enrollmentGet := func(pattern string, fn func(context.Context, mdm.EnrollmentID) (any, error)) {
		mux.HandleFunc(
			"GET /enrollments/{channel}/{id}/"+pattern,
			func(w http.ResponseWriter, r *http.Request) {
				id, err := enrollmentFromPath(r)
				if err != nil {
					writeError(w, http.StatusBadRequest, err)
					return
				}
				v, err := fn(r.Context(), id)
				if err != nil {
					writeError(w, statusFor(err), err)
					return
				}
				writeJSON(w, http.StatusOK, v)
			},
		)
	}
	enrollmentGet(
		"declarations",
		func(ctx context.Context, id mdm.EnrollmentID) (any, error) {
			// The effective static list: direct assignments and set members.
			decls, err := e.Store().StaticDeclarations(ctx, id)
			if err != nil {
				return nil, fmt.Errorf("app: declarations: %w", err)
			}
			ids := make([]string, 0, len(decls))
			for _, d := range decls {
				ids = append(ids, d.Identifier)
			}
			return ids, nil
		},
	)
	enrollmentGet(
		"status",
		func(ctx context.Context, id mdm.EnrollmentID) (any, error) { return e.DeclarationStatus(ctx, id) },
	)
	enrollmentGet("status/values", func(ctx context.Context, id mdm.EnrollmentID) (any, error) {
		return e.StatusValues(ctx, id, ddm.StatusValueQuery{}, storage.Page{Limit: 1000})
	})
	enrollmentGet("tokens", func(ctx context.Context, id mdm.EnrollmentID) (any, error) {
		body, err := e.Tokens(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("app: tokens: %w", err)
		}
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			return nil, fmt.Errorf("app: tokens: %w", err)
		}
		return v, nil
	})
	mux.HandleFunc("POST /notify", func(w http.ResponseWriter, r *http.Request) {
		res, err := a.Notifier.DrainOnce(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	return a.requireToken(mux)
}

// requireToken checks "Authorization: Bearer <AdminToken>" in constant time.
func (a *App) requireToken(next http.Handler) http.Handler {
	want := []byte(a.cfg.AdminToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="mdm-admin"`)
			writeError(w, http.StatusUnauthorized, ErrUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func enrollmentFromPath(r *http.Request) (mdm.EnrollmentID, error) {
	id := mdm.EnrollmentID{ID: r.PathValue("id")}
	switch r.PathValue("channel") {
	case "device":
		id.Channel = mdm.ChannelDevice
	case "user":
		id.Channel = mdm.ChannelUser
		id.ParentID = r.URL.Query().Get("parent")
	default:
		return mdm.EnrollmentID{}, fmt.Errorf("%w: %q", ErrBadChannel, r.PathValue("channel"))
	}
	if err := id.Validate(); err != nil {
		return mdm.EnrollmentID{}, fmt.Errorf("app: enrollment: %w", err)
	}
	return id, nil
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, ddm.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ddm.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, ddm.ErrInvalid),
		errors.Is(err, ddm.ErrInvalidDeclaration),
		errors.Is(err, ddm.ErrUnknownType):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func respond(w http.ResponseWriter, err error) {
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func respondChanged(w http.ResponseWriter, changed bool, err error) {
	if err != nil {
		writeError(w, statusFor(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"Changed": changed})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.MarshalWrite(w, v); err != nil {
		_, _ = fmt.Fprintf(
			w,
			`{"Error":%q}`,
			err.Error(),
		) // #nosec G705 -- a JSON encoder error message, quoted, not request input
	}
}

func writeError(w http.ResponseWriter, status int, err error) {
	msg := err.Error()
	if status == http.StatusInternalServerError {
		msg = "internal error"
	}
	writeJSON(w, status, map[string]string{"Error": msg})
}
