package app_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/ca"
	"github.com/deploymenttheory/go-apple-mdm/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-mdm/enroll/discovery"
	"github.com/deploymenttheory/go-apple-mdm/enroll/webauth/webauthtest"
	"github.com/deploymenttheory/go-apple-mdm/internal/app"
	"github.com/deploymenttheory/go-apple-mdm/internal/testpki"
	"github.com/deploymenttheory/go-apple-mdm/mdm"
	"github.com/deploymenttheory/go-apple-mdm/simulator"
)

// enrollFixture is an app with the enrollment routes on, served over TLS
// at a public URL known before Build.
type enrollFixture struct {
	app       *app.App
	srv       *httptest.Server
	publicURL string
	deviceCA  *testpki.CA // plays Apple's device certificate chain
	appCA     *x509.Certificate
	idp       *webauthtest.Provider
}

func writeCA(t *testing.T) (certFile, keyFile string, cert *x509.Certificate) {
	t.Helper()
	cert, key, err := ca.NewSelfSigned(ca.SelfSignedOptions{Subject: pkix.Name{CommonName: "app test CA"}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "ca.pem"), filepath.Join(dir, "ca.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certFile, keyFile, cert
}

func newEnrollFixture(t *testing.T, method string, mutate func(*app.Config)) *enrollFixture {
	t.Helper()
	f := &enrollFixture{idp: webauthtest.New(t)}
	var err error
	if f.deviceCA, err = testpki.NewCA("device CA"); err != nil {
		t.Fatal(err)
	}
	certFile, keyFile, appCA := writeCA(t)
	f.appCA = appCA
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.publicURL = "https://" + l.Addr().String()
	roots := x509.NewCertPool()
	roots.AddCert(appCA)
	cfg := app.Config{Role: app.RoleAll, Storage: "inmem", CARoots: roots, Logger: quiet, Enroll: app.EnrollConfig{
		PublicURL: f.publicURL, Topic: "com.apple.mgmt.External.simulator", CACertFile: certFile, CAKeyFile: keyFile, SCEPChallenge: "secret",
		Discovery:           map[discovery.ModelFamily]string{discovery.ModelFamilyMac: discovery.VersionADDE, discovery.ModelFamilyIPhone: discovery.VersionBYOD},
		AccountDrivenMethod: method, Anchors: []*x509.Certificate{f.deviceCA.Cert},
		OIDC: app.OIDCConfig{Issuer: f.idp.Issuer(), ClientID: "mdm-webview", HTTPClient: f.idp.HTTPClient()},
	}}
	if mutate != nil {
		mutate(&cfg)
	}
	f.app = build(t, cfg)
	f.srv = httptest.NewUnstartedServer(f.app.Handler)
	f.srv.Listener = l
	f.srv.StartTLS()
	t.Cleanup(f.srv.Close)
	f.idp.Set(func(o *webauthtest.Options) {
		o.ClientID, o.RedirectURI = "mdm-webview", f.publicURL+app.PathOIDCCallback
		o.Subject, o.Email, o.EmailVerified = "sub-alice", "alice@example.com", true
	})
	return f
}

// client trusts the app and the identity provider and stops at the
// apple-remotemanagement-user-login scheme like the device's web view.
func (f *enrollFixture) client() *http.Client {
	pool := x509.NewCertPool()
	pool.AddCert(f.srv.Certificate())
	pool.AddCert(f.idp.Certificate())
	return &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			if req.URL.Scheme == accountdriven.CallbackScheme {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
}

func (f *enrollFixture) device(t *testing.T, udid, product string) *simulator.Device {
	t.Helper()
	id, err := f.deviceCA.Issue(udid, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	d := simulator.New(udid, simulator.WithClient(f.client()), simulator.WithIdentity(&simulator.Identity{Cert: id.Cert, Key: id.Key}))
	d.SerialNumber, d.ProductName, d.OSVersion = "SER-"+udid, product, "26.0"
	return d
}

// signIn plays the person in the apple-as-web page: GET the web-auth URL
// (Begin, the provider, the callback, Finish) and read the 308.
func (f *enrollFixture) signIn(t *testing.T) func(context.Context, simulator.AuthChallenge) (string, error) {
	return func(ctx context.Context, c simulator.AuthChallenge) (string, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.URL+"?user-identifier=alice%40example.com", nil)
		res, err := f.client().Do(req)
		if err != nil {
			return "", err
		}
		res.Body.Close()
		if res.StatusCode != http.StatusPermanentRedirect {
			return "", errors.New("sign-in did not end in the 308: " + res.Status)
		}
		return simulator.AccessTokenFromRedirect(res.Header.Get("Location"))
	}
}

func TestEnrollment(t *testing.T) {
	ctx := context.Background()
	t.Run("Disabled", func(t *testing.T) {
		a := build(t, app.Config{Role: app.RoleAll, Storage: "inmem"})
		srv := serve(t, a)
		for _, p := range []string{app.PathSCEP, app.PathWellKnown, app.PathADE, app.PathEnroll + "mdm-byod"} {
			if got := get(t, srv.URL+p, ""); got != http.StatusNotFound {
				t.Fatalf("%s = %d without enrollment config", p, got)
			}
		}
	})
	t.Run("Discovery", func(t *testing.T) {
		f := newEnrollFixture(t, "", nil)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, f.publicURL+app.PathWellKnown+"?model-family=Mac&user-identifier=a%40b", nil)
		res, err := f.client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || !strings.Contains(string(body), `"Version":"mdm-adde"`) || !strings.Contains(string(body), f.publicURL+"/enroll/mdm-adde") {
			t.Fatalf("discovery = %d %s", res.StatusCode, body)
		}
		req, _ = http.NewRequestWithContext(ctx, http.MethodGet, f.publicURL+app.PathWellKnown+"?model-family=Watch", nil)
		res, _ = f.client().Do(req)
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("unrouted family = %d", res.StatusCode)
		}
	})
	t.Run("ADETokenLane", func(t *testing.T) {
		f := newEnrollFixture(t, "", nil)
		d := f.device(t, "UDID-APP-ADE", "Mac16,1")
		if err := d.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{}); err != nil {
			t.Fatalf("ADE through the app: %v", err)
		}
		if d.Identity.Cert.Issuer.CommonName != f.appCA.Subject.CommonName {
			t.Fatalf("identity issued by %q, want the app CA", d.Identity.Cert.Issuer.CommonName)
		}
		e, err := f.app.Store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-APP-ADE"})
		if err != nil || !e.Enabled {
			t.Fatalf("enrollment = %+v %v", e, err)
		}
		if got, err := d.Connect(ctx); err != nil || len(got) != 0 {
			t.Fatalf("connect: %v %v", got, err)
		}
	})
	t.Run("ADEWebView", func(t *testing.T) {
		f := newEnrollFixture(t, "", nil)
		d := f.device(t, "UDID-APP-WEB", "Mac16,1")
		err := d.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{WebView: func(_ context.Context, first *http.Response) (*http.Response, error) { return first, nil }})
		if err != nil {
			t.Fatalf("ADE web view through the app: %v", err)
		}
		if !strings.Contains(d.Identity.Cert.Subject.CommonName, "alice@example.com") {
			t.Fatalf("profile not personalised: %q", d.Identity.Cert.Subject.CommonName)
		}
	})
	t.Run("AccountDrivenAsWeb", func(t *testing.T) {
		f := newEnrollFixture(t, "", nil)
		d := f.device(t, "UDID-APP-BYOD", "iPhone17,2")
		res, err := d.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "alice@example.com", DiscoveryURL: f.publicURL, Authenticate: f.signIn(t)})
		if err != nil {
			t.Fatalf("account-driven through the app: %v", err)
		}
		if res.Chosen.Version != discovery.VersionBYOD || res.Challenge.Method != accountdriven.MethodAppleAsWeb {
			t.Fatalf("result = %+v", res)
		}
		if e, err := f.app.Store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: d.EnrollmentID}); err != nil || !e.Enabled {
			t.Fatalf("enrollment = %+v %v", e, err)
		}
		// The enrollment token guards the check-in.
		withToken := d.CheckinURL
		d.CheckinURL = f.publicURL + app.PathMDM
		var herr *simulator.HTTPError
		if err := d.TokenUpdate(ctx); !errors.As(err, &herr) || herr.Status != http.StatusForbidden {
			t.Fatalf("check-in without the enrollment token = %v", err)
		}
		d.CheckinURL = withToken
		if err := d.TokenUpdate(ctx); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("AccountDrivenOAuth2", func(t *testing.T) {
		f := newEnrollFixture(t, accountdriven.MethodAppleOAuth2, nil)
		d := f.device(t, "UDID-APP-OAUTH", "iPhone17,2")
		_, err := d.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "alice@example.com", DiscoveryURL: f.publicURL,
			Authenticate: func(ctx context.Context, c simulator.AuthChallenge) (string, error) {
				return d.OAuth2CodeFlow(ctx, c, "alice@example.com", func(ctx context.Context, authorizationURL string) (string, error) {
					// The sign-in page: our authorization endpoint hands
					// off to the provider and ends with the 308.
					req, _ := http.NewRequestWithContext(ctx, http.MethodGet, authorizationURL, nil)
					res, err := f.client().Do(req)
					if err != nil {
						return "", err
					}
					res.Body.Close()
					if res.StatusCode != http.StatusPermanentRedirect {
						return "", errors.New("authorization did not end in the 308: " + res.Status)
					}
					return res.Header.Get("Location"), nil
				})
			}})
		if err != nil {
			t.Fatalf("oauth2 through the app: %v", err)
		}
	})
	t.Run("AnchorFile", func(t *testing.T) {
		var anchorFile string
		f := newEnrollFixture(t, "", func(c *app.Config) {
			anchorFile = filepath.Join(t.TempDir(), "anchors.pem")
			if err := os.WriteFile(anchorFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Enroll.Anchors[0].Raw}), 0o600); err != nil {
				t.Fatal(err)
			}
			c.Enroll.Anchors, c.Enroll.ADEAnchorFile = nil, anchorFile
		})
		d := f.device(t, "UDID-APP-ANCHOR", "Mac16,1")
		if err := d.ADEEnroll(ctx, f.publicURL+app.PathADE, simulator.ADEOptions{}); err != nil {
			t.Fatalf("ADE with anchors from a file: %v", err)
		}
	})
	t.Run("SelfSignedCAAndHMAC", func(t *testing.T) {
		f := newEnrollFixture(t, "", func(c *app.Config) {
			c.Enroll.CACertFile, c.Enroll.CAKeyFile, c.Enroll.SCEPChallenge, c.Enroll.SCEPHMACKey = "", "", "", []byte("hmac-key-of-at-least-sixteen-bytes")
			c.Enroll.OIDC = app.OIDCConfig{}
			c.CARoots = nil
			c.CertHeader = "X-Client-Cert" // check-in identity is not exercised here
		})
		d := f.device(t, "UDID-APP-HMAC", "Mac16,1")
		// Enrolment itself needs a check-in identity source; the profile
		// fetch and SCEP are what this case proves.
		signed, err := d.SignedMachineInfo(simulator.ADEOptions{})
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, f.publicURL+app.PathADE, strings.NewReader(string(signed)))
		req.Header.Set("Content-Type", "application/pkcs7-signature")
		res, err := f.client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/x-apple-aspen-config" || len(body) == 0 {
			t.Fatalf("profile = %d %s", res.StatusCode, res.Header.Get("Content-Type"))
		}
		// No OIDC: the web view lane answers 501.
		req, _ = http.NewRequestWithContext(ctx, http.MethodGet, f.publicURL+app.PathADE, nil)
		req.Header.Set("x-apple-aspen-deviceinfo", base64.StdEncoding.EncodeToString(signed))
		res, _ = f.client().Do(req)
		res.Body.Close()
		if res.StatusCode != http.StatusNotImplemented {
			t.Fatalf("web view without OIDC = %d", res.StatusCode)
		}
	})
	t.Run("BadConfig", func(t *testing.T) {
		certFile, keyFile, _ := writeCA(t)
		base := app.EnrollConfig{PublicURL: "https://mdm.example", Topic: "t", CACertFile: certFile, CAKeyFile: keyFile, SCEPChallenge: "c"}
		cases := map[string]func(*app.EnrollConfig){
			"http":         func(e *app.EnrollConfig) { e.PublicURL = "http://mdm.example" },
			"half ca":      func(e *app.EnrollConfig) { e.CAKeyFile = "" },
			"no challenge": func(e *app.EnrollConfig) { e.SCEPChallenge = "" },
			"method":       func(e *app.EnrollConfig) { e.AccountDrivenMethod = "saml" },
			"family": func(e *app.EnrollConfig) {
				e.Discovery = map[discovery.ModelFamily]string{"Toaster": discovery.VersionBYOD}
			},
			"version": func(e *app.EnrollConfig) {
				e.Discovery = map[discovery.ModelFamily]string{discovery.ModelFamilyMac: "mdm-x"}
			},
			"missing ca":  func(e *app.EnrollConfig) { e.CACertFile, e.CAKeyFile = "/nonexistent/ca.pem", "/nonexistent/ca.key" },
			"bad anchors": func(e *app.EnrollConfig) { e.ADEAnchorFile = keyFile },
			"missing key": func(e *app.EnrollConfig) { e.CAKeyFile = certFile },
			"bad oidc":    func(e *app.EnrollConfig) { e.OIDC = app.OIDCConfig{Issuer: "http://insecure"} },
		}
		for name, mutate := range cases {
			e := base
			mutate(&e)
			if _, err := app.Build(ctx, app.Config{Role: app.RoleAll, Storage: "inmem", Logger: quiet, Enroll: e}); err == nil {
				t.Errorf("%s: no error", name)
			}
		}
	})
	t.Run("ParseDiscovery", func(t *testing.T) {
		d, err := app.ParseDiscovery(" Mac=mdm-adde, iPhone = mdm-byod ,")
		if err != nil || d[discovery.ModelFamilyMac] != discovery.VersionADDE || d[discovery.ModelFamilyIPhone] != discovery.VersionBYOD {
			t.Fatalf("%v %v", d, err)
		}
		if _, err := app.ParseDiscovery("Mac"); !errors.Is(err, app.ErrConfig) {
			t.Fatalf("malformed = %v", err)
		}
		env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
		cfg, err := app.ParseEnv(env(map[string]string{app.EnvDiscovery: "Mac=mdm-adde", app.EnvPublicURL: "https://x", app.EnvPushTopic: "t", app.EnvSCEPChallenge: "c", app.EnvADEAudit: "true", app.EnvRequireUserAuth: "1"}))
		if err != nil || cfg.Enroll.Discovery[discovery.ModelFamilyMac] != discovery.VersionADDE || !cfg.Enroll.ADEAudit || !cfg.Enroll.RequireUserAuth {
			t.Fatalf("env = %+v %v", cfg.Enroll, err)
		}
		if _, err := app.ParseEnv(env(map[string]string{app.EnvDiscovery: "Mac"})); !errors.Is(err, app.ErrConfig) {
			t.Fatal(err)
		}
		if _, err := app.ParseEnv(env(map[string]string{app.EnvADEAudit: "maybe"})); !errors.Is(err, app.ErrConfig) {
			t.Fatal(err)
		}
	})
}
