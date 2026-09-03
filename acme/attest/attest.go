package attest

import (
	"crypto"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"time"

	"github.com/deploymenttheory/go-apple-dm/internal/cbor"
)

// FormatApple is the WebAuthn attestation statement format Apple uses for
// Managed Device Attestation.
const FormatApple = "apple"

// MaxChain bounds the certificates accepted in one attestation. Apple sends
// a leaf and one intermediate; the limit exists so a hostile sender cannot
// make the server verify an arbitrarily long path.
const MaxChain = 10

// Errors reported by this package. Each is a distinct reason so a caller can
// answer with the right ACME problem document and log something specific.
var (
	// ErrFormat is a malformed attestation object, or one whose statement
	// format is not Apple's.
	ErrFormat = errors.New("attest: malformed attestation object")
	// ErrNoAttestation is a well-formed statement that carries no chain.
	// Apple sends this when the profile did not ask for an attestation or
	// the hardware cannot produce one, so it is a legitimate answer and the
	// caller's policy decides what it means.
	ErrNoAttestation = errors.New("attest: statement carries no attestation")
	// ErrChain is a chain that does not verify to the trust anchors.
	ErrChain = errors.New("attest: certificate chain does not verify")
	// ErrFreshness is a missing or wrong freshness code, which means the
	// attestation was not produced for this challenge.
	ErrFreshness = errors.New("attest: freshness code does not match")
	// ErrKeyMismatch is an attestation for a different key than the one
	// being certified.
	ErrKeyMismatch = errors.New("attest: attested key is not the requested key")
	// ErrExtension is an extension whose contents do not match the encoding
	// Apple documents for it.
	ErrExtension = errors.New("attest: malformed certificate extension")
	// ErrOptions is a caller mistake rather than a bad attestation.
	ErrOptions = errors.New("attest: invalid verify options")
)

// Apple's object identifiers on the attestation leaf, from the
// DevicePropertiesAttestation documentation. Which ones appear depends on
// the OS version and on whether the enrollment is a user enrollment.
var (
	// OIDSerialNumber holds the device serial number. Omitted for a user
	// enrollment.
	OIDSerialNumber = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 9, 1}
	// OIDUDID holds the UDID. On a Mac this is the ProvisioningUDID, which
	// is not the UDID used elsewhere in the MDM protocol. Omitted for a
	// user enrollment.
	OIDUDID = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 9, 2}
	// OIDSoftwareUpdateDeviceID holds the model identifier used to look up
	// available updates. Introduced in iOS 17.2 and macOS 14.2.
	OIDSoftwareUpdateDeviceID = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 9, 4}
	// OIDOSVersion holds the operating system version. Introduced in
	// iOS 17.2 and macOS 14.2.
	OIDOSVersion = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 10, 1}
	// OIDSEPOSVersion holds the version running on the Secure Enclave.
	OIDSEPOSVersion = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 10, 2}
	// OIDLLBVersion holds the Low Level Bootloader firmware version.
	// Introduced in iOS 17.2 and macOS 14.2.
	OIDLLBVersion = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 10, 3}
	// OIDFreshness holds the freshness code: for ACME, the SHA-256 of the
	// device-attest-01 challenge token.
	OIDFreshness = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 11, 1}
	// OIDSIPStatus holds System Integrity Protection status as a DER
	// integer: 0 enabled, 1 disabled. macOS 14.2 and later.
	OIDSIPStatus = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 13, 1}
	// OIDSecureBoot holds the secure boot status: "Full Security",
	// "Reduced Security", or "Permissive Security". macOS 14.2 and later.
	OIDSecureBoot = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 13, 2}
	// OIDKextsAllowed holds third party kernel extension policy as a DER
	// integer: 0 means none are allowed. macOS 14.2 and later.
	OIDKextsAllowed = asn1.ObjectIdentifier{1, 2, 840, 113635, 100, 8, 13, 3}
)

// Secure boot statuses Apple documents.
const (
	SecureBootFull       = "Full Security"
	SecureBootReduced    = "Reduced Security"
	SecureBootPermissive = "Permissive Security"
)

// Properties are the device facts Apple's attestation servers vouched for.
// A field is empty, or a pointer nil, when the corresponding extension was
// absent: Apple omits the serial number and UDID for a user enrollment, and
// may omit or blank any property it could not verify.
type Properties struct {
	SerialNumber           string
	UDID                   string
	SoftwareUpdateDeviceID string
	OSVersion              string
	SEPOSVersion           string
	LLBVersion             string
	SecureBoot             string
	// SIPEnabled reports System Integrity Protection, on macOS 14.2 and
	// later.
	SIPEnabled *bool
	// KextsAllowed reports whether some third party kernel extensions are
	// allowed, on macOS 14.2 and later.
	KextsAllowed *bool
	// Freshness is the freshness code the leaf carries.
	Freshness []byte
}

// Identified reports whether the attestation names the device. A user
// enrollment carries neither a serial number nor a UDID, so an attestation
// can be genuine and still identify nothing.
func (p Properties) Identified() bool {
	return p.SerialNumber != "" || p.UDID != ""
}

// Attestation is a parsed, not yet verified, attestation.
type Attestation struct {
	// Format is the attestation statement format, always FormatApple here.
	Format string
	// Chain is the certificate chain, leaf first.
	Chain []*x509.Certificate
	// Properties are the extensions read from the leaf.
	Properties Properties
	// Raw is the attestation object exactly as it arrived, empty when the
	// attestation came from a DeviceInformation response. A server stores
	// this so the attestation can be verified again when the order is
	// finalized, which a decoded copy could not support.
	Raw []byte
}

// Leaf is the attestation certificate: the one carrying the device
// properties and the attested key.
func (a *Attestation) Leaf() *x509.Certificate { return a.Chain[0] }

// PublicKey is the attested key. For ACME this is the key the device is
// asking the server to certify.
func (a *Attestation) PublicKey() crypto.PublicKey { return a.Chain[0].PublicKey }

// object is the WebAuthn attestation object. Apple sends no authData
// member, and the ACME device attestation draft does not use one; an
// unknown member is skipped by the decoder rather than rejected.
type object struct {
	Format  string          `cbor:"fmt"`
	AttStmt cbor.RawMessage `cbor:"attStmt"`
}

// appleStatement is the attestation statement for the apple format.
type appleStatement struct {
	X5C [][]byte `cbor:"x5c"`
}

// ParseObject reads the base64-decoded WebAuthn attestation object a device
// sends in answer to an ACME device-attest-01 challenge. The statement is
// decoded in two stages so its contents are interpreted only once the
// format name says how to read them.
//
// A statement with no chain returns ErrNoAttestation together with the
// parsed object, because that is Apple's way of saying the device produced
// no attestation rather than a fault in what it sent.
func ParseObject(raw []byte) (*Attestation, error) {
	if err := cbor.Wellformed(raw); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	var obj object
	if err := cbor.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFormat, err)
	}
	if obj.Format != FormatApple {
		return nil, fmt.Errorf("%w: statement format %q", ErrFormat, obj.Format)
	}
	var stmt appleStatement
	if len(obj.AttStmt) > 0 {
		if err := cbor.Unmarshal(obj.AttStmt, &stmt); err != nil {
			return nil, fmt.Errorf("%w: attestation statement: %w", ErrFormat, err)
		}
	}
	if len(stmt.X5C) == 0 {
		return nil, fmt.Errorf("%w: %w", ErrNoAttestation, errEmptyChain)
	}
	a, err := fromDER(stmt.X5C)
	if err != nil {
		return nil, err
	}
	a.Raw = append([]byte(nil), raw...)
	return a, nil
}

// errEmptyChain gives ErrNoAttestation a cause without a second sentinel.
var errEmptyChain = errors.New("x5c is absent or empty")

// ParseChain reads the certificate chain a device returns in the
// DevicePropertiesAttestation key of a DeviceInformation response, where
// the same attestation arrives as DER certificates rather than CBOR.
func ParseChain(ders [][]byte) (*Attestation, error) {
	if len(ders) == 0 {
		return nil, fmt.Errorf("%w: %w", ErrNoAttestation, errEmptyChain)
	}
	return fromDER(ders)
}

func fromDER(ders [][]byte) (*Attestation, error) {
	if len(ders) > MaxChain {
		return nil, fmt.Errorf("%w: %d certificates, limit %d", ErrFormat, len(ders), MaxChain)
	}
	chain := make([]*x509.Certificate, 0, len(ders))
	for i, der := range ders {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("%w: certificate %d: %w", ErrFormat, i, err)
		}
		chain = append(chain, cert)
	}
	props, err := properties(chain[0])
	if err != nil {
		return nil, err
	}
	return &Attestation{Format: FormatApple, Chain: chain, Properties: props}, nil
}

// properties reads the device facts from the leaf. Each extension is read
// by the encoding Apple documents for it: the identity and version
// extensions hold bare UTF-8 rather than a DER string, while the macOS
// security statuses hold DER integers.
func properties(leaf *x509.Certificate) (Properties, error) {
	var p Properties
	for _, ext := range leaf.Extensions {
		switch {
		case ext.Id.Equal(OIDSerialNumber):
			p.SerialNumber = string(ext.Value)
		case ext.Id.Equal(OIDUDID):
			p.UDID = string(ext.Value)
		case ext.Id.Equal(OIDSoftwareUpdateDeviceID):
			p.SoftwareUpdateDeviceID = string(ext.Value)
		case ext.Id.Equal(OIDOSVersion):
			p.OSVersion = string(ext.Value)
		case ext.Id.Equal(OIDSEPOSVersion):
			p.SEPOSVersion = string(ext.Value)
		case ext.Id.Equal(OIDLLBVersion):
			p.LLBVersion = string(ext.Value)
		case ext.Id.Equal(OIDSecureBoot):
			p.SecureBoot = string(ext.Value)
		case ext.Id.Equal(OIDFreshness):
			p.Freshness = append([]byte(nil), ext.Value...)
		case ext.Id.Equal(OIDSIPStatus):
			// 0 is enabled, 1 is disabled.
			n, err := extensionInt(ext.Value, "SIP status")
			if err != nil {
				return p, err
			}
			if n != nil {
				enabled := *n == 0
				p.SIPEnabled = &enabled
			}
		case ext.Id.Equal(OIDKextsAllowed):
			// 0 means none are allowed; anything else means some are.
			n, err := extensionInt(ext.Value, "kernel extension policy")
			if err != nil {
				return p, err
			}
			if n != nil {
				allowed := *n != 0
				p.KextsAllowed = &allowed
			}
		}
	}
	return p, nil
}

// extensionInt reads a DER integer extension. An empty value is treated as
// absent, since Apple states that a property its servers cannot verify may
// be blank; anything else that will not parse is an error rather than a
// silently wrong answer.
func extensionInt(value []byte, what string) (*int, error) {
	if len(value) == 0 {
		return nil, nil
	}
	var n int
	rest, err := asn1.Unmarshal(value, &n)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrExtension, what, err)
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: %s: %d trailing bytes", ErrExtension, what, len(rest))
	}
	return &n, nil
}

// VerifyOptions controls Verify.
type VerifyOptions struct {
	// Anchors are the trust anchors the chain must reach. Empty means
	// AppleAnchors, which is what a deployment facing real devices wants;
	// tests and labs supply their own.
	Anchors []*x509.Certificate
	// Now is the verification time. Nil means time.Now.
	Now func() time.Time
	// Freshness is the value the leaf's freshness extension must carry: for
	// ACME, FreshnessForToken of the challenge token. It is required, so
	// that an attestation minted for another challenge cannot be replayed
	// into this one.
	Freshness []byte
	// PublicKey is the key the caller is about to certify or trust. When
	// set, the attested key must be the same key. The ACME server always
	// sets it; a DeviceInformation reader has no key to bind and leaves it
	// nil.
	PublicKey crypto.PublicKey
}

// Verify checks the attestation: the chain reaches the anchors, the
// freshness code is the one the caller expects, and, when a key was given,
// the attestation covers that key.
//
// The order matters. The chain is checked first, so that nothing else is
// read from a leaf that no trusted authority signed.
func (a *Attestation) Verify(o VerifyOptions) error {
	if len(o.Freshness) == 0 {
		return fmt.Errorf("%w: a freshness code is required", ErrOptions)
	}
	if err := a.verifyChain(o); err != nil {
		return err
	}
	// A leaf with no freshness extension proves nothing about which
	// challenge it answers, so an absent code fails exactly like a wrong
	// one rather than passing unchecked.
	if subtle.ConstantTimeCompare(a.Properties.Freshness, o.Freshness) != 1 {
		return fmt.Errorf(
			"%w: leaf carries %d bytes, expected %d",
			ErrFreshness, len(a.Properties.Freshness), len(o.Freshness),
		)
	}
	if o.PublicKey != nil {
		if err := sameKey(a.PublicKey(), o.PublicKey); err != nil {
			return err
		}
	}
	return nil
}

func (a *Attestation) verifyChain(o VerifyOptions) error {
	anchorCerts := o.Anchors
	if len(anchorCerts) == 0 {
		anchorCerts = AppleAnchors()
	}
	roots := x509.NewCertPool()
	for _, c := range anchorCerts {
		roots.AddCert(c)
	}
	intermediates := x509.NewCertPool()
	for _, c := range a.Chain[1:] {
		intermediates.AddCert(c)
	}
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	// Apple's leaf carries a critical key usage and no extended key usage,
	// so the path is built without an extended key usage requirement.
	if _, err := a.Leaf().Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return fmt.Errorf("%w: %w", ErrChain, err)
	}
	return nil
}

// sameKey compares two public keys by their marshalled SubjectPublicKeyInfo,
// which is exact and does not depend on the concrete key type.
func sameKey(attested, requested crypto.PublicKey) error {
	a, err := x509.MarshalPKIXPublicKey(attested)
	if err != nil {
		return fmt.Errorf("%w: attested key: %w", ErrKeyMismatch, err)
	}
	b, err := x509.MarshalPKIXPublicKey(requested)
	if err != nil {
		return fmt.Errorf("%w: requested key: %w", ErrKeyMismatch, err)
	}
	if subtle.ConstantTimeCompare(a, b) != 1 {
		return ErrKeyMismatch
	}
	return nil
}

// FreshnessForToken is Apple's rule for ACME: the freshness code in the
// attestation certificate is the SHA-256 of the challenge token. Note that
// it is the token alone, not the RFC 8555 key authorization, so the
// attestation is not bound to the ACME account key and the server must bind
// the device by other means.
func FreshnessForToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}
