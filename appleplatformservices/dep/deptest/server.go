package deptest

import (
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/deploymenttheory/go-apple-dm/v3/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/v3/clock"
)

// Defaults for Options.
const (
	DefaultWindow       = 5 * time.Minute
	DefaultCursorMaxAge = 7 * 24 * time.Hour
	DefaultPageLimit    = 100
	MaxPageLimit        = 1000
)

// Options configure NewServer.
type Options struct {
	// Clock drives OAuth timestamp checks, cursor ageing, and op_date.
	// Default clock.Real.
	Clock clock.Clock
	// Tokens are the OAuth credentials the fake accepts; a default set is
	// generated when empty.
	Tokens dep.Tokens
	// Window is the OAuth timestamp tolerance. Default 5m.
	Window time.Duration
	// RotateEvery rotates the session token on every Nth authenticated
	// request, invalidating the previous one. 0 never rotates.
	RotateEvery int
	// CursorMaxAge is when a cursor answers EXPIRED_CURSOR. Default 7 days.
	CursorMaxAge time.Duration
	// QuotedErrors writes error codes as JSON strings ("CODE") rather than
	// bare text (CODE).
	QuotedErrors bool
}

// Scripted is one canned answer for a path, consumed in order.
type Scripted struct {
	Status int
	// Code is written as the body (bare or quoted per QuotedErrors); Body
	// overrides it verbatim.
	Code string
	Body string
	// RetryAfter is sent as the Retry-After header when set.
	RetryAfter string
	// Session is sent as X-ADM-Auth-Session when set.
	Session string
}

// Request is one logged request.
type Request struct {
	Method  string
	Path    string
	Query   url.Values
	Header  http.Header
	Body    []byte
	Session string
}

type session struct {
	requests int
}

type cursor struct {
	kind   dep.Phase
	offset int
	seq    int
	issued time.Time
}

type op struct {
	seq    int
	typ    string
	date   time.Time
	device dep.Device
}

// Server is a fake DEP service. All methods are safe for concurrent use.
type Server struct {
	srv *httptest.Server
	o   Options

	mu           sync.Mutex
	sessions     map[string]*session
	sessionCalls int
	nextSession  int
	nonces       map[string]struct{}
	devices      map[string]dep.Device
	order        []string
	ops          []op
	cursors      map[string]*cursor
	nextCursor   int
	profiles     map[string]dep.Profile
	nextProfile  int
	scripts      map[string][]Scripted
	throttled    map[string]int
	notAccess    map[string]bool
	failed       map[string]bool
	repeatCursor bool
	termsNot     bool
	rejectAuth   bool
	seedOff      bool
	betaTokens   []dep.BetaToken
	discovery    string
	requests     []Request
	account      dep.AccountDetail
}

// NewServer starts the fake; Close stops it.
func NewServer(o Options) *Server {
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Window <= 0 {
		o.Window = DefaultWindow
	}
	if o.CursorMaxAge <= 0 {
		o.CursorMaxAge = DefaultCursorMaxAge
	}
	if o.Tokens.Validate() != nil {
		exp := o.Clock.Now().Add(365 * 24 * time.Hour)
		o.Tokens = dep.Tokens{ConsumerKey: "CK_deptest", ConsumerSecret: "CS_deptest", AccessToken: "AT_deptest", AccessSecret: "AS_deptest", AccessTokenExpiry: &exp} // #nosec G101 -- fixture credentials for the fake service
	}
	s := &Server{
		o:         o,
		sessions:  map[string]*session{},
		nonces:    map[string]struct{}{},
		devices:   map[string]dep.Device{},
		cursors:   map[string]*cursor{},
		profiles:  map[string]dep.Profile{},
		scripts:   map[string][]Scripted{},
		throttled: map[string]int{},
		notAccess: map[string]bool{},
		failed:    map[string]bool{},
		account: dep.AccountDetail{
			ServerName: "deptest", ServerUUID: "SERVER-UUID-DEPTEST", AdminID: "admin@example.com", OrgName: "Deployment Theory", OrgID: "ORG-1", OrgType: "org", OrgVersion: "v2",
			URLs: []dep.URL{
				{URI: dep.PathFetchDevices, HTTPMethod: []string{"POST"}, Limit: &dep.Limit{Default: DefaultPageLimit, Maximum: MaxPageLimit}},
				{URI: dep.PathSyncDevices, HTTPMethod: []string{"POST"}, Limit: &dep.Limit{Default: DefaultPageLimit, Maximum: MaxPageLimit}},
				{URI: dep.PathDeviceDetails, HTTPMethod: []string{"POST"}, Limit: &dep.Limit{Default: DefaultPageLimit, Maximum: MaxPageLimit}},
				{URI: dep.PathProfileDevs, HTTPMethod: []string{"POST", "PUT", "DELETE"}, Limit: &dep.Limit{Default: DefaultPageLimit, Maximum: MaxPageLimit}},
			},
		},
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	return s
}

// URL is the base URL for dep.ClientConfig.BaseURL.
func (s *Server) URL() string { return s.srv.URL }

// Close stops the server.
func (s *Server) Close() { s.srv.Close() }

// Tokens returns the OAuth credentials the fake accepts.
func (s *Server) Tokens() dep.Tokens { return s.o.Tokens }

// TokenP7M produces the server token file the portal would issue for the
// public key in cert, through dep.Wrap.
func (s *Server) TokenP7M(cert *x509.Certificate) ([]byte, error) {
	raw, err := dep.Marshal(s.o.Tokens)
	if err != nil {
		return nil, err
	}
	return dep.Wrap(raw, cert)
}

// Script queues canned answers for a path, served before any handler.
func (s *Server) Script(path string, answers ...Scripted) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripts[path] = append(s.scripts[path], answers...)
}

// SetTermsNotSigned makes /session answer 403 T_C_NOT_SIGNED.
func (s *Server) SetTermsNotSigned(v bool) { s.set(func() { s.termsNot = v }) }

// SetRejectTokens makes /session answer 401.
func (s *Server) SetRejectTokens(v bool) { s.set(func() { s.rejectAuth = v }) }

// SetRepeatCursor makes sync answer the cursor it was given with
// more_to_follow set, the loop condition the syncer must refuse.
func (s *Server) SetRepeatCursor(v bool) { s.set(func() { s.repeatCursor = v }) }

// SetSeedForITOff makes the beta tokens endpoint answer 403
// APPLE_SEED_FOR_IT_TURNED_OFF.
func (s *Server) SetSeedForITOff(v bool) { s.set(func() { s.seedOff = v }) }

// SetBetaTokens sets what the beta tokens endpoint lists.
func (s *Server) SetBetaTokens(t []dep.BetaToken) { s.set(func() { s.betaTokens = slices.Clone(t) }) }

// SetAccount replaces the account detail.
func (s *Server) SetAccount(a dep.AccountDetail) { s.set(func() { s.account = a }) }

// DiscoveryURL returns the account-driven enrollment discovery URL set.
func (s *Server) DiscoveryURL() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.discovery
}

// Throttle makes assignment answer THROTTLED for the serial with the
// retry_after_seconds given; 0 clears it.
func (s *Server) Throttle(serial string, retryAfterSeconds int) {
	s.set(func() {
		if retryAfterSeconds <= 0 {
			delete(s.throttled, serial)
			return
		}
		s.throttled[serial] = retryAfterSeconds
	})
}

// NotAccessible makes assignment answer NOT_ACCESSIBLE for the serial.
func (s *Server) NotAccessible(serial string, v bool) { s.set(func() { s.notAccess[serial] = v }) }

// Fail makes assignment answer FAILED for the serial.
func (s *Server) Fail(serial string, v bool) { s.set(func() { s.failed[serial] = v }) }

// InvalidateSessions forgets every session so the next call answers 401.
func (s *Server) InvalidateSessions() { s.set(func() { s.sessions = map[string]*session{} }) }

// SessionCalls counts successful /session authentications.
func (s *Server) SessionCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionCalls
}

// Requests returns the request log.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.requests)
}

// ResetRequests clears the request log.
func (s *Server) ResetRequests() { s.set(func() { s.requests = nil }) }

// Count returns how many logged requests hit method and path.
func (s *Server) Count(method, path string) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

// AddDevices adds devices to the organisation, each as an added op.
func (s *Server) AddDevices(devs ...dep.Device) {
	s.set(func() {
		for _, d := range devs {
			d = d.Clone()
			if d.ProfileStatus == "" {
				d.ProfileStatus = dep.ProfileStatusEmpty
			}
			if d.DeviceAssignedDate == nil {
				d.DeviceAssignedDate = dep.Time(s.o.Clock.Now())
			}
			if _, ok := s.devices[d.SerialNumber]; !ok {
				s.order = append(s.order, d.SerialNumber)
			}
			s.devices[d.SerialNumber] = d
			s.record(dep.OpAdded, d)
		}
	})
}

// ModifyDevice applies fn to a device and records a modified op.
func (s *Server) ModifyDevice(serial string, fn func(*dep.Device)) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[serial]
	if !ok {
		return false
	}
	fn(&d)
	s.devices[serial] = d
	s.record(dep.OpModified, d)
	return true
}

// DeleteDevice removes a device and records a deleted op.
func (s *Server) DeleteDevice(serial string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.remove(serial)
}

// Device returns the current record of a serial.
func (s *Server) Device(serial string) (dep.Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[serial]
	return d.Clone(), ok
}

// Profiles returns the profiles defined.
func (s *Server) Profiles() map[string]dep.Profile {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]dep.Profile, len(s.profiles))
	for k, p := range s.profiles {
		out[k] = p.Clone()
	}
	return out
}

func (s *Server) set(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

func (s *Server) record(typ string, d dep.Device) {
	d = d.Clone()
	s.ops = append(s.ops, op{seq: len(s.ops) + 1, typ: typ, date: s.o.Clock.Now(), device: d})
}

func (s *Server) remove(serial string) bool {
	d, ok := s.devices[serial]
	if !ok {
		return false
	}
	delete(s.devices, serial)
	s.order = slices.DeleteFunc(s.order, func(x string) bool { return x == serial })
	s.record(dep.OpDeleted, d)
	return true
}

// handle routes one request.
func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Header: r.Header.Clone(), Body: body, Session: r.Header.Get(dep.HeaderSession)})
	if r.Header.Get("User-Agent") == "" {
		s.fail(w, http.StatusBadRequest, dep.CodeUserAgentMissing)
		return
	}
	if q := s.scripts[r.URL.Path]; len(q) > 0 {
		s.scripts[r.URL.Path] = q[1:]
		s.scripted(w, q[0])
		return
	}
	if r.URL.Path == dep.PathSession {
		s.handleSession(w, r)
		return
	}
	token := r.Header.Get(dep.HeaderSession)
	sess, ok := s.sessions[token]
	if s.rejectAuth {
		// Rejected credentials invalidate live sessions too, so a client
		// with a cached session hits the refusal on its re-authentication.
		ok = false
	}
	if token == "" || !ok {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	sess.requests++
	if s.o.RotateEvery > 0 && sess.requests%s.o.RotateEvery == 0 {
		delete(s.sessions, token)
		w.Header().Set(dep.HeaderSession, s.newSession())
	}
	key := r.Method + " " + r.URL.Path
	switch key {
	case "GET " + dep.PathAccount:
		s.writeJSON(w, s.account)
	case "POST " + dep.PathFetchDevices:
		s.handleFetch(w, body)
	case "POST " + dep.PathSyncDevices:
		s.handleSync(w, body)
	case "POST " + dep.PathDeviceDetails:
		s.handleDetails(w, body)
	case "POST " + dep.PathDisown:
		s.handleDisown(w, body)
	case "POST " + dep.PathActivationLck:
		s.handleActivationLock(w, body)
	case "POST " + dep.PathProfile:
		s.handleDefine(w, body)
	case "GET " + dep.PathProfile:
		s.handleFetchProfile(w, r.URL.Query().Get("profile_uuid"))
	case "POST " + dep.PathProfileDevs, "PUT " + dep.PathProfileDevs:
		s.handleAssign(w, body)
	case "DELETE " + dep.PathProfileDevs:
		s.handleRemove(w, body)
	case "POST " + dep.PathDiscovery:
		s.handleSetDiscovery(w, body)
	case "GET " + dep.PathDiscovery:
		if s.discovery == "" {
			s.fail(w, http.StatusNotFound, dep.CodeNotFound)
			return
		}
		s.writeJSON(w, map[string]string{"mdm_service_discovery_url": s.discovery})
	case "DELETE " + dep.PathDiscovery:
		s.discovery = ""
		w.WriteHeader(http.StatusOK)
	case "GET " + dep.PathBetaTokens:
		if s.seedOff {
			s.fail(w, http.StatusForbidden, dep.CodeSeedForITOff)
			return
		}
		s.writeJSON(w, map[string]any{"betaEnrollmentTokens": s.betaTokens})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Server) scripted(w http.ResponseWriter, sc Scripted) {
	if sc.RetryAfter != "" {
		w.Header().Set("Retry-After", sc.RetryAfter)
	}
	if sc.Session != "" {
		w.Header().Set(dep.HeaderSession, sc.Session)
	}
	if sc.Body != "" {
		w.Header().Set("Content-Type", "application/json;charset=UTF8")
		w.WriteHeader(sc.Status)
		_, _ = io.WriteString(w, sc.Body)
		return
	}
	s.fail(w, sc.Status, sc.Code)
}

// fail writes an error code in the configured form.
func (s *Server) fail(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json;charset=UTF8")
	w.WriteHeader(status)
	if code == "" {
		return
	}
	if s.o.QuotedErrors {
		code = `"` + code + `"`
	}
	_, _ = io.WriteString(w, code)
}

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	raw, err := dep.Marshal(v)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json;charset=UTF8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(raw)
}

func (s *Server) newSession() string {
	s.nextSession++
	tok := fmt.Sprintf("SESSION-%04d", s.nextSession)
	s.sessions[tok] = &session{}
	return tok
}

// handleSession verifies the OAuth 1.0a signature, timestamp window, and
// nonce, then issues a session.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	if err := s.verifyOAuth(r); err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "oauth: "+err.Error()) // #nosec G705 -- the fake's own verification message, plain text with nosniff
		return
	}
	switch {
	case s.rejectAuth:
		w.WriteHeader(http.StatusUnauthorized)
		return
	case s.termsNot:
		s.fail(w, http.StatusForbidden, dep.CodeTermsNotSigned)
		return
	}
	s.sessionCalls++
	s.writeJSON(w, map[string]string{"auth_session_token": s.newSession()})
}

func (s *Server) verifyOAuth(r *http.Request) error {
	p, err := dep.ParseOAuth1Header(r.Header.Get("Authorization"))
	if err != nil {
		return err
	}
	if p["realm"] != dep.OAuth1Realm {
		return fmt.Errorf("%w: realm %q", dep.ErrInvalid, p["realm"])
	}
	if p["oauth_signature_method"] != "HMAC-SHA1" {
		return fmt.Errorf("%w: signature method %q", dep.ErrInvalid, p["oauth_signature_method"])
	}
	if p["oauth_consumer_key"] != s.o.Tokens.ConsumerKey || p["oauth_token"] != s.o.Tokens.AccessToken {
		return fmt.Errorf("%w: unknown consumer key or token", dep.ErrTokenInvalid)
	}
	ts, err := strconv.ParseInt(p["oauth_timestamp"], 10, 64)
	if err != nil {
		return fmt.Errorf("%w: timestamp: %w", dep.ErrInvalid, err)
	}
	now := s.o.Clock.Now()
	if skew := now.Sub(time.Unix(ts, 0)); skew > s.o.Window || skew < -s.o.Window {
		return fmt.Errorf("%w: timestamp outside window", dep.ErrInvalid)
	}
	nonce := p["oauth_timestamp"] + ":" + p["oauth_nonce"]
	if p["oauth_nonce"] == "" {
		return fmt.Errorf("%w: empty nonce", dep.ErrInvalid)
	}
	if _, replay := s.nonces[nonce]; replay {
		return fmt.Errorf("%w: nonce replayed", dep.ErrInvalid)
	}
	u := *r.URL
	u.Scheme, u.Host = "http", r.Host
	o := dep.OAuth1{ConsumerKey: s.o.Tokens.ConsumerKey, ConsumerSecret: s.o.Tokens.ConsumerSecret, Token: s.o.Tokens.AccessToken, TokenSecret: s.o.Tokens.AccessSecret, Timestamp: ts, Nonce: p["oauth_nonce"], Version: p["oauth_version"] != ""}
	if subtle.ConstantTimeCompare([]byte(o.Sign(r.Method, &u)), []byte(p["oauth_signature"])) != 1 {
		return fmt.Errorf("%w: bad signature", dep.ErrTokenInvalid)
	}
	s.nonces[nonce] = struct{}{}
	return nil
}

type pageRequest struct {
	Cursor string `json:"cursor"`
	Limit  int    `json:"limit"`
}

func (s *Server) limit(n int) int {
	switch {
	case n <= 0:
		return DefaultPageLimit
	case n > MaxPageLimit:
		return MaxPageLimit
	}
	return n
}

func (s *Server) issueCursor(c cursor) string {
	s.nextCursor++
	id := fmt.Sprintf("%016x", s.nextCursor)
	c.issued = s.o.Clock.Now()
	s.cursors[id] = &c
	return id
}

// lookupCursor returns the cursor or the error code to answer.
func (s *Server) lookupCursor(id string) (*cursor, string) {
	c, ok := s.cursors[id]
	if !ok {
		return nil, dep.CodeInvalidCursor
	}
	if s.o.Clock.Now().Sub(c.issued) > s.o.CursorMaxAge {
		return nil, dep.CodeExpiredCursor
	}
	return c, ""
}

// stripOp removes the sync-only keys from a fetch record.
func stripOp(d dep.Device) dep.Device {
	d = d.Clone()
	d.OpType, d.OpDate = "", nil
	return d
}

func (s *Server) handleFetch(w http.ResponseWriter, body []byte) {
	var req pageRequest
	if err := dep.Unmarshal(body, &req); err != nil {
		s.fail(w, http.StatusBadRequest, dep.CodeMalformedBody)
		return
	}
	c := &cursor{kind: dep.PhaseFetch, seq: len(s.ops)}
	if req.Cursor != "" {
		var code string
		if c, code = s.lookupCursor(req.Cursor); code != "" {
			s.fail(w, http.StatusBadRequest, code)
			return
		}
		if c.kind != dep.PhaseFetch {
			s.fail(w, http.StatusBadRequest, dep.CodeExhaustedCursor)
			return
		}
	}
	limit := s.limit(req.Limit)
	end := min(c.offset+limit, len(s.order))
	devs := make([]dep.Device, 0, end-c.offset)
	for _, serial := range s.order[c.offset:end] {
		devs = append(devs, stripOp(s.devices[serial]))
	}
	more := end < len(s.order)
	next := cursor{kind: dep.PhaseSync, seq: c.seq}
	if more {
		next = cursor{kind: dep.PhaseFetch, offset: end, seq: c.seq}
	}
	s.writeJSON(w, dep.DevicePage{Cursor: s.issueCursor(next), Devices: devs, FetchedUntil: dep.Time(s.o.Clock.Now()), MoreToFollow: more})
}

func (s *Server) handleSync(w http.ResponseWriter, body []byte) {
	var req pageRequest
	if err := dep.Unmarshal(body, &req); err != nil {
		s.fail(w, http.StatusBadRequest, dep.CodeMalformedBody)
		return
	}
	if req.Cursor == "" {
		s.fail(w, http.StatusBadRequest, dep.CodeCursorRequired)
		return
	}
	c, code := s.lookupCursor(req.Cursor)
	if code != "" {
		s.fail(w, http.StatusBadRequest, code)
		return
	}
	if s.repeatCursor {
		s.writeJSON(w, dep.DevicePage{Cursor: req.Cursor, MoreToFollow: true})
		return
	}
	limit := s.limit(req.Limit)
	start := min(c.seq, len(s.ops))
	end := min(start+limit, len(s.ops))
	devs := make([]dep.Device, 0, end-start)
	for _, o := range s.ops[start:end] {
		d := o.device.Clone()
		d.OpType, d.OpDate = o.typ, dep.Time(o.date)
		devs = append(devs, d)
	}
	next := s.issueCursor(cursor{kind: dep.PhaseSync, seq: end})
	s.writeJSON(w, dep.DevicePage{Cursor: next, Devices: devs, FetchedUntil: dep.Time(s.o.Clock.Now()), MoreToFollow: end < len(s.ops)})
}

type serialsBody struct {
	ProfileUUID string   `json:"profile_uuid"`
	Devices     []string `json:"devices"`
}

func (s *Server) serials(w http.ResponseWriter, body []byte) (serialsBody, bool) {
	var req serialsBody
	if err := dep.Unmarshal(body, &req); err != nil {
		s.fail(w, http.StatusBadRequest, dep.CodeMalformedBody)
		return req, false
	}
	if len(req.Devices) == 0 {
		s.fail(w, http.StatusBadRequest, dep.CodeDeviceIDRequired)
		return req, false
	}
	return req, true
}

func (s *Server) handleDetails(w http.ResponseWriter, body []byte) {
	req, ok := s.serials(w, body)
	if !ok {
		return
	}
	out := map[string]dep.Device{}
	for _, serial := range req.Devices {
		d, ok := s.devices[serial]
		if !ok {
			out[serial] = dep.Device{ResponseStatus: dep.StatusNotAccessible}
			continue
		}
		d = stripOp(d)
		d.ResponseStatus = dep.StatusSuccess
		out[serial] = d
	}
	s.writeJSON(w, map[string]any{"devices": out})
}

func (s *Server) handleDisown(w http.ResponseWriter, body []byte) {
	req, ok := s.serials(w, body)
	if !ok {
		return
	}
	out := map[string]string{}
	for _, serial := range req.Devices {
		if s.remove(serial) {
			out[serial] = dep.StatusSuccess
		} else {
			out[serial] = dep.StatusNotAccessible
		}
	}
	s.writeJSON(w, map[string]any{"devices": out})
}

func (s *Server) handleActivationLock(w http.ResponseWriter, body []byte) {
	var req dep.ActivationLockRequest
	if err := dep.Unmarshal(body, &req); err != nil || req.Device == "" {
		s.fail(w, http.StatusBadRequest, dep.CodeMalformedBody)
		return
	}
	status := dep.StatusSuccess
	if _, ok := s.devices[req.Device]; !ok {
		status = "DEVICE_NOT_FOUND"
	}
	s.writeJSON(w, dep.ActivationLockResponse{SerialNumber: req.Device, ResponseStatus: status})
}

// assign applies the profile to the serials and returns the outcomes and
// the largest retry_after_seconds among throttled serials.
func (s *Server) assign(uuid string, serials []string) (map[string]string, int) {
	out := map[string]string{}
	retry := 0
	for _, serial := range serials {
		d, ok := s.devices[serial]
		switch {
		case !ok || s.notAccess[serial]:
			out[serial] = dep.StatusNotAccessible
		case s.throttled[serial] > 0:
			out[serial] = dep.StatusThrottled
			retry = max(retry, s.throttled[serial])
		case s.failed[serial]:
			out[serial] = dep.StatusFailed
		default:
			d.ProfileUUID, d.ProfileStatus, d.ProfileAssignTime = uuid, dep.ProfileStatusAssigned, dep.Time(s.o.Clock.Now())
			s.devices[serial] = d
			s.record(dep.OpModified, d)
			out[serial] = dep.StatusSuccess
		}
	}
	return out, retry
}

func (s *Server) handleDefine(w http.ResponseWriter, body []byte) {
	var p dep.Profile
	if err := dep.Unmarshal(body, &p); err != nil {
		s.fail(w, http.StatusBadRequest, dep.CodeMalformedBody)
		return
	}
	if err := p.Validate(); err != nil {
		var pe *dep.ProfileError
		code := dep.CodeMalformedBody
		if errors.As(err, &pe) {
			code = pe.Code
		}
		s.fail(w, http.StatusBadRequest, code)
		return
	}
	s.nextProfile++
	p.ProfileUUID = fmt.Sprintf("PROFILE-%04d", s.nextProfile)
	s.profiles[p.ProfileUUID] = p.Clone()
	out := map[string]string{}
	if len(p.Devices) > 0 {
		out, _ = s.assign(p.ProfileUUID, p.Devices)
	}
	s.writeJSON(w, dep.ProfileResponse{ProfileUUID: p.ProfileUUID, Devices: out})
}

func (s *Server) handleFetchProfile(w http.ResponseWriter, uuid string) {
	if uuid == "" {
		s.fail(w, http.StatusBadRequest, dep.CodeProfileUUIDRequired)
		return
	}
	p, ok := s.profiles[uuid]
	if !ok {
		s.fail(w, http.StatusBadRequest, dep.CodeNotFound)
		return
	}
	s.writeJSON(w, p)
}

func (s *Server) handleAssign(w http.ResponseWriter, body []byte) {
	var req serialsBody
	if err := dep.Unmarshal(body, &req); err != nil {
		s.fail(w, http.StatusBadRequest, dep.CodeMalformedBody)
		return
	}
	if req.ProfileUUID == "" {
		s.fail(w, http.StatusBadRequest, dep.CodeProfileUUIDRequired)
		return
	}
	if len(req.Devices) == 0 {
		s.fail(w, http.StatusBadRequest, dep.CodeDeviceIDRequired)
		return
	}
	if _, ok := s.profiles[req.ProfileUUID]; !ok {
		s.fail(w, http.StatusNotFound, dep.CodeNotFound)
		return
	}
	out, retry := s.assign(req.ProfileUUID, req.Devices)
	s.writeJSON(w, dep.AssignResponse{ProfileUUID: req.ProfileUUID, Devices: out, RetryAfterSeconds: retry})
}

func (s *Server) handleRemove(w http.ResponseWriter, body []byte) {
	req, ok := s.serials(w, body)
	if !ok {
		return
	}
	out := map[string]string{}
	for _, serial := range req.Devices {
		d, ok := s.devices[serial]
		if !ok {
			out[serial] = dep.StatusNotAccessible
			continue
		}
		d.ProfileUUID, d.ProfileStatus = "", dep.ProfileStatusRemoved
		s.devices[serial] = d
		s.record(dep.OpModified, d)
		out[serial] = dep.StatusSuccess
	}
	s.writeJSON(w, map[string]any{"devices": out})
}

func (s *Server) handleSetDiscovery(w http.ResponseWriter, body []byte) {
	var req struct {
		URL string `json:"mdm_service_discovery_url"` //nolint:tagliatelle // Apple's key
	}
	if err := dep.Unmarshal(body, &req); err != nil {
		s.fail(w, http.StatusBadRequest, dep.CodeMalformedBody)
		return
	}
	if req.URL == "" {
		s.fail(w, http.StatusBadRequest, dep.CodeDiscoveryRequired)
		return
	}
	if u, err := url.Parse(req.URL); err != nil || u.Scheme != "https" || u.Host == "" {
		s.fail(w, http.StatusBadRequest, dep.CodeDiscoveryInvalid)
		return
	}
	s.discovery = req.URL
	w.WriteHeader(http.StatusOK)
}

// String renders a request for test failure messages.
func (r Request) String() string {
	return r.Method + " " + r.Path + " " + strings.TrimSpace(string(r.Body))
}
