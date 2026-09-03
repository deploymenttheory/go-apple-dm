//go:build e2e

package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/enroll"
	"github.com/deploymenttheory/go-apple-dm/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/enroll/webauth"
	"github.com/deploymenttheory/go-apple-dm/enroll/webauth/webauthtest"
	"github.com/deploymenttheory/go-apple-dm/mdm"
	"github.com/deploymenttheory/go-apple-dm/service"
	"github.com/deploymenttheory/go-apple-dm/simulator"
)

// adeFixture mounts the ADE endpoint with the web view flow bound to a
// fake identity provider. The device's built-in certificate is played by
// the test CA, so the MachineInfo anchors are that CA.
type adeFixture struct {
	*harness
	idp     *webauthtest.Provider
	handler *ade.Handler
	// served records the personalised profile identities.
	served []ade.Identity
}

func newADEFixture(t *testing.T, mutate ...func(*ade.Config)) *adeFixture {
	t.Helper()
	f := &adeFixture{idp: webauthtest.New(t)}
	f.harness = newHarnessMounted(t, service.Config{}, newStore(t), newBus(), func(h *harness, mux *http.ServeMux) {
		signer, err := h.ca.Issue("profile signer", time.Now().Add(-time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		// The flow needs the server URL for its redirect, so it is built
		// on first use.
		flow := sync.OnceValues(func() (*webauth.Flow, error) {
			return webauth.New(webauth.Config{
				Issuer: f.idp.Server.URL, Endpoints: f.idp.Endpoints(), ClientID: "mdm-webview", RedirectURL: h.server.URL + "/ade/callback",
				StateStore: webauth.NewMemoryStore(), HTTPClient: f.idp.HTTPClient(),
				Authorizer: func(_ context.Context, _ webauth.Bound, c webauth.Claims) (webauth.Decision, error) {
					if !strings.HasSuffix(c.Email, "@example.com") {
						return webauth.Decision{}, webauth.ErrDenied
					}
					return webauth.Decision{Profile: "default"}, nil
				},
				Complete: func(ctx context.Context, bound webauth.Bound, claims webauth.Claims, _ webauth.Decision, w http.ResponseWriter, r *http.Request) {
					p, id, err := f.handler.Resume(ctx, bound.Serial)
					if err != nil {
						http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
						return
					}
					id.Subject = claims.Subject
					id.Claims = map[string]any{"email": claims.Email}
					f.handler.Finish(w, r, p, id)
				},
			})
		})
		cfg := ade.Config{
			Parse:  ade.ParseOptions{Anchors: []*x509.Certificate{h.ca.Cert}},
			Signer: ade.Signer{Cert: signer.Cert, Key: signer.Key},
			Profile: func(_ context.Context, p *ade.Parsed, id ade.Identity) (*enroll.Profile, error) {
				f.served = append(f.served, id)
				challenge, err := h.challenges.Issue(context.Background())
				if err != nil {
					return nil, err
				}
				cn := p.SERIAL
				if email, _ := id.Claims["email"].(string); email != "" {
					cn = email + "/" + p.SERIAL
				}
				return &enroll.Profile{
					Identifier: "com.example.e2e.ade", DisplayName: "go-apple-dm e2e", Organization: "go-apple-dm",
					Topic: pushTopic, ServerURL: h.server.URL + "/mdm", CheckInURL: h.server.URL + "/mdm",
					SCEP:  &enroll.SCEP{URL: h.server.URL + "/scep", Challenge: challenge, Subject: pkix.Name{CommonName: cn, Organization: []string{"go-apple-dm"}}},
					Roots: []*x509.Certificate{h.scepCA},
				}, nil
			},
			WebAuth: ade.WebAuthFunc(func(w http.ResponseWriter, r *http.Request, b ade.Bound) {
				fl, err := flow()
				if err == nil {
					err = fl.Begin(w, r, webauth.Bound{Serial: b.Serial, UDID: b.UDID})
				}
				if err != nil {
					t.Logf("web view begin: %v", err)
					http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}),
		}
		for _, m := range mutate {
			m(&cfg)
		}
		f.handler = ade.New(cfg)
		mux.Handle("/ade", f.handler)
		mux.HandleFunc("/ade/callback", func(w http.ResponseWriter, r *http.Request) {
			fl, err := flow()
			if err != nil {
				t.Logf("web view flow: %v", err)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			fl.Callback().ServeHTTP(w, r)
		})
	})
	// The redirect URL and the provider's expectations need the server URL.
	f.idp.Set(func(o *webauthtest.Options) {
		o.ClientID, o.RedirectURI = "mdm-webview", f.server.URL+"/ade/callback"
		o.Subject, o.Email, o.EmailVerified = "sub-alice", "alice@example.com", true
	})
	return f
}

// trustBoth is a client that trusts the harness and the identity provider,
// as the device's web view would through the DEP profile's anchor_certs.
func (f *adeFixture) trustBoth() *http.Client {
	pool := x509.NewCertPool()
	pool.AddCert(f.server.Certificate())
	pool.AddCert(f.idp.Certificate())
	return &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}}}
}

// TestE2E_ADEWebViewAuth is E2E-018: configuration_web_url receives the
// signed x-apple-aspen-deviceinfo, the OIDC flow completes against the
// fake provider with PKCE and nonce, and the profile served as
// application/x-apple-aspen-config is personalised with the identity.
func TestE2E_ADEWebViewAuth(t *testing.T) {
	ctx := context.Background()
	f := newADEFixture(t)
	d := f.device("UDID-ADE-WEB-1")
	d.SerialNumber, d.ProductName, d.OSVersion = "C02ADEWEB1", "Mac16,1", "26.0"
	d.Client = f.trustBoth()
	err := d.ADEEnroll(ctx, f.server.URL+"/ade", simulator.ADEOptions{WebView: func(_ context.Context, first *http.Response) (*http.Response, error) {
		// The client already followed the flow: our Begin redirect to the
		// provider, its redirect back with the code, and the profile.
		return first, nil
	}})
	if err != nil {
		t.Fatalf("ADE web view enrol: %v", err)
	}
	if len(f.served) != 1 || f.served[0].Serial != "C02ADEWEB1" || f.served[0].Subject != "sub-alice" || f.served[0].Claims["email"] != "alice@example.com" {
		t.Fatalf("served identity = %+v", f.served)
	}
	if !strings.Contains(d.Identity.Cert.Subject.CommonName, "alice@example.com") {
		t.Fatalf("profile not personalised: CN %q", d.Identity.Cert.Subject.CommonName)
	}
	if e, err := f.store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-ADE-WEB-1"}); err != nil || !e.Enabled {
		t.Fatalf("enrollment = %+v %v", e, err)
	}
	if auths := f.idp.Authorizes(); len(auths) != 1 || auths[0].CodeChallenge == "" || auths[0].Nonce == "" {
		t.Fatalf("provider saw %+v, want PKCE and nonce", auths)
	}

	// The token-based lane still works for a device that posts MachineInfo.
	d2 := f.device("UDID-ADE-POST-1")
	d2.SerialNumber, d2.ProductName, d2.OSVersion = "C02ADEPOST1", "Mac16,1", "26.0"
	if err := d2.ADEEnroll(ctx, f.server.URL+"/ade", simulator.ADEOptions{}); err != nil {
		t.Fatalf("ADE token lane: %v", err)
	}

	// Access denied at the provider ends the enrolment with 403.
	f.idp.Set(func(o *webauthtest.Options) { o.AuthorizeError = "access_denied" })
	d3 := f.device("UDID-ADE-WEB-2")
	d3.SerialNumber, d3.ProductName = "C02ADEWEB2", "Mac16,1"
	d3.Client = f.trustBoth()
	var herr *simulator.HTTPError
	err = d3.ADEEnroll(ctx, f.server.URL+"/ade", simulator.ADEOptions{WebView: func(_ context.Context, first *http.Response) (*http.Response, error) { return first, nil }})
	if !errors.As(err, &herr) || herr.Status != http.StatusForbidden {
		t.Fatalf("access denied = %v, want 403", err)
	}
	if _, err := f.store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-ADE-WEB-2"}); err == nil {
		t.Fatal("denied device enrolled")
	}
}
