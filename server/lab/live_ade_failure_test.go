package lab

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/appleplatformservices/dep/deptest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// adeFailureSession combines the real OAuth fixture with isolated admin failure seams.
func adeFailureSession(t *testing.T, handler http.HandlerFunc) (*Session, adeAcceptance) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	apple := deptest.NewServer(deptest.Options{})
	t.Cleanup(apple.Close)
	s := &Session{Env: &Environment{Instance: Instance{Mode: "simulated", URL: server.URL}, Client: server.Client(), Workspace: &Workspace{Directory: t.TempDir()}, DEP: apple}, Evidence: t.TempDir()}
	c := adeAcceptance{Account: "fleet", ServerUUID: "SERVER-UUID-DEPTEST", Serial: "DESIGNATED", Platform: "macOS", OSVersion: "27.0", Identity: "scep", Mutate: true, Quiescent: true}
	adeTestConfig(t, s, c)
	adeTestJSON(t, s.Env.Workspace.path("ade", "tokens.json"), apple.Tokens())
	adeTestJSON(t, s.Env.Workspace.path("ade", "profile.json"), map[string]any{"profile_name": "acceptance", "url": "https://mdm.example/enroll/ade", "org_magic": "test"})
	apple.AddDevices(dep.Device{SerialNumber: c.Serial})
	return s, c
}

// adeAccountReply returns the selected account without credential material.
func adeAccountReply(t *testing.T, w http.ResponseWriter, c adeAcceptance) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(map[string]any{"Items": []adeAccount{{Name: c.Account, ServerUUID: c.ServerUUID, HasTokens: true}}}); err != nil {
		t.Error(err)
	}
}

// TestADEPrivatePrerequisites cannot substitute ABM keys, implicit scope or a
// public error containing tokens/serials for the separate ADE prerequisites.
func TestADEPrivatePrerequisites(t *testing.T) {
	s := &Session{Env: &Environment{}}
	if _, err := adeConfig(s); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	s.Env.Workspace = &Workspace{Directory: t.TempDir()}
	if _, err := adeConfig(s); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(s.Env.Workspace.path("ade"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{`, `{}`, `{"account":"fleet","server_uuid":"server","serial":"PRIVATE"}{}`, `{"account":"fleet","server_uuid":"server","serial":"PRIVATE","unknown":true}`} {
		if err := os.WriteFile(s.Env.Workspace.path("ade", "acceptance.json"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := adeConfig(s); !errors.Is(err, ErrBlocked) || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatal("private malformed scope passed or leaked", err)
		}
	}
	if _, _, err := adeScope(s, "api"); !errors.Is(err, ErrBlocked) {
		t.Fatal("missing evidence directory accepted", err)
	}
	s.Evidence = t.TempDir()
	if err := os.Mkdir(filepath.Join(s.Evidence, "blocked"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := adeSave(s, "blocked", "PRIVATE"); err == nil || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal("failed evidence write hidden or leaked", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if !errors.Is(adeError(ctx, "request"), context.Canceled) {
		t.Fatal("cancellation lost")
	}
}

// TestADEReferenceRequestClassifications keeps permission failures separate from
// unavailable prerequisites without copying identifiable remote errors.
func TestADEReferenceRequestClassifications(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500, 200} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			s, _ := adeFailureSession(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "PRIVATE SERIAL TOKEN")
			})
			var out any
			err := adeRequest(t.Context(), s, "GET", "/PRIVATE", nil, &out)
			if err == nil || errors.Is(err, ErrBlocked) != (status == 404) || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("classification or privacy failure", err)
			}
			if err = adeRequest(t.Context(), s, "PUT", "/PRIVATE", make(chan int), nil); err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("invalid request input accepted or leaked", err)
			}
		})
	}
}

// TestADEAccountAndTokenGuards exercises real SDK authentication while refusing
// absent credentials, foreign server bindings and incomplete account pagination.
func TestADEAccountAndTokenGuards(t *testing.T) {
	for _, fault := range []string{"missing account", "no tokens", "reference binding", "invalid state", "repeated page", "account bound", "missing token", "ABM token", "SDK binding", "rejected token", "valid"} {
		t.Run(fault, func(t *testing.T) {
			c := adeAcceptance{Account: "fleet", ServerUUID: "SERVER-UUID-DEPTEST"}
			s, configured := adeFailureSession(t, func(w http.ResponseWriter, r *http.Request) {
				row := adeAccount{Name: c.Account, ServerUUID: c.ServerUUID, HasTokens: true}
				page := map[string]any{"Items": []adeAccount{row}}
				switch fault {
				case "missing account":
					page["Items"] = []adeAccount{}
				case "no tokens":
					row.HasTokens = false
					page["Items"] = []adeAccount{row}
				case "reference binding":
					row.ServerUUID = "OTHER"
					page["Items"] = []adeAccount{row}
				case "invalid state":
					row.State.TermsExpired = true
					page["Items"] = []adeAccount{row}
				case "repeated page":
					page["Items"] = []adeAccount{}
					page["NextCursor"] = "same"
				case "account bound":
					page["Items"] = []adeAccount{}
					page["NextCursor"] = r.URL.Query().Get("cursor") + "x"
				}
				if err := json.NewEncoder(w).Encode(page); err != nil {
					t.Error(err)
				}
			})
			c = configured
			switch fault {
			case "missing token":
				if err := os.Remove(s.Env.Workspace.path("ade", "tokens.json")); err != nil {
					t.Fatal(err)
				}
			case "ABM token":
				adeTestJSON(t, s.Env.Workspace.path("ade", "tokens.json"), map[string]string{"key_id": "PRIVATE", "client_id": "PRIVATE"})
			case "SDK binding":
				s.Env.DEP.SetAccount(dep.AccountDetail{ServerUUID: "OTHER"})
			case "rejected token":
				s.Env.DEP.SetRejectTokens(true)
			}
			access, err := adeOpen(t.Context(), s)
			if fault == "valid" {
				if err != nil || access == nil {
					t.Fatal(err)
				}
			} else if err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("bad ADE account/token accepted or leaked", err)
			}
		})
	}
}

// TestADECollectionRejectsIncompleteServiceEvidence checks missing devices,
// repeated/empty cursors, malformed detail and incomplete profile identities.
func TestADECollectionRejectsIncompleteServiceEvidence(t *testing.T) {
	for _, fault := range []string{"empty", "missing cursor", "repeat", "fetch denied", "delta denied", "delta exhausted", "detail inaccessible", "detail serial", "detail status", "detail profile omitted", "profile missing", "valid"} {
		t.Run(fault, func(t *testing.T) {
			c := adeAcceptance{}
			s, configured := adeFailureSession(t, func(w http.ResponseWriter, _ *http.Request) { adeAccountReply(t, w, c) })
			c = configured
			switch fault {
			case "empty":
				s.Env.DEP.DeleteDevice(c.Serial)
			case "missing cursor":
				s.Env.DEP.Script(dep.PathFetchDevices, deptest.Scripted{Status: 200, Body: `{"devices":[],"more_to_follow":false}`})
			case "repeat":
				s.Env.DEP.Script(dep.PathFetchDevices, deptest.Scripted{Status: 200, Body: `{"cursor":"same","devices":[],"more_to_follow":true}`}, deptest.Scripted{Status: 200, Body: `{"cursor":"same","devices":[],"more_to_follow":true}`})
			case "fetch denied":
				s.Env.DEP.Script(dep.PathFetchDevices, deptest.Scripted{Status: 403, Body: "PRIVATE"})
			case "delta denied":
				s.Env.DEP.Script(dep.PathSyncDevices, deptest.Scripted{Status: 403, Body: "PRIVATE"})
			case "delta exhausted":
				s.Env.DEP.Script(dep.PathSyncDevices, deptest.Scripted{Status: 400, Code: dep.CodeExhaustedCursor})
			case "detail inaccessible":
				s.Env.DEP.Script(dep.PathDeviceDetails, deptest.Scripted{Status: 200, Body: `{"devices":{"DESIGNATED":{"serial_number":"DESIGNATED","response_status":"NOT_ACCESSIBLE"}}}`})
			case "detail serial":
				s.Env.DEP.Script(dep.PathDeviceDetails, deptest.Scripted{Status: 200, Body: `{"devices":{"DESIGNATED":{"serial_number":"OTHER","response_status":"SUCCESS"}}}`})
			case "detail status":
				s.Env.DEP.Script(dep.PathDeviceDetails, deptest.Scripted{Status: 200, Body: `{"devices":{"DESIGNATED":{"serial_number":"DESIGNATED","response_status":"UNKNOWN"}}}`})
			case "detail profile omitted":
				s.Env.DEP.Script(dep.PathDeviceDetails, deptest.Scripted{Status: 200, Body: `{"devices":{"DESIGNATED":{"serial_number":"DESIGNATED","response_status":"SUCCESS"}}}`})
			case "profile missing":
				s.Env.DEP.ModifyDevice(c.Serial, func(d *dep.Device) { d.ProfileUUID = "unknown"; d.ProfileStatus = dep.ProfileStatusAssigned })
			}
			err := liveADECollection(t.Context(), s)
			if fault == "valid" || fault == "delta exhausted" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal("incomplete ADE source passed or leaked", err)
			}
		})
	}
}

// TestADEProfileAndStateGuards prevents profile definition from implicitly
// assigning other serials or claiming unvalidated platform support.
func TestADEProfileAndStateGuards(t *testing.T) {
	s, c := adeFailureSession(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{}`) })
	for _, p := range []map[string]any{
		{"profile_uuid": "old"},
		{"devices": []string{"OTHER"}},
		{"is_return_to_service": true},
		{"profile_name": "valid", "url": "https://mdm.example/ade", "org_magic": "m", "future": true},
		{"profile_name": "valid", "url": "https://mdm.example/ade", "org_magic": "m", "is_multi_user": true},
	} {
		adeTestJSON(t, s.Env.Workspace.path("ade", "profile.json"), p)
		if _, err := adeProfile(s, c); err == nil {
			t.Fatal("unsafe/incomplete profile accepted", p)
		}
	}
	for _, d := range []dep.Device{{}, {ProfileUUID: "id", ProfileStatus: "UNKNOWN"}, {ProfileUUID: "id", ProfileStatus: dep.ProfileStatusEmpty}, {ProfileStatus: dep.ProfileStatusAssigned}} {
		if _, err := profileState(d); err == nil {
			t.Fatal("inconsistent profile state accepted", d)
		}
	}
	if _, err := adeDDMAvailable("macOS", "invalid", new(true)); !errors.Is(err, ErrBlocked) {
		t.Fatal(err)
	}
	if got, ok := adePlatform("iPadOS"); !ok || got != support.IOS {
		t.Fatal(got, ok)
	}
	if got := adeProductPlatform("unknown"); got != "" {
		t.Fatal(got)
	}
	for _, p := range []struct{ model, platform string }{{"iPhone17,1", "iOS"}, {"iPad16,1", "iPadOS"}, {"Mac16,1", "macOS"}, {"RealityDevice14,1", "visionOS"}, {"AppleTV14,1", "tvOS"}} {
		if adeProductPlatform(p.model) != p.platform {
			t.Fatal(p)
		}
	}
	if adeAssignmentComplete(adeSyncOutcome{Assign: dep.AssignResult{Pending: 1}}) || adeAssignmentComplete(adeSyncOutcome{Assign: dep.AssignResult{NotBefore: time.Now()}}) {
		t.Fatal("partial assignment passed")
	}
}
