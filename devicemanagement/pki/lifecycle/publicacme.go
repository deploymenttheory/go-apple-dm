package lifecycle

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/crypto/acme"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

const ProductionDirectory = "https://acme-v02.api.letsencrypt.org/directory"

// PublicACMEOptions configures server HTTPS issuance, independently of the
// device identity ACME server. HTTP-01 is the only enabled challenge type.
type PublicACMEOptions struct {
	Directory   string `json:"directory"`
	Contact     string `json:"contact"`
	AcceptTerms bool   `json:"acceptTerms"`
}

type publicACMERecord struct {
	PublicACMEOptions
	AccountKey  []byte    `json:"AccountKey"`
	AccountURL  string    `json:"AccountURL"`
	OrderURL    string    `json:"OrderURL"`
	Revision    string    `json:"Revision"`
	Lease       string    `json:"Lease"`
	LeaseUntil  time.Time `json:"LeaseUntil"`
	NextAttempt time.Time `json:"NextAttempt"`
	Failures    int       `json:"Failures"`
}

// PublicACMEStatus contains no account or certificate private keys.
type PublicACMEStatus struct {
	PublicACMEOptions
	Revision    string    `json:"revision,omitempty"`
	OrderURL    string    `json:"orderUrl,omitempty"`
	NextAttempt time.Time `json:"nextAttempt,omitempty"`
	Failures    int       `json:"failures"`
}

// acmeKey constructs the namespaced key for ACME state.
func acmeKey(id string) string { return "pki/lifecycle/public-acme/" + id }

// readACME loads and decodes the persistent public-ACME account and order state.
func readACME(ctx context.Context, s state.Reader, id string) (publicACMERecord, error) {
	var v publicACMERecord
	r, err := s.Get(ctx, acmeKey(id))
	if err != nil {
		return v, err
	}
	err = json.Unmarshal(r.Value, &v)
	return v, err
}

// writeACME encodes public-ACME state, including private account material, into the
// supplied transaction.
func writeACME(ctx context.Context, tx state.Tx, id string, v publicACMERecord) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return tx.Put(ctx, state.Record{Key: acmeKey(id), Value: b})
}

// ConfigurePublicACME stores the account key before contacting the CA, so retries
// can recover an account registered just before an interrupted response.
func (m *Manager) ConfigurePublicACME(ctx context.Context, id string, o PublicACMEOptions) error {
	r, err := read(ctx, m.Store, id)
	if err != nil {
		return err
	}
	if r.Kind != HTTPS {
		return ErrInvalid
	}
	if o.Directory == "" {
		o.Directory = ProductionDirectory
	}
	u, err := url.Parse(o.Directory)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return ErrInvalid
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return fmt.Errorf("%w: public ACME directory requires HTTPS", ErrInvalid)
		}
	}
	if !o.AcceptTerms || o.Contact == "" {
		return fmt.Errorf("%w: contact and explicit ACME terms acceptance required", ErrInvalid)
	}
	for _, host := range r.DNSNames {
		if strings.Contains(host, "*") || net.ParseIP(host) != nil {
			return fmt.Errorf("%w: public HTTPS issuance requires explicit DNS hostnames", ErrInvalid)
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	return m.Store.Update(ctx, []string{acmeKey(id)}, func(tx state.Tx) error {
		v, err := readACME(ctx, tx, id)
		if err == nil {
			if v.PublicACMEOptions != o {
				return fmt.Errorf("%w: ACME account settings already configured", ErrConflict)
			}
			return nil
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		return writeACME(ctx, tx, id, publicACMERecord{PublicACMEOptions: o, AccountKey: der})
	})
}

// PublicACMEStatus returns persisted HTTPS issuance settings and retry status without
// account keys. It returns ErrNotFound when ACME has not been configured for the identity.
func (m *Manager) PublicACMEStatus(ctx context.Context, id string) (PublicACMEStatus, error) {
	v, err := readACME(ctx, m.Store, id)
	return PublicACMEStatus{PublicACMEOptions: v.PublicACMEOptions, Revision: v.Revision, OrderURL: v.OrderURL, NextAttempt: v.NextAttempt, Failures: v.Failures}, err
}

// RunPublicACME advances one persistent order and activates its certificate.
// httpClient nil uses verified system HTTPS; tests can supply a local CA client.
// A five-minute store lease fences writes and bounds each network attempt.
func (m *Manager) RunPublicACME(ctx context.Context, id string, httpClient *http.Client) (Identity, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	r, err := read(ctx, m.Store, id)
	if err != nil {
		return Identity{}, err
	}
	if r.Kind != HTTPS {
		return Identity{}, ErrInvalid
	}
	if r.Pending == "" {
		return m.Get(ctx, id)
	}
	leaseBytes := make([]byte, 16)
	if _, err = rand.Read(leaseBytes); err != nil {
		return Identity{}, err
	}
	lease := hex.EncodeToString(leaseBytes)
	var v publicACMERecord
	err = m.Store.Update(ctx, []string{acmeKey(id)}, func(tx state.Tx) error {
		var err error
		v, err = readACME(ctx, tx, id)
		if err != nil {
			return err
		}
		if v.Lease != "" && tx.Now().Before(v.LeaseUntil) {
			return fmt.Errorf("%w: HTTPS issuance already running", ErrConflict)
		}
		if tx.Now().Before(v.NextAttempt) {
			return fmt.Errorf("%w: HTTPS issuance retry is scheduled", ErrConflict)
		}
		if v.Revision != r.Pending {
			v.Revision = r.Pending
			v.OrderURL = ""
			v.Failures = 0
		}
		v.Lease = lease
		v.LeaseUntil = tx.Now().Add(5 * time.Minute)
		return writeACME(ctx, tx, id, v)
	})
	if err != nil {
		return Identity{}, err
	}
	persist := func() error {
		return m.Store.Update(ctx, []string{acmeKey(id)}, func(tx state.Tx) error {
			current, err := readACME(ctx, tx, id)
			if err != nil {
				return err
			}
			if current.Lease != lease || !tx.Now().Before(current.LeaseUntil) {
				return ErrConflict
			}
			return writeACME(ctx, tx, id, v)
		})
	}
	finish := func(failed bool) {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = m.Store.Update(cleanup, []string{acmeKey(id)}, func(tx state.Tx) error {
			current, err := readACME(cleanup, tx, id)
			if err != nil {
				return err
			}
			if current.Lease != lease {
				return nil
			}
			current.Lease = ""
			current.LeaseUntil = time.Time{}
			if failed {
				current.Failures++
				delay := time.Minute * time.Duration(1<<min(current.Failures, 10))
				current.NextAttempt = tx.Now().Add(delay)
			} else {
				current.Failures = 0
				current.NextAttempt = time.Time{}
			}
			return writeACME(cleanup, tx, id, current)
		})
	}
	succeeded := false
	defer func() { finish(!succeeded) }()
	private, err := x509.ParsePKCS8PrivateKey(v.AccountKey)
	if err != nil {
		return Identity{}, err
	}
	signer, ok := private.(crypto.Signer)
	if !ok {
		return Identity{}, ErrInvalid
	}
	client := &acme.Client{Key: signer, KID: acme.KeyID(v.AccountURL), DirectoryURL: v.Directory, HTTPClient: httpClient}
	if v.AccountURL == "" {
		contact := v.Contact
		if !strings.HasPrefix(contact, "mailto:") {
			contact = "mailto:" + contact
		}
		account, err := client.Register(ctx, &acme.Account{Contact: []string{contact}}, func(string) bool { return v.AcceptTerms })
		if err != nil && !errors.Is(err, acme.ErrAccountAlreadyExists) {
			return Identity{}, err
		}
		v.AccountURL = string(client.KID)
		if account != nil {
			v.AccountURL = account.URI
		}
		client.KID = acme.KeyID(v.AccountURL)
		if err = persist(); err != nil {
			return Identity{}, err
		}
	}
	if v.OrderURL == "" {
		var identifiers []acme.AuthzID
		for _, host := range r.DNSNames {
			identifiers = append(identifiers, acme.AuthzID{Type: "dns", Value: host})
		}
		order, err := client.AuthorizeOrder(ctx, identifiers)
		if err != nil {
			return Identity{}, err
		}
		v.OrderURL = order.URI
		if err = persist(); err != nil {
			return Identity{}, err
		}
	}
	order, err := client.GetOrder(ctx, v.OrderURL)
	if err != nil {
		return Identity{}, err
	}
	if order.Status == acme.StatusInvalid {
		v.OrderURL = ""
		if err = persist(); err != nil {
			return Identity{}, err
		}
		return Identity{}, fmt.Errorf("lifecycle: ACME order invalid; a new order will be attempted after backoff")
	}
	for _, authURL := range order.AuthzURLs {
		auth, err := client.GetAuthorization(ctx, authURL)
		if err != nil {
			return Identity{}, err
		}
		if auth.Status == acme.StatusValid {
			continue
		}
		var challenge *acme.Challenge
		for _, c := range auth.Challenges {
			if c.Type == "http-01" {
				challenge = c
				break
			}
		}
		if challenge == nil {
			return Identity{}, fmt.Errorf("lifecycle: CA did not offer HTTP-01")
		}
		response, err := client.HTTP01ChallengeResponse(challenge.Token)
		if err != nil {
			return Identity{}, err
		}
		challengeKey := "pki/lifecycle/http01/" + fingerprint([]byte(strings.ToLower(auth.Identifier.Value)+"/"+challenge.Token))
		err = m.Store.Update(ctx, []string{challengeKey}, func(tx state.Tx) error {
			return tx.Put(ctx, state.Record{Key: challengeKey, Value: []byte(response), ExpiresAt: tx.Now().Add(10 * time.Minute)})
		})
		if err != nil {
			return Identity{}, err
		}
		if challenge.Status != acme.StatusProcessing {
			if _, err = client.Accept(ctx, challenge); err != nil {
				return Identity{}, err
			}
		}
	}
	order, err = client.WaitOrder(ctx, v.OrderURL)
	if err != nil {
		return Identity{}, err
	}
	var chain [][]byte
	if order.Status == acme.StatusValid {
		chain, err = client.FetchCert(ctx, order.CertURL, true)
	} else {
		material, e := m.LoadMaterial(ctx, id, v.Revision)
		if e != nil {
			return Identity{}, e
		}
		block, _ := pem.Decode(material.CSR)
		if block == nil {
			return Identity{}, ErrInvalid
		}
		chain, _, err = client.CreateOrderCert(ctx, order.FinalizeURL, block.Bytes, true)
	}
	if err != nil {
		return Identity{}, err
	}
	var cert []byte
	for _, der := range chain {
		cert = append(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	if err = persist(); err != nil {
		return Identity{}, err
	}
	if _, err = m.Import(ctx, id, v.Revision, cert); err != nil {
		return Identity{}, err
	}
	item, err := m.activate(ctx, id, v.Revision, []string{acmeKey(id)}, func(tx state.Tx) error {
		current, err := readACME(ctx, tx, id)
		if err != nil {
			return err
		}
		if current.Lease != lease || current.Revision != v.Revision || !tx.Now().Before(current.LeaseUntil) {
			return ErrConflict
		}
		return nil
	})
	succeeded = err == nil
	return item, err
}

// HTTP01Handler serves only currently valid challenge responses for the exact
// hostname and token. Any replica sharing the repository can answer a challenge.
func (m *Manager) HTTP01Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const path = "/.well-known/acme-challenge/"
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, path) {
			http.NotFound(w, r)
			return
		}
		token := strings.TrimPrefix(r.URL.Path, path)
		if token == "" || strings.Contains(token, "/") {
			http.NotFound(w, r)
			return
		}
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		k := "pki/lifecycle/http01/" + fingerprint([]byte(strings.ToLower(host)+"/"+token))
		var response []byte
		err := m.Store.Update(r.Context(), []string{k}, func(tx state.Tx) error {
			rec, err := tx.Get(r.Context(), k)
			if err != nil {
				return err
			}
			if !tx.Now().Before(rec.ExpiresAt) {
				return ErrNotFound
			}
			response = rec.Value
			return nil
		})
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(response)
	})
}
