//go:build e2e

package e2e

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/enroll/discovery"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/v3/simulator"
)

// adFixture is the account-driven side of a harness: discovery, one
// enrollment endpoint per version, the two authenticators, and the tokens.
type adFixture struct {
	*harness
	tokens *accountdriven.Tokens
	asweb  *accountdriven.AppleAsWeb
	oauth  *accountdriven.OAuth2
}

// newADFixture mounts service discovery and both enrollment endpoints. The
// device's built-in identity is played by a certificate from the test CA,
// so the body parser verifies against that pool.
func newADFixture(t *testing.T, oauth bool) *adFixture {
	t.Helper()
	f := &adFixture{}
	f.tokens = &accountdriven.Tokens{Store: accountdriven.NewMemStore()}
	f.asweb = &accountdriven.AppleAsWeb{URL: "https://mdm.example/authenticate", Tokens: f.tokens}
	cfg := service.Config{Hooks: []service.Hook{&accountdriven.CheckinHook{Tokens: f.tokens}}}
	f.harness = newHarnessMounted(t, cfg, newStore(t), newBus(), func(h *harness, mux *http.ServeMux) {
		signer, err := h.ca.Issue("profile signer", time.Now().Add(-time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		f.oauth = &accountdriven.OAuth2{AuthorizationURL: "https://mdm.example/oauth2/authorize", TokenURL: "https://placeholder/oauth2/token",
			RedirectURL: "apple-remotemanagement-user-login:/oauth2/redirection", ClientID: "mdm-client", Scope: "MDM", Tokens: f.tokens}
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
		profileHook := func(_ context.Context, id accountdriven.Identity, info *accountdriven.DeviceInfo) (*enroll.Profile, error) {
			challenge, err := h.challenges.Issue(context.Background())
			if err != nil {
				return nil, err
			}
			return &enroll.Profile{
				Identifier: "com.example.e2e.account-driven", DisplayName: "go-apple-dm e2e", Organization: "go-apple-dm",
				Topic: pushTopic, ServerURL: h.server.URL + "/mdm", CheckInURL: h.server.URL + "/mdm",
				SCEP:  &enroll.SCEP{URL: h.server.URL + "/scep", Challenge: challenge, Subject: pkix.Name{CommonName: id.ManagedAppleAccount + "/" + info.Product, Organization: []string{"go-apple-dm"}}},
				Roots: []*x509.Certificate{h.scepCA},
			}, nil
		}
		for _, version := range []string{accountdriven.VersionBYOD, accountdriven.VersionADDE} {
			var auth accountdriven.Authenticator = f.asweb
			if oauth {
				auth = f.oauth
			}
			handler, err := accountdriven.New(accountdriven.Config{Version: version, Parse: parse, Auth: auth, Tokens: f.tokens, Profile: profileHook, SignCert: signer.Cert, SignKey: signer.Key})
			if err != nil {
				t.Fatal(err)
			}
			mux.Handle("/enroll/"+version, handler)
		}
		mux.Handle("/oauth2/token", f.oauth.TokenHandler())
		mux.Handle(discovery.WellKnownPath, discovery.Handler(discovery.Config{Router: func(_ context.Context, req discovery.Request) ([]discovery.Server, error) {
			switch req.ModelFamily {
			case discovery.ModelFamilyMac:
				return []discovery.Server{{Version: discovery.VersionADDE, BaseURL: h.server.URL + "/enroll/" + accountdriven.VersionADDE}}, nil
			case discovery.ModelFamilyIPhone, discovery.ModelFamilyIPad:
				return []discovery.Server{{Version: discovery.VersionBYOD, BaseURL: h.server.URL + "/enroll/" + accountdriven.VersionBYOD}}, nil
			}
			return nil, discovery.Reject("this device cannot enrol here", "model family "+string(req.ModelFamily))
		}}))
	})
	// The token endpoint URL is only known once the server exists.
	f.oauth.TokenURL = f.server.URL + "/oauth2/token"
	return f
}

func (f *adFixture) identity(user string) accountdriven.Identity {
	return accountdriven.Identity{UserIdentifier: user, ManagedAppleAccount: user, Subject: "sub-" + user}
}

// asWebAuthenticate plays the sign-in page of the apple-as-web flow.
func (f *adFixture) asWebAuthenticate(user string) func(context.Context, simulator.AuthChallenge) (string, error) {
	return func(_ context.Context, c simulator.AuthChallenge) (string, error) {
		if c.Method != accountdriven.MethodAppleAsWeb || c.URL != f.asweb.URL {
			return "", errors.New("unexpected challenge")
		}
		rec := httptest.NewRecorder()
		if err := f.asweb.Finish(rec, httptest.NewRequest(http.MethodPost, "/authenticate-results", nil), f.identity(user)); err != nil {
			return "", err
		}
		return simulator.AccessTokenFromRedirect(rec.Header().Get("Location"))
	}
}

// TestE2E_ServiceDiscovery is E2E-012: discovery routes a Mac to
// mdm-adde and an iPhone to mdm-byod; each enrols through the
// apple-as-web flow with the right EnrollmentMode, the enrollment token
// authorises the check-in, and a check-in without it is refused.
func TestE2E_ServiceDiscovery(t *testing.T) {
	ctx := context.Background()
	f := newADFixture(t, false)
	mac := f.device("UDID-MAC-1")
	mac.ProductName, mac.OSVersion = "Mac16,1", "26.0"
	res, err := mac.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "mac@example.com", DiscoveryURL: f.server.URL,
		Authenticate: f.asWebAuthenticate("mac@example.com"), Parse: profile.ParseOptions{}})
	if err != nil {
		t.Fatalf("mac: %v", err)
	}
	if res.Chosen.Version != discovery.VersionADDE {
		t.Fatalf("mac routed to %s", res.Chosen.Version)
	}
	p, err := enroll.Parse(res.Profile, profile.ParseOptions{})
	if err != nil || p.EnrollmentMode != accountdriven.ModeADDE || p.AssignedManagedAppleID != "mac@example.com" {
		t.Fatalf("mac profile = %+v %v", p, err)
	}
	if e, err := f.store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: mac.EnrollmentID}); err != nil || !e.Enabled {
		t.Fatalf("mac enrollment = %+v %v", e, err)
	}

	phone := f.device("UDID-PHONE-1")
	phone.ProductName, phone.OSVersion = "iPhone17,2", "26.0"
	res, err = phone.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "phone@example.com", DiscoveryURL: f.server.URL,
		Authenticate: f.asWebAuthenticate("phone@example.com")})
	if err != nil {
		t.Fatalf("phone: %v", err)
	}
	if res.Chosen.Version != discovery.VersionBYOD {
		t.Fatalf("phone routed to %s", res.Chosen.Version)
	}
	p, _ = enroll.Parse(res.Profile, profile.ParseOptions{})
	if p.EnrollmentMode != accountdriven.ModeBYOD {
		t.Fatalf("phone mode = %s", p.EnrollmentMode)
	}
	if got, err := phone.Connect(ctx); err != nil || len(got) != 0 {
		t.Fatalf("phone connect: %v %v", got, err)
	}

	// A watch is rejected with Apple's well-known.failed document.
	watch := f.device("UDID-WATCH-1")
	watch.ProductName = "Watch7,1"
	var herr *simulator.HTTPError
	if _, err := watch.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "w@example.com", DiscoveryURL: f.server.URL, Authenticate: f.asWebAuthenticate("w@example.com")}); !errors.As(err, &herr) || herr.Status != http.StatusForbidden {
		t.Fatalf("watch = %v", err)
	}

	// Without the enrollment token the check-in is refused (403), and a
	// retried check-in with it succeeds because the token is not consumed.
	withToken := phone.CheckinURL
	phone.CheckinURL = f.server.URL + "/mdm"
	if err := phone.TokenUpdate(ctx); !errors.As(err, &herr) || herr.Status != http.StatusForbidden {
		t.Fatalf("check-in without enrollment token = %v", err)
	}
	phone.CheckinURL = withToken
	for range 2 {
		if err := phone.TokenUpdate(ctx); err != nil {
			t.Fatalf("retried check-in with the enrollment token: %v", err)
		}
	}
	if f.countEvents("enrolled") == 0 {
		t.Fatalf("events = %v", f.eventTypes())
	}
}

func (f *adFixture) countEvents(name string) int {
	n := 0
	for _, e := range f.eventTypes() {
		if string(e) == name {
			n++
		}
	}
	return n
}

// TestE2E_AccountDrivenOAuth2 is E2E-019: the apple-oauth2 flow end to
// end, including a refresh.
func TestE2E_AccountDrivenOAuth2(t *testing.T) {
	ctx := context.Background()
	f := newADFixture(t, true)
	phone := f.device("UDID-PHONE-2")
	phone.ProductName, phone.OSVersion = "iPhone17,2", "26.0"
	var challenge simulator.AuthChallenge
	res, err := phone.AccountDrivenEnroll(ctx, simulator.AccountDrivenOptions{UserIdentifier: "oauth@example.com", DiscoveryURL: f.server.URL,
		Authenticate: func(ctx context.Context, c simulator.AuthChallenge) (string, error) {
			challenge = c
			return phone.OAuth2CodeFlow(ctx, c, "oauth@example.com", func(_ context.Context, authorizationURL string) (string, error) {
				r := httptest.NewRequest(http.MethodGet, authorizationURL, nil)
				req, err := f.oauth.ParseAuthorization(r)
				if err != nil {
					return "", err
				}
				if req.LoginHint != "oauth@example.com" {
					return "", errors.New("login_hint not forwarded")
				}
				rec := httptest.NewRecorder()
				if err := f.oauth.Grant(rec, r, req, f.identity("oauth@example.com")); err != nil {
					return "", err
				}
				return rec.Header().Get("Location"), nil
			})
		}})
	if err != nil {
		t.Fatalf("oauth2 enrol: %v", err)
	}
	if challenge.Method != accountdriven.MethodAppleOAuth2 || challenge.TokenURL != f.oauth.TokenURL || challenge.ClientID != "mdm-client" || challenge.Scope != "MDM" {
		t.Fatalf("challenge = %+v", challenge)
	}
	p, err := enroll.Parse(res.Profile, profile.ParseOptions{})
	if err != nil || p.EnrollmentMode != accountdriven.ModeBYOD || p.AssignedManagedAppleID != "oauth@example.com" {
		t.Fatalf("profile = %+v %v", p, err)
	}
	if e, err := f.store.Get(ctx, mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: phone.EnrollmentID}); err != nil || !e.Enabled {
		t.Fatalf("enrollment = %+v %v", e, err)
	}
	if got, err := phone.Connect(ctx); err != nil || len(got) != 0 {
		t.Fatalf("connect: %v %v", got, err)
	}
}
