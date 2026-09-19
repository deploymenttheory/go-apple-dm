package app_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appartifact"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appidentity"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/publicappstoreidentity"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

// TestApplicationSettingsWorkflow joins discovery, explicit selection, typed
// authoring and Blueprint administration to certificate-authenticated DDM HTTP.
// It proves delivery and removal, not native execution policy or installation.
func TestApplicationSettingsWorkflow(t *testing.T) {
	for _, backend := range []string{"inmem", "sqlite"} {
		for _, source := range []string{"public-app-store", "artifact"} {
			t.Run(backend+"/"+source, func(t *testing.T) {
				ctx := t.Context()
				var offline atomic.Bool
				var lookups atomic.Int32
				store := &publicappstoreidentity.Client{HTTPClient: &http.Client{Transport: identityTransport(func(r *http.Request) (*http.Response, error) {
					lookups.Add(1)
					if offline.Load() {
						return nil, errors.New("discovery is offline")
					}
					if q := r.URL.Query(); q.Get("country") != "GB" || q.Get("entity") != "iPadSoftware" {
						t.Errorf("store context lost: %s", r.URL)
					}
					// Same name, different developer and ID; the selected app is second.
					body := `{"resultCount":2,"results":[{"trackId":999,"bundleId":"com.example.other","trackName":"Example","artistName":"Other Inc","kind":"software"},{"trackId":123,"bundleId":"com.example.selected","trackName":"Example","artistName":"Selected Inc","kind":"software","features":["iosUniversal"]}]}`
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
				})}}
				ca, err := testpki.NewCA("application authoring")
				if err != nil {
					t.Fatal(err)
				}
				scratch := t.TempDir()
				a := build(t, app.Config{Role: app.RoleAll, Storage: backend, DSN: filepath.Join(t.TempDir(), "app.db"), AdminToken: "admin", CARoots: ca.Pool(), ApplicationIdentities: app.ApplicationIdentityConfig{PublicAppStore: store, Artifacts: appartifact.Options{TempDir: scratch}}})
				srv := serve(t, a)
				server := appSettingsExample{client: srv.Client(), baseURL: srv.URL, token: "admin"}
				osName, product := "iOS", "iPad16,1"
				if source == "artifact" {
					osName, product = "macOS", "Mac16,1"
				}
				device := applicationSettingsDevice(t, a, srv.URL, ca, "selected-device", product)
				other := applicationSettingsDevice(t, a, srv.URL, ca, "other-device", product)
				if _, err := device.SyncDDM(ctx); err != nil {
					t.Fatal(err)
				}
				before := device.DDM().DeclarationsToken
				var payload *ddm.AppSettings
				var want string
				if source == "public-app-store" {
					var results struct {
						Items []publicappstoreidentity.App `json:"items"`
					}
					if err := server.request(ctx, "GET", "/authoring/app-identities/public-app-store?term=Example&country=GB&entity=iPadSoftware", nil, nil, &results); err != nil {
						t.Fatal(err)
					}
					if len(results.Items) != 2 || results.Items[0].ID == 123 {
						t.Fatal("fixture must require explicit selection", results)
					}
					payload, err = exampleDenyStoreApp(results.Items, 123)
					want = `{"Allowed":{"DeniedApps":["com.example.selected"]}}`
				} else {
					artifact := applicationSettingsArtifact(t)
					var report appartifact.Report
					if err := server.request(ctx, "POST", "/authoring/app-identities/artifacts", bytes.NewReader(artifact), http.Header{"Content-Type": {"application/octet-stream"}}, &report); err != nil {
						t.Fatal(err)
					}
					if report.SHA256 != fmt.Sprintf("%x", sha256.Sum256(artifact)) || len(report.Applications) != 2 {
						t.Fatal("artifact provenance or candidates lost", report)
					}
					payload, err = exampleDenyArtifactBuild(report, "artifact!Selected.app")
					// Independent hashes recorded with Apple's codesign for our owned
					// universal fixture. The decoy has different code-directory hashes.
					want = `{"Allowed":{"DeniedBinaries":[{"CDHash":"c167705737e365fa79b9bff6927b7594bed5a190"},{"CDHash":"7fe0792159dba657a8dce283963273c74e35cba8"}]}}`
					files, readErr := os.ReadDir(scratch)
					if readErr != nil || len(files) != 0 {
						t.Fatal("discovery retained uploaded bytes", files, readErr)
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				assertApplicationSettingsJSON(t, payload, want)
				// All following actions must work without discovery or the upload.
				offline.Store(true)
				lookupCount := lookups.Load()
				target := url.Values{"os": {osName}, "version": {"26"}, "channel": {"device"}, "supervised": {"true"}}
				if _, err := server.publish(ctx, "app-controls", payload, target); err == nil {
					t.Fatal("unsupported target passed validation")
				}
				target.Set("version", "27")
				record, err := server.publish(ctx, "app-controls", payload, target)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := device.SyncDDM(ctx); err != nil || len(device.DDM().Declarations) != 0 {
					t.Fatal("publication alone delivered a policy", err)
				}
				assignment := "/enrollments/device/selected-device/blueprints/app-controls"
				if err := server.request(ctx, "PUT", assignment, nil, nil, nil); err != nil {
					t.Fatal(err)
				}
				sync, err := device.SyncDDM(ctx)
				if err != nil || len(sync.Fetched) != 2 || sync.Token == before {
					t.Fatal("device did not fetch configuration and activation", sync, err)
				}
				state := device.DDM()
				configurationID := record.Compiled.Identifiers["applications"]
				configuration := state.Declarations["configuration/"+configurationID]
				if configuration == nil || configuration.Type != payload.DeclarationTypeName() || len(state.Items.Declarations.Configurations) != 1 || state.Items.Declarations.Configurations[0].ServerToken != configuration.ServerToken {
					t.Fatal("device declaration does not match manifest", state)
				}
				assertApplicationSettingsJSON(t, configuration.Payload, want)
				activationID := record.Compiled.Activations["default"]
				activation := state.Declarations["activation/"+activationID]
				if activation == nil || len(state.Items.Declarations.Activations) != 1 || state.Items.Declarations.Activations[0].ServerToken != activation.ServerToken {
					t.Fatal("missing activation or token mismatch", state)
				}
				assertApplicationSettingsJSON(t, activation.Payload, fmt.Sprintf(`{"StandardConfigurations":[%q]}`, configurationID))
				if _, err := other.SyncDDM(ctx); err != nil || len(other.DDM().Declarations) != 0 {
					t.Fatal("policy leaked to unassigned enrollment", err)
				}
				var httpErr *simulator.HTTPError
				endpoint := "declaration/configuration/" + configurationID
				if _, err := other.DeclarativeManagement(ctx, endpoint, nil); !errors.As(err, &httpErr) || httpErr.Status != http.StatusNotFound {
					t.Fatal("unassigned enrollment can retrieve the declaration", err)
				}
				// Idempotent republication uses the stored explicit spec and revision.
				raw, err := json.Marshal(record.Spec)
				if err != nil {
					t.Fatal(err)
				}
				var replay blueprints.Record
				revision := http.Header{"If-Match": {`"` + record.Revision + `"`}}
				if err := server.request(ctx, "PUT", "/blueprints/app-controls", bytes.NewReader(raw), revision, &replay); err != nil || replay.Revision != record.Revision {
					t.Fatal("republication changed explicit policy", replay, err)
				}
				if replaySync, err := device.SyncDDM(ctx); err != nil || replaySync.Changed || len(replaySync.Fetched) != 0 {
					t.Fatal("unchanged policy triggered new device content", replaySync, err)
				}
				if lookups.Load() != lookupCount {
					t.Fatal("publication or device delivery repeated discovery")
				}
				if err := server.request(ctx, "DELETE", assignment, nil, nil, nil); err != nil {
					t.Fatal(err)
				}
				removed, err := device.SyncDDM(ctx)
				if err != nil || len(removed.Removed) != 2 || len(device.DDM().Declarations) != 0 || removed.Token == state.DeclarationsToken {
					t.Fatal("unassignment did not remove the delivered policy", removed, err)
				}
				if _, err := device.DeclarativeManagement(ctx, endpoint, nil); !errors.As(err, &httpErr) || httpErr.Status != http.StatusNotFound {
					t.Fatal("unassigned policy remains retrievable", err)
				}
				if err := server.request(ctx, "DELETE", "/blueprints/app-controls", nil, revision, nil); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func applicationSettingsDevice(t *testing.T, a *app.App, baseURL string, ca *testpki.CA, id, product string) *simulator.Device {
	t.Helper()
	identity, err := ca.Issue(id, time.Now().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	// Seed an existing supervised enrollment; enrollment provisioning is outside
	// this authoring workflow. Every DDM request still carries its CMS signature.
	if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{
		ID: mdm.EnrollmentID{ID: id, Channel: mdm.ChannelDevice}, Enabled: true,
		CertHash:     cms.Fingerprint(identity.Cert),
		Capabilities: storage.Capabilities{Supervised: storage.CapabilityTrue},
		Device:       storage.DeviceInfo{ProductName: product, OSVersion: "27.0"},
	}}); err != nil {
		t.Fatal(err)
	}
	return simulator.New(id, simulator.WithURLs(baseURL+"/mdm", baseURL+"/mdm"), simulator.WithIdentity(&simulator.Identity{Cert: identity.Cert, Key: identity.Key}))
}

func assertApplicationSettingsJSON(t *testing.T, actual any, expected string) {
	t.Helper()
	raw, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payload = %s; want %s", raw, expected)
	}
}

func applicationSettingsArtifact(t *testing.T) []byte {
	t.Helper()
	executable, err := os.ReadFile("../../../devicemanagement/utility/appidentity/testdata/fixture.macho")
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, name := range []string{"Decoy", "Selected"} {
		binary := executable
		if name == "Decoy" {
			// Same-length signing-ID mutation yields distinct code-directory hashes.
			// These parser fixtures are never executed or treated as trusted code.
			binary = bytes.ReplaceAll(executable, []byte("identity.fixture"), []byte("identity.another"))
		}
		info := fmt.Sprintf(`<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>com.example.%s</string><key>CFBundleExecutable</key><string>Fixture</string></dict></plist>`, name)
		for _, file := range []struct {
			path string
			data []byte
		}{{"Contents/Info.plist", []byte(info)}, {"Contents/MacOS/Fixture", binary}} {
			w, err := z.Create(name + ".app/" + file.path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(file.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestApplicationSettingsExampleSelection(t *testing.T) {
	app := publicappstoreidentity.App{ID: 123, BundleID: "com.example.selected"}
	for _, candidates := range [][]publicappstoreidentity.App{nil, {app}, {app, app}, {{ID: 123}}} {
		selected := int64(123)
		if len(candidates) == 1 && candidates[0].BundleID != "" {
			selected = 999 // An unavailable selection must not fall back to the first app.
		}
		if _, err := exampleDenyStoreApp(candidates, selected); err == nil {
			t.Fatal("accepted missing or ambiguous selection", candidates)
		}
	}
	const first = "1111111111111111111111111111111111111111"
	const second = "2222222222222222222222222222222222222222"
	candidate := appartifact.Application{Location: "artifact!Selected.app", Identity: appidentity.Identity{Architectures: []appidentity.Architecture{{Name: "arm64", CodeDirectories: []appidentity.CodeDirectory{{CDHash: first}, {CDHash: second}}}}}}
	report := appartifact.Report{Complete: true, Applications: []appartifact.Application{candidate}}
	payload, err := exampleDenyArtifactBuild(report, candidate.Location)
	if err != nil {
		t.Fatal(err)
	}
	assertApplicationSettingsJSON(t, payload, fmt.Sprintf(`{"Allowed":{"DeniedBinaries":[{"CDHash":%q},{"CDHash":%q}]}}`, first, second))
	for _, test := range []struct {
		name     string
		report   appartifact.Report
		location string
	}{
		{"incomplete", appartifact.Report{Applications: report.Applications}, candidate.Location},
		{"missing", report, "another.app"},
		{"ambiguous", appartifact.Report{Complete: true, Applications: []appartifact.Application{candidate, candidate}}, candidate.Location},
		{"no-architectures", appartifact.Report{Complete: true, Applications: []appartifact.Application{{Location: candidate.Location}}}, candidate.Location},
		{"no-hashes", appartifact.Report{Complete: true, Applications: []appartifact.Application{{Location: candidate.Location, Identity: appidentity.Identity{Architectures: []appidentity.Architecture{{Name: "arm64"}}}}}}, candidate.Location},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := exampleDenyArtifactBuild(test.report, test.location); err == nil {
				t.Fatal("accepted unsafe hash-rule selection")
			}
		})
	}
}
