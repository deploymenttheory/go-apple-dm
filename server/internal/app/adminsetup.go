package app

import (
	"bytes"
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/server/adminauth"
)

type setupActorKey struct{}

const (
	ActionReadSetup     = "readCertificateSetup"
	ActionManageVendor  = "manageVendorSigning"
	ActionSignVendorCSR = "signCustomerPushRequests"
	ActionManageHTTPS   = "manageHTTPSCertificates"
	ActionManageIssuer  = "manageEnrollmentIssuers"
)

// SetupRequest is shared by the local CLI and authenticated setup API.
type SetupRequest struct {
	Force      bool                         `json:"force,omitempty"`
	PublicACME *lifecycle.PublicACMEOptions `json:"publicAcme,omitempty"`
	lifecycle.Request
	Device        string `json:"device,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Revision      string `json:"revision,omitempty"`
	Certificate   []byte `json:"certificate,omitempty"`
	Key           []byte `json:"key,omitempty"`
	CSR           []byte `json:"csr,omitempty"`
	SignedRequest []byte `json:"signedRequest,omitempty"`
	Vendor        string `json:"vendor,omitempty"`
	Artifact      string `json:"artifact,omitempty"`
	ValidityDays  int    `json:"validityDays,omitempty"`
}

// SetupResult exposes public metadata and explicitly requested public artifacts.
type SetupResult struct {
	History    []lifecycle.Activity  `json:"history,omitempty"`
	Rollover   *lifecycle.Rollover   `json:"rollover,omitempty"`
	Migrations []lifecycle.Migration `json:"migrations,omitempty"`
	NextCursor string                `json:"nextCursor,omitempty"`
	Identity   *lifecycle.Identity   `json:"identity,omitempty"`
	Data       []byte                `json:"data,omitempty"`
}

func setupActions() []adminauth.Action {
	return []adminauth.Action{
		{
			ID:       ActionSignVendorCSR,
			Help:     "Sign public customer CSRs without changing vendor identities.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionReadSetup,
			Help:     "Read certificate setup status and public artifacts.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionManageVendor,
			Help:     "Manage the vendor identity and sign public customer requests.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionManageHTTPS,
			Help:     "Create, import and activate server HTTPS identities.",
			Resource: adminauth.EntitySystem,
		},
		{
			ID:       ActionManageIssuer,
			Help:     "Manage enrollment issuers and coordinate device identity migration.",
			Resource: adminauth.EntitySystem,
		},
	}
}

func (a *App) setupRoutes() []adminRoute {
	if a.Certificates == nil {
		return nil
	}
	out := []adminRoute{
		{
			Pattern: "POST /setup/vendor/sign",
			Action:  ActionSignVendorCSR,
			Family:  "setup",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.SetPathValue("operation", "sign")
				a.setupOperation(w, r, lifecycle.Vendor)
			}),
		},
		{
			Pattern: "GET /setup/workflow/{id}/history",
			Action:  ActionReadSetup,
			Family:  "setup",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				entries, next, err := a.Certificates.History(
					r.Context(),
					r.PathValue("id"),
					r.URL.Query().Get("cursor"),
					100,
				)
				if err != nil {
					a.setupError(w, err)
					return
				}
				w.Header().Set("Cache-Control", "no-store")
				writeJSON(w, 200, SetupResult{History: entries, NextCursor: next})
			}),
		},
		{
			Pattern: "GET /setup/trust",
			Action:  ActionReadSetup,
			Family:  "setup",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, err := a.SetupTrustProfile(r.Context())
				if err != nil {
					a.setupError(w, err)
					return
				}
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Type", "application/x-apple-aspen-config")
				_, _ = w.Write(data)
			}),
		},
		{
			Pattern: "GET /setup",
			Action:  ActionReadSetup,
			Family:  "setup",
			Handler: http.HandlerFunc(a.setupStatus),
		},
		{
			Pattern: "GET /setup/workflow/{id}",
			Action:  ActionReadSetup,
			Family:  "setup",
			Handler: http.HandlerFunc(a.setupWorkflow),
		},
		{
			Pattern: "GET /setup/workflow/{id}/export",
			Action:  ActionReadSetup,
			Family:  "setup",
			Handler: http.HandlerFunc(a.setupExport),
		},
	}
	for kind, action := range map[string]string{"vendor": ActionManageVendor, "push": ActionManagePushCerts, "https": ActionManageHTTPS, "issuer": ActionManageIssuer} {
		out = append(
			out,
			adminRoute{
				Pattern: "POST /setup/" + kind + "/{operation}",
				Action:  action,
				Family:  "setup",
				Handler: http.HandlerFunc(
					func(w http.ResponseWriter, r *http.Request) { a.setupOperation(w, r, lifecycle.Kind(kind)) },
				),
			},
		)
	}
	return out
}

func (a *App) setupStatus(w http.ResponseWriter, r *http.Request) {
	status, err := a.CertificateSetupStatus(r.Context())
	if err != nil {
		a.setupError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, status)
}

func (a *App) setupWorkflow(w http.ResponseWriter, r *http.Request) {
	item, err := a.Certificates.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		a.setupError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, item)
}

func (a *App) setupExport(w http.ResponseWriter, r *http.Request) {
	b, err := a.Certificates.Export(
		r.Context(),
		r.PathValue("id"),
		r.URL.Query().Get("revision"),
		r.URL.Query().Get("artifact"),
	)
	if err != nil {
		a.setupError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="certificate-artifact.bin"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(
		b,
	) // #nosec G705 -- Public artifact download, served as an octet-stream attachment with nosniff.
}

func (a *App) setupOperation(w http.ResponseWriter, r *http.Request, kind lifecycle.Kind) {
	var req SetupRequest
	b, err := io.ReadAll(io.LimitReader(r.Body, MaxAdminBody+1))
	if err != nil || len(b) > MaxAdminBody {
		writeError(w, 413, ErrBodyTooLarge)
		return
	}
	if err = json.Unmarshal(b, &req); err != nil {
		writeError(w, 400, lifecycle.ErrInvalid)
		return
	}
	result, err := a.ExecuteSetup(r.Context(), kind, r.PathValue("operation"), req)
	if err != nil {
		a.setupError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, result)
}

func (a *App) setupError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lifecycle.ErrNotFound):
		writeError(w, 404, err)
	case errors.Is(err, lifecycle.ErrConflict):
		writeError(w, 409, err)
	case errors.Is(err, lifecycle.ErrInvalid):
		writeError(w, 400, err)
	default:
		writeError(w, 500, errOperation)
	}
}

// ExecuteSetup keeps CLI bootstrap and remote operations on the same workflow.
//
// Keep the ordered workflow transitions and their failure handling together.
func (a *App) ExecuteSetup(
	ctx context.Context,
	kind lifecycle.Kind,
	operation string,
	req SetupRequest,
) (SetupResult, error) {
	if a.Certificates == nil {
		return SetupResult{}, ErrConfig
	}
	if kind != lifecycle.Vendor && kind != lifecycle.Push && kind != lifecycle.HTTPS &&
		kind != lifecycle.Issuer {
		return SetupResult{}, lifecycle.ErrInvalid
	}
	if len(req.Key) > 0 && operation != "adopt" {
		return SetupResult{}, fmt.Errorf(
			"%w: key material is accepted only for adoption",
			lifecycle.ErrInvalid,
		)
	}
	actor, _ := ctx.Value(setupActorKey{}).(string)
	if actor == "" {
		actor = "local-bootstrap"
	}
	ctx = lifecycle.WithAudit(ctx, actor, string(kind)+"/"+operation)
	manager := *a.Certificates
	if caID := a.cfg.Setup.HTTPSCAID; caID != "" {
		material, e := manager.LoadMaterial(ctx, caID, "")
		if e == nil {
			pool, e := x509.SystemCertPool()
			if e != nil {
				return SetupResult{}, wrapError(e)
			}
			pool.AppendCertsFromPEM(material.Certificate)
			manager.Trust.HTTPSRoots = pool
		} else if !errors.Is(e, lifecycle.ErrNotFound) {
			return SetupResult{}, wrapError(e)
		}
	}
	config := a.cfg.Setup
	if (kind == lifecycle.Vendor && config.Role == "customer") ||
		((kind == lifecycle.Push || kind == lifecycle.Issuer) && config.Role == "vendor") {
		return SetupResult{}, fmt.Errorf(
			"%w: operation unavailable for deployment role",
			lifecycle.ErrInvalid,
		)
	}
	if req.ID == "" {
		switch kind {
		case lifecycle.Vendor:
			req.ID = config.VendorID
		case lifecycle.Push:
			req.ID = config.PushID
		case lifecycle.HTTPS:
			req.ID = config.HTTPSID
		case lifecycle.Issuer:
			req.ID = config.IssuerID
		}
	}
	req.Kind = kind
	existing, err := manager.Get(ctx, req.ID)
	if err == nil {
		if existing.Kind != kind {
			return SetupResult{}, lifecycle.ErrConflict
		}
		if req.Subject.CommonName == "" {
			req.Request = existing.Request
		}
	} else if !errors.Is(err, lifecycle.ErrNotFound) {
		return SetupResult{}, wrapError(err)
	}
	var item lifecycle.Identity
	switch operation {
	case "retry":
		if kind != lifecycle.Issuer {
			return SetupResult{}, lifecycle.ErrInvalid
		}
		return SetupResult{}, a.retryMigration(ctx, req.ID, req.Revision, req.Device)
	case "device-status":
		if kind != lifecycle.Issuer || req.Device == "" {
			return SetupResult{}, lifecycle.ErrInvalid
		}
		migration, e := a.deviceRenewalStatus(ctx, req.Device)
		return SetupResult{Migrations: []lifecycle.Migration{migration}}, wrapError(e)
	case "status":
		if err != nil {
			return SetupResult{}, wrapError(err)
		}
		if kind == lifecycle.Issuer {
			rev := req.Revision
			if rev == "" {
				rev = existing.Pending
				if rev == "" {
					rev = existing.Active
				}
			}
			job, e := a.Certificates.Rollover(ctx, req.ID, rev)
			if errors.Is(e, lifecycle.ErrNotFound) {
				return SetupResult{Identity: &existing}, nil
			}
			if e != nil {
				return SetupResult{}, wrapError(e)
			}
			limit := req.Limit
			if limit == 0 {
				limit = 100
			}
			migrations, next, e := a.Certificates.Migrations(ctx, req.ID, rev, req.Cursor, limit)
			result := SetupResult{
				Identity:   &existing,
				Rollover:   &job,
				Migrations: migrations,
				NextCursor: next,
			}
			return result, wrapError(e)
		}
		return SetupResult{Identity: &existing}, nil
	case "rollover":
		if kind != lifecycle.Issuer {
			return SetupResult{}, lifecycle.ErrInvalid
		}
		var job lifecycle.Rollover
		var e error
		if req.ID == config.HTTPSCAID {
			job, e = a.startHTTPSTrustRollover(ctx, req.ID, req.Revision)
		} else {
			job, e = a.startIssuerRollover(ctx, req.ID, req.Revision)
		}
		return SetupResult{Rollover: &job}, wrapError(e)
	case "retire":
		if kind != lifecycle.Issuer {
			return SetupResult{}, lifecycle.ErrInvalid
		}
		if req.ID == config.HTTPSCAID {
			item, err = a.retireHTTPSTrust(ctx, req.ID, req.Revision)
		} else {
			item, err = a.retireManagedIssuer(ctx, req.ID, req.Revision)
		}
	case "request", "renew":
		if existing.Active != "" && existing.Pending == "" &&
			(operation == "request" || (existing.Severity == "" && !req.Force)) {
			return SetupResult{Identity: &existing}, nil
		}
		item, err = manager.Begin(ctx, req.Request)
	case "adopt":
		item, err = manager.Adopt(ctx, req.Request, req.Certificate, req.Key)
	case "import":
		if req.Revision == "" {
			return SetupResult{}, fmt.Errorf("%w: revision required", lifecycle.ErrInvalid)
		}
		item, err = manager.Import(ctx, req.ID, req.Revision, req.Certificate)
	case "activate":
		if kind == lifecycle.Issuer && existing.Active != "" && existing.Active != req.Revision {
			return SetupResult{}, fmt.Errorf(
				"%w: use issuer rollover for an existing enrollment authority",
				lifecycle.ErrConflict,
			)
		}
		item, err = manager.Activate(ctx, req.ID, req.Revision)
	case "cancel":
		item, err = manager.Cancel(ctx, req.ID, req.Revision)
	case "acme":
		if kind != lifecycle.HTTPS || req.PublicACME == nil {
			return SetupResult{}, lifecycle.ErrInvalid
		}
		item, err = manager.Begin(ctx, req.Request)
		if err == nil {
			err = a.Certificates.ConfigurePublicACME(ctx, req.ID, *req.PublicACME)
		}
	case "lab":
		if existing.Active != "" && existing.Pending == "" {
			return SetupResult{Identity: &existing}, nil
		}
		if kind != lifecycle.HTTPS {
			return SetupResult{}, lifecycle.ErrInvalid
		}
		caID := config.HTTPSCAID
		if caID == "" {
			return SetupResult{}, fmt.Errorf("%w: lab HTTPS needs httpsCaId", lifecycle.ErrInvalid)
		}
		caIdentity, e := manager.Get(ctx, caID)
		if errors.Is(e, lifecycle.ErrNotFound) {
			caIdentity, e = manager.Begin(
				ctx,
				lifecycle.Request{
					ID:      caID,
					Kind:    lifecycle.Issuer,
					Subject: pkix.Name{CommonName: "go-apple-dm lab HTTPS CA"},
				},
			)
		}
		if e != nil {
			return SetupResult{}, wrapError(e)
		}
		if caIdentity.Active == "" {
			if _, e = manager.CreateIssuer(ctx, caID, caIdentity.Pending, 0); e != nil {
				return SetupResult{}, wrapError(e)
			}
			if _, e = manager.Activate(ctx, caID, caIdentity.Pending); e != nil {
				return SetupResult{}, wrapError(e)
			}
		}
		caMaterial, e := a.Certificates.LoadMaterial(ctx, caID, "")
		if e != nil {
			return SetupResult{}, wrapError(e)
		}
		manager.Trust.HTTPSRoots = x509.NewCertPool()
		manager.Trust.HTTPSRoots.AppendCertsFromPEM(caMaterial.Certificate)
		item, err = manager.Begin(ctx, req.Request)
		if err == nil {
			item, err = manager.IssueHTTPS(ctx, req.ID, item.Pending, caID)
		}
	case "create":
		if existing.Active != "" && existing.Pending == "" {
			return SetupResult{Identity: &existing}, nil
		}
		if kind != lifecycle.Issuer {
			return SetupResult{}, lifecycle.ErrInvalid
		}
		item, err = manager.Begin(ctx, req.Request)
		if err == nil {
			item, err = manager.CreateIssuer(
				ctx,
				req.ID,
				item.Pending,
				time.Duration(req.ValidityDays)*24*time.Hour,
			)
		}
	case "sign":
		if kind == lifecycle.Vendor {
			data, err := manager.Sign(ctx, req.ID, req.CSR)
			return SetupResult{Data: data}, wrapError(err)
		}
		if kind != lifecycle.Push {
			return SetupResult{}, lifecycle.ErrInvalid
		}
		if req.Revision == "" {
			return SetupResult{}, fmt.Errorf("%w: revision required", lifecycle.ErrInvalid)
		}
		signed := req.SignedRequest
		if len(signed) == 0 {
			if config.Role != "combined" && config.VendorURL == "" {
				return SetupResult{}, fmt.Errorf(
					"%w: supply the vendor-signed request",
					lifecycle.ErrInvalid,
				)
			}
			csr, e := manager.Export(ctx, req.ID, req.Revision, "csr")
			if e != nil {
				return SetupResult{}, wrapError(e)
			}
			vendor := req.Vendor
			if vendor == "" {
				vendor = config.VendorID
			}
			if config.Role == "combined" {
				signed, err = manager.Sign(ctx, vendor, csr)
			} else {
				signed, err = a.remoteVendorSignature(ctx, vendor, csr)
			}
			if err != nil {
				return SetupResult{}, wrapError(err)
			}
		}
		item, err = manager.AttachSignature(ctx, req.ID, req.Revision, signed)
	default:
		return SetupResult{}, fmt.Errorf("%w: unsupported setup operation", lifecycle.ErrInvalid)
	}
	if err != nil {
		return SetupResult{}, wrapError(err)
	}
	return SetupResult{Identity: &item}, nil
}

// remoteVendorSignature sends only the public CSR to the explicitly configured
// signing service. Redirects cannot forward its administrative credential.
func (a *App) remoteVendorSignature(
	ctx context.Context,
	vendor string,
	csr []byte,
) ([]byte, error) {
	cfg := a.cfg.Setup
	u, err := url.Parse(cfg.VendorURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "" {
		return nil, fmt.Errorf("%w: vendor URL must be an HTTPS server URL", lifecycle.ErrInvalid)
	}
	token, err := os.ReadFile(cfg.VendorTokenFile)
	if err != nil {
		return nil, fmt.Errorf("app: read vendor credential: %w", err)
	}
	if len(bytes.TrimSpace(token)) == 0 {
		return nil, fmt.Errorf("%w: empty vendor credential", lifecycle.ErrInvalid)
	}
	body, err := json.Marshal(SetupRequest{Request: lifecycle.Request{ID: vendor}, CSR: csr})
	if err != nil {
		return nil, wrapError(err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/admin/v1/setup/vendor/sign"
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		u.String(),
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, wrapError(err)
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("app: vendor signing request: %w", err)
	}
	defer func(body io.Closer) { _ = body.Close() }(response.Body)
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf(
			"%w: vendor signing service returned HTTP %d",
			errOperation,
			response.StatusCode,
		)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxAdminBody+1))
	if err != nil {
		return nil, wrapError(err)
	}
	if len(data) > MaxAdminBody {
		return nil, ErrBodyTooLarge
	}
	var result SetupResult
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, wrapError(err)
	}
	if len(result.Data) == 0 {
		return nil, fmt.Errorf("%w: vendor returned no signed request", lifecycle.ErrInvalid)
	}
	return result.Data, nil
}
