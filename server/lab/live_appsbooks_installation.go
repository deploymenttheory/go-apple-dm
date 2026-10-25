package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"reflect"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/applications/managedcontent"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/inventory"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	schemaddm "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/status"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

type appsInstallation struct {
	Operation     string `json:"operation"`
	BundleID      string `json:"bundle_id"`
	Version       string `json:"version"`
	Platform      string `json:"platform"`
	Identity      string `json:"identity"`
	MaxAgeSeconds int    `json:"max_age_seconds"`
}

func appsInstallationConfig(s *Session) (appsInstallation, error) {
	var c appsInstallation
	b, err := appsRead(s, "installation.json")
	if err != nil {
		return c, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Operation == "" || c.BundleID == "" || c.Version == "" || c.Platform == "" || (c.Identity != "acme" && c.Identity != "scep") || c.MaxAgeSeconds < 1 || c.MaxAgeSeconds > 3600 {
		return c, fmt.Errorf("%w: explicit installation operation, bundle, platform, identity and freshness bound required", ErrBlocked)
	}
	return c, nil
}

// appsInstallationSource binds the privileged native response to the exact
// operation dispatch. ACK, publication and stale historical processing fail.
func appsInstallationSource(op managedcontent.Operation, e managedcontent.Evidence, c appsInstallation, now time.Time) error {
	d, o := e.Dispatch, e.Observation
	if op.ID != c.Operation || op.Phase != managedcontent.Dispatched || d == nil || o == nil || op.Dispatch == nil ||
		d.ID != op.Dispatch.ID || d.Digest != op.Dispatch.Digest || d.Method != op.Plan.Method || d.Content != op.Plan.Content || d.Enrollment != op.Plan.Target.Enrollment || d.Serial != op.Plan.Target.Serial || d.ClientUserID != op.Plan.Target.ClientUserID ||
		o.ID != d.ID || o.Digest != d.Digest || o.Enrollment != d.Enrollment || o.Method != d.Method || o.ManagementMethod != d.Method || !o.Authoritative || !o.OwnsIdentity || !o.Found || !o.Terminal || !o.Installed || len(o.Raw) == 0 ||
		o.ObservedAt.IsZero() || o.ObservedAt.After(now) || now.Sub(o.ObservedAt) > time.Duration(c.MaxAgeSeconds)*time.Second {
		return fmt.Errorf("%w: fresh owned native installation provenance required", ErrBlocked)
	}
	if d.Method == managedcontent.MDM {
		var provenance struct {
			BundleID             string `json:"bundleId"`
			InstallationResponse []byte `json:"installationResponse"`
			CurrentReport        []byte `json:"currentReport"`
		}
		var response commands.InstallApplicationResponse
		var current commands.InstalledApplicationListResponse
		if !o.CommandAcknowledged || len(d.MDM) == 0 || json.Unmarshal(o.Raw, &provenance) != nil || provenance.BundleID != c.BundleID || plist.Unmarshal(provenance.InstallationResponse, &response) != nil || response.Identifier == nil || *response.Identifier != c.BundleID || response.State == nil || *response.State != "Managed" || plist.Unmarshal(provenance.CurrentReport, &current) != nil {
			return fmt.Errorf("%w: actual managed-app response with matching bundle required", ErrBlocked)
		}
		matches := 0
		for _, app := range current.InstalledApplicationList {
			if app.Identifier != nil && *app.Identifier == c.BundleID {
				if app.Version == nil || *app.Version != c.Version || app.Installing == nil || *app.Installing {
					return fmt.Errorf("%w: current installed app version required", ErrBlocked)
				}
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("%w: current installed app version required", ErrBlocked)
		}
	} else if d.Method == managedcontent.DDM {
		var apps status.AppManagedList
		if !o.DeclarationPublished || json.Unmarshal(o.Raw, &apps) != nil {
			return fmt.Errorf("%w: current declarative managed-app status required", ErrBlocked)
		}
		matches := 0
		for _, app := range apps.Value {
			if app.Identifier != c.BundleID {
				continue
			}
			if app.State == nil || *app.State != "managed" || app.DeclarationIdentifier == nil || app.Removed != nil && *app.Removed || app.Version == nil || *app.Version != c.Version {
				return fmt.Errorf("%w: current declarative managed-app status required", ErrBlocked)
			}
			for _, doc := range d.Declarations {
				if doc.Identifier == *app.DeclarationIdentifier {
					matches++
				}
			}
		}
		if matches != 1 {
			return appsError(context.Background(), "declaration provenance")
		}
	} else {
		return appsError(context.Background(), "installation method")
	}
	return nil
}

// appsNativeQuery uses generated target support and the ordinary tracked MDM
// query path. Its command ACK is retained separately from the query's app facts.
func appsNativeQuery(ctx context.Context, s *Session, path string, payload commands.Command) ([]byte, error) {
	cmd, err := mdm.NewCommand(payload)
	if err != nil {
		return nil, appsError(ctx, "native query construction")
	}
	b, err := Command(ctx, s.Env, path, cmd, s.Evidence)
	if err != nil {
		return nil, appsError(ctx, "native query push and acknowledgment")
	}
	return b, nil
}

// liveAppsBooksInstallation only inspects an operator-prepared app. It does not
// install, remove, assign or revoke it, and cannot assert environment restoration.
func liveAppsBooksInstallation(ctx context.Context, session *Session) (result error) {
	s, _, err := appsScope(session, "installation")
	if err != nil {
		return err
	}
	platforms := map[string]string{"macOS": "unexercised", "iOS": "unexercised", "iPadOS": "unexercised", "visionOS": "unexercised", "tvOS": "unexercised"}
	checks := map[string]any{"source_revision": appsRevision(), "mode": s.Env.Mode, "platforms": platforms, "licensing_mutations": false, "installation_mutations": false, "cleanup": "not_applicable_no_app_or_license_changes"}
	defer func() { result = errors.Join(result, appsSave(s, "assertions.json", checks)) }()
	c, err := appsInstallationConfig(s)
	if err != nil {
		return err
	}
	if _, ok := platforms[c.Platform]; !ok {
		return fmt.Errorf("%w: supported explicit platform required", ErrBlocked)
	}
	if s.Info.Kind != target.KindDevice || s.Info.UDID == "" {
		return fmt.Errorf("%w: selected authenticated device target required", ErrBlocked)
	}
	a, err := appsOpen(ctx, s)
	if err != nil {
		return err
	}
	base := "/managed-content/operations/" + url.PathEscape(c.Operation)
	var op managedcontent.Operation
	var evidence managedcontent.Evidence
	if err = appsRequest(ctx, s, "GET", base, nil, &op); err != nil {
		return err
	}
	if err = appsRequest(ctx, s, "GET", base+"/evidence", nil, &evidence); err != nil {
		return err
	}
	if op.Plan.LocationID != a.config.Location || op.LocationUID != a.config.UID || op.MDMID != a.config.MDMID || op.Plan.Content.Asset != a.config.Asset || op.Plan.Content.Kind != "App" || op.Plan.Target.Serial != a.config.Serial || op.Plan.Target.ClientUserID != a.config.User {
		return appsError(ctx, "designated content operation")
	}
	if err = appsInstallationSource(op, evidence, c, time.Now()); err != nil {
		return err
	}
	id := op.Plan.Target.Enrollment
	if id.Validate() != nil {
		return appsError(ctx, "native enrollment locator")
	}
	deviceID := id.Device()
	device := deviceID.ID
	if device != s.Info.UDID || id.Channel.IsUser() && (s.Info.UserID == "" || id.ID != device+":"+s.Info.UserID) {
		return appsError(ctx, "selected native enrollment")
	}
	devicePath := "/enrollments/" + deviceID.Channel.String() + "/" + url.PathEscape(device)
	path := devicePath
	var identity adeEnrollmentEvidence
	if err = appsRequest(ctx, s, "GET", path+"/enrollment-evidence", nil, &identity); err != nil {
		return err
	}
	if identity.ID != device || identity.Identity != c.Identity || identity.Certificate == "" || !identity.Enabled || identity.AuthenticatedAt.IsZero() || identity.TokenUpdatedAt.IsZero() || !time.Now().Before(identity.NotAfter) {
		return fmt.Errorf("%w: authenticated current native identity required", ErrBlocked)
	}
	queries := []string{"OSVersion", "BuildVersion", "ProductName", "IsSupervised"}
	if a.config.Serial != "" {
		queries = append(queries, "SerialNumber")
	}
	b, err := appsNativeQuery(ctx, s, path, &commands.DeviceInformation{Queries: queries})
	if err != nil {
		return err
	}
	var native struct {
		QueryResponses struct {
			OSVersion, BuildVersion, ProductName, SerialNumber string
			IsSupervised                                       *bool
		}
	}
	if plist.Unmarshal(b, &native) != nil || native.QueryResponses.OSVersion == "" || native.QueryResponses.BuildVersion == "" || adeProductPlatform(native.QueryResponses.ProductName) != c.Platform || a.config.Serial != "" && inventory.CanonicalSerial(native.QueryResponses.SerialNumber) != inventory.CanonicalSerial(a.config.Serial) {
		return appsError(ctx, "fresh native platform and serial")
	}
	version, err := osversion.Parse(native.QueryResponses.OSVersion)
	if err != nil {
		return appsError(ctx, "native OS version")
	}
	os, _ := adePlatform(c.Platform)
	channel := support.ChannelDevice
	if id.Channel.IsUser() {
		path = "/enrollments/" + id.Channel.String() + "/" + url.PathEscape(id.ID)
		channel = support.ChannelUser
	}
	currentTarget := support.Target{OS: os, Version: version, Channel: channel, UserEnrollment: deviceID.Channel == mdm.ChannelUserEnrollmentDevice, DEP: identity.EnrollmentMethod == "automated-device-enrollment"}
	if native.QueryResponses.IsSupervised != nil {
		currentTarget.Supervised = *native.QueryResponses.IsSupervised
	}
	feature := commands.Support("InstallApplication")
	if op.Plan.Method == managedcontent.DDM {
		feature = schemaddm.Support("AppManaged")
	}
	if feature == nil || !feature.Check(currentTarget).Supported {
		return fmt.Errorf("%w: native app delivery unsupported for current target", ErrBlocked)
	}
	list := &commands.InstalledApplicationList{Identifiers: []string{c.BundleID}}
	if entry := commands.Support("InstalledApplicationList.Items"); entry != nil && entry.Check(currentTarget).Supported {
		list.Items = []string{"Identifier", "Version", "Installing"}
	}
	if list.Validate(currentTarget) != nil {
		return fmt.Errorf("%w: native installed-app query unsupported", ErrBlocked)
	}
	b, err = appsNativeQuery(ctx, s, path, list)
	if err != nil {
		return err
	}
	var installed commands.InstalledApplicationListResponse
	if plist.Unmarshal(b, &installed) != nil || len(installed.InstalledApplicationList) != 1 {
		return fmt.Errorf("%w: current app installation unobserved", ErrBlocked)
	}
	app := installed.InstalledApplicationList[0]
	if app.Identifier == nil || *app.Identifier != c.BundleID || app.Version == nil || *app.Version != c.Version || app.Installing == nil || *app.Installing {
		return fmt.Errorf("%w: actual installed app and version required", ErrBlocked)
	}
	if op.Plan.Method == managedcontent.MDM {
		managed := &commands.ManagedApplicationList{Identifiers: []string{c.BundleID}}
		checks["managed_list"] = "unsupported"
		if managed.Validate(currentTarget) == nil {
			b, err = appsNativeQuery(ctx, s, path, managed)
			if err != nil {
				return err
			}
			var state commands.ManagedApplicationListResponse
			if plist.Unmarshal(b, &state) != nil || len(state.ManagedApplicationList) != 1 || state.ManagedApplicationList[c.BundleID].Status != "Managed" {
				return fmt.Errorf("%w: current managed-app installation unobserved", ErrBlocked)
			}
			checks["managed_list"] = "passed"
		}
	}
	var fresh adeEnrollmentEvidence
	if err = appsRequest(ctx, s, "GET", devicePath+"/enrollment-evidence", nil, &fresh); err != nil {
		return err
	}
	if !reflect.DeepEqual(identity, fresh) {
		return appsError(ctx, "native identity continuity")
	}
	if err = appsSave(s, "operation.json", op); err != nil {
		return err
	}
	if err = appsSave(s, "native-evidence.json", evidence); err != nil {
		return err
	}
	platforms[c.Platform] = "passed"
	if s.Env.Mode == "simulated" || s.Info.Virtual {
		platforms[c.Platform] = "simulated"
	}
	checks["app_version"], checks["os_version"], checks["build_version"] = *app.Version, native.QueryResponses.OSVersion, native.QueryResponses.BuildVersion
	return nil
}
