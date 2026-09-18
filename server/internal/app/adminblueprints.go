package app

import (
	json "encoding/json/v2"
	"io"
	"net/http"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

const (
	ActionReadBlueprints    = "readBlueprints"
	ActionPublishBlueprints = "publishBlueprints"
	ActionAssignBlueprint   = "assignBlueprint"
)

func blueprintActions() []adminauth.Action {
	return []adminauth.Action{
		{ID: ActionReadBlueprints, Resource: adminauth.EntityBlueprint, Help: "Read Blueprint source, publication revisions and compilation results."},
		{ID: ActionPublishBlueprints, Resource: adminauth.EntityBlueprint, Help: "Validate, publish or delete Blueprints and their complete declaration sets."},
		{ID: ActionAssignBlueprint, Resource: adminauth.EntityEnrollment, Help: "Assign or unassign a Blueprint to a device or user enrollment."},
	}
}

func expectedRevision(r *http.Request) string { return strings.Trim(r.Header.Get("If-Match"), `"`) }

func (a *App) blueprintAdminRoutes() []adminRoute {
	if a.Blueprints == nil {
		return nil
	}
	var routes []adminRoute
	add := func(action, pattern string, fn http.HandlerFunc) {
		routes = append(routes, adminRoute{Pattern: pattern, Action: action, Family: "ddm", LocalMutation: true, Handler: fn})
	}
	decode := func(w http.ResponseWriter, r *http.Request) (blueprint.Spec, bool) {
		var spec blueprint.Spec
		b, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
		if err != nil || len(b) > MaxAdminBody {
			writeError(w, http.StatusRequestEntityTooLarge, ErrBodyTooLarge)
			return spec, false
		}
		if err := json.Unmarshal(b, &spec, json.RejectUnknownMembers(true)); err != nil {
			writeError(w, http.StatusBadRequest, ddm.ErrInvalid)
			return spec, false
		}
		return spec, true
	}
	add(ActionPublishBlueprints, "POST /blueprints/validate", func(w http.ResponseWriter, r *http.Request) {
		spec, ok := decode(w, r)
		if !ok {
			return
		}
		v, err := a.Blueprints.Validate(r.Context(), spec, support.Target{})
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	add(ActionPublishBlueprints, "PUT /blueprints/{blueprint}", func(w http.ResponseWriter, r *http.Request) {
		spec, ok := decode(w, r)
		if !ok {
			return
		}
		if spec.Identifier != r.PathValue("blueprint") {
			writeError(w, http.StatusBadRequest, ddm.ErrInvalid)
			return
		}
		v, err := a.Blueprints.Publish(r.Context(), spec, expectedRevision(r))
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		w.Header().Set("ETag", `"`+v.Revision+`"`)
		writeJSON(w, http.StatusOK, v)
	})
	add(ActionReadBlueprints, "GET /blueprints", func(w http.ResponseWriter, r *http.Request) {
		p, err := page(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		v, err := a.Blueprints.List(r.Context(), p)
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
	add(ActionReadBlueprints, "GET /blueprints/{blueprint}", func(w http.ResponseWriter, r *http.Request) {
		v, err := a.Blueprints.Get(r.Context(), r.PathValue("blueprint"))
		if err != nil {
			writeError(w, statusFor(err), err)
			return
		}
		w.Header().Set("ETag", `"`+v.Revision+`"`)
		writeJSON(w, http.StatusOK, v)
	})
	add(ActionPublishBlueprints, "DELETE /blueprints/{blueprint}", func(w http.ResponseWriter, r *http.Request) {
		respond(w, a.Blueprints.Delete(r.Context(), r.PathValue("blueprint"), expectedRevision(r)))
	})
	for _, method := range []string{http.MethodPut, http.MethodDelete} {
		add(ActionAssignBlueprint, method+" /enrollments/{channel}/{id}/blueprints/{blueprint}", func(w http.ResponseWriter, r *http.Request) {
			id, err := enrollmentFromPath(r)
			if err != nil {
				writeError(w, http.StatusBadRequest, err)
				return
			}
			changed, err := a.Blueprints.Assign(r.Context(), id, r.PathValue("blueprint"), r.Method == http.MethodPut)
			respondChanged(w, changed, err)
		})
	}
	return routes
}
