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

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/gdmf"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	schemaerrors "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/errors"
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
	// Parsed is the exact validated MachineInfo that initiated this handoff.
	// It is borrowed read-only data, including reachable maps, slices and certificates;
	// the in-memory store aliases this value. Callers must not mutate it. Retaining
	// callers must snapshot this evidence into their authentication state and bind
	// it to that state's original serial, UDID, expiry and consumption.
	Parsed    *Parsed
	Serial    string
	UDID      string
	Product   string
	OSVersion string
}

// WebAuth starts web view authentication for a device whose MachineInfo
// was verified on the configuration_web_url GET. Begin typically
// redirects to an identity provider. The authenticator retains Bound.Parsed in
// its expiring, one-use authentication state and, after authentication, validates
// the retained evidence against that state's serial and UDID before calling
// Handler.Finish. A latest-per-serial lookup cannot bind the original session.
// WebAuthFunc adapts enroll/webauth's Flow.Begin to this interface.
type WebAuth interface {
	// Begin starts browser authentication bound to the verified device and writes the
	// initial HTTP response.
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
	// SigningIdentity selects the current profile signer when supplied.
	SigningIdentity func(context.Context) (*x509.Certificate, crypto.Signer, error)
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

// now returns the configured clock time, using the package default when no clock is
// supplied.
func (h *Handler) now() time.Time {
	if h.cfg.Now != nil {
		return h.cfg.Now()
	}
	return time.Now()
}

// ServeHTTP serves the token-based POST and the web view GET.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
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
		h.logger.InfoContext(
			r.Context(),
			"ade: enrollment gated",
			"serial",
			p.SERIAL,
			"action",
			decision.Action.String(),
			"reason",
			decision.Reason,
		)
		if err := decision.Write(w, r); err != nil {
			h.fail(w, r, err)
		}
		return
	}
	if r.Method == http.MethodGet && h.cfg.WebAuth != nil {
		h.cfg.WebAuth.Begin(
			w,
			r,
			Bound{Parsed: p, Serial: p.SERIAL, UDID: p.UDID, Product: p.PRODUCT, OSVersion: p.OSVERSION},
		)
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

// Resume retrieves the latest admitted record for a serial. It returns the stored
// MachineInfo and identity; the in-memory store's returned data is borrowed and
// read-only. Another request may have replaced the record since a session began,
// so Resume is unsuitable for binding an original browser authentication session.
// Browser callbacks instead use evidence retained from Bound.Parsed.
func (h *Handler) Resume(ctx context.Context, serial string) (*Parsed, Identity, error) {
	rec, ok, err := h.store.Get(ctx, serial)
	if err != nil {
		return nil, Identity{}, fmt.Errorf("%w: %w", ErrStore, err)
	}
	if !ok {
		return nil, Identity{}, fmt.Errorf(
			"%w: no MachineInfo for serial %q",
			ErrNoMachineInfo,
			serial,
		)
	}
	p := rec.Parsed
	return p, Identity{
		Serial:   p.SERIAL,
		UDID:     p.UDID,
		Platform: p.Platform,
		Verified: p.Verified,
		DEP:      rec.DEP,
	}, nil
}

// Finish builds, signs, and serves the profile for an admitted device.
// The web view authenticator calls it once the user is authenticated.
func (h *Handler) Finish(w http.ResponseWriter, r *http.Request, p *Parsed, id Identity) {
	w.Header().Set("Cache-Control", "no-store")
	if h.cfg.Profile == nil {
		http.Error(w, http.StatusText(http.StatusNotImplemented), http.StatusNotImplemented)
		return
	}
	prof, err := h.cfg.Profile(r.Context(), p, id)
	if err != nil {
		if errors.Is(err, ErrRejected) {
			h.logger.InfoContext(
				r.Context(),
				"ade: profile hook rejected enrollment",
				"serial",
				p.SERIAL,
				"error",
				err,
			)
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
	signer := h.cfg.Signer
	if h.cfg.SigningIdentity != nil {
		signer.Cert, signer.Key, err = h.cfg.SigningIdentity(r.Context())
		if err != nil {
			h.fail(w, r, err)
			return
		}
		if signer.Cert == nil || signer.Key == nil {
			h.fail(w, r, fmt.Errorf("ade: signing identity unavailable"))
			return
		}
	}
	if signer.Cert != nil && signer.Key != nil {
		out, err = built.Sign(signer.Cert, signer.Key)
	} else {
		out, err = built.Marshal()
	}
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.logger.InfoContext(
		r.Context(),
		"ade: profile served",
		"serial",
		p.SERIAL,
		"udid",
		p.UDID,
		"origin",
		string(p.Origin),
		"verified",
		p.Verified,
		"subject",
		id.Subject,
	)
	w.Header().Set("Content-Type", ContentTypeProfile)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Length", strconv.Itoa(len(out)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(
		out,
	) // #nosec G705 -- a configuration profile with an explicit non-HTML content type
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
			h.logger.InfoContext(
				r.Context(),
				"ade: unknown signer, answering unrecognized device",
				"error",
				err,
				"remote",
				r.RemoteAddr,
			)
			body := &schemaerrors.UnrecognizedDevice{
				Code:        schemaerrors.ErrorCodeUnrecognizedDevice,
				Description: new("MachineInfo signer is not trusted by this server"),
			}
			if werr := WriteError(w, r, http.StatusForbidden, body); werr != nil {
				h.fail(w, r, werr)
			}
			return
		}
	}
	h.logger.InfoContext(
		r.Context(),
		"ade: request rejected",
		"status",
		status,
		"error",
		err,
		"remote",
		r.RemoteAddr,
	)
	http.Error(w, http.StatusText(status), status)
}

// fail logs the enrollment failure and writes a generic HTTP 500 response.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.ErrorContext(r.Context(), "ade: request failed", "error", err, "remote", r.RemoteAddr)
	http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
}
