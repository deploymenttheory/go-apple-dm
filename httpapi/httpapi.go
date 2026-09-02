package httpapi

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/plist"
	schemaerrors "github.com/deploymenttheory/go-apple-mdm/schema/errors"
	"github.com/deploymenttheory/go-apple-mdm/service"
)

// Content types Apple devices send.
const (
	ContentTypeCheckin = "application/x-apple-aspen-mdm-checkin"
	ContentTypeConnect = "application/x-apple-aspen-mdm"
	contentTypePlist   = "application/xml; charset=utf-8"
)

// Checkiner is the check-in half of the service.
type Checkiner interface {
	Checkin(ctx context.Context, r *mdm.Request, ck *mdm.Checkin) (*service.CheckinResult, error)
}

// Connecter is the command half of the service.
type Connecter interface {
	Connect(ctx context.Context, r *mdm.Request, resp *mdm.Response) (*mdm.Command, error)
}

// Config builds handlers.
type Config struct {
	Checkin Checkiner
	Connect Connecter
	// Logger defaults to slog.Default.
	Logger *slog.Logger
	// Decoder sets plist limits; zero means the defaults.
	Decoder plist.Decoder
	// UnenrollUnknown answers unknown enrollments with Apple's
	// ErrorUnrecognizedDevice body, which makes the device unenroll. Off
	// by default because it is irreversible for the device.
	UnenrollUnknown bool
	// Now defaults to time.Now.
	Now func() time.Time
}

func (c Config) logger() *slog.Logger {
	if c.Logger != nil {
		return c.Logger
	}
	return slog.Default()
}

func (c Config) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c Config) maxBytes() int64 {
	if c.Decoder.MaxBytes > 0 {
		return int64(c.Decoder.MaxBytes)
	}
	if c.Decoder.MaxBytes < 0 {
		return 0
	}
	return plist.DefaultMaxBytes
}

type ctxKey int

const certKey ctxKey = iota

// WithCert stores the device certificate in the context.
func WithCert(ctx context.Context, cert *x509.Certificate) context.Context {
	return context.WithValue(ctx, certKey, cert)
}

// CertFromContext returns the device certificate stored by a middleware,
// or nil.
func CertFromContext(ctx context.Context) *x509.Certificate {
	cert, _ := ctx.Value(certKey).(*x509.Certificate)
	return cert
}

// readBody reads at most max bytes; a larger body is an error.
func readBody(r *http.Request, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, fmt.Errorf("httpapi: read body: %w", err)
		}
		return body, nil
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("httpapi: read body: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%w: body exceeds %d bytes", plist.ErrTooLarge, maxBytes)
	}
	return body, nil
}

// request builds the service request from the HTTP request.
func (c Config) request(r *http.Request) *mdm.Request {
	params := map[string]string{}
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			params[k] = v[0]
		}
	}
	return &mdm.Request{
		Certificate: CertFromContext(r.Context()),
		Params:      params,
		Peer:        mdm.PeerInfo{RemoteAddr: r.RemoteAddr, UserAgent: r.UserAgent()},
		ReceivedAt:  c.now(),
	}
}

// CheckinHandler serves the check-in URL.
func CheckinHandler(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedMethod(w, r) {
			return
		}
		body, err := readBody(r, cfg.maxBytes())
		if err != nil {
			cfg.fail(w, r, http.StatusBadRequest, err)
			return
		}
		ck, err := mdm.DecodeCheckin(body, mdm.WithLimits(cfg.Decoder))
		if err != nil {
			cfg.fail(w, r, http.StatusBadRequest, err)
			return
		}
		res, err := cfg.Checkin.Checkin(r.Context(), cfg.request(r), ck)
		if err != nil {
			cfg.serviceError(w, r, err)
			return
		}
		status := http.StatusOK
		if res.Status != 0 {
			status = res.Status
		}
		writeBody(w, status, res.ContentType, res.Body)
	})
}

// writeBody sends a response body the device parses as a plist or JSON
// document. Bodies are never HTML: the content type is always explicit and
// sniffing is disabled, so a browser cannot be made to render them.
func writeBody(w http.ResponseWriter, status int, contentType string, body []byte) {
	if contentType == "" {
		contentType = contentTypePlist
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body) // #nosec G705 -- machine-readable plist/JSON with an explicit non-HTML content type
}

// ConnectHandler serves the server URL (command channel).
func ConnectHandler(cfg Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowedMethod(w, r) {
			return
		}
		body, err := readBody(r, cfg.maxBytes())
		if err != nil {
			cfg.fail(w, r, http.StatusBadRequest, err)
			return
		}
		resp, err := mdm.DecodeResponse(body, "", mdm.WithLimits(cfg.Decoder))
		if err != nil {
			cfg.fail(w, r, http.StatusBadRequest, err)
			return
		}
		cmd, err := cfg.Connect.Connect(r.Context(), cfg.request(r), resp)
		if err != nil {
			cfg.serviceError(w, r, err)
			return
		}
		if cmd == nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		writeBody(w, http.StatusOK, contentTypePlist, cmd.Raw)
	})
}

// Handler routes by content type so both URLs can point at one path, as
// NanoMDM and MicroMDM deployments commonly do.
func Handler(cfg Config) http.Handler {
	checkin, connect := CheckinHandler(cfg), ConnectHandler(cfg)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(ct, ContentTypeCheckin):
			checkin.ServeHTTP(w, r)
		case strings.HasPrefix(ct, ContentTypeConnect):
			connect.ServeHTTP(w, r)
		default:
			cfg.fail(w, r, http.StatusUnsupportedMediaType, fmt.Errorf("httpapi: unsupported content type %q", ct))
		}
	})
}

func allowedMethod(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodPut || r.Method == http.MethodPost {
		return true
	}
	w.Header().Set("Allow", "PUT, POST")
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	return false
}

func (c Config) fail(w http.ResponseWriter, r *http.Request, status int, err error) {
	c.logger().InfoContext(r.Context(), "mdm request rejected", "status", status, "err", err, "remote", r.RemoteAddr)
	http.Error(w, http.StatusText(status), status)
}

// serviceError maps service codes to HTTP statuses. 401 is never used.
func (c Config) serviceError(w http.ResponseWriter, r *http.Request, err error) {
	switch service.CodeOf(err) {
	case service.CodeBadRequest:
		c.fail(w, r, http.StatusBadRequest, err)
	case service.CodeForbidden:
		c.fail(w, r, http.StatusForbidden, err)
	case service.CodeNotImplemented:
		c.fail(w, r, http.StatusNotImplemented, err)
	case service.CodeGone:
		c.fail(w, r, http.StatusGone, err)
	case service.CodeUnknownEnrollment:
		if !c.UnenrollUnknown {
			c.fail(w, r, http.StatusForbidden, err)
			return
		}
		c.logger().InfoContext(r.Context(), "unknown enrollment, instructing device to unenroll", "err", err, "remote", r.RemoteAddr)
		body, merr := plist.Marshal(schemaerrors.UnrecognizedDevice{
			Code:        schemaerrors.ErrorCodeUnrecognizedDevice,
			Description: new("enrollment not recognized by this server"),
		})
		if merr != nil {
			c.fail(w, r, http.StatusInternalServerError, merr)
			return
		}
		writeBody(w, http.StatusForbidden, contentTypePlist, body)
	case service.CodeInternal:
		c.logger().ErrorContext(r.Context(), "mdm request failed", "err", err, "remote", r.RemoteAddr)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	default:
		c.logger().ErrorContext(r.Context(), "mdm request failed", "err", err, "remote", r.RemoteAddr)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
	}
}

// bodyReplacer lets middlewares read the body and hand it on.
func replaceBody(r *http.Request, body []byte) {
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
}

var errCertMissing = errors.New("httpapi: no client certificate")

// IsCertMissing reports whether err came from a missing certificate.
func IsCertMissing(err error) bool { return errors.Is(err, errCertMissing) }
