package cms

import (
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/smallstep/pkcs7"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
)

// Envelope errors do not include ciphertext, key material or plaintext.
var (
	ErrRecipient = fault.NewOperator(fault.Internal, "the recipient certificate or key is not valid")
	ErrDecrypt   = fault.NewOperator(fault.Internal, "the envelope could not be decrypted")
)

// DecryptEnvelope decrypts CMS EnvelopedData (BER or DER), including FileVault's
// EncryptedNewRecoveryKey. cert must be the ReplyEncryptionCertificate used for
// the command; key may be an external RSA crypto.Decrypter. Retain both until
// delayed command responses have been handled. No certificate expiry check is
// made: expiration does not prevent decrypting a previously encrypted response.
// Supported algorithms are those of the existing smallstep/pkcs7 decoder.
// Decryption provides no sender authentication; authenticate the MDM response.
func DecryptEnvelope(data []byte, cert *x509.Certificate, key crypto.Decrypter) ([]byte, error) {
	if cert == nil || key == nil {
		return nil, ErrRecipient
	}
	pub, ok := key.Public().(*rsa.PublicKey)
	if !ok || pub == nil || !pub.Equal(cert.PublicKey) {
		return nil, ErrRecipient
	}
	p7, err := pkcs7.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid encrypted envelope", ErrParse)
	}
	plain, err := p7.Decrypt(cert, key)
	if err != nil {
		if errors.Is(err, pkcs7.ErrUnsupportedAlgorithm) ||
			errors.Is(err, pkcs7.ErrUnsupportedAsymmetricEncryptionAlgorithm) {
			return nil, ErrAlgorithm
		}
		return nil, ErrDecrypt
	}
	return plain, nil
}
