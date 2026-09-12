package revocation_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/x509"
	json "encoding/json/v2"
	"errors"
	"io"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
	"golang.org/x/crypto/ocsp"
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

func TestRegistryFailuresAreAtomic(t *testing.T) {
	for _, tc := range []struct{ action, op, prefix string }{
		{"register", "get", "pki/cert/"}, {"register", "get", "pki/fingerprint/"},
		{"register", "put", "pki/cert/"}, {"register", "put", "pki/fingerprint/"},
		{"revoke", "get", "pki/cert/"}, {"revoke", "put", "pki/cert/"},
		{"revoke", "get", "pki/crl/"}, {"revoke", "put", "pki/crl/"},
		{"crl", "get", "pki/crl/"}, {"crl", "list", ""}, {"crl", "put", "pki/crl/"},
	} {
		t.Run(tc.action+tc.op+tc.prefix, func(t *testing.T) {
			f := setup(t)
			ctx := t.Context()
			if tc.action != "register" {
				if err := f.reg.Register(ctx, f.id, f.leaf, revocation.Provenance{}); err != nil {
					t.Fatal(err)
				}
			}
			before, err := f.st.List(ctx, "pki/", "", 100)
			if err != nil {
				t.Fatal(err)
			}
			f.reg.Store = faultStore{Store: f.st, op: tc.op, prefix: tc.prefix}
			switch tc.action {
			case "register":
				err = f.reg.Register(ctx, f.id, f.leaf, revocation.Provenance{})
			case "revoke":
				err = f.reg.Revoke(ctx, f.id, f.leaf.SerialNumber, 1)
			case "crl":
				_, err = f.reg.CRL(ctx, f.id)
			}
			if !errors.Is(err, errStorage) {
				t.Fatal(err)
			}
			after, err := f.st.List(ctx, "pki/", "", 100)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("failed operation changed durable state", err)
			}
		})
	}
}

type failingSigner struct {
	crypto.Signer
	fail bool
}

func (s *failingSigner) Sign(random io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	if s.fail {
		return nil, errStorage
	}
	return s.Signer.Sign(random, digest, opts)
}

func TestSigningFailureDoesNotAdvanceCRLOrClaimGoodStatus(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	signer := &failingSigner{Signer: f.issuer.Signer}
	i := f.issuer
	i.Signer = signer
	r, err := revocation.New(f.st, i)
	if err != nil {
		t.Fatal(err)
	}
	r.Now = f.clock.Now
	if err := r.Register(ctx, f.id, f.leaf, revocation.Provenance{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CRL(ctx, f.id); err != nil {
		t.Fatal(err)
	}
	if err := r.Revoke(ctx, f.id, f.leaf.SerialNumber, 1); err != nil {
		t.Fatal(err)
	}
	signer.fail = true
	request, _ := ocsp.CreateRequest(f.leaf, i.Certificate, nil)
	if _, err := r.CRL(ctx, f.id); !errors.Is(err, errStorage) {
		t.Fatal(err)
	}
	if _, err := r.OCSP(ctx, f.id, request); !errors.Is(err, errStorage) {
		t.Fatal(err)
	}
	signer.fail = false
	der, err := r.CRL(ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	crl, err := x509.ParseRevocationList(der)
	if err != nil || crl.CheckSignatureFrom(i.Certificate) != nil || crl.Number.Int64() != 2 || len(crl.RevokedCertificateEntries) != 1 {
		t.Fatal(crl, err)
	}
	for _, prefix := range []string{"pki/cert/", "pki/fingerprint/"} {
		r.Store = faultStore{Store: f.st, direct: true, prefix: prefix}
		if err := r.Check(ctx, f.leaf); !errors.Is(err, errStorage) {
			t.Fatal("status check concealed outage", err)
		}
	}
	r.Store = faultStore{Store: f.st, direct: true, prefix: "pki/cert/"}
	if _, err := r.OCSP(ctx, f.id, request); !errors.Is(err, errStorage) {
		t.Fatal("OCSP concealed outage", err)
	}
	// Publications never extend beyond the issuer's own validity.
	r.Store = f.st
	f.clock.Advance(i.Certificate.NotAfter.Sub(f.clock.Now()) - time.Minute)
	der, err = r.CRL(ctx, f.id)
	if err != nil {
		t.Fatal(err)
	}
	crl, err = x509.ParseRevocationList(der)
	if err != nil || !crl.NextUpdate.Equal(i.Certificate.NotAfter) {
		t.Fatal(crl, err)
	}
	der, err = r.OCSP(ctx, f.id, request)
	if err != nil {
		t.Fatal(err)
	}
	response, err := ocsp.ParseResponse(der, i.Certificate)
	if err != nil || !response.NextUpdate.Equal(i.Certificate.NotAfter) {
		t.Fatal(response, err)
	}
}

func TestConflictingAndCorruptCertificateRecordsFailClosed(t *testing.T) {
	f := setup(t)
	ctx := t.Context()
	if err := f.reg.Register(ctx, f.id, f.leaf, revocation.Provenance{}); err != nil {
		t.Fatal(err)
	}
	// A different signed certificate cannot replace the same issuer/serial.
	replacement := *f.leaf
	replacement.Subject.CommonName = "different subject"
	replacement.RawSubject = nil
	der, err := x509.CreateCertificate(rand.Reader, &replacement, f.issuer.Certificate, f.csr.PublicKey, f.issuer.Signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.reg.Register(ctx, f.id, cert, revocation.Provenance{}); !errors.Is(err, revocation.ErrInvalid) {
		t.Fatal(err)
	}
	certKey := "pki/cert/" + f.id + "/" + f.leaf.SerialNumber.Text(16)
	original, err := f.st.Get(ctx, certKey)
	if err != nil {
		t.Fatal(err)
	}
	put := func(key string, value []byte) {
		t.Helper()
		if err := f.st.Update(ctx, []string{key}, func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: key, Value: value}) }); err != nil {
			t.Fatal(err)
		}
	}
	var record revocation.Certificate
	if err := json.Unmarshal(original.Value, &record); err != nil {
		t.Fatal(err)
	}
	record.DER = []byte("another certificate")
	value, _ := json.Marshal(record)
	put(certKey, value)
	if err := f.reg.Check(ctx, f.leaf); !errors.Is(err, revocation.ErrInvalid) {
		t.Fatal(err)
	}
	record.DER = f.leaf.Raw
	record.Status = revocation.Unknown
	value, _ = json.Marshal(record)
	put(certKey, value)
	if err := f.reg.Check(ctx, f.leaf); !errors.Is(err, revocation.ErrUnknown) {
		t.Fatal(err)
	}
	put(certKey, []byte("broken JSON"))
	if _, err := f.reg.CRL(ctx, f.id); err == nil {
		t.Fatal("corrupt record published")
	}
	put(certKey, original.Value)
	// An index that claims a different record never authorizes a fresh import.
	f2 := setup(t)
	key := "pki/fingerprint/" + cms.Fingerprint(f2.leaf)
	put2 := func() error {
		return f2.st.Update(ctx, []string{key}, func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: key, Value: []byte("different-record")}) })
	}
	if err := put2(); err != nil {
		t.Fatal(err)
	}
	if err := f2.reg.Register(ctx, f2.id, f2.leaf, revocation.Provenance{}); !errors.Is(err, revocation.ErrInvalid) {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	f.reg.Handler("/pki").ServeHTTP(w, httptest.NewRequest("GET", "/pki/ocsp/"+f.id+"/"+strings.Repeat("A", 8193), nil))
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
