package cms

import (
	"bytes"
	"crypto"
	"crypto/subtle"
	"crypto/x509"
	"encoding/asn1"
	"fmt"

	"github.com/smallstep/pkcs7"
)

// SignAttached produces a CMS SignedData with the content embedded, which
// is what signed configuration profiles are (a .mobileconfig whose bytes are
// the DER structure).
func SignAttached(content []byte, cert *x509.Certificate, key crypto.Signer) ([]byte, error) {
	return signedData(content, cert, key, false)
}

// VerifyAttached checks an attached signature and returns the embedded
// content and the signer certificate. The same trust and skew options as
// Verify apply.
func VerifyAttached(der []byte, o VerifyOptions) ([]byte, *x509.Certificate, error) {
	p7, err := pkcs7.Parse(der)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrParse, err)
	}
	if len(p7.Content) == 0 {
		return nil, nil, fmt.Errorf("%w: no embedded content", ErrParse)
	}
	content := append([]byte(nil), p7.Content...)
	signer, err := Verify(der, content, o)
	if err != nil {
		return nil, nil, err
	}
	return content, signer, nil
}

// IsSigned reports whether data looks like a DER CMS structure rather than
// a plain plist, so callers can accept both signed and unsigned profiles.
func IsSigned(data []byte) bool {
	return len(data) > 2 && data[0] == 0x30
}

// VerifyAttachedOptions control VerifyAttachedWith.
type VerifyAttachedOptions struct {
	VerifyOptions
	// IgnoreValidity builds the certificate path by name and signature
	// alone, without applying validity windows. Apple device identities
	// chain through the Apple iPhone Device CA, which expired in 2014 and
	// still issues current leaves, so stock chain verification cannot
	// accept them. SHA-1 signatures are tolerated on this path because the
	// chain uses them.
	IgnoreValidity bool
	// Anchors are trust anchors matched by identity: the path is accepted
	// when it reaches a certificate issued (by name and signature) by one
	// of them, or one of them itself. They are the trust store for
	// IgnoreValidity, since a CertPool cannot be walked; when
	// IgnoreValidity is false they are added to Roots. Nil Anchors and nil
	// Roots skip chain verification, as in Verify.
	Anchors []*x509.Certificate
}

// VerifyAttachedWith verifies an attached SignedData the way Apple device
// identities sign MachineInfo: exactly one signer whose certificate is in
// the bundle; when authenticated attributes are present the signature
// covers their DER SET and the messageDigest attribute must equal the
// digest of the content while contentType must be id-data, otherwise the
// signature covers the content; digest and signature algorithms are taken
// from the SignerInfo. It returns the embedded content and the signer.
func VerifyAttachedWith(der []byte, o VerifyAttachedOptions) ([]byte, *x509.Certificate, error) {
	p7, err := pkcs7.Parse(der)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrParse, err)
	}
	if len(p7.Content) == 0 {
		return nil, nil, fmt.Errorf("%w: no embedded content", ErrParse)
	}
	switch len(p7.Signers) {
	case 0:
		return nil, nil, ErrNoSigner
	case 1:
	default:
		return nil, nil, fmt.Errorf("%w: %d", ErrMultipleSigners, len(p7.Signers))
	}
	signer := p7.GetOnlySigner()
	if signer == nil {
		return nil, nil, fmt.Errorf("%w: signer certificate not in bundle", ErrNoSigner)
	}
	content := append([]byte(nil), p7.Content...)
	si := p7.Signers[0]
	h, err := hashFor(si.DigestAlgorithm.Algorithm)
	if err != nil {
		return nil, nil, err
	}
	signed := content
	var attrs []rawAttribute
	for _, a := range si.AuthenticatedAttributes {
		attrs = append(attrs, rawAttribute{Type: a.Type, Value: a.Value})
	}
	if len(attrs) > 0 {
		if signed, err = signedAttributes(attrs, h, content); err != nil {
			return nil, nil, err
		}
	}
	alg, err := signatureAlgorithm(si.DigestEncryptionAlgorithm.Algorithm, h)
	if err != nil {
		return nil, nil, err
	}
	if err := signer.CheckSignature(alg, signed, si.EncryptedDigest); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrSignature, err)
	}
	if o.IgnoreValidity {
		err = verifyPath(signer, p7.Certificates, o.Anchors)
	} else {
		err = verifyChain(p7, signer, rootsWith(o.Roots, o.Anchors), o.now())
	}
	if err != nil {
		return nil, nil, err
	}
	return content, signer, nil
}

// signedAttributes checks the messageDigest and contentType attributes
// (RFC 5652 section 11) and returns the DER SET the signature covers.
func signedAttributes(attrs []rawAttribute, h crypto.Hash, content []byte) ([]byte, error) {
	var (
		digest      []byte
		contentType asn1.ObjectIdentifier
	)
	for _, a := range attrs {
		switch {
		case a.Type.Equal(pkcs7.OIDAttributeMessageDigest):
			if digest != nil {
				return nil, fmt.Errorf("%w: duplicate messageDigest attribute", ErrSignature)
			}
			if _, err := asn1.Unmarshal(a.Value.Bytes, &digest); err != nil {
				return nil, fmt.Errorf("%w: messageDigest attribute: %w", ErrSignature, err)
			}
		case a.Type.Equal(pkcs7.OIDAttributeContentType):
			if contentType != nil {
				return nil, fmt.Errorf("%w: duplicate contentType attribute", ErrSignature)
			}
			if _, err := asn1.Unmarshal(a.Value.Bytes, &contentType); err != nil {
				return nil, fmt.Errorf("%w: contentType attribute: %w", ErrSignature, err)
			}
		}
	}
	if digest == nil {
		return nil, fmt.Errorf("%w: no messageDigest attribute", ErrSignature)
	}
	if contentType == nil {
		return nil, fmt.Errorf("%w: no contentType attribute", ErrSignature)
	}
	if !contentType.Equal(pkcs7.OIDData) {
		return nil, fmt.Errorf("%w: contentType %v is not data", ErrSignature, contentType)
	}
	hasher := h.New()
	hasher.Write(content)
	if subtle.ConstantTimeCompare(digest, hasher.Sum(nil)) != 1 {
		return nil, fmt.Errorf("%w: message digest mismatch", ErrSignature)
	}
	signed, err := marshalAttributes(attrs)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSignature, err)
	}
	return signed, nil
}

// verifyPath walks from the signer through the bundle by issuer name and
// signature until it reaches an anchor or a certificate an anchor issued.
// Validity windows are not applied. Nil anchors skip the walk.
func verifyPath(signer *x509.Certificate, bundle, anchors []*x509.Certificate) error {
	if len(anchors) == 0 {
		return nil
	}
	cert := signer
	for range len(bundle) + 1 {
		for _, a := range anchors {
			if cert.Equal(a) || (bytes.Equal(cert.RawIssuer, a.RawSubject) && checkIssued(cert, a) == nil) {
				return nil
			}
		}
		var next *x509.Certificate
		for _, c := range bundle {
			if c.Equal(cert) || !bytes.Equal(cert.RawIssuer, c.RawSubject) || !c.BasicConstraintsValid || !c.IsCA {
				continue
			}
			if checkIssued(cert, c) == nil {
				next = c
				break
			}
		}
		if next == nil {
			return fmt.Errorf("%w: no path from %q to a trust anchor", ErrChain, cert.Subject.CommonName)
		}
		cert = next
	}
	return fmt.Errorf("%w: certificate path is a cycle", ErrChain)
}

// checkIssued verifies cert's signature with parent's key. It goes through
// CheckSignature rather than CheckSignatureFrom because the latter rejects
// the SHA-1 signatures Apple's device chain carries.
func checkIssued(cert, parent *x509.Certificate) error {
	return parent.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature)
}

// rootsWith returns a pool holding roots and anchors, or nil when both are
// empty.
func rootsWith(roots *x509.CertPool, anchors []*x509.Certificate) *x509.CertPool {
	if len(anchors) == 0 {
		return roots
	}
	pool := x509.NewCertPool()
	if roots != nil {
		pool = roots.Clone()
	}
	for _, a := range anchors {
		pool.AddCert(a)
	}
	return pool
}
