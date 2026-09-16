package cms_test

import (
	"crypto"
	"crypto/x509"
	"errors"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
)

var errMissingRotationResult = errors.New("missing encrypted recovery key")

// readRotatedKey is called after authenticating/decoding the MDM response.
// Load the original reply certificate/key by CommandUUID, even if the active
// reply key has since rotated. Store the recovered bytes in your secret store.
func readRotatedKey(response *commands.RotateFileVaultKeyResponse, cert *x509.Certificate, key crypto.Decrypter) ([]byte, error) {
	if response == nil || response.RotateResult == nil || len(response.RotateResult.EncryptedNewRecoveryKey) == 0 {
		return nil, errMissingRotationResult
	}
	return cms.DecryptEnvelope(response.RotateResult.EncryptedNewRecoveryKey, cert, key)
}

func ExampleDecryptEnvelope() {
	// For the outgoing RotateFileVaultKey command, set
	// ReplyEncryptionCertificate to cert.Raw. Persist that certificate and its
	// private-key reference with the command before enqueueing it. A later
	// response is handled by readRotatedKey, not by selecting today's active key.
	_ = readRotatedKey
}
