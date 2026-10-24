package lab

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
)

// TestCommandCannotAcceptAnIntermediateResultOrLoseWakeEvidence requires the
// actual result status and durable wake evidence before a command can succeed.
func TestCommandCannotAcceptAnIntermediateResultOrLoseWakeEvidence(t *testing.T) {
	for _, failure := range []string{"intermediate result", "push.json", "push-attempts.json"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			command, err := mdm.NewCommand(&commands.ProfileList{})
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			blocked := ""
			if failure != "intermediate result" {
				blocked = filepath.Join(dir, command.UUID+"-"+failure)
				if err := os.Mkdir(blocked, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			resultRequests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/commands"):
					_, _ = io.WriteString(w, `{"Queued":1}`)
				case strings.HasSuffix(r.URL.Path, "/push"):
					_, _ = io.WriteString(w, `{"Sent":true}`)
				case strings.HasSuffix(r.URL.Path, "/result"):
					resultRequests++
					w.WriteHeader(http.StatusAccepted)
				default:
					t.Error("unexpected command route", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			e := &Environment{Instance: Instance{URL: server.URL}, Client: server.Client()}
			response, err := Command(t.Context(), e, "/enrollments/device/mac", command, dir)
			if err == nil || len(response) != 0 {
				t.Fatal("incomplete command evidence passed")
			}
			if failure == "intermediate result" {
				if !strings.Contains(err.Error(), "command result HTTP 202") || resultRequests != 1 {
					t.Fatal("unexpected result was accepted as a native acknowledgment", err)
				}
			} else {
				var pathError *os.PathError
				if !errors.As(err, &pathError) || pathError.Path != blocked {
					t.Fatal("wake evidence failure was hidden", err)
				}
				if resultRequests != 0 {
					t.Fatal("continued command polling after losing wake evidence")
				}
			}
		})
	}
}

// TestNativeInventoryCannotBecomeBlueprintEvidence rejects malformed native
// inventory or an unusable OS version before creating any managed declarations.
func TestNativeInventoryCannotBecomeBlueprintEvidence(t *testing.T) {
	for _, failure := range []string{"malformed inventory", "unparseable OS"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			fixture := &blueprintProtocolFixture{t: t, uploads: map[string]*profile.Profile{}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/result") {
					response := []byte("invalid-native-plist")
					if failure == "unparseable OS" {
						var err error
						response, err = plist.Marshal(map[string]any{"QueryResponses": map[string]string{"OSVersion": "unusable", "BuildVersion": "25G83"}})
						if err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
					}
					fixture.encode(w, map[string]any{"Status": "Acknowledged", "Response": response})
					return
				}
				fixture.serve(w, r)
			}))
			defer server.Close()
			e := &Environment{Instance: Instance{URL: server.URL, Mode: "live"}, Client: server.Client(), Workspace: &Workspace{Directory: t.TempDir()}}
			err := liveBlueprints(false)(t.Context(), e, "mac")
			if err == nil {
				t.Fatal("invalid native inventory became Blueprint evidence")
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.assigned || len(fixture.uploads) != 0 || fixture.record.Revision != "" {
				t.Fatal("invalid inventory allowed managed state to be created")
			}
		})
	}
}

// TestBlueprintCannotPassWhenAcceptedEvidenceCannotBeStored checks that native
// success never masks failure to retain accepted declaration or ProfileList evidence.
func TestBlueprintCannotPassWhenAcceptedEvidenceCannotBeStored(t *testing.T) {
	for _, phase := range []string{"accepted status", "removed status", "profile attempt", "profile final", "compatibility", "compatibility accepted"} {
		t.Run(phase, func(t *testing.T) {
			t.Parallel()
			fixture := &blueprintProtocolFixture{t: t, uploads: map[string]*profile.Profile{}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if phase == "accepted status" && strings.HasSuffix(r.URL.Path, "/status") {
					fixture.encode(w, []ddm.DeclarationStatus{{Identifier: "subscription", Active: true, Valid: "valid", ServerToken: "observed-token", LastSeen: time.Now().UTC()}})
					return
				}
				fixture.serve(w, r)
			}))
			defer server.Close()
			e := &Environment{Instance: Instance{URL: server.URL, Mode: "live"}, Client: server.Client()}
			check := &liveBlueprintTest{e: e, path: "/enrollments/device/mac", id: "contract", dir: t.TempDir(), phase: "contract", record: blueprints.Record{Compiled: &blueprint.Compiled{Identifiers: map[string]string{}, Activations: map[string]string{}}}}
			if phase == "accepted status" {
				check.record.Compiled.Identifiers["subscription"] = "subscription"
				check.identifiers = map[string]bool{"subscription": true}
			}
			name := "contract-accepted-status.json"
			switch phase {
			case "profile attempt":
				name = "contract-profile-list-attempt-1.json"
			case "profile final":
				name = "contract-profile-list.json"
			case "compatibility":
				name = "macOS-27-compatibility-compatibility.json"
			case "compatibility accepted":
				name = "macOS-27-compatibility-accepted-status.json"
			}
			blocked := filepath.Join(check.dir, name)
			if err := os.Mkdir(blocked, 0o700); err != nil {
				t.Fatal(err)
			}
			var err error
			switch phase {
			case "accepted status":
				err = check.wait(t.Context(), time.Now(), true, "", nil)
			case "removed status":
				err = check.waitRemoved(t.Context())
			case "profile attempt", "profile final":
				err = check.checkProfile(t.Context(), "")
			default:
				revision := strings.Repeat("a", 64)
				fixture.uploads[revision] = &profile.Profile{Identifier: "com.example.contract", UUID: profile.NewUUID()}
				spec := blueprint.Spec{Identifier: "contract", Declarations: []blueprint.Declaration{
					{Identifier: "subscription", Type: "com.apple.configuration.management.status-subscriptions", Payload: []byte(`{"StatusItems":[{"Name":"device.operating-system.version"}]}`)},
					{Identifier: "preferences", ConfigurationProfile: &blueprint.ConfigurationProfileReference{Revision: revision}},
				}, Activations: []blueprint.Activation{{Identifier: "always", StandardConfigurations: []string{"subscription"}}, {Identifier: "conditional", StandardConfigurations: []string{"preferences"}, Predicate: "TRUEPREDICATE"}}}
				err = check.profileAssetReference(t.Context(), spec)
			}
			var pathError *os.PathError
			if !errors.As(err, &pathError) || pathError.Path != blocked {
				t.Fatalf("accepted evidence persistence did not fail closed: %v", err)
			}
			if err := os.Remove(blocked); err != nil {
				t.Fatal(err)
			}
			if err := check.cleanup(t.Context()); err != nil {
				t.Fatalf("evidence repair prevented cleanup: %v", err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if fixture.assigned || fixture.record.Revision != "" {
				t.Fatal("accepted evidence failure left temporary managed state")
			}
		})
	}
}

// TestBlueprintCompatibilityRejectionStillCleansItsPublication checks a status
// outage after compatibility evaluation cannot pass and does not strand assignments.
func TestBlueprintCompatibilityRejectionStillCleansItsPublication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		fixture := &blueprintProtocolFixture{t: t, uploads: map[string]*profile.Profile{}}
		injected := false
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fixture.mu.Lock()
			fail := !injected && strings.HasSuffix(r.URL.Path, "/status") && fixture.assetReference()
			if fail {
				injected = true
			}
			fixture.mu.Unlock()
			if fail {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			fixture.serve(w, r)
		})
		e := &Environment{Instance: Instance{URL: "https://lab.invalid", Mode: "live"}, Client: synchronousHTTPClient(handler), Workspace: &Workspace{Directory: t.TempDir()}}
		started := time.Now()
		if err := liveBlueprints(false)(t.Context(), e, "mac"); err == nil || !strings.Contains(err.Error(), "macOS 27 compatibility") {
			t.Fatal("compatibility status outage passed", err)
		}
		if elapsed := time.Since(started); elapsed != 10*time.Second {
			t.Fatalf("compatibility failure polling took %s, want 10s", elapsed)
		}
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		if !injected || fixture.assigned || fixture.record.Revision != "" {
			t.Fatal("compatibility failure was not exercised or cleanup was lost")
		}
	})
}

// TestFixtureTrustRejectsUnsupportedTransport avoids silently running an OIDC
// fixture with a client that cannot acquire its private trust root.
func TestFixtureTrustRejectsUnsupportedTransport(t *testing.T) {
	w := &Workspace{Directory: t.TempDir()}
	if err := os.Mkdir(w.path("fixtures"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, cancel := context.WithCancel(t.Context())
	defer cancel()
	e := &Environment{Workspace: w, Client: &http.Client{Transport: transportFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("not reached") })}, cancel: cancel}
	defer e.Close()
	if err := e.oidcFixture(map[string]string{}); err == nil || !strings.Contains(err.Error(), "does not support fixture trust") {
		t.Fatal("unsupported transport accepted fixture trust", err)
	}
	stored, err := os.ReadFile(w.path("fixtures", "oidc-root.pem"))
	if err != nil || !bytes.Contains(stored, []byte("BEGIN CERTIFICATE")) {
		t.Fatal("fixture trust artifact was not retained before transport rejection", err)
	}
}
