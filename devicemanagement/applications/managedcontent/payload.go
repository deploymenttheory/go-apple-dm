package managedcontent

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	json "encoding/json/v2"
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/appsbooks"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	schemaddm "github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// resolve checks an explicit locator against every returned authenticated candidate.
func (w *Workflow) resolve(ctx context.Context, p Plan, mode Mode) (Target, error) {
	candidates, err := w.resolver.Resolve(ctx, p.Target)
	if err != nil {
		return Target{}, err
	}
	var target Target
	count := 0
	for _, candidate := range candidates {
		if candidate.Enrollment == p.Target.Enrollment {
			target = candidate
			count++
		}
	}
	if count != 1 || !target.Authenticated || !target.AllowAppInstall || target.Enrollment.Validate() != nil ||
		(p.Target.Serial != "" && target.Serial != p.Target.Serial) || target.ClientUserID != p.Target.ClientUserID {
		return Target{}, ErrTarget
	}
	if target.Support.OS == "" || target.Support.Version.IsZero() ||
		(target.Support.Channel != support.ChannelDevice && target.Support.Channel != support.ChannelUser) ||
		(target.Support.Channel == support.ChannelUser) != target.Enrollment.Channel.IsUser() ||
		target.Support.UserEnrollment != (target.Enrollment.Channel == mdm.ChannelUserEnrollmentDevice || target.Enrollment.Channel == mdm.ChannelUserEnrollmentUser) {
		return Target{}, ErrTarget
	}
	if target.Enrollment.Channel.IsUser() && target.Serial != "" && target.ScopeID == "" {
		return Target{}, ErrTarget
	}
	if mode != AssignOnly {
		if _, err := buildDispatch("validation", p, target); err != nil {
			return Target{}, err
		}
	}
	target.Context = PlatformContext{OS: target.Support.OS, Version: target.Support.Version.String(), Channel: target.Support.Channel, Supervised: target.Support.Supervised, SharedIPad: target.Support.SharedIPad, UserEnrollment: target.Support.UserEnrollment, ADE: target.Support.DEP, UserApproved: target.Support.UserApproved}
	return target, nil
}

// checkAsset validates native capabilities rather than inferring platform or kind
// from an AdamID. Apple represents iPadOS assets using the iOS platform identifier.
func checkAsset(p Plan, target Target, asset appsbooks.AssetRecord) error {
	if asset.Asset != p.Content.Asset || asset.ProductType != p.Content.Kind {
		return ErrLicense
	}
	if err := asset.CheckAssignment(p.Target.ClientUserID == "", false); err != nil {
		return err
	}
	if !slices.Contains(asset.SupportedPlatforms, string(target.Support.OS)) {
		return ErrUnsupported
	}
	return nil
}

// buildDispatch constructs actual generated protocol payloads and validates their
// OS, version, enrollment and channel metadata. It never changes delivery method.
func buildDispatch(id string, p Plan, target Target) (*Dispatch, error) {
	return buildDispatchFor(id, id, p, target)
}

// buildDispatchFor renders validated native content using the selected stable declaration identity.
func buildDispatchFor(id, declarationID string, p Plan, target Target) (*Dispatch, error) {
	numeric, err := strconv.ParseInt(p.Content.Asset.AdamID, 10, 64)
	if err != nil || numeric <= 0 || strconv.FormatInt(numeric, 10) != p.Content.Asset.AdamID {
		return nil, ErrInput
	}
	d := &Dispatch{ID: id, Enrollment: target.Enrollment, Serial: p.Target.Serial, ClientUserID: p.Target.ClientUserID, Content: p.Content, Method: p.Method}
	if target.Support.OS == support.MacOS && (target.Support.Channel == support.ChannelUser) != (p.Target.ClientUserID != "") {
		return nil, ErrUnsupported
	}
	switch p.Method {
	case MDM:
		// The legacy app command supports a user channel only on macOS. iOS
		// User Enrollment still uses its privacy-preserving device channel.
		if target.Support.Channel == support.ChannelUser && target.Support.OS != support.MacOS {
			return nil, ErrUnsupported
		}
		var payload commands.Command
		if p.Content.Kind == "Book" {
			payload = &commands.InstallMedia{ITunesStoreID: &numeric, MediaType: "Book"}
		} else {
			app := &commands.InstallApplication{ITunesStoreID: &numeric, Options: &commands.InstallApplicationOptions{PurchaseMethod: new(int64(1))}}
			if target.Support.OS == support.MacOS && commands.Support("InstallApplication.InstallAsManaged").Check(target.Support).Supported {
				app.InstallAsManaged = new(true)
			}
			payload = app
		}
		if err := payload.Validate(target.Support); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUnsupported, err)
		}
		command, err := mdm.NewCommand(payload, mdm.WithUUID(id))
		if err != nil {
			return nil, err
		}
		d.MDM = command.Raw
	case DDM:
		if p.Content.Kind != "App" || !target.DDMEnabled {
			return nil, ErrUnsupported
		}
		assignment := "Device"
		if p.Target.ClientUserID != "" {
			assignment = "User"
		}
		payload := &schemaddm.AppManaged{AppStoreID: new(p.Content.Asset.AdamID), InstallBehavior: &schemaddm.AppManagedInstallBehavior{Install: new("Required"), License: &schemaddm.AppManagedInstallBehaviorLicense{Assignment: &assignment}}}
		configuration, err := document(declarationID, "com.apple.configuration.app.managed", payload, target.Support)
		if err != nil {
			return nil, err
		}
		activation, err := document(declarationID+"-activation", "com.apple.activation.simple", &schemaddm.ActivationSimple{StandardConfigurations: []string{declarationID}}, target.Support)
		if err != nil {
			return nil, err
		}
		d.Declarations = []Document{configuration, activation}
	default:
		return nil, ErrInput
	}
	digest, err := dispatchDigest(*d)
	if err != nil {
		return nil, err
	}
	d.Digest = digest
	return d, nil
}

// document uses the existing declaration engine for canonical bytes and token
// derivation. Callers never supply or synthesize a device-applied status.
func document(id, kind string, payload any, target support.Target) (Document, error) {
	raw, err := json.Marshal(map[string]any{"Identifier": id, "Type": kind, "Payload": payload})
	if err != nil {
		return Document{}, err
	}
	parsed, err := ddm.ParseDeclaration(raw, target)
	if err != nil {
		return Document{}, fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	raw, err = ddm.RenderDeclaration(parsed.Canonical, parsed.ServerToken)
	if err != nil {
		return Document{}, err
	}
	return Document{Identifier: id, ServerToken: parsed.ServerToken, Raw: raw}, nil
}

// dispatchID keeps a full SHA-256 identity within the declaration engine's
// identifier limit, including the separate activation suffix.
func dispatchID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return "mc-" + base64.RawURLEncoding.EncodeToString(sum[:])
}

// dispatchDigest binds dispatch bytes and target identity to an immutable content digest.
func dispatchDigest(d Dispatch) (string, error) {
	d.Digest = ""
	raw, err := json.Marshal(d)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

// validateDispatch rejects changed native payloads even when their digest was recomputed.
func validateDispatch(d Dispatch, id, declarationID string, previous *HandoffEvidence, p Plan, t Target) error {
	if !reflect.DeepEqual(d.Predecessor, predecessorReference(previous)) {
		return ErrConflict
	}
	if d.ID != id || d.Enrollment != t.Enrollment || d.Serial != p.Target.Serial || d.ClientUserID != p.Target.ClientUserID || d.Content != p.Content || d.Method != p.Method {
		return ErrConflict
	}
	digest, err := dispatchDigest(d)
	if err != nil {
		return err
	}
	if digest != d.Digest {
		return ErrConflict
	}
	if declarationID == "" {
		declarationID = id
	}
	expected, err := buildDispatchFor(id, declarationID, p, t)
	if err != nil {
		return err
	}
	if d.Method == MDM {
		actual, err := mdm.DecodeCommand(d.MDM)
		if err != nil {
			return ErrConflict
		}
		wanted, err := mdm.DecodeCommand(expected.MDM)
		if err != nil {
			return err
		}
		if len(d.Declarations) != 0 || actual.UUID != id || actual.RequestType != wanted.RequestType || !reflect.DeepEqual(actual.Payload, wanted.Payload) {
			return ErrConflict
		}
	} else {
		if len(d.MDM) != 0 || len(d.Declarations) != len(expected.Declarations) {
			return ErrConflict
		}
		for i, doc := range d.Declarations {
			want := expected.Declarations[i]
			if doc.Identifier != want.Identifier || doc.ServerToken != want.ServerToken || !bytes.Equal(doc.Raw, want.Raw) {
				return ErrConflict
			}
		}
	}
	return nil
}
