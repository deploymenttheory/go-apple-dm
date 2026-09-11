package acme

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"reflect"

	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/mdmprotocol/event"
	"github.com/deploymenttheory/go-apple-dm/pki/acme/attest"
	"github.com/deploymenttheory/go-apple-dm/pki/ca"
	"github.com/deploymenttheory/go-apple-dm/pki/revocation"
)

type issued struct {
	order *Order
	cert  *Certificate
}

// issue commits a receipt under the order lock before any registration side
// effects. A failed transaction exposes no certificate. Once committed, every
// retry uses the same DER, even after a process restart or a registration failure.
func (s *Server) issue(e *exchange, o *Order, csr *x509.CertificateRequest) (*issued, error) {
	challenge, err := s.challengeOf(e, o)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(csr.Raw)
	csrHash := hex.EncodeToString(hash[:])
	var receipt issued
	err = s.cfg.Store.UpdateOrder(e.ctx(), o.ID, func(tx Tx) error {
		current, err := tx.GetOrder(e.ctx(), o.ID)
		if err != nil {
			return err
		}
		if current.AccountID != o.AccountID || current.Identifier != o.Identifier ||
			current.AuthzID != o.AuthzID || !reflect.DeepEqual(current.Binding, o.Binding) {
			return ErrConflict
		}
		receipt.order = current
		if current.Status == StatusValid || current.Status == StatusProcessing {
			if current.CertificateID == "" {
				return NewProblem(ProblemOrderNotReady, "the order is processing")
			}
			if current.Status == StatusProcessing && current.CSRHash != csrHash {
				return NewProblem(ProblemBadCSR, "the order already has a receipt for another CSR")
			}
			receipt.cert, err = tx.GetCertificate(e.ctx(), current.CertificateID)
			return err
		}
		if current.Status != StatusReady || !s.cfg.Clock.Now().Before(current.Expires) {
			return NewProblem(ProblemOrderNotReady, "the order is no longer ready")
		}
		authz, err := tx.GetAuthorization(e.ctx(), current.AuthzID)
		if err != nil {
			return err
		}
		stored, err := tx.GetChallenge(e.ctx(), authz.ChallengeID)
		if err != nil {
			return err
		}
		if authz.Status != StatusValid || stored.Status != StatusValid ||
			!bytes.Equal(
				stored.Attestation,
				challenge.Attestation,
			) || stored.Token != challenge.Token {
			return NewProblem(ProblemOrderNotReady, "the authorization changed")
		}
		record, err := s.signReceipt(e, current, csr, stored)
		if err != nil {
			return err
		}
		current.Status, current.CSRHash, current.CertificateID = StatusProcessing, csrHash, record.ID
		current.Error = nil
		if err := tx.PutCertificate(e.ctx(), record); err != nil {
			return err
		}
		if err := tx.PutOrder(e.ctx(), current); err != nil {
			return err
		}
		receipt.cert = record
		return nil
	})
	if err != nil {
		return nil, AsProblem(err)
	}
	if !receipt.cert.PendingRegistration {
		return &receipt, nil
	}
	if err := s.completeReceipt(e, &receipt); err != nil {
		return nil, err
	}
	return &receipt, nil
}

// completeReceipt is shared by finalize retries and standard POST-as-GET polls.
// The account has already been authenticated against the order in either path.
func (s *Server) completeReceipt(e *exchange, receipt *issued) error {
	if receipt.cert.OrderID != receipt.order.ID ||
		receipt.cert.AccountID != receipt.order.AccountID {
		return NewProblem(ProblemServerInternal, "the issuance receipt does not match the order")
	}
	// Policy lookups may use the same database and must run outside the
	// transaction. Recheck before registration, including retries of a receipt.
	if !s.cfg.Clock.Now().Before(receipt.order.Expires) ||
		!s.cfg.Clock.Now().Before(receipt.cert.NotAfter) {
		return NewProblem(ProblemOrderNotReady, "the issuance receipt has expired")
	}
	block, _ := pem.Decode(receipt.cert.ChainPEM)
	if block == nil {
		return NewProblem(ProblemServerInternal, "the issuance receipt is malformed")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return WrapProblem(ProblemServerInternal, err, "the issuance receipt is malformed")
	}
	if err := s.checkKey(e, receipt.order, cert.PublicKey); err != nil {
		return err
	}
	return s.registerReceipt(e, receipt, cert)
}

func issuanceProvenance(o *Order) revocation.Provenance {
	return revocation.Provenance{
		Source: "acme", EnrollmentID: o.Binding.EnrollmentID, AccountID: o.AccountID,
		UDID: o.Binding.EnrollmentUDID(), Serial: o.Binding.Serial,
		Identifiers: []string{o.Identifier.Type + ":" + o.Identifier.Value},
	}
}

// signReceipt uses only pure signing: no registry, depot or policy callbacks.
func (s *Server) signReceipt(
	e *exchange,
	o *Order,
	csr *x509.CertificateRequest,
	c *Challenge,
) (*Certificate, error) {
	otherName, err := ca.PermanentIdentifier(o.Identifier.Value)
	if err != nil {
		return nil, WrapProblem(ProblemServerInternal, err, "the identifier could not be encoded")
	}
	policy := s.cfg.CAPolicy
	policy.OtherNames = append(append([]ca.OtherName(nil), policy.OtherNames...), otherName)
	subject := pkix.Name{CommonName: o.Binding.CommonName, Organization: o.Binding.Organization}
	if subject.CommonName == "" {
		subject.CommonName = o.Binding.Serial
	}
	if subject.CommonName == "" {
		subject.CommonName = o.Identifier.Value
	}
	policy.Subject = &subject
	if !o.Binding.NotAfter.IsZero() {
		if !o.Binding.NotAfter.After(s.cfg.Clock.Now()) {
			return nil, NewProblem(ProblemRejectedIdentifier, "the certificate deadline has passed")
		}
		policy.NotAfter = o.Binding.NotAfter
	}
	cert, err := s.cfg.Signer.Sign(
		revocation.WithProvenance(e.ctx(), issuanceProvenance(o)),
		csr,
		policy,
	)
	if err != nil {
		if errors.Is(err, ca.ErrPolicy) || errors.Is(err, ca.ErrCSR) {
			return nil, WrapProblem(ProblemBadCSR, err, "the certificate request was refused")
		}
		return nil, WrapProblem(ProblemServerInternal, err, "the certificate could not be signed")
	}
	var device attest.Properties
	if a, err := attest.ParseObject(c.Attestation); err == nil {
		device = a.Properties
	}
	id, err := newID()
	if err != nil {
		return nil, WrapProblem(ProblemServerInternal, err, "the certificate could not be recorded")
	}
	return &Certificate{
		ID: id, OrderID: o.ID, AccountID: o.AccountID, Serial: cert.SerialNumber.String(),
		ChainPEM: encodeChain(cert, s.cfg.Signer.Chain()), Device: device, Binding: o.Binding,
		NotAfter: cert.NotAfter, IssuedAt: s.cfg.Clock.Now(), PendingRegistration: true,
	}, nil
}

func (s *Server) registerReceipt(e *exchange, r *issued, cert *x509.Certificate) error {
	provenance := issuanceProvenance(r.order)
	ctx := revocation.WithProvenance(e.ctx(), provenance)
	if s.cfg.Register != nil {
		if err := s.cfg.Register(ctx, cert); err != nil {
			return WrapProblem(ProblemServerInternal, err, "certificate registration unavailable")
		}
	}
	if s.cfg.Revocations != nil {
		if err := s.cfg.Revocations.Register(
			ctx,
			cms.Fingerprint(s.cfg.Signer.Certificate()),
			cert,
			provenance,
		); err != nil {
			return WrapProblem(ProblemServerInternal, err, "certificate registry unavailable")
		}
	}
	changed := false
	err := s.cfg.Store.UpdateOrder(e.ctx(), r.order.ID, func(tx Tx) error {
		current, err := tx.GetOrder(e.ctx(), r.order.ID)
		if err != nil {
			return err
		}
		if current.CertificateID != r.cert.ID || current.CSRHash != r.order.CSRHash {
			return ErrConflict
		}
		if current.Status == StatusValid {
			r.order = current
			return nil
		}
		if current.Status != StatusProcessing || !s.cfg.Clock.Now().Before(current.Expires) ||
			!s.cfg.Clock.Now().Before(r.cert.NotAfter) {
			return NewProblem(ProblemOrderNotReady, "the order is no longer processing")
		}
		r.cert.PendingRegistration = false
		if err := tx.PutCertificate(e.ctx(), r.cert); err != nil {
			return err
		}
		current.Status = StatusValid
		if err := tx.PutOrder(e.ctx(), current); err != nil {
			return err
		}
		r.order, changed = current, true
		return nil
	})
	if err != nil {
		return AsProblem(err)
	}
	if changed {
		s.publish(e.ctx(), event.ACMEIssued, map[string]any{
			"serial": r.cert.Serial, "identifier": r.order.Identifier.Value,
			"device": r.cert.Device.SerialNumber, "udid": r.cert.Device.UDID,
			"enrollment": r.order.Binding.EnrollmentID, "not_after": r.cert.NotAfter,
			"certificate": r.cert.ID,
		})
	}
	return nil
}

func encodeChain(leaf *x509.Certificate, issuers []*x509.Certificate) []byte {
	var out []byte
	for _, c := range append([]*x509.Certificate{leaf}, issuers...) {
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	return out
}
