package app

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
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
	binding, err := json.Marshal(b)
	if err != nil {
		return "", fmt.Errorf("app: encode SCEP binding: %w", err)
	}
	admission, err := json.Marshal(g)
	if err != nil {
		return "", fmt.Errorf("app: encode SCEP admission: %w", err)
	}
	password, err := e.scepGrants().
		Issue(ctx, scep.Grant{Binding: binding, Admission: admission, ExpiresAt: g.ExpiresAt})
	if err != nil {
		return "", fmt.Errorf("app: SCEP grant: %w", err)
	}
	return password, nil
}

func (e *enrollment) scepGrants() scep.Grants {
	return scep.Grants{
		Store:  e.state,
		Policy: ca.Policy{},
		Authorize: func(ctx context.Context, grant scep.Grant, csr *x509.CertificateRequest) error {
			var binding acme.Binding
			if err := json.Unmarshal(grant.Binding, &binding); err != nil {
				return fmt.Errorf("app: decode SCEP binding: %w", err)
			}
			if binding.CommonName != csr.Subject.CommonName {
				return scep.ErrChallenge
			}
			_, err := e.admit(ctx, binding)
			return err
		},
	}
}

func (e *enrollment) verifySCEPGrant(
	ctx context.Context,
	password string,
	csr *x509.CertificateRequest,
) error {
	if err := e.scepGrants().Verify(ctx, password, csr); err != nil {
		return fmt.Errorf("app: SCEP grant: %w", err)
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
	ctx = context.WithValue(ctx, issuanceBindingKey{}, binding)
	issuer := scep.CertificateIssuer{
		Store: e.state, Signer: pure, Register: e.depot.Put,
		Authorize: func(ctx context.Context, password string, csr *x509.CertificateRequest) error {
			if err := (enrollmentChallenge{app: e.app, associations: e.depot.associations}).Verify(
				ctx,
				password,
				csr,
			); err != nil {
				return err
			}
			_, err := e.admit(ctx, binding)
			return err
		},
	}
	cert, err := issuer.Issue(ctx, password, csr, p)
	if err != nil {
		return nil, fmt.Errorf("app: issue SCEP: %w", err)
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
