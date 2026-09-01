// Package cms signs and verifies the detached CMS (PKCS #7) signatures Apple
// MDM uses: devices send one in the Mdm-Signature header when the MDM payload
// sets SignMessage, and servers sign configuration profiles.
//
// Apple documentation:
//   - https://developer.apple.com/documentation/devicemanagement/check-in
//   - https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//
// Verification wraps github.com/smallstep/pkcs7 and adds a trust store, an
// injectable clock, and a signing-time tolerance (decision record 0006): a
// device whose clock lags can sign with a certificate whose NotBefore is a
// few seconds in the future, which the library rejects unconditionally.
package cms

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/smallstep/pkcs7"
)

// Errors returned by this package.
var (
	ErrHeader          = errors.New("cms: malformed Mdm-Signature header")
	ErrParse           = errors.New("cms: malformed CMS structure")
	ErrNoSigner        = errors.New("cms: no signer")
	ErrMultipleSigners = errors.New("cms: more than one signer")
	ErrSignature       = errors.New("cms: signature verification failed")
	ErrSigningTime     = errors.New("cms: signing time outside certificate validity")
	ErrChain           = errors.New("cms: certificate chain verification failed")
	ErrAlgorithm       = errors.New("cms: unsupported algorithm")
	ErrSign            = errors.New("cms: signing failed")
)

// HeaderName is the HTTP header Apple devices use.
const HeaderName = "Mdm-Signature"

// VerifyOptions control Verify.
type VerifyOptions struct {
	// Roots, when set, requires the signer certificate to chain to one of
	// them (intermediates from the CMS structure are used). Nil skips chain
	// verification and only checks the signature itself.
	Roots *x509.CertPool
	// Now supplies the verification time; defaults to time.Now.
	Now func() time.Time
	// ClockSkew tolerates a signing time this far outside the signer
	// certificate's validity. Zero means no tolerance.
	ClockSkew time.Duration
}

func (o VerifyOptions) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Sign produces a detached, DER-encoded CMS SignedData over content with
// SHA-256, signed by key and carrying cert.
func Sign(content []byte, cert *x509.Certificate, key crypto.Signer) ([]byte, error) {
	return signedData(content, cert, key, true)
}

// signedData builds the SignedData for Sign (detached) and SignAttached.
func signedData(content []byte, cert *x509.Certificate, key crypto.Signer, detached bool) ([]byte, error) {
	if cert == nil || key == nil {
		return nil, fmt.Errorf("%w: nil certificate or key", ErrSign)
	}
	sd, err := pkcs7.NewSignedData(content)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSign, err)
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	switch key.Public().(type) {
	case *rsa.PublicKey:
		sd.SetEncryptionAlgorithm(pkcs7.OIDEncryptionAlgorithmRSA)
	case *ecdsa.PublicKey:
		sd.SetEncryptionAlgorithm(pkcs7.OIDEncryptionAlgorithmECDSAP256)
	default:
		return nil, fmt.Errorf("%w: key type %T", ErrAlgorithm, key.Public())
	}
	if err := sd.AddSigner(cert, key, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSign, err)
	}
	if detached {
		sd.Detach()
	}
	der, err := sd.Finish()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSign, err)
	}
	return der, nil
}

// EncodeHeader renders a DER signature as the Mdm-Signature header value.
func EncodeHeader(der []byte) string { return base64.StdEncoding.EncodeToString(der) }

// DecodeHeader parses an Mdm-Signature header value.
func DecodeHeader(header string) ([]byte, error) {
	if header == "" {
		return nil, fmt.Errorf("%w: empty", ErrHeader)
	}
	der, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHeader, err)
	}
	return der, nil
}

// VerifyHeader verifies an Mdm-Signature header against the request body.
func VerifyHeader(header string, body []byte, o VerifyOptions) (*x509.Certificate, error) {
	der, err := DecodeHeader(header)
	if err != nil {
		return nil, err
	}
	return Verify(der, body, o)
}

// Verify checks a detached signature over content and returns the signer
// certificate.
func Verify(der, content []byte, o VerifyOptions) (*x509.Certificate, error) {
	p7, err := pkcs7.Parse(der)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrParse, err)
	}
	switch len(p7.Signers) {
	case 0:
		return nil, ErrNoSigner
	case 1:
	default:
		return nil, fmt.Errorf("%w: %d", ErrMultipleSigners, len(p7.Signers))
	}
	p7.Content = content
	signer := p7.GetOnlySigner()
	if signer == nil {
		return nil, ErrNoSigner
	}
	now := o.now()
	// The library checks the signature and the signing time; the chain is
	// verified here so its errors keep their x509 types.
	err = p7.VerifyWithChainAtTime(nil, now)
	var ste *pkcs7.SigningTimeNotValidError
	switch {
	case err == nil:
	case errors.As(err, &ste) && o.ClockSkew > 0 && withinSkew(ste, o.ClockSkew):
		if terr := verifyTolerant(p7, signer, content); terr != nil {
			return nil, terr
		}
	case errors.As(err, &ste):
		return nil, fmt.Errorf("%w: %w", ErrSigningTime, err)
	default:
		return nil, fmt.Errorf("%w: %w", ErrSignature, err)
	}
	if err := verifyChain(p7, signer, o.Roots, now); err != nil {
		return nil, err
	}
	return signer, nil
}

// verifyChain checks the signer chains to roots at now using the CMS
// structure's certificates as intermediates. A nil roots pool skips it.
func verifyChain(p7 *pkcs7.PKCS7, signer *x509.Certificate, roots *x509.CertPool, now time.Time) error {
	if roots == nil {
		return nil
	}
	inter := x509.NewCertPool()
	for _, c := range p7.Certificates {
		inter.AddCert(c)
	}
	if _, err := signer.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, CurrentTime: now, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}}); err != nil {
		return fmt.Errorf("%w: %w", ErrChain, err)
	}
	return nil
}

func withinSkew(e *pkcs7.SigningTimeNotValidError, skew time.Duration) bool {
	return !e.SigningTime.Before(e.NotBefore.Add(-skew)) && !e.SigningTime.After(e.NotAfter.Add(skew))
}

// rawAttribute mirrors the library's parsed attribute so the signed
// attributes can be re-encoded exactly as the signer did.
type rawAttribute struct {
	Type  asn1.ObjectIdentifier
	Value asn1.RawValue `asn1:"set"`
}

// verifyTolerant repeats the library's checks without the signing-time
// rejection: message digest and the signature over the authenticated
// attributes.
func verifyTolerant(p7 *pkcs7.PKCS7, signer *x509.Certificate, content []byte) error {
	si := p7.Signers[0]
	var attrs []rawAttribute
	var digest []byte
	found := false
	for _, a := range si.AuthenticatedAttributes {
		attrs = append(attrs, rawAttribute{Type: a.Type, Value: a.Value})
		if a.Type.Equal(pkcs7.OIDAttributeMessageDigest) {
			if _, err := asn1.Unmarshal(a.Value.Bytes, &digest); err != nil {
				return fmt.Errorf("%w: message digest attribute: %w", ErrSignature, err)
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%w: no message digest attribute", ErrSignature)
	}
	h, err := hashFor(si.DigestAlgorithm.Algorithm)
	if err != nil {
		return err
	}
	hasher := h.New()
	hasher.Write(content)
	if subtle.ConstantTimeCompare(digest, hasher.Sum(nil)) != 1 {
		return fmt.Errorf("%w: message digest mismatch", ErrSignature)
	}
	signed, err := marshalAttributes(attrs)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSignature, err)
	}
	alg, err := signatureAlgorithm(si.DigestEncryptionAlgorithm.Algorithm, h)
	if err != nil {
		return err
	}
	if err := signer.CheckSignature(alg, signed, si.EncryptedDigest); err != nil {
		return fmt.Errorf("%w: %w", ErrSignature, err)
	}
	return nil
}

// marshalAttributes encodes the authenticated attributes as the DER SET the
// signature covers.
func marshalAttributes(attrs []rawAttribute) ([]byte, error) {
	return asn1.MarshalWithParams(attrs, "set")
}

func hashFor(oid asn1.ObjectIdentifier) (crypto.Hash, error) {
	switch {
	case oid.Equal(pkcs7.OIDDigestAlgorithmSHA1):
		return crypto.SHA1, nil
	case oid.Equal(pkcs7.OIDDigestAlgorithmSHA256):
		return crypto.SHA256, nil
	case oid.Equal(pkcs7.OIDDigestAlgorithmSHA384):
		return crypto.SHA384, nil
	case oid.Equal(pkcs7.OIDDigestAlgorithmSHA512):
		return crypto.SHA512, nil
	}
	return 0, fmt.Errorf("%w: digest %v", ErrAlgorithm, oid)
}

func signatureAlgorithm(oid asn1.ObjectIdentifier, h crypto.Hash) (x509.SignatureAlgorithm, error) {
	rsaByHash := map[crypto.Hash]x509.SignatureAlgorithm{
		crypto.SHA1: x509.SHA1WithRSA, crypto.SHA256: x509.SHA256WithRSA, crypto.SHA384: x509.SHA384WithRSA, crypto.SHA512: x509.SHA512WithRSA,
	}
	ecdsaByHash := map[crypto.Hash]x509.SignatureAlgorithm{
		crypto.SHA1: x509.ECDSAWithSHA1, crypto.SHA256: x509.ECDSAWithSHA256, crypto.SHA384: x509.ECDSAWithSHA384, crypto.SHA512: x509.ECDSAWithSHA512,
	}
	switch {
	case oid.Equal(pkcs7.OIDEncryptionAlgorithmRSA), oid.Equal(pkcs7.OIDEncryptionAlgorithmRSASHA1),
		oid.Equal(pkcs7.OIDEncryptionAlgorithmRSASHA256), oid.Equal(pkcs7.OIDEncryptionAlgorithmRSASHA384), oid.Equal(pkcs7.OIDEncryptionAlgorithmRSASHA512):
		return rsaByHash[h], nil
	case oid.Equal(pkcs7.OIDEncryptionAlgorithmECDSAP256), oid.Equal(pkcs7.OIDEncryptionAlgorithmECDSAP384), oid.Equal(pkcs7.OIDEncryptionAlgorithmECDSAP521),
		oid.Equal(pkcs7.OIDDigestAlgorithmECDSASHA1), oid.Equal(pkcs7.OIDDigestAlgorithmECDSASHA256), oid.Equal(pkcs7.OIDDigestAlgorithmECDSASHA384), oid.Equal(pkcs7.OIDDigestAlgorithmECDSASHA512):
		return ecdsaByHash[h], nil
	}
	return 0, fmt.Errorf("%w: signature %v", ErrAlgorithm, oid)
}

// Fingerprint is the lower-case hex SHA-256 of the certificate's DER, the
// value stored for identity pinning.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}
