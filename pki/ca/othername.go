package ca

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"net"
	"net/url"
)

// OIDs for the otherName forms this package can build.
var (
	OIDPermanentIdentifier = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 8, 3} // RFC 4043
	OIDHardwareModuleName  = asn1.ObjectIdentifier{1, 3, 6, 1, 5, 5, 7, 8, 4} // RFC 4108
	OIDNTPrincipalName     = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 20, 2, 3}
)

// oidSubjectAltName is the subjectAltName extension of RFC 5280 section
// 4.2.1.6. The package builds this extension itself rather than letting
// crypto/x509 do it, because a certificate may hold only one of them and
// crypto/x509 cannot express the otherName form.
var oidSubjectAltName = asn1.ObjectIdentifier{2, 5, 29, 17}

// Identifier octets of the GeneralName choice of RFC 5280 section
// 4.2.1.6: context class (0x80) plus the tag number, with the constructed
// bit (0x20) on the forms that hold a nested value. The choices this
// package does not build (x400Address, directoryName, ediPartyName and
// registeredID) have no place in a device identity.
const (
	idSequence      byte = 0x30
	idOtherName     byte = 0xa0 // [0] IMPLICIT SEQUENCE
	idExplicitValue byte = 0xa0 // the [0] EXPLICIT wrapper inside an otherName
	idRFC822Name    byte = 0x81
	idDNSName       byte = 0x82
	idURI           byte = 0x86
	idIPAddress     byte = 0x87
)

// tagZero is the tag number that encoding/asn1 reports when parsing a [0]
// value, which is both the otherName choice of a GeneralName and the
// explicit wrapper around an otherName's value.
const tagZero = 0

// OtherName is one GeneralName of the otherName form.
type OtherName struct {
	ID    asn1.ObjectIdentifier
	Value []byte // the DER of the value, already encoded
}

// permanentIdentifier is the RFC 4043 PermanentIdentifier structure. Both
// members are optional, so a decoder has to accept a value that carries
// neither.
type permanentIdentifier struct {
	IdentifierValue string                `asn1:"utf8,optional"`
	Assigner        asn1.ObjectIdentifier `asn1:"optional"`
}

// hardwareModuleName is the RFC 4108 HardwareModuleName structure.
type hardwareModuleName struct {
	Type   asn1.ObjectIdentifier
	Serial []byte
}

// PermanentIdentifier builds the RFC 4043 otherName for an identifier
// value. RFC 4043: PermanentIdentifier ::= SEQUENCE { identifierValue
// UTF8String OPTIONAL, assigner OBJECT IDENTIFIER OPTIONAL }. When the
// assigner is absent the certificate issuer is the assigner.
func PermanentIdentifier(value string) (OtherName, error) {
	if value == "" {
		return OtherName{}, errors.New("ca: permanent identifier value is empty")
	}
	der, err := asn1.Marshal(permanentIdentifier{IdentifierValue: value})
	if err != nil {
		return OtherName{}, fmt.Errorf("ca: encode permanent identifier: %w", err)
	}
	return OtherName{ID: OIDPermanentIdentifier, Value: der}, nil
}

// HardwareModuleName builds the RFC 4108 otherName. HardwareModuleName ::=
// SEQUENCE { hwType OBJECT IDENTIFIER, hwSerialNum OCTET STRING }. Both
// members are required.
func HardwareModuleName(hwType asn1.ObjectIdentifier, serial []byte) (OtherName, error) {
	if len(hwType) == 0 {
		return OtherName{}, errors.New("ca: hardware module type is empty")
	}
	if len(serial) == 0 {
		return OtherName{}, errors.New("ca: hardware module serial number is empty")
	}
	der, err := asn1.Marshal(hardwareModuleName{Type: hwType, Serial: serial})
	if err != nil {
		return OtherName{}, fmt.Errorf("ca: encode hardware module name: %w", err)
	}
	return OtherName{ID: OIDHardwareModuleName, Value: der}, nil
}

// NTPrincipalName builds the otherName Apple's ACME payload calls
// ntPrincipalName, a UTF8String under 1.3.6.1.4.1.311.20.2.3.
func NTPrincipalName(name string) (OtherName, error) {
	if name == "" {
		return OtherName{}, errors.New("ca: NT principal name is empty")
	}
	der, err := asn1.MarshalWithParams(name, "utf8")
	if err != nil {
		return OtherName{}, fmt.Errorf("ca: encode NT principal name: %w", err)
	}
	return OtherName{ID: OIDNTPrincipalName, Value: der}, nil
}

// SANs are the subject alternative names of an issued certificate.
type SANs struct {
	OtherNames     []OtherName
	DNSNames       []string
	EmailAddresses []string
	IPAddresses    []net.IP
	URIs           []*url.URL
}

// empty reports whether there is nothing at all to encode.
func (s SANs) empty() bool {
	return len(s.OtherNames) == 0 && len(s.DNSNames) == 0 && len(s.EmailAddresses) == 0 &&
		len(s.IPAddresses) == 0 && len(s.URIs) == 0
}

// SANExtension builds the subjectAltName extension (OID 2.5.29.17) from
// otherNames plus the conventional name forms. It returns the zero
// Extension and false when there is nothing to encode. The extension is
// marked critical when the certificate's subject is empty, as RFC 5280
// section 4.2.1.6 requires.
func SANExtension(names SANs, subjectEmpty bool) (pkix.Extension, bool, error) {
	if names.empty() {
		return pkix.Extension{}, false, nil
	}
	var body []byte
	for _, other := range names.OtherNames {
		der, err := other.der()
		if err != nil {
			return pkix.Extension{}, false, err
		}
		body = append(body, der...)
	}
	// The conventional forms follow crypto/x509's order so that a
	// certificate issued through this path differs from one issued by
	// x509.CreateCertificate only by the otherName entries.
	for _, name := range names.DNSNames {
		der, err := generalNameString(idDNSName, "DNS name", name)
		if err != nil {
			return pkix.Extension{}, false, err
		}
		body = append(body, der...)
	}
	for _, email := range names.EmailAddresses {
		der, err := generalNameString(idRFC822Name, "email address", email)
		if err != nil {
			return pkix.Extension{}, false, err
		}
		body = append(body, der...)
	}
	for _, ip := range names.IPAddresses {
		der, err := generalNameIP(ip)
		if err != nil {
			return pkix.Extension{}, false, err
		}
		body = append(body, der...)
	}
	for _, uri := range names.URIs {
		if uri == nil {
			return pkix.Extension{}, false, errors.New("ca: nil URI in subject alternative names")
		}
		der, err := generalNameString(idURI, "URI", uri.String())
		if err != nil {
			return pkix.Extension{}, false, err
		}
		body = append(body, der...)
	}
	return pkix.Extension{Id: oidSubjectAltName, Critical: subjectEmpty, Value: tlv(idSequence, body)}, true, nil
}

// tlv encodes one DER value: the identifier octet, the length in its
// shortest definite form, then the body. The package writes these by hand
// because encoding/asn1 offers no way to say "this tag around bytes I have
// already encoded" without an error return that the fixed tags here can
// never produce.
func tlv(identifier byte, body []byte) []byte {
	der := make([]byte, 0, len(body)+6)
	der = append(der, identifier)
	if n := len(body); n < 0x80 {
		der = append(der, byte(n))
	} else {
		// The long form is the count of length octets, then the length
		// big-endian with no leading zero. An int needs at most eight
		// octets, so the count always fits in the low bits of the marker.
		var length [8]byte
		var used byte
		for v := n; v > 0; v >>= 8 {
			length[len(length)-1-int(used)] = byte(v)
			used++
		}
		der = append(der, 0x80|used)
		der = append(der, length[len(length)-int(used):]...)
	}
	return append(der, body...)
}

// der encodes the otherName as a GeneralName: [0] IMPLICIT SEQUENCE {
// type-id OBJECT IDENTIFIER, value [0] EXPLICIT ANY }.
func (o OtherName) der() ([]byte, error) {
	if len(o.ID) == 0 {
		return nil, errors.New("ca: otherName has no type identifier")
	}
	if len(o.Value) == 0 {
		return nil, errors.New("ca: otherName has no value")
	}
	id, err := asn1.Marshal(o.ID)
	if err != nil {
		return nil, fmt.Errorf("ca: encode otherName type %v: %w", o.ID, err)
	}
	// The value is already DER, so it only needs the explicit [0] wrapper
	// that RFC 5280 puts around it.
	return tlv(idOtherName, append(id, tlv(idExplicitValue, o.Value)...)), nil
}

// generalNameString encodes one of the IA5String GeneralName forms. The
// kind names the form in any error, because a caller handed a rejected
// value has no other way to tell which one it was.
func generalNameString(identifier byte, kind, value string) ([]byte, error) {
	for _, r := range value {
		if r > 127 {
			return nil, fmt.Errorf("ca: %s %q is not an IA5 string", kind, value)
		}
	}
	return tlv(identifier, []byte(value)), nil
}

// generalNameIP encodes an iPAddress GeneralName, which RFC 5280 requires
// to be the bare four or sixteen address octets.
func generalNameIP(ip net.IP) ([]byte, error) {
	octets := ip
	if v4 := ip.To4(); v4 != nil {
		octets = v4
	}
	if len(octets) != net.IPv4len && len(octets) != net.IPv6len {
		return nil, fmt.Errorf("ca: IP address %v is neither four nor sixteen octets", []byte(ip))
	}
	return tlv(idIPAddress, octets), nil
}

// ParseOtherNames returns the otherName entries of a certificate's
// subjectAltName extension.
func ParseOtherNames(cert *x509.Certificate) ([]OtherName, error) {
	if cert == nil {
		return nil, errors.New("ca: nil certificate")
	}
	for _, ext := range cert.Extensions {
		if ext.Id.Equal(oidSubjectAltName) {
			return parseSANOtherNames(ext.Value)
		}
	}
	return nil, nil
}

// parseSANOtherNames walks a subjectAltName value and keeps the otherName
// entries. The conventional forms are skipped rather than rejected,
// because crypto/x509 has already decoded those.
func parseSANOtherNames(der []byte) ([]OtherName, error) {
	var seq asn1.RawValue
	rest, err := asn1.Unmarshal(der, &seq)
	if err != nil {
		return nil, fmt.Errorf("ca: parse subject alternative names: %w", err)
	}
	if len(rest) != 0 {
		return nil, errors.New("ca: trailing data after subject alternative names")
	}
	if seq.Class != asn1.ClassUniversal || seq.Tag != asn1.TagSequence || !seq.IsCompound {
		return nil, errors.New("ca: subject alternative names are not a sequence")
	}
	var names []OtherName
	body := seq.Bytes
	for len(body) > 0 {
		var name asn1.RawValue
		body, err = asn1.Unmarshal(body, &name)
		if err != nil {
			return nil, fmt.Errorf("ca: parse general name: %w", err)
		}
		if name.Class != asn1.ClassContextSpecific || name.Tag != tagZero || !name.IsCompound {
			continue
		}
		other, err := parseOtherName(name.Bytes)
		if err != nil {
			return nil, err
		}
		names = append(names, other)
	}
	return names, nil
}

// parseOtherName decodes the content of an otherName GeneralName, which
// is implicitly tagged and so arrives without its SEQUENCE header.
func parseOtherName(der []byte) (OtherName, error) {
	var id asn1.ObjectIdentifier
	rest, err := asn1.Unmarshal(der, &id)
	if err != nil {
		return OtherName{}, fmt.Errorf("ca: parse otherName type: %w", err)
	}
	var wrapper asn1.RawValue
	trailing, err := asn1.Unmarshal(rest, &wrapper)
	if err != nil {
		return OtherName{}, fmt.Errorf("ca: parse otherName value: %w", err)
	}
	if len(trailing) != 0 {
		return OtherName{}, errors.New("ca: trailing data after otherName value")
	}
	if wrapper.Class != asn1.ClassContextSpecific || wrapper.Tag != tagZero || !wrapper.IsCompound {
		return OtherName{}, errors.New("ca: otherName value is not tagged [0] EXPLICIT")
	}
	return OtherName{ID: id, Value: wrapper.Bytes}, nil
}

// ParsePermanentIdentifier returns the identifier value of the first RFC
// 4043 otherName, and whether one was present.
func ParsePermanentIdentifier(cert *x509.Certificate) (string, bool, error) {
	names, err := ParseOtherNames(cert)
	if err != nil {
		return "", false, err
	}
	for _, name := range names {
		if !name.ID.Equal(OIDPermanentIdentifier) {
			continue
		}
		var value permanentIdentifier
		rest, err := asn1.Unmarshal(name.Value, &value)
		if err != nil {
			return "", false, fmt.Errorf("ca: parse permanent identifier: %w", err)
		}
		if len(rest) != 0 {
			return "", false, errors.New("ca: trailing data after permanent identifier")
		}
		return value.IdentifierValue, true, nil
	}
	return "", false, nil
}
