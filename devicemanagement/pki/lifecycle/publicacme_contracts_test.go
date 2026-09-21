package lifecycle

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// acmeAuthority speaks the ACME HTTP protocol to the real client, including
// decoding the submitted CSR and validating the published HTTP-01 response.
type acmeAuthority struct {
	t                                                       *testing.T
	m                                                       *Manager
	root                                                    Material
	now                                                     *time.Time
	requests                                                map[string]int
	orderStatus, authStatus, challengeStatus, challengeType string
	existingAccount                                         bool
	certificate                                             []byte
	before                                                  func(*http.Request)
	failPath                                                string
}

// acmeFixture creates a public ACME lifecycle fixture with a trusted fake authority and pending
// HTTPS identity.
func acmeFixture(t *testing.T) (*Manager, *faultRepository, *acmeAuthority) {
	t.Helper()
	m, s, now := testManager(t)
	root := rootIdentity(t, m, "ca")
	cs, err := certificates(root.Certificate)
	requireError(t, err, nil)
	m.Trust.HTTPSRoots = x509.NewCertPool()
	m.Trust.HTTPSRoots.AddCert(cs[0])
	req := requestFor("web", HTTPS)
	req.DNSNames = []string{"mdm.example"}
	_, err = m.Begin(t.Context(), req)
	requireError(t, err, nil)
	requireError(
		t,
		m.ConfigurePublicACME(
			t.Context(),
			"web",
			PublicACMEOptions{
				Directory:   "https://ca.example/directory",
				Contact:     "operator@example.com",
				AcceptTerms: true,
			},
		),
		nil,
	)
	return m, s, &acmeAuthority{
		t:               t,
		m:               m,
		root:            root,
		now:             now,
		requests:        map[string]int{},
		orderStatus:     "ready",
		authStatus:      "pending",
		challengeStatus: "pending",
		challengeType:   "http-01",
	}
}

// RoundTrip serves the fake ACME authority's discovery, issuance, and HTTP-01 validation
// responses.
func (a *acmeAuthority) RoundTrip(r *http.Request) (*http.Response, error) {
	a.requests[r.URL.Path]++
	if a.before != nil {
		a.before(r)
	}
	status, body := http.StatusOK, "{}"
	h := make(http.Header)
	h.Set("Replay-Nonce", fmt.Sprintf("nonce-%d", a.requests[r.URL.Path]))
	h.Set("Content-Type", "application/json")
	order := func(status string) string {
		return fmt.Sprintf(
			`{"status":%q,"authorizations":["https://ca.example/auth"],"finalize":"https://ca.example/finalize","certificate":"https://ca.example/cert"}`,
			status,
		)
	}
	switch r.URL.Path {
	case "/directory":
		body = `{"newNonce":"https://ca.example/nonce","newAccount":"https://ca.example/account","newOrder":"https://ca.example/new-order","meta":{"termsOfService":"https://ca.example/terms"}}`
	case "/nonce":
	case "/account":
		status = http.StatusCreated
		if a.existingAccount {
			status = http.StatusOK
		}
		h.Set("Location", "https://ca.example/account/1")
		body = `{"status":"valid"}`
	case "/new-order":
		status = http.StatusCreated
		h.Set("Location", "https://ca.example/order")
		body = order(a.orderStatus)
	case "/order":
		h.Set("Location", "https://ca.example/order")
		body = order(a.orderStatus)
	case "/auth":
		body = fmt.Sprintf(
			`{"status":%q,"identifier":{"type":"dns","value":"mdm.example"},"challenges":[{"type":%q,"url":"https://ca.example/challenge","token":"token","status":%q}]}`,
			a.authStatus,
			a.challengeType,
			a.challengeStatus,
		)
	case "/challenge":
		w := httptest.NewRecorder()
		a.m.HTTP01Handler().
			ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://MDM.EXAMPLE:80/.well-known/acme-challenge/token", nil))
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "token.") ||
			w.Header().Get("Cache-Control") != "no-store" {
			a.t.Fatal("HTTP-01 response not published", w.Code, w.Body.String())
		}
		body = `{"type":"http-01","status":"valid","url":"https://ca.example/challenge","token":"token"}`
	case "/finalize":
		var envelope struct {
			Payload string `json:"payload"`
		}
		requireError(a.t, json.NewDecoder(r.Body).Decode(&envelope), nil)
		payload, err := base64.RawURLEncoding.DecodeString(envelope.Payload)
		requireError(a.t, err, nil)
		var request struct {
			CSR string `json:"csr"`
		}
		requireError(a.t, json.Unmarshal(payload, &request), nil)
		der, err := base64.RawURLEncoding.DecodeString(request.CSR)
		requireError(a.t, err, nil)
		csr, err := x509.ParseCertificateRequest(der)
		requireError(a.t, err, nil)
		requireError(a.t, csr.CheckSignature(), nil)
		a.certificate = append(
			issueCertificate(
				a.t,
				a.root,
				csr.PublicKey,
				&x509.Certificate{
					Subject:     csr.Subject,
					DNSNames:    csr.DNSNames,
					NotBefore:   a.now.Add(-time.Minute),
					NotAfter:    a.now.Add(90 * 24 * time.Hour),
					KeyUsage:    x509.KeyUsageDigitalSignature,
					ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
				},
			),
			a.root.Certificate...)
		h.Set("Location", "https://ca.example/order")
		body = order("valid")
	case "/cert":
		h.Set("Content-Type", "application/pem-certificate-chain")
		body = string(a.certificate)
	default:
		a.t.Fatalf("unexpected ACME request: %s %s", r.Method, r.URL)
	}
	if r.URL.Path == a.failPath {
		status, body = http.StatusForbidden, `{"type":"urn:ietf:params:acme:error:unauthorized","detail":"test authority denied request"}`
	}
	return &http.Response{
		StatusCode: status,
		Header:     h,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    r,
	}, nil
}

// TestPublicACMEFencesFailuresAndInterruptedWorkers checks public ACME fences failures and
// interrupted workers.
func TestPublicACMEFencesFailuresAndInterruptedWorkers(t *testing.T) {
	for _, mode := range []string{"unconfigured", "lease held", "invalid account key", "invalid order", "invalid order persistence", "missing challenge", "challenge storage", "wait order", "missing CSR", "material read", "certificate rejected", "lease expired", "lease replaced", "account persistence", "order persistence", "certificate persistence", "persistence read", "activation read", "activation lease"} {
		t.Run(mode, func(t *testing.T) {
			m, s, ca := acmeFixture(t)
			ctx := t.Context()
			account, err := readACME(ctx, s, "web")
			requireError(t, err, nil)
			switch mode {
			case "unconfigured":
				s.txGet = func(k string) error {
					if k == acmeKey("web") {
						return ErrNotFound
					}
					return nil
				}
			case "lease held":
				account.Lease, account.LeaseUntil = "other-worker", ca.now.Add(time.Minute)
			case "invalid account key":
				account.AccountKey = []byte("corrupt")
			case "invalid order", "invalid order persistence":
				ca.orderStatus = "invalid"
			case "missing challenge":
				ca.challengeType = "dns-01"
			case "missing CSR":
				changeRecord(t, m, "web", func(r *record) { r.Revisions[0].CSR = nil })
			case "certificate rejected":
				m.Trust.HTTPSRoots = x509.NewCertPool()
			}
			putRecord(t, s.Store, acmeKey("web"), account)
			ca.before = func(r *http.Request) {
				switch mode {
				case "invalid order persistence":
					if r.URL.Path == "/order" {
						s.put = func(string) error { return errRepository }
					}
				case "challenge storage":
					if r.URL.Path == "/auth" {
						s.put = func(k string) error {
							if strings.HasPrefix(k, "pki/lifecycle/http01/") {
								return errRepository
							}
							return nil
						}
					}
				case "wait order":
					if r.URL.Path == "/order" && ca.requests["/order"] == 2 {
						ca.failPath = "/order"
					}
				case "material read":
					if r.URL.Path == "/order" {
						s.get = func(string) error { return errRepository }
					}
				case "lease expired":
					if r.URL.Path == "/account" {
						*ca.now = ca.now.Add(6 * time.Minute)
					}
				case "lease replaced":
					if r.URL.Path == "/account" {
						current, err := readACME(ctx, s, "web")
						requireError(t, err, nil)
						current.Lease = "successor"
						putRecord(t, s.Store, acmeKey("web"), current)
					}
				case "account persistence", "order persistence", "certificate persistence":
					path := map[string]string{"account persistence": "/account", "order persistence": "/new-order", "certificate persistence": "/cert"}[mode]
					if r.URL.Path == path {
						s.put = func(k string) error {
							if k == acmeKey("web") {
								return errRepository
							}
							return nil
						}
					}
				case "persistence read":
					if r.URL.Path == "/account" {
						s.txGet = func(string) error { return errRepository }
					}
				case "activation read", "activation lease":
					if r.URL.Path == "/cert" {
						s.put = func(k string) error {
							if k == prefix+"web" {
								if mode == "activation read" {
									s.txGet = func(k string) error {
										if k == acmeKey("web") {
											return errRepository
										}
										return nil
									}
								} else {
									*ca.now = ca.now.Add(6 * time.Minute)
								}
							}
							return nil
						}
					}
				}
			}
			if _, err = m.RunPublicACME(ctx, "web", &http.Client{Transport: ca}); err == nil {
				t.Fatal("failed or stale issuance activated")
			}
			s.get, s.txGet, s.put = nil, nil, nil
			identity, err := m.Get(ctx, "web")
			requireError(t, err, nil)
			if identity.Active != "" || identity.Pending != "1" {
				t.Fatal("failure lost pending revision", identity)
			}
			after, err := readACME(ctx, s, "web")
			requireError(t, err, nil)
			if !bytes.Equal(account.AccountKey, after.AccountKey) {
				t.Fatal("failure replaced account key")
			}
			if mode == "lease replaced" && after.Lease != "successor" {
				t.Fatal("stale worker cleared successor lease")
			}
			if mode == "invalid order" && after.OrderURL != "" {
				t.Fatal("invalid order retained for retry")
			}
		})
	}
}

// TestPublicACMECompletesAndResumesPersistedOrders checks that public ACME completes and resumes
// persisted orders.
func TestPublicACMECompletesAndResumesPersistedOrders(t *testing.T) {
	for _, mode := range []string{"new", "existing account", "processing challenge", "authorized", "issued certificate"} {
		t.Run(mode, func(t *testing.T) {
			m, _, ca := acmeFixture(t)
			before, err := readACME(t.Context(), m.Store, "web")
			requireError(t, err, nil)
			switch mode {
			case "existing account":
				ca.existingAccount = true
			case "processing challenge":
				ca.challengeStatus = "processing"
			case "authorized":
				ca.authStatus = "valid"
			case "issued certificate":
				v, err := m.Get(t.Context(), "web")
				requireError(t, err, nil)
				mat, err := m.LoadMaterial(t.Context(), "web", v.Pending)
				requireError(t, err, nil)
				csrBlock, _ := pem.Decode(mat.CSR)
				csr, err := x509.ParseCertificateRequest(csrBlock.Bytes)
				requireError(t, err, nil)
				ca.certificate = append(
					issueCertificate(
						t,
						ca.root,
						csr.PublicKey,
						&x509.Certificate{
							Subject:     csr.Subject,
							DNSNames:    csr.DNSNames,
							NotBefore:   ca.now.Add(-time.Minute),
							NotAfter:    ca.now.Add(90 * 24 * time.Hour),
							KeyUsage:    x509.KeyUsageDigitalSignature,
							ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
						},
					),
					ca.root.Certificate...)
				ca.orderStatus, ca.authStatus = "valid", "valid"
				before.Revision, before.AccountURL, before.OrderURL = v.Pending, "https://ca.example/account/1", "https://ca.example/order"
				putRecord(t, m.Store, acmeKey("web"), before)
			}
			v, err := m.RunPublicACME(t.Context(), "web", &http.Client{Transport: ca})
			requireError(t, err, nil)
			if v.Active != "1" || v.Pending != "" {
				t.Fatal(v)
			}
			after, err := readACME(t.Context(), m.Store, "web")
			requireError(t, err, nil)
			if !bytes.Equal(before.AccountKey, after.AccountKey) || after.Lease != "" ||
				after.Failures != 0 ||
				!after.NextAttempt.IsZero() {
				t.Fatal("account or completion state changed incorrectly")
			}
			_, err = m.RunPublicACME(t.Context(), "web", nil)
			requireError(t, err, nil)
			if mode == "issued certificate" &&
				(ca.requests["/new-order"] != 0 || ca.requests["/finalize"] != 0 || ca.requests["/account"] != 0) {
				t.Fatal("resumed order was recreated")
			}
		})
	}
}

// TestPublicACMEFailureBackoffAndRecovery checks public ACME failure backoff and recovery.
func TestPublicACMEFailureBackoffAndRecovery(t *testing.T) {
	for _, path := range []string{"/account", "/new-order", "/order", "/auth", "/challenge", "/finalize", "/cert"} {
		t.Run(path, func(t *testing.T) {
			m, _, ca := acmeFixture(t)
			ca.failPath = path
			client := &http.Client{Transport: ca}
			if _, err := m.RunPublicACME(t.Context(), "web", client); err == nil {
				t.Fatal("authority rejection ignored")
			}
			status, err := m.PublicACMEStatus(t.Context(), "web")
			requireError(t, err, nil)
			if status.Failures != 1 || !status.NextAttempt.Equal(ca.now.Add(2*time.Minute)) {
				t.Fatal(status)
			}
			_, err = m.RunPublicACME(t.Context(), "web", client)
			requireError(t, err, ErrConflict)
			*ca.now = status.NextAttempt
			ca.failPath = ""
			_, err = m.RunPublicACME(t.Context(), "web", client)
			requireError(t, err, nil)
		})
	}
}

// TestPublicACMEConfigurationAndChallengeIsolation checks public ACME configuration and challenge
// isolation.
func TestPublicACMEConfigurationAndChallengeIsolation(t *testing.T) {
	m, s, ca := acmeFixture(t)
	ctx := t.Context()
	valid := PublicACMEOptions{
		Directory:   "https://ca.example/directory",
		Contact:     "operator@example.com",
		AcceptTerms: true,
	}
	requireError(t, m.ConfigurePublicACME(ctx, "web", valid), nil)
	other := valid
	other.Contact = "other@example.com"
	requireError(t, m.ConfigurePublicACME(ctx, "web", other), ErrConflict)
	for _, directory := range []string{"%", "https:///missing", "https://user:password@ca.example", "https://ca.example/#fragment", "http://ca.example", "ftp://127.0.0.1"} {
		options := valid
		options.Directory = directory
		requireError(t, m.ConfigurePublicACME(ctx, "web", options), ErrInvalid)
	}
	for _, options := range []PublicACMEOptions{{Contact: valid.Contact}, {AcceptTerms: true}} {
		requireError(t, m.ConfigurePublicACME(ctx, "web", options), ErrInvalid)
	}
	requireError(t, m.ConfigurePublicACME(ctx, "ca", valid), ErrInvalid)
	requireError(t, m.ConfigurePublicACME(ctx, "missing", valid), ErrNotFound)
	for i, host := range []string{"*.example", "127.0.0.1", "mdm.example", "local.example"} {
		req := requestFor(fmt.Sprintf("host-%d", i), HTTPS)
		req.DNSNames = []string{host}
		_, err := m.Begin(ctx, req)
		requireError(t, err, nil)
		options := valid
		options.Directory = ""
		if i == 3 {
			options.Directory = "http://127.0.0.1/directory"
		}
		err = m.ConfigurePublicACME(ctx, req.ID, options)
		if i < 2 {
			requireError(t, err, ErrInvalid)
		} else {
			requireError(t, err, nil)
		}
	}
	s.txGet = func(string) error { return errRepository }
	requireError(t, m.ConfigurePublicACME(ctx, "web", valid), errRepository)
	s.txGet = nil
	_, err := m.RunPublicACME(ctx, "missing", nil)
	requireError(t, err, ErrNotFound)
	_, err = m.RunPublicACME(ctx, "ca", nil)
	requireError(t, err, ErrInvalid)
	ca.authStatus = "valid"
	_, err = m.RunPublicACME(ctx, "web", &http.Client{Transport: ca})
	requireError(t, err, nil)
	// Challenge responses are bounded by host, method, path and repository time.
	k := "pki/lifecycle/http01/" + fingerprint([]byte("mdm.example/token"))
	requireError(t, s.Update(ctx, []string{k}, func(tx state.Tx) error {
		return tx.Put(
			ctx,
			state.Record{
				Key:       k,
				Value:     []byte("token.response"),
				ExpiresAt: tx.Now().Add(time.Minute),
			},
		)
	}), nil)
	for _, tc := range []struct {
		method, url string
		code        int
	}{
		{"GET", "http://mdm.example/.well-known/acme-challenge/token", 200},
		{"POST", "http://mdm.example/.well-known/acme-challenge/token", 404},
		{"GET", "http://other.example/.well-known/acme-challenge/token", 404},
		{"GET", "http://mdm.example/elsewhere", 404},
		{"GET", "http://mdm.example/.well-known/acme-challenge/", 404},
		{"GET", "http://mdm.example/.well-known/acme-challenge/token/extra", 404},
	} {
		w := httptest.NewRecorder()
		m.HTTP01Handler().ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), tc.method, tc.url, nil))
		if w.Code != tc.code {
			t.Fatal(tc, w.Code)
		}
	}
	*ca.now = ca.now.Add(time.Minute)
	w := httptest.NewRecorder()
	m.HTTP01Handler().
		ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), "GET", "http://mdm.example/.well-known/acme-challenge/token", nil))
	if w.Code != 404 {
		t.Fatal("expired challenge served")
	}
}
