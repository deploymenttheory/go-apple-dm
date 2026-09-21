package acme

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"math/big"
	"net/http"
	"slices"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/paging"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/pki/revocation"
)

// Revocations is the optional issuer registry used by the ACME server.
type Revocations interface {
	// Register records the issued certificate and its provenance under the selected issuer.
	Register(context.Context, string, *x509.Certificate, revocation.Provenance) error
	// ByCertificate resolves a certificate to its retained issuance and revocation record.
	ByCertificate(context.Context, *x509.Certificate) (revocation.Certificate, error)
	// Revoke records revocation for the issuer and serial number using the selected reason
	// code.
	Revoke(context.Context, string, *big.Int, int) error
}

// revocationURL returns this server's certificate-revocation endpoint URL.
func (s *Server) revocationURL() string {
	if s.cfg.Revocations == nil {
		return ""
	}
	return s.url("/revoke-cert")
}

// revokeCertificate validates account or certificate-key authority before recording ACME
// certificate revocation.
func (s *Server) revokeCertificate(e *exchange) error {
	var body struct {
		Certificate string `json:"certificate"`
		Reason      int    `json:"reason"`
	}
	if err := json.Unmarshal(e.payload, &body); err != nil {
		return NewProblem(ProblemMalformed, "invalid revocation request")
	}
	if !revocation.ValidReason(body.Reason) {
		return NewProblem(ProblemBadRevocationReason, "unsupported revocation reason")
	}
	der, err := base64.RawURLEncoding.DecodeString(body.Certificate)
	if err != nil {
		return NewProblem(ProblemMalformed, "invalid certificate encoding")
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return NewProblem(ProblemMalformed, "invalid certificate")
	}
	record, err := s.cfg.Revocations.ByCertificate(e.ctx(), cert)
	if errors.Is(err, revocation.ErrUnknown) {
		return NewProblem(ProblemUnauthorized, "certificate was not issued here")
	}
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "certificate status unavailable")
	}
	if e.jws.Header.JWK != nil {
		pub, err := e.jws.Header.JWK.Public()
		if err != nil {
			return NewProblem(ProblemBadPublicKey, "invalid signing key")
		}
		a, err := x509.MarshalPKIXPublicKey(pub)
		if err != nil {
			return NewProblem(ProblemBadPublicKey, "invalid signing key")
		}
		b, err := x509.MarshalPKIXPublicKey(cert.PublicKey)
		if err != nil || !bytes.Equal(a, b) {
			return NewProblem(ProblemUnauthorized, "signing key does not match certificate")
		}
	} else if e.account == nil {
		return NewProblem(ProblemUnauthorized, "account required")
	} else if record.Provenance.AccountID != e.account.ID || record.Provenance.Source != "acme" {
		if err := s.authorizedForAll(e, record.Provenance.Identifiers); err != nil {
			return err
		}
	}
	err = event.Run(e.ctx(), s.cfg.Bus, func(ctx context.Context) error {
		if err := s.cfg.Revocations.Revoke(ctx, record.Issuer, cert.SerialNumber, body.Reason); err != nil {
			return err
		}
		s.publish(ctx, event.CertificateRevoked, map[string]any{"issuer": record.Issuer, "serial": record.Serial, "reason": body.Reason})
		return nil
	})
	if errors.Is(err, revocation.ErrRevoked) {
		return NewProblem(ProblemAlreadyRevoked, "certificate is already revoked")
	}
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "revocation could not be recorded")
	}
	e.w.WriteHeader(http.StatusOK)
	return nil
}

// authorizedForAll implements RFC 8555 section 7.6's third authorization mode.
// Every identifier must have a currently valid authorization for this account;
// ownership of some unrelated issued certificate confers no authority.
func (s *Server) authorizedForAll(e *exchange, identifiers []string) error {
	if len(identifiers) == 0 {
		return NewProblem(ProblemUnauthorized, "no identifier authorization")
	}
	remaining := slices.Clone(identifiers)
	page := paging.Page{}
	for {
		orders, err := s.cfg.Store.ListOrders(e.ctx(), e.account.ID, page)
		if err != nil {
			return WrapProblem(ProblemServerInternal, err, "authorizations unavailable")
		}
		for _, o := range orders.Items {
			a, err := s.cfg.Store.GetAuthorization(e.ctx(), o.AuthzID)
			if err != nil {
				return WrapProblem(ProblemServerInternal, err, "authorization unavailable")
			}
			if a.AccountID != e.account.ID || a.Status != StatusValid || !s.cfg.Clock.Now().Before(a.Expires) {
				continue
			}
			value := a.Identifier.Type + ":" + a.Identifier.Value
			remaining = slices.DeleteFunc(remaining, func(s string) bool { return s == value })
		}
		if len(remaining) == 0 {
			return nil
		}
		if orders.NextCursor == "" {
			break
		}
		page.Cursor = orders.NextCursor
	}
	return NewProblem(ProblemUnauthorized, "account is not authorized for all certificate identifiers")
}
