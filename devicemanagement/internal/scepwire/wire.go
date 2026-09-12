// Package scepwire builds SCEP CMS messages with per-message algorithms.
// The upstream encoder's process-global defaults are DES and SHA-1; changing
// those globals would affect unrelated callers and race with their requests.
package scepwire

import (
	"bytes"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"

	"github.com/smallstep/pkcs7"
	smallscep "github.com/smallstep/scep"
)

// ErrWire indicates an unsupported or malformed CMS message.
var ErrWire = errors.New("scep: invalid CMS message")

type contentInfo struct {
	Type    asn1.ObjectIdentifier
	Content asn1.RawValue `asn1:"explicit,tag:0"`
}

type issuerSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

type recipientInfo struct {
	Version      int
	IssuerSerial issuerSerial
	Algorithm    pkix.AlgorithmIdentifier
	Key          []byte
}

type encryptedContent struct {
	Type      asn1.ObjectIdentifier
	Algorithm pkix.AlgorithmIdentifier
	Content   asn1.RawValue `asn1:"tag:0,optional"`
}

type envelope struct {
	Version    int
	Recipients []recipientInfo `asn1:"set"`
	Encrypted  encryptedContent
}

func attribute(n int, value any) pkcs7.Attribute {
	return pkcs7.Attribute{
		Type:  asn1.ObjectIdentifier{2, 16, 840, 1, 113733, 1, 9, n},
		Value: value,
	}
}

func sign(
	content []byte,
	cert *x509.Certificate,
	key crypto.PrivateKey,
	attrs []pkcs7.Attribute,
) ([]byte, error) {
	signed, err := pkcs7.NewSignedData(content)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWire, err)
	}
	signed.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := signed.AddSigner(
		cert,
		key,
		pkcs7.SignerInfoConfig{ExtraSignedAttributes: attrs},
	); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWire, err)
	}
	out, err := signed.Finish()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWire, err)
	}
	return out, nil
}

// encrypt implements CMS EnvelopedData using mandatory SCEP AES-128-CBC and
// RSA key transport (RFC 8894 sections 2.9 and 3.2.2). The outer signature
// authenticates the ciphertext before any decryption occurs.
func encrypt(content []byte, recipients []*x509.Certificate) ([]byte, error) {
	key, iv := make([]byte, 16), make([]byte, aes.BlockSize)
	_, _ = rand.Read(key) // Go 1.24+ guarantees rand.Read succeeds.
	_, _ = rand.Read(iv)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWire, err)
	}
	padding := aes.BlockSize - len(content)%aes.BlockSize
	padded := append(bytes.Clone(content), bytes.Repeat([]byte{byte(padding)}, padding)...)
	// #nosec G407 -- a fresh random IV is generated for every message.
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(padded, padded)
	inner := envelope{Encrypted: encryptedContent{
		Type: pkcs7.OIDData,
		Algorithm: pkix.AlgorithmIdentifier{
			Algorithm:  pkcs7.OIDEncryptionAlgorithmAES128CBC,
			Parameters: asn1.RawValue{Tag: asn1.TagOctetString, Bytes: iv},
		},
		Content: asn1.RawValue{Class: 2, Tag: 0, Bytes: padded},
	}}
	for _, cert := range recipients {
		if cert == nil {
			return nil, ErrWire
		}
		pub, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok || pub.N.BitLen() < 2048 {
			return nil, fmt.Errorf("%w: recipient requires RSA >= 2048 bits", ErrWire)
		}
		// #nosec G401 -- SCEP CMS RSA key transport interoperability (RFC 8894).
		//nolint:staticcheck // SA1019: required for existing SCEP RSA key transport peers.
		wrapped, err := rsa.EncryptPKCS1v15(
			rand.Reader,
			pub,
			key,
		)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrWire, err)
		}
		inner.Recipients = append(inner.Recipients, recipientInfo{
			IssuerSerial: issuerSerial{
				Issuer: asn1.RawValue{FullBytes: cert.RawIssuer},
				Serial: cert.SerialNumber,
			},
			Algorithm: pkix.AlgorithmIdentifier{
				Algorithm:  pkcs7.OIDEncryptionAlgorithmRSA,
				Parameters: asn1.NullRawValue,
			},
			Key: wrapped,
		})
	}
	if len(inner.Recipients) == 0 {
		return nil, ErrWire
	}
	der, err := asn1.Marshal(inner)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWire, err)
	}
	out, err := asn1.Marshal(
		contentInfo{
			Type:    pkcs7.OIDEnvelopedData,
			Content: asn1.RawValue{Class: 2, Tag: 0, IsCompound: true, Bytes: der},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWire, err)
	}
	return out, nil
}

// CheckEnvelope rejects single DES and unsupported content before decryption.
func CheckEnvelope(raw []byte) error {
	var outer contentInfo
	rest, err := asn1.Unmarshal(raw, &outer)
	if err != nil || len(rest) != 0 || !outer.Type.Equal(pkcs7.OIDEnvelopedData) {
		return ErrWire
	}
	var inner envelope
	rest, err = asn1.Unmarshal(outer.Content.Bytes, &inner)
	if err != nil || len(rest) != 0 || !inner.Encrypted.Type.Equal(pkcs7.OIDData) {
		return ErrWire
	}
	algorithm := inner.Encrypted.Algorithm.Algorithm
	for _, allowed := range []asn1.ObjectIdentifier{pkcs7.OIDEncryptionAlgorithmAES128CBC, pkcs7.OIDEncryptionAlgorithmAES256CBC, pkcs7.OIDEncryptionAlgorithmAES128GCM, pkcs7.OIDEncryptionAlgorithmAES256GCM} {
		if algorithm.Equal(allowed) {
			return nil
		}
	}
	return fmt.Errorf("%w: content encryption must use AES", ErrWire)
}

// Request builds a PKCSReq or RenewalReq with fresh transaction attributes.
func Request(
	csr *x509.CertificateRequest,
	template *smallscep.PKIMessage,
) (*smallscep.PKIMessage, error) {
	content, err := encrypt(csr.Raw, template.Recipients)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(csr.RawSubjectPublicKeyInfo)
	msg := &smallscep.PKIMessage{
		MessageType:   template.MessageType,
		TransactionID: smallscep.TransactionID(hex.EncodeToString(sum[:])),
		SenderNonce:   make([]byte, 16),
	}
	_, _ = rand.Read(msg.SenderNonce)
	msg.Raw, err = sign(content, template.SignerCert, template.SignerKey, []pkcs7.Attribute{
		attribute(
			7,
			msg.TransactionID,
		),
		attribute(2, msg.MessageType),
		attribute(5, msg.SenderNonce),
	})
	return msg, err
}

// Reply signs a CertRep. A nil issued certificate denotes FAILURE.
func Reply(
	request *smallscep.PKIMessage,
	authority *x509.Certificate,
	key crypto.Signer,
	recipient, issued *x509.Certificate,
	failure smallscep.FailInfo,
) ([]byte, error) {
	status := smallscep.FAILURE
	var content []byte
	if issued != nil {
		status = smallscep.SUCCESS
		der, err := smallscep.DegenerateCertificates([]*x509.Certificate{issued})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrWire, err)
		}
		content, err = encrypt(der, []*x509.Certificate{recipient})
		if err != nil {
			return nil, err
		}
	}
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	attrs := []pkcs7.Attribute{
		attribute(7, request.TransactionID),
		attribute(2, smallscep.CertRep),
		attribute(3, status),
		attribute(5, nonce),
		attribute(6, request.SenderNonce),
	}
	if issued == nil {
		attrs = append(attrs, attribute(4, failure))
	}
	return sign(content, authority, key, attrs)
}
