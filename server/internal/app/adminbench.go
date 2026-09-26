package app

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
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

// enrollmentAdminRoutes declares profile-issuance, enrollment-link, replacement and evidence
// routes when enrollment is configured.
func (a *App) enrollmentAdminRoutes() []adminRoute {
	if a.enroll == nil {
		return nil
	}
	return append([]adminRoute{
		{
			Pattern: "GET /enrollments/{channel}/{id}/enrollment-evidence",
			Action:  ActionReadEnrollment,
			Family:  "mdm",
			Handler: http.HandlerFunc(a.enrollmentEvidence),
		},
		{
			Pattern: "POST /enrollments/{channel}/{id}/replacement",
			Action:  ActionReplaceEnrollment,
			Family:  "mdm",
			Handler: http.HandlerFunc(a.replaceEnrollment),
		},
		{
			Pattern: "GET /enrollments/{channel}/{id}/replacement",
			Action:  ActionReadEnrollment,
			Family:  "mdm",
			Handler: http.HandlerFunc(a.replaceEnrollment),
		},
		{
			Pattern: "DELETE /enrollments/{channel}/{id}/replacement/{attempt}",
			Action:  ActionReplaceEnrollment,
			Family:  "mdm",
			Handler: http.HandlerFunc(a.replaceEnrollment),
		},
		{
			Pattern: "POST /enrollment-profiles",
			Action:  ActionIssueEnrollmentProfile,
			Family:  "enrollment",
			Handler: http.HandlerFunc(a.issueEnrollmentProfile),
		},
	}, a.enrollmentLinkRoutes()...)
}

// issueEnrollmentProfile validates a bounded issuance request and returns an enrollment
// profile with caching disabled.
func (a *App) issueEnrollmentProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	var req EnrollmentProfileRequest
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
	if err != nil || len(body) > MaxAdminBody {
		writeError(w, 413, ErrBodyTooLarge)
		return
	}
	if json.Unmarshal(body, &req) != nil || req.DeviceID == "" {
		writeError(w, 400, fmt.Errorf("%w: DeviceID is required", errOperation))
		return
	}
	b, err := a.ExportEnrollmentProfile(r.Context(), req)
	if err != nil {
		status := 400
		if errors.Is(err, errProfileExport) {
			status = 500
		}
		writeError(w, status, err)
		return
	}

	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(b)
}

var errProfileExport = errors.New("enrollment profile export failed")

// EnrollmentProfileRequest selects the device and enrollment identity method.
//
// Preserve the existing enrollment profile API field names.
type EnrollmentProfileRequest struct {
	DeviceID     string              `json:"DeviceID"`
	Serial       string              `json:"Serial"`
	Product      string              `json:"Product"`
	OSVersion    string              `json:"OSVersion"`
	MacHardware  enroll.MacHardware  `json:"MacHardware"`
	Identity     string              `json:"Identity"`
	AccessRights enroll.AccessRights `json:"AccessRights"`
	Scope        string              `json:"Scope"`
}

// ExportEnrollmentProfile issues and records the same profile used by the API.
func (a *App) ExportEnrollmentProfile(
	ctx context.Context,
	req EnrollmentProfileRequest,
) ([]byte, error) {
	if a.enroll == nil || req.DeviceID == "" {
		return nil, fmt.Errorf(
			"%w: enrollment must be configured and DeviceID supplied",
			errOperation,
		)
	}
	if req.Identity == "" {
		req.Identity = a.enroll.cfg.Identity
	}
	if req.Scope != "" && req.Scope != profile.ScopeSystem && req.Scope != profile.ScopeUser {
		return nil, fmt.Errorf("%w: Scope must be System or User", errOperation)
	}
	p, err := a.enroll.profileForDevice(
		ctx,
		acme.Binding{MDMUDID: req.DeviceID, Serial: req.Serial, CommonName: req.DeviceID},
		req.Identity, req.Product, req.OSVersion, req.MacHardware,
	)
	if err != nil {
		return nil, err
	}
	p.CheckOutWhenRemoved = true
	p.Scope = req.Scope
	if p.Scope == "" && p.Target.OS == support.OS("macOS") {
		p.Scope = profile.ScopeUser
	}
	p.AccessRights = req.AccessRights
	if p.AccessRights == 0 {
		p.AccessRights = enroll.RightQueryDeviceInfo
	}
	b, err := p.Marshal()
	if err != nil {
		return nil, fmt.Errorf("%w: profile generation failed", errProfileExport)
	}
	if err := a.enroll.recordProfile(
		ctx,
		acme.Binding{MDMUDID: req.DeviceID},
		p,
	); err != nil {
		return nil, fmt.Errorf("%w: %w", errProfileExport, err)
	}
	return b, nil
}
