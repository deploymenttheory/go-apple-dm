package proxyserver_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/ddm"
	"github.com/deploymenttheory/go-apple-dm/ddm/adapter/inproc"
	"github.com/deploymenttheory/go-apple-dm/ddm/adapter/internal/proxywire"
	"github.com/deploymenttheory/go-apple-dm/ddm/adapter/proxyserver"
	"github.com/deploymenttheory/go-apple-dm/ddm/inmem"
	"github.com/deploymenttheory/go-apple-dm/internal/testpki"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/plist"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/service"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// stub records the last call and answers from a table keyed by endpoint.
type stub struct {
	responses map[string]ddm.Response
	errs      map[string]error
	mu        sync.Mutex
	last      lastCall
}

type lastCall struct {
	id       mdm.EnrollmentID
	endpoint string
	data     []byte
}

func (s *stub) seen() lastCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.last
}

func (s *stub) Handle(_ context.Context, id mdm.EnrollmentID, endpoint string, data []byte) (ddm.Response, error) {
	s.mu.Lock()
	s.last = lastCall{id: id, endpoint: endpoint, data: data}
	s.mu.Unlock()
	if err := s.errs[endpoint]; err != nil {
		return ddm.Response{}, err
	}
	if r, ok := s.responses[endpoint]; ok {
		return r, nil
	}
	return ddm.Response{Body: []byte(`{"endpoint":"` + endpoint + `"}`), Status: 200}, nil
}

func newStub() *stub {
	return &stub{
		responses: map[string]ddm.Response{
			"declaration/configuration/missing": {Status: 404},
			"status":                            {Status: 200},
		},
		errs: map[string]error{
			"bogus":   ddm.ErrBadEndpoint,
			"big":     ddm.ErrStatusTooLarge,
			"garbled": ddm.ErrStatusMalformed,
			"failing": errors.New("store down"),
		},
	}
}

func mustHandler(t *testing.T, cfg proxyserver.Config) http.Handler {
	t.Helper()
	if cfg.Logger == nil {
		cfg.Logger = quiet
	}
	h, err := proxyserver.Handler(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// dmPlist builds a DeclarativeManagement check-in plist; a nil value in
// fields removes that key (the encoder cannot encode nil).
func dmPlist(t *testing.T, fields map[string]any) []byte {
	t.Helper()
	f := map[string]any{"MessageType": "DeclarativeManagement", "UDID": "D1", "Endpoint": "tokens"}
	for k, v := range fields {
		if v == nil {
			delete(f, k)
			continue
		}
		f[k] = v
	}
	raw, err := plist.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type reqOpt func(*http.Request)

func withHeader(k, v string) reqOpt { return func(r *http.Request) { r.Header.Set(k, v) } }

// post sends body to h as the mdm role would, with overrides.
func post(h http.Handler, body []byte, opts ...reqOpt) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, proxywire.Path, bytes.NewReader(body))
	r.Header.Set("Content-Type", proxywire.ContentType)
	for _, o := range opts {
		o(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHandler(t *testing.T) {
	t.Parallel()
	if _, err := proxyserver.Handler(proxyserver.Config{}); !errors.Is(err, proxyserver.ErrNoBackend) {
		t.Fatalf("no backend: %v", err)
	}
	// The default logger is used when none is configured.
	h, err := proxyserver.Handler(proxyserver.Config{Backend: newStub()})
	if err != nil || h == nil {
		t.Fatalf("Handler: %v", err)
	}
}

func TestRoutes(t *testing.T) {
	t.Parallel()
	h := mustHandler(t, proxyserver.Config{Backend: newStub()})
	t.Run("OnlyPost", func(t *testing.T) {
		t.Parallel()
		for _, method := range []string{http.MethodGet, http.MethodPut, http.MethodDelete, http.MethodHead} {
			r := httptest.NewRequest(method, proxywire.Path, nil)
			r.Header.Set("Content-Type", proxywire.ContentType)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s: %d", method, w.Code)
			}
		}
		if w := post(h, dmPlist(t, nil)); w.Code != http.StatusOK || w.Body.String() != `{"endpoint":"tokens"}` {
			t.Fatalf("POST: %d %s", w.Code, w.Body)
		}
	})
	t.Run("WrongPath404", func(t *testing.T) {
		t.Parallel()
		for _, p := range []string{"/", "/v1", "/v1/declarative-management/tokens", "/v2/declarative-management", "/declaration/configuration/x"} {
			r := httptest.NewRequest(http.MethodPost, p, bytes.NewReader(dmPlist(t, nil)))
			r.Header.Set("Content-Type", proxywire.ContentType)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusNotFound {
				t.Errorf("%s: %d", p, w.Code)
			}
		}
	})
	t.Run("WrongContentType415", func(t *testing.T) {
		t.Parallel()
		for _, ct := range []string{"", "application/json", "application/xml", "text/plain; charset=utf-8", "garbage;;"} {
			if w := post(h, dmPlist(t, nil), withHeader("Content-Type", ct)); w.Code != http.StatusUnsupportedMediaType {
				t.Errorf("%q: %d", ct, w.Code)
			}
		}
		// Parameters on the right type are tolerated.
		if w := post(h, dmPlist(t, nil), withHeader("Content-Type", proxywire.ContentType+"; charset=utf-8")); w.Code != http.StatusOK {
			t.Fatalf("with charset: %d", w.Code)
		}
	})
}

func TestSignature(t *testing.T) {
	t.Parallel()
	recv, send := []byte("recv-key"), []byte("send-key")
	h := mustHandler(t, proxyserver.Config{Backend: newStub(), RecvKey: recv, SendKey: send})
	body := dmPlist(t, nil)
	t.Run("Signed", func(t *testing.T) {
		t.Parallel()
		w := post(h, body, withHeader(proxywire.HeaderSignature, proxywire.Sign(recv, body)))
		if w.Code != http.StatusOK || w.Body.String() != `{"endpoint":"tokens"}` {
			t.Fatalf("signed request: %d %s", w.Code, w.Body)
		}
	})
	t.Run("MissingRejected", func(t *testing.T) {
		t.Parallel()
		w := post(h, body)
		if w.Code != http.StatusUnauthorized || w.Body.Len() != 0 {
			t.Fatalf("unsigned request: %d %s", w.Code, w.Body)
		}
	})
	t.Run("WrongKeyRejected", func(t *testing.T) {
		t.Parallel()
		w := post(h, body, withHeader(proxywire.HeaderSignature, proxywire.Sign([]byte("other"), body)))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("wrong key: %d", w.Code)
		}
		// A signature over different bytes is rejected too.
		w = post(h, body, withHeader(proxywire.HeaderSignature, proxywire.Sign(recv, dmPlist(t, map[string]any{"Endpoint": "status"}))))
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("signature over other bytes: %d", w.Code)
		}
	})
	t.Run("ResponseSigned", func(t *testing.T) {
		t.Parallel()
		w := post(h, body, withHeader(proxywire.HeaderSignature, proxywire.Sign(recv, body)))
		if err := proxywire.VerifyResponse(send, w.Header().Get(proxywire.HeaderSignature), w.Code, w.Body.Bytes()); err != nil {
			t.Fatalf("response signature: %v", err)
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("headers: %v", w.Header())
		}
		// Every status is signed, including the rejections.
		w = post(h, body)
		if err := proxywire.VerifyResponse(send, w.Header().Get(proxywire.HeaderSignature), w.Code, w.Body.Bytes()); err != nil || w.Code != http.StatusUnauthorized {
			t.Fatalf("401 signature: %v (%d)", err, w.Code)
		}
	})
	t.Run("ResponseSignedOn404", func(t *testing.T) {
		t.Parallel()
		b := dmPlist(t, map[string]any{"Endpoint": "declaration/configuration/missing"})
		w := post(h, b, withHeader(proxywire.HeaderSignature, proxywire.Sign(recv, b)))
		if w.Code != http.StatusNotFound || w.Body.Len() != 0 {
			t.Fatalf("404: %d %s", w.Code, w.Body)
		}
		if err := proxywire.VerifyResponse(send, w.Header().Get(proxywire.HeaderSignature), w.Code, nil); err != nil {
			t.Fatalf("404 signature: %v", err)
		}
		if w.Header().Get("Content-Type") != "" {
			t.Fatalf("empty body carries a content type: %q", w.Header().Get("Content-Type"))
		}
		// Empty 200 for status is signed over the empty body as well.
		b = dmPlist(t, map[string]any{"Endpoint": "status", "Data": []byte(`{}`)})
		w = post(h, b, withHeader(proxywire.HeaderSignature, proxywire.Sign(recv, b)))
		if err := proxywire.VerifyResponse(send, w.Header().Get(proxywire.HeaderSignature), w.Code, nil); err != nil || w.Code != http.StatusOK || w.Body.Len() != 0 {
			t.Fatalf("empty 200 signature: %v (%d %q)", err, w.Code, w.Body)
		}
	})
	t.Run("UnsignedWhenNoKey", func(t *testing.T) {
		t.Parallel()
		plain := mustHandler(t, proxyserver.Config{Backend: newStub()})
		w := post(plain, body)
		if w.Code != http.StatusOK || w.Header().Get(proxywire.HeaderSignature) != "" {
			t.Fatalf("no keys: %d %v", w.Code, w.Header())
		}
	})
}

// clientIdentity turns a testpki identity into a TLS client certificate.
func clientIdentity(t *testing.T, ca *testpki.CA, name string) tls.Certificate {
	t.Helper()
	id, err := ca.Issue(name, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{id.Cert.Raw}, PrivateKey: id.Key}
}

// tlsClient returns srv's client with the given client certificate.
func tlsClient(t *testing.T, srv *httptest.Server, certs ...tls.Certificate) *http.Client {
	t.Helper()
	c := srv.Client()
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport is %T", c.Transport)
	}
	tr.TLSClientConfig.Certificates = certs
	return c
}

// tlsServer starts h behind TLS with cfg, silencing handshake log noise.
func tlsServer(t *testing.T, h http.Handler, cfg *tls.Config) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ErrorLog = log.New(io.Discard, "", 0)
	srv.TLS = cfg
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func send(t *testing.T, c *http.Client, url string, body []byte, opts ...reqOpt) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url+proxywire.Path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", proxywire.ContentType)
	for _, o := range opts {
		o(req)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

// handshakeFails reports whether c cannot even complete a request to url.
func handshakeFails(t *testing.T, c *http.Client, url string, body []byte) bool {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url+proxywire.Path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", proxywire.ContentType)
	resp, err := c.Do(req)
	if err != nil {
		return true
	}
	_ = resp.Body.Close()
	return false
}

func TestAuth(t *testing.T) {
	t.Parallel()
	body := dmPlist(t, nil)
	t.Run("Bearer", func(t *testing.T) {
		t.Parallel()
		h := mustHandler(t, proxyserver.Config{Backend: newStub(), Auth: proxyserver.BearerAuth("s3cret")})
		if w := post(h, body, withHeader("Authorization", "Bearer s3cret")); w.Code != http.StatusOK {
			t.Fatalf("right token: %d", w.Code)
		}
		for name, hdr := range map[string]string{"missing": "", "wrong": "Bearer other", "prefix": "Bearer s3cre", "longer": "Bearer s3cret!", "basic": "Basic s3cret", "lowercase": "bearer s3cret"} {
			if w := post(h, body, withHeader("Authorization", hdr)); w.Code != http.StatusUnauthorized || w.Body.Len() != 0 {
				t.Errorf("%s (%q): %d %s", name, hdr, w.Code, w.Body)
			}
		}
		// Auth runs before routing: a wrong path with a bad token is 401.
		r := httptest.NewRequest(http.MethodGet, "/nope", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated wrong path: %d", w.Code)
		}
		// An empty configured token accepts nothing.
		empty := mustHandler(t, proxyserver.Config{Backend: newStub(), Auth: proxyserver.BearerAuth("")})
		if w := post(empty, body, withHeader("Authorization", "Bearer ")); w.Code != http.StatusUnauthorized {
			t.Fatalf("empty token: %d", w.Code)
		}
	})
	t.Run("MTLS", func(t *testing.T) {
		t.Parallel()
		ca, err := testpki.NewCA("ddm-clients")
		if err != nil {
			t.Fatal(err)
		}
		other, err := testpki.NewCA("someone-else")
		if err != nil {
			t.Fatal(err)
		}
		h := mustHandler(t, proxyserver.Config{Backend: newStub(), ClientCAs: ca.Pool()})
		srv := tlsServer(t, h, proxyserver.TLSConfig(tls.Certificate{}, ca.Pool()))
		if resp := send(t, tlsClient(t, srv, clientIdentity(t, ca, "mdm-role")), srv.URL, body); resp.StatusCode != http.StatusOK {
			t.Fatalf("pinned client certificate: %d", resp.StatusCode)
		}
		// No client certificate, or one from another CA: the TLS layer
		// refuses the handshake.
		if !handshakeFails(t, tlsClient(t, srv), srv.URL, body) {
			t.Fatal("no client certificate was accepted")
		}
		if !handshakeFails(t, tlsClient(t, srv, clientIdentity(t, other, "impostor")), srv.URL, body) {
			t.Fatal("foreign client certificate was accepted")
		}
		// The handler pins independently of the listener: a listener that
		// verified against a different pool still gets 401.
		lenient := tlsServer(t, h, proxyserver.TLSConfig(tls.Certificate{}, other.Pool()))
		if resp := send(t, tlsClient(t, lenient, clientIdentity(t, other, "impostor")), lenient.URL, body); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("certificate outside ClientCAs: %d", resp.StatusCode)
		}
		// A listener that does not verify certificates leaves
		// VerifiedChains empty, which is 401 as well.
		optional := tlsServer(t, h, &tls.Config{ClientAuth: tls.RequestClientCert, MinVersion: tls.VersionTLS12})
		if resp := send(t, tlsClient(t, optional, clientIdentity(t, ca, "mdm-role")), optional.URL, body); resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unverified certificate: %d", resp.StatusCode)
		}
		// Plain HTTP never satisfies ClientCAs.
		if w := post(h, body); w.Code != http.StatusUnauthorized {
			t.Fatalf("plain HTTP with ClientCAs: %d", w.Code)
		}
	})
	t.Run("Rejected", func(t *testing.T) {
		t.Parallel()
		vetoed := errors.New("no")
		h := mustHandler(t, proxyserver.Config{Backend: newStub(), Auth: func(*http.Request) error { return vetoed }})
		if w := post(h, body); w.Code != http.StatusUnauthorized || w.Body.Len() != 0 {
			t.Fatalf("auth veto: %d %s", w.Code, w.Body)
		}
	})
}

func TestTLSConfig(t *testing.T) {
	t.Parallel()
	ca, err := testpki.NewCA("x")
	if err != nil {
		t.Fatal(err)
	}
	cfg := proxyserver.TLSConfig(tls.Certificate{}, ca.Pool())
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert || cfg.ClientCAs == nil || len(cfg.Certificates) != 0 || cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("without server cert: %+v", cfg)
	}
	cfg = proxyserver.TLSConfig(clientIdentity(t, ca, "srv"), ca.Pool())
	if len(cfg.Certificates) != 1 {
		t.Fatalf("with server cert: %d certificates", len(cfg.Certificates))
	}
}

func TestCheckin(t *testing.T) {
	t.Parallel()
	t.Run("DeviceChannel", func(t *testing.T) {
		t.Parallel()
		s := newStub()
		h := mustHandler(t, proxyserver.Config{Backend: s})
		data := []byte(`{"StatusItems":{}}`)
		w := post(h, dmPlist(t, map[string]any{"UDID": "DEV-1", "Endpoint": "status", "Data": data}))
		if w.Code != http.StatusOK || w.Body.Len() != 0 {
			t.Fatalf("status: %d %s", w.Code, w.Body)
		}
		want := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "DEV-1"}
		if last := s.seen(); last.id != want || last.endpoint != "status" || !bytes.Equal(last.data, data) {
			t.Fatalf("backend saw %+v", last)
		}
		// User Enrollment identifies the device by EnrollmentID.
		raw, err := plist.Marshal(map[string]any{"MessageType": "DeclarativeManagement", "EnrollmentID": "UE-1", "Endpoint": "tokens"})
		if err != nil {
			t.Fatal(err)
		}
		if w := post(h, raw); w.Code != http.StatusOK {
			t.Fatalf("user enrollment: %d", w.Code)
		}
		if want := (mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: "UE-1"}); s.seen().id != want {
			t.Fatalf("user enrollment id: %+v", s.seen().id)
		}
	})
	t.Run("UserChannel", func(t *testing.T) {
		t.Parallel()
		s := newStub()
		h := mustHandler(t, proxyserver.Config{Backend: s})
		w := post(h, dmPlist(t, map[string]any{"UDID": "DEV-1", "UserID": "U-1", "UserShortName": "alice", "Endpoint": "declaration-items"}))
		if w.Code != http.StatusOK {
			t.Fatalf("user channel: %d", w.Code)
		}
		want := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "DEV-1:U-1", ParentID: "DEV-1"}
		if last := s.seen(); last.id != want || last.endpoint != "declaration-items" {
			t.Fatalf("backend saw %+v, want %+v", last, want)
		}
		// Shared iPad resolves through UserShortName.
		w = post(h, dmPlist(t, map[string]any{"UDID": "DEV-1", "UserID": mdm.SharedIPadUserID, "UserShortName": "bob"}))
		if w.Code != http.StatusOK {
			t.Fatalf("shared ipad: %d", w.Code)
		}
		if want := (mdm.EnrollmentID{Channel: mdm.ChannelSharedIPadUser, ID: "DEV-1:bob", ParentID: "DEV-1"}); s.seen().id != want {
			t.Fatalf("shared ipad id: %+v", s.seen().id)
		}
	})
	t.Run("Malformed400", func(t *testing.T) {
		t.Parallel()
		h := mustHandler(t, proxyserver.Config{Backend: newStub()})
		for name, body := range map[string][]byte{
			"empty":       {},
			"not a plist": []byte("hello"),
			"no identity": dmPlist(t, map[string]any{"UDID": nil}),
			"unknown":     dmPlist(t, map[string]any{"MessageType": "Nope"}),
			"shared ipad": dmPlist(t, map[string]any{"UserID": mdm.SharedIPadUserID}),
		} {
			if w := post(h, body); w.Code != http.StatusBadRequest || w.Body.Len() != 0 {
				t.Errorf("%s: %d %s", name, w.Code, w.Body)
			}
		}
		// Decoder limits apply to the plist.
		tight := mustHandler(t, proxyserver.Config{Backend: newStub(), Decoder: plist.Decoder{MaxBytes: 16}})
		if w := post(tight, dmPlist(t, nil)); w.Code != http.StatusBadRequest {
			t.Fatalf("decoder limit: %d", w.Code)
		}
	})
	t.Run("NotDeclarativeManagement400", func(t *testing.T) {
		t.Parallel()
		s := newStub()
		h := mustHandler(t, proxyserver.Config{Backend: s})
		raw, err := plist.Marshal(map[string]any{"MessageType": "CheckOut", "UDID": "D1"})
		if err != nil {
			t.Fatal(err)
		}
		if w := post(h, raw); w.Code != http.StatusBadRequest {
			t.Fatalf("CheckOut: %d", w.Code)
		}
		if last := s.seen(); last.endpoint != "" {
			t.Fatalf("backend called for a %q", last.endpoint)
		}
	})
	t.Run("TooLarge413", func(t *testing.T) {
		t.Parallel()
		h := mustHandler(t, proxyserver.Config{Backend: newStub(), MaxBody: 64})
		if w := post(h, dmPlist(t, map[string]any{"Data": bytes.Repeat([]byte("x"), 64)})); w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("over MaxBody: %d", w.Code)
		}
		// Reads that fail for another reason are 400.
		r := httptest.NewRequest(http.MethodPost, proxywire.Path, failingReader{})
		r.Header.Set("Content-Type", proxywire.ContentType)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("read failure: %d", w.Code)
		}
	})
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestBackendErrors(t *testing.T) {
	t.Parallel()
	h := mustHandler(t, proxyserver.Config{Backend: newStub(), SendKey: []byte("k")})
	t.Run("404", func(t *testing.T) {
		t.Parallel()
		w := post(h, dmPlist(t, map[string]any{"Endpoint": "declaration/configuration/missing"}))
		if w.Code != http.StatusNotFound || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "" {
			t.Fatalf("404: %d %s %v", w.Code, w.Body, w.Header())
		}
	})
	t.Run("400", func(t *testing.T) {
		t.Parallel()
		for _, ep := range []string{"bogus", "big", "garbled"} {
			if w := post(h, dmPlist(t, map[string]any{"Endpoint": ep})); w.Code != http.StatusBadRequest || w.Body.Len() != 0 {
				t.Errorf("%s: %d %s", ep, w.Code, w.Body)
			}
		}
	})
	t.Run("500", func(t *testing.T) {
		t.Parallel()
		w := post(h, dmPlist(t, map[string]any{"Endpoint": "failing"}))
		if w.Code != http.StatusInternalServerError || w.Body.Len() != 0 {
			t.Fatalf("500: %d %q", w.Code, w.Body)
		}
		if err := proxywire.VerifyResponse([]byte("k"), w.Header().Get(proxywire.HeaderSignature), w.Code, nil); err != nil {
			t.Fatalf("500 is signed: %v", err)
		}
	})
}

// TestHandleParity drives one engine through inproc and through
// proxyserver and expects the same status and body for every endpoint.
func TestHandleParity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	e, err := ddm.New(ddm.Config{Store: inmem.New()})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.PutDeclaration(ctx, []byte(`{"Type":"com.apple.management.organization-info","Identifier":"org","Payload":{"Name":"Acme"}}`)); err != nil {
		t.Fatal(err)
	}
	dev := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "D1"}
	if _, err := e.AssignDeclaration(ctx, dev, "org"); err != nil {
		t.Fatal(err)
	}
	direct := inproc.Handler(e)
	remote := mustHandler(t, proxyserver.Config{Backend: e})
	codeStatus := map[service.Code]int{service.CodeBadRequest: http.StatusBadRequest, service.CodeInternal: http.StatusInternalServerError}
	for _, tc := range []struct {
		endpoint string
		data     []byte
		want     int
	}{
		{"tokens", nil, 200},
		{"declaration-items", nil, 200},
		{"declaration/management/org", nil, 200},
		{"declaration/management/nope", nil, 404},
		{"declaration/configuration/org", nil, 404},
		{"status", []byte(`{"StatusItems":{"device":{"model":{"family":"Mac"}}}}`), 200},
		{"status", nil, 400},
		{"status", []byte(`{"StatusItems":`), 400},
		{"", nil, 400},
		{"declaration/x/y", nil, 400},
		{"declaration/configuration/" + strings.Repeat("i", 65), nil, 400},
	} {
		fields := map[string]any{"Endpoint": tc.endpoint}
		if tc.data != nil {
			fields["Data"] = tc.data
		}
		raw := dmPlist(t, fields)
		ck, err := mdm.DecodeCheckin(raw)
		if err != nil {
			t.Fatal(err)
		}
		m, _ := ck.Message.(*checkin.DeclarativeManagement)
		got, err := direct(ctx, &mdm.Request{}, ck, m)
		status, body := got.Status, got.Body
		if err != nil {
			status, body = codeStatus[service.CodeOf(err)], nil
		}
		w := post(remote, raw)
		if status != tc.want || w.Code != tc.want || !bytes.Equal(body, w.Body.Bytes()) {
			t.Errorf("%s: inproc %d %s (%v), proxyserver %d %s, want %d", tc.endpoint, status, body, err, w.Code, w.Body, tc.want)
		}
		if tc.want == 200 && len(body) > 0 && w.Header().Get("Content-Type") != got.ContentType {
			t.Errorf("%s: content types %q vs %q", tc.endpoint, w.Header().Get("Content-Type"), got.ContentType)
		}
	}
}
