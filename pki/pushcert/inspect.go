package pushcert

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"slices"
	"time"
)

// Info contains public metadata only. Capabilities maps each topic to Apple's
// certificate service names (topic, voip, complication, and so on).
type Info struct {
	Subject      string              `json:"subject"`
	Issuer       string              `json:"issuer"`
	Topic        string              `json:"topic"`
	MDM          bool                `json:"mdm"`
	NotBefore    time.Time           `json:"notBefore"`
	NotAfter     time.Time           `json:"notAfter"`
	Capabilities map[string][]string `json:"capabilities"`
}

// Inspect reads a PEM chain or a DER certificate without requiring its key.
// It does not establish trust in the issuer or check revocation.
func Inspect(data []byte) (Info, error) {
	chain, err := decodeCertificates(data)
	if err != nil {
		return Info{}, err
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return Info{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return inspectLeaf(leaf)
}

func inspectLeaf(leaf *x509.Certificate) (Info, error) {
	i := Info{
		Subject:      leaf.Subject.String(),
		Issuer:       leaf.Issuer.String(),
		NotBefore:    leaf.NotBefore,
		NotAfter:     leaf.NotAfter,
		Capabilities: map[string][]string{},
	}
	if topic, err := TopicFromCert(leaf); err == nil {
		i.Topic, i.MDM = topic, true
		i.Capabilities[topic] = []string{"mdm"}
		return i, nil
	}
	for _, n := range leaf.Subject.Names {
		if n.Type.Equal(oidUserID) {
			i.Topic, _ = n.Value.(string)
		}
	}
	legacy, explicit := false, false
	for _, ext := range leaf.Extensions {
		switch ext.Id.String() {
		case "1.2.840.113635.100.6.3.1", "1.2.840.113635.100.6.3.2":
			legacy = true
		case "1.2.840.113635.100.6.3.4", "1.2.840.113635.100.6.3.6":
			explicit = true
			if err := parseTopics(ext.Value, i.Capabilities); err != nil {
				return Info{}, err
			}
		}
	}
	// Legacy single-topic certificates have no topic extension. Never infer
	// authorization from the UID when an explicit topic list is present.
	if legacy && !explicit && i.Topic != "" {
		i.Capabilities[i.Topic] = []string{"topic"}
	}
	return i, nil
}

func parseTopics(data []byte, topics map[string][]string) error {
	var sequence asn1.RawValue
	rest, err := asn1.Unmarshal(data, &sequence)
	if err != nil || len(rest) != 0 || sequence.Tag != asn1.TagSequence ||
		sequence.Class != asn1.ClassUniversal ||
		!sequence.IsCompound {
		return fmt.Errorf("%w: malformed APNs topic extension", ErrInvalid)
	}
	data = sequence.Bytes
	for len(data) > 0 {
		var topic string
		data, err = asn1.Unmarshal(data, &topic)
		if err != nil || topic == "" {
			return fmt.Errorf("%w: malformed APNs topic", ErrInvalid)
		}
		var services []string
		data, err = asn1.Unmarshal(data, &services)
		if err != nil || len(services) == 0 {
			return fmt.Errorf("%w: malformed APNs services", ErrInvalid)
		}
		topics[topic] = append(topics[topic], services...)
	}
	return nil
}

// ParseApp pairs an ordinary APNs app certificate with its private key.
// MDM certificates and certificates without an authorized base app topic fail.
func ParseApp(certData, keyPEM []byte) (Parsed, error) {
	p, err := parsePair(certData, keyPEM)
	if err != nil {
		return Parsed{}, err
	}
	i, err := inspectLeaf(p.Leaf)
	if err != nil {
		return Parsed{}, err
	}
	if i.MDM || i.Topic == "" || !slices.Contains(i.Capabilities[i.Topic], "topic") {
		return Parsed{}, fmt.Errorf("%w: certificate has no ordinary app topic", ErrNoTopic)
	}
	p.Topic = i.Topic
	return p, nil
}

// Validate checks a TLS identity even when supplied by a custom CertStore.
// mdm selects MDM versus ordinary alert/background app authorization.
// The validity interval is NotBefore <= at < NotAfter.
func Validate(pair tls.Certificate, topic string, mdm bool, at time.Time) error {
	if len(pair.Certificate) == 0 {
		return fmt.Errorf("%w: missing certificate", ErrInvalid)
	}
	// Parse the wire certificate, not a potentially inconsistent cached Leaf.
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	key, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return fmt.Errorf("%w: missing signing key", ErrInvalid)
	}
	pub, ok := leaf.PublicKey.(interface{ Equal(crypto.PublicKey) bool })
	if !ok || !pub.Equal(key.Public()) {
		return ErrKeyMismatch
	}
	if at.Before(leaf.NotBefore) || !at.Before(leaf.NotAfter) {
		return fmt.Errorf(
			"%w: certificate outside validity interval %s to %s",
			ErrInvalid,
			leaf.NotBefore.UTC().Format(time.RFC3339),
			leaf.NotAfter.UTC().Format(time.RFC3339),
		)
	}
	if leaf.IsCA || (leaf.KeyUsage != 0 && leaf.KeyUsage&x509.KeyUsageDigitalSignature == 0) {
		return fmt.Errorf("%w: certificate cannot authenticate TLS clients", ErrInvalid)
	}
	if len(leaf.ExtKeyUsage) > 0 &&
		!slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageClientAuth) &&
		!slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageAny) {
		return fmt.Errorf("%w: certificate lacks TLS client authentication usage", ErrInvalid)
	}
	i, err := inspectLeaf(leaf)
	if err != nil {
		return err
	}
	service := "topic"
	if mdm {
		service = "mdm"
	}
	if mdm != i.MDM || !slices.Contains(i.Capabilities[topic], service) {
		return fmt.Errorf(
			"%w: certificate does not authorize %s for %s",
			ErrNoTopic,
			topic,
			service,
		)
	}
	return nil
}

// PEM converts a downloaded certificate or chain to canonical PEM.
func PEM(data []byte) ([]byte, error) {
	chain, err := decodeCertificates(data)
	if err != nil {
		return nil, err
	}
	var out []byte
	for _, der := range chain {
		if _, err := x509.ParseCertificate(der); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	return out, nil
}
