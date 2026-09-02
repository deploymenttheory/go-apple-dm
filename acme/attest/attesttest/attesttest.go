package attesttest

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/acme/attest"
	"github.com/deploymenttheory/go-apple-mdm/internal/cbor"
)

// ErrCA reports a fault building a chain.
var ErrCA = errors.New("attesttest: attestation authority")

// CA is a stand-in for Apple's attestation authority: a root, an
// intermediate signed by it, and the ability to issue leaves. Apple sends
// the leaf and the intermediate and keeps the root out of band, so Chain
// does the same.
type CA struct {
	Root         *x509.Certificate
	Intermediate *x509.Certificate

	rootKey  crypto.Signer
	interKey crypto.Signer
	serial   int64
}

// NewCA builds an authority. The keys are P-384, as Apple's are.
func NewCA() (*CA, error) {
	c := &CA{}
	var err error
	if c.rootKey, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader); err != nil {
		return nil, fmt.Errorf("%w: root key: %w", ErrCA, err)
	}
	if c.interKey, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader); err != nil {
		return nil, fmt.Errorf("%w: intermediate key: %w", ErrCA, err)
	}
	// The authority's own window is generous so that a test may verify a
	// leaf at a time well in the past without the chain above it being the
	// reason the path fails.
	now := time.Now().Add(-5 * 365 * 24 * time.Hour)
	rootTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Enterprise Attestation Root CA"},
		NotBefore:             now,
		NotAfter:              now.Add(30 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	if c.Root, err = create(rootTmpl, rootTmpl, c.rootKey.Public(), c.rootKey); err != nil {
		return nil, err
	}
	interTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "Test Enterprise Attestation Sub CA 1"},
		NotBefore:             now,
		NotAfter:              now.Add(30 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	if c.Intermediate, err = create(interTmpl, c.Root, c.interKey.Public(), c.rootKey); err != nil {
		return nil, err
	}
	return c, nil
}

func create(
	tmpl, parent *x509.Certificate,
	pub crypto.PublicKey,
	signer crypto.Signer,
) (*x509.Certificate, error) {
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parent, pub, signer)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCA, err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCA, err)
	}
	return cert, nil
}

// Anchors is the trust anchor list a verifier needs for chains from this
// authority.
func (c *CA) Anchors() []*x509.Certificate {
	return []*x509.Certificate{c.Root}
}

// LeafOptions describes one attestation to mint.
type LeafOptions struct {
	// Properties become the leaf's extensions. Properties.Freshness is the
	// freshness code; leave it empty to omit the extension, which is what a
	// device that produced no freshness code would send.
	Properties attest.Properties
	// PublicKey is the attested key. Required: an attestation exists to
	// speak for a key.
	PublicKey crypto.PublicKey
	// NotBefore and NotAfter default to a window around now. Apple's leaves
	// live about ninety days.
	NotBefore, NotAfter time.Time
	// Extra extensions are added verbatim, for tests that need a malformed
	// or unexpected one.
	Extra []pkix.Extension
	// Issuer signs the leaf when set, so a test can produce a chain that
	// looks right but comes from the wrong authority.
	Issuer    *x509.Certificate
	IssuerKey crypto.Signer
}

// Leaf issues one attestation certificate.
func (c *CA) Leaf(o LeafOptions) (*x509.Certificate, error) {
	if o.PublicKey == nil {
		return nil, fmt.Errorf("%w: an attested key is required", ErrCA)
	}
	if o.NotBefore.IsZero() {
		o.NotBefore = time.Now().Add(-time.Hour)
	}
	if o.NotAfter.IsZero() {
		o.NotAfter = o.NotBefore.Add(90 * 24 * time.Hour)
	}
	exts, err := extensions(o.Properties)
	if err != nil {
		return nil, err
	}
	exts = append(exts, o.Extra...)
	c.serial++
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1000 + c.serial),
		Subject:      pkix.Name{CommonName: "Test attestation certificate"},
		NotBefore:    o.NotBefore,
		NotAfter:     o.NotAfter,
		// Apple's leaf carries a critical key usage and no extended key
		// usage, so a verifier that demands client authentication would
		// reject a real attestation.
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment | x509.KeyUsageKeyEncipherment | x509.KeyUsageDataEncipherment,
		BasicConstraintsValid: true,
		ExtraExtensions:       exts,
	}
	issuer, issuerKey := c.Intermediate, c.interKey
	if o.Issuer != nil {
		issuer, issuerKey = o.Issuer, o.IssuerKey
	}
	return create(tmpl, issuer, o.PublicKey, issuerKey)
}

// Chain issues a leaf and returns the DER chain a device sends: the leaf
// first, then the intermediate. The root stays out of band, as Apple's
// does.
func (c *CA) Chain(o LeafOptions) ([][]byte, error) {
	leaf, err := c.Leaf(o)
	if err != nil {
		return nil, err
	}
	return [][]byte{leaf.Raw, c.Intermediate.Raw}, nil
}

// Object issues a leaf and wraps the chain in a WebAuthn attestation
// object, the form an ACME device-attest-01 response carries.
func (c *CA) Object(o LeafOptions) ([]byte, error) {
	chain, err := c.Chain(o)
	if err != nil {
		return nil, err
	}
	return Object(chain)
}

// Object wraps an existing chain in a WebAuthn attestation object.
func Object(chain [][]byte) ([]byte, error) {
	raw, err := cbor.Marshal(map[string]any{
		"fmt":     attest.FormatApple,
		"attStmt": map[string]any{"x5c": chain},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrCA, err)
	}
	return raw, nil
}

// ObjectForToken is the common case: an attestation for key, answering the
// ACME challenge token, describing the given device.
func (c *CA) ObjectForToken(
	token string,
	props attest.Properties,
	key crypto.PublicKey,
) ([]byte, error) {
	props.Freshness = attest.FreshnessForToken(token)
	return c.Object(LeafOptions{Properties: props, PublicKey: key})
}

// extensions encodes properties the way Apple does: the identity and
// version values are bare UTF-8 in the extension value, while the macOS
// security statuses are DER integers.
func extensions(p attest.Properties) ([]pkix.Extension, error) {
	var exts []pkix.Extension
	str := func(oid asn1.ObjectIdentifier, v string) {
		if v != "" {
			exts = append(exts, pkix.Extension{Id: oid, Value: []byte(v)})
		}
	}
	str(attest.OIDSerialNumber, p.SerialNumber)
	str(attest.OIDUDID, p.UDID)
	str(attest.OIDSoftwareUpdateDeviceID, p.SoftwareUpdateDeviceID)
	str(attest.OIDOSVersion, p.OSVersion)
	str(attest.OIDSEPOSVersion, p.SEPOSVersion)
	str(attest.OIDLLBVersion, p.LLBVersion)
	str(attest.OIDSecureBoot, p.SecureBoot)
	if len(p.Freshness) > 0 {
		exts = append(exts, pkix.Extension{Id: attest.OIDFreshness, Value: p.Freshness})
	}
	if p.SIPEnabled != nil {
		// 0 is enabled, 1 is disabled.
		v := 1
		if *p.SIPEnabled {
			v = 0
		}
		ext, err := intExtension(attest.OIDSIPStatus, v)
		if err != nil {
			return nil, err
		}
		exts = append(exts, ext)
	}
	if p.KextsAllowed != nil {
		v := 0
		if *p.KextsAllowed {
			v = 1
		}
		ext, err := intExtension(attest.OIDKextsAllowed, v)
		if err != nil {
			return nil, err
		}
		exts = append(exts, ext)
	}
	return exts, nil
}

func intExtension(oid asn1.ObjectIdentifier, v int) (pkix.Extension, error) {
	der, err := asn1.Marshal(v)
	if err != nil {
		return pkix.Extension{}, fmt.Errorf("%w: %w", ErrCA, err)
	}
	return pkix.Extension{Id: oid, Value: der}, nil
}
