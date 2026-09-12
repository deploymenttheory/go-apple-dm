package accountdriven

import (
	"context"
	"crypto/x509"
	json "encoding/json/v2"
	"errors"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/secrets"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/state"
)

// Verifier verifies access tokens, including external identity provider tokens.
// Implementations must check expiry, issuer, audience and invalidation as applicable.
type Verifier interface {
	Verify(context.Context, secrets.Secret) (Identity, error)
}

// VerifyFunc adapts a verifier function.
type VerifyFunc func(context.Context, secrets.Secret) (Identity, error)

// Verify implements Verifier.
func (f VerifyFunc) Verify(ctx context.Context, s secrets.Secret) (Identity, error) { return f(ctx, s) }

// CertificateSubjectPrefix identifies an account enrollment CSR. It conveys no
// authorization: only a CA issuance callback may register the resulting certificate.
const CertificateSubjectPrefix = "accountdriven:"

// Association ties an authenticated account to one profile issuance and its device.
// Reference is an opaque identifier, not a credential. Enrollment is reserved before
// the first Authenticate side effect; ConfirmedAt is set only after it succeeds.
type Association struct {
	Reference        string
	Identity         Identity
	Origin           string
	Product          string
	Enrollment       mdm.EnrollmentID
	CreatedAt        time.Time
	ExpiresAt        time.Time
	ConfirmedAt      time.Time
	ChallengeHash    string
	ChallengeExpires time.Time
	CSRHash          string
}

// Associations persists immutable account/profile and certificate associations.
type Associations struct{ Store state.Store }

var ErrAssociation = errors.New("accountdriven: enrollment association mismatch")

func associationKey(ref string) string  { return "account/enrollment/" + ref }
func certificateKey(hash string) string { return "account/certificate/" + hash }

func readAssociation(ctx context.Context, r state.Reader, ref string) (Association, error) {
	v, err := r.Get(ctx, associationKey(ref))
	if err != nil {
		return Association{}, err
	}
	var a Association
	err = json.Unmarshal(v.Value, &a)
	return a, err
}
func writeAssociation(ctx context.Context, tx state.Tx, a Association) error {
	b, err := json.Marshal(a)
	if err != nil {
		return err
	}
	return tx.Put(ctx, state.Record{Key: associationKey(a.Reference), Value: b, ExpiresAt: a.ExpiresAt})
}

// Create records an authenticated profile issuance. Each call creates a distinct
// enrollment so a person may enroll multiple devices without an arbitrary quota.
func (s *Associations) Create(ctx context.Context, id Identity, origin, product string) (Association, error) {
	if id.ManagedAppleAccount == "" {
		return Association{}, ErrManagedAppleAccount
	}
	if _, err := Mode(origin); err != nil {
		return Association{}, err
	}
	if product == "" {
		return Association{}, ErrAssociation
	}
	ref, err := NewToken()
	if err != nil {
		return Association{}, err
	}
	a := Association{Reference: ref, Identity: id, Origin: origin, Product: product}
	err = s.Store.Update(ctx, []string{associationKey(ref)}, func(tx state.Tx) error {
		a.CreatedAt = tx.Now()
		a.ExpiresAt = tx.Now().Add(24 * time.Hour)
		return writeAssociation(ctx, tx, a)
	})
	return a, err
}

// Get retrieves an association by its non-secret reference.
func (s *Associations) Get(ctx context.Context, ref string) (Association, error) {
	return readAssociation(ctx, s.Store, ref)
}

// RegisterCertificate is called by the issuer before returning a certificate.
// Non-account certificates are ignored. Never call this on an inbound certificate.
func (s *Associations) RegisterCertificate(ctx context.Context, cert *x509.Certificate) error {
	if cert == nil {
		return ErrAssociation
	}
	ref, ok := strings.CutPrefix(cert.Subject.CommonName, CertificateSubjectPrefix)
	if !ok {
		return nil
	}
	hash := cms.Fingerprint(cert)
	key := certificateKey(hash)
	return s.Store.Update(ctx, []string{associationKey(ref), key}, func(tx state.Tx) error {
		a, err := readAssociation(ctx, tx, ref)
		if err != nil {
			return err
		}
		if !a.ExpiresAt.IsZero() && !tx.Now().Before(a.ExpiresAt) {
			return ErrAssociation
		}
		a.ExpiresAt = time.Time{}
		if err := writeAssociation(ctx, tx, a); err != nil {
			return err
		}
		if old, err := tx.Get(ctx, key); err == nil && string(old.Value) != ref {
			return ErrAssociation
		} else if err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
		return tx.Put(ctx, state.Record{Key: key, Value: []byte(ref)})
	})
}

// ByCertificate requires a certificate previously registered by the issuer.
func (s *Associations) ByCertificate(ctx context.Context, cert *x509.Certificate) (Association, error) {
	if cert == nil {
		return Association{}, state.ErrNotFound
	}
	r, err := s.Store.Get(ctx, certificateKey(cms.Fingerprint(cert)))
	if err != nil {
		return Association{}, err
	}
	return s.Get(ctx, string(r.Value))
}

// Bind atomically claims the device-generated identity. Retrying the same identity
// is safe even after a downstream failure. A different identity can never take over
// a claim. Confirmation is a separate durable marker after Authenticate succeeds.
func (s *Associations) Bind(ctx context.Context, ref string, id mdm.EnrollmentID, confirm bool) error {
	if err := id.Validate(); err != nil {
		return err
	}
	if id.Channel.IsUser() {
		return ErrAssociation
	}
	key := associationKey(ref)
	return s.Store.Update(ctx, []string{key}, func(tx state.Tx) error {
		a, err := readAssociation(ctx, tx, ref)
		if err != nil {
			return err
		}
		if (a.Origin == VersionBYOD) != (id.Channel == mdm.ChannelUserEnrollmentDevice) {
			return ErrAssociation
		}
		if a.Enrollment.ID != "" && a.Enrollment != id {
			return ErrAssociation
		}
		a.Enrollment = id
		if confirm && a.ConfirmedAt.IsZero() {
			a.ConfirmedAt = tx.Now()
		}
		return writeAssociation(ctx, tx, a)
	})
}

// RequiresBearer applies Apple's platform/channel rule to a known account flow.
func (a Association) RequiresBearer(ch mdm.Channel) bool {
	isMac := strings.HasPrefix(a.Product, "Mac") || strings.HasPrefix(a.Product, "iMac")
	return !isMac || ch.IsUser()
}

func sameIdentity(a, b Identity) bool {
	return a.ManagedAppleAccount != "" && a.ManagedAppleAccount == b.ManagedAppleAccount && a.Subject == b.Subject && a.Issuer == b.Issuer
}

type associationContextKey struct{}

// IssueSCEPChallenge binds a short-lived credential to a profile's CSR subject.
// Replacing a challenge invalidates the previous value. It is stored only hashed.
func (s *Associations) IssueSCEPChallenge(ctx context.Context, ref string, ttl time.Duration) (string, error) {
	if ttl <= 0 {
		return "", ErrConfig
	}
	token, err := NewToken()
	if err != nil {
		return "", err
	}
	err = s.Store.Update(ctx, []string{associationKey(ref)}, func(tx state.Tx) error {
		a, err := readAssociation(ctx, tx, ref)
		if err != nil {
			return err
		}
		a.ChallengeHash = Hash(token)
		a.ChallengeExpires = tx.Now().Add(ttl)
		a.CSRHash = ""
		return writeAssociation(ctx, tx, a)
	})
	return token, err
}

// VerifySCEPChallenge atomically binds first use to a CSR. A network retry with
// the same CSR is safe; a different key cannot reuse the credential. Renewal is
// authorized separately by proof of the current certificate's private key.
func (s *Associations) VerifySCEPChallenge(ctx context.Context, password string, csr *x509.CertificateRequest) error {
	if csr == nil || password == "" {
		return ErrAssociation
	}
	ref, ok := strings.CutPrefix(csr.Subject.CommonName, CertificateSubjectPrefix)
	if !ok {
		return ErrAssociation
	}
	if err := csr.CheckSignature(); err != nil {
		return ErrAssociation
	}
	return s.Store.Update(ctx, []string{associationKey(ref)}, func(tx state.Tx) error {
		a, err := readAssociation(ctx, tx, ref)
		if err != nil {
			return err
		}
		if a.ChallengeHash != Hash(password) || !tx.Now().Before(a.ChallengeExpires) {
			return ErrAssociation
		}
		hash := Hash(string(csr.Raw))
		if a.CSRHash != "" && a.CSRHash != hash {
			return ErrAssociation
		}
		a.CSRHash = hash
		return writeAssociation(ctx, tx, a)
	})
}

// AssociationFromContext gives ProfileHook the reference to embed in its CSR subject.
func AssociationFromContext(ctx context.Context) (Association, bool) {
	a, ok := ctx.Value(associationContextKey{}).(Association)
	return a, ok
}
