package schemagen

import (
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// TestValueFloorPreservesStructuredRequirements checks that value floor preserves structured
// requirements.
func TestValueFloorPreservesStructuredRequirements(t *testing.T) {
	t.Parallel()
	tree := &Tree{Schemas: []*Schema{{
		Path: "mdm/profiles/com.apple.extensiblesso.yaml", Title: "ExtensibleSingleSignOn", Family: FamilyProfiles,
		Payload: Payload{PayloadType: "com.apple.extensiblesso", SupportedOS: SupportedOS{
			MacOS: &OSSupport{Introduced: "28.2", Removed: "29.0", Supervised: new(true)},
		}},
		PayloadKeys: []Key{{Key: "AuthenticationMethod", Type: "<string>", RangeList: []any{"Password", "OpenID"}}},
	}}}
	packages, err := Build(tree)
	if err != nil {
		t.Fatal(err)
	}
	values := reviewedValueSupport(packages[0].Schemas[0])
	entry := values["ExtensibleSingleSignOn.AuthenticationMethod"]["OpenID"]
	if entry == nil || entry.OS[support.MacOS].Introduced != osversion.New(28, 2, 0) {
		t.Fatal("reviewed floor weakened the newer structured introduction")
	}
	for _, tc := range []struct {
		version             string
		supervised, allowed bool
	}{
		{"27.0", true, false},
		{"28.1.9", true, false},
		{"28.2", false, false},
		{"28.2", true, true},
		{"28.2.1", true, true},
		{"29.0", true, false},
	} {
		target := support.Target{OS: support.MacOS, Version: osversion.MustParse(tc.version), Supervised: tc.supervised}
		if got := entry.Check(target); got.Supported != tc.allowed {
			t.Fatalf("%+v: %+v", tc, got)
		}
	}
	if tree.Schemas[0].Payload.SupportedOS.MacOS.Introduced != "28.2" {
		t.Fatal("supplement mutated its source")
	}
}

// TestVersionLiteralsRetainPrecisionAndPlatform checks version literals retain precision and
// platform.
func TestVersionLiteralsRetainPrecisionAndPlatform(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		os      support.OS
		version osversion.Version
		want    string
	}{
		{support.MacOS, osversion.New(osversion.MacOS10, 15, 4), "osversion.New(osversion.MacOS10, 15, 4)"},
		{support.MacOS, osversion.New(osversion.MacOS26, 4, 1), "osversion.New(osversion.MacOS26, 4, 1)"},
		{support.MacOS, osversion.New(28, 1, 0), "osversion.New(28, 1, 0)"},
		{support.IOS, osversion.New(26, 4, 1), "osversion.New(26, 4, 1)"},
	} {
		if got := versionLit(tc.version, tc.os); got != tc.want {
			t.Fatalf("%s %s: %s", tc.os, tc.version, got)
		}
	}
}
