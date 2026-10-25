package lab

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep/deptest"
)

// adeOriginalDefinition creates an actual reference definition. Assigned fixtures
// retain remote assignment but remove local intent before the acceptance run.
func adeOriginalDefinition(t *testing.T, s *Session, c adeAcceptance, assigned bool) string {
	t.Helper()
	var p dep.ProfileResponse
	if err := s.Env.api(t.Context(), "PUT", adeBase(c)+"/profile", map[string]string{"profile_name": "original", "url": s.Env.URL + "/enroll/ade", "org_magic": "original"}, &p); err != nil {
		t.Fatal(err)
	}
	if assigned {
		if err := s.Env.api(t.Context(), "PUT", adeBase(c)+"/targets/"+c.Serial, map[string]string{"profile_uuid": p.ProfileUUID}, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.Env.api(t.Context(), "POST", adeBase(c)+"/sync", nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.Env.api(t.Context(), "DELETE", adeBase(c)+"/targets/"+c.Serial, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	return p.ProfileUUID
}

// adeUnwritableArtifact makes a future evidence filename a directory after its
// exclusive scope exists. It exercises real filesystem failure without relying
// on platform-specific permission bits or replacing the persistence functions.
func adeUnwritableArtifact(t *testing.T, s *Session, prefix, name string) *bool {
	t.Helper()
	base := s.Env.Client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	active := false
	s.Env.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		if !active && r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/dep/accounts") {
			roots, err := filepath.Glob(filepath.Join(s.Evidence, prefix+"-*"))
			if err != nil {
				return nil, err
			}
			if len(roots) != 1 {
				return nil, fmt.Errorf("fixture expected one private evidence scope, got %d", len(roots))
			}
			if err := os.Mkdir(filepath.Join(roots[0], name), 0o700); err != nil {
				return nil, err
			}
			active = true
		}
		return base.RoundTrip(r)
	})
	t.Cleanup(func() { s.Env.Client.Transport = base })
	return &active
}

// adeDeviceWrites counts every supported assignment method, including removal.
func adeDeviceWrites(s *Session) int {
	return s.Env.DEP.Count("POST", dep.PathProfileDevs) + s.Env.DEP.Count("PUT", dep.PathProfileDevs) + s.Env.DEP.Count("DELETE", dep.PathProfileDevs)
}

// TestADEAssignmentRejectsUnsafeInitialState refuses missing original evidence
// and account-wide policy before changing a remote profile or acquiring intent.
func TestADEAssignmentRejectsUnsafeInitialState(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"missing profile", "invalid platform", "invalid definition", "default profile", "other serial", "unaligned selection", "missing original profile", "missing observed profile", "unreadable selection", "unavailable detail", "evidence parent is file"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			s, c := adeRuntime(t)
			switch fault {
			case "missing profile":
				if err := os.Remove(s.Env.Workspace.path("ade", "profile.json")); err != nil {
					t.Fatal(err)
				}
			case "invalid platform":
				c.Platform = "unknown"
				adeTestConfig(t, s, c)
			case "invalid definition":
				adeTestJSON(t, s.Env.Workspace.path("ade", "profile.json"), map[string]string{})
			case "default profile", "other serial", "unaligned selection":
				profile := adeOriginalDefinition(t, s, c, false)
				path := adeBase(c) + "/targets/" + c.Serial
				if fault == "default profile" {
					path = adeBase(c) + "/default-profile"
				} else if fault == "other serial" {
					path = adeBase(c) + "/targets/SECOND-ADE"
				}
				if err := s.Env.api(t.Context(), "PUT", path, map[string]string{"profile_uuid": profile}, nil); err != nil {
					t.Fatal(err)
				}
			case "missing original profile":
				s.Env.DEP.ModifyDevice(c.Serial, func(d *dep.Device) { d.ProfileUUID, d.ProfileStatus = "missing", dep.ProfileStatusAssigned })
			case "missing observed profile":
				body, err := json.Marshal(map[string]any{"devices": map[string]any{c.Serial: map[string]string{"serial_number": c.Serial, "response_status": dep.StatusSuccess}}})
				if err != nil {
					t.Fatal(err)
				}
				s.Env.DEP.Script(dep.PathDeviceDetails, deptest.Scripted{Status: 200, Body: string(body)})
			case "unavailable detail":
				s.Env.DEP.Script(dep.PathDeviceDetails, deptest.Scripted{Status: 403, Body: "PRIVATE-CANARY"})
			case "unreadable selection":
				base := s.Env.Client.Transport
				s.Env.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
					if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/targets") {
						return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("PRIVATE-CANARY")), Request: r}, nil
					}
					return base.RoundTrip(r)
				})
			case "evidence parent is file":
				s.Evidence = filepath.Join(t.TempDir(), "report")
				if err := os.WriteFile(s.Evidence, []byte("private report"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			s.Env.DEP.ResetRequests()
			err := liveADEAssignment(t.Context(), s)
			if err == nil || strings.Contains(err.Error(), "PRIVATE-CANARY") {
				t.Fatal("unsafe scope passed or exposed a private error", err)
			}
			if s.Env.DEP.Count("POST", dep.PathProfile) != 0 || adeDeviceWrites(s) != 0 {
				t.Fatal("refused preflight mutated a remote profile")
			}
			if _, err := os.Stat(s.Env.Workspace.path("ade", "pending-assignment.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("non-mutating preflight acquired a pending operation", err)
			}
		})
	}
}

// TestADEEvidenceFailurePreservesRecovery exercises failures before mutation,
// after definition, after assignment and during restoration through real SQL.
func TestADEEvidenceFailurePreservesRecovery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		original   bool
		definition bool
		pending    bool
		local      bool
	}{
		{name: "profile-support.json"},
		{name: "original-profile.json", original: true},
		{name: "original-assignment.json"},
		{name: "defined-profile.json", definition: true},
		{name: "profile-readback.json", definition: true},
		{name: "assigned-device.json", definition: true},
		{name: "restoration-outcomes.json", definition: true, pending: true, local: true},
		{name: "restored-device.json", definition: true, pending: true, local: true},
		{name: "stored-device.json", definition: true, pending: true},
		{name: "cleanup.json", definition: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s, c := adeRuntime(t)
			original := ""
			if tc.original {
				original = adeOriginalDefinition(t, s, c, true)
			}
			active := adeUnwritableArtifact(t, s, "assignment", tc.name)
			s.Env.DEP.ResetRequests()
			err := liveADEAssignment(t.Context(), s)
			if err == nil || !*active {
				t.Fatal("failed evidence was accepted or the boundary was not reached", err)
			}
			if strings.Contains(err.Error(), s.Evidence) || strings.Contains(err.Error(), c.Serial) {
				t.Fatal("evidence failure exposed a private path or serial", err)
			}
			if got := s.Env.DEP.Count("POST", dep.PathProfile); (got > 0) != tc.definition {
				t.Fatalf("definition calls=%d, want definition=%t", got, tc.definition)
			}
			device, ok := s.Env.DEP.Device(c.Serial)
			if !ok || device.ProfileUUID != original {
				t.Fatal("evidence failure lost the original remote assignment", device)
			}
			_, markerErr := os.Stat(s.Env.Workspace.path("ade", "pending-assignment.json"))
			if tc.pending && markerErr != nil || !tc.pending && !errors.Is(markerErr, os.ErrNotExist) {
				t.Fatal("recovery fence did not match the unresolved evidence boundary", markerErr)
			}
			var selection adeSelection
			if err := s.Env.api(t.Context(), "GET", adeBase(c)+"/targets", nil, &selection); err != nil {
				t.Fatal(err)
			}
			_, selected := selection.Serials[c.Serial]
			if selected != tc.local || selection.Default != "" {
				t.Fatal("local selection was silently changed or recovery was erased", selection)
			}
			if tc.pending {
				before, err := os.ReadFile(s.Env.Workspace.path("ade", "pending-assignment.json"))
				if err != nil {
					t.Fatal(err)
				}
				writes := adeDeviceWrites(s)
				s.Evidence = t.TempDir()
				if err := liveADEAssignment(t.Context(), s); !errors.Is(err, ErrBlocked) {
					t.Fatal("unresolved evidence allowed another operation", err)
				}
				after, err := os.ReadFile(s.Env.Workspace.path("ade", "pending-assignment.json"))
				if err != nil || !bytes.Equal(before, after) || adeDeviceWrites(s) != writes {
					t.Fatal("a rerun overwrote recovery intent or repeated a mutation", err)
				}
			}
		})
	}
}

// TestADEStoredProfileReadbackTraversesPages verifies independent worker evidence
// when the designated serial is after other actual persisted organization devices.
func TestADEStoredProfileReadbackTraversesPages(t *testing.T) {
	t.Parallel()
	s, c := adeRuntime(t)
	c.Serial = "ZZZ-DESIGNATED"
	s.Env.DEP.AddDevices(dep.Device{SerialNumber: c.Serial, DeviceFamily: "Mac", OS: "OSX"})
	adeTestConfig(t, s, c)
	if err := liveADEAssignment(t.Context(), s); err != nil {
		t.Fatal(err)
	}
	device, ok := s.Env.DEP.Device(c.Serial)
	if !ok || device.ProfileUUID != "" {
		t.Fatal("paginated readback did not restore the designated device", device)
	}
	if len(adeTestEvidence(t, s, "assignment-*/stored-device.json")) != 1 {
		t.Fatal("no independent paginated device proof")
	}
}

// TestADECollectionProfileAndPersistenceProof retains actual assigned-profile
// evidence, including explicit false, and refuses truncated private reports.
func TestADECollectionProfileAndPersistenceProof(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"valid", "foreign profile", "account.json", "fetch-page-0001.json", "delta-page-0001.json", "delta-exhausted.json", "device-detail.json", "observed-profile.json"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			var c adeAcceptance
			s, configured := adeFailureSession(t, func(w http.ResponseWriter, _ *http.Request) { adeAccountReply(t, w, c) })
			c = configured
			access, err := adeOpen(t.Context(), s)
			if err != nil {
				t.Fatal(err)
			}
			profile, err := access.client.DefineProfile(t.Context(), c.Account, &dep.Profile{ProfileName: "native", URL: "https://mdm.example/enroll/ade", OrgMagic: "fixture", AllowPairing: new(false)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = access.client.AssignProfile(t.Context(), c.Account, profile.ProfileUUID, []string{c.Serial}); err != nil {
				t.Fatal(err)
			}
			var injected *bool
			switch fault {
			case "valid":
			case "foreign profile":
				s.Env.DEP.Script(dep.PathProfile, deptest.Scripted{Status: 200, Body: `{"profile_uuid":"FOREIGN","profile_name":"native","url":"https://mdm.example/enroll/ade","org_magic":"fixture"}`})
			default:
				injected = adeUnwritableArtifact(t, s, "api", fault)
				if fault == "delta-exhausted.json" {
					s.Env.DEP.Script(dep.PathSyncDevices, deptest.Scripted{Status: 400, Code: dep.CodeExhaustedCursor})
				} else if fault == "delta-page-0001.json" {
					s.Env.DEP.Script(dep.PathSyncDevices, deptest.Scripted{Status: 200, Body: `{"cursor":"unchanged","more_to_follow":false,"devices":[]}`})
				}
			}
			s.Env.DEP.ResetRequests()
			err = liveADECollection(t.Context(), s)
			if (err == nil) != (fault == "valid") || injected != nil && !*injected {
				t.Fatal("profile or private-report boundary was not enforced", err)
			}
			if adeDeviceWrites(s) != 0 || s.Env.DEP.Count("POST", dep.PathProfile) != 0 {
				t.Fatal("collection changed the actual assignment")
			}
			if fault == "valid" {
				profiles := adeTestEvidence(t, s, "api-*/observed-profile.json")
				assertions := adeTestEvidence(t, s, "api-*/api-assertions.json")
				if len(profiles) != 1 || !bytes.Contains(profiles[0], []byte(`"allow_pairing":false`)) || len(assertions) != 1 || !bytes.Contains(assertions[0], []byte(`"profile_readback":"passed"`)) || !bytes.Contains(assertions[0], []byte(`"hardware":"unexercised"`)) {
					t.Fatal("assigned-profile evidence was missing, normalized or promoted to hardware proof")
				}
			}
		})
	}
}

// TestADERestorationRequiresIndependentProof refuses remote ownership changes,
// partial acknowledgments and failed readback after a real assignment completed.
func TestADERestorationRequiresIndependentProof(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"foreign assignment", "restoration detail denied", "restoration detail incomplete", "restoration rejected per device", "restoration readback denied", "restoration readback disagrees", "reference refresh denied", "reference refresh partial"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			s, c := adeRuntime(t)
			base := s.Env.Client.Transport
			syncs := 0
			injected := false
			s.Env.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				isSync := r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/sync")
				if isSync {
					syncs++
				}
				if isSync && syncs == 2 && fault == "reference refresh denied" {
					injected = true
					return &http.Response{StatusCode: 503, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("PRIVATE-CANARY")), Request: r}, nil
				}
				response, err := base.RoundTrip(r)
				if err != nil || !isSync {
					return response, err
				}
				if syncs == 2 && fault == "reference refresh partial" {
					var result adeSyncOutcome
					decodeErr := json.NewDecoder(response.Body).Decode(&result)
					closeErr := response.Body.Close()
					if decodeErr != nil || closeErr != nil {
						return nil, errors.Join(decodeErr, closeErr)
					}
					result.Assign.Pending = 1
					body, err := json.Marshal(result)
					if err != nil {
						return nil, err
					}
					response.Body = io.NopCloser(bytes.NewReader(body))
					injected = true
				}
				if syncs != 1 {
					return response, nil
				}
				device, ok := s.Env.DEP.Device(c.Serial)
				if !ok || device.ProfileUUID == "" {
					_ = response.Body.Close()
					return nil, errors.New("fixture assignment did not complete")
				}
				device.ResponseStatus = dep.StatusSuccess
				snapshot, err := json.Marshal(map[string]any{"devices": map[string]dep.Device{c.Serial: device}})
				if err != nil {
					_ = response.Body.Close()
					return nil, err
				}
				good := deptest.Scripted{Status: 200, Body: string(snapshot)}
				switch fault {
				case "foreign assignment":
					s.Env.DEP.ModifyDevice(c.Serial, func(d *dep.Device) { d.ProfileUUID = "OTHER-OWNER"; d.ProfileStatus = dep.ProfileStatusAssigned })
				case "restoration detail denied":
					s.Env.DEP.Script(dep.PathDeviceDetails, good, deptest.Scripted{Status: 403, Body: "PRIVATE-CANARY"})
				case "restoration detail incomplete":
					body, err := json.Marshal(map[string]any{"devices": map[string]any{c.Serial: map[string]string{"serial_number": c.Serial, "response_status": dep.StatusSuccess, "profile_status": dep.ProfileStatusAssigned}}})
					if err != nil {
						_ = response.Body.Close()
						return nil, err
					}
					s.Env.DEP.Script(dep.PathDeviceDetails, good, deptest.Scripted{Status: 200, Body: string(body)})
				case "restoration rejected per device":
					body, err := json.Marshal(map[string]any{"devices": map[string]string{c.Serial: dep.StatusFailed}})
					if err != nil {
						_ = response.Body.Close()
						return nil, err
					}
					s.Env.DEP.Script(dep.PathProfileDevs, deptest.Scripted{Status: 200, Body: string(body)})
				case "restoration readback denied":
					s.Env.DEP.Script(dep.PathDeviceDetails, good, good, deptest.Scripted{Status: 403, Body: "PRIVATE-CANARY"})
				case "restoration readback disagrees":
					s.Env.DEP.Script(dep.PathDeviceDetails, good, good, good)
				}
				if strings.HasPrefix(fault, "restoration") || fault == "foreign assignment" {
					injected = true
				}
				return response, nil
			})
			err := liveADEAssignment(t.Context(), s)
			if err == nil || !injected || strings.Contains(err.Error(), "PRIVATE-CANARY") {
				t.Fatal("unverified restoration passed or exposed a private response", err)
			}
			if _, err := os.Stat(s.Env.Workspace.path("ade", "pending-assignment.json")); err != nil {
				t.Fatal("unverified restoration discarded the reconciliation fence", err)
			}
			cleanup := adeTestEvidence(t, s, "assignment-*/cleanup.json")
			if len(cleanup) != 1 || !bytes.Contains(cleanup[0], []byte(`"status":"unresolved"`)) {
				t.Fatal("restoration failure did not remain inspectable")
			}
			if fault == "foreign assignment" {
				device, ok := s.Env.DEP.Device(c.Serial)
				if !ok || device.ProfileUUID != "OTHER-OWNER" || s.Env.DEP.Count("DELETE", dep.PathProfileDevs) != 0 {
					t.Fatal("cleanup overwrote a concurrent remote assignment", device)
				}
			}
		})
	}
}

// TestADEAssignmentRechecksAuthorityAtMutationBoundaries changes observations
// between real requests, proving that earlier authorization/readback is not a
// substitute for the next mutation's required binding and ownership evidence.
func TestADEAssignmentRechecksAuthorityAtMutationBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fault      string
		definition bool
		pending    bool
	}{
		{fault: "binding denied"},
		{fault: "binding changed"},
		{fault: "selection guard denied"},
		{fault: "post-definition binding denied", definition: true},
		{fault: "definition missing identity", definition: true, pending: true},
		{fault: "definition claims other devices", definition: true, pending: true},
		{fault: "profile identity differs", definition: true},
		{fault: "profile content differs", definition: true},
		{fault: "selection write denied", definition: true, pending: true},
		{fault: "selected view unavailable", definition: true, pending: true},
		{fault: "pre-sync guard denied", definition: true},
		{fault: "assigned readback unavailable", definition: true},
		{fault: "stored device unavailable", definition: true},
	} {
		t.Run(tc.fault, func(t *testing.T) {
			t.Parallel()
			s, c := adeRuntime(t)
			base := s.Env.Client.Transport
			accounts, selections := 0, 0
			injected := false
			s.Env.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				accountRead := r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/dep/accounts")
				selectionRead := r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/targets")
				if accountRead {
					accounts++
				}
				if selectionRead {
					selections++
				}
				deny := !injected && (accountRead && (tc.fault == "binding denied" && accounts == 2 || tc.fault == "post-definition binding denied" && accounts == 3) ||
					selectionRead && (tc.fault == "selection guard denied" && selections == 2 || tc.fault == "selected view unavailable" && selections == 4 || tc.fault == "pre-sync guard denied" && selections == 5) ||
					tc.fault == "selection write denied" && r.Method == "PUT" && strings.Contains(r.URL.Path, "/targets/") ||
					tc.fault == "stored device unavailable" && r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/devices"))
				if deny {
					injected = true
					return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("PRIVATE-CANARY")), Request: r}, nil
				}
				response, err := base.RoundTrip(r)
				if err != nil || injected {
					return response, err
				}
				if accountRead && accounts == 2 && tc.fault == "binding changed" {
					s.Env.DEP.SetAccount(dep.AccountDetail{ServerUUID: "FOREIGN-CANARY"})
					injected = true
				}
				if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/sync") && tc.fault == "assigned readback unavailable" {
					s.Env.DEP.Script(dep.PathDeviceDetails, deptest.Scripted{Status: 403, Body: "PRIVATE-CANARY"})
					injected = true
				}
				if r.Method != "PUT" || !strings.HasSuffix(r.URL.Path, "/profile") || !strings.HasPrefix(tc.fault, "profile ") && !strings.HasPrefix(tc.fault, "definition ") {
					return response, nil
				}
				var defined dep.ProfileResponse
				decodeErr := json.NewDecoder(response.Body).Decode(&defined)
				closeErr := response.Body.Close()
				if decodeErr != nil || closeErr != nil {
					return nil, errors.Join(decodeErr, closeErr)
				}
				var body []byte
				switch tc.fault {
				case "definition missing identity":
					defined.ProfileUUID = ""
				case "definition claims other devices":
					defined.Devices = map[string]string{"OTHER": dep.StatusSuccess}
				case "profile identity differs", "profile content differs":
					profile := map[string]string{"profile_uuid": defined.ProfileUUID, "profile_name": "acceptance", "url": s.Env.URL + "/enroll/ade", "org_magic": "fixture"}
					if tc.fault == "profile identity differs" {
						profile["profile_uuid"] = "FOREIGN-CANARY"
					} else {
						profile["url"] = "https://different.example/enroll"
					}
					body, err = json.Marshal(profile)
					if err != nil {
						return nil, err
					}
					s.Env.DEP.Script(dep.PathProfile, deptest.Scripted{Status: 200, Body: string(body)})
				}
				body, err = json.Marshal(defined)
				if err != nil {
					return nil, err
				}
				response.Body = io.NopCloser(bytes.NewReader(body))
				response.ContentLength = int64(len(body))
				response.Header.Set("Content-Length", fmt.Sprint(len(body)))
				injected = true
				return response, nil
			})
			s.Env.DEP.ResetRequests()
			err := liveADEAssignment(t.Context(), s)
			if err == nil || !injected || strings.Contains(err.Error(), "PRIVATE-CANARY") || strings.Contains(err.Error(), "FOREIGN-CANARY") {
				t.Fatal("changed authority or profile proof was accepted or leaked", err)
			}
			if got := s.Env.DEP.Count("POST", dep.PathProfile); (got > 0) != tc.definition {
				t.Fatalf("definition calls=%d, expected definition=%t", got, tc.definition)
			}
			_, markerErr := os.Stat(s.Env.Workspace.path("ade", "pending-assignment.json"))
			if tc.pending && markerErr != nil || !tc.pending && !errors.Is(markerErr, os.ErrNotExist) {
				t.Fatal("pending fence did not preserve the mutation's certainty boundary", markerErr)
			}
			device, ok := s.Env.DEP.Device(c.Serial)
			if !ok || device.ProfileUUID != "" {
				t.Fatal("changed authority lost the original unassigned state", device)
			}
			if tc.pending {
				before, err := os.ReadFile(s.Env.Workspace.path("ade", "pending-assignment.json"))
				if err != nil {
					t.Fatal(err)
				}
				writes := adeDeviceWrites(s)
				s.Evidence = t.TempDir()
				if err := liveADEAssignment(t.Context(), s); !errors.Is(err, ErrBlocked) {
					t.Fatal("a new report path bypassed unresolved authority", err)
				}
				after, err := os.ReadFile(s.Env.Workspace.path("ade", "pending-assignment.json"))
				if err != nil || !bytes.Equal(before, after) || adeDeviceWrites(s) != writes {
					t.Fatal("a rerun changed uncertainty or replayed a remote mutation", err)
				}
			}
		})
	}
}
