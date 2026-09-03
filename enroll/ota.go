package enroll

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/deploymenttheory/go-apple-mdm/cms"
	"github.com/deploymenttheory/go-apple-mdm/plist"
	"github.com/deploymenttheory/go-apple-mdm/profile"
)

// Over-the-air profile delivery ("profile service"): the device installs
// a small Profile Service profile, POSTs its attributes signed with the
// Apple-issued device certificate (phase 1), receives a profile carrying a
// SCEP payload, enrolls, then POSTs again signed with that identity (phase
// 2) to receive the final MDM enrollment profile.
//
// Apple documentation:
// https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles
// https://developer.apple.com/library/archive/documentation/NetworkingInternet/Conceptual/iPhoneOTAConfiguration/

// PayloadTypeProfileService is the payload type of the initial OTA profile.
const PayloadTypeProfileService = "Profile Service"

// ContentTypeProfile is the response content type for a profile.
const ContentTypeProfile = "application/x-apple-aspen-config"

// ErrOTA is returned by the OTA service.
var ErrOTA = errors.New("enroll: ota")

// Device attribute names a Profile Service profile may request.
const (
	AttrUDID    = "UDID"
	AttrVersion = "VERSION"
	AttrProduct = "PRODUCT"
	AttrSerial  = "SERIAL"
	AttrIMEI    = "IMEI"
	AttrMEID    = "MEID"
	AttrICCID   = "ICCID"
)

// AttrChallenge is the key the device echoes the profile's Challenge back
// under. It is not a device attribute a profile requests, so it is not part
// of DefaultDeviceAttributes, but it is read from the same dictionary.
const AttrChallenge = "CHALLENGE"

// DefaultDeviceAttributes is a sensible request list.
var DefaultDeviceAttributes = []string{AttrUDID, AttrVersion, AttrProduct, AttrSerial}

// OTAProfile builds the Profile Service profile the device installs first.
type OTAProfile struct {
	Identifier   string
	DisplayName  string
	Organization string
	Description  string
	UUID         string
	PayloadUUID  string
	// URL the device POSTs its attributes to.
	URL string
	// Challenge is echoed back by the device in phase 1.
	Challenge        string
	DeviceAttributes []string
}

// Build assembles the profile.
func (o OTAProfile) Build() (*profile.Profile, error) {
	if o.Identifier == "" || o.URL == "" {
		return nil, fmt.Errorf("%w: Identifier and URL are required", ErrOTA)
	}
	attrs := o.DeviceAttributes
	if len(attrs) == 0 {
		attrs = DefaultDeviceAttributes
	}
	content := map[string]any{"URL": o.URL, "DeviceAttributes": attrs}
	if o.Challenge != "" {
		content["Challenge"] = o.Challenge
	}
	p := &profile.Profile{
		Identifier: o.Identifier, UUID: orUUID(o.UUID), DisplayName: o.DisplayName,
		Organization: o.Organization, Description: o.Description,
		Payloads: []profile.Payload{{
			Identifier: o.Identifier + ".profile-service", UUID: orUUID(o.PayloadUUID), DisplayName: o.DisplayName,
			Content: &profile.Raw{Type: PayloadTypeProfileService, Keys: map[string]any{"PayloadContent": content}},
		}},
	}
	return p, nil
}

// DeviceAttributes is what the device signs and sends.
type DeviceAttributes struct {
	UDID, Version, Product, Serial, IMEI, MEID, ICCID string
	Challenge                                         string
	// Raw keeps every key received.
	Raw map[string]any
}

// Phase of the OTA flow, derived from which CA the request signature
// chains to.
type Phase int

// Phases.
const (
	PhaseDevice   Phase = 1 // signed by the Apple-issued device certificate
	PhaseIdentity Phase = 2 // signed by the SCEP identity issued in phase 1
)

// OTARequest is a verified request.
type OTARequest struct {
	Phase      Phase
	Attributes DeviceAttributes
	Signer     *x509.Certificate
}

// OTAService serves the profile-service URL.
type OTAService struct {
	// DeviceRoots verify phase 1 (the Apple iPhone Device CA chain).
	DeviceRoots *x509.CertPool
	// IdentityRoots verify phase 2 (the CA behind the SCEP endpoint).
	IdentityRoots *x509.CertPool
	// ClockSkew for signing-time checks; default 5 minutes.
	ClockSkew time.Duration
	// Now for verification; default time.Now.
	Now func() time.Time
	// Authorize vets a verified request: challenge, allow-lists. Nil allows.
	Authorize func(ctx context.Context, r *OTARequest) error
	// Profile returns the profile bytes for the phase: the SCEP-bearing
	// profile for phase 1, the MDM enrollment profile for phase 2.
	Profile func(ctx context.Context, r *OTARequest) ([]byte, error)
	// Logger defaults to slog.Default.
	Logger *slog.Logger
	// MaxBytes bounds the request body; default 64 KiB.
	MaxBytes int64
}

// Verify checks the signed body and classifies the phase.
func (s *OTAService) Verify(body []byte) (*OTARequest, error) {
	if s.DeviceRoots == nil && s.IdentityRoots == nil {
		return nil, fmt.Errorf("%w: no trust roots configured", ErrOTA)
	}
	opts := cms.VerifyOptions{ClockSkew: s.ClockSkew, Now: s.Now}
	if opts.ClockSkew == 0 {
		opts.ClockSkew = 5 * time.Minute
	}
	var (
		content []byte
		signer  *x509.Certificate
		phase   Phase
		err     error
	)
	if s.IdentityRoots != nil {
		opts.Roots = s.IdentityRoots
		if content, signer, err = cms.VerifyAttached(body, opts); err == nil {
			phase = PhaseIdentity
		}
	}
	if phase == 0 && s.DeviceRoots != nil {
		opts.Roots = s.DeviceRoots
		if content, signer, err = cms.VerifyAttached(body, opts); err == nil {
			phase = PhaseDevice
		}
	}
	if phase == 0 {
		return nil, fmt.Errorf("%w: signature: %w", ErrOTA, err)
	}
	var raw map[string]any
	if err := plist.Unmarshal(content, &raw); err != nil {
		return nil, fmt.Errorf("%w: attributes: %w", ErrOTA, err)
	}
	a := DeviceAttributes{Raw: raw}
	a.UDID, a.Version, a.Product, a.Serial = str(raw, AttrUDID), str(raw, AttrVersion), str(raw, AttrProduct), str(raw, AttrSerial)
	a.IMEI, a.MEID, a.ICCID, a.Challenge = str(raw, AttrIMEI), str(raw, AttrMEID), str(raw, AttrICCID), str(raw, AttrChallenge)
	if a.UDID == "" {
		return nil, fmt.Errorf("%w: UDID missing from attributes", ErrOTA)
	}
	return &OTARequest{Phase: phase, Attributes: a, Signer: signer}, nil
}

// Handler serves POST requests from devices.
func (s *OTAService) Handler() http.Handler {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	limit := s.MaxBytes
	if limit <= 0 {
		limit = 64 << 10
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
		if err != nil || int64(len(body)) > limit {
			http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
			return
		}
		req, err := s.Verify(body)
		if err != nil {
			logger.WarnContext(r.Context(), "ota: request rejected", "error", err, "remote", r.RemoteAddr)
			http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
			return
		}
		if s.Authorize != nil {
			if err := s.Authorize(r.Context(), req); err != nil {
				logger.WarnContext(r.Context(), "ota: request not authorized", "error", err, "udid", req.Attributes.UDID, "phase", int(req.Phase))
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
		}
		if s.Profile == nil {
			http.Error(w, http.StatusText(http.StatusNotImplemented), http.StatusNotImplemented)
			return
		}
		out, err := s.Profile(r.Context(), req)
		if err != nil {
			logger.ErrorContext(r.Context(), "ota: profile", "error", err, "udid", req.Attributes.UDID, "phase", int(req.Phase))
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", ContentTypeProfile)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(out)
	})
}

func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}
