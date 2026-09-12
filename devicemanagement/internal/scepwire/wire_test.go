package scepwire

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/smallstep/pkcs7"
	smallscep "github.com/smallstep/scep"
)

func TestWireRejectsInvalidRecipientsAndEnvelopes(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pattern := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "recipient"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	der, err := x509.CreateCertificate(rand.Reader, pattern, pattern, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"nil", "empty", "bad-rsa", "missing-serial"} {
		c := *cert
		recipients := []*x509.Certificate{&c}
		switch mode {
		case "nil":
			recipients[0] = nil
		case "empty":
			recipients = nil
		case "bad-rsa":
			pub := key.PublicKey
			pub.E = -1
			c.PublicKey = &pub
		case "missing-serial":
			c.SerialNumber = nil
		}
		if _, err := encrypt([]byte("credential"), recipients); !errors.Is(err, ErrWire) {
			t.Fatal(mode, err)
		}
	}
	good, err := encrypt([]byte("credential"), []*x509.Certificate{cert})
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckEnvelope(good); err != nil {
		t.Fatal(err)
	}
	for _, raw := range [][]byte{nil, []byte("invalid"), append(append([]byte(nil), good...), 0)} {
		if err := CheckEnvelope(raw); !errors.Is(err, ErrWire) {
			t.Fatal("malformed envelope accepted", err)
		}
	}
	badInner, err := asn1.Marshal(
		contentInfo{
			Type:    pkcs7.OIDEnvelopedData,
			Content: asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: []byte{0x30, 0}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckEnvelope(badInner); !errors.Is(err, ErrWire) {
		t.Fatal("malformed inner envelope", err)
	}
	if _, err := sign(nil, cert, struct{}{}, nil); !errors.Is(err, ErrWire) {
		t.Fatal("unsupported signing key", err)
	}
	missingSerial := *cert
	missingSerial.SerialNumber = nil
	if _, err := sign(nil, &missingSerial, key, nil); !errors.Is(err, ErrWire) {
		t.Fatal("missing signer serial", err)
	}
	request := &smallscep.PKIMessage{TransactionID: "transaction", SenderNonce: make([]byte, 16)}
	if _, err := Reply(
		request,
		cert,
		key,
		nil,
		cert,
		smallscep.BadRequest,
	); !errors.Is(
		err,
		ErrWire,
	) {
		t.Fatal("reply without recipient", err)
	}
	if _, err := Request(
		&x509.CertificateRequest{},
		&smallscep.PKIMessage{},
	); !errors.Is(
		err,
		ErrWire,
	) {
		t.Fatal("request without recipients", err)
	}
}
