package discovery

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/plist"
	schemaerrors "github.com/deploymenttheory/go-apple-dm/schema/errors"
)

// WellKnownPath is the path Apple devices fetch on the organisation domain.
const WellKnownPath = "/.well-known/com.apple.remotemanagement"

// Query parameter names the device sends.
const (
	QueryModelFamily    = "model-family"
	QueryUserIdentifier = "user-identifier"
)

// ModelFamily is the device's model family as sent in model-family.
type ModelFamily string

// Model families Apple documents.
const (
	ModelFamilyAppleTV       ModelFamily = "AppleTV"
	ModelFamilyIPad          ModelFamily = "iPad"
	ModelFamilyIPhone        ModelFamily = "iPhone"
	ModelFamilyMac           ModelFamily = "Mac"
	ModelFamilyRealityDevice ModelFamily = "RealityDevice"
	ModelFamilyWatch         ModelFamily = "Watch"
)

// ModelFamilies lists the documented values in Apple's order.
var ModelFamilies = []ModelFamily{
	ModelFamilyAppleTV, ModelFamilyIPad, ModelFamilyIPhone,
	ModelFamilyMac, ModelFamilyRealityDevice, ModelFamilyWatch,
}

// ParseModelFamily matches s exactly against the documented values. An
// unknown value is preserved so a Router can log or reject it; known
// reports whether it matched.
func ParseModelFamily(s string) (family ModelFamily, known bool) {
	for _, f := range ModelFamilies {
		if string(f) == s {
			return f, true
		}
	}
	return ModelFamily(s), false
}

// Known reports whether the family is one Apple documents.
func (m ModelFamily) Known() bool {
	_, ok := ParseModelFamily(string(m))
	return ok
}

// Versions of the enrollment protocol a Server may announce.
const (
	// VersionBYOD selects a user enrollment.
	VersionBYOD = "mdm-byod"
	// VersionADDE selects a device enrollment.
	VersionADDE = "mdm-adde"
)

// Server is one entry of the Servers array in the response.
//
//nolint:tagliatelle // keys are Apple's WellKnown.AvailableServer names
type Server struct {
	Version string `json:"Version"`
	BaseURL string `json:"BaseURL"`
}

// wellKnown is the response document.
//
//nolint:tagliatelle // key is Apple's WellKnown name
type wellKnown struct {
	Servers []Server `json:"Servers"`
}

// Request is what the device asked.
type Request struct {
	// ModelFamily is the parsed model-family; unknown values are preserved.
	ModelFamily ModelFamily
	// UserIdentifier is the user-identifier as sent (user@domain).
	UserIdentifier string
	// RawQuery is the complete query string for routers that need more.
	RawQuery string
}

// Router decides which servers a request is routed to. Return a
// *Rejection (or an error wrapping ErrReject) to answer 403
// com.apple.well-known.failed; any other error is a 500 without detail.
type Router func(ctx context.Context, req Request) ([]Server, error)

// ErrReject marks a rejection: errors.Is(err, ErrReject) holds for every
// *Rejection.
var ErrReject = errors.New("discovery: rejected")

// ErrRouter is wrapped by errors the Handler logs about a Router result.
var ErrRouter = errors.New("discovery: router")

// Rejection answers a request with 403 and Apple's well-known.failed body.
type Rejection struct {
	// Code is the error code; empty means com.apple.well-known.failed,
	// the only value the schema allows.
	Code string
	// Description is for logs on the device, never shown to the user.
	Description string
	// Message is shown to the user.
	Message string
}

// Error implements error.
func (r *Rejection) Error() string {
	return fmt.Sprintf("%s: %s", ErrReject.Error(), r.Description)
}

// Is reports ErrReject.
func (r *Rejection) Is(target error) bool { return target == ErrReject }

// Reject returns a *Rejection with the user-facing message and log
// description.
func Reject(message, description string) error {
	return &Rejection{Code: schemaerrors.ErrorCodeWellKnownFailed, Description: description, Message: message}
}

// Config configures Handler.
type Config struct {
	// Router decides the answer; required.
	Router Router
	// Logger defaults to slog.Default.
	Logger *slog.Logger
}

func (c Config) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

// Content types this package writes.
const (
	contentTypeJSON  = "application/json"
	contentTypePlist = "application/x-plist"
	contentTypeXML   = "application/xml; charset=utf-8"
)

// Handler serves the well-known document at whatever path it is mounted.
func Handler(cfg Config) http.Handler {
	logger := cfg.logger()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedMethod(w, r) {
			return
		}
		if cfg.Router == nil {
			logger.ErrorContext(r.Context(), "discovery: no router configured", "remote", r.RemoteAddr)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		q := r.URL.Query()
		family, known := ParseModelFamily(q.Get(QueryModelFamily))
		req := Request{ModelFamily: family, UserIdentifier: q.Get(QueryUserIdentifier), RawQuery: r.URL.RawQuery}
		servers, err := cfg.Router(r.Context(), req)
		if err != nil {
			var rej *Rejection
			if errors.As(err, &rej) {
				logger.InfoContext(r.Context(), "discovery: rejected", "model_family", string(family), "user", req.UserIdentifier, "description", rej.Description, "remote", r.RemoteAddr)
				writeRejection(w, r, logger, rej)
				return
			}
			logger.ErrorContext(r.Context(), "discovery: router failed", "error", err, "model_family", string(family), "known_family", known, "remote", r.RemoteAddr)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		if err := validateServers(servers); err != nil {
			logger.ErrorContext(r.Context(), "discovery: router returned an unservable answer", "error", err, "model_family", string(family), "remote", r.RemoteAddr)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		body, err := json.Marshal(wellKnown{Servers: servers})
		if err != nil {
			logger.ErrorContext(r.Context(), "discovery: encode", "error", err)
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		writeBody(w, r, http.StatusOK, contentTypeJSON, body)
	})
}

// validateServers enforces what the device requires: at least one entry,
// a Version, and an absolute https BaseURL.
func validateServers(servers []Server) error {
	if len(servers) == 0 {
		return fmt.Errorf("%w: no servers", ErrRouter)
	}
	for i, s := range servers {
		if s.Version == "" {
			return fmt.Errorf("%w: server %d: empty Version", ErrRouter, i)
		}
		if err := requireHTTPS(s.BaseURL); err != nil {
			return fmt.Errorf("%w: server %d: BaseURL: %w", ErrRouter, i, err)
		}
	}
	return nil
}

// ErrNotHTTPS reports a URL that is not absolute https.
var ErrNotHTTPS = errors.New("discovery: URL must be absolute https")

func requireHTTPS(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotHTTPS, err)
	}
	if u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%w: %q", ErrNotHTTPS, raw)
	}
	return nil
}

func allowedMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		return true
	}
	w.Header().Set("Allow", "GET, HEAD")
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	return false
}

// writeBody sends a machine-readable body with sniffing disabled and
// caching off. HEAD gets the headers and length only.
func writeBody(w http.ResponseWriter, r *http.Request, status int, contentType string, body []byte) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(body) // #nosec G705 -- machine-readable JSON/plist with an explicit non-HTML content type
}

// writeRejection answers 403 with the well-known.failed body as JSON, or as
// a plist when the Accept header prefers one.
func writeRejection(w http.ResponseWriter, r *http.Request, logger *slog.Logger, rej *Rejection) {
	code := rej.Code
	if code == "" {
		code = schemaerrors.ErrorCodeWellKnownFailed
	}
	if code != schemaerrors.ErrorCodeWellKnownFailed {
		logger.ErrorContext(r.Context(), "discovery: rejection code not allowed by the schema", "code", code)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	failed := schemaerrors.WellKnownFailed{Code: code}
	if rej.Description != "" {
		failed.Description = &rej.Description
	}
	if rej.Message != "" {
		failed.Message = &rej.Message
	}
	var (
		body []byte
		err  error
		ct   string
	)
	if plistType := preferredPlistType(r.Header.Get("Accept")); plistType != "" {
		ct = plistType
		body, err = plist.Marshal(failed)
	} else {
		ct = contentTypeJSON
		body, err = json.Marshal(failed)
	}
	if err != nil {
		logger.ErrorContext(r.Context(), "discovery: encode rejection", "error", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	writeBody(w, r, http.StatusForbidden, ct, body)
}

// preferredPlistType returns the plist content type to answer with when
// the Accept header ranks a plist media type above application/json, or
// "" when JSON is preferred. Absent, malformed, or tied preferences mean
// JSON.
func preferredPlistType(accept string) string {
	var (
		jsonQ  float64
		plistQ float64
		ct     string
	)
	for part := range strings.SplitSeq(accept, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		mediaType, params, err := mime.ParseMediaType(part)
		if err != nil {
			continue
		}
		q := 1.0
		if v, ok := params["q"]; ok {
			if parsed, perr := strconv.ParseFloat(v, 64); perr == nil {
				q = parsed
			}
		}
		switch mediaType {
		case "application/json":
			jsonQ = max(jsonQ, q)
		case "application/x-plist":
			if q > plistQ {
				plistQ, ct = q, contentTypePlist
			}
		case "application/xml", "text/xml":
			if q > plistQ {
				plistQ, ct = q, contentTypeXML
			}
		}
	}
	if plistQ > jsonQ {
		return ct
	}
	return ""
}

// Redirect answers GET and HEAD with a 302 to target carrying the
// request's query parameters, because a device following a redirect from
// the well-known URL does not re-send them. Parameters already on target
// are kept unless the request carries the same name. A target that is not
// an absolute https URL is answered with 500 so a misconfiguration never
// sends devices to plain http.
func Redirect(target string) http.Handler {
	t, err := url.Parse(target)
	if err == nil {
		err = requireHTTPS(target)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedMethod(w, r) {
			return
		}
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		u := *t
		q := u.Query()
		for k, vs := range r.URL.Query() {
			q[k] = vs
		}
		u.RawQuery = q.Encode()
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, u.String(), http.StatusFound)
	})
}

// StaticRouter routes each model family to a fixed server and rejects
// families that are not in the table.
func StaticRouter(table map[ModelFamily]Server) Router {
	return func(_ context.Context, req Request) ([]Server, error) {
		s, ok := table[req.ModelFamily]
		if !ok {
			return nil, Reject("This device type cannot enroll here.", "model family "+strconv.Quote(string(req.ModelFamily))+" not routed")
		}
		return []Server{s}, nil
	}
}
