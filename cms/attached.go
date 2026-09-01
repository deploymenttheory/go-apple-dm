package cms

import (
	"crypto"
	"crypto/x509"
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
