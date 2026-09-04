package httpapi_test

import (
	"context"
	"crypto/x509/pkix"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/httpapi"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll/discovery"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/v3/pki/scep"
	"github.com/deploymenttheory/go-apple-dm/v3/simulator"
	"github.com/deploymenttheory/go-apple-dm/v3/storage/inmem"
	"github.com/deploymenttheory/go-apple-dm/v3/testpki"
)

// adHarness serves discovery, both account-driven flows, SCEP, and check-in.
type adHarness struct {
	srv    *httptest.Server
	store  *inmem.Store
	ca     *testpki.CA
	tokens *accountdriven.Tokens
	asweb  *accountdriven.AppleAsWeb
	oauth  *accountdriven.OAuth2
	// identity the fake sign-in pages grant.
	identity accountdriven.Identity
}

func newADHarness(t *testing.T, version string, oauth bool) *adHarness {
	t.Helper()
	h := &adHarness{store: inmem.New(), identity: accountdriven.Identity{UserIdentifier: "alice@example.com", ManagedAppleAccount: "alice@example.com"}}
	var err error
	if h.ca, err = testpki.NewCA("account-driven test CA"); err != nil {
		t.Fatal(err)
	}
	signer, err := h.ca.Issue("profile signer", time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	scepCert, scepKey, err := ca.NewSelfSigned(ca.SelfSignedOptions{Subject: pkix.Name{CommonName: "SCEP CA"}})
	if err != nil {
		t.Fatal(err)
	}
	local, err := ca.NewLocal(scepCert, scepKey, ca.WithDepot(ca.NewMemoryDepot()))
	if err != nil {
		t.Fatal(err)
	}
	scepServer, err := scep.NewServer(local, scepCert, scepKey, scep.WithChallenge(scep.StaticChallenge("secret")))
	if err != nil {
		t.Fatal(err)
	}
	core, err := service.New(service.Config{Store: h.store, Pinning: service.PinOff})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	// Apple requires https everywhere in this flow; httptest supplies the TLS.
	h.srv = httptest.NewTLSServer(mux)
	t.Cleanup(h.srv.Close)
	h.tokens = &accountdriven.Tokens{Store: accountdriven.NewMemStore()}
	h.asweb = &accountdriven.AppleAsWeb{URL: "https://mdm.example/authenticate", Tokens: h.tokens}
	h.oauth = &accountdriven.OAuth2{AuthorizationURL: "https://mdm.example/oauth2/authorize", TokenURL: h.srv.URL + "/oauth2/token",
		RedirectURL: "apple-remotemanagement-user-login:/oauth2/redirection", ClientID: "client-1", Scope: "MDM", Tokens: h.tokens}
	var auth accountdriven.Authenticator = h.asweb
	if oauth {
		auth = h.oauth
	}
	parse := func(r *http.Request) (*accountdriven.DeviceInfo, error) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		content, _, err := cms.VerifyAttached(raw, cms.VerifyOptions{Roots: h.ca.Pool()})
		if err != nil {
			return nil, err
		}
		var body struct {
			Language string `plist:"LANGUAGE"`
			Product  string `plist:"PRODUCT"`
			Version  string `plist:"VERSION"`
		}
		if err := plist.Unmarshal(content, &body); err != nil {
			return nil, err
		}
		return &accountdriven.DeviceInfo{Language: body.Language, Product: body.Product, Version: body.Version, Raw: content}, nil
	}
	handler, err := accountdriven.New(accountdriven.Config{Version: version, Parse: parse, Auth: auth, Tokens: h.tokens,
		Profile: func(context.Context, accountdriven.Identity, *accountdriven.DeviceInfo) (*enroll.Profile, error) {
			return &enroll.Profile{Identifier: "com.example.mdm", Topic: "com.apple.mgmt.External.simulator", ServerURL: h.srv.URL + "/mdm", CheckInURL: h.srv.URL + "/mdm",
				SCEP: &enroll.SCEP{URL: h.srv.URL + "/scep", Challenge: "secret"}}, nil
		}, SignCert: signer.Cert, SignKey: signer.Key})
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle(discovery.WellKnownPath, discovery.Handler(discovery.Config{Router: discovery.StaticRouter(map[discovery.ModelFamily]discovery.Server{
		discovery.ModelFamilyMac:    {Version: discovery.VersionADDE, BaseURL: h.srv.URL + "/enroll"},
		discovery.ModelFamilyIPhone: {Version: discovery.VersionBYOD, BaseURL: h.srv.URL + "/enroll"},
	})}))
	mux.Handle("/enroll", handler)
	mux.Handle("/scep", scepServer.Handler())
	mux.Handle("/mdm", httpapi.Handler(httpapi.Config{Checkin: core, Connect: core}))
	mux.Handle("/oauth2/token", h.oauth.TokenHandler())
	return h
}

// device is a phone with the built-in identity issued by the test CA.
func (h *adHarness) device(t *testing.T, udid string) *simulator.Device {
	t.Helper()
	id, err := h.ca.Issue(udid, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	d := simulator.New(udid, simulator.WithClient(h.srv.Client()), simulator.WithIdentity(&simulator.Identity{Cert: id.Cert, Key: id.Key}))
	d.ProductName = "iPhone17,2"
	return d
}

func TestAccountDrivenEnroll(t *testing.T) {
	ctx := context.Background()
	t.Run("AppleAsWeb", func(t *testing.T) {
		h := newADHarness(t, accountdriven.VersionBYOD, false)
		d := h.device(t, "UDID-AD-1")
		var seen simulator.AuthChallenge
		res, err := d.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "alice@example.com", DiscoveryURL: h.srv.URL,
			Authenticate: func(_ context.Context, c simulator.AuthChallenge) (string, error) {
				seen = c
				// The web page authenticates alice and finishes the flow.
				rec := httptest.NewRecorder()
				if err := h.asweb.Finish(rec, httptest.NewRequest(http.MethodPost, "/authenticate-results", nil), h.identity); err != nil {
					return "", err
				}
				return simulator.AccessTokenFromRedirect(rec.Header().Get("Location"))
			}})
		if err != nil {
			t.Fatalf("enroll: %v", err)
		}
		if seen.Method != "apple-as-web" || seen.URL != h.asweb.URL {
			t.Fatalf("challenge = %+v", seen)
		}
		if res.Chosen.Version != discovery.VersionBYOD || len(res.Profile) == 0 {
			t.Fatalf("result = %+v", res)
		}
		p, err := enroll.Parse(res.Profile, profile.ParseOptions{})
		if err != nil || p.EnrollmentMode != "BYOD" || p.AssignedManagedAppleID != "alice@example.com" {
			t.Fatalf("profile = %+v %v", p, err)
		}
		if d.EnrollmentID == "" {
			t.Fatal("no EnrollmentID after account-driven enrollment")
		}
		e, err := h.store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: d.EnrollmentID})
		if err != nil || !e.Enabled {
			t.Fatalf("user enrollment record = %+v %v", e, err)
		}
		if _, err := h.store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "UDID-AD-1"}); err == nil {
			t.Fatal("UDID leaked into a device channel enrollment")
		}
		// The user channel of a User Enrollment carries EnrollmentUserID.
		u := d.User("alice", "alice", "Alice")
		if err := u.TokenUpdate(ctx); err != nil {
			t.Fatalf("user channel: %v", err)
		}
		if _, err := h.store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentUser, ID: d.EnrollmentID + ":alice", ParentID: d.EnrollmentID}); err != nil {
			t.Fatalf("user enrollment user channel: %v", err)
		}
		if got, err := d.Connect(ctx); err != nil || len(got) != 0 {
			t.Fatalf("connect: %v %v", got, err)
		}
	})
	t.Run("OAuth2", func(t *testing.T) {
		h := newADHarness(t, accountdriven.VersionBYOD, true)
		d := h.device(t, "UDID-AD-2")
		_, err := d.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "alice@example.com", DiscoveryURL: h.srv.URL,
			Authenticate: func(ctx context.Context, c simulator.AuthChallenge) (string, error) {
				if c.Method != "apple-oauth2" {
					return "", errors.New("wrong method")
				}
				return d.OAuth2CodeFlow(ctx, c, "alice@example.com", func(_ context.Context, authorizationURL string) (string, error) {
					// The person signs in on the authorization page.
					r := httptest.NewRequest(http.MethodGet, authorizationURL, nil)
					req, err := h.oauth.ParseAuthorization(r)
					if err != nil {
						return "", err
					}
					if req.LoginHint != "alice@example.com" {
						return "", errors.New("login_hint missing")
					}
					rec := httptest.NewRecorder()
					if err := h.oauth.Grant(rec, r, req, h.identity); err != nil {
						return "", err
					}
					return rec.Header().Get("Location"), nil
				})
			}})
		if err != nil {
			t.Fatalf("oauth2 enroll: %v", err)
		}
		if d.EnrollmentID == "" {
			t.Fatal("no EnrollmentID")
		}
	})
	t.Run("Errors", func(t *testing.T) {
		h := newADHarness(t, accountdriven.VersionBYOD, false)
		d := h.device(t, "UDID-AD-3")
		noop := func(context.Context, simulator.AuthChallenge) (string, error) { return "", nil }
		if _, err := d.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "alice@example.com"}); !errors.Is(err, simulator.ErrAccountDriven) {
			t.Fatalf("no authenticate = %v", err)
		}
		if _, err := d.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "alice", Authenticate: noop}); !errors.Is(err, simulator.ErrAccountDriven) {
			t.Fatalf("bad identifier = %v", err)
		}
		var herr *simulator.HTTPError
		if _, err := d.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "alice@example.com", DiscoveryURL: h.srv.URL + "/nowhere", Authenticate: noop}); !errors.As(err, &herr) {
			t.Fatalf("discovery 404 = %v", err)
		}
		// Watches are not routed: the router rejects with 403.
		if _, err := d.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "alice@example.com", DiscoveryURL: h.srv.URL, ModelFamily: "Watch", Authenticate: noop}); !errors.As(err, &herr) || herr.Status != http.StatusForbidden {
			t.Fatalf("rejected family = %v", err)
		}
		// Authentication that fails stops the flow.
		if _, err := d.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "alice@example.com", DiscoveryURL: h.srv.URL,
			Authenticate: func(context.Context, simulator.AuthChallenge) (string, error) { return "", errors.New("cancelled") }}); !errors.Is(err, simulator.ErrAccountDriven) {
			t.Fatalf("cancelled = %v", err)
		}
		// A wrong bearer: the server answers 401 again.
		if _, err := d.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "alice@example.com", DiscoveryURL: h.srv.URL, Authenticate: func(context.Context, simulator.AuthChallenge) (string, error) { return "bogus", nil }}); !errors.As(err, &herr) || herr.Status != http.StatusUnauthorized {
			t.Fatalf("bogus bearer = %v", err)
		}
		// No identity to sign the body.
		bare := simulator.New("UDID-AD-4", simulator.WithClient(h.srv.Client()))
		if _, err := bare.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "alice@example.com", DiscoveryURL: h.srv.URL, Authenticate: noop}); !errors.Is(err, simulator.ErrAccountDriven) {
			t.Fatalf("no identity = %v", err)
		}
		for _, bad := range []string{"Basic realm=x", "Bearer url=\"x\""} {
			if _, err := simulator.ParseAuthChallenge(bad); !errors.Is(err, simulator.ErrAccountDriven) {
				t.Fatalf("%q: %v", bad, err)
			}
		}
		for _, bad := range []string{"https://x/?access-token=1", "apple-remotemanagement-user-login://authentication-results?x=1", "::"} {
			if _, err := simulator.AccessTokenFromRedirect(bad); !errors.Is(err, simulator.ErrAccountDriven) {
				t.Fatalf("%q: %v", bad, err)
			}
		}
		c := simulator.AuthChallenge{Method: "apple-oauth2", AuthorizationURL: "https://x/a?y=1", TokenURL: h.srv.URL + "/oauth2/token", RedirectURL: "apple-remotemanagement-user-login:/r", ClientID: "client-1"}
		for name, authorize := range map[string]func(context.Context, string) (string, error){
			"error":        func(context.Context, string) (string, error) { return "", errors.New("nope") },
			"wrong scheme": func(context.Context, string) (string, error) { return "https://x/?code=1&state=s", nil },
			"bad state": func(context.Context, string) (string, error) {
				return "apple-remotemanagement-user-login:/r?code=1&state=other", nil
			},
			"no code": func(_ context.Context, u string) (string, error) {
				state := strings.Split(strings.Split(u, "state=")[1], "&")[0]
				return "apple-remotemanagement-user-login:/r?state=" + state, nil
			},
			"token rejected": func(_ context.Context, u string) (string, error) {
				state := strings.Split(strings.Split(u, "state=")[1], "&")[0]
				return "apple-remotemanagement-user-login:/r?code=bogus&state=" + state, nil
			},
		} {
			if _, err := d.OAuth2CodeFlow(ctx, c, "alice@example.com", authorize); err == nil {
				t.Fatalf("%s: no error", name)
			}
		}
	})
}
