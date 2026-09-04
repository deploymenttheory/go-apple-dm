package simulator

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	json "encoding/json/v2"

	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/v3/mdmprotocol/profile"
)

// Automated Device Enrollment constants from Apple's documentation.
const (
	// HeaderDeviceInfo carries the CMS-signed MachineInfo on the web view's
	// first request.
	HeaderDeviceInfo = "x-apple-aspen-deviceinfo"
	// ErrorCodeSoftwareUpdateRequired is the 403 body code that makes
	// Setup Assistant update before enrolling.
	ErrorCodeSoftwareUpdateRequired = "com.apple.softwareupdate.required"
)

// ErrADE reports a failure in the ADE flow.
var ErrADE = errors.New("simulator: automated device enrollment")

// SoftwareUpdateRequired is the typed form of Apple's 403 response: the
// device must update to OSVersion (and BuildVersion when given) first.
type SoftwareUpdateRequired struct {
	OSVersion    string
	BuildVersion string
	Message      string
}

func (e *SoftwareUpdateRequired) Error() string {
	return fmt.Sprintf("simulator: software update required: %s %s", e.OSVersion, e.BuildVersion)
}

// ADEOptions drive ADEEnroll.
type ADEOptions struct {
	// Language is the LANGUAGE key (default "en").
	Language string
	// CanRequestSoftwareUpdate sets MDM_CAN_REQUEST_SOFTWARE_UPDATE.
	CanRequestSoftwareUpdate bool
	// SoftwareUpdateDeviceID is SOFTWARE_UPDATE_DEVICE_ID (default the
	// product name).
	SoftwareUpdateDeviceID string
	// Signer signs the MachineInfo; nil uses the device Identity (the
	// built-in Apple certificate in real life).
	Signer *Identity
	// Parse configures profile parsing.
	Parse profile.ParseOptions
	// WebView, when the profile URL is a configuration_web_url, plays the
	// person in the web view: it receives the first response (already
	// carrying x-apple-aspen-deviceinfo) and must return the final
	// application/x-apple-aspen-config response by following the
	// redirects and signing in. Nil means the token-based POST lane only.
	WebView func(ctx context.Context, first *http.Response) (*http.Response, error)
}

// MachineInfo builds the plist Apple documents for the ADE enrollment
// request, from the device's fields.
func (d *Device) MachineInfo(opts ADEOptions) map[string]any {
	lang := opts.Language
	if lang == "" {
		lang = "en"
	}
	sudid := opts.SoftwareUpdateDeviceID
	if sudid == "" {
		sudid = d.ProductName
	}
	return map[string]any{
		"UDID": d.UDID, "SERIAL": d.SerialNumber, "PRODUCT": d.ProductName, "VERSION": d.BuildVersion, "OS_VERSION": d.OSVersion,
		"LANGUAGE": lang, "MDM_CAN_REQUEST_SOFTWARE_UPDATE": opts.CanRequestSoftwareUpdate, "SOFTWARE_UPDATE_DEVICE_ID": sudid,
	}
}

// SignedMachineInfo is the CMS-signed MachineInfo blob.
func (d *Device) SignedMachineInfo(opts ADEOptions) ([]byte, error) {
	raw, err := plist.Marshal(d.MachineInfo(opts))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrADE, err)
	}
	signer := opts.Signer
	if signer == nil {
		signer = d.Identity
	}
	if signer == nil {
		return nil, fmt.Errorf("%w: no identity to sign MachineInfo", ErrADE)
	}
	signed, err := cms.SignAttached(raw, signer.Cert, signer.Key)
	if err != nil {
		return nil, fmt.Errorf("%w: sign: %w", ErrADE, err)
	}
	return signed, nil
}

// ADEEnroll runs Automated Device Enrollment against the DEP profile's
// URL: it posts the signed MachineInfo (token-based lane) or, with
// opts.WebView, opens the web view lane with the x-apple-aspen-deviceinfo
// header; a 403 with the software update body becomes
// *SoftwareUpdateRequired; the returned profile is applied and the device
// enrols.
func (d *Device) ADEEnroll(ctx context.Context, profileURL string, opts ADEOptions) error {
	signed, err := d.SignedMachineInfo(opts)
	if err != nil {
		return err
	}
	var resp *http.Response
	if opts.WebView != nil {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, profileURL, nil)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrADE, err)
		}
		req.Header.Set(HeaderDeviceInfo, base64.StdEncoding.EncodeToString(signed))
		req.Header.Set("User-Agent", "MDM/1.0 go-apple-dm-simulator")
		first, err := d.Client.Do(req)
		if err != nil {
			return fmt.Errorf("%w: web view: %w", ErrADE, err)
		}
		if resp, err = opts.WebView(ctx, first); err != nil {
			return fmt.Errorf("%w: web view: %w", ErrADE, err)
		}
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, profileURL, bytes.NewReader(signed))
		if err != nil {
			return fmt.Errorf("%w: %w", ErrADE, err)
		}
		req.Header.Set("Content-Type", ContentTypeDeviceInfo)
		req.Header.Set("User-Agent", "MDM/1.0 go-apple-dm-simulator")
		if resp, err = d.Client.Do(req); err != nil {
			return fmt.Errorf("%w: enroll: %w", ErrADE, err)
		}
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, accountDrivenMaxBody))
	if resp.StatusCode == http.StatusForbidden {
		if sur := parseSoftwareUpdateRequired(resp.Header.Get("Content-Type"), data); sur != nil {
			return sur
		}
	}
	if resp.StatusCode != http.StatusOK {
		return &HTTPError{Status: resp.StatusCode, Body: data}
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, ContentTypeAspenConfig) {
		return fmt.Errorf("%w: unexpected content type %q", ErrADE, ct)
	}
	if err := d.ApplyProfile(ctx, data, opts.Parse); err != nil {
		return err
	}
	return d.Enroll(ctx)
}

// parseSoftwareUpdateRequired reads Apple's 403 body as JSON or plist.
func parseSoftwareUpdateRequired(contentType string, body []byte) *SoftwareUpdateRequired {
	var doc struct {
		Code    string `json:"code" plist:"code"`
		Message string `json:"message" plist:"message"`
		Details struct {
			OSVersion    string `json:"OSVersion" plist:"OSVersion"`
			BuildVersion string `json:"BuildVersion" plist:"BuildVersion"`
		} `json:"details" plist:"details"`
	}
	var err error
	if strings.Contains(contentType, "json") {
		err = json.Unmarshal(body, &doc)
	} else {
		err = plist.Unmarshal(body, &doc)
	}
	if err != nil || doc.Code != ErrorCodeSoftwareUpdateRequired {
		return nil
	}
	return &SoftwareUpdateRequired{OSVersion: doc.Details.OSVersion, BuildVersion: doc.Details.BuildVersion, Message: doc.Message}
}
