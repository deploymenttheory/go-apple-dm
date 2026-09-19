package dmctl_test

import (
	json "encoding/json/v2"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm/blueprint"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/simulator"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/testpki"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appartifact"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/publicappstoreidentity"
	"github.com/deploymenttheory/go-apple-dm/server/blueprints"
	"github.com/deploymenttheory/go-apple-dm/server/internal/app"
)

func TestApplicationIdentityCLIWorkflow(t *testing.T) {
	for _, source := range []string{"public-app-store", "artifact"} {
		t.Run(source, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, `{"resultCount":1,"results":[{"trackId":123,"bundleId":"com.example.selected","trackName":"Selected","artistName":"Example","kind":"software"}]}`)
			}))
			t.Cleanup(upstream.Close)
			ca, err := testpki.NewCA("CLI workflow")
			if err != nil {
				t.Fatal(err)
			}
			identity, err := ca.Issue("device", time.Now().Add(-time.Minute))
			if err != nil {
				t.Fatal(err)
			}
			a, err := app.Build(t.Context(), app.Config{
				Role: app.RoleAll, Storage: "inmem", AdminToken: "operator", CARoots: ca.Pool(),
				Logger:                slog.New(slog.NewTextHandler(io.Discard, nil)),
				ApplicationIdentities: app.ApplicationIdentityConfig{PublicAppStore: &publicappstoreidentity.Client{BaseURL: upstream.URL, HTTPClient: upstream.Client()}},
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = a.Close() })
			srv := httptest.NewServer(a.Handler)
			t.Cleanup(srv.Close)
			env := noConfig(t)
			env["DMCTL_SERVER"], env["DMCTL_TOKEN"], env["DMCTL_OUTPUT"] = srv.URL, "operator", "json"
			call := func(args ...string) string {
				t.Helper()
				out, stderr, err := run(t, env, args...)
				if err != nil {
					t.Fatalf("%v: %v %s", args, err, stderr)
				}
				return out
			}
			payload := &ddm.AppSettings{Allowed: &ddm.AppSettingsAllowed{}}
			osName, product := "ios", "iPad16,1"
			if source == "public-app-store" {
				var listing publicappstoreidentity.App
				out := call("app-identities", "public-app-store", "lookup", "123", "-country", "GB", "-entity", "iPadSoftware")
				if err := json.Unmarshal([]byte(out), &listing); err != nil || listing.BundleID != "com.example.selected" {
					t.Fatal("lookup failed", out, err)
				}
				payload.Allowed.DeniedApps = []string{listing.BundleID}
			} else {
				osName, product = "macos", "Mac16,1"
				var report appartifact.Report
				out := call("app-identities", "inspect", "-file", "../../../devicemanagement/utility/appidentity/testdata/fixture.macho")
				if err := json.Unmarshal([]byte(out), &report); err != nil || !report.Complete || len(report.Applications) != 1 {
					t.Fatal("artifact inspection failed", out, err)
				}
				for _, arch := range report.Applications[0].Identity.Architectures {
					for _, directory := range arch.CodeDirectories {
						payload.Allowed.DeniedBinaries = append(payload.Allowed.DeniedBinaries, ddm.AppSettingsAllowedDeniedBinaries{CDHash: new(directory.CDHash)})
					}
				}
				if len(payload.Allowed.DeniedBinaries) != 2 {
					t.Fatal("architecture hashes lost", payload)
				}
			}
			// Author an explicit typed policy; CLI discovery makes no policy choice.
			declaration, err := blueprint.NewDeclaration("applications", payload)
			if err != nil {
				t.Fatal(err)
			}
			spec, err := json.Marshal(blueprint.Spec{Identifier: "app-controls", Declarations: []blueprint.Declaration{declaration}})
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "blueprint.json")
			if err := os.WriteFile(file, spec, 0o600); err != nil {
				t.Fatal(err)
			}
			upstream.Close() // Validation and publication must use only the authored IDs.
			if _, _, err := run(t, env, "blueprints", "validate", "-file", file, "-target", osName+":26,channel=device,supervised"); err == nil {
				t.Fatal("unsupported target accepted")
			}
			call("blueprints", "validate", "-file", file, "-target", osName+":27,channel=device,supervised")
			var record blueprints.Record
			if err := json.Unmarshal([]byte(call("blueprints", "publish", "-file", file)), &record); err != nil {
				t.Fatal(err)
			}
			// Seed the existing supervised enrollment; device requests authenticate
			// with its identity certificate through the normal CMS check-in path.
			if err := a.Store.Import(t.Context(), storage.EnrollmentExport{Enrollment: storage.Enrollment{
				ID: mdm.EnrollmentID{ID: "device", Channel: mdm.ChannelDevice}, Enabled: true,
				CertHash: cms.Fingerprint(identity.Cert), Capabilities: storage.Capabilities{Supervised: storage.CapabilityTrue},
				Device: storage.DeviceInfo{ProductName: product, OSVersion: "27.0"},
			}}); err != nil {
				t.Fatal(err)
			}
			call("blueprints", "assign", "app-controls", "device")
			device := simulator.New("device", simulator.WithURLs(srv.URL+"/mdm", srv.URL+"/mdm"), simulator.WithIdentity(&simulator.Identity{Cert: identity.Cert, Key: identity.Key}))
			if sync, err := device.SyncDDM(t.Context()); err != nil || len(sync.Fetched) != 2 {
				t.Fatal("device did not fetch policy and activation", sync, err)
			}
			state := device.DDM()
			id := record.Compiled.Identifiers["applications"]
			got := state.Declarations["configuration/"+id]
			if got == nil || got.Type != payload.DeclarationTypeName() || len(state.Items.Declarations.Configurations) != 1 || got.ServerToken != state.Items.Declarations.Configurations[0].ServerToken {
				t.Fatal("configuration or token missing", state)
			}
			var want map[string]any
			if err := json.Unmarshal(declaration.Payload, &want); err != nil || !reflect.DeepEqual(got.Payload, want) {
				t.Fatal("delivered selection changed", got.Payload, want, err)
			}
			activation := state.Declarations["activation/"+record.Compiled.Activations["default"]]
			if activation == nil || !reflect.DeepEqual(activation.Payload, map[string]any{"StandardConfigurations": []any{id}}) {
				t.Fatal("activation does not reference the selected policy", activation)
			}
			call("blueprints", "unassign", "app-controls", "device")
			if sync, err := device.SyncDDM(t.Context()); err != nil || len(sync.Removed) != 2 || len(device.DDM().Declarations) != 0 {
				t.Fatal("policy not removed", sync, err)
			}
			call("blueprints", "delete", "-revision", record.Revision, "app-controls")
			t.Logf("%s discovery, typed authoring, CLI publication and device delivery passed", source)
		})
	}
}
