package accountdriven

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/deploymenttheory/go-apple-mdm/enroll"
)

// Versions from service discovery and the EnrollmentMode they imply.
const (
	VersionBYOD = "mdm-byod"
	VersionADDE = "mdm-adde"
	ModeBYOD    = "BYOD"
	ModeADDE    = "ADDE"
	// ParamEnrollmentToken is the ServerURL query parameter carrying the
	// enrollment token into every check-in and connect.
	ParamEnrollmentToken = "enrollment-token"
	// ContentTypeProfile is the profile response type.
	ContentTypeProfile = "application/x-apple-aspen-config"
	// MaxBody bounds the first POST.
	MaxBody = 64 << 10
)

// Handler errors.
var (
	ErrConfig              = errors.New("accountdriven: invalid configuration")
	ErrManagedAppleAccount = errors.New("accountdriven: identity has no Managed Apple Account")
	ErrMode                = errors.New("accountdriven: enrollment mode does not match the discovery version")
)

// DeviceInfo is the verified first-POST body: Apple documents LANGUAGE,
// PRODUCT, VERSION; devices also send the MachineInfo keys, which the
// parser may surface in Extra.
type DeviceInfo struct {
	Language, Product, Version, OSVersion string
	Extra                                 map[string]any
	Raw                                   []byte
}

// Parser verifies and decodes the signed body (enroll/ade supplies one).
// Returning an *HTTPError relays a policy answer such as the software
// update gate's 403 to the device.
type Parser func(r *http.Request) (*DeviceInfo, error)

// HTTPError lets a Parser or Authenticator answer with a specific status
// and body (Apple's 403 error documents).
type HTTPError struct {
	Status      int
	ContentType string
	Body        []byte
	Err         error
}

func (e *HTTPError) Error() string { return fmt.Sprintf("accountdriven: http %d: %v", e.Status, e.Err) }
func (e *HTTPError) Unwrap() error { return e.Err }

// Authenticator produces the challenge for an unauthenticated attempt.
type Authenticator interface {
	Challenge(ctx context.Context, r *http.Request, info *DeviceInfo) (Challenge, error)
}

// ProfileHook builds the enrollment profile for an authenticated identity.
// The handler then sets EnrollmentMode and AssignedManagedAppleID and
// appends the enrollment token, so hooks leave those alone.
type ProfileHook func(ctx context.Context, id Identity, info *DeviceInfo) (*enroll.Profile, error)

// Config builds a Handler.
type Config struct {
	// Version is the discovery Version this endpoint serves (mdm-byod or
	// mdm-adde); it fixes EnrollmentMode.
	Version string
	Parse   Parser
	Auth    Authenticator
	Tokens  *Tokens
	Profile ProfileHook
	// SignCert and SignKey sign the profile (CMS attached).
	SignCert *x509.Certificate
	SignKey  crypto.Signer
	Logger   *slog.Logger
}

// Handler serves the enrollment URL named by service discovery.
type Handler struct {
	cfg Config
}

// New validates cfg.
func New(cfg Config) (*Handler, error) {
	switch {
	case cfg.Version != VersionBYOD && cfg.Version != VersionADDE:
		return nil, fmt.Errorf("%w: Version must be %s or %s", ErrConfig, VersionBYOD, VersionADDE)
	case cfg.Parse == nil || cfg.Auth == nil || cfg.Tokens == nil || cfg.Profile == nil:
		return nil, fmt.Errorf("%w: Parse, Auth, Tokens, and Profile are required", ErrConfig)
	case cfg.SignCert == nil || cfg.SignKey == nil:
		return nil, fmt.Errorf("%w: SignCert and SignKey are required", ErrConfig)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Handler{cfg: cfg}, nil
}

// Mode is the EnrollmentMode for a discovery version.
func Mode(version string) (string, error) {
	switch version {
	case VersionBYOD:
		return ModeBYOD, nil
	case VersionADDE:
		return ModeADDE, nil
	}
	return "", fmt.Errorf("%w: version %q", ErrMode, version)
}

// ServeHTTP handles the enrollment POST.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBody)
	info, err := h.cfg.Parse(r)
	if err != nil {
		h.fail(w, r, err, http.StatusBadRequest)
		return
	}
	bearer, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || bearer == "" {
		h.challenge(w, r, info)
		return
	}
	rec, err := h.cfg.Tokens.Consume(r.Context(), KindAccess, bearer)
	if err != nil {
		// Invalid, expired, or replayed: a fresh challenge, never 500.
		h.cfg.Logger.InfoContext(r.Context(), "accountdriven: bearer rejected", "error", err)
		h.challenge(w, r, info)
		return
	}
	h.serveProfile(w, r, rec.Identity, info)
}

func (h *Handler) challenge(w http.ResponseWriter, r *http.Request, info *DeviceInfo) {
	c, err := h.cfg.Auth.Challenge(r.Context(), r, info)
	if err != nil {
		h.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	header, err := c.Header()
	if err != nil {
		h.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("WWW-Authenticate", header)
	w.WriteHeader(http.StatusUnauthorized)
}

// serveProfile finalises, signs, and writes the profile.
func (h *Handler) serveProfile(w http.ResponseWriter, r *http.Request, id Identity, info *DeviceInfo) {
	ctx := r.Context()
	p, err := h.cfg.Profile(ctx, id, info)
	if err != nil {
		h.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	enrollmentToken, err := h.cfg.Tokens.Issue(ctx, KindEnrollment, id, nil)
	if err != nil {
		h.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	if err := Finalize(p, h.cfg.Version, id, enrollmentToken); err != nil {
		h.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	built, err := p.Build()
	if err != nil {
		h.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	signed, err := built.Sign(h.cfg.SignCert, h.cfg.SignKey)
	if err != nil {
		h.fail(w, r, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", ContentTypeProfile)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(signed)
}

// Finalize enforces the account-driven keys on a profile: EnrollmentMode
// from the discovery version, AssignedManagedAppleID from the identity
// (required), and the enrollment token appended to ServerURL and
// CheckInURL. A profile that already carries a different mode or managed
// Apple ID is rejected: those keys are immutable.
func Finalize(p *enroll.Profile, version string, id Identity, enrollmentToken string) error {
	mode, err := Mode(version)
	if err != nil {
		return err
	}
	if id.ManagedAppleAccount == "" {
		return ErrManagedAppleAccount
	}
	if p.EnrollmentMode != "" && p.EnrollmentMode != mode {
		return fmt.Errorf("%w: profile says %s, version %s", ErrMode, p.EnrollmentMode, version)
	}
	if p.AssignedManagedAppleID != "" && p.AssignedManagedAppleID != id.ManagedAppleAccount {
		return fmt.Errorf("%w: profile already assigned to another account", ErrManagedAppleAccount)
	}
	p.EnrollmentMode = mode
	p.AssignedManagedAppleID = id.ManagedAppleAccount
	if enrollmentToken != "" {
		p.ServerURL, err = withParam(p.ServerURL, ParamEnrollmentToken, enrollmentToken)
		if err != nil {
			return err
		}
		if p.CheckInURL != "" {
			if p.CheckInURL, err = withParam(p.CheckInURL, ParamEnrollmentToken, enrollmentToken); err != nil {
				return err
			}
		}
	}
	return nil
}

func withParam(raw, key, value string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("accountdriven: url %q: %w", raw, err)
	}
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// fail writes an error: an *HTTPError is relayed as is (the device sees
// Apple's documented 403 bodies), anything else gets status with no detail.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error, status int) {
	var he *HTTPError
	if errors.As(err, &he) {
		if he.ContentType != "" {
			w.Header().Set("Content-Type", he.ContentType)
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(he.Status)
		_, _ = w.Write(he.Body)
		return
	}
	if status >= http.StatusInternalServerError {
		h.cfg.Logger.ErrorContext(r.Context(), "accountdriven: enrollment failed", "error", err)
	} else {
		h.cfg.Logger.InfoContext(r.Context(), "accountdriven: request rejected", "error", err)
	}
	http.Error(w, http.StatusText(status), status)
}
