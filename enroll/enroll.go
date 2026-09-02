package enroll

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/deploymenttheory/go-apple-mdm/profile"
	"github.com/deploymenttheory/go-apple-mdm/schema/profiles"
	"github.com/deploymenttheory/go-apple-mdm/schema/support"
)

// ErrProfile is returned for an enrollment profile that cannot be built or
// read.
var ErrProfile = errors.New("enroll: profile")

// AccessRights is the MDM payload AccessRights bit mask.
type AccessRights int64

// AccessRights bits, from the MDM payload documentation.
const (
	RightInspectProfiles     AccessRights = 1
	RightInstallProfiles     AccessRights = 2
	RightLockAndPasscode     AccessRights = 4
	RightErase               AccessRights = 8
	RightQueryDeviceInfo     AccessRights = 16
	RightQueryNetworkInfo    AccessRights = 32
	RightInspectProvisioning AccessRights = 64
	RightInstallProvisioning AccessRights = 128
	RightInspectApps         AccessRights = 256
	RightQueryRestrictions   AccessRights = 512
	RightQuerySecurity       AccessRights = 1024
	RightManipulateSettings  AccessRights = 2048
	RightManageApps          AccessRights = 4096
	AccessRightsAll          AccessRights = 8191
)

// Has reports whether every bit in r is set.
func (a AccessRights) Has(r AccessRights) bool { return a&r == r }

// ServerCapabilities values: Apple's capability identifiers for the
// enrollment profile, not credentials.
const (
	CapabilityPerUserConnections = "com.apple.mdm.per-user-connections"
	CapabilityBootstrapToken     = "com.apple.mdm.bootstraptoken" // #nosec G101 -- capability identifier
	CapabilityToken              = "com.apple.mdm.token"          // #nosec G101 -- capability identifier
)

// SCEP describes the SCEP identity payload.
type SCEP struct {
	URL       string
	Name      string
	Challenge string
	Subject   pkix.Name
	// KeySize default 2048; KeyUsage default 5 (signing and encryption).
	KeySize       int64
	KeyUsage      int64
	CAFingerprint []byte
	Retries       int64
	RetryDelay    int64
}

// PKCS12 describes a pre-issued identity.
// Key types Apple's ACME payload accepts.
const (
	// KeyTypeEC is Apple's name for an elliptic curve key on a NIST prime
	// curve. It is the only type a hardware bound key may have.
	KeyTypeEC = "ECSECPrimeRandom"
	// KeyTypeRSA can only be used for a key that is not hardware bound, and
	// so cannot be attested.
	KeyTypeRSA = "RSA"
)

// ACME describes the com.apple.security.acme payload: the device generates
// a key, obtains a certificate for it from an ACME server, and uses the
// result as its identity.
//
// This is the alternative to SCEP, and the better one where the hardware
// allows it. A SCEP identity is authenticated by a challenge password that
// has to be carried in the profile; an attested ACME identity is a key the
// Secure Enclave generated and that Apple's servers vouch for, so the
// profile carries a client identifier rather than a secret that issues
// certificates to whoever holds it.
type ACME struct {
	// DirectoryURL is the ACME directory, which must use https.
	DirectoryURL string
	// ClientIdentifier is what the device orders with, as the
	// permanent-identifier. Apple treats it as an anti-replay code, so it
	// should be unguessable and issued for one device.
	ClientIdentifier string
	// KeyType is KeyTypeEC or KeyTypeRSA, and KeySize is the curve size or
	// the modulus size. Apple requires both.
	KeyType string
	KeySize int64
	// HardwareBound generates the key in the Secure Enclave, where it
	// cannot be exported. Required for Attest.
	HardwareBound bool
	// Attest asks the device for an attestation of the key and of the
	// hardware, which the ACME server verifies. Requires HardwareBound.
	Attest bool
	// Subject, SubjectAltName, UsageFlags, and ExtendedKeyUsage are what
	// the device asks for. Apple states the server may override or ignore
	// them, and ours sets the subject itself.
	Subject          pkix.Name
	SubjectAltName   *profiles.ACMECertificateSubjectAltName
	UsageFlags       *int64
	ExtendedKeyUsage []string
	// KeyIsExtractable and AllowAllAppsAccess are macOS only.
	KeyIsExtractable   *bool
	AllowAllAppsAccess *bool
}

// validate applies Apple's rules about which combinations of key type, key
// size, hardware binding, and attestation are usable. A profile that breaks
// them installs and then fails on the device, where the reason is far
// harder to see than it is here.
func (a *ACME) validate() error {
	if a.DirectoryURL == "" {
		return fmt.Errorf("%w: ACME DirectoryURL is required", ErrProfile)
	}
	if !strings.HasPrefix(a.DirectoryURL, "https://") {
		return fmt.Errorf("%w: ACME DirectoryURL %q must use https", ErrProfile, a.DirectoryURL)
	}
	if a.ClientIdentifier == "" {
		return fmt.Errorf("%w: ACME ClientIdentifier is required", ErrProfile)
	}
	switch a.KeyType {
	case KeyTypeRSA:
		if a.HardwareBound {
			return fmt.Errorf("%w: an RSA key cannot be hardware bound", ErrProfile)
		}
		if a.KeySize < 1024 || a.KeySize > 4096 || a.KeySize%8 != 0 {
			return fmt.Errorf(
				"%w: an RSA KeySize must be a multiple of 8 between 1024 and 4096, got %d",
				ErrProfile, a.KeySize,
			)
		}
	case KeyTypeEC:
		switch a.KeySize {
		case 192, 256, 384, 521:
		default:
			return fmt.Errorf(
				"%w: an %s KeySize must be 192, 256, 384, or 521, got %d",
				ErrProfile, KeyTypeEC, a.KeySize,
			)
		}
		if a.HardwareBound && a.KeySize != 256 && a.KeySize != 384 {
			return fmt.Errorf(
				"%w: a hardware bound key must be 256 or 384 bits, got %d", ErrProfile, a.KeySize,
			)
		}
	default:
		return fmt.Errorf(
			"%w: ACME KeyType must be %s or %s, got %q",
			ErrProfile, KeyTypeEC, KeyTypeRSA, a.KeyType,
		)
	}
	if a.Attest && !a.HardwareBound {
		// Apple attests that a key lives in the Secure Enclave, so there is
		// nothing to attest about a key that lives anywhere else.
		return fmt.Errorf("%w: Attest requires HardwareBound", ErrProfile)
	}
	return nil
}

func (a *ACME) payload() *profiles.ACMECertificate {
	out := &profiles.ACMECertificate{
		DirectoryURL:       a.DirectoryURL,
		ClientIdentifier:   a.ClientIdentifier,
		KeyType:            a.KeyType,
		KeySize:            a.KeySize,
		HardwareBound:      a.HardwareBound,
		SubjectAltName:     a.SubjectAltName,
		UsageFlags:         a.UsageFlags,
		ExtendedKeyUsage:   a.ExtendedKeyUsage,
		KeyIsExtractable:   a.KeyIsExtractable,
		AllowAllAppsAccess: a.AllowAllAppsAccess,
	}
	if a.Attest {
		attest := true
		out.Attest = &attest
	}
	if subject := SubjectFromName(a.Subject); len(subject) > 0 {
		out.Subject = subject
	}
	return out
}

type PKCS12 struct {
	Data     []byte
	Password string
	FileName string
}

// Profile is the input to Build.
type Profile struct {
	Identifier   string // top-level PayloadIdentifier, e.g. com.example.mdm
	DisplayName  string
	Description  string
	Organization string

	Topic      string
	ServerURL  string
	CheckInURL string

	// Exactly one identity source.
	SCEP   *SCEP
	ACME   *ACME
	PKCS12 *PKCS12

	// Roots are installed as com.apple.security.root payloads so the device
	// trusts the MDM server and SCEP CA.
	Roots []*x509.Certificate

	AccessRights       AccessRights // default AccessRightsAll
	ServerCapabilities []string
	// SharedIPad marks a profile for Shared iPad (DEP is_multi_user):
	// Apple requires com.apple.mdm.per-user-connections in
	// ServerCapabilities, which Build adds when absent (decision record 0029).
	SharedIPad          bool
	SignMessage         *bool // default true
	CheckOutWhenRemoved bool
	UseDevelopmentAPNS  bool

	// Account-driven and user-enrollment keys.
	AssignedManagedAppleID string
	EnrollmentMode         string

	// UUIDs make the profile stable across rebuilds; empty values are
	// generated and reported back through Built.
	UUID         string
	MDMUUID      string
	IdentityUUID string
	RootUUIDs    []string

	// Target for schema validation; the zero value skips OS checks.
	Target support.Target
}

// Build assembles and validates the profile.
func (p Profile) Build() (*profile.Profile, error) {
	if p.Identifier == "" || p.Topic == "" || p.ServerURL == "" {
		return nil, fmt.Errorf("%w: Identifier, Topic, and ServerURL are required", ErrProfile)
	}
	if !strings.HasPrefix(p.Topic, "com.apple.mgmt.") {
		return nil, fmt.Errorf("%w: Topic must begin with com.apple.mgmt.", ErrProfile)
	}
	identities := 0
	for _, present := range []bool{p.SCEP != nil, p.ACME != nil, p.PKCS12 != nil} {
		if present {
			identities++
		}
	}
	if identities != 1 {
		return nil, fmt.Errorf("%w: exactly one of SCEP, ACME, or PKCS12 is required", ErrProfile)
	}
	for _, u := range []string{p.ServerURL, p.CheckInURL} {
		if u != "" && !strings.HasPrefix(u, "https://") {
			return nil, fmt.Errorf("%w: %q must use https", ErrProfile, u)
		}
	}
	out := &profile.Profile{
		Identifier: p.Identifier, UUID: orUUID(p.UUID),
		DisplayName: p.DisplayName, Description: p.Description, Organization: p.Organization,
		Scope: profile.ScopeSystem,
	}
	for i, root := range p.Roots {
		u := ""
		if i < len(p.RootUUIDs) {
			u = p.RootUUIDs[i]
		}
		out.Payloads = append(out.Payloads, profile.Payload{
			Identifier:  fmt.Sprintf("%s.root.%d", p.Identifier, i),
			UUID:        orUUID(u),
			DisplayName: "Root certificate: " + root.Subject.CommonName,
			Content: &profiles.CertificateRoot{
				PayloadCertificateFileName: new(root.Subject.CommonName + ".cer"),
				PayloadContent:             root.Raw,
			},
		})
	}
	identityUUID := orUUID(p.IdentityUUID)
	switch {
	case p.SCEP != nil:
		if p.SCEP.URL == "" {
			return nil, fmt.Errorf("%w: SCEP URL is required", ErrProfile)
		}
		out.Payloads = append(out.Payloads, profile.Payload{
			Identifier: p.Identifier + ".scep", UUID: identityUUID, DisplayName: "MDM identity (SCEP)",
			Content: p.SCEP.payload(),
		})
	case p.ACME != nil:
		if err := p.ACME.validate(); err != nil {
			return nil, err
		}
		out.Payloads = append(out.Payloads, profile.Payload{
			Identifier: p.Identifier + ".acme", UUID: identityUUID, DisplayName: "MDM identity (ACME)",
			Content: p.ACME.payload(),
		})
	default:
		if len(p.PKCS12.Data) == 0 {
			return nil, fmt.Errorf("%w: PKCS12 data is required", ErrProfile)
		}
		fn := p.PKCS12.FileName
		if fn == "" {
			fn = "identity.p12"
		}
		out.Payloads = append(out.Payloads, profile.Payload{
			Identifier: p.Identifier + ".identity", UUID: identityUUID, DisplayName: "MDM identity",
			Content: &profiles.CertificatePKCS12{
				PayloadCertificateFileName: &fn, PayloadContent: p.PKCS12.Data, Password: nonEmpty(p.PKCS12.Password),
			},
		})
	}
	rights := p.AccessRights
	if rights == 0 {
		rights = AccessRightsAll
	}
	sign := true
	if p.SignMessage != nil {
		sign = *p.SignMessage
	}
	mdmPayload := &profiles.MDM{
		IdentityCertificateUUID: identityUUID,
		Topic:                   p.Topic,
		ServerURL:               p.ServerURL,
		CheckInURL:              nonEmpty(p.CheckInURL),
		SignMessage:             &sign,
		AccessRights:            new(int64(rights)),
		ServerCapabilities:      p.ServerCapabilities,
		AssignedManagedAppleID:  nonEmpty(p.AssignedManagedAppleID),
		EnrollmentMode:          nonEmpty(p.EnrollmentMode),
	}
	if p.SharedIPad && !slices.Contains(mdmPayload.ServerCapabilities, CapabilityPerUserConnections) {
		mdmPayload.ServerCapabilities = append(slices.Clone(mdmPayload.ServerCapabilities), CapabilityPerUserConnections)
	}
	if p.CheckOutWhenRemoved {
		mdmPayload.CheckOutWhenRemoved = new(true)
	}
	if p.UseDevelopmentAPNS {
		mdmPayload.UseDevelopmentAPNS = new(true)
	}
	out.Payloads = append(out.Payloads, profile.Payload{
		Identifier: p.Identifier + ".mdm", UUID: orUUID(p.MDMUUID), DisplayName: "MDM", Content: mdmPayload,
	})
	if err := out.Validate(p.Target); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProfile, err)
	}
	return out, nil
}

// Marshal builds and renders the unsigned profile.
func (p Profile) Marshal() ([]byte, error) {
	built, err := p.Build()
	if err != nil {
		return nil, err
	}
	return built.Marshal()
}

func (s *SCEP) payload() *profiles.SCEP {
	keySize, keyUsage := s.KeySize, s.KeyUsage
	if keySize == 0 {
		keySize = 2048
	}
	if keyUsage == 0 {
		keyUsage = 5
	}
	c := profiles.SCEPPayloadContent{
		URL: s.URL, Name: nonEmpty(s.Name), Challenge: nonEmpty(s.Challenge),
		Subject: SubjectFromName(s.Subject), Keysize: &keySize, KeyType: new("RSA"), KeyUsage: &keyUsage,
		CAFingerprint: s.CAFingerprint,
	}
	if s.Retries > 0 {
		c.Retries = new(s.Retries)
	}
	if s.RetryDelay > 0 {
		c.RetryDelay = new(s.RetryDelay)
	}
	return &profiles.SCEP{PayloadContent: c}
}

// SubjectFromName renders a name in the SCEP payload's array-of-arrays
// form: [[["CN","value"]], [["O","value"]], ...].
func SubjectFromName(n pkix.Name) [][][]string {
	var out [][][]string
	add := func(k string, vals []string) {
		for _, v := range vals {
			out = append(out, [][]string{{k, v}})
		}
	}
	add("C", n.Country)
	add("O", n.Organization)
	add("OU", n.OrganizationalUnit)
	add("L", n.Locality)
	add("ST", n.Province)
	if n.CommonName != "" {
		add("CN", []string{n.CommonName})
	}
	return out
}

// NameFromSubject is the inverse of SubjectFromName.
func NameFromSubject(s [][][]string) pkix.Name {
	var n pkix.Name
	for _, rdn := range s {
		for _, pair := range rdn {
			if len(pair) != 2 {
				continue
			}
			switch strings.ToUpper(pair[0]) {
			case "C":
				n.Country = append(n.Country, pair[1])
			case "O":
				n.Organization = append(n.Organization, pair[1])
			case "OU":
				n.OrganizationalUnit = append(n.OrganizationalUnit, pair[1])
			case "L":
				n.Locality = append(n.Locality, pair[1])
			case "ST":
				n.Province = append(n.Province, pair[1])
			case "CN":
				n.CommonName = pair[1]
			}
		}
	}
	return n
}

// Parse reads an enrollment profile (signed or not) back into Profile so
// a client can follow it.
func Parse(data []byte, o profile.ParseOptions) (*Profile, error) {
	parsed, err := profile.Parse(data, o)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProfile, err)
	}
	pr := parsed.Profile
	m, ok := profile.Find[*profiles.MDM](pr)
	if !ok {
		return nil, fmt.Errorf("%w: no com.apple.mdm payload", ErrProfile)
	}
	out := &Profile{
		Identifier: pr.Identifier, DisplayName: pr.DisplayName, Description: pr.Description, Organization: pr.Organization,
		UUID: pr.UUID, Topic: m.Topic, ServerURL: m.ServerURL, CheckInURL: deref(m.CheckInURL),
		ServerCapabilities: m.ServerCapabilities, SignMessage: m.SignMessage,
		AssignedManagedAppleID: deref(m.AssignedManagedAppleID), EnrollmentMode: deref(m.EnrollmentMode),
		IdentityUUID: m.IdentityCertificateUUID,
	}
	if m.AccessRights != nil {
		out.AccessRights = AccessRights(*m.AccessRights)
	}
	out.CheckOutWhenRemoved = m.CheckOutWhenRemoved != nil && *m.CheckOutWhenRemoved
	out.UseDevelopmentAPNS = m.UseDevelopmentAPNS != nil && *m.UseDevelopmentAPNS
	for _, pl := range pr.Payloads {
		switch c := pl.Content.(type) {
		case *profiles.MDM:
			out.MDMUUID = pl.UUID
		case *profiles.CertificateRoot:
			cert, err := x509.ParseCertificate(c.PayloadContent)
			if err != nil {
				return nil, fmt.Errorf("%w: root payload %s: %w", ErrProfile, pl.Identifier, err)
			}
			out.Roots = append(out.Roots, cert)
			out.RootUUIDs = append(out.RootUUIDs, pl.UUID)
		}
	}
	id, ok := pr.FindUUID(m.IdentityCertificateUUID)
	if !ok {
		return nil, fmt.Errorf("%w: IdentityCertificateUUID %s not found", ErrProfile, m.IdentityCertificateUUID)
	}
	switch c := id.Content.(type) {
	case *profiles.SCEP:
		out.SCEP = &SCEP{
			URL: c.PayloadContent.URL, Name: deref(c.PayloadContent.Name), Challenge: deref(c.PayloadContent.Challenge),
			Subject: NameFromSubject(c.PayloadContent.Subject), CAFingerprint: c.PayloadContent.CAFingerprint,
		}
		if c.PayloadContent.Keysize != nil {
			out.SCEP.KeySize = *c.PayloadContent.Keysize
		}
		if c.PayloadContent.KeyUsage != nil {
			out.SCEP.KeyUsage = *c.PayloadContent.KeyUsage
		}
		if c.PayloadContent.Retries != nil {
			out.SCEP.Retries = *c.PayloadContent.Retries
		}
		if c.PayloadContent.RetryDelay != nil {
			out.SCEP.RetryDelay = *c.PayloadContent.RetryDelay
		}
	case *profiles.ACMECertificate:
		out.ACME = &ACME{
			DirectoryURL: c.DirectoryURL, ClientIdentifier: c.ClientIdentifier,
			KeyType: c.KeyType, KeySize: c.KeySize, HardwareBound: c.HardwareBound,
			Subject: NameFromSubject(c.Subject), SubjectAltName: c.SubjectAltName,
			UsageFlags: c.UsageFlags, ExtendedKeyUsage: c.ExtendedKeyUsage,
			KeyIsExtractable: c.KeyIsExtractable, AllowAllAppsAccess: c.AllowAllAppsAccess,
		}
		if c.Attest != nil {
			out.ACME.Attest = *c.Attest
		}
	case *profiles.CertificatePKCS12:
		out.PKCS12 = &PKCS12{Data: c.PayloadContent, Password: deref(c.Password), FileName: deref(c.PayloadCertificateFileName)}
	default:
		return nil, fmt.Errorf("%w: identity payload %s has type %s", ErrProfile, id.Identifier, id.Content.PayloadTypeName())
	}
	return out, nil
}

func orUUID(u string) string {
	if u == "" {
		return profile.NewUUID()
	}
	return u
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
