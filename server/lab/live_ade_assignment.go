package lab

import (
	"bytes"
	"context"
	"encoding/json"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

type adeAssignmentIntent struct {
	RequestID         string       `json:"request_id"`
	EvidenceDirectory string       `json:"evidence_directory"`
	Account           string       `json:"account"`
	ServerUUID        string       `json:"server_uuid"`
	Serial            string       `json:"serial"`
	OriginalProfile   string       `json:"original_profile_uuid"`
	Original          adeSelection `json:"original_selection"`
	DefinedProfile    string       `json:"defined_profile_uuid,omitempty"`
}

type adeSyncOutcome struct {
	Sync   dep.SyncResult   `json:"Sync"`
	Assign dep.AssignResult `json:"Assign"`
}

// adePlatform uses the generated schema OS spelling. iPadOS retains its separate
// acceptance label even though the schema shares its iOS identifier.
func adePlatform(platform string) (support.OS, bool) {
	switch platform {
	case "macOS":
		return support.MacOS, true
	case "iOS", "iPadOS":
		return support.IOS, true
	case "tvOS":
		return support.TvOS, true
	case "visionOS":
		return support.VisionOS, true
	default:
		return "", false
	}
}

// adeProfile rejects implicit multi-device assignment and destructive setup
// options. The caller's explicit platform/version facts must complete validation.
func adeProfile(s *Session, c adeAcceptance) (*dep.Profile, error) {
	b, err := adeRead(s, "profile.json")
	if err != nil {
		return nil, err
	}
	var p dep.Profile
	if jsonv2.Unmarshal(b, &p) != nil || p.ProfileUUID != "" || len(p.Devices) != 0 || p.IsReturnToService != nil && *p.IsReturnToService {
		return nil, fmt.Errorf("%w: ADE profile must be an unassigned definition without return-to-service", ErrBlocked)
	}
	os, known := adePlatform(c.Platform)
	version, err := osversion.Parse(c.OSVersion)
	if !known || err != nil {
		return nil, fmt.Errorf("%w: explicit ADE platform and OS version required", ErrBlocked)
	}
	ipad := c.Platform == "iPadOS"
	report, err := p.ValidateForTarget(dep.ProfileTarget{Target: support.Target{OS: os, Version: version}, ProtocolVersion: dep.DefaultProtocolVersion, IsIPad: &ipad})
	if err != nil {
		return nil, fmt.Errorf("%w: ADE profile is structurally invalid", errOperation)
	}
	if err = adeSave(s, "profile-support.json", report); err != nil {
		return nil, err
	}
	if len(report.Unsupported) > 0 || len(report.Unchecked) > 0 {
		return nil, fmt.Errorf("%w: ADE profile target validation is unsupported or incomplete", ErrBlocked)
	}
	return &p, nil
}

// adeProfileMatches compares every caller-supplied field with Apple's independent
// profile readback. Server-added fields do not manufacture requested evidence.
func adeProfileMatches(requested, observed *dep.Profile, uuid string) bool {
	if observed == nil || observed.ProfileUUID != uuid {
		return false
	}
	want, err := jsonv2.Marshal(requested)
	if err != nil {
		return false
	}
	got, err := jsonv2.Marshal(observed)
	if err != nil {
		return false
	}
	var a, b map[string]json.RawMessage
	if json.Unmarshal(want, &a) != nil || json.Unmarshal(got, &b) != nil {
		return false
	}
	for field, value := range a {
		if !bytes.Equal(value, b[field]) {
			return false
		}
	}
	return true
}

// adeAcquireFence persists the original state before any mutation. A crashed or
// uncertain prior invocation blocks later runs even under another report path.
func adeAcquireFence(s *Session, intent adeAssignmentIntent) error {
	b, err := jsonv2.Marshal(intent)
	if err != nil {
		return fmt.Errorf("%w: ADE intent encoding failed", errOperation)
	}
	file, err := os.OpenFile(s.Env.Workspace.path("ade", "pending-assignment.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: prior ADE assignment requires operator reconciliation", ErrBlocked)
	}
	if err != nil {
		return fmt.Errorf("%w: ADE pending intent could not be retained", errOperation)
	}
	_, writeErr := file.Write(b)
	if err = errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("%w: ADE pending intent could not be retained", errOperation)
	}
	return nil
}

// adeReleaseFence removes only this invocation's retained marker. Operators must
// stop the original process before manually reconciling a retained marker.
func adeReleaseFence(s *Session, id string) error {
	b, err := adeRead(s, "pending-assignment.json")
	if err != nil {
		return err
	}
	var intent adeAssignmentIntent
	if jsonv2.Unmarshal(b, &intent) != nil || intent.RequestID != id {
		return fmt.Errorf("%w: ADE pending intent ownership differs", errOperation)
	}
	if err = os.Remove(s.Env.Workspace.path("ade", "pending-assignment.json")); err != nil {
		return fmt.Errorf("%w: ADE pending intent could not be released", errOperation)
	}
	return nil
}

// ownedSelection verifies a still-exclusive local choice before every mutation.
// It is an observation, not an API compare-and-swap: operator quiescence remains
// necessary between this read and the following request.
func (a *adeAccess) ownedSelection(ctx context.Context, s *Session, uuid string, revision int64) error {
	if err := a.binding(ctx, s); err != nil {
		return err
	}
	v, err := a.selection(ctx, s)
	if err != nil {
		return err
	}
	if v.Revision != revision || v.Serials[a.config.Serial] != uuid {
		return fmt.Errorf("%w: ADE selection changed; operator reconciliation required", errOperation)
	}
	return nil
}

// adeAssignmentComplete rejects partial or unsettled worker outcomes.
func adeAssignmentComplete(r adeSyncOutcome) bool {
	return r.Assign.Failed == 0 && r.Assign.NotAccessible == 0 && r.Assign.Throttled == 0 &&
		r.Assign.Pending == 0 && r.Assign.Deferred == 0 && r.Assign.NotBefore.IsZero()
}

// storedDevice independently inspects the reference worker's persisted record.
func (a *adeAccess) storedDevice(ctx context.Context, s *Session, uuid string) error {
	cursor := ""
	seen := map[string]bool{}
	for pageNumber := 0; pageNumber < 1000; pageNumber++ {
		var page struct {
			Items      []dep.StoredDevice
			NextCursor string
		}
		if err := adeRequest(ctx, s, "GET", adeBase(a.config)+"/devices?limit=2&cursor="+url.QueryEscape(cursor), nil, &page); err != nil {
			return err
		}
		for _, d := range page.Items {
			if d.SerialNumber != a.config.Serial {
				continue
			}
			if d.Deleted || d.ProfileUUID != uuid {
				return adeError(ctx, "persisted device profile")
			}
			return adeSave(s, "stored-device.json", d)
		}
		if page.NextCursor == "" || seen[page.NextCursor] {
			return adeError(ctx, "persisted device enumeration")
		}
		seen[page.NextCursor] = true
		cursor = page.NextCursor
	}
	return adeError(ctx, "persisted device page bound")
}

// cleanupADEAssignment restores only known settled operations. A matching current
// profile never proves that an unanswered earlier request cannot arrive later.
func cleanupADEAssignment(ctx context.Context, s *Session, a *adeAccess, intent adeAssignmentIntent, selected, uncertain bool, revision int64) error {
	if uncertain {
		return fmt.Errorf("%w: ADE outcome unresolved; pending intent retained without inverse replay", errOperation)
	}
	if !selected {
		return adeReleaseFence(s, intent.RequestID)
	}
	if err := a.ownedSelection(ctx, s, intent.DefinedProfile, revision); err != nil {
		return err
	}
	d, err := a.device(ctx)
	if err != nil {
		return err
	}
	current, err := profileState(d)
	if err != nil {
		return err
	}
	if current != intent.DefinedProfile && current != intent.OriginalProfile {
		return fmt.Errorf("%w: ADE remote profile changed outside this invocation", errOperation)
	}
	if current != intent.OriginalProfile {
		if err = a.ownedSelection(ctx, s, intent.DefinedProfile, revision); err != nil {
			return err
		}
		var outcomes map[string]string
		if intent.OriginalProfile == "" {
			outcomes, err = a.client.RemoveProfile(ctx, a.config.Account, []string{a.config.Serial})
		} else {
			var response *dep.AssignResponse
			response, err = a.client.AssignProfile(ctx, a.config.Account, intent.OriginalProfile, []string{a.config.Serial})
			if response != nil {
				outcomes = response.Devices
			}
		}
		if err != nil {
			return adeError(ctx, "restoration outcome unresolved; pending intent retained")
		}
		if err = adeSave(s, "restoration-outcomes.json", outcomes); err != nil {
			return err
		}
		if len(outcomes) != 1 || outcomes[a.config.Serial] != dep.StatusSuccess {
			return adeError(ctx, "per-device restoration")
		}
		d, err = a.device(ctx)
		if err != nil {
			return err
		}
		current, err = profileState(d)
		if err != nil || current != intent.OriginalProfile {
			return adeError(ctx, "restoration readback")
		}
	}
	if err = adeSave(s, "restored-device.json", d); err != nil {
		return err
	}
	if err = a.ownedSelection(ctx, s, intent.DefinedProfile, revision); err != nil {
		return err
	}
	path := adeBase(a.config) + "/targets/" + url.PathEscape(a.config.Serial)
	if original, exists := intent.Original.Serials[a.config.Serial]; exists {
		err = adeRequest(ctx, s, "PUT", path, map[string]string{"profile_uuid": original}, nil)
	} else {
		err = adeRequest(ctx, s, "DELETE", path, nil, nil)
	}
	if err != nil {
		return err
	}
	v, err := a.selection(ctx, s)
	if err != nil || v.Default != intent.Original.Default || len(v.Serials) != len(intent.Original.Serials) || v.Serials[a.config.Serial] != intent.Original.Serials[a.config.Serial] {
		return adeError(ctx, "original local selection restoration")
	}
	if err = a.ownedSelection(ctx, s, intent.Original.Serials[a.config.Serial], v.Revision); err != nil {
		return err
	}
	var refresh adeSyncOutcome
	if err = adeRequest(ctx, s, "POST", adeBase(a.config)+"/sync", nil, &refresh); err != nil {
		return err
	}
	if !adeAssignmentComplete(refresh) {
		return adeError(ctx, "restored reference reconciliation")
	}
	if err = a.storedDevice(ctx, s, intent.OriginalProfile); err != nil {
		return err
	}
	return adeReleaseFence(s, intent.RequestID)
}

// liveADEAssignment defines a caller profile and exercises the maintained
// reference worker for one designated serial. Its durable private fence is not
// cleared when any mutation has an unknown outcome.
func liveADEAssignment(ctx context.Context, session *Session) (result error) {
	c, err := adeConfig(session)
	if err != nil {
		return err
	}
	if !c.Mutate || !c.Quiescent {
		return fmt.Errorf("%w: explicit ADE assignment opt-in and exclusive quiescent account required", ErrBlocked)
	}
	if _, err := os.Stat(session.Env.Workspace.path("ade", "pending-assignment.json")); err == nil {
		return fmt.Errorf("%w: prior ADE assignment requires operator reconciliation", ErrBlocked)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: ADE pending intent cannot be inspected", errOperation)
	}
	s, id, err := adeScope(session, "assignment")
	if err != nil {
		return err
	}
	a, err := adeOpen(ctx, s)
	if err != nil {
		return err
	}
	p, err := adeProfile(s, c)
	if err != nil {
		return err
	}
	original, err := a.selection(ctx, s)
	if err != nil {
		return err
	}
	d, err := a.device(ctx)
	if err != nil {
		return err
	}
	previous, err := profileState(d)
	if err != nil {
		return err
	}
	if choice, exists := original.Serials[c.Serial]; exists && choice != previous {
		return fmt.Errorf("%w: original ADE desired and observed profiles must be aligned", ErrBlocked)
	}
	if previous != "" {
		profile, err := a.client.FetchProfile(ctx, c.Account, previous)
		if err != nil || profile == nil || profile.ProfileUUID != previous {
			return adeError(ctx, "original profile readback")
		}
		if err = adeSave(s, "original-profile.json", profile); err != nil {
			return err
		}
	}
	evidenceDirectory, err := filepath.Abs(s.Evidence)
	if err != nil {
		return adeError(ctx, "private evidence locator")
	}
	intent := adeAssignmentIntent{EvidenceDirectory: evidenceDirectory, RequestID: id, Account: c.Account, ServerUUID: c.ServerUUID, Serial: c.Serial, OriginalProfile: previous, Original: original}
	if err = adeSave(s, "original-assignment.json", intent); err != nil {
		return err
	}
	if err = adeAcquireFence(s, intent); err != nil {
		return err
	}
	selected, uncertain := false, false
	revision := original.Revision
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
		defer cancel()
		err := cleanupADEAssignment(cleanup, s, a, intent, selected, uncertain, revision)
		status := "passed"
		if err != nil {
			status = "unresolved"
		}
		saveErr := adeSave(s, "cleanup.json", map[string]string{"status": status, "request_id": id})
		result = errors.Join(result, err, saveErr)
	}()
	if err = a.ownedSelection(ctx, s, original.Serials[c.Serial], revision); err != nil {
		return err
	}
	var defined dep.ProfileResponse
	if err = adeRequest(ctx, s, "PUT", adeBase(c)+"/profile", p, &defined); err != nil {
		uncertain = true
		return err
	}
	if defined.ProfileUUID == "" || len(defined.Devices) != 0 {
		uncertain = true
		return adeError(ctx, "profile definition outcome")
	}
	intent.DefinedProfile = defined.ProfileUUID
	if err = adeSave(s, "defined-profile.json", defined); err != nil {
		return err
	}
	readback, err := a.client.FetchProfile(ctx, c.Account, defined.ProfileUUID)
	if err != nil || !adeProfileMatches(p, readback, defined.ProfileUUID) {
		return adeError(ctx, "defined profile readback")
	}
	if err = adeSave(s, "profile-readback.json", readback); err != nil {
		return err
	}
	if err = a.ownedSelection(ctx, s, original.Serials[c.Serial], revision); err != nil {
		return err
	}
	if err = adeRequest(ctx, s, "PUT", adeBase(c)+"/targets/"+url.PathEscape(c.Serial), map[string]string{"profile_uuid": defined.ProfileUUID}, nil); err != nil {
		uncertain = true
		return err
	}
	selected = true
	next, err := a.selection(ctx, s)
	if err != nil {
		uncertain = true
		return err
	}
	revision = next.Revision
	if err = a.ownedSelection(ctx, s, defined.ProfileUUID, revision); err != nil {
		return err
	}
	var run adeSyncOutcome
	if err = adeRequest(ctx, s, "POST", adeBase(c)+"/sync", nil, &run); err != nil {
		uncertain = true
		return err
	}
	uncertain = run.Assign.Pending != 0
	if err = adeSave(s, "reference-sync-assignment.json", run); err != nil {
		return err
	}
	if !adeAssignmentComplete(run) || run.Assign.Candidates != 1 || run.Assign.Assigned != 1 {
		return adeError(ctx, "designated per-device assignment")
	}
	d, err = a.device(ctx)
	if err != nil {
		return err
	}
	current, err := profileState(d)
	if err != nil || current != defined.ProfileUUID {
		return adeError(ctx, "assigned profile readback")
	}
	if err = adeSave(s, "assigned-device.json", d); err != nil {
		return err
	}
	return a.storedDevice(ctx, s, defined.ProfileUUID)
}
