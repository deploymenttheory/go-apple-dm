package accountdriven_test

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/dmhook"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

var errStorage = errors.New("injected storage failure")

type faultStore struct {
	state.Store
	op, prefix string
	direct     bool
}

func (s faultStore) Get(ctx context.Context, key string) (state.Record, error) {
	if s.direct && strings.HasPrefix(key, s.prefix) {
		return state.Record{}, errStorage
	}
	return s.Store.Get(ctx, key)
}
func (s faultStore) Update(ctx context.Context, keys []string, fn func(state.Tx) error) error {
	return s.Store.Update(ctx, keys, func(tx state.Tx) error { return fn(faultTx{Tx: tx, op: s.op, prefix: s.prefix}) })
}

type faultTx struct {
	state.Tx
	op, prefix string
}

func (tx faultTx) Get(ctx context.Context, key string) (state.Record, error) {
	if tx.op == "get" && strings.HasPrefix(key, tx.prefix) {
		return state.Record{}, errStorage
	}
	return tx.Tx.Get(ctx, key)
}
func (tx faultTx) Put(ctx context.Context, r state.Record) error {
	if tx.op == "put" && strings.HasPrefix(r.Key, tx.prefix) {
		return errStorage
	}
	return tx.Tx.Put(ctx, r)
}
func (tx faultTx) List(ctx context.Context, prefix, after string, n int) ([]state.Record, error) {
	if tx.op == "list" {
		return nil, errStorage
	}
	return tx.Tx.List(ctx, prefix, after, n)
}

func (tx faultTx) Delete(ctx context.Context, key string) error {
	if tx.op == "delete" && strings.HasPrefix(key, tx.prefix) {
		return errStorage
	}
	return tx.Tx.Delete(ctx, key)
}

func TestRefreshTransactionFailurePreservesBothCredentials(t *testing.T) {
	for _, tc := range []struct{ op, prefix string }{
		{"get", "account/token/refresh"}, {"put", "account/token/refresh"},
		{"delete", "account/token/access"}, {"put", "account/token/replacement"},
	} {
		t.Run(tc.op+tc.prefix, func(t *testing.T) {
			ctx := t.Context()
			st := state.NewMemory()
			ts := &accountdriven.StateTokenStore{Backend: st}
			now := time.Now()
			refresh := accountdriven.Record{Kind: accountdriven.KindRefresh, ExpiresAt: now.Add(time.Hour), Meta: map[string]string{"access_hash": "access"}}
			access := accountdriven.Record{Kind: accountdriven.KindAccess, ExpiresAt: now.Add(time.Hour)}
			if err := ts.Put(ctx, "refresh", refresh); err != nil {
				t.Fatal(err)
			}
			if err := ts.Put(ctx, "access", access); err != nil {
				t.Fatal(err)
			}
			before, _ := st.List(ctx, "account/", "", 100)
			ts.Backend = faultStore{Store: st, op: tc.op, prefix: tc.prefix}
			err := ts.Exchange(ctx, "refresh", now, func(accountdriven.Record) error { return nil }, map[string]accountdriven.Record{"replacement": access})
			if !errors.Is(err, errStorage) {
				t.Fatal(err)
			}
			after, _ := st.List(ctx, "account/", "", 100)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("partially rotated credentials")
			}
			ts.Backend = st
			if err := ts.Exchange(ctx, "refresh", now, func(accountdriven.Record) error { return nil }, nil); err != nil {
				t.Fatal("retry failed", err)
			}
		})
	}
	ctx := t.Context()
	st := state.NewMemory()
	ts := &accountdriven.StateTokenStore{Backend: st}
	if err := ts.Put(ctx, "expired", accountdriven.Record{ExpiresAt: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := ts.MarkUsed(ctx, "expired", time.Now()); !errors.Is(err, accountdriven.ErrTokenExpired) {
		t.Fatal(err)
	}
	ts.Backend = faultStore{Store: st, direct: true, prefix: "account/"}
	if _, err := ts.Get(ctx, "expired"); !errors.Is(err, errStorage) {
		t.Fatal(err)
	}
	if (*accountdriven.Tokens)(nil).AssociationStore() != nil {
		t.Fatal("nil tokens have associations")
	}
	if (&accountdriven.Tokens{Store: &failingStore{TokenStore: accountdriven.NewMemStore()}}).AssociationStore() != nil {
		t.Fatal("custom backend inferred")
	}
}

func TestAssociationFailuresAndExpiry(t *testing.T) {
	ctx := t.Context()
	ca, err := testpki.NewCA("account failure")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ op, prefix string }{
		{"get", "account/enrollment/"}, {"put", "account/enrollment/"},
		{"get", "account/certificate/"}, {"put", "account/certificate/"},
	} {
		t.Run(tc.op+tc.prefix, func(t *testing.T) {
			st := state.NewMemory()
			a := &accountdriven.Associations{Store: st}
			record, err := a.Create(ctx, alice, accountdriven.VersionBYOD, "iPhone17,2")
			if err != nil {
				t.Fatal(err)
			}
			cert, err := ca.Issue(accountdriven.CertificateSubjectPrefix+record.Reference, time.Now().Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			before, _ := st.List(ctx, "account/", "", 100)
			a.Store = faultStore{Store: st, op: tc.op, prefix: tc.prefix}
			if err := a.RegisterCertificate(ctx, cert.Cert); !errors.Is(err, errStorage) {
				t.Fatal(err)
			}
			after, _ := st.List(ctx, "account/", "", 100)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("partial certificate registration")
			}
		})
	}
	st := state.NewMemory()
	now := time.Now()
	st.Now = func() time.Time { return now }
	a := &accountdriven.Associations{Store: st}
	record, err := a.Create(ctx, alice, accountdriven.VersionBYOD, "iPhone17,2")
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := ca.Issue(accountdriven.CertificateSubjectPrefix+record.Reference, now.Add(-time.Minute))
	now = now.Add(25 * time.Hour)
	if err := a.RegisterCertificate(ctx, cert.Cert); !errors.Is(err, accountdriven.ErrAssociation) {
		t.Fatal("expired pending profile registered", err)
	}
	for _, id := range []mdm.EnrollmentID{{Channel: mdm.ChannelUser, ID: "user", ParentID: "dev"}, {Channel: mdm.ChannelDevice, ID: "dev"}} {
		if err := a.Bind(ctx, record.Reference, id, false); !errors.Is(err, accountdriven.ErrAssociation) {
			t.Fatal(id, err)
		}
	}
	if err := a.Bind(ctx, "missing", mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "dev"}, false); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
	if _, err := a.IssueSCEPChallenge(ctx, "missing", time.Hour); !errors.Is(err, state.ErrNotFound) {
		t.Fatal(err)
	}
	for _, csr := range []*x509.CertificateRequest{{}, {Subject: cert.Cert.Subject}} {
		if err := a.VerifySCEPChallenge(ctx, "password", csr); !errors.Is(err, accountdriven.ErrAssociation) {
			t.Fatal(err)
		}
	}
}

type challengeAuth struct {
	challenge accountdriven.Challenge
	err       error
}

func (a challengeAuth) Challenge(context.Context, *http.Request, *accountdriven.DeviceInfo) (accountdriven.Challenge, error) {
	return a.challenge, a.err
}

func TestExternalVerifierAndHookFailures(t *testing.T) {
	ctx := t.Context()
	st := state.NewMemory()
	a := &accountdriven.Associations{Store: st}
	record, err := a.Create(ctx, alice, accountdriven.VersionBYOD, "iPhone17,2")
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := testpki.NewCA("external verifier")
	cert, _ := ca.Issue(accountdriven.CertificateSubjectPrefix+record.Reference, time.Now().Add(-time.Minute))
	if err := a.RegisterCertificate(ctx, cert.Cert); err != nil {
		t.Fatal(err)
	}
	request := &mdm.Request{Certificate: cert.Cert, ID: mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: "device"}}
	call := &dmhook.Call{Op: "checkin:Authenticate", Request: request}
	h := &accountdriven.CheckinHook{Associations: a}
	if _, err := h.Before(ctx, call); !errors.Is(err, accountdriven.ErrConfig) {
		t.Fatal("missing verifier", err)
	}
	if _, err := (&accountdriven.CheckinHook{}).Before(ctx, call); !errors.Is(err, accountdriven.ErrConfig) {
		t.Fatal("missing associations", err)
	}
	h.Verifier = accountdriven.VerifyFunc(func(context.Context, secrets.Secret) (accountdriven.Identity, error) { return alice, errStorage })
	if _, err := h.Before(ctx, call); !errors.Is(err, errStorage) {
		t.Fatal(err)
	}
	h.Verifier = accountdriven.VerifyFunc(func(context.Context, secrets.Secret) (accountdriven.Identity, error) {
		return alice, accountdriven.ErrTokenExpired
	})
	if _, err := h.Before(ctx, call); !errors.Is(err, accountdriven.ErrEnrollmentToken) {
		t.Fatal(err)
	}
	h.Auth = challengeAuth{err: errStorage}
	if _, err := h.Before(ctx, call); !errors.Is(err, errStorage) {
		t.Fatal(err)
	}
	h.Auth = challengeAuth{}
	if _, err := h.Before(ctx, call); err == nil {
		t.Fatal("invalid challenge accepted")
	}
	wrong := alice
	wrong.Issuer = "other-issuer"
	h.Verifier = accountdriven.VerifyFunc(func(context.Context, secrets.Secret) (accountdriven.Identity, error) { return wrong, nil })
	if _, err := h.Before(ctx, call); !errors.Is(err, accountdriven.ErrAssociation) {
		t.Fatal("different issuer accepted", err)
	}
	h.Verifier = accountdriven.VerifyFunc(func(context.Context, secrets.Secret) (accountdriven.Identity, error) { return alice, nil })
	for _, id := range []mdm.EnrollmentID{{}, {Channel: mdm.ChannelDevice, ID: "device"}} {
		request.ID = id
		if _, err := h.Before(ctx, call); err == nil {
			t.Fatal("invalid identity accepted", id)
		}
	}
	request.ID = mdm.EnrollmentID{Channel: mdm.ChannelUserEnrollmentDevice, ID: "device"}
	call.Op = "connect"
	if _, err := h.Before(ctx, call); !errors.Is(err, accountdriven.ErrAssociation) {
		t.Fatal("unbound connect accepted", err)
	}
	call.Op = "checkin:Authenticate"
	a.Store = faultStore{Store: st, op: "put", prefix: "account/enrollment/"}
	if _, err := h.Before(ctx, call); !errors.Is(err, errStorage) {
		t.Fatal("reservation failure hidden", err)
	}
	a.Store = faultStore{Store: st, direct: true, prefix: "account/certificate/"}
	if _, err := h.Before(ctx, call); !errors.Is(err, errStorage) {
		t.Fatal("lookup failure hidden", err)
	}
}

func TestProfileFailuresAreReported(t *testing.T) {
	ctx := t.Context()
	ca, _ := testpki.NewCA("profile failures")
	cert, _ := ca.Issue("profile signer", time.Now().Add(-time.Minute))
	tk := &accountdriven.Tokens{Store: accountdriven.NewMemStore()}
	token, err := tk.Issue(ctx, accountdriven.KindAccess, alice, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := accountdriven.Config{Version: accountdriven.VersionBYOD, Parse: parseBody, Auth: challengeAuth{}, Tokens: tk, Profile: baseProfile, SignCert: cert.Cert, SignKey: cert.Key}
	for _, tc := range []struct {
		name   string
		edit   func(*accountdriven.Config)
		bearer bool
	}{
		{"verifier outage", func(c *accountdriven.Config) {
			c.Verifier = accountdriven.VerifyFunc(func(context.Context, secrets.Secret) (accountdriven.Identity, error) { return alice, errStorage })
		}, true},
		{"challenge outage", func(c *accountdriven.Config) { c.Auth = challengeAuth{err: errStorage} }, false},
		{"nil profile", func(c *accountdriven.Config) {
			c.Profile = func(context.Context, accountdriven.Identity, *accountdriven.DeviceInfo) (*enroll.Profile, error) {
				return nil, nil
			}
		}, true},
		{"bad signing key", func(c *accountdriven.Config) { c.SignKey = brokenProfileSigner{Signer: cert.Key} }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.edit(&cfg)
			h, err := accountdriven.New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			r := httptest.NewRequest("POST", "/enroll", strings.NewReader(body))
			if tc.bearer {
				r.Header.Set("Authorization", "Bearer "+token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 500 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	base.Tokens = nil
	base.Verifier = accountdriven.VerifyFunc(func(context.Context, secrets.Secret) (accountdriven.Identity, error) { return alice, nil })
	if _, err := accountdriven.New(base); !errors.Is(err, accountdriven.ErrConfig) {
		t.Fatal("missing association backend", err)
	}
	e := &accountdriven.HTTPError{Status: 403, Err: errStorage}
	if !errors.Is(e, errStorage) || !strings.Contains(e.Error(), "403") {
		t.Fatal(e)
	}
}

type brokenProfileSigner struct{ crypto.Signer }

func (brokenProfileSigner) Sign(io.Reader, []byte, crypto.SignerOpts) ([]byte, error) {
	return nil, errStorage
}
