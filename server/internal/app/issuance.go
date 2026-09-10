package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/enroll/accountdriven"
	"github.com/deploymenttheory/go-apple-dm/pki/acme"
	"github.com/deploymenttheory/go-apple-dm/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/pki/scep"
	"github.com/deploymenttheory/go-apple-dm/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/server/service"
	"github.com/deploymenttheory/go-apple-dm/state"
)

type (
	issuanceBindingKey struct{}
	scepGrant          struct {
		Binding   acme.Binding   `json:"binding"`
		Admission AdmissionGrant `json:"admission"`
		ExpiresAt time.Time      `json:"expiresAt"`
		CSRHash   string         `json:"csrHash"`
	}
)

func issuanceHash(b []byte) string        { h := sha256.Sum256(b); return fmt.Sprintf("%x", h) }
func scepGrantKey(password string) string { return "scep/grant/" + issuanceHash([]byte(password)) }

func (e *enrollment) issueSCEPGrant(
	ctx context.Context,
	b acme.Binding,
	g AdmissionGrant,
) (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("app: issuance grant: %w", err)
	}
	password := base64.RawURLEncoding.EncodeToString(secret)
	expires := e.now().Add(time.Hour)
	if g.ExpiresAt.Before(expires) {
		expires = g.ExpiresAt
	}
	raw, err := json.Marshal(scepGrant{Binding: b, Admission: g, ExpiresAt: expires})
	if err != nil {
		return "", fmt.Errorf("app: issuance grant: %w", err)
	}
	k := scepGrantKey(password)
	err = e.state.Update(
		ctx,
		[]string{k},
		func(tx state.Tx) error { return tx.Put(ctx, state.Record{Key: k, Value: raw, ExpiresAt: expires}) },
	)
	if err != nil {
		return "", fmt.Errorf("app: persist issuance grant: %w", err)
	}
	return password, nil
}

func (e *enrollment) verifySCEPGrant(
	ctx context.Context,
	password string,
	csr *x509.CertificateRequest,
) error {
	if csr == nil || password == "" {
		return scep.ErrChallenge
	}
	k := scepGrantKey(password)
	record, err := e.state.Get(ctx, k)
	if errors.Is(err, state.ErrNotFound) {
		return scep.ErrChallenge
	}
	if err != nil {
		return fmt.Errorf("app: issuance state: %w", err)
	}
	var grant scepGrant
	if err = json.Unmarshal(record.Value, &grant); err != nil {
		return fmt.Errorf("app: issuance state: %w", err)
	}
	if _, err = e.admit(ctx, grant.Binding); err != nil {
		return fmt.Errorf("app: issuance state: %w", err)
	}
	err = e.state.Update(ctx, []string{k}, func(tx state.Tx) error {
		r, err := tx.Get(ctx, k)
		if errors.Is(err, state.ErrNotFound) {
			return scep.ErrChallenge
		}
		if err != nil {
			return fmt.Errorf("app: issuance state: %w", err)
		}
		var g scepGrant
		if err := json.Unmarshal(r.Value, &g); err != nil {
			return fmt.Errorf("app: issuance state: %w", err)
		}
		if !tx.Now().Before(g.ExpiresAt) || csr.Subject.CommonName != g.Binding.CommonName {
			return scep.ErrChallenge
		}
		hash := issuanceHash(csr.Raw)
		if g.CSRHash != "" && g.CSRHash != hash {
			return scep.ErrChallenge
		}
		g.CSRHash = hash
		r.Value, err = json.Marshal(g)
		if err != nil {
			return fmt.Errorf("app: issuance state: %w", err)
		}
		return tx.Put(ctx, r)
	})
	if err != nil {
		return fmt.Errorf("app: reserve issuance grant: %w", err)
	}
	return nil
}

// issueSCEP caches the exact certificate before registration. Signing within
// this transaction uses a pure CA without callbacks, avoiding nested state locks.
// Registration then completes idempotently before any certificate is returned.
func (e *enrollment) issueSCEP(
	ctx context.Context,
	csr *x509.CertificateRequest,
	p ca.Policy,
	password string,
	renewal bool,
) (*x509.Certificate, error) {
	if renewal {
		old := scep.RenewalCertificate(ctx)
		if old == nil {
			return nil, scep.ErrChallenge
		}
		r, err := e.state.Get(ctx, "issued-identity:"+cms.Fingerprint(old))
		if err != nil {
			return nil, fmt.Errorf("app: issue certificate: %w", err)
		}
		var evidence identityEvidence
		if err = json.Unmarshal(r.Value, &evidence); err != nil {
			return nil, fmt.Errorf("app: issue certificate: %w", err)
		}
		binding := acme.Binding{
			CommonName: csr.Subject.CommonName,
			UDID:       evidence.UDID,
			Serial:     evidence.Serial,
		}
		if _, err = e.admit(ctx, binding); err != nil {
			return nil, fmt.Errorf("app: issue certificate: %w", err)
		}
		ctx = context.WithValue(ctx, issuanceBindingKey{}, binding)
		cert, err := e.local.Sign(ctx, csr, p)
		if err != nil {
			return nil, fmt.Errorf("app: renew identity: %w", err)
		}
		return cert, nil
	}
	var binding acme.Binding
	switch {
	case strings.HasPrefix(csr.Subject.CommonName, replacementSubjectPrefix):
		id, _, err := parseReplacementSubject(csr.Subject.CommonName)
		if err != nil {
			return nil, err
		}
		device, err := e.app.Store.Get(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("app: replacement admission: %w", err)
		}
		binding = acme.Binding{
			CommonName: csr.Subject.CommonName,
			UDID:       id.ID,
			Serial:     device.Device.SerialNumber,
		}
	case strings.HasPrefix(csr.Subject.CommonName, accountdriven.CertificateSubjectPrefix):
		binding.CommonName = csr.Subject.CommonName
	default:
		r, err := e.state.Get(ctx, scepGrantKey(password))
		if err != nil {
			return nil, fmt.Errorf("app: issuance grant: %w", err)
		}
		var g scepGrant
		if err := json.Unmarshal(r.Value, &g); err != nil {
			return nil, fmt.Errorf("app: issue certificate: %w", err)
		}
		if !e.now().Before(g.ExpiresAt) || g.CSRHash != issuanceHash(csr.Raw) ||
			g.Binding.CommonName != csr.Subject.CommonName {
			return nil, scep.ErrChallenge
		}
		binding = g.Binding
	}
	if _, err := e.admit(ctx, binding); err != nil {
		return nil, err
	}
	pure, err := ca.NewLocal(e.caCert, e.caKey, ca.WithClock(e.app.cfg.Clock))
	if err != nil {
		return nil, fmt.Errorf("app: issue certificate: %w", err)
	}
	k := "scep/certificate/" + issuanceHash([]byte(password)) + "/" + issuanceHash(csr.Raw)
	var cert *x509.Certificate
	err = e.state.Update(ctx, []string{k}, func(tx state.Tx) error {
		if r, err := tx.Get(ctx, k); err == nil {
			cert, err = x509.ParseCertificate(r.Value)
			if err != nil {
				return fmt.Errorf("app: cached certificate: %w", err)
			}
			return nil
		} else if !errors.Is(err, state.ErrNotFound) {
			return fmt.Errorf("app: issuance state: %w", err)
		}
		var err error
		cert, err = pure.Sign(ctx, csr, p)
		if err != nil {
			return fmt.Errorf("app: issuance state: %w", err)
		}
		return tx.Put(ctx, state.Record{Key: k, Value: cert.Raw, ExpiresAt: cert.NotAfter})
	})
	if err != nil {
		return nil, fmt.Errorf("app: issue certificate: %w", err)
	}
	ctx = context.WithValue(ctx, issuanceBindingKey{}, binding)
	if err := e.depot.Put(ctx, cert); err != nil {
		return nil, fmt.Errorf("app: issue certificate: %w", err)
	}
	return cert, nil
}

type issuanceAdmission struct{ app *App }

func (h issuanceAdmission) Before(ctx context.Context, c *service.Call) (context.Context, error) {
	if c.Op != "checkin:Authenticate" || c.Request == nil || c.Request.Certificate == nil {
		return ctx, nil
	}
	cert := c.Request.Certificate
	if strings.HasPrefix(cert.Subject.CommonName, accountdriven.CertificateSubjectPrefix) ||
		strings.HasPrefix(cert.Subject.CommonName, replacementSubjectPrefix) {
		return ctx, nil
	}
	r, err := h.app.protocol.Get(ctx, "issued-identity:"+cms.Fingerprint(cert))
	if err != nil {
		return ctx, fmt.Errorf("app: issuance admission: %w", err)
	}
	var evidence identityEvidence
	if err := json.Unmarshal(r.Value, &evidence); err != nil {
		return ctx, fmt.Errorf("app: issuance admission: %w", err)
	}
	if evidence.EnrollmentID != "" && evidence.EnrollmentID != c.Request.ID.ID {
		return ctx, scep.ErrChallenge
	}
	if evidence.UDID == "" && evidence.Serial == "" && evidence.EnrollmentID == "" {
		return ctx, scep.ErrChallenge
	}
	if evidence.UDID != "" && evidence.UDID != c.Request.ID.ID {
		return ctx, scep.ErrChallenge
	}
	if evidence.Serial != "" {
		m, ok := c.Checkin.Message.(*checkin.Authenticate)
		if !ok || m.SerialNumber == nil || *m.SerialNumber != evidence.Serial {
			return ctx, scep.ErrChallenge
		}
	}
	return ctx, nil
}
func (issuanceAdmission) After(context.Context, *service.Call, error) {}
