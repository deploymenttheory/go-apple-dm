package scep

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/smallstep/pkcs7"
	smallscep "github.com/smallstep/scep"

	"github.com/deploymenttheory/go-apple-mdm/ca"
)

// Errors returned by this package.
var (
	ErrCSR       = errors.New("scep: CSR rejected")
	ErrOperation = errors.New("scep: unsupported operation")
	ErrRA        = errors.New("scep: registration authority misconfigured")
	ErrIssue     = errors.New("scep: issuance failed")
)

// Content types on the wire.
const (
	ContentTypeCACert     = "application/x-x509-ca-cert"
	ContentTypeCARACert   = "application/x-x509-ca-ra-cert"
	ContentTypePKIMessage = "application/x-pki-message"
)

// caps is the CACaps advertised. POSTPKIOperation lets the device POST the
// PKCS#7 rather than base64 it into the query.
const caps = "POSTPKIOperation\nSHA-256\nSHA-1\nAES\nDES3\nRenewal\nSCEPStandard"

// maxMessage bounds a PKIOperation body; a CSR envelope is a few KB.
const maxMessage = 1 << 20

// CSRVerifier inspects the decrypted CSR before signing. A nil return
// allows issuance.
type CSRVerifier interface {
	VerifyCSR(ctx context.Context, csr *x509.CertificateRequest) error
}

// CSRVerifierFunc adapts a function to CSRVerifier.
type CSRVerifierFunc func(ctx context.Context, csr *x509.CertificateRequest) error

// VerifyCSR implements CSRVerifier.
func (f CSRVerifierFunc) VerifyCSR(ctx context.Context, csr *x509.CertificateRequest) error {
	return f(ctx, csr)
}

// Server implements the SCEP operations. The RA (the certificate and key
// devices encrypt the envelope to) must be RSA.
type Server struct {
	signer     ca.Signer
	raCert     *x509.Certificate
	raKey      crypto.Signer
	policy     ca.Policy
	challenge  Challenge
	csrVerify  CSRVerifier
	extraCerts []*x509.Certificate
	log        *slog.Logger
}

// Option configures the Server.
type Option func(*Server)

// WithChallenge sets the challenge provider (default: NoChallenge).
func WithChallenge(c Challenge) Option { return func(s *Server) { s.challenge = c } }

// WithCSRVerifier sets the CSR verifier hook.
func WithCSRVerifier(v CSRVerifier) Option { return func(s *Server) { s.csrVerify = v } }

// WithPolicy sets the signing policy.
func WithPolicy(p ca.Policy) Option { return func(s *Server) { s.policy = p } }

// WithExtraCerts adds certificates to GetCACert (intermediates, or a
// separate CA when the RA is not the CA).
func WithExtraCerts(certs ...*x509.Certificate) Option {
	return func(s *Server) { s.extraCerts = append(s.extraCerts, certs...) }
}

// WithLogger sets the logger for rejected requests.
func WithLogger(l *slog.Logger) Option { return func(s *Server) { s.log = l } }

// NewServer builds a Server. raCert and raKey are the RSA registration
// authority the device encrypts to; they are commonly the CA itself.
func NewServer(signer ca.Signer, raCert *x509.Certificate, raKey crypto.Signer, opts ...Option) (*Server, error) {
	if signer == nil || raCert == nil || raKey == nil {
		return nil, fmt.Errorf("%w: signer, RA certificate, and RA key are required", ErrRA)
	}
	if _, ok := raKey.Public().(*rsa.PublicKey); !ok {
		return nil, fmt.Errorf("%w: RA key must be RSA (devices encrypt the envelope to it)", ErrRA)
	}
	s := &Server{signer: signer, raCert: raCert, raKey: raKey, challenge: NoChallenge{}, log: slog.Default()}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// CACaps returns the advertised capabilities.
func (s *Server) CACaps() string { return caps }

// CACert returns the DER the device needs to encrypt to and verify against:
// a single certificate when the RA is the CA, or a PKCS#7 degenerate bundle.
func (s *Server) CACert() ([]byte, string, error) {
	if len(s.extraCerts) == 0 {
		return s.raCert.Raw, ContentTypeCACert, nil
	}
	certs := append([]*x509.Certificate{s.raCert}, s.extraCerts...)
	der, err := smallscep.DegenerateCertificates(certs)
	if err != nil {
		return nil, "", fmt.Errorf("scep: %w", err)
	}
	return der, ContentTypeCARACert, nil
}

// PKIOperation processes a PKCSReq or RenewalReq and returns the DER
// CertRep. A non-nil reply with a non-nil error is a signed failure the
// device should receive; a nil reply means the request was unparseable.
func (s *Server) PKIOperation(ctx context.Context, body []byte) ([]byte, error) {
	// ParsePKIMessage verifies the signature against the embedded signer
	// but does not expose it; parse the PKCS #7 once more for isRenewal.
	p7, err := pkcs7.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCSR, err)
	}
	msg, err := smallscep.ParsePKIMessage(body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCSR, err)
	}
	if err := msg.DecryptPKIEnvelope(s.raCert, s.raKey); err != nil {
		return nil, fmt.Errorf("%w: decrypt: %w", ErrCSR, err)
	}
	req := msg.CSRReqMessage
	if req == nil || req.CSR == nil {
		return nil, fmt.Errorf("%w: no CSR in envelope", ErrCSR)
	}
	// A renewal is signed with a certificate we issued; the challenge is
	// skipped because the existing identity already proves possession.
	if !s.isRenewal(msg, p7.GetOnlySigner()) {
		if err := s.challenge.Verify(ctx, req.ChallengePassword, req.CSR); err != nil {
			return s.fail(msg, smallscep.BadRequest, err)
		}
	}
	if s.csrVerify != nil {
		if err := s.csrVerify.VerifyCSR(ctx, req.CSR); err != nil {
			return s.fail(msg, smallscep.BadRequest, fmt.Errorf("%w: %w", ErrCSR, err))
		}
	}
	cert, err := s.signer.Sign(ctx, req.CSR, s.policy)
	if err != nil {
		return s.fail(msg, smallscep.BadRequest, fmt.Errorf("%w: %w", ErrIssue, err))
	}
	rep, err := msg.Success(s.raCert, s.raKey, cert)
	if err != nil {
		return nil, fmt.Errorf("scep: build CertRep: %w", err)
	}
	return rep.Raw, nil
}

// isRenewal reports whether the request is a RenewalReq signed by a
// certificate that chains to our CA.
func (s *Server) isRenewal(msg *smallscep.PKIMessage, signer *x509.Certificate) bool {
	if msg.MessageType != smallscep.RenewalReq || signer == nil {
		return false
	}
	roots := x509.NewCertPool()
	roots.AddCert(s.signer.Certificate())
	inter := x509.NewCertPool()
	for _, c := range s.signer.Chain() {
		inter.AddCert(c)
	}
	_, err := signer.Verify(x509.VerifyOptions{
		Roots: roots, Intermediates: inter, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	return err == nil
}

// fail builds a signed failure CertRep, returning the original error so the
// caller can log it while still handing the device a valid response.
func (s *Server) fail(msg *smallscep.PKIMessage, info smallscep.FailInfo, cause error) ([]byte, error) {
	rep, err := msg.Fail(s.raCert, s.raKey, info)
	if err != nil {
		return nil, fmt.Errorf("scep: build failure CertRep: %w", err)
	}
	return rep.Raw, cause
}

// Handler serves the SCEP endpoint: GET operation=GetCACaps, GET
// operation=GetCACert, and GET or POST operation=PKIOperation.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("operation") {
		case "GetCACaps":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = io.WriteString(w, caps)
		case "GetCACert":
			der, ct, err := s.CACert()
			if err != nil {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", ct)
			_, _ = w.Write(der)
		case "PKIOperation":
			s.servePKIOperation(w, r)
		default:
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		}
	})
}

func (s *Server) servePKIOperation(w http.ResponseWriter, r *http.Request) {
	body, err := readMessage(r)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	rep, opErr := s.PKIOperation(r.Context(), body)
	if opErr != nil {
		s.log.WarnContext(r.Context(), "scep: request rejected", "error", opErr, "remote", r.RemoteAddr)
	}
	if rep == nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}
	// A signed failure CertRep is still delivered with 200: the device
	// reads the failInfo attribute, not the HTTP status.
	w.Header().Set("Content-Type", ContentTypePKIMessage)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// rep is a DER CertRep produced and signed by this server, never bytes
	// echoed from the request, and the content type is binary.
	_, _ = w.Write(rep) // #nosec G705 -- server-built PKI message, not reflected input
}

func readMessage(r *http.Request) ([]byte, error) {
	if r.Method == http.MethodPost {
		b, err := io.ReadAll(io.LimitReader(r.Body, maxMessage+1))
		if err != nil {
			return nil, fmt.Errorf("scep: read body: %w", err)
		}
		if len(b) > maxMessage {
			return nil, fmt.Errorf("%w: body exceeds %d bytes", ErrOperation, maxMessage)
		}
		return b, nil
	}
	msg := r.URL.Query().Get("message")
	if msg == "" {
		return nil, fmt.Errorf("%w: empty message", ErrOperation)
	}
	return decodeBase64(msg)
}

// decodeBase64 accepts the GET form: standard base64, with '+' possibly
// turned into a space by URL decoding and padding sometimes omitted.
func decodeBase64(s string) ([]byte, error) {
	s = strings.ReplaceAll(strings.TrimSpace(s), " ", "+")
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrOperation, err)
	}
	return b, nil
}
