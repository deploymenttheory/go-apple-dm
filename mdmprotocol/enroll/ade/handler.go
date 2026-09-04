package ade

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/gdmf"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll"
	schemaerrors "github.com/deploymenttheory/go-apple-dm/v3/schema/errors"
)

// ErrRejected may be returned by a ProfileHook to refuse enrollment with
// 403 rather than 500.
var ErrRejected = errors.New("ade: enrollment rejected")

// ContentTypeProfile is the response content type for the profile.
const ContentTypeProfile = enroll.ContentTypeProfile

// Identity is what the handler knows about the enrolling device and,
// after web view authentication, the user.
type Identity struct {
	Serial   string
	UDID     string
	Platform Platform
	// Verified is false only in audit mode.
	Verified bool
	// DEP is the record DEPLookup found for the serial; nil when none.
	DEP any
	// Subject and Claims are filled by web view authentication; empty on
	// the token lane.
	Subject string
	Claims  map[string]any
}

// ProfileHook chooses and personalises the enrollment profile: the SCEP
// challenge, AssignedManagedAppleID, the ServerURL reference. Returning
// an error wrapping ErrRejected answers 403; any other error is 500.
type ProfileHook func(ctx context.Context, p *Parsed, id Identity) (*enroll.Profile, error)

// Bound is what the web view authenticator binds its state to, so the
// callback can be matched to the device that started it.
type Bound struct {
	Serial    string
	UDID      string
	Product   string
	OSVersion string
}

// WebAuth starts web view authentication for a device whose MachineInfo
// was verified on the configuration_web_url GET. Begin typically
// redirects to an identity provider; when the user is authenticated the
// authenticator calls Handler.Resume and Handler.Finish. enroll/webauth
// satisfies this interface.
type WebAuth interface {
	Begin(w http.ResponseWriter, r *http.Request, bound Bound)
}

// WebAuthFunc adapts a function to WebAuth, for example around
// enroll/webauth's Flow.Begin, whose Bound type and error return differ.
type WebAuthFunc func(w http.ResponseWriter, r *http.Request, bound Bound)

// Begin implements WebAuth.
func (f WebAuthFunc) Begin(w http.ResponseWriter, r *http.Request, bound Bound) { f(w, r, bound) }

// Signer signs the profile; the zero value serves it unsigned.
type Signer struct {
	Cert *x509.Certificate
	Key  crypto.Signer
}

// Config wires a Handler.
type Config struct {
	// Parse controls verification; the zero value verifies against Apple's chain.
	Parse ParseOptions
	// Store keeps MachineInfo per serial; default an in-memory store.
	Store MachineInfoStore
	// DEP joins the MachineInfo to the DEP record; optional.
	DEP DEPLookup
	// Profile builds the profile; required to serve one.
	Profile ProfileHook
	// Signer signs the profile; optional.
	Signer Signer
	// SoftwareUpdate is the gate policy; optional.
	SoftwareUpdate Policy
	// GDMF resolves "latest" targets; optional.
	GDMF gdmf.Lookup
	// WebAuth handles the web view GET; optional. Without it a GET with
	// MachineInfo is served the profile directly.
	WebAuth WebAuth
	// UnrecognizedDevice answers an unknown signer with 403 and the
	// unrecognized.device body instead of 401.
	UnrecognizedDevice bool
	// Logger defaults to slog.Default.
	Logger *slog.Logger
	// Now defaults to time.Now.
	Now func() time.Time
}

// Handler serves the ADE endpoint.
type Handler struct {
	cfg    Config
	store  MachineInfoStore
	logger *slog.Logger
}

// New builds a Handler.
func New(cfg Config) *Handler {
	h := &Handler{cfg: cfg, store: cfg.Store, logger: cfg.Logger}
	if h.store == nil {
		h.store = NewMemStore()
	}
	if h.logger == nil {
		h.logger = slog.Default()
	}
	if h.cfg.Parse.Logger == nil {
		h.cfg.Parse.Logger = h.logger
	}
	return h
}

func (h *Handler) now() time.Time {
	if h.cfg.Now != nil {
		return h.cfg.Now()
	}
	return time.Now()
}

// ServeHTTP serves the token-based POST and the web view GET.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	p, err := ParseMachineInfo(r, h.cfg.Parse)
	if err != nil {
		h.reject(w, r, err)
		return
	}
	if err := p.Validate(false); err != nil {
		h.reject(w, r, err)
		return
	}
	id, err := h.admit(r.Context(), p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	decision, err := Gate(r.Context(), p, h.cfg.SoftwareUpdate, h.cfg.GDMF, h.logger)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	if decision.Action != Proceed {
		h.logger.InfoContext(r.Context(), "ade: enrollment gated", "serial", p.SERIAL, "action", decision.Action.String(), "reason", decision.Reason)
		if err := decision.Write(w, r); err != nil {
			h.fail(w, r, err)
		}
		return
	}
	if r.Method == http.MethodGet && h.cfg.WebAuth != nil {
		h.cfg.WebAuth.Begin(w, r, Bound{Serial: p.SERIAL, UDID: p.UDID, Product: p.PRODUCT, OSVersion: p.OSVERSION})
		return
	}
	h.Finish(w, r, p, id)
}

// admit joins the DEP record and persists the MachineInfo.
func (h *Handler) admit(ctx context.Context, p *Parsed) (Identity, error) {
	id := Identity{Serial: p.SERIAL, UDID: p.UDID, Platform: p.Platform, Verified: p.Verified}
	if h.cfg.DEP != nil {
		rec, found, err := h.cfg.DEP.DeviceBySerial(ctx, p.SERIAL)
		if err != nil {
			return id, fmt.Errorf("dep lookup for %s: %w", p.SERIAL, err)
		}
		if found {
			id.DEP = rec
		}
	}
	if err := h.store.Put(ctx, &Record{Parsed: p, DEP: id.DEP, ReceivedAt: h.now()}); err != nil {
		return id, fmt.Errorf("persist MachineInfo for %s: %w", p.SERIAL, err)
	}
	return id, nil
}

// Resume reloads a device admitted earlier by serial, for the web view
// callback. It returns the stored MachineInfo and an Identity the caller
// completes with the authenticated Subject and Claims before Finish.
func (h *Handler) Resume(ctx context.Context, serial string) (*Parsed, Identity, error) {
	rec, ok, err := h.store.Get(ctx, serial)
	if err != nil {
		return nil, Identity{}, fmt.Errorf("%w: %w", ErrStore, err)
	}
	if !ok {
		return nil, Identity{}, fmt.Errorf("%w: no MachineInfo for serial %q", ErrNoMachineInfo, serial)
	}
	p := rec.Parsed
	return p, Identity{Serial: p.SERIAL, UDID: p.UDID, Platform: p.Platform, Verified: p.Verified, DEP: rec.DEP}, nil
}

// Finish builds, signs, and serves the profile for an admitted device.
// The web view authenticator calls it once the user is authenticated.
func (h *Handler) Finish(w http.ResponseWriter, r *http.Request, p *Parsed, id Identity) {
	if h.cfg.Profile == nil {
		http.Error(w, http.StatusText(http.StatusNotImplemented), http.StatusNotImplemented)
		return
	}
	prof, err := h.cfg.Profile(r.Context(), p, id)
	if err != nil {
		if errors.Is(err, ErrRejected) {
			h.logger.InfoContext(r.Context(), "ade: profile hook rejected enrollment", "serial", p.SERIAL, "error", err)
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		h.fail(w, r, fmt.Errorf("profile dmhook: %w", err))
		return
	}
	built, err := prof.Build()
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var out []byte
	if h.cfg.Signer.Cert != nil && h.cfg.Signer.Key != nil {
		out, err = built.Sign(h.cfg.Signer.Cert, h.cfg.Signer.Key)
	} else {
		out, err = built.Marshal()
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.logger.InfoContext(r.Context(), "ade: profile served", "serial", p.SERIAL, "udid", p.UDID, "origin", string(p.Origin), "verified", p.Verified, "subject", id.Subject)
	w.Header().Set("Content-Type", ContentTypeProfile)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out) // #nosec G705 -- a configuration profile with an explicit non-HTML content type
}

// reject maps a parse or presence error to its status. Decode errors are
// never 500.
func (h *Handler) reject(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, ErrUnverified):
		status = http.StatusUnauthorized
	case errors.Is(err, ErrUnknownSigner):
		status = http.StatusUnauthorized
		if h.cfg.UnrecognizedDevice {
			h.logger.InfoContext(r.Context(), "ade: unknown signer, answering unrecognized device", "error", err, "remote", r.RemoteAddr)
			body := &schemaerrors.UnrecognizedDevice{Code: schemaerrors.ErrorCodeUnrecognizedDevice, Description: new("MachineInfo signer is not trusted by this server")}
			if werr := WriteError(w, r, http.StatusForbidden, body); werr != nil {
				h.fail(w, r, werr)
			}
			return
		}
	}
	h.logger.InfoContext(r.Context(), "ade: request rejected", "status", status, "error", err, "remote", r.RemoteAddr)
	http.Error(w, http.StatusText(status), status)
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.ErrorContext(r.Context(), "ade: request failed", "error", err, "remote", r.RemoteAddr)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}
