package accountdriven_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/enroll"
	"github.com/deploymenttheory/go-apple-dm/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/internal/testpki"
)

// failingStore fails the named operations.
type failingStore struct {
	accountdriven.TokenStore
	fail map[string]error
}

func (f *failingStore) Put(ctx context.Context, h string, r accountdriven.Record) error {
	if err := f.fail["Put"]; err != nil {
		return err
	}
	return f.TokenStore.Put(ctx, h, r)
}

func (f *failingStore) Get(ctx context.Context, h string) (accountdriven.Record, error) {
	if err := f.fail["Get"]; err != nil {
		return accountdriven.Record{}, err
	}
	return f.TokenStore.Get(ctx, h)
}

func (f *failingStore) MarkUsed(ctx context.Context, h string, at time.Time) error {
	if err := f.fail["MarkUsed"]; err != nil {
		return err
	}
	return f.TokenStore.MarkUsed(ctx, h, at)
}

func TestStoreFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("disk on fire")
	ca, _ := testpki.NewCA("ca")
	signer, _ := ca.Issue("s", time.Now().Add(-time.Minute))
	newFailing := func(fail map[string]error) (*accountdriven.Tokens, *failingStore) {
		fs := &failingStore{TokenStore: accountdriven.NewMemStore(), fail: fail}
		return &accountdriven.Tokens{Store: fs, Now: func() time.Time { return t0 }}, fs
	}
	t.Run("IssueSurfacesPut", func(t *testing.T) {
		tk, _ := newFailing(map[string]error{"Put": boom})
		if _, err := tk.Issue(ctx, accountdriven.KindAccess, alice, nil); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("ConsumeSurfacesGetAndMarkUsed", func(t *testing.T) {
		tk, fs := newFailing(map[string]error{})
		tok, _ := tk.Issue(ctx, accountdriven.KindAccess, alice, nil)
		fs.fail["MarkUsed"] = boom
		if _, err := tk.Consume(ctx, accountdriven.KindAccess, tok); !errors.Is(err, boom) {
			t.Fatalf("mark used = %v", err)
		}
		fs.fail["Get"] = boom
		if _, err := tk.Check(ctx, accountdriven.KindAccess, tok); !errors.Is(err, boom) {
			t.Fatalf("get = %v", err)
		}
	})
	t.Run("HandlerEnrollmentTokenIssueFails", func(t *testing.T) {
		tk, fs := newFailing(map[string]error{})
		access, _ := tk.Issue(ctx, accountdriven.KindAccess, alice, nil)
		h, _ := accountdriven.New(accountdriven.Config{Version: accountdriven.VersionBYOD, Parse: parseBody, Auth: &accountdriven.AppleAsWeb{URL: "https://x/a", Tokens: tk}, Tokens: tk, Profile: baseProfile, SignCert: signer.Cert, SignKey: signer.Key})
		fs.fail["Put"] = boom
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/enroll", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+access)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d", rec.Code)
		}
	})
	t.Run("AsWebFinishIssueFails", func(t *testing.T) {
		tk, _ := newFailing(map[string]error{"Put": boom})
		a := &accountdriven.AppleAsWeb{URL: "https://x/a", Tokens: tk}
		if err := a.Finish(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/x", nil), alice); !errors.Is(err, boom) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("OAuth2GrantAndTokenFailures", func(t *testing.T) {
		tk, fs := newFailing(map[string]error{})
		o := &accountdriven.OAuth2{AuthorizationURL: "https://x/a", TokenURL: "https://x/t", RedirectURL: "apple-remotemanagement-user-login:/r", ClientID: "c", Scope: "s", Tokens: tk}
		r := httptest.NewRequest(http.MethodGet, "/a?response_type=code&client_id=c&redirect_uri=apple-remotemanagement-user-login:/r&state=s", nil)
		req, err := o.ParseAuthorization(r)
		if err != nil {
			t.Fatal(err)
		}
		fs.fail["Put"] = boom
		if err := o.Grant(httptest.NewRecorder(), r, req, alice); !errors.Is(err, boom) {
			t.Fatalf("grant = %v", err)
		}
		delete(fs.fail, "Put")
		code, _ := tk.Issue(ctx, accountdriven.KindCode, alice, nil)
		fs.fail["Put"] = boom
		form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {o.RedirectURL}, "client_id": {"c"}}
		rec := httptest.NewRecorder()
		tr := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader(form.Encode()))
		tr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		o.TokenHandler().ServeHTTP(rec, tr)
		if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "server_error") {
			t.Fatalf("token endpoint = %d %s", rec.Code, rec.Body.String())
		}
		// Malformed form body.
		rec = httptest.NewRecorder()
		bad := httptest.NewRequest(http.MethodPost, "/t", strings.NewReader("%zz"))
		bad.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		o.TokenHandler().ServeHTTP(rec, bad)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("malformed form = %d", rec.Code)
		}
		// A redirect URL that cannot be parsed fails Grant.
		o2 := *o
		o2.RedirectURL = "::bad"
		delete(fs.fail, "Put")
		if err := o2.Grant(httptest.NewRecorder(), r, req, alice); !errors.Is(err, accountdriven.ErrOAuth2Request) {
			t.Fatalf("bad redirect = %v", err)
		}
		if _, err := o.Challenge(ctx, nil, nil); err != nil {
			t.Fatal(err)
		}
		if o.AccessTTL = time.Minute; o.AccessTTL != time.Minute {
			t.Fatal("unreachable")
		}
		code2, _ := tk.Issue(ctx, accountdriven.KindCode, alice, nil)
		rec = httptest.NewRecorder()
		tr = httptest.NewRequest(http.MethodPost, "/t", strings.NewReader(url.Values{"grant_type": {"authorization_code"}, "code": {code2}, "redirect_uri": {o.RedirectURL}, "client_id": {"c"}}.Encode()))
		tr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		o.TokenHandler().ServeHTTP(rec, tr)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"expires_in":60`) {
			t.Fatalf("custom ttl = %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("FinalizeBadCheckInURL", func(t *testing.T) {
		p := &enroll.Profile{ServerURL: "https://ok/", CheckInURL: "::bad"}
		if err := accountdriven.Finalize(p, accountdriven.VersionBYOD, alice, "tok"); err == nil {
			t.Fatal("bad CheckInURL accepted")
		}
	})
}
