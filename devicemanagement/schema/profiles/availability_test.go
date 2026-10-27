package profiles_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/validation"
)

// reportsUnder reports whether err contains a validation failure at or below
// the wire path prefix.
func reportsUnder(t *testing.T, err error, prefix string) bool {
	t.Helper()
	if err == nil {
		return false
	}
	var issues validation.Errors
	if !errors.As(err, &issues) {
		t.Fatalf("unexpected error type: %v", err)
	}
	for _, issue := range issues {
		if strings.HasPrefix(issue.Path, prefix) {
			return true
		}
	}
	return false
}

// TestSharedStructsValidateEachUseAgainstItsOwnAvailability validates structs
// that Apple shares between keys with different availability. Each use must be
// checked against its own support entries, not those of the struct's first use.
func TestSharedStructsValidateEachUseAgainstItsOwnAvailability(t *testing.T) {
	t.Parallel()
	rules := []profiles.VPNOnDemandRulesElement{{Action: "Connect"}}
	vpn := profiles.VPN{
		VPNType: "VPN", UserDefinedName: "Example",
		VPN: &profiles.VPNVPN{RemoteAddress: "vpn.example.com", OnDemandRules: rules},
	}
	ikev2 := profiles.VPN{
		VPNType: "IKEv2", UserDefinedName: "Example",
		IKEv2: &profiles.VPNIKEv2{
			RemoteAddress: "vpn.example.com", LocalIdentifier: "client", RemoteIdentifier: "server",
			AuthenticationMethod: "None", OnDemandRules: rules,
		},
	}
	allowed := true
	identity := []profiles.PrivacyPreferencesPolicyControlIdentity{{
		Identifier: "com.example.app", IdentifierType: "bundleID", CodeRequirement: "identifier com.example.app", Allowed: &allowed,
	}}
	privacy := profiles.PrivacyPreferencesPolicyControl{Services: profiles.PrivacyPreferencesPolicyControlServices{
		AddressBook: identity, BluetoothAlways: identity,
	}}
	macOS := func(version string) support.Target {
		return support.Target{OS: support.MacOS, Version: osversion.MustParse(version), Channel: support.ChannelDevice, UserApproved: true}
	}
	for _, c := range []struct {
		name    string
		payload interface{ Validate(support.Target) error }
		target  support.Target
		prefix  string
		reject  bool
	}{
		// VPN on-demand rules are available on iOS 4.0 and macOS 10.7 in every
		// VPN dictionary, but on watchOS only within IKEv2.
		{"VPN rule on iOS", &vpn, support.Target{OS: support.IOS, Version: osversion.MustParse("17.0")}, "VPN.OnDemandRules", false},
		{"VPN rule on macOS 13", &vpn, macOS("13.0"), "VPN.OnDemandRules", false},
		{"VPN rule on watchOS", &vpn, support.Target{OS: support.WatchOS, Version: osversion.MustParse("10.0")}, "VPN.OnDemandRules", true},
		{"IKEv2 rule on watchOS", &ikev2, support.Target{OS: support.WatchOS, Version: osversion.MustParse("10.0")}, "IKEv2.OnDemandRules", false},
		// AddressBook identities are available from macOS 10.14 and Bluetooth
		// identities from macOS 11.0.
		{"AddressBook on macOS 10.15", &privacy, macOS("10.15"), "Services.AddressBook", false},
		{"BluetoothAlways on macOS 10.15", &privacy, macOS("10.15"), "Services.BluetoothAlways", true},
		{"BluetoothAlways on macOS 11.0", &privacy, macOS("11.0"), "Services.BluetoothAlways", false},
	} {
		if got := reportsUnder(t, c.payload.Validate(c.target), c.prefix); got != c.reject {
			t.Errorf("%s: rejected %s = %v, want %v (%v)", c.name, c.prefix, got, c.reject, c.payload.Validate(c.target))
		}
	}
}
