package app_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	json "encoding/json/v2"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/clock"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
	"github.com/deploymenttheory/go-apple-dm/simulator"
)

func TestPKIAndAccountStatePersistAcrossInstances(t *testing.T) {
	var config app.Config
	f := newEnrollFixture(t, "", func(c *app.Config) {
		c.PKI = app.PKIConfig{
			Enabled:    true,
			CRLTTL:     time.Hour,
			CRLRefresh: time.Minute,
			OCSPTTL:    time.Minute,
		}
		c.AdminToken = "admin"
		c.Storage = "sqlite"
		c.DSN = filepath.Join(t.TempDir(), "security.db")
		c.StorageKeys = []string{"test"}
		c.Secrets = secrets.Static{"test": bytes.Repeat([]byte{1}, 32)}
		config = *c
	})
	d := f.device(t, "account-device", "iPhone17,2")
	if _, err := d.AccountDrivenEnroll(
		t.Context(),
		simulator.AccountDrivenOptions{
			UserIdentifier: "alice@example.com",
			DiscoveryURL:   f.publicURL,
			Authenticate:   f.signIn(t),
		},
	); err != nil {
		t.Fatal(err)
	}
	second := build(t, config)
	access, _ := d.AccountTokens()
	body, err := plist.Marshal(
		map[string]any{
			"MessageType":  "TokenUpdate",
			"EnrollmentID": d.EnrollmentID,
			"Topic":        d.Topic,
			"PushMagic":    "magic",
			"Token":        []byte{1, 2, 3},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	signature, err := cms.Sign(body, d.Identity.Cert, d.Identity.Key)
	if err != nil {
		t.Fatal(err)
	}
	request := func() *http.Request {
		r := httptest.NewRequest("PUT", "/mdm", bytes.NewReader(body))
		r.Header.Set("Content-Type", httpapi.ContentTypeCheckin)
		r.Header.Set("Authorization", "Bearer "+string(access.Bytes()))
		r.Header.Set(cms.HeaderName, cms.EncodeHeader(signature))
		return r
	}
	for _, a := range []*app.App{f.app, second} {
		w := httptest.NewRecorder()
		a.Handler.ServeHTTP(w, request())
		if w.Code != 200 {
			t.Fatal("shared tokens and associations", w.Code, w.Body.String())
		}
	}
	issuer, serial := cms.Fingerprint(f.appCA), d.Identity.Cert.SerialNumber.Text(16)
	admin := func(method, path, token string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, "/admin/v1"+path, bytes.NewReader(raw))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		second.Handler.ServeHTTP(w, r)
		return w
	}
	path := "/pki/certificates/" + issuer + "/" + serial
	if w := admin(
		"GET",
		path,
		"admin",
		nil,
	); w.Code != 200 ||
		!strings.Contains(w.Body.String(), "issued") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := admin("POST", path+"/revoke", "wrong", map[string]int{"reason": 1}); w.Code == 200 {
		t.Fatal("unauthorized revocation")
	}
	if w := admin("POST", path+"/revoke", "admin", map[string]int{"reason": 1}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := admin("POST", path+"/revoke", "admin", map[string]int{"reason": 1}); w.Code != 409 {
		t.Fatal(w.Code)
	}
	// Reimport cannot undo a revocation.
	if w := admin(
		"POST",
		"/pki/certificates/import",
		"admin",
		map[string]any{"issuer": issuer, "certificate": d.Identity.Cert.Raw},
	); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := admin(
		"GET",
		path,
		"admin",
		nil,
	); w.Code != 200 ||
		!strings.Contains(w.Body.String(), "revoked") {
		t.Fatal(w.Code, w.Body.String())
	}

	for _, tc := range []struct {
		method, path string
		body         any
		want         int
	}{
		{"GET", "/pki/certificates/" + issuer + "/invalid", nil, 400},
		{"GET", "/pki/certificates/unknown/1", nil, 404},
		{"POST", "/pki/certificates/" + issuer + "/invalid/revoke", map[string]int{"reason": 1}, 400},
		{"POST", path + "/revoke", []int{1}, 400},
		{"POST", path + "/revoke", map[string]int{"reason": 6}, 400},
		{"POST", "/pki/certificates/import", []int{1}, 400},
		{"POST", "/pki/certificates/import", map[string]any{"issuer": issuer, "certificate": []byte("invalid DER")}, 400},
		{"POST", "/pki/certificates/import", map[string]any{"issuer": "unknown", "certificate": d.Identity.Cert.Raw}, 404},
		{"POST", "/pki/certificates/import", map[string]any{"issuer": issuer, "certificate": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d.Identity.Cert.Raw})}, 200},
	} {
		if w := admin(tc.method, tc.path, "admin", tc.body); w.Code != tc.want {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	// All transports enter the same status gate before service writes.
	api := httpapi.Handler(httpapi.Config{Checkin: f.app.Core, Connect: f.app.Core})
	for _, source := range []string{"cms", "tls", "proxy"} {
		r := request()
		var h http.Handler
		switch source {
		case "cms":
			h = httpapi.CertFromMdmSignature(cms.VerifyOptions{Roots: config.CARoots}, 0)(api)
		case "tls":
			r.TLS = &tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{d.Identity.Cert},
				VerifiedChains:   [][]*x509.Certificate{{d.Identity.Cert, f.appCA}},
			}
			h = httpapi.CertFromTLS(api)
		case "proxy":
			r.Header.Set(
				"X-Client-Cert",
				":"+base64.StdEncoding.EncodeToString(d.Identity.Cert.Raw)+":",
			)
			h = httpapi.CertFromHeader(
				"X-Client-Cert",
				httpapi.WithHeaderRoots(config.CARoots),
				httpapi.WithHeaderPeers(netip.MustParsePrefix("192.0.2.0/24")),
			)(
				api,
			)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatal(source, w.Code, w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	f.app.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/pki/crl/"+issuer, nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	crl, err := x509.ParseRevocationList(w.Body.Bytes())
	if err != nil || crl.CheckSignatureFrom(f.appCA) != nil ||
		len(crl.RevokedCertificateEntries) != 1 {
		t.Fatal(crl, err)
	}
	// A credential document cannot be issued using a revoked identity.
	r := httptest.NewRequest("GET", app.PathACMECredential, nil)
	sig, _ := cms.Sign(nil, d.Identity.Cert, d.Identity.Key)
	r.Header.Set(cms.HeaderName, cms.EncodeHeader(sig))
	w = httptest.NewRecorder()
	f.app.Handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("credential after revocation", w.Code)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if w := admin("GET", path, "admin", nil); w.Code != 500 {
		t.Fatal("closed registry", w.Code)
	}
}

func TestExplicitRateLimitsAndSecurityEnvironment(t *testing.T) {
	raw := `{"enroll":{"interval":"1h","burst":1,"global_interval":"1h","global_burst":10},"acme":{"interval":"1h","burst":1,"global_interval":"1h","global_burst":10}}`
	get := func(k string) string {
		if k == app.EnvStorage {
			return "inmem"
		}
		if k == app.EnvRateLimits {
			return raw
		}
		if k == app.EnvTrustedProxies {
			return "10.0.0.0/8"
		}
		return ""
	}
	cfg, err := app.ParseEnv(get)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Storage = "inmem"
	cfg.Role = app.RoleAll
	a := build(t, cfg)
	for _, path := range []string{"/enroll/ade", "/acme/new-account"} {
		for _, want := range []int{404, 429} {
			w := httptest.NewRecorder()
			r := httptest.NewRequest("POST", path, strings.NewReader("unparsed body"))
			r.RemoteAddr = "192.0.2.1:1"
			r.Header.Set("X-Forwarded-For", "1.1.1.1")
			a.Handler.ServeHTTP(w, r)
			if w.Code != want {
				t.Fatal(path, w.Code, w.Body.String())
			}
			if want == 429 {
				if w.Header().Get("Retry-After") == "" {
					t.Fatal("missing retry")
				}
				if strings.HasPrefix(path, "/acme/") && !strings.Contains(w.Body.String(), "rateLimited") {
					t.Fatal(w.Body.String())
				}
			}
		}
	}
	for range 3 {
		w := httptest.NewRecorder()
		a.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
	}
	for name, value := range map[string]string{app.EnvPKIRevocation: "bad", app.EnvPKICRLTTL: "bad", app.EnvPKIRetiredIssuers: "bad", app.EnvRateLimitMaxEntries: "bad", app.EnvTrustedProxies: "bad", app.EnvRateLimits: `{"unknown":{}}`} {
		if _, err := app.ParseEnv(func(k string) string {
			if k == name {
				return value
			}
			return ""
		}); err == nil {
			t.Fatal(name)
		}
	}
	if _, err := app.ParseEnv(func(k string) string {
		if k == app.EnvPKIRevocation {
			return "true"
		}
		return ""
	}); err == nil {
		t.Fatal("missing publication lifetimes accepted")
	}
	// Off by default, including public PKI endpoints.
	off := build(t, app.Config{Role: app.RoleAll, Storage: "inmem"})
	w := httptest.NewRecorder()
	off.Handler.ServeHTTP(w, httptest.NewRequest("GET", "/pki/crl/unknown", nil))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestRetiredIssuerConfiguration(t *testing.T) {
	certFile, keyFile, retired := writeCA(t)
	var config app.Config
	f := newEnrollFixture(t, "", func(c *app.Config) {
		c.PKI = app.PKIConfig{
			Enabled:    true,
			CRLTTL:     time.Hour,
			CRLRefresh: time.Minute,
			OCSPTTL:    time.Minute,
			Retired:    []app.IssuerFiles{{Certificate: certFile, Key: keyFile}},
		}
		config = *c
	})
	w := httptest.NewRecorder()
	f.app.Handler.ServeHTTP(
		w,
		httptest.NewRequest("GET", "/pki/crl/"+cms.Fingerprint(retired), nil),
	)
	crl, err := x509.ParseRevocationList(w.Body.Bytes())
	if w.Code != 200 || err != nil || crl.CheckSignatureFrom(retired) != nil {
		t.Fatal(w.Code, err)
	}
	invalid := filepath.Join(t.TempDir(), "invalid.key")
	if err := os.WriteFile(invalid, []byte("invalid key"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, files := range []app.IssuerFiles{
		{Certificate: "/missing", Key: keyFile},
		{Certificate: certFile, Key: "/missing"},
		{Certificate: certFile, Key: invalid},
		{Certificate: certFile, Key: config.Enroll.CAKeyFile},
	} {
		cfg := config
		cfg.PKI.Retired = []app.IssuerFiles{files}
		if a, err := app.Build(t.Context(), cfg); err == nil {
			_ = a.Close()
			t.Fatal("invalid retired issuer accepted", files)
		}
	}
}

func TestRateLimitFamiliesCapacityAndConfiguration(t *testing.T) {
	quotas := map[string]app.RouteQuota{}
	for _, name := range []string{"enroll", "auth", "scep", "admin", "pki", "mdm", "acme"} {
		quotas[name] = app.RouteQuota{
			Interval:       time.Hour,
			Burst:          1,
			GlobalInterval: time.Hour,
			GlobalBurst:    10,
		}
	}
	cfg := app.Config{
		Role:       app.RoleAll,
		Storage:    "inmem",
		RateLimits: app.RateLimitConfig{Routes: quotas},
	}
	a := build(t, cfg)
	for _, path := range []string{"/enroll/ade", app.PathAuthenticate, "/scep", "/admin/v1/config", "/pki/crl/unknown", "/mdm", "/acme/new-account", "/ota", "/ota/start", "/ddm/v1/declarative-management"} {
		for i := range 2 {
			w := httptest.NewRecorder()
			a.Handler.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
			if i == 1 && w.Code != 429 {
				t.Fatal(path, w.Code, w.Body.String())
			}
		}
	}
	for _, maxEntries := range []int{-1, 10001} {
		cfg.RateLimits.MaxEntries = maxEntries
		if _, err := app.Build(t.Context(), cfg); err == nil {
			t.Fatal("invalid capacity", maxEntries)
		}
	}
	for _, routes := range []map[string]app.RouteQuota{{"unknown": quotas["auth"]}, {"auth": {Interval: time.Second, Burst: 1}}} {
		cfg.RateLimits = app.RateLimitConfig{Routes: routes}
		if _, err := app.Build(t.Context(), cfg); err == nil {
			t.Fatal("invalid quota accepted", routes)
		}
	}
	cfg.RateLimits = app.RateLimitConfig{Routes: quotas, MaxEntries: 1}
	limited := build(t, cfg)
	for _, path := range []string{app.PathAuthenticate, "/acme/new-account"} {
		w := httptest.NewRecorder()
		limited.Handler.ServeHTTP(w, httptest.NewRequest("POST", path, nil))
		if w.Code != 503 {
			t.Fatal("capacity did not fail closed", w.Code)
		}
		if strings.HasPrefix(path, "/acme/") &&
			!strings.Contains(w.Body.String(), "serverInternal") {
			t.Fatal(w.Body.String())
		}
	}
	for _, value := range []string{`{`, `{"auth":{"interval":"1s","global_interval":"bad"}}`} {
		if _, err := app.ParseEnv(func(k string) string {
			if k == app.EnvRateLimits {
				return value
			}
			if k == app.EnvStorage {
				return "inmem"
			}
			return ""
		}); err == nil {
			t.Fatal("bad environment accepted", value)
		}
	}
	if _, err := app.ParseEnv(func(k string) string {
		if k == app.EnvRateLimitMaxEntries {
			return "100"
		}
		if k == app.EnvStorage {
			return "inmem"
		}
		return ""
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProtocolStateRetentionWorker(t *testing.T) {
	clk := clock.NewFake(time.Now())
	quota := app.RouteQuota{Interval: time.Second, Burst: 1, GlobalInterval: time.Second, GlobalBurst: 1}
	a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem", Clock: clk, RateLimits: app.RateLimitConfig{Routes: map[string]app.RouteQuota{"auth": quota}}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	deadline := time.After(5 * time.Second)
	for clk.Pending() == 0 {
		select {
		case <-deadline:
			t.Fatal("retention worker did not start")
		case <-time.After(time.Millisecond):
		}
	}
	w := httptest.NewRecorder()
	a.Handler.ServeHTTP(w, httptest.NewRequest("POST", app.PathAuthenticate, nil))
	clk.Advance(2 * time.Minute)
	// The worker re-arms after pruning. Cancellation must terminate its loop.
	for clk.Pending() == 0 {
		select {
		case <-deadline:
			t.Fatal("retention worker did not re-arm")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retention worker did not stop")
	}
}
