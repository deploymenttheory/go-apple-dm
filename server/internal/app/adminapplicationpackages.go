package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

const (
	ActionReadApplicationPackages    = "readApplicationPackages"
	ActionManageApplicationPackages  = "manageApplicationPackages"
	ActionUploadApplicationPackage   = "uploadApplicationPackage"
	ActionImportApplicationPackage   = "importApplicationPackage"
	ActionDownloadApplicationPackage = "downloadApplicationPackage"
	ActionDeliverApplicationPackage  = "deliverApplicationPackage"
)

// applicationPackageActions makes package privileges explicit in the permission catalogue.
func applicationPackageActions() []adminauth.Action {
	return []adminauth.Action{
		{ID: ActionReadApplicationPackages, Help: "Read package metadata, manifests and history, including license metadata.", Resource: adminauth.EntitySystem},
		{ID: ActionManageApplicationPackages, Help: "Create, update and delete package metadata and manifests.", Resource: adminauth.EntitySystem},
		{ID: ActionUploadApplicationPackage, Help: "Upload and verify installer bytes into configured storage.", Resource: adminauth.EntitySystem},
		{ID: ActionImportApplicationPackage, Help: "Import an installer from configured filesystem or HTTPS sources.", Resource: adminauth.EntitySystem},
		{ID: ActionDownloadApplicationPackage, Help: "Download verified installer content.", Resource: adminauth.EntitySystem},
		{ID: ActionDeliverApplicationPackage, Help: "Queue package delivery and revoke download grants for an enrollment.", Resource: adminauth.EntityEnrollment},
	}
}

// packageError preserves actionable validation errors while mapping storage failures.
func packageError(w http.ResponseWriter, err error) {
	code := http.StatusInternalServerError
	switch {
	case errors.Is(err, applications.ErrNotFound):
		code = http.StatusNotFound
	case errors.Is(err, applications.ErrConflict):
		code = http.StatusConflict
	case errors.Is(err, applications.ErrTooLarge):
		code = http.StatusRequestEntityTooLarge
	case errors.Is(err, applications.ErrInvalid), errors.Is(err, applications.ErrIntegrity), errors.Is(err, applications.ErrUnsupported), errors.Is(err, applications.ErrIneligible):
		code = http.StatusBadRequest
	}
	writeError(w, code, err)
}

// packageJSON decodes one bounded object and rejects unknown fields and trailing data.
func packageJSON(r *http.Request, dst any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
	if err != nil {
		return err
	}
	if len(data) > MaxAdminBody {
		return applications.ErrTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("%w: package request: %w", applications.ErrInvalid, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: one JSON object required", applications.ErrInvalid)
	}
	return nil
}

// writePackage emits the optimistic concurrency token with the complete stored record.
func writePackage(w http.ResponseWriter, status int, r applications.Record) {
	w.Header().Set("ETag", `"`+r.Revision+`"`)
	writeJSON(w, status, r)
}

// applicationPackageAdminRoutes exposes library operations without buffering package
// uploads or putting remote storage calls inside a local database transaction.
func (a *App) applicationPackageAdminRoutes() []adminRoute {
	if a.ApplicationPackages == nil {
		return nil
	}
	var routes []adminRoute
	add := func(action, pattern string, local bool, handler http.HandlerFunc) {
		routes = append(routes, adminRoute{Action: action, Pattern: pattern, Family: "applications", LocalMutation: local, Sensitive: true, MaxResponseBytes: 64 << 20, Handler: handler})
	}
	base := "/application-packages"
	add(ActionManageApplicationPackages, "POST "+base, true, func(w http.ResponseWriter, r *http.Request) {
		metadata, err := applications.DecodeMetadata(r.Body)
		if err != nil {
			packageError(w, err)
			return
		}
		result, err := a.ApplicationPackages.Create(r.Context(), metadata)
		if err != nil {
			packageError(w, err)
			return
		}
		writePackage(w, http.StatusCreated, result)
	})
	add(ActionReadApplicationPackages, "GET "+base, false, func(w http.ResponseWriter, r *http.Request) {
		p, err := page(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		p.Limit = min(p.Size(), 20)
		result, err := a.ApplicationPackages.List(r.Context(), p)
		if err != nil {
			packageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	add(ActionReadApplicationPackages, "GET "+base+"/export", false, func(w http.ResponseWriter, r *http.Request) {
		p, err := page(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		p.Limit = min(p.Size(), 20)
		result, err := a.ApplicationPackages.List(r.Context(), p)
		if err != nil {
			packageError(w, err)
			return
		}
		w.Header().Set("X-Next-Cursor", result.NextCursor)
		if r.URL.Query().Get("format") == "csv" {
			fields := []string{}
			if raw := r.URL.Query().Get("fields"); raw != "" {
				fields = strings.Split(raw, ",")
			}
			var b strings.Builder
			if err = applications.ExportCSV(&b, result.Items, fields); err != nil {
				packageError(w, err)
				return
			}
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			_, _ = io.WriteString(w, b.String())
		} else {
			w.Header().Set("Content-Type", "application/json")
			_ = applications.ExportJSON(w, result.Items)
		}
	})
	add(ActionManageApplicationPackages, "POST "+base+"/delete-multiple", false, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Packages []applications.DeleteRequest `json:"packages"`
		}
		if err := packageJSON(r, &request); err != nil {
			packageError(w, err)
			return
		}
		result, err := a.ApplicationPackages.DeleteMany(r.Context(), request.Packages)
		if err != nil {
			packageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	path := base + "/{package}"
	add(ActionReadApplicationPackages, "GET "+path, false, func(w http.ResponseWriter, r *http.Request) {
		result, err := a.ApplicationPackages.Get(r.Context(), r.PathValue("package"))
		if err != nil {
			packageError(w, err)
			return
		}
		writePackage(w, http.StatusOK, result)
	})
	add(ActionManageApplicationPackages, "PUT "+path, true, func(w http.ResponseWriter, r *http.Request) {
		metadata, err := applications.DecodeMetadata(r.Body)
		if err != nil {
			packageError(w, err)
			return
		}
		result, err := a.ApplicationPackages.Update(r.Context(), r.PathValue("package"), expectedRevision(r), metadata)
		if err != nil {
			packageError(w, err)
			return
		}
		writePackage(w, http.StatusOK, result)
	})
	add(ActionManageApplicationPackages, "DELETE "+path, false, func(w http.ResponseWriter, r *http.Request) {
		if err := a.ApplicationPackages.Delete(r.Context(), r.PathValue("package"), expectedRevision(r)); err != nil {
			packageError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	add(ActionUploadApplicationPackage, "POST "+path+"/upload", false, func(w http.ResponseWriter, r *http.Request) {
		result, err := a.ApplicationPackages.Upload(r.Context(), r.PathValue("package"), expectedRevision(r), r.URL.Query().Get("backend"), applications.Source{Kind: "upload"}, packageUploadBody{r.Body}, r.URL.Query().Get("sha256"))
		if err != nil {
			packageError(w, err)
			return
		}
		writePackage(w, http.StatusOK, result)
	})
	routes[len(routes)-1].Handler = packageTransfer(routes[len(routes)-1].Handler, true, packageTransferTimeout)
	add(ActionImportApplicationPackage, "POST "+path+"/source", false, a.importApplicationPackage)
	routes[len(routes)-1].Handler = packageTransfer(routes[len(routes)-1].Handler, false, packageTransferTimeout)
	add(ActionManageApplicationPackages, "POST "+path+"/manifest", true, func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
		if err != nil {
			packageError(w, err)
			return
		}
		result, err := a.ApplicationPackages.AssignManifest(r.Context(), r.PathValue("package"), expectedRevision(r), r.URL.Query().Get("filename"), b)
		if err != nil {
			packageError(w, err)
			return
		}
		writePackage(w, http.StatusOK, result)
	})
	add(ActionManageApplicationPackages, "DELETE "+path+"/manifest", true, func(w http.ResponseWriter, r *http.Request) {
		result, err := a.ApplicationPackages.DeleteManifest(r.Context(), r.PathValue("package"), expectedRevision(r))
		if err != nil {
			packageError(w, err)
			return
		}
		writePackage(w, http.StatusOK, result)
	})
	add(ActionReadApplicationPackages, "GET "+path+"/history", false, func(w http.ResponseWriter, r *http.Request) {
		p, err := page(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		result, err := a.ApplicationPackages.History(r.Context(), r.PathValue("package"), p)
		if err != nil {
			packageError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})
	add(ActionManageApplicationPackages, "POST "+path+"/history", true, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Note string `json:"note"`
		}
		if err := packageJSON(r, &body); err != nil {
			packageError(w, err)
			return
		}
		result, err := a.ApplicationPackages.AddHistoryNote(r.Context(), r.PathValue("package"), expectedRevision(r), body.Note)
		if err != nil {
			packageError(w, err)
			return
		}
		writePackage(w, http.StatusOK, result)
	})
	add(ActionReadApplicationPackages, "GET "+path+"/history/export", false, func(w http.ResponseWriter, r *http.Request) {
		p, err := page(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		result, err := a.ApplicationPackages.History(r.Context(), r.PathValue("package"), p)
		if err != nil {
			packageError(w, err)
			return
		}
		w.Header().Set("X-Next-Cursor", result.NextCursor)
		if r.URL.Query().Get("format") == "csv" {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")
			_ = applications.ExportHistoryCSV(w, result.Items)
		} else {
			w.Header().Set("Content-Type", "application/json")
			_ = applications.ExportHistoryJSON(w, result.Items)
		}
	})
	add(ActionDownloadApplicationPackage, "GET "+path+"/revisions/{revision}/content", false, a.downloadAdminPackage)
	routes[len(routes)-1].StreamResponse = true
	routes[len(routes)-1].Handler = packageTransfer(routes[len(routes)-1].Handler, false, packageTransferTimeout)
	delivery := "/enrollments/{channel}/{id}/application-packages"
	add(ActionDeliverApplicationPackage, "POST "+delivery, true, a.deliverApplicationPackage)
	routes[len(routes)-1].NotifyDeclarations = true
	add(ActionDeliverApplicationPackage, "DELETE "+delivery+"/grants/{grant}", true, func(w http.ResponseWriter, r *http.Request) {
		id, err := enrollmentFromPath(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		if err = a.packageHost.Revoke(r.Context(), id, r.PathValue("grant")); err != nil {
			packageError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	return routes
}

// importApplicationPackage ingests configured local or HTTPS sources through the library.
func (a *App) importApplicationPackage(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind     string `json:"kind"`
		Location string `json:"location"`
		Backend  string `json:"backend"`
	}
	if err := packageJSON(r, &body); err != nil {
		packageError(w, err)
		return
	}
	var result applications.Record
	var err error
	switch body.Kind {
	case "file":
		if a.packageImports == nil {
			packageError(w, applications.ErrInvalid)
			return
		}
		result, err = a.packageImports.Import(r.Context(), a.ApplicationPackages, r.PathValue("package"), expectedRevision(r), body.Backend, body.Location)
	case "https":
		result, err = a.packageHTTPS.Import(r.Context(), a.ApplicationPackages, r.PathValue("package"), expectedRevision(r), body.Backend, body.Location)
	default:
		err = applications.ErrInvalid
	}
	if err != nil {
		packageError(w, err)
		return
	}
	writePackage(w, http.StatusOK, result)
}

// deliverApplicationPackage prepares and queues native delivery after enrollment.
// The transaction boundary includes the grant and protocol state in SQL deployments.
func (a *App) deliverApplicationPackage(w http.ResponseWriter, r *http.Request) {
	id, err := enrollmentFromPath(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var body struct {
		PackageID       string `json:"packageId"`
		ContentRevision string `json:"contentRevision"`
		Method          string `json:"method"`
		TTLSeconds      int64  `json:"ttlSeconds"`
	}
	if err = packageJSON(r, &body); err != nil {
		packageError(w, err)
		return
	}
	if body.TTLSeconds < 0 || body.TTLSeconds > 7*24*3600 {
		packageError(w, applications.ErrInvalid)
		return
	}
	plan, err := a.packageHost.Prepare(r.Context(), id, body.PackageID, body.ContentRevision, body.Method, time.Duration(body.TTLSeconds)*time.Second)
	if err != nil {
		packageError(w, err)
		return
	}
	success := false
	defer func() {
		if !success {
			_ = a.packageHost.Revoke(r.Context(), id, plan.Grant.ID)
		}
	}()
	result := map[string]any{"Grant": plan.Grant, "Status": "queued"}
	if plan.Command != nil {
		queued, err := a.Core.Enqueue(r.Context(), []mdm.EnrollmentID{id}, plan.Command, storage.EnqueueOptions{})
		if err != nil {
			packageError(w, err)
			return
		}
		if len(queued.Queued) != 1 {
			packageError(w, fmt.Errorf("%w: %v", applications.ErrIneligible, queued.Skipped[id]))
			return
		}
		result["CommandUUID"] = plan.Command.UUID
	} else {
		if _, err = a.Engine.PublishSet(r.Context(), plan.Publication); err != nil {
			packageError(w, err)
			return
		}
		if _, err = a.Engine.AssignSet(r.Context(), id, plan.Publication.Name); err != nil {
			packageError(w, err)
			return
		}
		result["Set"] = plan.Publication.Name
	}
	success = true
	writeJSON(w, http.StatusAccepted, result)
}

// packageUploadBody classifies malformed or interrupted HTTP bodies as invalid
// client input while preserving separate storage and verification failures.
type packageUploadBody struct{ io.Reader }

// Read preserves clean EOF and marks transport read failures as invalid uploads.
func (r packageUploadBody) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		return n, fmt.Errorf("%w: interrupted upload: %w", applications.ErrInvalid, err)
	}
	return n, err
}
