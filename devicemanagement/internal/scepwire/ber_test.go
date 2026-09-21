package scepwire

import (
	"bytes"
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
)

// Constructed values use indefinite BER lengths, as Apple's SecSCEP encoder
// does. Ciphertext uses constructed OCTET STRING chunks as permitted by CMS.
func streamingBER(t *testing.T, raw []byte) []byte {
	t.Helper()
	var v asn1.RawValue
	rest, err := asn1.Unmarshal(raw, &v)
	if err != nil || len(rest) != 0 {
		t.Fatal("invalid fixture DER", err)
	}
	if !v.IsCompound {
		if v.Class == 2 && v.Tag == 0 {
			a, err := asn1.Marshal(v.Bytes[:len(v.Bytes)/2])
			if err != nil {
				t.Fatal(err)
			}
			b, err := asn1.Marshal(v.Bytes[len(v.Bytes)/2:])
			if err != nil {
				t.Fatal(err)
			}
			return append(append(append([]byte{0xa0, 0x80}, a...), b...), 0, 0)
		}
		return raw
	}
	out := []byte{raw[0], 0x80}
	for remaining := v.Bytes; len(remaining) > 0; {
		var child asn1.RawValue
		remaining, err = asn1.Unmarshal(remaining, &child)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, streamingBER(t, child.FullBytes)...)
	}
	return append(out, 0, 0)
}

// TestCheckEnvelopeAcceptsAuthenticatedStreamingBER checks that check envelope accepts
// authenticated streaming BER.
func TestCheckEnvelopeAcceptsAuthenticatedStreamingBER(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synthetic BER recipient"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("synthetic CSR content")
	encrypted, err := encrypt(want, []*x509.Certificate{cert})
	if err != nil {
		t.Fatal(err)
	}
	ber := streamingBER(t, encrypted)
	parsed, err := pkcs7.Parse(ber)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parsed.Decrypt(cert, key)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatal("upstream cannot decrypt streaming fixture", err)
	}
	if err := CheckEnvelope(ber); err != nil {
		t.Fatal("valid CMS BER rejected before decryption", err)
	}
	for _, raw := range [][]byte{ber[:len(ber)-1], append(bytes.Clone(ber), 0), append(bytes.Clone(ber), encrypted...)} {
		if err := CheckEnvelope(raw); !errors.Is(err, ErrWire) {
			t.Fatal("malformed BER accepted", err)
		}
	}
	var outer contentInfo
	if _, err := asn1.Unmarshal(encrypted, &outer); err != nil {
		t.Fatal(err)
	}
	var inner envelope
	if _, err := asn1.Unmarshal(outer.Content.Bytes, &inner); err != nil {
		t.Fatal(err)
	}
	inner.Encrypted.Algorithm.Algorithm = pkcs7.OIDEncryptionAlgorithmDESCBC
	innerDER, err := asn1.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	outer.Content = asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: innerDER}
	weak, err := asn1.Marshal(outer)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckEnvelope(streamingBER(t, weak)); !errors.Is(err, ErrWire) {
		t.Fatal("BER bypassed AES policy", err)
	}
}

// TestEnvelopeBERBoundsAndFraming checks envelope BER bounds and framing.
func TestEnvelopeBERBoundsAndFraming(t *testing.T) {
	for _, raw := range [][]byte{
		nil,
		{0x30},
		{0, 0},
		{0x04, 0x80, 0, 0}, // no indefinite primitive values
		{0x30, 0x80},
		{0x30, 0x80, 0},
		{0x30, 0x02, 0, 0}, // missing or misplaced EOC
		{0x30, 0x81},
		{0x30, 0x85, 0, 0, 0, 0, 0},
		{0x30, 0x81, 0xff},
		{0x30, 0x03, 0x02, 0x02, 0},
		{0x30, 0x01, 0x04}, // child crosses enclosing length
		{0x1f},
		{0x1f, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0},
		{0x1f, 0x00, 0x00}, // nonminimal high tag
		bytes.Repeat([]byte{0}, maxEnvelopeBytes+1),
	} {
		if _, err := envelopeDER(raw); !errors.Is(err, ErrWire) {
			t.Fatalf("malformed BER framing accepted: %x (%v)", raw[:min(len(raw), 16)], err)
		}
	}
	for _, raw := range [][]byte{
		{0x30, 0},
		{0x30, 0x80, 0, 0},
		{0x30, 0x81, 0},
		{0x30, 0x80, 0x04, 0x03, 0, 0, 1, 0, 0}, // EOC-like bytes inside primitive content
		{0x9f, 0x20, 0x01, 0},                   // valid high tag number
	} {
		original := bytes.Clone(raw)
		der, err := envelopeDER(raw)
		if err != nil {
			t.Fatal("valid BER framing rejected", err)
		}
		var value asn1.RawValue
		if rest, err := asn1.Unmarshal(der, &value); err != nil || len(rest) != 0 {
			t.Fatal("normalized value is not definite ASN.1", err)
		}
		if !bytes.Equal(raw, original) {
			t.Fatal("normalization modified authenticated bytes")
		}
	}
	deep := []byte{0x04, 0}
	for range maxBERDepth + 2 {
		deep = append(append([]byte{0x30, 0x80}, deep...), 0, 0)
	}
	if _, err := envelopeDER(deep); !errors.Is(err, ErrWire) {
		t.Fatal("unbounded nesting accepted", err)
	}
}

// FuzzEnvelopeBER checks that BER envelope validation never modifies the signed input bytes.
func FuzzEnvelopeBER(f *testing.F) {
	for _, raw := range [][]byte{nil, {0x30, 0}, {0x30, 0x80, 0, 0}, {0x30, 0x80, 0x04, 0x03, 0, 0, 1, 0, 0}} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		original := bytes.Clone(raw)
		_ = CheckEnvelope(raw)
		if !bytes.Equal(raw, original) {
			t.Fatal("envelope check modified signed bytes")
		}
	})
}
