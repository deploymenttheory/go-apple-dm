package bench

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
	"github.com/deploymenttheory/go-apple-dm/server/configurationprofile"
)

// liveBlueprints uses ordinary administration routes and native MDM responses.
// The separate device and user scenarios each use fresh identifiers. Evidence
// contains no bearer tokens, private keys or enrolled identity certificates.
func liveBlueprints(user bool) func(context.Context, *Environment, string) error {
	return func(ctx context.Context, e *Environment, device string) (err error) {
		inventory, err := liveInventory(ctx, e, device)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(inventory["OSVersion"], "26.") {
			return fmt.Errorf("%w: this scenario requires macOS 26", ErrBlocked)
		}
		path, query, scope := "/enrollments/device/"+url.PathEscape(device), "", profile.ScopeSystem
		if user {
			if e.InstallingUserID == "" {
				return fmt.Errorf("%w: -user-id is required", ErrBlocked)
			}
			path = "/enrollments/user/" + url.PathEscape(device+":"+e.InstallingUserID)
			query = "?parent=" + url.QueryEscape(device)
			scope = profile.ScopeUser
		}
		var enrollment struct {
			Enabled        bool
			ParentID       string
			TokenUpdatedAt time.Time
		}
		if err := e.api(ctx, "GET", path+query, nil, &enrollment); err != nil {
			return err
		}
		if !enrollment.Enabled || enrollment.TokenUpdatedAt.IsZero() || (user && enrollment.ParentID != device) {
			return fmt.Errorf("%w: the selected enrollment channel is not available", ErrBlocked)
		}
		id := "live-" + randomID()
		test := &liveBlueprintTest{e: e, path: path, query: query, id: id, phase: "setup"}
		test.dir = e.Workspace.path("evidence", "blueprints-"+id)
		if err := os.MkdirAll(test.dir, 0o700); err != nil {
			return wrapError(err)
		}
		if err := test.evidence("inventory", inventory); err != nil {
			return err
		}
		defer func() {
			if err != nil {
				err = fmt.Errorf("%s: %w", test.phase, err)
			}
			test.phase = "cleanup"
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 45*time.Second)
			defer cancel()
			err = errors.Join(err, test.cleanup(cleanup))
		}()
		p := &profile.Profile{
			Identifier: "com.deploymenttheory.acceptance." + id,
			UUID:       profile.NewUUID(), Scope: scope, DisplayName: "Blueprint acceptance v1",
			Payloads: []profile.Payload{{
				Identifier: "com.deploymenttheory.acceptance." + id + ".preferences",
				UUID:       profile.NewUUID(),
				Content: &profile.Raw{Type: "com.apple.ManagedClient.preferences", Keys: map[string]any{
					"PayloadContent": map[string]any{"com.deploymenttheory.acceptance." + id: map[string]any{
						"Forced": []any{map[string]any{"mcx_preference_settings": map[string]any{"AcceptanceMarker": "v1"}}},
					}},
				}},
			}},
		}
		test.profileID, test.profileUUID = p.Identifier, p.UUID
		revision, err := test.upload(ctx, p)
		if err != nil {
			return err
		}
		spec := blueprint.Spec{Identifier: id, Declarations: []blueprint.Declaration{
			{Identifier: "subscription", Type: "com.apple.configuration.management.status-subscriptions", Payload: []byte(`{"StatusItems":[{"Name":"device.operating-system.version"},{"Name":"device.operating-system.build-version"}]}`)},
			{Identifier: "preferences", ConfigurationProfile: &blueprint.ConfigurationProfileReference{Revision: revision}},
		}, Activations: []blueprint.Activation{
			{Identifier: "always", StandardConfigurations: []string{"subscription"}},
			{Identifier: "conditional", StandardConfigurations: []string{"preferences"}, Predicate: "FALSEPREDICATE"},
		}}
		if err := test.publish(ctx, spec); err != nil {
			return err
		}
		started := time.Now().UTC()
		if err := test.assign(ctx, true); err != nil {
			return err
		}
		test.phase = "inactive predicate"
		if err := test.wait(ctx, started, false, "", nil); err != nil {
			return err
		}
		before := test.record
		test.phase = "identical publication"
		if err := test.publish(ctx, spec); err != nil {
			return err
		}
		if before.Revision != test.record.Revision || !reflect.DeepEqual(before.Compiled, test.record.Compiled) {
			return fmt.Errorf("%w: identical source changed the publication", errOperation)
		}
		test.phase = "active predicate"
		spec.Activations[1].Predicate = "TRUEPREDICATE"
		started = time.Now().UTC()
		if err := test.publish(ctx, spec); err != nil {
			return err
		}
		if err := test.wait(ctx, started, true, p.DisplayName, []string{"conditional", "preferences"}); err != nil {
			return err
		}
		test.phase = "profile replacement"
		oldToken := test.status[test.record.Compiled.Identifiers["preferences"]].ServerToken
		p.DisplayName = "Blueprint acceptance v2"
		p.Payloads[0].Content = &profile.Raw{Type: "com.apple.ManagedClient.preferences", Keys: map[string]any{"PayloadContent": map[string]any{p.Identifier: map[string]any{
			"Forced": []any{map[string]any{"mcx_preference_settings": map[string]any{"AcceptanceMarker": "v2"}}},
		}}}}
		revision, err = test.upload(ctx, p)
		if err != nil {
			return err
		}
		spec.Declarations[1].ConfigurationProfile.Revision = revision
		started = time.Now().UTC()
		if err := test.publish(ctx, spec); err != nil {
			return err
		}
		if err := test.wait(ctx, started, true, p.DisplayName, []string{"preferences"}); err != nil {
			return err
		}
		if oldToken == test.status[test.record.Compiled.Identifiers["preferences"]].ServerToken {
			return fmt.Errorf("%w: device did not report the replacement ServerToken", errOperation)
		}
		test.phase = "clear publication"
		if err := test.publish(ctx, blueprint.Spec{Identifier: id}); err != nil {
			return err
		}
		if err := test.waitRemoved(ctx); err != nil {
			return err
		}
		test.phase = "republish with preserved assignment"
		started = time.Now().UTC()
		if err := test.publish(ctx, spec); err != nil {
			return err
		}
		if err := test.wait(ctx, started, true, p.DisplayName, nil); err != nil {
			return err
		}
		test.phase = "unassign"
		if err := test.assign(ctx, false); err != nil {
			return err
		}
		if err := test.waitRemoved(ctx); err != nil {
			return err
		}
		test.phase = "reassign"
		started = time.Now().UTC()
		if err := test.assign(ctx, true); err != nil {
			return err
		}
		if err := test.wait(ctx, started, true, p.DisplayName, nil); err != nil {
			return err
		}
		test.phase = "delete while assigned"
		if err := test.cleanup(ctx); err != nil {
			return err
		}
		if !user {
			return test.profileAssetReference(ctx, spec)
		}
		return nil
	}
}

type liveBlueprintTest struct {
	e                           *Environment
	path, query, id, dir, phase string
	profileID, profileUUID      string
	record                      blueprints.Record
	identifiers                 map[string]bool
	status                      map[string]ddm.DeclarationStatus
}

// evidence writes phase-specific Blueprint evidence to a JSON file in the scenario
// directory.
func (t *liveBlueprintTest) evidence(name string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return wrapError(err)
	}
	return wrapError(os.WriteFile(filepath.Join(t.dir, strings.ReplaceAll(t.phase, " ", "-")+"-"+name+".json"), append(b, '\n'), 0o600))
}

// upload uploads a profile artifact used by the live blueprint scenario.
func (t *liveBlueprintTest) upload(ctx context.Context, p *profile.Profile) (string, error) {
	raw, err := p.Marshal()
	if err != nil {
		return "", wrapError(err)
	}
	var info configurationprofile.Info
	if err := t.e.api(ctx, "POST", "/configuration-profiles", raw, &info); err != nil {
		return "", err
	}
	if info.PayloadIdentifier != p.Identifier || info.PayloadUUID != p.UUID {
		return "", fmt.Errorf("%w: upload changed the profile identity", errOperation)
	}
	return info.Revision, t.evidence("profile", info)
}

// publish publishes the scenario's current blueprint source and retains its revision.
func (t *liveBlueprintTest) publish(ctx context.Context, spec blueprint.Spec) error {
	raw, err := json.Marshal(spec)
	if err != nil {
		return wrapError(err)
	}
	header := http.Header{}
	if t.record.Revision != "" {
		header.Set("If-Match", `"`+t.record.Revision+`"`)
	}
	b, _, err := HTTP(ctx, t.e.Client, t.e.URL, t.e.Token, "PUT", "/blueprints/"+t.id, bytes.NewReader(raw), header)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &t.record); err != nil {
		return wrapError(err)
	}
	if t.record.Revision == "" || t.record.Compiled == nil {
		return fmt.Errorf("%w: incomplete publication", errOperation)
	}
	if t.identifiers == nil {
		t.identifiers = map[string]bool{}
	}
	for _, id := range t.record.Compiled.Identifiers {
		t.identifiers[id] = true
	}
	for _, id := range t.record.Compiled.Activations {
		t.identifiers[id] = true
	}
	if err := t.evidence("publication", t.record); err != nil {
		return err
	}
	return t.e.api(ctx, "POST", "/notify", nil, nil)
}

// assign changes the live enrollment's blueprint membership.
func (t *liveBlueprintTest) assign(ctx context.Context, assigned bool) error {
	method := "DELETE"
	if assigned {
		method = "PUT"
	}
	if err := t.e.api(ctx, method, t.path+"/blueprints/"+t.id+t.query, nil, nil); err != nil {
		return err
	}
	return t.e.api(ctx, "POST", "/notify", nil, nil)
}

// readStatus loads the device's reported declaration status for scenario checks.
func (t *liveBlueprintTest) readStatus(ctx context.Context) error {
	var rows []ddm.DeclarationStatus
	if err := t.e.api(ctx, "GET", t.path+"/status"+t.query, nil, &rows); err != nil {
		return err
	}
	t.status = map[string]ddm.DeclarationStatus{}
	for _, row := range rows {
		if t.identifiers[row.Identifier] {
			t.status[row.Identifier] = row
		}
	}
	return t.evidence("status", t.status)
}

// wait waits for the expected activation and profile status after the scenario change.
func (t *liveBlueprintTest) wait(ctx context.Context, since time.Time, active bool, display string, changed []string) error {
	if err := waitLive(ctx, func() (bool, error) {
		if err := t.readStatus(ctx); err != nil {
			return false, err
		}
		for name, id := range t.record.Compiled.Identifiers {
			want := name != "preferences" || active
			if !liveBlueprintStatus(t.status[id], want, since, changed == nil || slices.Contains(changed, name)) {
				return false, nil
			}
		}
		for name, id := range t.record.Compiled.Activations {
			want := name != "conditional" || active
			if !liveBlueprintStatus(t.status[id], want, since, changed == nil || slices.Contains(changed, name)) {
				return false, nil
			}
		}
		return true, nil
	}); err != nil {
		return err
	}
	if err := t.evidence("accepted-status", t.status); err != nil {
		return err
	}
	return t.checkProfile(ctx, display)
}

// liveBlueprintStatus checks whether reported declaration state matches the expected active
// state and observation time.
func liveBlueprintStatus(row ddm.DeclarationStatus, active bool, since time.Time, fresh bool) bool {
	return row.Identifier != "" && row.ServerToken != "" && row.Active == active &&
		(row.Valid == "valid" || (!active && row.Valid == "unknown")) && (!fresh || !row.LastSeen.Before(since))
}

// checkProfile checks ProfileList for the expected profile identity, display name and
// DDM source, or for its absence.
func (t *liveBlueprintTest) checkProfile(ctx context.Context, display string) error {
	cmd, err := mdm.NewCommand(&commands.ProfileList{})
	if err != nil {
		return wrapError(err)
	}
	raw, err := liveCommand(ctx, t.e, t.path, cmd)
	if err != nil {
		return err
	}
	var response struct {
		ProfileList []struct{ PayloadIdentifier, PayloadUUID, PayloadDisplayName, Source string }
	}
	if err := plist.Unmarshal(raw, &response); err != nil {
		return wrapError(err)
	}
	if err := t.evidence("profile-list", response); err != nil {
		return err
	}
	found := 0
	for _, p := range response.ProfileList {
		if p.PayloadIdentifier != t.profileID {
			continue
		}
		found++
		if display == "" || p.PayloadUUID != t.profileUUID || p.PayloadDisplayName != display || p.Source != "Declarative Device Management" {
			return fmt.Errorf("%w: ProfileList does not match the expected DDM configuration profile", errOperation)
		}
	}
	if (display != "" && found != 1) || (display == "" && found != 0) {
		return fmt.Errorf("%w: ProfileList contains %d matching profiles", errOperation, found)
	}
	return nil
}

// waitRemoved waits for the scenario declarations to disappear from status, then
// verifies that ProfileList no longer includes the profile.
func (t *liveBlueprintTest) waitRemoved(ctx context.Context) error {
	if err := waitLive(ctx, func() (bool, error) {
		if err := t.readStatus(ctx); err != nil {
			return false, err
		}
		return len(t.status) == 0, nil
	}); err != nil {
		return err
	}
	if err := t.evidence("accepted-status", t.status); err != nil {
		return err
	}
	return t.checkProfile(ctx, "")
}

// cleanup deletes the scenario Blueprint, notifies the device, and verifies removal of
// its declarations and profile.
func (t *liveBlueprintTest) cleanup(ctx context.Context) error {
	if t.record.Revision == "" {
		return nil
	}
	_, status, err := HTTP(ctx, t.e.Client, t.e.URL, t.e.Token, "DELETE", "/blueprints/"+t.id, nil, http.Header{"If-Match": {`"` + t.record.Revision + `"`}})
	if err != nil && status != http.StatusNotFound {
		return err
	}
	if err := t.e.api(ctx, "POST", "/notify", nil, nil); err != nil {
		return err
	}
	if err := t.waitRemoved(ctx); err != nil {
		return err
	}
	t.record.Revision = ""
	return nil
}

// macOS 26 must never receive LegacyProfile.ProfileAssetReference. A fresh,
// compatible activation in the same publication proves a native sync happened;
// absence of a status row alone would also pass for a device that never checked in.
func (t *liveBlueprintTest) profileAssetReference(ctx context.Context, spec blueprint.Spec) error {
	t.phase = "macOS 27 compatibility"
	spec.Declarations[1].ConfigurationProfile.UseProfileAssetReference = true
	started := time.Now().UTC()
	if err := t.publish(ctx, spec); err != nil {
		return err
	}
	if err := t.assign(ctx, true); err != nil {
		return err
	}
	var compatibility ddm.CompatibilityReport
	if err := t.e.api(ctx, "GET", t.path+"/compatibility"+t.query, nil, &compatibility); err != nil {
		return err
	}
	profileID := t.record.Compiled.Identifiers["preferences"]
	withheld := false
	for _, issue := range compatibility.Issues {
		if issue.Identifier == profileID && issue.Withheld && issue.Reason == "unsupported-target" {
			withheld = true
		}
	}
	if !compatibility.Enforced || !withheld {
		return fmt.Errorf("%w: ProfileAssetReference was not withheld for macOS 26", errOperation)
	}
	if err := t.evidence("compatibility", compatibility); err != nil {
		return err
	}
	if err := waitLive(ctx, func() (bool, error) {
		if err := t.readStatus(ctx); err != nil {
			return false, err
		}
		if _, present := t.status[profileID]; present {
			return false, fmt.Errorf("%w: macOS 26 received ProfileAssetReference", errOperation)
		}
		return liveBlueprintStatus(t.status[t.record.Compiled.Identifiers["subscription"]], true, started, true) &&
			liveBlueprintStatus(t.status[t.record.Compiled.Activations["always"]], true, started, true), nil
	}); err != nil {
		return err
	}
	if err := t.evidence("accepted-status", t.status); err != nil {
		return err
	}
	return t.checkProfile(ctx, "")
}
