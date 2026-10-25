package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/server/lab/target"
)

// adeTestJSON writes only fixture configuration/evidence in test-owned paths.
func adeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// adeTestConfig writes an explicit private acceptance scope.
func adeTestConfig(t *testing.T, s *Session, c adeAcceptance) {
	t.Helper()
	if err := os.MkdirAll(s.Env.Workspace.path("ade"), 0o700); err != nil {
		t.Fatal(err)
	}
	adeTestJSON(t, s.Env.Workspace.path("ade", "acceptance.json"), c)
}

// adeRuntime exercises the shipped reference composition with its actual SQL
// store and OAuth SDK fixture, without creating an alternate acceptance server.
func adeRuntime(t *testing.T) (*Session, adeAcceptance) {
	t.Helper()
	dir := t.TempDir()
	if err := Init(dir, "simulated", "sqlite", "127.0.0.1:0", AdapterProcess, nil); err != nil {
		t.Fatal(err)
	}
	w, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	e, err := Start(t.Context(), w, "", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	e.DEP.AddDevices(dep.Device{SerialNumber: benchSerial, DeviceFamily: "Mac", OS: "OSX"}, dep.Device{SerialNumber: "SECOND-ADE"}, dep.Device{SerialNumber: "THIRD-ADE"})
	token, err := os.ReadFile(w.path("fixtures", "dep-tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	var detail dep.AccountDetail
	if err = e.api(t.Context(), "PUT", "/dep/accounts/fleet/tokens", token, &detail); err != nil {
		t.Fatal(err)
	}
	c := adeAcceptance{Account: "fleet", ServerUUID: detail.ServerUUID, Serial: benchSerial, Platform: "macOS", OSVersion: "27.0", Identity: "scep", Mutate: true, Quiescent: true, DDM: true}
	s := &Session{Env: e, Evidence: t.TempDir(), Info: target.Info{Kind: target.KindSimulator}}
	adeTestConfig(t, s, c)
	adeTestJSON(t, w.path("ade", "tokens.json"), e.DEP.Tokens())
	adeTestJSON(t, w.path("ade", "profile.json"), map[string]any{"profile_name": "acceptance", "url": e.URL + "/enroll/ade", "org_magic": "fixture"})
	return s, c
}

// adeTestEvidence reads private artifacts retained by the selected invocation.
func adeTestEvidence(t *testing.T, s *Session, pattern string) [][]byte {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(s.Evidence, pattern))
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for _, path := range paths {
		b, err := os.ReadFile(path) // #nosec G304 -- Glob is beneath a test-owned evidence directory.
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, b)
	}
	return out
}

// TestADELiveAPIAndRestorationThroughRuntime exercises real token validation,
// small pages, remote definitions, SQL selection and the maintained assigner.
func TestADELiveAPIAndRestorationThroughRuntime(t *testing.T) {
	s, c := adeRuntime(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	s.Env.DEP.ResetRequests()
	if err := liveADECollection(ctx, s); err != nil {
		t.Fatal(err)
	}
	if s.Env.DEP.Count("POST", dep.PathProfile) != 0 || s.Env.DEP.Count("POST", dep.PathProfileDevs) != 0 || s.Env.DEP.Count("DELETE", dep.PathProfileDevs) != 0 {
		t.Fatal("read-only acceptance changed a profile")
	}
	if len(adeTestEvidence(t, s, "api-*/fetch-page-*.json")) < 2 || s.Env.DEP.Count("POST", dep.PathSyncDevices) == 0 {
		t.Fatal("full and delta pagination were not exercised")
	}
	for _, b := range adeTestEvidence(t, s, "api-*/api-assertions.json") {
		if !bytes.Contains(b, []byte(`"hardware":"unexercised"`)) || !bytes.Contains(b, []byte(`"full_fetch":"passed"`)) || !bytes.Contains(b, []byte(`"delta_sync":"passed"`)) {
			t.Fatal("API pass invented hardware evidence", string(b))
		}
	}
	for _, state := range []string{"unassigned", "assigned local", "assigned remote only"} {
		t.Run(state, func(t *testing.T) {
			original := ""
			if state != "unassigned" {
				var p dep.ProfileResponse
				if err := s.Env.api(ctx, "PUT", adeBase(c)+"/profile", map[string]any{"profile_name": "original", "url": s.Env.URL + "/enroll/ade", "org_magic": "original"}, &p); err != nil {
					t.Fatal(err)
				}
				original = p.ProfileUUID
				if err := s.Env.api(ctx, "PUT", adeBase(c)+"/targets/"+c.Serial, map[string]string{"profile_uuid": original}, nil); err != nil {
					t.Fatal(err)
				}
				if err := s.Env.api(ctx, "POST", adeBase(c)+"/sync", nil, nil); err != nil {
					t.Fatal(err)
				}
				if state == "assigned remote only" {
					if err := s.Env.api(ctx, "DELETE", adeBase(c)+"/targets/"+c.Serial, nil, nil); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := liveADEAssignment(ctx, s); err != nil {
				t.Fatal(err)
			}
			d, ok := s.Env.DEP.Device(c.Serial)
			if !ok || d.ProfileUUID != original {
				t.Fatal("original remote profile not restored", d)
			}
			var targets adeSelection
			if err := s.Env.api(ctx, "GET", adeBase(c)+"/targets", nil, &targets); err != nil {
				t.Fatal(err)
			}
			_, exists := targets.Serials[c.Serial]
			if exists != (state == "assigned local") || exists && targets.Serials[c.Serial] != original || targets.Default != "" {
				t.Fatal("original local selection not restored", targets)
			}
			if _, err := os.Stat(s.Env.Workspace.path("ade", "pending-assignment.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("successful restoration retained a pending fence", err)
			}
		})
	}
	if len(adeTestEvidence(t, s, "assignment-*/cleanup.json")) != 3 {
		t.Fatal("assignment invocations did not retain separate cleanup evidence")
	}
	for _, b := range adeTestEvidence(t, s, "assignment-*/cleanup.json") {
		if !bytes.Contains(b, []byte(`"status":"passed"`)) {
			t.Fatal("restoration evidence incomplete", string(b))
		}
	}
}

// TestADEUnansweredMutationFencesLaterRuns drops a response after the real worker
// assigned a profile. Matching readback cannot establish that uncertainty settled.
func TestADEUnansweredMutationFencesLaterRuns(t *testing.T) {
	for _, phase := range []struct{ name, method, path string }{
		{"definition", "PUT", "/profile"}, {"selection", "PUT", "/targets/" + benchSerial}, {"assignment", "POST", "/sync"},
	} {
		t.Run(phase.name, func(t *testing.T) {
			s, c := adeRuntime(t)
			base := s.Env.Client.Transport
			dropped := false
			s.Env.Client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
				response, err := base.RoundTrip(r)
				if err == nil && !dropped && r.Method == phase.method && strings.HasSuffix(r.URL.Path, phase.path) {
					dropped = true
					if err := response.Body.Close(); err != nil {
						return nil, err
					}
					return nil, errors.New("PRIVATE serial/token marker")
				}
				return response, err
			})
			err := liveADEAssignment(t.Context(), s)
			if err == nil || !dropped || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("unanswered assignment passed or leaked private error", err)
			}
			first, err := os.ReadFile(s.Env.Workspace.path("ade", "pending-assignment.json"))
			if err != nil {
				t.Fatal(err)
			}
			var pending adeAssignmentIntent
			if json.Unmarshal(first, &pending) != nil || pending.EvidenceDirectory == "" || !filepath.IsAbs(pending.EvidenceDirectory) {
				t.Fatal("pending intent lacks private progress locator")
			}
			if _, err := os.Stat(filepath.Join(pending.EvidenceDirectory, "original-assignment.json")); err != nil {
				t.Fatal("pending intent cannot locate original evidence", err)
			}
			d, ok := s.Env.DEP.Device(c.Serial)
			if !ok || phase.path == "/sync" && d.ProfileUUID == "" || s.Env.DEP.Count("DELETE", dep.PathProfileDevs) != 0 {
				t.Fatal("uncertain assignment was silently reversed", d)
			}
			original := adeTestEvidence(t, s, "assignment-*/original-assignment.json")
			if len(original) != 1 {
				t.Fatal("original assignment manifest missing")
			}
			cleanup := adeTestEvidence(t, s, "assignment-*/cleanup.json")
			if len(cleanup) != 1 || !bytes.Contains(cleanup[0], []byte(`"status":"unresolved"`)) {
				t.Fatal("uncertain cleanup not reported")
			}
			before := len(s.Env.DEP.Requests())
			oldReport := s.Evidence
			s.Env.Client.Transport = base
			s.Evidence = t.TempDir() // A different report parent cannot bypass the workspace fence.
			if err = liveADEAssignment(t.Context(), s); !errors.Is(err, ErrBlocked) {
				t.Fatal("second run bypassed unresolved mutation", err)
			}
			after, err := os.ReadFile(s.Env.Workspace.path("ade", "pending-assignment.json"))
			if err != nil || !bytes.Equal(first, after) || len(s.Env.DEP.Requests()) != before {
				t.Fatal("second run overwrote pending intent or submitted another mutation", err)
			}
			old := *s
			old.Evidence = oldReport
			if got := adeTestEvidence(t, &old, "assignment-*/original-assignment.json"); len(got) != 1 || !bytes.Equal(got[0], original[0]) {
				t.Fatal("later run overwrote original evidence")
			}
			if got := adeTestEvidence(t, &old, "assignment-*/cleanup.json"); len(got) != 1 || !bytes.Equal(got[0], cleanup[0]) {
				t.Fatal("later run overwrote unresolved cleanup evidence")
			}
		})
	}
}

// adeNativeDevice uses actual signed ADE MachineInfo, SCEP issuance and native
// check-ins. Only the device side is simulated; no enrollment evidence is forged.
func adeNativeDevice(t *testing.T, s *Session, c *adeAcceptance) *simulator.Device {
	t.Helper()
	var p dep.ProfileResponse
	if err := s.Env.api(t.Context(), "PUT", adeBase(*c)+"/profile", map[string]string{"profile_name": "hardware", "url": s.Env.URL + "/enroll/ade", "org_magic": "hardware"}, &p); err != nil {
		t.Fatal(err)
	}
	if err := s.Env.api(t.Context(), "PUT", adeBase(*c)+"/targets/"+c.Serial, map[string]string{"profile_uuid": p.ProfileUUID}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Env.api(t.Context(), "POST", adeBase(*c)+"/sync", nil, nil); err != nil {
		t.Fatal(err)
	}
	d, err := s.Env.factoryDevice()
	if err != nil {
		t.Fatal(err)
	}
	simulator.WithDDM(nil)(d)
	d.OSVersion, d.BuildVersion = "27.0", "27A100"
	d.Responder = func(cmd *mdm.Command) simulator.Reply {
		reply := simulator.AcknowledgeAll(cmd)
		if cmd.RequestType == "DeviceInformation" {
			reply.Payload = &commands.DeviceInformationResponse{QueryResponses: commands.DeviceInformationResponseQueryResponses{SerialNumber: new(c.Serial), OSVersion: new(d.OSVersion), BuildVersion: new(d.BuildVersion), ProductName: new(d.ProductName), IsSupervised: new(true)}}
		}
		return reply
	}
	if err = d.ADEEnroll(t.Context(), s.Env.URL+"/enroll/ade", simulator.ADEOptions{}); err != nil {
		t.Fatal(err)
	}
	c.DeviceID = d.UDID
	adeTestConfig(t, s, *c)
	return d
}

// TestADENativeHardwareThroughRuntime requires genuine retained provenance and
// actual authenticated command/DDM exchanges; simulated proof stays labeled.
func TestADENativeHardwareThroughRuntime(t *testing.T) {
	s, c := adeRuntime(t)
	d := adeNativeDevice(t, s, &c)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		seen := len(s.Env.APNS.Requests())
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				count := len(s.Env.APNS.Requests())
				if count > seen {
					seen = count
					if _, err := d.Connect(ctx); err != nil && ctx.Err() == nil {
						t.Error(err)
						return
					}
				}
			}
		}
	}()
	defer func() { cancel(); <-stopped }()
	if err := liveADEHardware(ctx, s); err != nil {
		t.Fatal(err)
	}
	rows := adeTestEvidence(t, s, "hardware-*/platform-results.json")
	if len(rows) != 1 || !bytes.Contains(rows[0], []byte(`"mode":"simulated"`)) {
		t.Fatal("fixture evidence claimed live hardware compatibility")
	}
	var report struct {
		Platforms map[string]adeHardwareResult `json:"platforms"`
	}
	if err := json.Unmarshal(rows[0], &report); err != nil {
		t.Fatal(err)
	}
	for platform, result := range report.Platforms {
		if platform == "macOS" {
			if result.Status != "passed" || result.ADEIssuance != "passed" || result.PushAndMDM != "passed" || result.DDM != "passed" {
				t.Fatal("native ADE proof incomplete", result)
			}
		} else if result.Status != "unexercised" {
			t.Fatal("one device certified another platform", platform, result)
		}
	}
	if len(adeTestEvidence(t, s, "hardware-*/ddm-declaration-status.json")) != 1 || len(adeTestEvidence(t, s, "hardware-*/ddm-status-values.json")) != 1 {
		t.Fatal("native DDM proof not retained")
	}
	for _, platform := range []string{"iOS", "iPadOS", "macOS", "visionOS", "tvOS"} {
		if available, err := adeDDMAvailable(platform, "27.0", new(true)); err != nil || !available {
			t.Fatal("current platform DDM status support lost", platform, available, err)
		}
	}
	if available, err := adeDDMAvailable("tvOS", "15.0", new(true)); err != nil || available {
		t.Fatal("tvOS status subscriptions enabled below their actual feature floor", available, err)
	}
	if _, err := adeDDMAvailable("unknown", "27.0", new(true)); !errors.Is(err, ErrBlocked) {
		t.Fatal("unknown platform accepted", err)
	}
}
