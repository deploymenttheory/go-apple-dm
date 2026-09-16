package service_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
	"uuid"

	enrollprotocol "github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/checkin"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// The caller owns this issuer. A generated certificate models the signing
// certificate contract; the test makes no claim that it is registered with Apple.
func TestManagedAppleAccountRS256TokenContract(t *testing.T) {
	t.Parallel()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: t0.Add(-time.Hour), NotAfter: t0.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	issuer := uuid.NewV4().String()
	tokenBytes, err := enrollprotocol.MAIDToken(cert, key, issuer, t0)
	if err != nil {
		t.Fatal(err)
	}
	token := string(tokenBytes)
	claimBytes, err := base64.RawURLEncoding.DecodeString(strings.Split(token, ".")[1])
	if err != nil {
		t.Fatal(err)
	}
	h := newHarness(t, service.Config{GetToken: func(_ context.Context, _ *mdm.Request, m *checkin.GetToken) (*checkin.GetTokenResponse, error) {
		if m.TokenServiceType != "com.apple.maid" {
			t.Fatal(m.TokenServiceType)
		}
		return &checkin.GetTokenResponse{TokenData: []byte(token)}, nil
	}})
	enroll(t, h, "D1")
	// #nosec G101 -- Synthetic protocol fixtures and invalid URLs; no live credentials.
	res, err := h.core.Checkin(t.Context(), req(h.cert), simple(t, "GetToken", "D1", map[string]any{"TokenServiceType": "com.apple.maid"}))
	if err != nil {
		t.Fatal(err)
	}
	var got checkin.GetTokenResponse
	if err := plist.Unmarshal(res.Body, &got); err != nil {
		t.Fatal(err)
	}
	if string(got.TokenData) != token {
		t.Fatal("JWT changed during response transport")
	}
	parts := strings.Split(string(got.TokenData), ".")
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var header map[string]string
	if err := json.Unmarshal(headerBytes, &header); err != nil || header["alg"] != "RS256" {
		t.Fatal(header, err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || string(payload) != string(claimBytes) {
		t.Fatal("claims changed", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	actual := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	publicKey, ok := cert.PublicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatal("not RSA")
	}
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, actual[:], signature); err != nil {
		t.Fatal(err)
	}
	signature[0] ^= 1
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, actual[:], signature); err == nil {
		t.Fatal("tampered signature verified")
	}
}
