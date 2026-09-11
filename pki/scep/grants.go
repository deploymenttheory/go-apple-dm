package scep

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/state"
)

// Grant authorizes one CSR. Binding and Admission are caller-owned JSON;
// neither a certificate subject nor a device-supplied identifier proves admission.
// The field names also preserve the reference server's persisted grant format.
type Grant struct {
	Binding   json.RawMessage `json:"binding"`
	Admission json.RawMessage `json:"admission"`
	ExpiresAt time.Time       `json:"expiresAt"`
	CSRHash   string          `json:"csrHash"`
}

// GrantAuthorization verifies the expected subject and current enrollment
// admission. It runs outside the store transaction and must fail closed.
type GrantAuthorization func(context.Context, Grant, *x509.CertificateRequest) error

// Grants persists expiring SCEP authorization and reserves it for one CSR.
// Repeating that CSR is allowed until expiry, including after issuance failure.
// Configure fields before use; all instances for an issuer must share Store.
type Grants struct {
	Store     state.Store
	Policy    ca.Policy
	Authorize GrantAuthorization
}

func grantHash(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func grantKey(password string) string { return "scep/grant/" + grantHash([]byte(password)) }

func (g Grants) configured() error {
	if g.Store == nil || g.Authorize == nil {
		return fmt.Errorf("%w: grant store and authorization are required", ErrChallenge)
	}
	return nil
}

// Issue creates an unpredictable challenge. The deadline is required and is
// capped at one hour from authoritative store time. CSRHash must be empty.
func (g Grants) Issue(ctx context.Context, grant Grant) (string, error) {
	if err := g.configured(); err != nil {
		return "", err
	}
	if grant.CSRHash != "" || grant.ExpiresAt.IsZero() {
		return "", fmt.Errorf("%w: invalid new grant", ErrChallenge)
	}
	var secret [32]byte
	_, _ = rand.Read(secret[:])
	password := base64.RawURLEncoding.EncodeToString(secret[:])
	k := grantKey(password)
	err := g.Store.Update(ctx, []string{k}, func(tx state.Tx) error {
		if !tx.Now().Before(grant.ExpiresAt) {
			return ErrChallenge
		}
		if limit := tx.Now().Add(time.Hour); grant.ExpiresAt.After(limit) {
			grant.ExpiresAt = limit
		}
		raw, err := json.Marshal(grant)
		if err != nil {
			return fmt.Errorf("scep: encode grant: %w", err)
		}
		return tx.Put(ctx, state.Record{Key: k, Value: raw, ExpiresAt: grant.ExpiresAt})
	})
	if err != nil {
		return "", fmt.Errorf("scep: persist grant: %w", err)
	}
	return password, nil
}

// Verify implements Challenge. Invalid CSRs never consume authorization.
// Authorization is rechecked for retries; expiry and reservation serialize in
// Store. A reservation never moves to another CSR, even after signing fails.
func (g Grants) Verify(ctx context.Context, password string, csr *x509.CertificateRequest) error {
	if err := g.configured(); err != nil {
		return err
	}
	if password == "" {
		return ErrChallenge
	}
	if err := ca.ValidateCSR(csr, g.Policy); err != nil {
		return err
	}
	k := grantKey(password)
	r, err := g.Store.Get(ctx, k)
	if errors.Is(err, state.ErrNotFound) {
		return ErrChallenge
	}
	if err != nil {
		return fmt.Errorf("scep: read grant: %w", err)
	}
	var authorized Grant
	if err := json.Unmarshal(r.Value, &authorized); err != nil {
		return fmt.Errorf("scep: decode grant: %w", err)
	}
	if err := g.Authorize(ctx, authorized, csr); err != nil {
		return err
	}
	err = g.Store.Update(ctx, []string{k}, func(tx state.Tx) error {
		r, err := tx.Get(ctx, k)
		if errors.Is(err, state.ErrNotFound) {
			return ErrChallenge
		}
		if err != nil {
			return err
		}
		var current Grant
		if err := json.Unmarshal(r.Value, &current); err != nil {
			return err
		}
		if !tx.Now().Before(current.ExpiresAt) || !current.ExpiresAt.Equal(authorized.ExpiresAt) ||
			!bytes.Equal(
				current.Binding,
				authorized.Binding,
			) || !bytes.Equal(current.Admission, authorized.Admission) {
			return ErrChallenge
		}
		hash := grantHash(csr.Raw)
		if current.CSRHash != "" && current.CSRHash != hash {
			return ErrChallenge
		}
		current.CSRHash = hash
		r.Value, err = json.Marshal(current)
		if err != nil {
			return err
		}
		return tx.Put(ctx, r)
	})
	if err != nil {
		return fmt.Errorf("scep: reserve grant: %w", err)
	}
	return nil
}

// CertificateIssuer caches the exact certificate before completing required
// registration. Authorize must recheck the challenge and current admission on
// every call; Grants.Verify can be used for the ordinary grant flow.
//
// Signer runs inside a Store transaction and must not call back into that store.
// Use ca.Local without a depot, or a signer with the same non-reentrant contract.
// Register runs outside the transaction and must be idempotent. A failed commit
// may cause signing again, but no certificate is returned before persistence
// and registration succeed. All replicas must use the same issuer and policy.
type CertificateIssuer struct {
	Store     state.Store
	Signer    ca.Signer
	Authorize func(context.Context, string, *x509.CertificateRequest) error
	Register  func(context.Context, *x509.Certificate) error
}

// Issue returns the certificate for this authorized challenge and CSR. The
// receipt keys and DER values preserve the reference server's existing format.
func (i CertificateIssuer) Issue(
	ctx context.Context,
	password string,
	csr *x509.CertificateRequest,
	policy ca.Policy,
) (*x509.Certificate, error) {
	if i.Store == nil || i.Signer == nil || i.Authorize == nil || i.Register == nil ||
		password == "" {
		return nil, fmt.Errorf(
			"%w: store, signer, authorization, registration and challenge are required",
			ErrIssue,
		)
	}
	if err := ca.ValidateCSR(csr, policy); err != nil {
		return nil, err
	}
	if err := i.Authorize(ctx, password, csr); err != nil {
		return nil, err
	}
	k := "scep/certificate/" + grantHash([]byte(password)) + "/" + grantHash(csr.Raw)
	var cert *x509.Certificate
	err := i.Store.Update(ctx, []string{k}, func(tx state.Tx) error {
		r, err := tx.Get(ctx, k)
		switch {
		case err == nil:
			cert, err = x509.ParseCertificate(r.Value)
		case errors.Is(err, state.ErrNotFound):
			cert, err = i.Signer.Sign(ctx, csr, policy)
		default:
			return err
		}
		if err != nil {
			return err
		}
		if cert == nil || cert.IsCA || tx.Now().Before(cert.NotBefore) ||
			!tx.Now().Before(cert.NotAfter) {
			return fmt.Errorf("%w: invalid issued certificate", ErrIssue)
		}
		key, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
		if err != nil || !bytes.Equal(key, csr.RawSubjectPublicKeyInfo) {
			return fmt.Errorf("%w: issued key does not match CSR", ErrIssue)
		}
		return tx.Put(ctx, state.Record{Key: k, Value: cert.Raw, ExpiresAt: cert.NotAfter})
	})
	if err != nil {
		return nil, fmt.Errorf("scep: persist certificate: %w", err)
	}
	if err := i.Register(ctx, cert); err != nil {
		return nil, fmt.Errorf("scep: register certificate: %w", err)
	}
	return cert, nil
}
