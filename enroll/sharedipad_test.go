package enroll_test

import (
	"slices"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/enroll"
	"github.com/deploymenttheory/go-apple-dm/profile"
	"github.com/deploymenttheory/go-apple-dm/schema/profiles"
)

// TestSharedIPadCapability: a Shared iPad profile always advertises
// per-user connections, once (decision record 0029).
func TestSharedIPadCapability(t *testing.T) {
	base := enroll.Profile{Identifier: "com.example.mdm", Topic: "com.apple.mgmt.t", ServerURL: "https://mdm.example/mdm", SCEP: &enroll.SCEP{URL: "https://mdm.example/scep"}}
	for _, tc := range []struct {
		name string
		caps []string
		want int
	}{
		{"added", nil, 1},
		{"kept once", []string{enroll.CapabilityPerUserConnections, enroll.CapabilityBootstrapToken}, 1},
	} {
		p := base
		p.SharedIPad = true
		p.ServerCapabilities = tc.caps
		built, err := p.Build()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		mdmPayload, ok := profile.Find[*profiles.MDM](built)
		if !ok || mdmPayload == nil {
			t.Fatalf("%s: no MDM payload", tc.name)
		}
		n := 0
		for _, c := range mdmPayload.ServerCapabilities {
			if c == enroll.CapabilityPerUserConnections {
				n++
			}
		}
		if n != tc.want {
			t.Fatalf("%s: per-user-connections count = %d, want %d (%v)", tc.name, n, tc.want, mdmPayload.ServerCapabilities)
		}
		if len(tc.caps) > 0 && !slices.Contains(mdmPayload.ServerCapabilities, enroll.CapabilityBootstrapToken) {
			t.Fatalf("%s: other capabilities lost", tc.name)
		}
	}
	// Not a Shared iPad: nothing is added.
	p := base
	built, err := p.Build()
	if err != nil {
		t.Fatal(err)
	}
	if m, ok := profile.Find[*profiles.MDM](built); ok && slices.Contains(m.ServerCapabilities, enroll.CapabilityPerUserConnections) {
		t.Fatal("capability added without SharedIPad")
	}
}
