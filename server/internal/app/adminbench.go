package app

import (
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/paging"
	"github.com/deploymenttheory/go-apple-dm/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

// getCommandResult exposes one enrollment-scoped response, without queue secrets.
func (a *App) getCommandResult(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		a.storageStatus(w, r, err)
		return
	}
	p := paging.Page{Limit: 100}
	for {
		rows, err := a.Store.Commands(r.Context(), id, storage.CommandQuery{}, p)
		if err != nil {
			a.storageStatus(w, r, err)
			return
		}
		for _, row := range rows.Items {
			if row.Command.UUID != r.PathValue("uuid") {
				continue
			}
			if row.Result == nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(
				w,
				http.StatusOK,
				map[string]any{
					"CommandUUID": row.Command.UUID,
					"Status":      row.Result.Status,
					"Response":    row.Result.Raw,
					"ErrorChain":  row.Result.ErrorChain,
				},
			)
			return
		}
		if rows.NextCursor == "" {
			a.storageStatus(w, r, storage.ErrNotFound)
			return
		}
		p.Cursor = rows.NextCursor
	}
}

func (a *App) enrollmentAdminRoutes() []adminRoute {
	if a.enroll == nil {
		return nil
	}
	return []adminRoute{
		{
			Pattern: "POST /enrollment-profiles",
			Action:  ActionIssueEnrollmentProfile,
			Family:  "enrollment",
			Handler: http.HandlerFunc(a.issueEnrollmentProfile),
		},
	}
}

func (a *App) issueEnrollmentProfile(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID, Serial string
		AccessRights     enroll.AccessRights
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
	if err != nil || len(body) > MaxAdminBody {
		writeError(w, 413, ErrBodyTooLarge)
		return
	}
	if json.Unmarshal(body, &req) != nil || req.DeviceID == "" {
		writeError(w, 400, fmt.Errorf("%w: DeviceID is required", errOperation))
		return
	}
	p, err := a.enroll.profile(
		acme.Binding{UDID: req.DeviceID, Serial: req.Serial, CommonName: req.DeviceID},
	)
	if err != nil {
		writeError(w, 400, err)
		return
	}
	p.CheckOutWhenRemoved = true
	p.AccessRights = req.AccessRights
	if p.AccessRights == 0 {
		p.AccessRights = enroll.RightQueryDeviceInfo
	}
	b, err := p.Marshal()
	if err != nil {
		writeError(w, 500, fmt.Errorf("%w: profile generation failed", errOperation))
		return
	}
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}
