package acme_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/acme"
	"github.com/deploymenttheory/go-apple-mdm/acme/attest"
	"github.com/deploymenttheory/go-apple-mdm/acme/attest/attesttest"
	"github.com/deploymenttheory/go-apple-mdm/acme/inmem"
	"github.com/deploymenttheory/go-apple-mdm/acme/jose"
	"github.com/deploymenttheory/go-apple-mdm/ca"
	"github.com/deploymenttheory/go-apple-mdm/event"
	"github.com/deploymenttheory/go-apple-mdm/internal/clock"
)

// The device every fixture is about: the serial number and UDID the default
// client identifier was minted for, so a test that changes one of them is
// deliberately describing a different device.
const (
	testSerial     = "C02XX1234567"
	testUDID       = "00008030-000A1B2C3D4E5F60"
	testIdentifier = "one-time-client-identifier"
	testCommonName = "Test Device"
)

// authority is the issuing certificate authority the fixtures share.
type authority struct {
	cert *x509.Certificate
	key  *rsa.PrivateKey
}

// sharedCA builds the issuing authority once for the whole package.
// Generating an RSA CA costs more than every other part of a fixture put
// together, and the authority is immutable once built, so sharing it is
// safe and keeps the suite quick.
var sharedCA = sync.OnceValues(func() (*authority, error) {
	cert, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{
		Subject: pkix.Name{CommonName: "go-apple-mdm ACME test CA"},
	})
	if err != nil {
		return nil, err
	}
	return &authority{cert: cert, key: key}, nil
})

// fixture is one ACME server behind a test HTTP server, with everything a
// test needs to reach past the wire: the store it writes to, the clock it
// reads, and the attestation authority it trusts.
type fixture struct {
	t      *testing.T
	clock  *clock.Fake
	store  *inmem.Store
	attest *attesttest.CA
	// ids is the default identifier source, a map a test may add to before
	// it orders.
	ids    acme.StaticIdentifiers
	events *eventLog
	server *acme.Server
	ts     *httptest.Server
	base   string
}

// newFixture builds a server. Each tweak sees the configuration before it
// is validated, so a test can change one setting and inherit the rest.
func newFixture(t *testing.T, tweak ...func(*acme.Config)) *fixture {
	t.Helper()
	root, err := sharedCA()
	if err != nil {
		t.Fatalf("issuing CA: %v", err)
	}
	signer, err := ca.NewLocal(root.cert, root.key)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	attestCA, err := attesttest.NewCA()
	if err != nil {
		t.Fatalf("attestation authority: %v", err)
	}
	f := &fixture{
		t: t,
		// The fake clock starts at the wall clock so that certificates
		// minted by the shared authority, whose own validity comes from
		// the real clock, are inside their window.
		clock:  clock.NewFake(time.Now()),
		store:  inmem.New(),
		attest: attestCA,
		ids:    acme.StaticIdentifiers{testIdentifier: defaultBinding()},
		events: newEventLog(),
	}
	// The base URL has to be the URL the server publishes, and that is not
	// known until something is listening, so the listener comes first and
	// the handler is attached before it is served.
	ts := httptest.NewUnstartedServer(nil)
	t.Cleanup(ts.Close)
	f.base = "http://" + ts.Listener.Addr().String()
	cfg := acme.Config{
		BaseURL:     f.base,
		Store:       f.store,
		Signer:      signer,
		CAPolicy:    ca.Policy{ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}},
		Identifiers: f.ids,
		Anchors:     attestCA.Anchors(),
		Clock:       f.clock,
		Bus:         f.events.bus,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	for _, m := range tweak {
		m(&cfg)
	}
	srv, err := acme.New(cfg)
	if err != nil {
		t.Fatalf("acme.New: %v", err)
	}
	f.server = srv
	ts.Config.Handler = srv.Handler()
	ts.Start()
	f.ts = ts
	return f
}

// defaultBinding is what the default client identifier resolves to.
func defaultBinding() acme.Binding {
	return acme.Binding{
		Serial:       testSerial,
		UDID:         testUDID,
		CommonName:   testCommonName,
		Organization: []string{"Deployment Theory"},
	}
}

// deviceProperties is a plausible attestation for the default device.
func deviceProperties() attest.Properties {
	enabled := true
	return attest.Properties{
		SerialNumber: testSerial,
		UDID:         testUDID,
		OSVersion:    "26.0",
		SEPOSVersion: "26.0",
		SecureBoot:   attest.SecureBootFull,
		SIPEnabled:   &enabled,
	}
}

// url builds an absolute URL for an endpoint under the default prefix.
func (f *fixture) url(elem string) string { return f.base + acme.DefaultPrefix + elem }

// eventLog records everything the server publishes.
type eventLog struct {
	bus *event.Bus
	mu  sync.Mutex
	got []event.Event
}

func newEventLog() *eventLog {
	l := &eventLog{bus: event.New()}
	l.bus.Subscribe(event.All, func(_ context.Context, e event.Event) error {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.got = append(l.got, e)
		return nil
	})
	return l
}

// count reports how many events of a type were published.
func (l *eventLog) count(t event.Type) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, e := range l.got {
		if e.Type == t {
			n++
		}
	}
	return n
}

// response is a finished exchange with the body already read, so no test
// has to remember to close anything.
type response struct {
	status int
	header http.Header
	body   []byte
}

func (f *fixture) send(req *http.Request) *response {
	f.t.Helper()
	res, err := f.ts.Client().Do(req)
	if err != nil {
		f.t.Fatalf("request %s %s: %v", req.Method, req.URL, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		f.t.Fatalf("read body: %v", err)
	}
	return &response{status: res.StatusCode, header: res.Header, body: body}
}

func (f *fixture) request(method, target string, body []byte) *http.Request {
	f.t.Helper()
	req, err := http.NewRequestWithContext(f.t.Context(), method, target, bytes.NewReader(body))
	if err != nil {
		f.t.Fatalf("build request: %v", err)
	}
	return req
}

func (f *fixture) get(target string) *response {
	f.t.Helper()
	return f.send(f.request(http.MethodGet, target, nil))
}

func (f *fixture) head(target string) *response {
	f.t.Helper()
	return f.send(f.request(http.MethodHead, target, nil))
}

// postRaw sends a body verbatim, for the requests a signer would not
// produce.
func (f *fixture) postRaw(
	target, contentType string,
	body []byte,
	mutate ...func(*http.Request),
) *response {
	f.t.Helper()
	req := f.request(http.MethodPost, target, body)
	req.Header.Set("Content-Type", contentType)
	for _, m := range mutate {
		m(req)
	}
	return f.send(req)
}

// nonce fetches a fresh nonce from new-nonce.
func (f *fixture) nonce() string {
	f.t.Helper()
	res := f.get(f.url("/new-nonce"))
	value := res.header.Get("Replay-Nonce")
	if value == "" {
		f.t.Fatalf("new-nonce returned no nonce (status %d)", res.status)
	}
	return value
}

// signed signs a JWS and posts it. An empty header URL means the target and
// an empty nonce means a fresh one, so a test names only what it is about
// to break.
func (f *fixture) signed(
	target string,
	key crypto.Signer,
	hdr jose.Header,
	payload []byte,
	mutate ...func(*http.Request),
) *response {
	f.t.Helper()
	if hdr.URL == "" {
		hdr.URL = target
	}
	if hdr.Nonce == "" {
		hdr.Nonce = f.nonce()
	}
	body, err := jose.Sign(key, hdr, payload)
	if err != nil {
		f.t.Fatalf("sign: %v", err)
	}
	return f.postRaw(target, acme.ContentTypeJOSE, body, mutate...)
}

// account is a registered ACME account and the key it was registered with.
type account struct {
	f   *fixture
	key crypto.Signer
	url string
	id  string
}

// register creates an account with a fresh key.
func (f *fixture) register() *account {
	f.t.Helper()
	return f.registerKey(newKey(f.t))
}

func (f *fixture) registerKey(key crypto.Signer) *account {
	f.t.Helper()
	res := f.newAccountRequest(key, map[string]any{"termsOfServiceAgreed": true})
	if res.status != http.StatusCreated && res.status != http.StatusOK {
		f.t.Fatalf("new-account: status %d, body %s", res.status, res.body)
	}
	location := res.header.Get("Location")
	if location == "" {
		f.t.Fatal("new-account returned no Location header")
	}
	return &account{f: f, key: key, url: location, id: path.Base(location)}
}

// newAccountRequest posts to new-account with the key embedded, which is
// the one endpoint that takes a jwk rather than a kid.
func (f *fixture) newAccountRequest(key crypto.Signer, payload any) *response {
	f.t.Helper()
	jwk, err := jose.JWKFromPublic(key.Public())
	if err != nil {
		f.t.Fatalf("jwk: %v", err)
	}
	return f.signed(f.url("/new-account"), key, jose.Header{JWK: jwk}, mustJSON(f.t, payload))
}

// post signs a request with the account key. A nil payload is a
// POST-as-GET.
func (a *account) post(target string, payload any) *response {
	a.f.t.Helper()
	return a.postAt(target, target, payload)
}

// postAt signs a request whose url header is not the target. Only the
// paging link needs it, because the server compares the header against the
// path alone: see TestAccountOrders/NextLinkCannotBeSigned.
func (a *account) postAt(target, urlHeader string, payload any) *response {
	a.f.t.Helper()
	var body []byte
	if payload != nil {
		body = mustJSON(a.f.t, payload)
	}
	return a.f.signed(target, a.key, jose.Header{KeyID: a.url, URL: urlHeader}, body)
}

// flow is one order in progress with the URLs its account was handed.
type flow struct {
	f        *fixture
	acct     *account
	orderURL string
	authzURL string
	chalURL  string
	finalize string
	token    string
	// key is the device key the certificate will be issued for, and the key
	// an attestation covers.
	key crypto.Signer
}

// begin registers an account and orders the identifier, following the
// authorization link the way a client does.
func (f *fixture) begin(identifier string) *flow {
	f.t.Helper()
	return f.beginAs(f.register(), identifier)
}

func (f *fixture) beginAs(a *account, identifier string) *flow {
	f.t.Helper()
	res := a.post(f.url("/new-order"), orderRequest(identifier))
	requireStatus(f.t, res, http.StatusCreated)
	body := decode[orderJSON](f.t, res)
	fl := &flow{
		f: f, acct: a,
		orderURL: res.header.Get("Location"),
		authzURL: body.Authorizations[0],
		finalize: body.Finalize,
		key:      newKey(f.t),
	}
	authz := decode[authzJSON](f.t, a.post(fl.authzURL, nil))
	if len(authz.Challenges) != 1 {
		f.t.Fatalf("authorization offered %d challenges, want 1", len(authz.Challenges))
	}
	fl.chalURL = authz.Challenges[0].URL
	fl.token = authz.Challenges[0].Token
	return fl
}

// attestation mints one for this challenge, covering the device key.
func (fl *flow) attestation(props attest.Properties) []byte {
	fl.f.t.Helper()
	raw, err := fl.f.attest.ObjectForToken(fl.token, props, fl.key.Public())
	if err != nil {
		fl.f.t.Fatalf("attestation: %v", err)
	}
	return raw
}

// answer posts an attestation object as the challenge response.
func (fl *flow) answer(object []byte) *response {
	fl.f.t.Helper()
	return fl.acct.post(fl.chalURL, map[string]string{
		"attObj": base64.RawURLEncoding.EncodeToString(object),
	})
}

// pass answers the challenge with a good attestation for the default
// device and insists it worked.
func (fl *flow) pass() *flow {
	fl.f.t.Helper()
	res := fl.answer(fl.attestation(deviceProperties()))
	requireStatus(fl.f.t, res, http.StatusOK)
	return fl
}

// finalizeWith posts a certificate request for the given key.
func (fl *flow) finalizeWith(key crypto.Signer, subject pkix.Name) *response {
	fl.f.t.Helper()
	return fl.acct.post(fl.finalize, map[string]string{
		"csr": base64.RawURLEncoding.EncodeToString(csrDER(fl.f.t, key, subject)),
	})
}

// order reads the order back with a POST-as-GET.
func (fl *flow) order() orderJSON {
	fl.f.t.Helper()
	return decode[orderJSON](fl.f.t, fl.acct.post(fl.orderURL, nil))
}

// challenge reads the challenge back with a POST-as-GET.
func (fl *flow) challenge() challengeJSON {
	fl.f.t.Helper()
	return decode[challengeJSON](fl.f.t, fl.acct.post(fl.chalURL, nil))
}

// authorization reads the authorization back with a POST-as-GET.
func (fl *flow) authorization() authzJSON {
	fl.f.t.Helper()
	return decode[authzJSON](fl.f.t, fl.acct.post(fl.authzURL, nil))
}

// The wire shapes, spelled out here so a test asserts against what a device
// would actually read rather than against the server's own types.

type orderJSON struct {
	Status         string            `json:"status"`
	Expires        string            `json:"expires"`
	Identifiers    []acme.Identifier `json:"identifiers"`
	Authorizations []string          `json:"authorizations"`
	Finalize       string            `json:"finalize"`
	Certificate    string            `json:"certificate"`
	Error          *wireProblem      `json:"error"`
}

type authzJSON struct {
	Status     string          `json:"status"`
	Expires    string          `json:"expires"`
	Identifier acme.Identifier `json:"identifier"`
	Challenges []challengeJSON `json:"challenges"`
}

type challengeJSON struct {
	Type      string       `json:"type"`
	URL       string       `json:"url"`
	Status    string       `json:"status"`
	Token     string       `json:"token"`
	Validated string       `json:"validated"`
	Error     *wireProblem `json:"error"`
}

type accountJSON struct {
	Status  string   `json:"status"`
	Contact []string `json:"contact"`
	Orders  string   `json:"orders"`
}

type ordersJSON struct {
	Orders []string `json:"orders"`
}

type directoryJSON struct {
	NewNonce   string            `json:"newNonce"`
	NewAccount string            `json:"newAccount"`
	NewOrder   string            `json:"newOrder"`
	Meta       map[string]string `json:"meta"`
}

type wireProblem struct {
	Type        string   `json:"type"`
	Detail      string   `json:"detail"`
	Status      int      `json:"status"`
	Algorithms  []string `json:"algorithms"`
	Subproblems []struct {
		Type       string           `json:"type"`
		Detail     string           `json:"detail"`
		Identifier *acme.Identifier `json:"identifier"`
	} `json:"subproblems"`
}

// orderRequest is the new-order payload for one permanent identifier.
func orderRequest(identifier string) map[string]any {
	return map[string]any{
		"identifiers": []acme.Identifier{
			{Type: acme.IdentifierPermanent, Value: identifier},
		},
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

func decode[T any](t *testing.T, r *response) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(r.body, &v); err != nil {
		t.Fatalf("decode %T from %s: %v", v, r.body, err)
	}
	return v
}

func requireStatus(t *testing.T, r *response, want int) {
	t.Helper()
	if r.status != want {
		t.Fatalf("status = %d, want %d; body %s", r.status, want, r.body)
	}
}

// requireProblem insists the response is the ACME problem document of a
// given kind and hands it back for the checks that are specific to a test.
func requireProblem(t *testing.T, r *response, kind string) wireProblem {
	t.Helper()
	if ct := r.header.Get("Content-Type"); ct != acme.ContentTypeProblem {
		t.Errorf("content type = %q, want %q", ct, acme.ContentTypeProblem)
	}
	var p wireProblem
	if err := json.Unmarshal(r.body, &p); err != nil {
		t.Fatalf("decode problem from %s: %v", r.body, err)
	}
	if want := acme.ProblemPrefix + kind; p.Type != want {
		t.Fatalf("problem type = %q, want %q (status %d, detail %q)", p.Type, want, r.status, p.Detail)
	}
	if p.Status != r.status {
		t.Errorf("problem status member = %d, HTTP status = %d", p.Status, r.status)
	}
	return p
}

// newKey is the key type Apple's clients use.
func newKey(t *testing.T) crypto.Signer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return key
}

// csrDER builds a certificate request carrying the permanent identifier as
// a subject alternative name, which is the shape a device sends.
func csrDER(t *testing.T, key crypto.Signer, subject pkix.Name) []byte {
	t.Helper()
	otherName, err := ca.PermanentIdentifier(testIdentifier)
	if err != nil {
		t.Fatalf("permanent identifier: %v", err)
	}
	ext, ok, err := ca.SANExtension(ca.SANs{OtherNames: []ca.OtherName{otherName}}, false)
	if err != nil {
		t.Fatalf("san extension: %v", err)
	}
	tmpl := &x509.CertificateRequest{Subject: subject}
	if ok {
		tmpl.ExtraExtensions = []pkix.Extension{ext}
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		t.Fatalf("csr: %v", err)
	}
	return der
}

// idOf is the record identifier at the end of a URL the server handed out.
func idOf(u string) string { return path.Base(u) }

// leafOf reads the first certificate out of a PEM chain.
func leafOf(t *testing.T, chain []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(chain)
	if block == nil {
		t.Fatalf("no PEM in %q", chain)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return cert
}
