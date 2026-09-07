package accountdriven_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/dmhook"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/secrets"
	"github.com/deploymenttheory/go-apple-dm/state"
	"github.com/deploymenttheory/go-apple-dm/testpki"
)

func TestBearerIdentityAndChannelRules(t *testing.T) {
	ctx := t.Context()
	ca, err := testpki.NewCA("account association")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		product, origin string
		channel         mdm.Channel
		bearer          bool
	}{
		{"iPhone17,2", accountdriven.VersionBYOD, mdm.ChannelUserEnrollmentDevice, true},
		{"iPad16,2", accountdriven.VersionADDE, mdm.ChannelDevice, true},
		{"RealityDevice1,1", accountdriven.VersionBYOD, mdm.ChannelUserEnrollmentDevice, true},
		{"MacBookPro18,1", accountdriven.VersionBYOD, mdm.ChannelUserEnrollmentDevice, false},
		{"Mac14,2", accountdriven.VersionADDE, mdm.ChannelDevice, false},
		{"iMac21,1", accountdriven.VersionADDE, mdm.ChannelDevice, false},
	} {
		t.Run(tc.product+tc.origin, func(t *testing.T) {
			now := time.Now()
			tk := &accountdriven.Tokens{Store: accountdriven.NewMemStore(), Now: func() time.Time { return now }}
			assocs := tk.AssociationStore()
			a, err := assocs.Create(ctx, alice, tc.origin, tc.product)
			if err != nil {
				t.Fatal(err)
			}
			cert, err := ca.Issue(accountdriven.CertificateSubjectPrefix+a.Reference, now.Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			if err := assocs.RegisterCertificate(ctx, cert.Cert); err != nil {
				t.Fatal(err)
			}
			access, err := tk.Issue(ctx, accountdriven.KindAccess, alice, nil)
			if err != nil {
				t.Fatal(err)
			}
			h := &accountdriven.CheckinHook{Tokens: tk, Auth: &accountdriven.AppleAsWeb{URL: "https://mdm.example/auth", Tokens: tk}}
			r := &mdm.Request{ID: mdm.EnrollmentID{Channel: tc.channel, ID: "device"}, Certificate: cert.Cert}
			call := &dmhook.Call{Op: "checkin:Authenticate", Request: r}
			_, err = h.Before(ctx, call)
			if tc.bearer {
				var reauth *accountdriven.Reauthentication
				if !errors.As(err, &reauth) {
					t.Fatal("missing bearer", err)
				}
				r.Bearer = secrets.New([]byte(access))
			} else if err != nil {
				t.Fatal("macOS device channel omission", err)
			}
			bound, err := h.Before(ctx, call)
			if err != nil {
				t.Fatal(err)
			}
			if got, ok := accountdriven.IdentityFromContext(bound); !ok || got.Subject != alice.Subject {
				t.Fatal(got, ok)
			}
			before, _ := assocs.Get(ctx, a.Reference)
			if before.Enrollment.ID != "device" || !before.ConfirmedAt.IsZero() {
				t.Fatal(before)
			}
			if _, err := h.Before(ctx, &dmhook.Call{Op: "connect", Request: r}); !errors.Is(err, accountdriven.ErrAssociation) {
				t.Fatal("pending claim allowed ongoing traffic", err)
			}
			if err := h.Complete(bound, call); err != nil {
				t.Fatal(err)
			}
			for _, op := range []string{"checkin:Authenticate", "checkin:TokenUpdate", "checkin:GetBootstrapToken", "checkin:DeclarativeManagement", "connect"} {
				if _, err := h.Before(ctx, &dmhook.Call{Op: op, Request: r}); err != nil {
					t.Fatal(op, err)
				}
			}
			// A valid certificate and token cannot claim a second device identity.
			other := *r
			other.ID.ID = "other-device"
			if _, err := h.Before(ctx, &dmhook.Call{Op: "checkin:Authenticate", Request: &other}); !errors.Is(err, accountdriven.ErrAssociation) {
				t.Fatal("identity replay", err)
			}
			// User channels always carry bearer, including on macOS.
			user := *r
			ch := mdm.ChannelUser
			if tc.channel == mdm.ChannelUserEnrollmentDevice {
				ch = mdm.ChannelUserEnrollmentUser
			}
			user.ID = mdm.EnrollmentID{Channel: ch, ID: "device:user", ParentID: "device"}
			user.Bearer = secrets.Secret{}
			if _, err := h.Before(ctx, &dmhook.Call{Op: "checkin:TokenUpdate", Request: &user}); err == nil {
				t.Fatal("user bearer omitted")
			}
			user.Bearer = secrets.New([]byte(access))
			if _, err := h.Before(ctx, &dmhook.Call{Op: "checkin:TokenUpdate", Request: &user}); err != nil {
				t.Fatal(err)
			}
			bob := alice
			bob.Subject = "another-subject"
			wrong, _ := tk.Issue(ctx, accountdriven.KindAccess, bob, nil)
			user.Bearer = secrets.New([]byte(wrong))
			if _, err := h.Before(ctx, &dmhook.Call{Op: "connect", Request: &user}); !errors.Is(err, accountdriven.ErrAssociation) {
				t.Fatal("wrong identity accepted", err)
			}
			bob = alice
			bob.ManagedAppleAccount = "bob@example.com"
			wrong, _ = tk.Issue(ctx, accountdriven.KindAccess, bob, nil)
			user.Bearer = secrets.New([]byte(wrong))
			if _, err := h.Before(ctx, &dmhook.Call{Op: "connect", Request: &user}); !errors.Is(err, accountdriven.ErrAssociation) {
				t.Fatal(err)
			}
			user.Bearer = secrets.New([]byte(access))
			now = now.Add(time.Hour)
			if _, err := h.Before(ctx, &dmhook.Call{Op: "connect", Request: &user}); err == nil {
				t.Fatal("expired bearer accepted")
			}
			fake, err := ca.Issue(accountdriven.CertificateSubjectPrefix+a.Reference, now.Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			other.Certificate = fake.Cert
			other.ID = r.ID
			if _, err := h.Before(ctx, &dmhook.Call{Op: "checkin:Authenticate", Request: &other}); !errors.Is(err, accountdriven.ErrEnrollmentToken) {
				t.Fatal("subject alone authorized", err)
			}
		})
	}
}

func TestAssociationClaimRaceAndChallenge(t *testing.T) {
	ctx := t.Context()
	st := state.NewMemory()
	now := time.Now()
	st.Now = func() time.Time { return now }
	a := &accountdriven.Associations{Store: st}
	association, err := a.Create(ctx, alice, accountdriven.VersionADDE, "iPhone17,2")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var won atomic.Int64
	for i := range 20 {
		wg.Go(func() {
			if err := a.Bind(ctx, association.Reference, mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: fmt.Sprint(i)}, false); err == nil {
				won.Add(1)
			} else if !errors.Is(err, accountdriven.ErrAssociation) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if won.Load() != 1 {
		t.Fatal(won.Load())
	}
	password, err := a.IssueSCEPChallenge(ctx, association.Reference, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	csr := func() *x509.CertificateRequest {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		raw, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: accountdriven.CertificateSubjectPrefix + association.Reference}}, key)
		out, _ := x509.ParseCertificateRequest(raw)
		return out
	}
	first := csr()
	if err := a.VerifySCEPChallenge(ctx, "wrong", first); err == nil {
		t.Fatal("wrong challenge")
	}
	for range 2 {
		if err := a.VerifySCEPChallenge(ctx, password, first); err != nil {
			t.Fatal("retry", err)
		}
	}
	if err := a.VerifySCEPChallenge(ctx, password, csr()); !errors.Is(err, accountdriven.ErrAssociation) {
		t.Fatal("different CSR", err)
	}
	now = now.Add(2 * time.Minute)
	if err := a.VerifySCEPChallenge(ctx, password, first); !errors.Is(err, accountdriven.ErrAssociation) {
		t.Fatal("expired challenge", err)
	}
	if _, err := a.IssueSCEPChallenge(ctx, association.Reference, 0); !errors.Is(err, accountdriven.ErrConfig) {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		id              accountdriven.Identity
		origin, product string
	}{{accountdriven.Identity{}, accountdriven.VersionBYOD, "Mac"}, {alice, "wrong", "Mac"}, {alice, accountdriven.VersionBYOD, ""}} {
		if _, err := a.Create(ctx, bad.id, bad.origin, bad.product); err == nil {
			t.Fatal(bad)
		}
	}
	if err := a.RegisterCertificate(ctx, nil); err == nil {
		t.Fatal("nil certificate")
	}
	if err := a.Bind(ctx, association.Reference, mdm.EnrollmentID{}, true); err == nil {
		t.Fatal("empty identity")
	}
	if err := a.VerifySCEPChallenge(ctx, password, nil); err == nil {
		t.Fatal("nil CSR")
	}
}

func TestOAuthMetadataBeforeConsumeAndConcurrentRotation(t *testing.T) {
	ctx := t.Context()
	now := time.Now()
	tokens := &accountdriven.Tokens{Store: accountdriven.NewMemStore(), Now: func() time.Time { return now }}
	o := &accountdriven.OAuth2{Tokens: tokens, ClientID: "client", Scope: "MDM", RedirectURL: "apple-remotemanagement-user-login:/oauth2/redirection", AccessTTL: time.Minute}
	meta := map[string]string{"client_id": o.ClientID, "redirect_uri": o.RedirectURL, "scope": o.Scope}
	code, _ := tokens.Issue(ctx, accountdriven.KindCode, alice, meta)
	post := func(form url.Values) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/token", strings.NewReader(form.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		o.TokenHandler().ServeHTTP(w, r)
		return w
	}
	for _, field := range []string{"client_id", "redirect_uri", "scope"} {
		f := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {o.ClientID}, "redirect_uri": {o.RedirectURL}, "scope": {o.Scope}}
		f.Set(field, "wrong")
		if w := post(f); w.Code == 200 {
			t.Fatal(field)
		}
		if _, err := tokens.Check(ctx, accountdriven.KindCode, code); err != nil {
			t.Fatal("bad request burned code", err)
		}
	}
	// Stored metadata, not just endpoint configuration, binds a code.
	foreign, _ := tokens.Issue(ctx, accountdriven.KindCode, alice, map[string]string{"client_id": "other", "redirect_uri": o.RedirectURL, "scope": o.Scope})
	if w := post(url.Values{"grant_type": {"authorization_code"}, "code": {foreign}, "client_id": {o.ClientID}, "redirect_uri": {o.RedirectURL}}); w.Code != 400 {
		t.Fatal(w.Code)
	}
	w := post(url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {o.ClientID}, "redirect_uri": {o.RedirectURL}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var first accountdriven.TokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	rec, err := tokens.Check(ctx, accountdriven.KindAccess, first.AccessToken)
	if err != nil || rec.ExpiresAt.Sub(rec.IssuedAt) != time.Minute || first.ExpiresIn != 60 {
		t.Fatal(rec, err, first.ExpiresIn)
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {first.RefreshToken}, "client_id": {o.ClientID}}
	var wg sync.WaitGroup
	var wins atomic.Int64
	for range 30 {
		wg.Go(func() {
			w := post(form)
			if w.Code == 200 {
				wins.Add(1)
			} else if w.Code != 400 {
				t.Error(w.Code)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatal("refresh race", wins.Load())
	}
	if _, err := tokens.Check(ctx, accountdriven.KindAccess, first.AccessToken); !errors.Is(err, accountdriven.ErrTokenNotFound) {
		t.Fatal("replaced access still valid", err)
	}
	// Apple-shaped authorization has no PKCE parameters and remains accepted.
	u := "/authorize?" + url.Values{"response_type": {"code"}, "client_id": {o.ClientID}, "redirect_uri": {o.RedirectURL}, "state": {"s"}}.Encode()
	if _, err := o.ParseAuthorization(httptest.NewRequest("GET", u, nil)); err != nil {
		t.Fatal(err)
	}
	if err := o.Grant(httptest.NewRecorder(), httptest.NewRequest("GET", u, nil), nil, alice); err == nil {
		t.Fatal("nil authorization")
	}
}

func TestRedactionAndExternalVerifier(t *testing.T) {
	for _, header := range []string{"", "Basic secret", "Bearer ", "Bearer a b", "Bearer a,b", "Bearer a\tb"} {
		if !accountdriven.Bearer(header).IsZero() {
			t.Fatal(header)
		}
	}
	secret := accountdriven.Bearer("bEaReR hidden-secret")
	if secret.IsZero() || strings.Contains(fmt.Sprintf("%+v", mdm.Request{Bearer: secret}), "hidden-secret") {
		t.Fatal("bearer leaked")
	}
	verify := accountdriven.VerifyFunc(func(_ context.Context, s secrets.Secret) (accountdriven.Identity, error) {
		if !s.Equal(secret) {
			return accountdriven.Identity{}, accountdriven.ErrTokenNotFound
		}
		return alice, nil
	})
	if _, err := verify.Verify(t.Context(), secrets.Secret{}); !errors.Is(err, accountdriven.ErrTokenNotFound) {
		t.Fatal(err)
	}
	if id, err := verify.Verify(t.Context(), secret); err != nil || id.Subject != alice.Subject {
		t.Fatal(id, err)
	}
}
