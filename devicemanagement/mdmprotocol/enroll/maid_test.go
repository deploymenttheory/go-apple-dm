package enroll_test

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
)

type badSigner struct{ crypto.Signer }

func (badSigner) Sign(io.Reader, []byte, crypto.SignerOpts) ([]byte, error) {
	return nil, io.ErrClosedPipe
}

func TestMAIDToken(t *testing.T) {
	ca, err := testpki.NewCA("ADE server")
	if err != nil {
		t.Fatal(err)
	}
	other, err := testpki.NewCA("other")
	if err != nil {
		t.Fatal(err)
	}
	issuer := uuid.NewV4().String()
	now := time.Unix(1700000000, 0)
	seen := map[string]bool{}
	for range 2 {
		token, err := enroll.MAIDToken(ca.Cert, ca.Key, issuer, now)
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.Split(string(token), ".")
		if len(parts) != 3 {
			t.Fatal("JWT shape")
		}
		h, _ := base64.RawURLEncoding.DecodeString(parts[0])
		if string(h) != `{"alg":"RS256","typ":"JWT"}` {
			t.Fatal(string(h))
		}
		body, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims map[string]any
		if err := json.Unmarshal(body, &claims); err != nil {
			t.Fatal(err)
		}
		if len(claims) != 4 || claims["iss"] != issuer || claims["iat"] != float64(now.Unix()) || claims["service_type"] != "com.apple.maid" {
			t.Fatal(claims)
		}
		id, _ := claims["jti"].(string)
		if _, err := uuid.Parse(id); err != nil || seen[id] {
			t.Fatal("jti reused/invalid")
		}
		seen[id] = true
		digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		if err := rsa.VerifyPKCS1v15(ca.Key.Public().(*rsa.PublicKey), crypto.SHA256, digest[:], sig); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []crypto.Signer{nil, other.Key, badSigner{ca.Key}} {
		if _, err := enroll.MAIDToken(ca.Cert, key, issuer, now); !errors.Is(err, enroll.ErrMAIDToken) {
			t.Fatal(err)
		}
	}
	if _, err := enroll.MAIDToken(ca.Cert, ca.Key, "invalid", now); !errors.Is(err, enroll.ErrMAIDToken) {
		t.Fatal(err)
	}
	if _, err := enroll.MAIDToken(ca.Cert, ca.Key, issuer, time.Time{}); !errors.Is(err, enroll.ErrMAIDToken) {
		t.Fatal(err)
	}
}
