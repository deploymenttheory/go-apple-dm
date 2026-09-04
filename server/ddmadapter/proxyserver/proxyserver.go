package proxyserver

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/v3/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/v3/server/ddmadapter/internal/proxywire"
)

// Backend serves one DeclarativeManagement check-in; *ddm.Engine
// satisfies it.
type Backend interface {
	Handle(ctx context.Context, id mdm.EnrollmentID, endpoint string, data []byte) (ddm.Response, error)
}

var _ Backend = (*ddm.Engine)(nil)

// Errors.
var (
	ErrNoBackend    = errors.New("proxyserver: Config.Backend is required")
	ErrUnauthorized = errors.New("proxyserver: unauthorized")
	ErrNoClientCert = errors.New("proxyserver: verified TLS client certificate required")
	ErrNotDM        = errors.New("proxyserver: check-in is not DeclarativeManagement")
)

// Config builds a Handler.
type Config struct {
	// Backend is required.
	Backend Backend
	// RecvKey, when set, must sign every request (X-MDM-Signature).
	RecvKey []byte
	// SendKey, when set, signs every response body, including empty ones
	// and error statuses.
	SendKey []byte
	// MaxBody bounds the request body; default proxywire.DefaultMaxBody.
	MaxBody int64
	// Auth, when set, runs first on every request; an error is 401. See
	// BearerAuth.
	Auth func(*http.Request) error
	// ClientCAs, when set, requires a TLS client certificate that the TLS
	// layer verified and that chains to this pool. See TLSConfig.
	ClientCAs *x509.CertPool
	// Decoder sets plist limits; zero means the defaults.
	Decoder plist.Decoder
	// Logger defaults to slog.Default.
	Logger *slog.Logger
}

// BearerAuth returns an Auth check that requires "Authorization: Bearer
// <token>", compared in constant time. An empty token rejects everything.
func BearerAuth(token string) func(*http.Request) error {
	want := []byte(token)
	return func(r *http.Request) error {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" || subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			return fmt.Errorf("%w: bearer token", ErrUnauthorized)
		}
		return nil
	}
}

// TLSConfig returns a server configuration that requires and verifies a
// client certificate against clientCAs. A serverCert with no certificate
// bytes is omitted so the caller (or httptest) can supply one.
func TLSConfig(serverCert tls.Certificate, clientCAs *x509.CertPool) *tls.Config {
	cfg := &tls.Config{
		ClientCAs:  clientCAs,
		ClientAuth: tls.RequireAndVerifyClientCert,
		MinVersion: tls.VersionTLS12,
	}
	if len(serverCert.Certificate) > 0 {
		cfg.Certificates = []tls.Certificate{serverCert}
	}
	return cfg
}

type server struct {
	cfg Config
	log *slog.Logger
}

// Handler validates cfg and returns the ingress handler. Only
// POST proxywire.Path is served: any other path is 404 and any other
// method 405.
func Handler(cfg Config) (http.Handler, error) {
	if cfg.Backend == nil {
		return nil, ErrNoBackend
	}
	s := &server{cfg: cfg, log: cfg.Logger}
	if s.log == nil {
		s.log = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+proxywire.Path, s.serve)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := s.authenticate(r); err != nil {
			s.reject(w, r, http.StatusUnauthorized, err)
			return
		}
		mux.ServeHTTP(w, r)
	}), nil
}

// authenticate runs the caller checks that do not need the body: Auth
// and the client certificate.
func (s *server) authenticate(r *http.Request) error {
	if s.cfg.Auth != nil {
		if err := s.cfg.Auth(r); err != nil {
			return fmt.Errorf("%w: %w", ErrUnauthorized, err)
		}
	}
	if s.cfg.ClientCAs == nil {
		return nil
	}
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		return ErrNoClientCert
	}
	_, err := r.TLS.PeerCertificates[0].Verify(x509.VerifyOptions{
		Roots:     s.cfg.ClientCAs,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNoClientCert, err)
	}
	return nil
}

func (s *server) serve(w http.ResponseWriter, r *http.Request) {
	if ct, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || ct != proxywire.ContentType {
		s.reject(w, r, http.StatusUnsupportedMediaType, fmt.Errorf("%w: %q", proxywire.ErrContentType, r.Header.Get("Content-Type")))
		return
	}
	body, err := proxywire.ReadBody(r.Body, s.cfg.MaxBody)
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, proxywire.ErrBodyTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		s.reject(w, r, status, err)
		return
	}
	if s.cfg.RecvKey != nil {
		if err := proxywire.Verify(s.cfg.RecvKey, r.Header.Get(proxywire.HeaderSignature), body); err != nil {
			s.reject(w, r, http.StatusUnauthorized, err)
			return
		}
	}
	ck, err := mdm.DecodeCheckin(body, mdm.WithLimits(s.cfg.Decoder))
	if err != nil {
		s.reject(w, r, http.StatusBadRequest, err)
		return
	}
	m, ok := ck.Message.(*checkin.DeclarativeManagement)
	if !ok {
		s.reject(w, r, http.StatusBadRequest, fmt.Errorf("%w: %s", ErrNotDM, ck.Type))
		return
	}
	resp, err := s.cfg.Backend.Handle(r.Context(), ck.ID, m.Endpoint, m.Data)
	switch {
	case errors.Is(err, ddm.ErrBadEndpoint), errors.Is(err, ddm.ErrStatusTooLarge), errors.Is(err, ddm.ErrStatusMalformed):
		s.reject(w, r, http.StatusBadRequest, err)
		return
	case err != nil:
		s.log.ErrorContext(r.Context(), "ddm backend failed", "enrollment", ck.ID.ID, "endpoint", m.Endpoint, "err", err)
		s.write(w, http.StatusInternalServerError, nil)
		return
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	if status == http.StatusNotFound {
		resp.Body = nil
	}
	s.write(w, status, resp.Body)
}

// reject answers a request the ingress refused, with no body so nothing
// about the failure reaches the caller beyond the status.
func (s *server) reject(w http.ResponseWriter, r *http.Request, status int, err error) {
	s.log.InfoContext(r.Context(), "declarative management request rejected", "status", status, "remote", r.RemoteAddr, "err", err)
	s.write(w, status, nil)
}

// write sends status and body, signing the exact bytes written when a
// SendKey is configured. Bodies are JSON and never sniffed.
func (s *server) write(w http.ResponseWriter, status int, body []byte) {
	h := w.Header()
	if len(body) > 0 {
		h.Set("Content-Type", "application/json")
	}
	h.Set("X-Content-Type-Options", "nosniff")
	if s.cfg.SendKey != nil {
		h.Set(proxywire.HeaderSignature, proxywire.SignResponse(s.cfg.SendKey, status, body))
	}
	w.WriteHeader(status)
	_, _ = w.Write(body) // #nosec G705 -- machine-readable JSON with an explicit non-HTML content type
}
