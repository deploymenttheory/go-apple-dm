package app

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/axm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
)

// assignmentRoutes exposes durable explicit requests under named account authority.
// Configuration and request writes participate in the existing local audit transaction.
func (a *App) assignmentRoutes(add func(string, string, func(http.ResponseWriter, *http.Request) (any, error))) {
	add("PUT /axm/accounts/{id}/assignment-service", ActionManageAxMAccounts, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		var input struct {
			Revision int64                  `json:"account_revision"`
			Service  string                 `json:"service_id"`
			Columns  axm.ActivityLogColumns `json:"log_columns"`
		}
		if e := assignmentBody(r, &input); e != nil {
			return nil, e
		}
		return a.Assignments.Configure(r.Context(), r.PathValue("id"), input.Revision, input.Service, input.Columns)
	})
	add("GET /axm/accounts/{id}/assignment-service", ActionReadBusinessMgr, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		return a.Assignments.Binding(r.Context(), r.PathValue("id"))
	})
	for _, op := range []struct{ path, action string }{{"assignments", ActionAssignBusinessMgr}, {"unassignments", ActionUnassignBusinessMgr}} {
		add("POST /axm/accounts/{id}/"+op.path, op.action, func(_ http.ResponseWriter, r *http.Request) (any, error) {
			var input struct {
				ID      string   `json:"request_id"`
				Serials []string `json:"serials"`
			}
			if e := assignmentBody(r, &input); e != nil {
				return nil, e
			}
			if op.path == "unassignments" {
				return a.Assignments.RequestUnassign(r.Context(), r.PathValue("id"), input.ID, input.Serials, time.Now().UTC())
			}
			return a.Assignments.Request(r.Context(), r.PathValue("id"), input.ID, input.Serials, time.Now().UTC())
		})
	}

	add("GET /axm/accounts/{id}/assignments/{request}", ActionReadBusinessMgr, func(_ http.ResponseWriter, r *http.Request) (any, error) {
		return a.Assignments.Get(r.Context(), r.PathValue("id"), r.PathValue("request"))
	})
	add("GET /axm/accounts/{id}/assignments/{request}/log", ActionReadRawInventory, func(w http.ResponseWriter, r *http.Request) (any, error) {
		evidence, e := a.Assignments.Evidence(r.Context(), r.PathValue("id"), r.PathValue("request"))
		if e != nil {
			return nil, e
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		_, e = w.Write(evidence.CSV)
		return nil, e
	})
}

// assignmentBody preserves the assignment API's explicit request-size error.
func assignmentBody(r *http.Request, out any) error {
	raw, e := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
	if e != nil {
		return inventory.ErrInvalid
	}
	if len(raw) > MaxAdminBody {
		return ErrBodyTooLarge
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	return inventoryBody(r, out)
}
