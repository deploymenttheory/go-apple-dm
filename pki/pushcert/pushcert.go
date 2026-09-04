package pushcert

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Errors returned by this package.
var (
	ErrInvalid     = errors.New("pushcert: invalid certificate or key")
	ErrNoTopic     = errors.New("pushcert: no APNs topic in certificate subject")
	ErrKeyMismatch = errors.New("pushcert: private key does not match certificate")
)

// TopicPrefix is the prefix every MDM push topic starts with.
const TopicPrefix = "com.apple.mgmt"

// oidUserID is the subject UID attribute that carries the topic.
var oidUserID = asn1.ObjectIdentifier{0, 9, 2342, 19200300, 100, 1, 1}

// Parsed is a validated certificate and key pair.
type Parsed struct {
	// TLS is ready to use as a client certificate: Leaf is set and
	// Certificate[0] is the leaf DER, followed by any chain certificates.
	TLS tls.Certificate
	// Leaf is the push certificate itself.
	Leaf *x509.Certificate
	// Topic is the APNs topic from the leaf's subject UID.
	Topic string
	// NotBefore and NotAfter are copied from the leaf.
	NotBefore, NotAfter time.Time
}

// Parse decodes a PEM certificate and a PEM private key, checks that the
// key matches the leaf's public key, and derives the topic from the leaf's
// subject UID. The first CERTIFICATE block is the leaf; further CERTIFICATE
// blocks are kept as the chain. The key may be PKCS#1 RSA, PKCS#8, or SEC 1
// EC; encrypted keys are rejected. Errors wrap ErrInvalid, ErrKeyMismatch,
// or ErrNoTopic.
func Parse(certPEM, keyPEM []byte) (Parsed, error) {
	chain, err := decodeCertificates(certPEM)
	if err != nil {
		return Parsed{}, err
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return Parsed{}, fmt.Errorf("%w: leaf: %w", ErrInvalid, err)
	}
	key, err := decodeKey(keyPEM)
	if err != nil {
		return Parsed{}, err
	}
	pub, ok := leaf.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !pub.Equal(key.Public()) {
		return Parsed{}, ErrKeyMismatch
	}
	topic, err := TopicFromCert(leaf)
	if err != nil {
		return Parsed{}, err
	}
	return Parsed{
		TLS:       tls.Certificate{Certificate: chain, PrivateKey: key, Leaf: leaf},
		Leaf:      leaf,
		Topic:     topic,
		NotBefore: leaf.NotBefore,
		NotAfter:  leaf.NotAfter,
	}, nil
}

// TopicFromCert returns the topic in cert's subject UID. It returns
// ErrNoTopic when the attribute is absent or does not start with
// TopicPrefix.
func TopicFromCert(cert *x509.Certificate) (string, error) {
	if cert == nil {
		return "", ErrNoTopic
	}
	for _, n := range cert.Subject.Names {
		if !n.Type.Equal(oidUserID) {
			continue
		}
		s, ok := n.Value.(string)
		if !ok || s == "" {
			continue
		}
		if !strings.HasPrefix(s, TopicPrefix) {
			return "", fmt.Errorf("%w: UID %q does not start with %s", ErrNoTopic, s, TopicPrefix)
		}
		return s, nil
	}
	return "", ErrNoTopic
}

// decodeCertificates returns the DER of every CERTIFICATE block in p. The
// first block must be a CERTIFICATE.
func decodeCertificates(p []byte) ([][]byte, error) {
	block, rest := pem.Decode(p)
	if block == nil {
		return nil, fmt.Errorf("%w: no PEM block in certificate", ErrInvalid)
	}
	if block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%w: first PEM block is %q, want CERTIFICATE", ErrInvalid, block.Type)
	}
	chain := [][]byte{block.Bytes}
	for {
		block, rest = pem.Decode(rest)
		if block == nil {
			return chain, nil
		}
		if block.Type == "CERTIFICATE" {
			chain = append(chain, block.Bytes)
		}
	}
}

// signer is the part of a private key needed to pair it with a certificate.
type signer interface {
	Public() crypto.PublicKey
}

// decodeKey parses the first PEM block of p as an unencrypted private key.
func decodeKey(p []byte) (signer, error) {
	block, _ := pem.Decode(p)
	if block == nil {
		return nil, fmt.Errorf("%w: no PEM block in key", ErrInvalid)
	}
	if _, encrypted := block.Headers["Proc-Type"]; encrypted || strings.Contains(block.Type, "ENCRYPTED") {
		return nil, fmt.Errorf("%w: encrypted private keys are not supported", ErrInvalid)
	}
	var (
		key any
		err error
	)
	switch block.Type {
	case "RSA PRIVATE KEY":
		key, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("%w: unsupported key PEM block %q", ErrInvalid, block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: key: %w", ErrInvalid, err)
	}
	s, ok := key.(signer)
	if !ok {
		return nil, fmt.Errorf("%w: key type %T has no public key", ErrInvalid, key)
	}
	return s, nil
}
