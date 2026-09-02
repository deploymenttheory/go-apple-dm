package ade

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/smallstep/pkcs7"

	"github.com/deploymenttheory/go-apple-mdm/cms"
	"github.com/deploymenttheory/go-apple-mdm/plist"
	"github.com/deploymenttheory/go-apple-mdm/schema/other"
	"github.com/deploymenttheory/go-apple-mdm/schema/support"
)

// MachineInfo is the plist a device signs to identify itself; the wire
// type is the generated schema type.
type MachineInfo = other.MachineInfo

// Where the blob arrives.
const (
	// HeaderName carries MachineInfo on the web view GET.
	HeaderName = "x-apple-aspen-deviceinfo"
	// QueryParam carries it when a redirect chain cannot keep the header.
	QueryParam = "deviceinfo"
	// ContentTypePKCS7 is the body content type on the token-based POST.
	ContentTypePKCS7 = "application/pkcs7-signature"
	// DefaultMaxBytes bounds the blob and the request body.
	DefaultMaxBytes = 64 << 10
)

// Errors returned by this package. Handlers map them to statuses: a
// malformed blob is 400, an unverified signature 401, an unknown signer
// 403 or 401, and nothing else about decoding is ever 500.
var (
	ErrNoMachineInfo = errors.New("ade: no MachineInfo in request")
	ErrTooLarge      = errors.New("ade: MachineInfo too large")
	ErrMalformed     = errors.New("ade: malformed MachineInfo")
	ErrUnverified    = errors.New("ade: MachineInfo signature not verified")
	ErrUnknownSigner = errors.New("ade: MachineInfo signer not trusted")
	ErrPresence      = errors.New("ade: MachineInfo presence rules")
)

// Origin says which request form carried the blob.
type Origin string

// Origins.
const (
	OriginHeader Origin = "header"
	OriginQuery  Origin = "query"
	OriginBody   Origin = "body"
)

// ParseOptions control ParseMachineInfo and Verify. The zero value
// verifies against Apple's chain.
type ParseOptions struct {
	// MaxBytes bounds the decoded blob and the body; default DefaultMaxBytes.
	MaxBytes int64
	// Anchors are the trust anchors the signer must chain to; default
	// AppleAnchors(). They are certificates rather than a pool because the
	// path is built without validity windows and a pool cannot be walked.
	Anchors []*x509.Certificate
	// EnforceValidity applies certificate validity windows at Now. Off by
	// default: Apple's Device CA expired in 2014 and still issues.
	EnforceValidity bool
	// Audit logs a verification failure and returns the MachineInfo with
	// Verified false instead of an error. It is per handler, never global.
	Audit bool
	// Now supplies the time for EnforceValidity; default time.Now.
	Now func() time.Time
	// Logger receives audit messages; default slog.Default.
	Logger *slog.Logger
}

func (o ParseOptions) maxBytes() int64 {
	if o.MaxBytes > 0 {
		return o.MaxBytes
	}
	return DefaultMaxBytes
}

func (o ParseOptions) anchors() []*x509.Certificate {
	if o.Anchors != nil {
		return o.Anchors
	}
	return AppleAnchors()
}

func (o ParseOptions) logger() *slog.Logger {
	if o.Logger != nil {
		return o.Logger
	}
	return slog.Default()
}

// Parsed is a MachineInfo read from a request.
type Parsed struct {
	MachineInfo
	// Origin is the request form that carried the blob.
	Origin Origin
	// Signer is the device certificate; nil when Verified is false.
	Signer *x509.Certificate
	// Raw is the CMS blob as received (after base64 decoding).
	Raw []byte
	// Verified is false only under Audit when verification failed.
	Verified bool
	// Platform derives from PRODUCT.
	Platform Platform
}

// Validate applies the presence rules; see the package function.
func (p *Parsed) Validate(userEnrollment bool) error {
	return Validate(&p.MachineInfo, userEnrollment)
}

// Target describes the device for schema checks: the OS family from
// PRODUCT and the version from OS_VERSION when present.
func (p *Parsed) Target() support.Target {
	t := support.Target{OS: support.OSFromProduct(p.PRODUCT)}
	if v, err := support.ParseVersion(p.OSVERSION); err == nil {
		t.Version = v
	}
	return t
}

// ParseMachineInfo finds the blob in r (the header, then the query
// parameter, then a POST or PUT body), verifies it, and decodes the
// plist. Presence rules are not applied here because they depend on the
// enrollment kind; call Validate.
func ParseMachineInfo(r *http.Request, o ParseOptions) (*Parsed, error) {
	der, origin, err := locate(r, o.maxBytes())
	if err != nil {
		return nil, err
	}
	content, signer, verr := Verify(der, o)
	if verr != nil {
		if !o.Audit || errors.Is(verr, ErrMalformed) {
			return nil, verr
		}
		o.logger().WarnContext(r.Context(), "ade: MachineInfo not verified, audit mode", "error", verr, "origin", string(origin), "remote", r.RemoteAddr)
		if content, err = unverifiedContent(der); err != nil {
			return nil, err
		}
	}
	var info MachineInfo
	if err := (plist.Decoder{MaxBytes: int(o.maxBytes())}).Unmarshal(content, &info); err != nil {
		return nil, fmt.Errorf("%w: plist: %w", ErrMalformed, err)
	}
	return &Parsed{
		MachineInfo: info, Origin: origin, Signer: signer, Raw: der,
		Verified: verr == nil, Platform: PlatformFromProduct(info.PRODUCT),
	}, nil
}

// Verify checks the CMS blob against the options and returns the plist
// and the device certificate. Errors are ErrMalformed, ErrUnverified, or
// ErrUnknownSigner, each wrapping the cms error.
func Verify(der []byte, o ParseOptions) ([]byte, *x509.Certificate, error) {
	vo := cms.VerifyAttachedOptions{
		VerifyOptions:  cms.VerifyOptions{Now: o.Now},
		IgnoreValidity: !o.EnforceValidity,
		Anchors:        o.anchors(),
	}
	content, signer, err := cms.VerifyAttachedWith(der, vo)
	if err != nil {
		switch {
		case errors.Is(err, cms.ErrChain):
			return nil, nil, fmt.Errorf("%w: %w", ErrUnknownSigner, err)
		case errors.Is(err, cms.ErrSignature):
			return nil, nil, fmt.Errorf("%w: %w", ErrUnverified, err)
		default:
			return nil, nil, fmt.Errorf("%w: %w", ErrMalformed, err)
		}
	}
	return content, signer, nil
}

// unverifiedContent extracts the embedded plist for audit mode.
func unverifiedContent(der []byte) ([]byte, error) {
	p7, err := pkcs7.Parse(der)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformed, err)
	}
	return p7.Content, nil
}

// locate returns the blob and where it came from.
func locate(r *http.Request, maxBytes int64) ([]byte, Origin, error) {
	if h := r.Header.Get(HeaderName); h != "" {
		der, err := decodeBase64(h, maxBytes)
		return der, OriginHeader, err
	}
	if q := r.URL.Query().Get(QueryParam); q != "" {
		// A '+' in a query value arrives as a space.
		der, err := decodeBase64(strings.ReplaceAll(q, " ", "+"), maxBytes)
		return der, OriginQuery, err
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut {
		return nil, "", ErrNoMachineInfo
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("%w: body: %w", ErrMalformed, err)
	}
	if int64(len(body)) > maxBytes {
		return nil, "", fmt.Errorf("%w: body exceeds %d bytes", ErrTooLarge, maxBytes)
	}
	if len(body) == 0 {
		return nil, "", ErrNoMachineInfo
	}
	if !cms.IsSigned(body) {
		return nil, "", fmt.Errorf("%w: body is not DER", ErrMalformed)
	}
	return body, OriginBody, nil
}

// decodeBase64 accepts standard or URL-safe alphabets with or without
// padding and bounds the result.
func decodeBase64(s string, maxBytes int64) ([]byte, error) {
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
			return -1
		}
		return r
	}, s)
	if int64(len(s)) > maxBytes*4/3+4 {
		return nil, fmt.Errorf("%w: encoded value exceeds %d bytes", ErrTooLarge, maxBytes)
	}
	s = strings.TrimRight(s, "=")
	der, err := base64.RawStdEncoding.DecodeString(s)
	if err != nil {
		if der, err = base64.RawURLEncoding.DecodeString(s); err != nil {
			return nil, fmt.Errorf("%w: base64: %w", ErrMalformed, err)
		}
	}
	if int64(len(der)) > maxBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(der))
	}
	if !cms.IsSigned(der) {
		return nil, fmt.Errorf("%w: not DER", ErrMalformed)
	}
	return der, nil
}

// Validate applies the presence rules from the schema. PRODUCT and
// VERSION are always required. Under device enrollment UDID and SERIAL
// are required; under user enrollment they, IMEI, and MEID are
// forbidden. OS_VERSION is required from iOS 17 and macOS 14, which is
// recognised by the presence of any key introduced with it.
func Validate(m *MachineInfo, userEnrollment bool) error {
	var missing, forbidden []string
	if m.PRODUCT == "" {
		missing = append(missing, "PRODUCT")
	}
	if m.VERSION == "" {
		missing = append(missing, "VERSION")
	}
	if userEnrollment {
		for k, present := range map[string]bool{"UDID": m.UDID != "", "SERIAL": m.SERIAL != "", "IMEI": m.IMEI != nil, "MEID": m.MEID != nil} {
			if present {
				forbidden = append(forbidden, k)
			}
		}
	} else {
		if m.UDID == "" {
			missing = append(missing, "UDID")
		}
		if m.SERIAL == "" {
			missing = append(missing, "SERIAL")
		}
	}
	if m.OSVERSION == "" && requiresOSVersion(m) {
		missing = append(missing, "OS_VERSION")
	}
	if len(missing) == 0 && len(forbidden) == 0 {
		return nil
	}
	return fmt.Errorf("%w: missing %v, forbidden %v", ErrPresence, sorted(missing), sorted(forbidden))
}

func requiresOSVersion(m *MachineInfo) bool {
	return m.MDMCANREQUESTSOFTWAREUPDATE != nil || m.SOFTWAREUPDATEDEVICEID != nil ||
		m.SUPPLEMENTALBUILDVERSION != nil || m.SUPPLEMENTALOSVERSIONEXTRA != nil ||
		m.MDMCANREQUESTPSSOCONFIG != nil || m.MANDATORYSOFTWAREUPDATEREQUIRED != nil
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Platform is the device family a PRODUCT value names.
type Platform string

// Platforms, named as the model-family values Apple uses in service
// discovery. PlatformUnknown is the explicit value for a prefix the
// table does not know.
const (
	PlatformUnknown       Platform = ""
	PlatformIPhone        Platform = "iPhone"
	PlatformIPad          Platform = "iPad"
	PlatformIPod          Platform = "iPod"
	PlatformMac           Platform = "Mac"
	PlatformAppleTV       Platform = "AppleTV"
	PlatformRealityDevice Platform = "RealityDevice"
	PlatformWatch         Platform = "Watch"
)

// productPrefixes is the table PlatformFromProduct consults, in order.
var productPrefixes = []struct {
	prefix   string
	platform Platform
}{
	{"iPhone", PlatformIPhone},
	{"iPad", PlatformIPad},
	{"iPod", PlatformIPod},
	{"AppleTV", PlatformAppleTV},
	{"RealityDevice", PlatformRealityDevice},
	{"Watch", PlatformWatch},
	{"Mac", PlatformMac},
	{"iMac", PlatformMac},
	{"VirtualMac", PlatformMac},
}

// PlatformFromProduct maps a PRODUCT value ("iPhone15,2", "Mac14,7",
// "MacBookPro18,1", "iMac21,1", "AppleTV14,1", "RealityDevice14,1",
// "Watch7,1") to its platform by prefix. Anything else is
// PlatformUnknown; there is no substring matching.
func PlatformFromProduct(product string) Platform {
	for _, e := range productPrefixes {
		if strings.HasPrefix(product, e.prefix) {
			return e.platform
		}
	}
	return PlatformUnknown
}

// String names the platform, "Unknown" for the zero value.
func (p Platform) String() string {
	if p == PlatformUnknown {
		return "Unknown"
	}
	return string(p)
}

// OS is the schema OS family the platform runs; "" for unknown.
func (p Platform) OS() support.OS {
	switch p {
	case PlatformIPhone, PlatformIPad, PlatformIPod:
		return support.IOS
	case PlatformMac:
		return support.MacOS
	case PlatformAppleTV:
		return support.TvOS
	case PlatformRealityDevice:
		return support.VisionOS
	case PlatformWatch:
		return support.WatchOS
	}
	return ""
}
