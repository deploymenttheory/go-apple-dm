package pushcert

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"time"

	"howett.net/plist"
)

// GenerateCSR returns an RSA-2048 PKCS#8 private key and a SHA-256 PKCS#10
// request, both PEM encoded. The key stays with the requesting customer.
func GenerateCSR(subject pkix.Name) (keyPEM, csrPEM []byte, err error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("pushcert: generate key: %w", err)
	}
	der, err := x509.CreateCertificateRequest(
		rand.Reader,
		&x509.CertificateRequest{Subject: subject, SignatureAlgorithm: x509.SHA256WithRSA},
		key,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("pushcert: create CSR: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, fmt.Errorf("pushcert: encode key: %w", err)
	}
	return pem.EncodeToMemory(
			&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER},
		), pem.EncodeToMemory(
			&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der},
		), nil
}

// SignCSR creates the base64 XML plist accepted by Apple's Push Certificates
// Portal. chainPEM must contain the vendor signing leaf, its intermediates, and
// its self-signed root in that order. roots is an explicit trust policy: callers
// must supply Apple roots obtained from Apple's certificate authority site.
// The customer private key is never an input. Portal acceptance also requires
// Apple to have issued the leaf as an MDM Vendor CSR Signing Certificate.
func SignCSR(
	csrData, chainPEM, vendorKeyPEM []byte,
	roots *x509.CertPool,
	at time.Time,
) ([]byte, error) {
	if b, _ := pem.Decode(csrData); b != nil {
		if b.Type != "CERTIFICATE REQUEST" {
			return nil, fmt.Errorf("%w: expected CSR", ErrInvalid)
		}
		csrData = b.Bytes
	}
	csr, err := x509.ParseCertificateRequest(csrData)
	if err != nil {
		return nil, fmt.Errorf("%w: CSR: %w", ErrInvalid, err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("%w: CSR signature: %w", ErrInvalid, err)
	}
	pair, err := parsePair(chainPEM, vendorKeyPEM)
	if err != nil {
		return nil, err
	}
	key, ok := pair.TLS.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%w: vendor signing requires RSA", ErrInvalid)
	}
	if err := verifyVendorChain(pair.TLS.Certificate, roots, at); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(csr.Raw)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return nil, fmt.Errorf("pushcert: sign CSR: %w", err)
	}
	canonical, err := PEM(chainPEM)
	if err != nil {
		return nil, err
	}
	envelope := struct {
		CSR       string `plist:"PushCertRequestCSR"`
		Chain     string `plist:"PushCertCertificateChain"`
		Signature string `plist:"PushCertSignature"`
	}{base64.StdEncoding.EncodeToString(csr.Raw), string(canonical), base64.StdEncoding.EncodeToString(sig)}
	xml, err := plist.Marshal(envelope, plist.XMLFormat)
	if err != nil {
		return nil, fmt.Errorf("pushcert: encode envelope: %w", err)
	}
	return []byte(base64.StdEncoding.EncodeToString(xml)), nil
}

func verifyVendorChain(chain [][]byte, roots *x509.CertPool, at time.Time) error {
	if roots == nil || len(chain) < 3 {
		return fmt.Errorf(
			"%w: vendor chain requires leaf, intermediate, root and explicit trusted roots",
			ErrInvalid,
		)
	}
	certs := make([]*x509.Certificate, len(chain))
	intermediates := x509.NewCertPool()
	for j, der := range chain {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return fmt.Errorf("%w: vendor chain: %w", ErrInvalid, err)
		}
		certs[j] = cert
		if at.Before(cert.NotBefore) || !at.Before(cert.NotAfter) {
			return fmt.Errorf("%w: vendor chain outside validity interval", ErrInvalid)
		}
		if j > 0 {
			intermediates.AddCert(cert)
		}
	}
	for j := range len(certs) - 1 {
		if err := certs[j].CheckSignatureFrom(certs[j+1]); err != nil {
			return fmt.Errorf("%w: unordered or incomplete vendor chain: %w", ErrInvalid, err)
		}
	}
	root := certs[len(certs)-1]
	if err := root.CheckSignatureFrom(root); err != nil {
		return fmt.Errorf("%w: last certificate must be a self-signed root: %w", ErrInvalid, err)
	}
	if certs[0].IsCA || certs[0].KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return fmt.Errorf("%w: vendor leaf cannot sign", ErrInvalid)
	}
	if _, err := certs[0].Verify(
		x509.VerifyOptions{
			Roots:         roots,
			Intermediates: intermediates,
			CurrentTime:   at,
			KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
		},
	); err != nil {
		return fmt.Errorf("%w: untrusted vendor chain: %w", ErrInvalid, err)
	}
	return nil
}
