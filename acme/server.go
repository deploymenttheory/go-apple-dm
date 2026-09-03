package acme

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/acme/jose"
	"github.com/deploymenttheory/go-apple-dm/ca"
	"github.com/deploymenttheory/go-apple-dm/event"
	"github.com/deploymenttheory/go-apple-dm/internal/clock"
)

// Defaults applied by New.
const (
	// DefaultPrefix is where the directory and its endpoints live.
	DefaultPrefix = "/acme"
	// DefaultNonceTTL is how long a nonce stays usable. It only has to
	// cover the round trip between fetching one and using it.
	DefaultNonceTTL = time.Hour
	// DefaultOrderTTL is how long an order and its authorization stay open.
	DefaultOrderTTL = 24 * time.Hour
	// DefaultMaxBody is the largest request body read.
	DefaultMaxBody = 256 << 10
	// ContentTypeJOSE is the only content type a signed ACME request may
	// carry.
	ContentTypeJOSE = "application/jose+json"
	// ContentTypeProblem is RFC 7807.
	ContentTypeProblem = "application/problem+json"
	// ContentTypeJSON is what every successful ACME response carries.
	ContentTypeJSON = "application/json"
	// ContentTypePEMChain is the certificate download format of RFC 8555
	// section 7.4.2.
	ContentTypePEMChain = "application/pem-certificate-chain"
)

// ErrConfig is a server built with missing or contradictory settings.
var ErrConfig = errors.New("acme: invalid configuration")

// Config builds a Server.
type Config struct {
	// BaseURL is the externally visible origin, such as
	// https://mdm.example. Every URL the server hands out is built from it,
	// and the url header of every signed request is checked against it, so
	// a deployment behind a proxy needs no header trust of its own: what
	// matters is what the directory published, not what the last hop said.
	BaseURL string
	// Prefix is where the endpoints are mounted. Default DefaultPrefix.
	Prefix string
	// Store holds accounts, orders, challenges, nonces, and issued
	// certificates.
	Store Store
	// Signer issues the certificate.
	Signer ca.Signer
	// CAPolicy is the issuance policy: validity, key usage, and the key
	// types accepted. The subject and the subject alternative name are set
	// per order from the binding.
	CAPolicy ca.Policy
	// Identifiers turns the client identifier an order asks for into the
	// device the server expects. Required.
	Identifiers Identifiers
	// Authorize decides whether to issue once the attestation is verified.
	// Nil means AllowAll, which is only as strong as the identifiers.
	Authorize Policy
	// AllowUnattested issues to a device that produced no attestation, on
	// the strength of the client identifier alone. The zero value requires
	// an attestation, which is the setting a deployment should keep: Apple
	// hardware that cannot attest is old enough to be worth knowing about.
	AllowUnattested bool
	// Anchors are the attestation trust anchors. Empty means Apple's root,
	// which is what a deployment facing real devices wants.
	Anchors []*x509.Certificate
	// Clock, Bus, and Logger default to the real clock, no bus, and the
	// default logger.
	Clock  clock.Clock
	Bus    *event.Bus
	Logger *slog.Logger
	// NonceTTL, OrderTTL, and MaxBody default to the constants above.
	NonceTTL time.Duration
	OrderTTL time.Duration
	MaxBody  int64
}

// Server answers the ACME endpoints.
type Server struct {
	cfg Config
}

// New checks a configuration and builds a server.
func New(cfg Config) (*Server, error) {
	switch {
	case cfg.BaseURL == "":
		return nil, fmt.Errorf("%w: a base URL is required", ErrConfig)
	case cfg.Store == nil:
		return nil, fmt.Errorf("%w: a store is required", ErrConfig)
	case cfg.Signer == nil:
		return nil, fmt.Errorf("%w: a signer is required", ErrConfig)
	case cfg.Identifiers == nil:
		return nil, fmt.Errorf("%w: an identifier verifier is required", ErrConfig)
	}
	if !strings.HasPrefix(cfg.BaseURL, "https://") && !strings.HasPrefix(cfg.BaseURL, "http://") {
		return nil, fmt.Errorf("%w: base URL %q has no scheme", ErrConfig, cfg.BaseURL)
	}
	cfg.BaseURL = strings.TrimSuffix(cfg.BaseURL, "/")
	if cfg.Prefix == "" {
		cfg.Prefix = DefaultPrefix
	}
	cfg.Prefix = "/" + strings.Trim(cfg.Prefix, "/")
	if cfg.Authorize == nil {
		cfg.Authorize = AllowAll()
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.NonceTTL <= 0 {
		cfg.NonceTTL = DefaultNonceTTL
	}
	if cfg.OrderTTL <= 0 {
		cfg.OrderTTL = DefaultOrderTTL
	}
	if cfg.MaxBody <= 0 {
		cfg.MaxBody = DefaultMaxBody
	}
	return &Server{cfg: cfg}, nil
}

// Endpoint paths, relative to the prefix.
const (
	pathDirectory  = "/directory"
	pathNewNonce   = "/new-nonce"
	pathNewAccount = "/new-account"
	pathNewOrder   = "/new-order"
	pathAccount    = "/account/"
	pathOrder      = "/order/"
	pathAuthz      = "/authz/"
	pathChallenge  = "/challenge/"
	pathCert       = "/cert/"
)

// DirectoryURL is the URL a device's ACME payload points at.
func (s *Server) DirectoryURL() string { return s.url(pathDirectory) }

// Handler routes the ACME endpoints. The patterns carry the configured
// prefix, so the result is mounted on a parent multiplexer at that prefix.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	p := s.cfg.Prefix
	mux.HandleFunc("GET "+p+pathDirectory, s.plain(s.directory))
	// RFC 8555 section 7.2: new-nonce answers HEAD with 200 and GET with
	// 204, and both carry a nonce.
	mux.HandleFunc("HEAD "+p+pathNewNonce, s.plain(s.newNonce))
	mux.HandleFunc("GET "+p+pathNewNonce, s.plain(s.newNonce))
	mux.HandleFunc("POST "+p+pathNewAccount, s.signed(s.newAccount, allowJWK))
	mux.HandleFunc("POST "+p+pathNewOrder, s.signed(s.newOrder, requireKID))
	mux.HandleFunc("POST "+p+pathAccount+"{id}", s.signed(s.account, requireKID))
	mux.HandleFunc("POST "+p+pathAccount+"{id}/orders", s.signed(s.accountOrders, requireKID))
	mux.HandleFunc("POST "+p+pathOrder+"{id}", s.signed(s.order, requireKID))
	mux.HandleFunc("POST "+p+pathOrder+"{id}/finalize", s.signed(s.finalize, requireKID))
	mux.HandleFunc("POST "+p+pathAuthz+"{id}", s.signed(s.authorization, requireKID))
	mux.HandleFunc("POST "+p+pathChallenge+"{id}", s.signed(s.challenge, requireKID))
	mux.HandleFunc("POST "+p+pathCert+"{id}", s.signed(s.certificate, requireKID))
	// Anything else under the prefix is a client mistake, answered as an
	// ACME problem rather than as the multiplexer's HTML.
	mux.HandleFunc(p+"/", s.plain(func(e *exchange) error {
		return NewProblem(ProblemMalformed, "no such endpoint")
	}))
	return mux
}

// url builds an absolute URL under the prefix.
func (s *Server) url(parts ...string) string {
	return s.cfg.BaseURL + s.cfg.Prefix + strings.Join(parts, "")
}

// exchange is one request in flight.
type exchange struct {
	w   http.ResponseWriter
	r   *http.Request
	srv *Server
	// jws and account are set for a signed request.
	jws     *jose.JWS
	account *Account
	payload []byte
}

func (e *exchange) ctx() context.Context { return e.r.Context() }

// keyMode says where a signed request's key comes from.
type keyMode int

const (
	// allowJWK is new-account, where the key is in the request because the
	// server does not know it yet.
	allowJWK keyMode = iota
	// requireKID is everything else, where the key is the account's.
	requireKID
)

// plain wraps an endpoint that takes no signature.
func (s *Server) plain(fn func(*exchange) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		e := &exchange{w: w, r: r, srv: s}
		s.addNonce(e)
		s.addIndex(e)
		if err := fn(e); err != nil {
			s.fail(e, err)
		}
	}
}

// signed wraps an endpoint that requires a valid JWS, and does everything
// RFC 8555 section 6 asks of one before the endpoint sees it.
func (s *Server) signed(fn func(*exchange) error, mode keyMode) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		e := &exchange{w: w, r: r, srv: s}
		s.addNonce(e)
		s.addIndex(e)
		if err := s.authenticate(e, mode); err != nil {
			s.fail(e, err)
			return
		}
		if err := fn(e); err != nil {
			s.fail(e, err)
		}
	}
}

// authenticate parses and checks the JWS, consumes the nonce, and finds the
// account.
func (s *Server) authenticate(e *exchange, mode keyMode) error {
	if ct := e.r.Header.Get("Content-Type"); !strings.HasPrefix(ct, ContentTypeJOSE) {
		return NewProblem(
			ProblemMalformed, "the content type must be %s", ContentTypeJOSE,
		)
	}
	body, err := io.ReadAll(io.LimitReader(e.r.Body, s.cfg.MaxBody+1))
	if err != nil {
		return WrapProblem(ProblemMalformed, err, "the request body could not be read")
	}
	if int64(len(body)) > s.cfg.MaxBody {
		return NewProblem(ProblemMalformed, "the request body is larger than %d bytes", s.cfg.MaxBody)
	}
	jws, err := jose.Parse(body)
	if err != nil {
		if errors.Is(err, jose.ErrAlgorithm) {
			p := WrapProblem(
				ProblemBadSignatureAlgorithm, err, "the signature algorithm is not supported",
			)
			p.Algorithms = jose.Algorithms()
			return p
		}
		return WrapProblem(ProblemMalformed, err, "the request is not a well formed JWS")
	}
	e.jws = jws
	// The url header must be the URL this server published, not merely one
	// that resolves here. Comparing against the configured base means a
	// deployment behind a proxy that terminates TLS needs no forwarded
	// header to be believed.
	//
	// RequestURI keeps the query string, because RFC 8555 section 6.4 makes
	// url "the URL to which the client is directing the request" and a
	// client signs what it sends. Comparing the path alone would make the
	// paging link this server publishes on an order listing impossible to
	// follow.
	if want := s.cfg.BaseURL + e.r.URL.RequestURI(); jws.Header.URL != want {
		return NewProblem(
			ProblemMalformed, "the url header is %q, expected %q", jws.Header.URL, want,
		)
	}
	if err := s.consumeNonce(e.ctx(), jws.Header.Nonce); err != nil {
		return err
	}
	key, err := s.keyFor(e, mode)
	if err != nil {
		return err
	}
	if err := jws.Verify(key); err != nil {
		return WrapProblem(ProblemUnauthorized, err, "the signature does not verify")
	}
	e.payload = jws.Payload
	return nil
}

// keyFor resolves the key a request is signed with: the embedded one for a
// new account, the account's own for everything else.
func (s *Server) keyFor(e *exchange, mode keyMode) (any, error) {
	h := e.jws.Header
	if mode == allowJWK {
		if h.JWK == nil {
			return nil, NewProblem(ProblemMalformed, "a new account must be signed with its own key")
		}
		key, err := h.JWK.Public()
		if err != nil {
			return nil, WrapProblem(ProblemBadPublicKey, err, "the account key is not usable")
		}
		return key, nil
	}
	if h.KeyID == "" {
		return nil, NewProblem(ProblemMalformed, "the request must be signed with an account key")
	}
	id, ok := strings.CutPrefix(h.KeyID, s.url(pathAccount))
	if !ok || id == "" || strings.Contains(id, "/") {
		return nil, NewProblem(ProblemAccountDoesNotExist, "the kid header is not an account of this server")
	}
	account, err := s.cfg.Store.GetAccount(e.ctx(), id)
	if errors.Is(err, ErrNotFound) {
		return nil, NewProblem(ProblemAccountDoesNotExist, "no such account")
	}
	if err != nil {
		return nil, WrapProblem(ProblemServerInternal, err, "the account could not be read")
	}
	if account.Status != StatusValid {
		return nil, NewProblem(ProblemUnauthorized, "the account is %s", account.Status)
	}
	e.account = account
	key, err := account.Key.Public()
	if err != nil {
		return nil, WrapProblem(ProblemServerInternal, err, "the stored account key is not usable")
	}
	return key, nil
}

// consumeNonce takes a nonce, which is what makes it single use: the first
// request to present one removes it, so a replay finds nothing.
func (s *Server) consumeNonce(ctx context.Context, value string) error {
	if value == "" {
		return NewProblem(ProblemBadNonce, "the request carries no nonce")
	}
	n, err := s.cfg.Store.TakeNonce(ctx, value)
	if errors.Is(err, ErrNotFound) {
		return NewProblem(ProblemBadNonce, "the nonce is not one this server issued, or was already used")
	}
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the nonce could not be read")
	}
	if s.cfg.Clock.Now().Sub(n.IssuedAt) > s.cfg.NonceTTL {
		return NewProblem(ProblemBadNonce, "the nonce has expired")
	}
	return nil
}

// addNonce puts a fresh nonce on the response. RFC 8555 section 6.5 wants
// one on every response, successful or not, so a client can always retry
// without a round trip to new-nonce.
func (s *Server) addNonce(e *exchange) {
	value, err := s.mintNonce(e.ctx())
	if err != nil {
		// A client that gets no nonce will fetch one; failing the request
		// for it would be worse than carrying on.
		s.cfg.Logger.WarnContext(e.ctx(), "acme: mint nonce", "error", err)
		return
	}
	e.w.Header().Set("Replay-Nonce", value)
	e.w.Header().Add("Cache-Control", "no-store")
}

func (s *Server) mintNonce(ctx context.Context) (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("acme: nonce: %w", err)
	}
	value := base64.RawURLEncoding.EncodeToString(buf)
	if err := s.cfg.Store.PutNonce(ctx, Nonce{Value: value, IssuedAt: s.cfg.Clock.Now()}); err != nil {
		return "", fmt.Errorf("acme: store nonce: %w", err)
	}
	return value, nil
}

// addIndex adds the directory link RFC 8555 section 7.1 asks for.
func (s *Server) addIndex(e *exchange) {
	e.w.Header().Add("Link", `<`+s.url(pathDirectory)+`>;rel="index"`)
}

// write sends a JSON response.
func (s *Server) write(e *exchange, status int, body any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the response could not be encoded")
	}
	e.w.Header().Set("Content-Type", ContentTypeJSON)
	e.w.Header().Set("X-Content-Type-Options", "nosniff")
	e.w.WriteHeader(status)
	_, _ = e.w.Write(data)
	return nil
}

// fail renders an error as an RFC 7807 problem document. The cause is
// logged; the device sees only the type and the detail.
func (s *Server) fail(e *exchange, err error) {
	p := AsProblem(err)
	level := slog.LevelInfo
	if !p.Terminal() {
		level = slog.LevelError
	}
	s.cfg.Logger.Log(
		e.ctx(), level, "acme: request failed",
		"path", e.r.URL.Path, "problem", p.Type, "detail", p.Detail, "error", p.Unwrap(),
	)
	wire := *p
	wire.Type = p.URN()
	data, err := json.Marshal(&wire)
	if err != nil {
		http.Error(e.w, `{"type":"`+ProblemPrefix+ProblemServerInternal+`"}`, http.StatusInternalServerError)
		return
	}
	e.w.Header().Set("Content-Type", ContentTypeProblem)
	e.w.Header().Set("X-Content-Type-Options", "nosniff")
	e.w.WriteHeader(p.Status)
	_, _ = e.w.Write(data)
}

// publish sends an event when a bus is configured.
func (s *Server) publish(ctx context.Context, t event.Type, data any) {
	if s.cfg.Bus == nil {
		return
	}
	ev := event.Event{Type: t, At: s.cfg.Clock.Now(), Actor: "acme", Data: data}
	if err := s.cfg.Bus.Publish(ctx, ev); err != nil {
		s.cfg.Logger.WarnContext(ctx, "acme: publish", "event", t, "error", err)
	}
}

// newID mints an opaque identifier for a record. The endpoints are
// unauthenticated by URL alone, so an identifier has to be unguessable.
func newID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("acme: identifier: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// newToken mints a challenge token. RFC 8555 section 8.1 asks for at least
// 128 bits of entropy; this is 256, and it is what the device hashes into
// the attestation's freshness code.
func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("acme: token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
