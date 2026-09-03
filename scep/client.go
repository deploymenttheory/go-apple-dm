package scep

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	smallscep "github.com/smallstep/scep"
	"github.com/smallstep/scep/x509util"
)

// Client errors.
var (
	ErrClient = errors.New("scep: client")
	// ErrRejected is returned when the CA answers with a FAILURE CertRep.
	ErrRejected = errors.New("scep: enrollment rejected by CA")
)

// Client enrolls against a SCEP server the way a device does: GetCACert,
// then a PKCSReq (or RenewalReq) carrying the challenge password.
type Client struct {
	URL  string
	HTTP *http.Client
}

// NewClient creates a client for the SCEP URL.
func NewClient(scepURL string, h *http.Client) *Client {
	if h == nil {
		h = http.DefaultClient
	}
	return &Client{URL: scepURL, HTTP: h}
}

func (c *Client) get(ctx context.Context, op string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.operationURL(op), nil)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrClient, err)
	}
	return c.do(req)
}

// operationURL appends operation=op to the SCEP URL, keeping any query
// the deployment put there.
func (c *Client) operationURL(op string) string {
	sep := "?"
	if strings.Contains(c.URL, "?") {
		sep = "&"
	}
	return c.URL + sep + "operation=" + url.QueryEscape(op)
}

func (c *Client) do(req *http.Request) ([]byte, string, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrClient, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMessage))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrClient, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%w: HTTP %d", ErrClient, resp.StatusCode)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// GetCACaps fetches the capability list.
func (c *Client) GetCACaps(ctx context.Context) (string, error) {
	b, _, err := c.get(ctx, "GetCACaps")
	return string(b), err
}

// GetCACert fetches the RA/CA certificates; the first is the recipient
// the envelope is encrypted to.
func (c *Client) GetCACert(ctx context.Context) ([]*x509.Certificate, error) {
	b, ct, err := c.get(ctx, "GetCACert")
	if err != nil {
		return nil, err
	}
	if ct == ContentTypeCACert {
		cert, err := x509.ParseCertificate(b)
		if err != nil {
			return nil, fmt.Errorf("%w: parse CA certificate: %w", ErrClient, err)
		}
		return []*x509.Certificate{cert}, nil
	}
	certs, err := smallscep.CACerts(b)
	if err != nil {
		return nil, fmt.Errorf("%w: parse CA bundle: %w", ErrClient, err)
	}
	if len(certs) == 0 {
		return nil, fmt.Errorf("%w: empty CA bundle", ErrClient)
	}
	return certs, nil
}

// EnrollOptions shape the request.
type EnrollOptions struct {
	Subject   pkix.Name
	Challenge string
	// Renew signs the request with an existing identity instead of a
	// throwaway self-signed certificate, making it a RenewalReq.
	Renew *Identity
	// Recipients overrides the CA certificates fetched with GetCACert.
	Recipients []*x509.Certificate
}

// Identity is a certificate and its key.
type Identity struct {
	Cert *x509.Certificate
	Key  crypto.Signer
}

// Enroll requests a certificate for key. Apple devices generate RSA keys
// for SCEP; any signer the CA policy accepts works here.
func (c *Client) Enroll(ctx context.Context, key crypto.Signer, o EnrollOptions) (*x509.Certificate, error) {
	if key == nil {
		return nil, fmt.Errorf("%w: nil key", ErrClient)
	}
	recipients := o.Recipients
	if len(recipients) == 0 {
		var err error
		if recipients, err = c.GetCACert(ctx); err != nil {
			return nil, err
		}
	}
	csrDER, err := x509util.CreateCertificateRequest(rand.Reader, &x509util.CertificateRequest{
		CertificateRequest: x509.CertificateRequest{
			Subject: o.Subject, SignatureAlgorithm: signatureAlgorithm(key),
		},
		ChallengePassword: o.Challenge,
	}, key)
	if err != nil {
		return nil, fmt.Errorf("%w: create CSR: %w", ErrClient, err)
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, fmt.Errorf("%w: parse CSR: %w", ErrClient, err)
	}
	tmpl := &smallscep.PKIMessage{MessageType: smallscep.PKCSReq, Recipients: recipients}
	if o.Renew != nil {
		tmpl.MessageType = smallscep.RenewalReq
		tmpl.SignerCert, tmpl.SignerKey = o.Renew.Cert, o.Renew.Key
	} else {
		self, err := SelfSigned(key, o.Subject)
		if err != nil {
			return nil, err
		}
		tmpl.SignerCert, tmpl.SignerKey = self, key
	}
	msg, err := smallscep.NewCSRRequest(csr, tmpl)
	if err != nil {
		return nil, fmt.Errorf("%w: build PKIMessage: %w", ErrClient, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.operationURL("PKIOperation"), bytes.NewReader(msg.Raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrClient, err)
	}
	req.Header.Set("Content-Type", ContentTypePKIMessage)
	body, _, err := c.do(req)
	if err != nil {
		return nil, err
	}
	rep, err := smallscep.ParsePKIMessage(body)
	if err != nil {
		return nil, fmt.Errorf("%w: parse CertRep: %w", ErrClient, err)
	}
	if rep.PKIStatus == smallscep.FAILURE {
		return nil, fmt.Errorf("%w: %s", ErrRejected, rep.FailInfo)
	}
	if err := rep.DecryptPKIEnvelope(tmpl.SignerCert, tmpl.SignerKey); err != nil {
		return nil, fmt.Errorf("%w: decrypt CertRep: %w", ErrClient, err)
	}
	if rep.CertRepMessage == nil || rep.Certificate == nil {
		return nil, fmt.Errorf("%w: CertRep without certificate", ErrClient)
	}
	return rep.Certificate, nil
}

func signatureAlgorithm(key crypto.Signer) x509.SignatureAlgorithm {
	if _, ok := key.Public().(*rsa.PublicKey); ok {
		return x509.SHA256WithRSA
	}
	return 0 // let x509 pick for the key type
}

// SelfSigned creates the short-lived self-signed certificate a device
// signs its first PKCSReq with before it has an identity.
func SelfSigned(key crypto.Signer, subject pkix.Name) (*x509.Certificate, error) {
	var serial [8]byte
	_, _ = rand.Read(serial[:]) // never fails since Go 1.24
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: new(big.Int).SetBytes(serial[:]), Subject: subject,
		NotBefore: now.Add(-time.Minute), NotAfter: now.Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, fmt.Errorf("%w: self-signed certificate: %w", ErrClient, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("%w: parse self-signed certificate: %w", ErrClient, err)
	}
	return cert, nil
}
