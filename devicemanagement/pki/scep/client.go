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

	"github.com/smallstep/pkcs7"
	smallscep "github.com/smallstep/scep"
	"github.com/smallstep/scep/x509util"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/internal/scepwire"
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

// NewClient creates a client for the SCEP URL. It copies h, supplies a
// 30-second timeout when unset, and refuses redirects.
func NewClient(scepURL string, h *http.Client) *Client {
	if h == nil {
		h = http.DefaultClient
	}
	copy := *h
	if copy.Timeout == 0 {
		copy.Timeout = 30 * time.Second
	}
	copy.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{URL: scepURL, HTTP: &copy}
}

func (c *Client) get(ctx context.Context, op string, requireTLS bool) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.operationURL(op), nil)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrClient, err)
	}
	return c.do(req, requireTLS)
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

func (c *Client) do(req *http.Request, requireTLS bool) ([]byte, string, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrClient, err)
	}
	defer resp.Body.Close()
	if requireTLS && (resp.TLS == nil || len(resp.TLS.VerifiedChains) == 0) {
		return nil, "", fmt.Errorf("%w: CA discovery requires verified TLS", ErrClient)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxMessage+1))
	if err != nil {
		return nil, "", fmt.Errorf("%w: %w", ErrClient, err)
	}
	if len(body) > maxMessage {
		return nil, "", fmt.Errorf("%w: response exceeds size limit", ErrClient)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%w: HTTP %d", ErrClient, resp.StatusCode)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

// GetCACaps fetches the capability list.
func (c *Client) GetCACaps(ctx context.Context) (string, error) {
	b, _, err := c.get(ctx, "GetCACaps", false)
	return string(b), err
}

// GetCACert fetches the RA/CA certificates; the first is the recipient
// the envelope is encrypted to. This method alone does not authenticate
// the certificates; Enroll authenticates discovery before using them.
func (c *Client) GetCACert(ctx context.Context) ([]*x509.Certificate, error) {
	return c.getCACert(ctx, false)
}

func (c *Client) getCACert(ctx context.Context, requireTLS bool) ([]*x509.Certificate, error) {
	b, ct, err := c.get(ctx, "GetCACert", requireTLS)
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
	// Renew signs with an existing identity and sends a RenewalReq.
	Renew *Identity
	// Recipients pins the trusted CA/RA bundle, overriding GetCACert.
	// Required for HTTP enrollment. Do not populate it from an untrusted fetch.
	Recipients []*x509.Certificate
	// Roots overrides the CA trust anchors derived from Recipients or
	// verified HTTPS discovery. Supply it when pinning only RA certificates.
	Roots *x509.CertPool
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
		u, err := url.Parse(c.URL)
		if err != nil || u.Scheme != "https" {
			return nil, fmt.Errorf(
				"%w: automatic CA discovery requires HTTPS; pin Recipients for HTTP",
				ErrClient,
			)
		}
		if recipients, err = c.getCACert(ctx, true); err != nil {
			return nil, err
		}
	}
	roots := o.Roots
	if roots == nil {
		roots = x509.NewCertPool()
		for _, cert := range recipients {
			if cert != nil && cert.IsCA {
				roots.AddCert(cert)
			}
		}
	}
	intermediates := x509.NewCertPool()
	for _, cert := range recipients {
		if cert == nil {
			return nil, fmt.Errorf("%w: nil CA/RA certificate", ErrClient)
		}
		intermediates.AddCert(cert)
	}
	verify := x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	for _, cert := range recipients {
		if _, err := cert.Verify(verify); err != nil {
			return nil, fmt.Errorf("%w: CA/RA certificate: %w", ErrClient, err)
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
		if o.Renew.Cert == nil || o.Renew.Key == nil {
			return nil, fmt.Errorf("%w: incomplete renewal identity", ErrClient)
		}
		tmpl.MessageType = smallscep.RenewalReq
		tmpl.SignerCert, tmpl.SignerKey = o.Renew.Cert, o.Renew.Key
	} else {
		self, err := SelfSigned(key, o.Subject)
		if err != nil {
			return nil, err
		}
		tmpl.SignerCert, tmpl.SignerKey = self, key
	}
	msg, err := scepwire.Request(csr, tmpl)
	if err != nil {
		return nil, fmt.Errorf("%w: build PKIMessage: %w", ErrClient, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.operationURL("PKIOperation"), bytes.NewReader(msg.Raw))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrClient, err)
	}
	req.Header.Set("Content-Type", ContentTypePKIMessage)
	body, _, err := c.do(req, false)
	if err != nil {
		return nil, err
	}
	// Limit signers to the authenticated CA/RA bundle, not arbitrary device
	// certificates that happen to chain to the same CA.
	rep, err := smallscep.ParsePKIMessage(body, smallscep.WithCACerts(recipients))
	if err != nil {
		return nil, fmt.Errorf("%w: parse CertRep: %w", ErrClient, err)
	}
	if rep.MessageType != smallscep.CertRep || rep.CertRepMessage == nil ||
		rep.TransactionID != msg.TransactionID || !bytes.Equal(rep.RecipientNonce, msg.SenderNonce) {
		return nil, fmt.Errorf("%w: CertRep does not match the request", ErrClient)
	}
	if rep.PKIStatus == smallscep.FAILURE {
		return nil, fmt.Errorf("%w: %s", ErrRejected, rep.FailInfo)
	}
	if rep.PKIStatus != smallscep.SUCCESS {
		return nil, fmt.Errorf("%w: enrollment is pending", ErrClient)
	}
	// Decode the entire inner bundle rather than assuming its first entry
	// is the issued certificate (or that an entry exists).
	signed, err := pkcs7.Parse(body)
	if err != nil {
		return nil, fmt.Errorf("%w: CertRep: %w", ErrClient, err)
	}
	if err := scepwire.CheckEnvelope(signed.Content); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrClient, err)
	}
	envelope, err := pkcs7.Parse(signed.Content)
	if err != nil {
		return nil, fmt.Errorf("%w: CertRep envelope: %w", ErrClient, err)
	}
	der, err := envelope.Decrypt(tmpl.SignerCert, tmpl.SignerKey)
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt CertRep: %w", ErrClient, err)
	}
	certs, err := smallscep.CACerts(der)
	if err != nil {
		return nil, fmt.Errorf("%w: CertRep certificates: %w", ErrClient, err)
	}
	var issued *x509.Certificate
	for _, cert := range certs {
		intermediates.AddCert(cert)
		if bytes.Equal(cert.RawSubjectPublicKeyInfo, csr.RawSubjectPublicKeyInfo) {
			if issued != nil {
				return nil, fmt.Errorf("%w: ambiguous issued certificate", ErrClient)
			}
			issued = cert
		}
	}
	if issued == nil {
		return nil, fmt.Errorf("%w: CertRep has no certificate for the requested key", ErrClient)
	}
	if _, err := issued.Verify(verify); err != nil {
		return nil, fmt.Errorf("%w: issued certificate: %w", ErrClient, err)
	}
	return issued, nil
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
