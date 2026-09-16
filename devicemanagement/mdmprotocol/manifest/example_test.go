package manifest_test

import (
	"fmt"
	"strings"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/manifest"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/plist"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
)

func ExampleBuild_macOS() {
	// In production read the exact signed .pkg bytes you publish over HTTPS.
	pkg := strings.NewReader("example package bytes")
	m, size, err := manifest.Build("https://mdm.example.com/assets/Example.pkg", manifest.Metadata{BundleIdentifier: "com.example.pkg", BundleVersion: "1", Title: "Example"}, pkg, 0)
	if err != nil {
		panic(err)
	}
	wire, err := plist.Marshal(m)
	if err != nil {
		panic(err)
	}
	var inline map[string]any
	if err := plist.Unmarshal(wire, &inline); err != nil {
		panic(err)
	}
	command := &commands.InstallEnterpriseApplication{Manifest: inline}
	fmt.Println(command.RequestTypeName(), size)
	// Output: InstallEnterpriseApplication 21
}

func ExampleBuild_iOS() {
	// Build against the actual enterprise-signed IPA and its bundle metadata.
	m, _, err := manifest.Build("https://mdm.example.com/assets/Example.ipa", manifest.Metadata{BundleIdentifier: "com.example.app", BundleVersion: "42", Title: "Example"}, strings.NewReader("example IPA bytes"), 0)
	if err != nil {
		panic(err)
	}
	wire, err := plist.Marshal(m)
	if err != nil {
		panic(err)
	}
	// Host wire as the manifest property list at this HTTPS URL before enqueueing.
	manifestURL := "https://mdm.example.com/manifests/Example.plist"
	command := &commands.InstallApplication{ManifestURL: &manifestURL}
	fmt.Println(command.RequestTypeName(), len(wire) > 0)
	// Output: InstallApplication true
}
