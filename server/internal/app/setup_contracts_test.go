package app

import (
	"bytes"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/clock"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/lifecycle"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

func setupRequire(t *testing.T, err, want error) {
	t.Helper()
	if want == nil && err != nil || want != nil && !errors.Is(err, want) {
		t.Fatalf("got %v, want %v", err, want)
	}
}

func memorySetupApp(t *testing.T) (*App, *clock.Fake) {
	t.Helper()
	c := clock.NewFake(time.Now().UTC().Truncate(time.Second))
	s := state.NewMemory()
	s.Now = c.Now
	a := &App{
		protocol: s,
		cfg: Config{
			Clock:  c,
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			Setup: &SetupConfig{
				Role:      "combined",
				VendorID:  "vendor",
				PushID:    "push",
				IssuerID:  "issuer",
				HTTPSID:   "https",
				HTTPSCAID: "https-ca",
			},
		},
	}
	a.Certificates = &lifecycle.Manager{Store: s}
	return a, c
}

func setupRequest(id string, kind lifecycle.Kind) SetupRequest {
	cn := id
	if cn == "" {
		cn = string(kind)
	}
	return SetupRequest{
		Request: lifecycle.Request{
			ID:       id,
			Kind:     kind,
			Subject:  pkix.Name{CommonName: cn},
			DNSNames: []string{"mdm.example"},
		},
	}
}

func setupExecute(
	t *testing.T,
	a *App,
	kind lifecycle.Kind,
	op string,
	req SetupRequest,
) SetupResult {
	t.Helper()
	out, err := a.ExecuteSetup(t.Context(), kind, op, req)
	setupRequire(t, err, nil)
	return out
}

func setupJSON(t *testing.T, s state.Store, key string, value any) {
	t.Helper()
	b, err := json.Marshal(value)
	setupRequire(t, err, nil)
	setupRequire(
		t,
		s.Update(
			t.Context(),
			[]string{key},
			func(tx state.Tx) error { return tx.Put(t.Context(), state.Record{Key: key, Value: b}) },
		),
		nil,
	)
}

// Stored records are edited only to model damaged persistence and legacy
// migrations; production validation is used to create every usable identity.
func editSetupIdentity(t *testing.T, s state.Store, id string, edit func(map[string]any)) {
	t.Helper()
	k := "pki/lifecycle/identity/" + id
	r, err := s.Get(t.Context(), k)
	setupRequire(t, err, nil)
	var value map[string]any
	setupRequire(t, json.Unmarshal(r.Value, &value), nil)
	edit(value)
	setupJSON(t, s, k, value)
}

func TestSetupOperationsPreserveWorkflowAndEnforceRoles(t *testing.T) {
	a, c := memorySetupApp(t)
	ctx := t.Context()
	_, err := (&App{}).ExecuteSetup(ctx, lifecycle.Push, "request", SetupRequest{})
	setupRequire(t, err, ErrConfig)
	for _, kind := range []lifecycle.Kind{"unknown", lifecycle.Push, lifecycle.HTTPS, lifecycle.Issuer, lifecycle.Vendor} {
		_, err = a.ExecuteSetup(ctx, kind, "request", SetupRequest{Key: []byte("private")})
		setupRequire(t, err, lifecycle.ErrInvalid)
	}
	for _, role := range []string{"vendor", "customer"} {
		a.cfg.Setup.Role = role
		kind := lifecycle.Vendor
		if role == "vendor" {
			kind = lifecycle.Issuer
		}
		_, err = a.ExecuteSetup(ctx, kind, "request", setupRequest("forbidden", kind))
		setupRequire(t, err, lifecycle.ErrInvalid)
	}
	a.cfg.Setup.Role = "combined"
	issuerReq := setupRequest("", lifecycle.Issuer)
	issuerReq.Subject.CommonName = "enrollment authority"
	issuer := setupExecute(t, a, lifecycle.Issuer, "create", issuerReq)
	setupExecute(
		t,
		a,
		lifecycle.Issuer,
		"activate",
		SetupRequest{Revision: issuer.Identity.Pending},
	)
	for _, operation := range []string{"status", "request", "create", "renew"} {
		result := setupExecute(t, a, lifecycle.Issuer, operation, SetupRequest{})
		if result.Identity.Active != "1" || result.Identity.Pending != "" {
			t.Fatal("retry unexpectedly rotated issuer", result)
		}
	}
	c.Advance(time.Hour)
	result := setupExecute(t, a, lifecycle.Issuer, "renew", SetupRequest{Force: true})
	if result.Identity.Pending != "2" {
		t.Fatal("forced renewal not prepared")
	}
	setupExecute(t, a, lifecycle.Issuer, "create", SetupRequest{})
	_, err = a.ExecuteSetup(ctx, lifecycle.Issuer, "activate", SetupRequest{Revision: "2"})
	setupRequire(t, err, lifecycle.ErrConflict)
	setupExecute(t, a, lifecycle.Issuer, "status", SetupRequest{})
	setupExecute(t, a, lifecycle.Issuer, "cancel", SetupRequest{Revision: "2"})
	for _, operation := range []string{"retry", "device-status", "rollover", "retire", "acme", "lab", "create", "sign", "unsupported"} {
		_, err = a.ExecuteSetup(ctx, lifecycle.Push, operation, SetupRequest{})
		setupRequire(t, err, lifecycle.ErrInvalid)
	}
	_, err = a.ExecuteSetup(ctx, lifecycle.HTTPS, "sign", SetupRequest{})
	setupRequire(t, err, lifecycle.ErrInvalid)
	_, err = a.ExecuteSetup(ctx, lifecycle.HTTPS, "import", SetupRequest{})
	setupRequire(t, err, lifecycle.ErrInvalid)
	_, err = a.ExecuteSetup(ctx, lifecycle.Push, "status", SetupRequest{})
	setupRequire(t, err, lifecycle.ErrNotFound)
	_, err = a.ExecuteSetup(ctx, lifecycle.Push, "request", setupRequest("issuer", lifecycle.Push))
	setupRequire(t, err, lifecycle.ErrConflict)
	httpsReq := setupRequest("", lifecycle.HTTPS)
	httpsReq.Subject.CommonName = "mdm.example"
	https := setupExecute(t, a, lifecycle.HTTPS, "lab", httpsReq)
	setupExecute(t, a, lifecycle.HTTPS, "activate", SetupRequest{Revision: https.Identity.Pending})
	setupExecute(t, a, lifecycle.HTTPS, "lab", SetupRequest{})
	setupExecute(t, a, lifecycle.HTTPS, "status", SetupRequest{})
	trust, err := a.SetupTrustProfile(ctx)
	setupRequire(t, err, nil)
	if !bytes.Contains(trust, []byte("com.apple.security.root")) {
		t.Fatal("missing trust payload")
	}
	mat, err := a.Certificates.LoadMaterial(ctx, "https", "")
	setupRequire(t, err, nil)
	setupExecute(
		t,
		a,
		lifecycle.HTTPS,
		"adopt",
		SetupRequest{
			Request:     lifecycle.Request{ID: "adopted"},
			Certificate: mat.Certificate,
			Key:         mat.Key,
		},
	)
	_, err = a.ExecuteSetup(
		ctx,
		lifecycle.HTTPS,
		"import",
		SetupRequest{Revision: "1", Certificate: mat.Certificate},
	)
	setupRequire(t, err, nil)
	_, err = a.TLSCertificate(nil)
	setupRequire(t, err, nil)
	acmeReq := setupRequest("public", lifecycle.HTTPS)
	acmeReq.PublicACME = &lifecycle.PublicACMEOptions{
		Contact:     "operator@example.com",
		AcceptTerms: true,
	}
	setupExecute(t, a, lifecycle.HTTPS, "acme", acmeReq)
	a.cfg.Setup.HTTPSID = "public"
	status, err := a.CertificateSetupStatus(ctx)
	setupRequire(t, err, nil)
	if status.PublicACME == nil ||
		!strings.Contains(strings.Join(status.Issues, ";"), "http01Listen") {
		t.Fatal(status)
	}
	a.cfg.Setup.HTTPSCAID = ""
	_, err = a.ExecuteSetup(
		ctx,
		lifecycle.HTTPS,
		"lab",
		setupRequest("missing-ca", lifecycle.HTTPS),
	)
	setupRequire(t, err, lifecycle.ErrInvalid)
	_, err = a.SetupTrustProfile(ctx)
	setupRequire(t, err, lifecycle.ErrNotFound)
	setupExecute(t, a, lifecycle.Push, "request", setupRequest("", lifecycle.Push))
	_, err = a.ExecuteSetup(ctx, lifecycle.Push, "sign", SetupRequest{Revision: "missing"})
	setupRequire(t, err, lifecycle.ErrNotFound)
	_, err = a.ExecuteSetup(ctx, lifecycle.Push, "sign", SetupRequest{Revision: "1"})
	setupRequire(t, err, lifecycle.ErrNotFound)
	_, err = a.ExecuteSetup(ctx, lifecycle.Vendor, "sign", SetupRequest{CSR: []byte("csr")})
	setupRequire(t, err, lifecycle.ErrNotFound)
	a.cfg.Setup.Role = "customer"
	_, err = a.ExecuteSetup(ctx, lifecycle.Push, "sign", SetupRequest{Revision: "1"})
	setupRequire(t, err, lifecycle.ErrInvalid)
	_, err = a.ExecuteSetup(
		ctx,
		lifecycle.Push,
		"sign",
		SetupRequest{Revision: "1", SignedRequest: []byte("invalid")},
	)
	setupRequire(t, err, lifecycle.ErrInvalid)
}

func TestSetupHTTPFailuresAndPublicResponses(t *testing.T) {
	a, _ := memorySetupApp(t)
	setupExecute(t, a, lifecycle.Push, "request", setupRequest("push", lifecycle.Push))
	mux := http.NewServeMux()
	for _, route := range a.setupRoutes() {
		mux.Handle(route.Pattern, route.Handler)
	}
	for _, tc := range []struct {
		method, path, body string
		code               int
	}{
		{"GET", "/setup/workflow/push", "", 200},
		{"GET", "/setup/workflow/missing", "", 404},
		{"GET", "/setup/workflow/missing/export?artifact=csr", "", 404},
		{"GET", "/setup/workflow/push/history", "", 200},
		{"GET", "/setup/trust", "", 404},
		{"POST", "/setup/push/request", "invalid", 400},
		{"POST", "/setup/push/activate", `{"revision":"1"}`, 409},
		{"POST", "/setup/vendor/sign", `{"csr":"Y3Ny"}`, 404},
		{"POST", "/setup/push/request", strings.Repeat("x", MaxAdminBody+1), 413},
	} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != tc.code {
			t.Fatal(tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	setupExecute(t, a, lifecycle.HTTPS, "lab", setupRequest("https", lifecycle.HTTPS))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/setup/trust", nil))
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/x-apple-aspen-config" {
		t.Fatal(w.Code)
	}
	backing := a.Certificates.Store
	a.Certificates.Store = unavailableAppState{Store: backing, failure: io.ErrUnexpectedEOF}
	for _, path := range []string{"/setup", "/setup/workflow/push/history"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 500 || strings.Contains(w.Body.String(), "unexpected EOF") {
			t.Fatal("storage failure disclosed", w.Code, w.Body.String())
		}
	}
}

type setupRoundTrip func(*http.Request) (*http.Response, error)

func (f setupRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type setupBrokenBody struct{}

func (setupBrokenBody) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (setupBrokenBody) Close() error             { return nil }

func TestRemoteVendorSigningBoundsRequestsAndNeverFollowsRedirects(t *testing.T) {
	a, _ := memorySetupApp(t)
	token := filepath.Join(t.TempDir(), "token")
	setupRequire(t, os.WriteFile(token, []byte(" credential\n"), 0o600), nil)
	a.cfg.Setup.VendorTokenFile = token
	ctx := t.Context()
	for _, address := range []string{"%", "http://vendor.example", "https:///missing", "https://user:password@vendor.example", "https://vendor.example?q=1", "https://vendor.example/#fragment"} {
		a.cfg.Setup.VendorURL = address
		_, err := a.remoteVendorSignature(ctx, "vendor", []byte("csr"))
		setupRequire(t, err, lifecycle.ErrInvalid)
	}
	a.cfg.Setup.VendorURL = "https://vendor.example/prefix/"
	a.cfg.Setup.VendorTokenFile = token + "missing"
	if _, err := a.remoteVendorSignature(ctx, "vendor", nil); err == nil {
		t.Fatal("missing token accepted")
	}
	a.cfg.Setup.VendorTokenFile = token
	setupRequire(t, os.WriteFile(token, nil, 0o600), nil)
	_, err := a.remoteVendorSignature(ctx, "vendor", nil)
	setupRequire(t, err, lifecycle.ErrInvalid)
	setupRequire(t, os.WriteFile(token, []byte("credential"), 0o600), nil)
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	for _, mode := range []string{"success", "network", "redirect", "denied", "read", "oversize", "json", "empty"} {
		t.Run(mode, func(t *testing.T) {
			http.DefaultTransport = setupRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "vendor.example" ||
					r.URL.Path != "/prefix/admin/v1/setup/vendor/sign" ||
					r.Header.Get("Authorization") != "Bearer credential" {
					t.Fatal("credential redirected or wrong request", r.URL)
				}
				var request SetupRequest
				setupRequire(t, json.NewDecoder(r.Body).Decode(&request), nil)
				if len(request.Key) != 0 || string(request.CSR) != "csr" {
					t.Fatal("signing request included wrong material")
				}
				if mode == "network" {
					return nil, io.ErrUnexpectedEOF
				}
				status, body := 200, `{"data":"c2lnbmVk"}`
				h := make(http.Header)
				switch mode {
				case "redirect":
					status = 302
					h.Set("Location", "https://other.example/steal")
				case "denied":
					status = 403
				case "oversize":
					body = strings.Repeat("x", MaxAdminBody+1)
				case "json":
					body = "invalid"
				case "empty":
					body = "{}"
				}
				var reader io.ReadCloser = io.NopCloser(strings.NewReader(body))
				if mode == "read" {
					reader = setupBrokenBody{}
				}
				return &http.Response{StatusCode: status, Header: h, Body: reader, Request: r}, nil
			})
			data, err := a.remoteVendorSignature(ctx, "vendor", []byte("csr"))
			if mode == "success" {
				setupRequire(t, err, nil)
				if string(data) != "signed" {
					t.Fatal("artifact changed")
				}
			} else if err == nil {
				t.Fatal("invalid response accepted")
			}
		})
	}
}

func TestManagedConfigurationAndTLSValidation(t *testing.T) {
	a, c := memorySetupApp(t)
	ctx := t.Context()
	_, err := (&App{}).LoadTLSCertificate(ctx)
	setupRequire(t, err, ErrConfig)
	_, err = (&App{}).CertificateSetupStatus(ctx)
	setupRequire(t, err, ErrConfig)
	setupRequire(t, (&App{}).configureManagedIdentities(ctx), nil)
	setupRequire(t, (&App{}).openCertificates(ctx), nil)
	for _, role := range []string{"invalid", "vendor", "customer"} {
		a.cfg.Setup.Role = role
		if role == "customer" {
			a.cfg.Setup.PushID = a.cfg.Setup.IssuerID
		}
		setupRequire(t, a.openCertificates(ctx), ErrConfig)
	}
	a.cfg.Setup.Role, a.cfg.Setup.PushID = "customer", "push"
	a.cfg.TLSCertFile = "legacy.pem"
	setupRequire(t, a.configureManagedIdentities(ctx), ErrConfig)
	a.cfg.TLSCertFile = ""
	setupRequire(t, a.configureManagedIdentities(ctx), nil)
	setupExecute(t, a, lifecycle.Push, "request", setupRequest("push", lifecycle.Push))
	setupRequire(t, a.configureManagedIdentities(ctx), nil)
	setupExecute(t, a, lifecycle.Issuer, "create", setupRequest("issuer", lifecycle.Issuer))
	setupRequire(t, a.configureManagedIdentities(ctx), nil)
	setupExecute(t, a, lifecycle.Issuer, "activate", SetupRequest{Revision: "1"})
	// The push identity's public projection models an adopted same-topic APNs
	// identity; no signing material is needed to configure runtime references.
	editSetupIdentity(
		t,
		a.protocol,
		"push",
		func(v map[string]any) { v["Active"], v["Pending"], v["Topic"] = "1", "", "com.apple.mgmt.test" },
	)
	a.cfg.Enroll.Topic = "different"
	setupRequire(t, a.configureManagedIdentities(ctx), ErrConfig)
	a.cfg.Enroll.Topic = ""
	setupRequire(t, a.configureManagedIdentities(ctx), nil)
	if a.cfg.Push.Source != PushSourceStore || !a.cfg.PKI.Enabled || a.cfg.PKI.CRLTTL == 0 {
		t.Fatal("managed runtime defaults not applied")
	}
	a.cfg.Setup.Role = "vendor"
	setupRequire(t, a.configureManagedIdentities(ctx), nil)
	if a.cfg.Enroll.Topic != "" {
		t.Fatal("vendor server enabled enrollment")
	}
	https := setupExecute(t, a, lifecycle.HTTPS, "lab", setupRequest("https", lifecycle.HTTPS))
	setupExecute(t, a, lifecycle.HTTPS, "activate", SetupRequest{Revision: https.Identity.Pending})
	_, err = a.LoadTLSCertificate(ctx)
	setupRequire(t, err, nil)
	c.Advance(366 * 24 * time.Hour)
	_, err = a.LoadTLSCertificate(ctx)
	setupRequire(t, err, ErrConfig)
	editSetupIdentity(
		t,
		a.protocol,
		"https",
		func(v map[string]any) { v["Revisions"].([]any)[0].(map[string]any)["Key"] = "Y29ycnVwdA==" },
	)
	if _, err = a.LoadTLSCertificate(ctx); err == nil {
		t.Fatal("corrupt key served")
	}
	a.Certificates.Store = issuanceStateFault{
		Store:     a.protocol,
		readErr:   io.ErrUnexpectedEOF,
		txReadErr: io.ErrUnexpectedEOF,
	}
	_, err = a.ExecuteSetup(ctx, lifecycle.HTTPS, "status", SetupRequest{})
	setupRequire(t, err, io.ErrUnexpectedEOF)
	a.cfg.Setup.HTTPSCAID = ""
	_, err = a.ExecuteSetup(ctx, lifecycle.HTTPS, "status", SetupRequest{})
	setupRequire(t, err, io.ErrUnexpectedEOF)
	_, err = a.CertificateSetupStatus(ctx)
	setupRequire(t, err, io.ErrUnexpectedEOF)
}
