package enroll

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"time"
	"uuid"
)

// MAIDService is the GetToken service for Managed Apple Accounts.
const MAIDService = "com.apple.maid"

// ErrMAIDToken indicates invalid credentials, claims or a signing failure.
var ErrMAIDToken = errors.New("enroll: cannot issue Managed Apple Account token")

// MAIDToken signs Apple's GetToken JWT with the certificate registered for the
// ADE server and its matching RSA key (including external crypto.Signers).
// serverUUID is that server's UUID, not a device or enrollment identifier.
// The caller authenticates the check-in and supplies the current issuance time.
// A fresh random jti is generated for every call. Certificate registration and
// Apple acceptance cannot be established locally.
//
// https://developer.apple.com/documentation/devicemanagement/get-token
func MAIDToken(
	cert *x509.Certificate,
	key crypto.Signer,
	serverUUID string,
	issuedAt time.Time,
) ([]byte, error) {
	if cert == nil || key == nil || issuedAt.IsZero() || issuedAt.Unix() < 0 {
		return nil, fmt.Errorf("%w: certificate, key and issuance time required", ErrMAIDToken)
	}
	if _, err := uuid.Parse(serverUUID); err != nil {
		return nil, fmt.Errorf("%w: invalid server UUID", ErrMAIDToken)
	}
	pub, ok := key.Public().(*rsa.PublicKey)
	if !ok || pub == nil || pub.N.BitLen() < 2048 || !pub.Equal(cert.PublicKey) {
		return nil, fmt.Errorf(
			"%w: matching RSA certificate and key of at least 2048 bits required",
			ErrMAIDToken,
		)
	}
	claims := struct {
		Issuer   string `json:"iss"`
		IssuedAt int64  `json:"iat"`
		ID       string `json:"jti"`
		Service  string `json:"service_type"`
	}{serverUUID, issuedAt.Unix(), uuid.NewV4().String(), MAIDService}
	data, err := json.Marshal(claims)
	if err != nil {
		return nil, fmt.Errorf("%w: encode claims: %w", ErrMAIDToken, err)
	}
	input := base64.RawURLEncoding.EncodeToString(
		[]byte(`{"alg":"RS256","typ":"JWT"}`),
	) + "." + base64.RawURLEncoding.EncodeToString(
		data,
	)
	digest := sha256.Sum256([]byte(input))
	sig, err := key.Sign(rand.Reader, digest[:], crypto.SHA256)
	if err != nil {
		// Signer errors may include vendor diagnostics; don't expose credentials.
		return nil, fmt.Errorf("%w: RSA signing failed", ErrMAIDToken)
	}
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err != nil {
		return nil, fmt.Errorf("%w: signer returned an invalid RS256 signature", ErrMAIDToken)
	}
	return []byte(input + "." + base64.RawURLEncoding.EncodeToString(sig)), nil
}
