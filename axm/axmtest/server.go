package axmtest

import (
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"uuid"
)

// Constants of the fake.
const (
	audience         = "https://account.apple.com/auth/oauth2/v2/token"
	assertionType    = "urn:ietf:params:oauth:client-assertion-type:jwt-bearer"
	maxAssertionLife = 180 * 24 * time.Hour
	// TokenPath is the token endpoint's path under URL.
	TokenPath = "/auth/oauth2/token" // #nosec G101 -- a URL path, not a credential
	// DefaultTokenTTL is expires_in.
	DefaultTokenTTL = time.Hour
	// DefaultLimit is the page size when limit is absent.
	DefaultLimit = 100
	// MaxLimit is the largest page size.
	MaxLimit = 1000
	// ClockSkew is tolerated on iat and exp.
	ClockSkew = 5 * time.Minute
	maxBody   = 8 << 20
)

// Request is one recorded request.
type Request struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   []byte
	Status int
}

// registeredKey is one API account.
type registeredKey struct {
	kid   string
	pub   *ecdsa.PublicKey
	scope string
}

// tokenInfo is one issued access token.
type tokenInfo struct {
	clientID string
	expires  time.Time
}

// Server is the fake. Create it with NewServer and Close it when done.
type Server struct {
	// URL is the base URL of the API; TokenURL the token endpoint.
	URL      string
	TokenURL string
	srv      *httptest.Server

	mu       sync.Mutex
	now      func() time.Time
	tokenTTL time.Duration
	keys     map[string]registeredKey
	jtis     map[string]struct{}
	tokens   map[string]tokenInfo

	rejectTokens int
	rateLimit    int
	retryAfter   string
	serverErrors int
	outcomes     map[string]string
	lag          time.Duration

	store         *store
	acts          map[string]*activity
	actOrder      []string
	unassigned404 bool
	requests      []Request
	ticker        *time.Ticker
	stop          chan struct{}
}

// NewServer starts a fake with an empty organization.
func NewServer() *Server {
	s := &Server{
		now:      time.Now,
		tokenTTL: DefaultTokenTTL,
		keys:     map[string]registeredKey{},
		jtis:     map[string]struct{}{},
		tokens:   map[string]tokenInfo{},
		outcomes: map[string]string{},
		store:    newStore(),
		acts:     map[string]*activity{},
	}
	s.srv = httptest.NewServer(s.handler())
	s.URL = s.srv.URL
	s.TokenURL = s.srv.URL + TokenPath
	return s
}

// Close stops the server and any auto-advance timer.
func (s *Server) Close() {
	s.mu.Lock()
	if s.ticker != nil {
		s.ticker.Stop()
		close(s.stop)
		s.ticker, s.stop = nil, nil
	}
	s.mu.Unlock()
	s.srv.Close()
}

// Client returns an HTTP client for the server.
func (s *Server) Client() *http.Client { return s.srv.Client() }

// SetNow replaces the server's clock (assertion windows, token expiry,
// timestamps).
func (s *Server) SetNow(now func() time.Time) {
	s.mu.Lock()
	s.now = now
	s.mu.Unlock()
}

// SetTokenTTL sets expires_in for tokens issued from now on.
func (s *Server) SetTokenTTL(d time.Duration) {
	s.mu.Lock()
	s.tokenTTL = d
	s.mu.Unlock()
}

// RegisterKey registers an API account: assertions for clientID must be
// signed by the key matching pub under kid. The scope is derived from the
// client id prefix (BUSINESSAPI. or SCHOOLAPI.).
func (s *Server) RegisterKey(clientID, kid string, pub *ecdsa.PublicKey) {
	scope := "business.api"
	if strings.HasPrefix(clientID, "SCHOOLAPI.") {
		scope = "school.api"
	}
	s.mu.Lock()
	s.keys[clientID] = registeredKey{kid: kid, pub: pub, scope: scope}
	s.mu.Unlock()
}

// ExpireTokens invalidates every token issued so far; the next API call
// with one of them gets a 401.
func (s *Server) ExpireTokens() {
	s.mu.Lock()
	s.tokens = map[string]tokenInfo{}
	s.mu.Unlock()
}

// RejectNextTokenRequests answers the next n token requests with 400
// invalid_client.
func (s *Server) RejectNextTokenRequests(n int) {
	s.mu.Lock()
	s.rejectTokens = n
	s.mu.Unlock()
}

// RateLimit answers the next n API requests with 429 and the given
// Retry-After value (seconds or an HTTP date; "" omits the header).
func (s *Server) RateLimit(n int, retryAfter string) {
	s.mu.Lock()
	s.rateLimit, s.retryAfter = n, retryAfter
	s.mu.Unlock()
}

// ServerError answers the next n API requests with 503.
func (s *Server) ServerError(n int) {
	s.mu.Lock()
	s.serverErrors = n
	s.mu.Unlock()
}

// SetOutcome makes every activity that touches serial fail for that
// serial with reason (an empty reason clears it). The activity then
// completes with COMPLETED_WITH_ERROR and the CSV carries the reason.
func (s *Server) SetOutcome(serial, reason string) {
	s.mu.Lock()
	if reason == "" {
		delete(s.outcomes, serial)
	} else {
		s.outcomes[serial] = reason
	}
	s.mu.Unlock()
}

// SetConsistencyLag delays, by d of wall-clock time, when a completed
// activity's assignment shows in the linkage endpoints.
func (s *Server) SetConsistencyLag(d time.Duration) {
	s.mu.Lock()
	s.lag = d
	s.mu.Unlock()
}

// Requests returns a copy of every request recorded since Reset.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// LastRequest returns the most recent request, or a zero Request.
func (s *Server) LastRequest() Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) == 0 {
		return Request{}
	}
	return s.requests[len(s.requests)-1]
}

// Reset clears the request log.
func (s *Server) Reset() {
	s.mu.Lock()
	s.requests = nil
	s.mu.Unlock()
}

// TokenRequests counts the recorded token requests.
func (s *Server) TokenRequests() int {
	n := 0
	for _, r := range s.Requests() {
		if r.Path == TokenPath {
			n++
		}
	}
	return n
}

// handler wires the routes.
func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+TokenPath, s.handleToken)
	s.routes(mux)
	return s.middleware(mux)
}

// recorder captures the status written by a handler.
type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// middleware records the request, then applies the Accept, bearer, and
// fault rules to API routes.
func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, maxBody))
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		defer func() {
			s.mu.Lock()
			s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header.Clone(), Body: body, Status: rec.status})
			s.mu.Unlock()
		}()
		if r.URL.Path == TokenPath {
			next.ServeHTTP(rec, r)
			return
		}
		accept := r.Header.Get("Accept")
		download := strings.HasSuffix(r.URL.Path, "/download") && strings.Contains(accept, "text/csv")
		if !acceptsJSON(accept) && !download {
			s.apiError(rec, http.StatusNotAcceptable, "NOT_ACCEPTABLE", "Accept must allow application/json", nil)
			return
		}
		if !s.authorized(r.Header.Get("Authorization")) {
			s.apiError(rec, http.StatusUnauthorized, "UNAUTHORIZED", "The access token is missing, expired, or invalid", nil)
			return
		}
		if status, header := s.fault(); status != 0 {
			if header != "" {
				rec.Header().Set("Retry-After", header)
			}
			code := "RATE_LIMIT_EXCEEDED"
			if status == http.StatusServiceUnavailable {
				code = "SERVICE_UNAVAILABLE"
			}
			s.apiError(rec, status, code, http.StatusText(status), nil)
			return
		}
		next.ServeHTTP(rec, r)
	})
}

// acceptsJSON reports whether an Accept header allows application/json.
func acceptsJSON(accept string) bool {
	for _, part := range strings.Split(accept, ",") {
		mt := strings.TrimSpace(strings.Split(part, ";")[0])
		if mt == "application/json" || mt == "*/*" || mt == "application/*" {
			return true
		}
	}
	return false
}

// authorized checks a bearer token.
func (s *Server) authorized(header string) bool {
	tok, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	info, ok := s.tokens[tok]
	if !ok {
		return false
	}
	if !s.now().Before(info.expires) {
		delete(s.tokens, tok)
		return false
	}
	return true
}

// fault consumes one injected fault, returning its status and Retry-After.
func (s *Server) fault() (int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.rateLimit > 0:
		s.rateLimit--
		return http.StatusTooManyRequests, s.retryAfter
	case s.serverErrors > 0:
		s.serverErrors--
		return http.StatusServiceUnavailable, ""
	}
	return 0, ""
}

// handleToken is the OAuth token endpoint.
func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		s.oauthError(w, "invalid_request", "Content-Type must be application/x-www-form-urlencoded")
		return
	}
	if err := r.ParseForm(); err != nil {
		s.oauthError(w, "invalid_request", "malformed form body")
		return
	}
	s.mu.Lock()
	if s.rejectTokens > 0 {
		s.rejectTokens--
		s.mu.Unlock()
		s.oauthError(w, "invalid_client", "rejected by fault injection")
		return
	}
	s.mu.Unlock()
	if r.PostForm.Get("grant_type") != "client_credentials" {
		s.oauthError(w, "unsupported_grant_type", "grant_type must be client_credentials")
		return
	}
	if r.PostForm.Get("client_assertion_type") != assertionType {
		s.oauthError(w, "invalid_request", "client_assertion_type must be "+assertionType)
		return
	}
	clientID := r.PostForm.Get("client_id")
	s.mu.Lock()
	key, ok := s.keys[clientID]
	s.mu.Unlock()
	if !ok {
		s.oauthError(w, "invalid_client", "unknown client_id")
		return
	}
	if scope := r.PostForm.Get("scope"); scope != key.scope {
		s.oauthError(w, "invalid_scope", fmt.Sprintf("scope must be %s", key.scope))
		return
	}
	assertion := r.PostForm.Get("client_assertion")
	header, claims, err := DecodeAssertion(assertion)
	if err != nil {
		s.oauthError(w, "invalid_client", err.Error())
		return
	}
	if header.Alg != "ES256" {
		s.oauthError(w, "invalid_client", "alg must be ES256")
		return
	}
	if header.Kid != key.kid {
		s.oauthError(w, "invalid_client", "unknown kid")
		return
	}
	if err := VerifyAssertion(assertion, key.pub); err != nil {
		s.oauthError(w, "invalid_client", err.Error())
		return
	}
	s.mu.Lock()
	now := s.now()
	if err := checkClaims(claims, clientID, now, ClockSkew); err != nil {
		s.mu.Unlock()
		s.oauthError(w, "invalid_client", err.Error())
		return
	}
	if _, seen := s.jtis[claims.Jti]; seen {
		s.mu.Unlock()
		s.oauthError(w, "invalid_client", "jti was already used")
		return
	}
	s.jtis[claims.Jti] = struct{}{}
	tok := "at-" + randomHex(16)
	s.tokens[tok] = tokenInfo{clientID: clientID, expires: now.Add(s.tokenTTL)}
	ttl := int64(s.tokenTTL / time.Second)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": tok, "token_type": "Bearer", "expires_in": ttl, "scope": key.scope,
	})
}

// oauthError writes an RFC 6749 error.
func (s *Server) oauthError(w http.ResponseWriter, code, description string) {
	writeJSON(w, http.StatusBadRequest, map[string]any{"error": code, "error_description": description})
}

// errorItem is one entry of Apple's error document.
type errorItem struct {
	ID     string         `json:"id"`
	Status string         `json:"status"`
	Code   string         `json:"code"`
	Title  string         `json:"title"`
	Detail string         `json:"detail"`
	Source map[string]any `json:"source,omitempty"`
}

// apiError writes Apple's error document; source is nil, or
// {"parameter": name} / {"pointer": path}.
func (s *Server) apiError(w http.ResponseWriter, status int, code, detail string, source map[string]any) {
	writeJSON(w, status, map[string]any{"errors": []errorItem{{
		ID: uuid.New().String(), Status: strconv.Itoa(status), Code: code, Title: http.StatusText(status), Detail: detail, Source: source,
	}}})
}

func (s *Server) notFound(w http.ResponseWriter, typ, id string) {
	s.apiError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", fmt.Sprintf("There is no resource of type '%s' with id '%s'", typ, id), nil)
}

func (s *Server) badParameter(w http.ResponseWriter, name, detail string) {
	s.apiError(w, http.StatusBadRequest, "PARAMETER_ERROR.INVALID", detail, map[string]any{"parameter": name})
}

func (s *Server) conflict(w http.ResponseWriter, code, detail, pointer string) {
	var src map[string]any
	if pointer != "" {
		src = map[string]any{"pointer": pointer}
	}
	s.apiError(w, http.StatusConflict, code, detail, src)
}

// writeJSON writes v with status.
func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// randomHex returns n random bytes as upper-case hex.
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToUpper(hex.EncodeToString(b))
}

// readJSON decodes a request body.
func readJSON(r *http.Request, v any) error {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxBody)).Decode(v); err != nil {
		return fmt.Errorf("decode body: %w", err)
	}
	return nil
}

// paging parses limit and cursor, answering 400 on bad values.
func (s *Server) paging(w http.ResponseWriter, r *http.Request) (limit, offset int, ok bool) {
	q := r.URL.Query()
	limit = DefaultLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > MaxLimit {
			s.badParameter(w, "limit", "limit must be an integer between 1 and 1000")
			return 0, 0, false
		}
		limit = n
	}
	if v := q.Get("cursor"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			s.badParameter(w, "cursor", "cursor is not valid")
			return 0, 0, false
		}
		offset = n
	}
	return limit, offset, true
}

// writePage slices items and writes a paged document with meta.paging
// and links.self/next; the next link carries every query parameter of the
// request with cursor replaced.
func (s *Server) writePage(w http.ResponseWriter, r *http.Request, items []any, limit, offset int, included []any) {
	total := len(items)
	if offset > total {
		offset = total
	}
	end := min(offset+limit, total)
	self := s.URL + r.URL.RequestURI()
	paging := map[string]any{"total": total, "limit": limit}
	links := map[string]any{"self": self}
	if end < total {
		q := r.URL.Query()
		q.Set("cursor", strconv.Itoa(end))
		next := *r.URL
		next.RawQuery = q.Encode()
		links["next"] = s.URL + next.RequestURI()
		paging["nextCursor"] = strconv.Itoa(end)
	}
	doc := map[string]any{"data": items[offset:end], "links": links, "meta": map[string]any{"paging": paging}}
	if included != nil {
		doc["included"] = included
	}
	writeJSON(w, http.StatusOK, doc)
}
