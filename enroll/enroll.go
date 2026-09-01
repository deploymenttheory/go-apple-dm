// Package enroll builds the MDM enrollment profile: the MDM payload, the
// identity it points at (SCEP or a pre-issued PKCS #12), and optional
// trust anchors, validated against Apple's schema (decision record 0009).
//
// Apple documentation:
// https://developer.apple.com/documentation/devicemanagement/mdm
// https://developer.apple.com/documentation/devicemanagement/scep
// https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles
package enroll

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
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

// ServerCapabilities values.
const (
	CapabilityPerUserConnections = "com.apple.mdm.per-user-connections"
	CapabilityBootstrapToken     = "com.apple.mdm.bootstraptoken"
	CapabilityToken              = "com.apple.mdm.token"
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
	PKCS12 *PKCS12

	// Roots are installed as com.apple.security.root payloads so the device
	// trusts the MDM server and SCEP CA.
	Roots []*x509.Certificate

	AccessRights        AccessRights // default AccessRightsAll
	ServerCapabilities  []string
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
	if (p.SCEP == nil) == (p.PKCS12 == nil) {
		return nil, fmt.Errorf("%w: exactly one of SCEP or PKCS12 is required", ErrProfile)
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
