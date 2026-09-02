package simulator

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	json "encoding/json/v2"
	"errors"
	"fmt"

	xacme "golang.org/x/crypto/acme"

	"github.com/deploymenttheory/go-apple-mdm/acme/attest"
	"github.com/deploymenttheory/go-apple-mdm/acme/attest/attesttest"
	"github.com/deploymenttheory/go-apple-mdm/ca"
	"github.com/deploymenttheory/go-apple-mdm/enroll"
)

// ErrACME is an ACME enrollment that did not complete.
var ErrACME = errors.New("simulator: ACME enrollment")

// ACMEOptions control how the simulated device answers a device-attest-01
// challenge.
//
// The client itself is golang.org/x/crypto/acme rather than anything from
// this repository. Testing a server against its own client proves that the
// two agree, not that the server implements RFC 8555, so the simulator
// drives it with an independent implementation and only the attestation,
// which is Apple's own extension, is ours.
type ACMEOptions struct {
	// Attestation mints the attestation. Nil means the device produces
	// none, which is what hardware without a Secure Enclave does and what
	// a profile with Attest false asks for.
	Attestation *attesttest.CA
	// Properties describe the device in the attestation. Empty identity
	// fields are filled from the device's own serial number and UDID.
	Properties attest.Properties
	// Faults make the device misbehave, for the tests that check a server
	// refuses what it should.
	Faults ACMEFaults
}

// ACMEFaults are ways a device can answer a challenge wrongly. Each one is
// a real attack or a real bug, not an arbitrary corruption.
type ACMEFaults struct {
	// WrongKey attests one key and asks the server to certify another,
	// which is what an attacker replaying somebody else's attestation
	// would do.
	WrongKey bool
	// StaleFreshness answers with an attestation minted for a different
	// challenge, which is a replay from an earlier order.
	StaleFreshness bool
	// NoAttestation sends a well formed statement carrying no chain, as a
	// device that cannot attest does.
	NoAttestation bool
	// ForeignCA attests with an authority the server does not trust.
	ForeignCA *attesttest.CA
}

// ACMEEnroll obtains an identity from the ACME server named in an
// enrollment profile's ACME payload and adopts it as the device identity.
func (d *Device) ACMEEnroll(ctx context.Context, p *enroll.ACME, o ACMEOptions) error {
	certKey, err := acmeKey(p)
	if err != nil {
		return err
	}
	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("%w: account key: %w", ErrACME, err)
	}
	client := &xacme.Client{
		Key:          accountKey,
		DirectoryURL: p.DirectoryURL,
		HTTPClient:   d.Client,
		UserAgent:    "go-apple-mdm-simulator",
	}
	if _, err := client.Register(ctx, &xacme.Account{}, xacme.AcceptTOS); err != nil {
		return fmt.Errorf("%w: register: %w", ErrACME, err)
	}
	order, err := client.AuthorizeOrder(
		ctx,
		[]xacme.AuthzID{{Type: "permanent-identifier", Value: p.ClientIdentifier}},
	)
	if err != nil {
		return fmt.Errorf("%w: order: %w", ErrACME, err)
	}
	if len(order.AuthzURLs) != 1 {
		return fmt.Errorf("%w: %d authorizations", ErrACME, len(order.AuthzURLs))
	}
	authz, err := client.GetAuthorization(ctx, order.AuthzURLs[0])
	if err != nil {
		return fmt.Errorf("%w: authorization: %w", ErrACME, err)
	}
	challenge, err := deviceAttestChallenge(authz)
	if err != nil {
		return err
	}
	payload, err := d.challengePayload(challenge.Token, certKey.Public(), o)
	if err != nil {
		return err
	}
	challenge.Payload = payload
	if _, err := client.Accept(ctx, challenge); err != nil {
		return fmt.Errorf("%w: challenge: %w", ErrACME, err)
	}
	if _, err := client.WaitOrder(ctx, order.URI); err != nil {
		return fmt.Errorf("%w: order did not become ready: %w", ErrACME, err)
	}
	csr, err := acmeCSR(p, certKey)
	if err != nil {
		return err
	}
	chain, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr, true)
	if err != nil {
		return fmt.Errorf("%w: finalize: %w", ErrACME, err)
	}
	if len(chain) == 0 {
		return fmt.Errorf("%w: the server returned no certificate", ErrACME)
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return fmt.Errorf("%w: issued certificate: %w", ErrACME, err)
	}
	d.Identity = &Identity{Cert: leaf, Key: certKey}
	return nil
}

// deviceAttestChallenge finds the challenge Apple answers. A device that is
// offered anything else cannot enroll, and saying so plainly is more useful
// than failing later on an empty token.
func deviceAttestChallenge(authz *xacme.Authorization) (*xacme.Challenge, error) {
	for _, c := range authz.Challenges {
		if c.Type == "device-attest-01" {
			return c, nil
		}
	}
	return nil, fmt.Errorf("%w: no device-attest-01 challenge was offered", ErrACME)
}

// challengePayload builds the WebAuthn attestation object the challenge
// response carries.
func (d *Device) challengePayload(
	token string,
	certKey crypto.PublicKey,
	o ACMEOptions,
) ([]byte, error) {
	authority := o.Attestation
	if o.Faults.ForeignCA != nil {
		authority = o.Faults.ForeignCA
	}
	var object []byte
	switch {
	case authority == nil || o.Faults.NoAttestation:
		// A device that cannot attest still answers the challenge, with a
		// statement that carries no chain.
		raw, err := attesttest.Object(nil)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrACME, err)
		}
		object = raw
	default:
		props := o.Properties
		if props.SerialNumber == "" {
			props.SerialNumber = d.SerialNumber
		}
		if props.UDID == "" {
			props.UDID = d.UDID
		}
		if props.SEPOSVersion == "" {
			props.SEPOSVersion = d.OSVersion
		}
		attested := certKey
		if o.Faults.WrongKey {
			other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			if err != nil {
				return nil, fmt.Errorf("%w: %w", ErrACME, err)
			}
			attested = other.Public()
		}
		freshFor := token
		if o.Faults.StaleFreshness {
			freshFor = token + "-from-an-earlier-order"
		}
		raw, err := authority.ObjectForToken(freshFor, props, attested)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrACME, err)
		}
		object = raw
	}
	payload, err := json.Marshal(map[string]string{
		"attObj": base64.RawURLEncoding.EncodeToString(object),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrACME, err)
	}
	return payload, nil
}

// acmeKey generates the key the certificate will be issued for, of the type
// and size the payload asks for. A hardware bound key would live in the
// Secure Enclave; here it is an ordinary key of the same shape.
func acmeKey(p *enroll.ACME) (crypto.Signer, error) {
	switch p.KeyType {
	case enroll.KeyTypeEC:
		var curve elliptic.Curve
		switch p.KeySize {
		case 256:
			curve = elliptic.P256()
		case 384:
			curve = elliptic.P384()
		case 521:
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("%w: unsupported curve size %d", ErrACME, p.KeySize)
		}
		key, err := ecdsa.GenerateKey(curve, rand.Reader)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrACME, err)
		}
		return key, nil
	case enroll.KeyTypeRSA:
		key, err := rsa.GenerateKey(rand.Reader, int(p.KeySize))
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrACME, err)
		}
		return key, nil
	default:
		return nil, fmt.Errorf("%w: unsupported key type %q", ErrACME, p.KeyType)
	}
}

// acmeCSR builds the certificate request. A real device sends an empty
// subject with a critical subject alternative name carrying the client
// identifier as a permanent identifier, so the simulator does the same.
func acmeCSR(p *enroll.ACME, key crypto.Signer) ([]byte, error) {
	otherName, err := ca.PermanentIdentifier(p.ClientIdentifier)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrACME, err)
	}
	subject := p.Subject
	ext, ok, err := ca.SANExtension(ca.SANs{OtherNames: []ca.OtherName{otherName}}, isEmptyName(subject))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrACME, err)
	}
	tmpl := &x509.CertificateRequest{Subject: subject}
	if ok {
		tmpl.ExtraExtensions = []pkix.Extension{ext}
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrACME, err)
	}
	return der, nil
}

func isEmptyName(n pkix.Name) bool {
	return n.CommonName == "" && len(n.Organization) == 0 && len(n.OrganizationalUnit) == 0 &&
		len(n.Country) == 0 && len(n.Locality) == 0 && len(n.Province) == 0
}

// DevicePropertiesAttestation answers the DevicePropertiesAttestation query
// of a DeviceInformation command with a chain describing this device.
//
// Apple's device caches the attestation it generated and returns that one
// for up to seven days, whatever freshness code the server asked for, so a
// server cannot treat a mismatched freshness code on this path as evidence
// of a replay the way it can on the ACME path. The simulator reproduces
// that: once an attestation has been minted it is returned again until
// ExpireAttestationCache is called.
func (d *Device) DevicePropertiesAttestation(nonce []byte) ([][]byte, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.acme.Attestation == nil {
		// Hardware that cannot attest ignores the keys entirely.
		return nil, nil
	}
	if d.attestation != nil {
		return d.attestation, nil
	}
	props := d.acme.Properties
	if props.SerialNumber == "" {
		props.SerialNumber = d.SerialNumber
	}
	if props.UDID == "" {
		props.UDID = d.UDID
	}
	if props.OSVersion == "" {
		props.OSVersion = d.OSVersion
	}
	props.Freshness = append([]byte(nil), nonce...)
	chain, err := d.acme.Attestation.Chain(attesttest.LeafOptions{
		Properties: props,
		PublicKey:  d.attestationKey(),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrACME, err)
	}
	d.attestation = chain
	return chain, nil
}

// ExpireAttestationCache makes the next DevicePropertiesAttestation mint a
// fresh attestation, as if Apple's seven day window had passed.
func (d *Device) ExpireAttestationCache() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.attestation = nil
}

// attestationKey is the key the device attests on the DeviceInformation
// path, where there is no certificate request to bind to. It is the
// enrollment identity's key when there is one, so the attestation still
// speaks for something the server knows.
func (d *Device) attestationKey() crypto.PublicKey {
	if d.Identity != nil {
		return d.Identity.Key.Public()
	}
	if d.attestKey == nil {
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			panic("simulator: attestation key: " + err.Error())
		}
		d.attestKey = key
	}
	return d.attestKey.Public()
}
