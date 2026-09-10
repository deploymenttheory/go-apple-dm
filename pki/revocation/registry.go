package revocation

import (
	"bytes"
	"context"
	"crypto"
	"crypto/x509"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/state"
)

var (
	ErrUnknown = errors.New("revocation: unknown certificate or issuer")
	ErrRevoked = errors.New("revocation: certificate revoked")
	ErrExpired = errors.New("revocation: certificate outside validity")
	ErrInvalid = errors.New("revocation: invalid certificate, reason or configuration")
)

// Status distinguishes a registered issuance from revocation and an unknown serial.
type Status string

const (
	Issued  Status = "issued"
	Revoked Status = "revoked"
	Unknown Status = "unknown"
)

// Provenance records authorization context at issuance; never include credentials.
type Provenance struct {
	// Device identifiers are authorization evidence, never issuance credentials.
	UDID, Serial        string
	EnrollmentID        string
	Source              string
	AccountID           string
	Identifiers         []string
	EnrollmentReference string
}

type provenanceKey struct{}

// WithProvenance carries issuance context to a CA depot.
func WithProvenance(ctx context.Context, p Provenance) context.Context {
	return context.WithValue(ctx, provenanceKey{}, p)
}

// ProvenanceFromContext returns issuance context or its zero value.
func ProvenanceFromContext(ctx context.Context) Provenance {
	p, _ := ctx.Value(provenanceKey{}).(Provenance)
	return p
}

// Certificate is the durable record of an issuance. DER permits later status
// publication and import without relying on a live certificate pin.
type Certificate struct {
	Issuer              string
	Serial              string
	Fingerprint         string
	DER                 []byte
	NotBefore, NotAfter time.Time
	Status              Status
	IssuedAt            time.Time
	RevokedAt           time.Time
	Reason              int
	Provenance          Provenance
}

// Issuer includes explicit publication lifetimes. Keep retired issuers configured
// while their certificates need status responses. Keys must match Certificate.
type Issuer struct {
	Certificate                 *x509.Certificate
	Signer                      crypto.Signer
	CRLTTL, CRLRefresh, OCSPTTL time.Duration
}

// Registry is immutable configuration over shared state.
type Registry struct {
	Store   state.Store
	issuers map[string]Issuer
	Now     func() time.Time
}

// New validates issuer signing authority and publication lifetimes.
func New(store state.Store, issuers ...Issuer) (*Registry, error) {
	if store == nil || len(issuers) == 0 {
		return nil, ErrInvalid
	}
	r := &Registry{Store: store, issuers: map[string]Issuer{}}
	for _, i := range issuers {
		if i.Certificate == nil || i.Signer == nil || !i.Certificate.IsCA || i.Certificate.KeyUsage&x509.KeyUsageCRLSign == 0 || len(i.Certificate.SubjectKeyId) == 0 || i.CRLTTL <= 0 || i.CRLRefresh <= 0 || i.CRLRefresh >= i.CRLTTL || i.OCSPTTL <= 0 {
			return nil, ErrInvalid
		}
		a, err := x509.MarshalPKIXPublicKey(i.Signer.Public())
		if err != nil {
			return nil, err
		}
		b, err := x509.MarshalPKIXPublicKey(i.Certificate.PublicKey)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(a, b) {
			return nil, ErrInvalid
		}
		id := cms.Fingerprint(i.Certificate)
		if _, ok := r.issuers[id]; ok {
			return nil, ErrInvalid
		}
		r.issuers[id] = i
	}
	return r, nil
}
func (r *Registry) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}
func issuerKey(id string) string             { return "pki/issuer/" + id }
func certificatePrefix(issuer string) string { return "pki/cert/" + issuer + "/" }
func certKey(issuer, serial string) string   { return certificatePrefix(issuer) + serial }
func fingerprintKey(hash string) string      { return "pki/fingerprint/" + hash }
func crlKey(issuer string) string            { return "pki/crl/" + issuer }

func putJSON(ctx context.Context, tx state.Tx, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return tx.Put(ctx, state.Record{Key: key, Value: b})
}
func readCertificate(ctx context.Context, st state.Reader, key string) (Certificate, error) {
	r, err := st.Get(ctx, key)
	if errors.Is(err, state.ErrNotFound) {
		return Certificate{Status: Unknown}, ErrUnknown
	}
	if err != nil {
		return Certificate{}, err
	}
	var c Certificate
	err = json.Unmarshal(r.Value, &c)
	return c, err
}

// Register records a certificate before the issuer returns it. Re-importing the
// same DER is idempotent and cannot clear revocation or change its provenance.
func (r *Registry) Register(ctx context.Context, issuer string, cert *x509.Certificate, p Provenance) error {
	i, ok := r.issuers[issuer]
	if !ok {
		return ErrUnknown
	}
	if cert == nil || cert.SerialNumber == nil || cert.SerialNumber.Sign() <= 0 || len(cert.SerialNumber.Bytes()) > 20 || cert.IsCA {
		return ErrInvalid
	}
	if err := cert.CheckSignatureFrom(i.Certificate); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	serial := cert.SerialNumber.Text(16)
	hash := cms.Fingerprint(cert)
	key := certKey(issuer, serial)
	return r.Store.Update(ctx, []string{issuerKey(issuer), key, fingerprintKey(hash)}, func(tx state.Tx) error {
		old, err := readCertificate(ctx, tx, key)
		if err == nil {
			if old.Fingerprint != hash {
				return ErrInvalid
			}
			return nil
		}
		if !errors.Is(err, ErrUnknown) {
			return err
		}
		if prior, err := tx.Get(ctx, fingerprintKey(hash)); err == nil && string(prior.Value) != key {
			return ErrInvalid
		} else if err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
		c := Certificate{Issuer: issuer, Serial: serial, Fingerprint: hash, DER: cert.Raw, NotBefore: cert.NotBefore, NotAfter: cert.NotAfter, Status: Issued, IssuedAt: tx.Now(), Provenance: p}
		if err := putJSON(ctx, tx, key, c); err != nil {
			return err
		}
		return tx.Put(ctx, state.Record{Key: fingerprintKey(hash), Value: []byte(key)})
	})
}

// Lookup retrieves a registered certificate by issuer and serial.
func (r *Registry) Lookup(ctx context.Context, issuer string, serial *big.Int) (Certificate, error) {
	if _, ok := r.issuers[issuer]; !ok {
		return Certificate{Status: Unknown}, ErrUnknown
	}
	if serial == nil || serial.Sign() <= 0 || len(serial.Bytes()) > 20 {
		return Certificate{Status: Unknown}, ErrUnknown
	}
	return readCertificate(ctx, r.Store, certKey(issuer, serial.Text(16)))
}

// ByCertificate looks up exact DER, never treating a different certificate with
// the same subject or serial as the registered identity.
func (r *Registry) ByCertificate(ctx context.Context, cert *x509.Certificate) (Certificate, error) {
	if cert == nil {
		return Certificate{Status: Unknown}, ErrUnknown
	}
	v, err := r.Store.Get(ctx, fingerprintKey(cms.Fingerprint(cert)))
	if errors.Is(err, state.ErrNotFound) {
		return Certificate{Status: Unknown}, ErrUnknown
	}
	if err != nil {
		return Certificate{}, err
	}
	c, err := readCertificate(ctx, r.Store, string(v.Value))
	if err == nil && !bytes.Equal(c.DER, cert.Raw) {
		return Certificate{}, ErrInvalid
	}
	return c, err
}

// Check rejects unknown, revoked and expired certificates. It is independent of
// pinning and must run before any mutation, command delivery or credential issuance.
func (r *Registry) Check(ctx context.Context, cert *x509.Certificate) error {
	c, err := r.ByCertificate(ctx, cert)
	if err != nil {
		return err
	}
	if c.Status == Revoked {
		return ErrRevoked
	}
	if c.Status != Issued {
		return ErrUnknown
	}
	now := r.now()
	if now.Before(c.NotBefore) || !now.Before(c.NotAfter) {
		return ErrExpired
	}
	return nil
}

// ValidReason accepts irreversible RFC 5280 reasons. certificateHold and
// removeFromCRL are deliberately unsupported because this registry has no unhold.
func ValidReason(reason int) bool {
	switch reason {
	case 0, 1, 2, 3, 4, 5, 9, 10:
		return true
	}
	return false
}

// Revoke atomically marks a known certificate and invalidates the cached CRL.
// Repeated revocation returns ErrRevoked and preserves the first timestamp/reason.
func (r *Registry) Revoke(ctx context.Context, issuer string, serial *big.Int, reason int) error {
	if !ValidReason(reason) {
		return ErrInvalid
	}
	if _, err := r.Lookup(ctx, issuer, serial); err != nil {
		return err
	}
	key := certKey(issuer, serial.Text(16))
	return r.Store.Update(ctx, []string{issuerKey(issuer), key}, func(tx state.Tx) error {
		c, err := readCertificate(ctx, tx, key)
		if err != nil {
			return err
		}
		if c.Status == Revoked {
			return ErrRevoked
		}
		c.Status = Revoked
		c.RevokedAt = tx.Now()
		c.Reason = reason
		if err := putJSON(ctx, tx, key, c); err != nil {
			return err
		}
		crl, err := readPublication(ctx, tx, issuer)
		if err != nil {
			return err
		}
		crl.Dirty = true
		return putJSON(ctx, tx, crlKey(issuer), crl)
	})
}
