package acme

import (
	"time"

	"github.com/deploymenttheory/go-apple-mdm/acme/attest"
	"github.com/deploymenttheory/go-apple-mdm/acme/jose"
)

// IdentifierPermanent is the only identifier type this server issues for.
// Apple's ACME payload orders with its ClientIdentifier as a
// permanent-identifier, and every other type would produce an authorization
// with no challenge that nothing could ever satisfy.
const IdentifierPermanent = "permanent-identifier"

// ChallengeDeviceAttest is the challenge type Apple answers.
const ChallengeDeviceAttest = "device-attest-01"

// Statuses from RFC 8555 section 7.1.6.
const (
	StatusPending     = "pending"
	StatusProcessing  = "processing"
	StatusReady       = "ready"
	StatusValid       = "valid"
	StatusInvalid     = "invalid"
	StatusDeactivated = "deactivated"
	StatusExpired     = "expired"
)

// Identifier is what an order asks for.
type Identifier struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// Binding is what the server knows about the device an identifier was
// issued to. It is decided when the order is created, stored on the order,
// and enforced when the attestation arrives and again when the certificate
// is signed, so a device cannot be told one thing at enrollment and issued
// something else later.
//
// A binding with neither a serial number nor a UDID names no device. That
// is the ordinary case for a user enrollment, where Apple's attestation
// carries no identity, and AllowUnidentified says whether the deployment
// accepts it.
type Binding struct {
	// Serial and UDID are the device the identifier was issued for. When
	// set, the attestation must agree.
	Serial string `json:"serial,omitempty"`
	UDID   string `json:"udid,omitempty"`
	// EnrollmentID ties the order to an existing enrollment, for a
	// certificate that renews an identity rather than establishing one.
	EnrollmentID string `json:"enrollment_id,omitempty"`
	// CommonName and Organization become the issued certificate's subject.
	// Apple states the server may override the Subject the profile asked
	// for, and this server does: the subject is the server's statement
	// about the device, not the device's.
	CommonName   string   `json:"common_name,omitempty"`
	Organization []string `json:"organization,omitempty"`
	// AllowUnidentified accepts an attestation carrying no serial number
	// and no UDID, which is what a user enrollment produces.
	AllowUnidentified bool `json:"allow_unidentified,omitempty"`
	// NotAfter caps the issued certificate. Zero means the CA policy
	// decides.
	NotAfter time.Time `json:"not_after,omitzero"`
}

// Identified reports whether the binding names a device.
func (b Binding) Identified() bool { return b.Serial != "" || b.UDID != "" }

// Account is an ACME account: a public key and the orders made with it.
type Account struct {
	ID string `json:"id"`
	// Thumbprint is the RFC 7638 thumbprint of Key, and is unique: two
	// registrations of the same key are the same account.
	Thumbprint string    `json:"thumbprint"`
	Key        *jose.JWK `json:"key"`
	Status     string    `json:"status"`
	Contact    []string  `json:"contact,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Order is one certificate request.
type Order struct {
	ID            string     `json:"id"`
	AccountID     string     `json:"account_id"`
	Identifier    Identifier `json:"identifier"`
	Binding       Binding    `json:"binding"`
	Status        string     `json:"status"`
	AuthzID       string     `json:"authz_id"`
	CertificateID string     `json:"certificate_id,omitempty"`
	Expires       time.Time  `json:"expires"`
	Error         *Problem   `json:"error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
}

// Authorization is the permission to satisfy one identifier.
type Authorization struct {
	ID          string     `json:"id"`
	OrderID     string     `json:"order_id"`
	AccountID   string     `json:"account_id"`
	Identifier  Identifier `json:"identifier"`
	Status      string     `json:"status"`
	ChallengeID string     `json:"challenge_id"`
	Expires     time.Time  `json:"expires"`
}

// Challenge is the device-attest-01 challenge for an authorization.
type Challenge struct {
	ID        string `json:"id"`
	AuthzID   string `json:"authz_id"`
	AccountID string `json:"account_id"`
	Type      string `json:"type"`
	// Token is what the device hashes into the attestation's freshness
	// code.
	Token  string `json:"token"`
	Status string `json:"status"`
	// Attestation is the attestation object exactly as it arrived. It is
	// kept so the attestation can be verified again when the order is
	// finalized, against the key in the certificate request that had not
	// been seen when the challenge was answered. A decoded copy would not
	// serve: re-verification has to see the bytes that were signed.
	Attestation []byte    `json:"attestation,omitempty"`
	ValidatedAt time.Time `json:"validated_at,omitzero"`
	Error       *Problem  `json:"error,omitempty"`
}

// Certificate is an issued identity and what the attestation said about the
// device it was issued to. Keeping the device properties alongside the
// certificate is what lets an operator ask later which hardware holds a
// given identity.
type Certificate struct {
	ID        string            `json:"id"`
	OrderID   string            `json:"order_id"`
	AccountID string            `json:"account_id"`
	Serial    string            `json:"serial"`
	ChainPEM  []byte            `json:"chain_pem"`
	Device    attest.Properties `json:"device"`
	Binding   Binding           `json:"binding"`
	NotAfter  time.Time         `json:"not_after"`
	IssuedAt  time.Time         `json:"issued_at"`
}

// Nonce is one anti-replay value. A nonce is single use: taking it removes
// it, and expiry is judged against IssuedAt.
type Nonce struct {
	Value    string    `json:"value"`
	IssuedAt time.Time `json:"issued_at"`
}

// CertificateQuery filters a certificate listing for the admin API.
type CertificateQuery struct {
	Serial    string
	UDID      string
	AccountID string
}
