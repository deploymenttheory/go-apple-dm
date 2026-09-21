//go:build schema_seed_os_27

package service_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/cms"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/profile"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/storage"
	"github.com/deploymenttheory/go-apple-dm/server/service"
)

// TestSeedOS27InstallProfileCompatibility checks the OpenID SSO version floor while retaining
// exact signed profile bytes for supported targets.
func TestSeedOS27InstallProfileCompatibility(t *testing.T) {
	for _, method := range []string{"Password", "OpenID"} {
		p := &profile.Profile{Identifier: "sso", UUID: "profile", Payloads: []profile.Payload{{Identifier: "sso.payload", UUID: "payload", Content: &profiles.ExtensibleSingleSignOn{ExtensionIdentifier: "com.example.sso", Type: "Redirect", PlatformSSO: &profiles.ExtensibleSingleSignOnPlatformSSO{AuthenticationMethod: new(method)}}}}}
		data, err := p.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		for _, version := range []string{"26.0", "26.4", "26.6.2", "27.0"} {
			h := newHarness(t, service.Config{})
			id := seedDevice(t, h, "Mac16,1", version, true, false)
			wireProfile, err := cms.SignAttached(data, h.ca.Cert, h.ca.Key)
			if err != nil {
				t.Fatal(err)
			}
			command := newCmd(t, &commands.InstallProfile{Payload: wireProfile})
			result, err := h.core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, command, storage.EnqueueOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if method == "OpenID" && version != "27.0" {
				if !errors.Is(result.Skipped[id], service.ErrUnsupportedTarget) {
					t.Fatalf("unsupported SSO accepted: %+v", result)
				}
				continue
			}
			got, err := h.core.Connect(t.Context(), req(h.cert), response(id.ID, "", mdm.StatusIdle))
			if err != nil || got == nil || !bytes.Equal(command.Raw, got.Raw) {
				t.Fatalf("profile bytes changed or not delivered: %v", err)
			}
		}
	}
}

// TestSeedOS27SoftwareUpdateQueryCompatibility checks that removed software-update inventory
// queries are withheld on macOS 27 while OSVersion remains available.
func TestSeedOS27SoftwareUpdateQueryCompatibility(t *testing.T) {
	for _, version := range []string{"26.0", "26.4", "26.6.2", "27.0"} {
		h := newHarness(t, service.Config{})
		id := seedDevice(t, h, "Mac16,1", version, true, false)
		for _, query := range []string{"OSVersion", "OSUpdateSettings", "SoftwareUpdateDeviceID"} {
			result, err := h.core.Enqueue(t.Context(), []mdm.EnrollmentID{id}, newCmd(t, &commands.DeviceInformation{Queries: []string{query}}), storage.EnqueueOptions{})
			if err != nil {
				t.Fatal(err)
			}
			want := version != "27.0" || query == "OSVersion"
			if (len(result.Queued) == 1) != want {
				t.Fatalf("%s %s: %+v", version, query, result)
			}
		}
	}
}
