package lab

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/correlation"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	schemaddm "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/status"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

type adeEnrollmentEvidence struct {
	ID, EnrollmentMethod, Serial, UDID, EnrollmentID string
	Identity, Certificate                            string
	Enabled                                          bool
	NotAfter, AuthenticatedAt, TokenUpdatedAt        time.Time
}

type adeHardwareResult struct {
	Status           string `json:"status"`
	ADEIssuance      string `json:"ade_issuance"`
	NativeCheckin    string `json:"native_checkin"`
	PushAndMDM       string `json:"push_and_mdm"`
	EffectiveProfile string `json:"effective_profile"`
	DDM              string `json:"ddm"`
	DDMCleanup       string `json:"ddm_cleanup"`
	OSVersion        string `json:"os_version,omitempty"`
	BuildVersion     string `json:"build_version,omitempty"`
	Supervised       *bool  `json:"supervised"`
}

// adeIssuance requires stored provenance from the actual ADE issuance flow. A
// certificate, device channel or successful HTTP request alone cannot imply ADE.
func adeIssuance(ctx context.Context, s *Session, c adeAcceptance) (adeEnrollmentEvidence, error) {
	var e adeEnrollmentEvidence
	path := "/enrollments/device/" + url.PathEscape(c.DeviceID) + "/enrollment-evidence"
	if err := adeRequest(ctx, s, "GET", path, nil, &e); err != nil {
		return e, err
	}
	if e.ID != c.DeviceID || e.EnrollmentMethod != string(correlation.AutomatedDeviceEnrollment) ||
		inventory.CanonicalSerial(e.Serial) != c.Serial || e.Certificate == "" || !e.Enabled ||
		e.AuthenticatedAt.IsZero() || e.TokenUpdatedAt.IsZero() || !time.Now().Before(e.NotAfter) ||
		(e.UDID == "" && e.EnrollmentID == "") || e.UDID != "" && e.UDID != c.DeviceID || e.EnrollmentID != "" && e.EnrollmentID != c.DeviceID {
		return e, fmt.Errorf("%w: completed, bound ADE identity and native check-ins required", ErrBlocked)
	}
	if (c.Identity != "acme" && c.Identity != "scep") || e.Identity != c.Identity {
		return e, fmt.Errorf("%w: designated ADE identity method differs or is unexercised", ErrBlocked)
	}
	return e, nil
}

// adeProductPlatform preserves iPadOS as a separate acceptance dimension using
// the existing product-type contract, rather than treating all iOS devices alike.
func adeProductPlatform(product string) string {
	switch ade.PlatformFromProduct(product) {
	case ade.PlatformMac:
		return "macOS"
	case ade.PlatformIPhone, ade.PlatformIPod:
		return "iOS"
	case ade.PlatformIPad:
		return "iPadOS"
	case ade.PlatformAppleTV:
		return "tvOS"
	case ade.PlatformRealityDevice:
		return "visionOS"
	default:
		return ""
	}
}

// adeDDMAvailable checks the actual status-subscription feature on the observed
// platform/version. tvOS supports these exchanges from its schema-defined floor;
// declarative app-delivery support is a different feature.
func adeDDMAvailable(platform, version string, supervised *bool) (bool, error) {
	os, known := adePlatform(platform)
	v, err := osversion.Parse(version)
	if !known || err != nil {
		return false, fmt.Errorf("%w: native platform/version is unknown", ErrBlocked)
	}
	t := support.Target{OS: os, Version: v, Channel: support.ChannelDevice, DEP: true}
	if supervised != nil {
		t.Supervised = *supervised
	}
	for _, entry := range []*support.Entry{
		schemaddm.Support("ManagementStatusSubscriptions"), schemaddm.Support("ActivationSimple"),
		status.Support("DeviceOperatingSystemVersion"), status.Support("DeviceOperatingSystemBuildVersion"),
	} {
		if entry == nil {
			return false, fmt.Errorf("%w: DDM feature support is unknown", ErrBlocked)
		}
		if !entry.Check(t).Supported {
			if supervised == nil {
				withSupervision := t
				withSupervision.Supervised = true
				if entry.Check(withSupervision).Supported {
					return false, fmt.Errorf("%w: fresh native supervision evidence required for DDM", ErrBlocked)
				}
			}
			return false, nil
		}
	}
	return true, nil
}

// liveADEHardware validates one designated device while retaining unexercised
// results for every other requested platform. API acceptance cannot satisfy it.
func liveADEHardware(ctx context.Context, session *Session) (result error) {
	s, _, err := adeScope(session, "hardware")
	if err != nil {
		return err
	}
	platforms := map[string]*adeHardwareResult{}
	for _, platform := range []string{"macOS", "iOS", "iPadOS", "visionOS", "tvOS"} {
		platforms[platform] = &adeHardwareResult{Status: "unexercised", ADEIssuance: "unexercised", NativeCheckin: "unexercised", PushAndMDM: "unexercised", EffectiveProfile: "unexercised", DDM: "unexercised", DDMCleanup: "unexercised"}
	}
	defer func() {
		result = errors.Join(result, adeSave(s, "platform-results.json", map[string]any{"mode": s.Env.Mode, "target": s.Info, "platforms": platforms}))
	}()
	c, err := adeConfig(s)
	if err != nil {
		return err
	}
	r, known := platforms[c.Platform]
	if !known || c.DeviceID == "" {
		return fmt.Errorf("%w: designated platform and enrolled device address required", ErrBlocked)
	}
	if s.Info.UDID != "" && s.Info.UDID != c.DeviceID || s.Info.Serial != "" && inventory.CanonicalSerial(s.Info.Serial) != c.Serial {
		return fmt.Errorf("%w: ADE configuration and selected target differ", ErrBlocked)
	}
	defer func() {
		if result != nil && !errors.Is(result, ErrBlocked) {
			r.Status = "failed"
		}
	}()
	a, err := adeOpen(ctx, s)
	if err != nil {
		return err
	}
	evidence, err := adeIssuance(ctx, s, c)
	if err != nil {
		return err
	}
	if err = adeSave(s, "ade-issuance.json", evidence); err != nil {
		return err
	}
	r.ADEIssuance, r.NativeCheckin = "passed", "passed"
	device, err := a.device(ctx)
	if err != nil {
		return err
	}
	profileUUID, err := profileState(device)
	if err != nil || profileUUID == "" {
		return fmt.Errorf("%w: effective ADE profile remains unexercised", ErrBlocked)
	}
	var selection adeSelection
	if err = adeRequest(ctx, s, "GET", adeBase(c)+"/targets", nil, &selection); err != nil {
		return err
	}
	desired, explicit := selection.Serials[c.Serial]
	if !explicit {
		desired = selection.Default
	}
	if desired == "" || desired != profileUUID {
		return fmt.Errorf("%w: effective desired and Apple ADE profiles differ", ErrBlocked)
	}
	if err = adeSave(s, "effective-profile.json", device); err != nil {
		return err
	}
	r.EffectiveProfile = "passed"
	cmd, err := mdm.NewCommand(&commands.DeviceInformation{Queries: []string{"SerialNumber", "OSVersion", "BuildVersion", "ProductName", "IsSupervised"}})
	if err != nil {
		return adeError(ctx, "native command construction")
	}
	response, err := Command(ctx, s.Env, "/enrollments/device/"+url.PathEscape(c.DeviceID), cmd, s.Evidence)
	if err != nil {
		return adeError(ctx, "native push and acknowledgment")
	}
	var reply struct {
		QueryResponses struct {
			SerialNumber, OSVersion, BuildVersion, ProductName string
			IsSupervised                                       *bool
		}
	}
	if plist.Unmarshal(response, &reply) != nil || inventory.CanonicalSerial(reply.QueryResponses.SerialNumber) != c.Serial ||
		reply.QueryResponses.OSVersion == "" || reply.QueryResponses.BuildVersion == "" || adeProductPlatform(reply.QueryResponses.ProductName) != c.Platform {
		return adeError(ctx, "native serial/platform inventory")
	}
	fresh, err := adeIssuance(ctx, s, c)
	if err != nil || fresh.Certificate != evidence.Certificate {
		return adeError(ctx, "native identity continuity")
	}
	r.PushAndMDM = "passed"
	r.OSVersion, r.BuildVersion, r.Supervised = reply.QueryResponses.OSVersion, reply.QueryResponses.BuildVersion, reply.QueryResponses.IsSupervised
	available, err := adeDDMAvailable(c.Platform, reply.QueryResponses.OSVersion, reply.QueryResponses.IsSupervised)
	if err != nil {
		return err
	}
	if !available {
		r.DDM, r.Status = "unsupported", "passed"
		return nil
	}
	if !c.DDM {
		return fmt.Errorf("%w: native ADE MDM passed; applicable DDM remains unexercised", ErrBlocked)
	}
	phases := liveDDMPhases{}
	err = liveDDMWithPhases(ctx, s.Env, c.DeviceID, s.Evidence, &phases)
	r.DDM, r.DDMCleanup = phases.Exchange, phases.Cleanup
	if err != nil {
		return adeError(ctx, "native DDM exchange or cleanup")
	}
	fresh, err = adeIssuance(ctx, s, c)
	if err != nil || fresh.Certificate != evidence.Certificate {
		return adeError(ctx, "post-DDM identity continuity")
	}
	r.DDM, r.Status = "passed", "passed"
	return nil
}
