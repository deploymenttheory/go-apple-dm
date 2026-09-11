package app

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/pki/revocation"
	"github.com/deploymenttheory/go-apple-dm/schema/support"
	"github.com/deploymenttheory/go-apple-dm/state"
	"github.com/deploymenttheory/go-apple-dm/storage"
)

const credentialGrantTTL = 5 * time.Minute

type credentialGrant struct {
	RequireAttestation bool             `json:"requireAttestation,omitempty"`
	Enrollment         mdm.EnrollmentID `json:"enrollment"`
	Certificate        []byte           `json:"certificate"`
	ExpiresAt          time.Time        `json:"expiresAt"`
}

func credentialGrantKey(identifier string) string {
	sum := sha256.Sum256([]byte(identifier))
	return "acme-credential:" + hex.EncodeToString(sum[:])
}

// The code is already unique and claimed atomically by the ACME store. This
// record scopes its use to the authenticated enrollment and a short deadline.
func (s *acmeService) recordCredentialGrant(
	ctx context.Context,
	identifier string,
	id mdm.EnrollmentID,
	cert *x509.Certificate,
	requireAttestation bool,
) error {
	expires := s.app.cfg.Clock.Now().Add(credentialGrantTTL)
	if cert.NotAfter.Before(expires) {
		expires = cert.NotAfter
	}
	if !s.app.cfg.Clock.Now().Before(expires) {
		return fmt.Errorf("%w: expired credential identity", ErrBadACMERequest)
	}
	grant := credentialGrant{
		RequireAttestation: requireAttestation,
		Enrollment:         id,
		Certificate:        cert.Raw,
		ExpiresAt:          expires,
	}
	value, err := json.Marshal(grant)
	if err != nil {
		return fmt.Errorf("app: credential grant: %w", err)
	}
	key := credentialGrantKey(identifier)
	if err := s.app.protocol.Update(ctx, []string{key}, func(tx state.Tx) error {
		return tx.Put(ctx, state.Record{Key: key, Value: value, ExpiresAt: expires})
	}); err != nil {
		return fmt.Errorf("app: record credential grant: %w", err)
	}
	return nil
}

// authorizeCredential rechecks the original identity at both challenge and
// finalization. A code stops working after checkout, certificate replacement,
// revocation, expiry or deletion. Initial enrollment codes have no grant.
func (s *acmeService) authorizeCredential(
	ctx context.Context,
	d *acme.Decision,
	requireMac bool,
) error {
	if d.Binding.EnrollmentID == "" {
		return acme.ErrUnauthorized
	}
	record, err := s.app.protocol.Get(ctx, credentialGrantKey(d.Identifier.Value))
	if errors.Is(err, state.ErrNotFound) {
		return acme.ErrUnauthorized
	}
	if err != nil {
		return fmt.Errorf("app: credential authorization: %w", err)
	}
	var grant credentialGrant
	if err := json.Unmarshal(record.Value, &grant); err != nil {
		return fmt.Errorf("app: decode credential grant: %w", err)
	}
	if (grant.RequireAttestation && d.Attestation == nil) ||
		grant.Enrollment.ID != d.Binding.EnrollmentID ||
		!s.app.cfg.Clock.Now().Before(grant.ExpiresAt) {
		return acme.ErrUnauthorized
	}
	cert, err := x509.ParseCertificate(grant.Certificate)
	if err != nil {
		return fmt.Errorf("app: credential identity: %w", err)
	}
	enrollment, err := s.app.Store.Get(ctx, grant.Enrollment)
	if errors.Is(err, storage.ErrNotFound) {
		return acme.ErrUnauthorized
	}
	if err != nil {
		return fmt.Errorf("app: credential enrollment: %w", err)
	}
	if !enrollment.Enabled || !enrollment.DisabledAt.IsZero() ||
		enrollment.CertHash != cms.Fingerprint(cert) ||
		!s.app.cfg.Clock.Now().Before(cert.NotAfter) ||
		enrollment.Device.SerialNumber != d.Binding.Serial {
		return acme.ErrUnauthorized
	}
	if requireMac && support.OSFromProduct(enrollment.Device.ProductName) != support.MacOS {
		return acme.ErrUnauthorized
	}
	if s.app.revocations != nil {
		if err := s.app.revocations.Check(ctx, cert); err != nil {
			if errors.Is(err, revocation.ErrRevoked) || errors.Is(err, revocation.ErrExpired) ||
				errors.Is(err, revocation.ErrUnknown) {
				return acme.ErrUnauthorized
			}
			return fmt.Errorf("app: credential revocation check: %w", err)
		}
	}
	return nil
}
