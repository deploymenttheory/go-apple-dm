package appsettings

import (
	json "encoding/json/v2"
	"errors"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/utility/appidentity"
)

func target(os support.OS, channel support.Channel) support.Target {
	return support.Target{OS: os, Version: osversion.New(27, 0, 0), Channel: channel, Supervised: true}
}

func identity() appidentity.Identity {
	return appidentity.Identity{BundleID: "com.example.bundle", Architectures: []appidentity.Architecture{
		{Name: "arm64", CDHash: strings.Repeat("A", 40), SigningID: "com.example.signed", TeamID: "EXAMPLETEAM", Signature: appidentity.Signature{Status: appidentity.Valid, Category: appidentity.DeveloperID}},
		{Name: "arm64e.x1", CDHash: strings.Repeat("b", 40), SigningID: "com.example.signed", TeamID: "EXAMPLETEAM", Signature: appidentity.Signature{Status: appidentity.Valid, Category: appidentity.DeveloperID}},
	}}
}

func TestAppLists(t *testing.T) {
	ids := []string{"com.example.app", "com.apple.webapp"}
	for _, build := range []func([]string, support.Target) (*ddm.AppSettings, error){AllowApps, DenyApps} {
		p, err := build(ids, target(support.IOS, support.ChannelDevice))
		if err != nil {
			t.Fatal(err)
		}
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var roundtrip ddm.AppSettings
		if err := json.Unmarshal(data, &roundtrip); err != nil {
			t.Fatal(err)
		}
		if err := roundtrip.Validate(target(support.IOS, support.ChannelDevice)); err != nil {
			t.Fatal(err)
		}
		ids[0] = "changed"
		if strings.Contains(string(data), "changed") {
			t.Fatal("unexpected payload")
		}
		if p.Allowed.AllowedApps != nil && p.Allowed.AllowedApps[0] != "com.example.app" {
			t.Fatal("caller list aliases payload")
		}
		if p.Allowed.DeniedApps != nil && p.Allowed.DeniedApps[0] != "com.example.app" {
			t.Fatal("caller list aliases payload")
		}
		ids[0] = "com.example.app"
		for _, bad := range [][]string{nil, {}, {""}, {"with space"}, {"a\nb"}, {"a{b}"}} {
			if p, err := build(bad, target(support.IOS, support.ChannelDevice)); !errors.Is(err, ErrInput) || p != nil {
				t.Fatalf("%v %v", p, err)
			}
		}
		if _, err := build(ids, target(support.MacOS, support.ChannelDevice)); err == nil {
			t.Fatal("accepted app list on macOS")
		}
	}
}

func TestBinaryMatchModes(t *testing.T) {
	for _, mode := range []MatchMode{MatchCDHash, MatchApp, MatchTeam, MatchSigningID} {
		for _, allow := range []bool{false, true} {
			id := identity()
			o := BinaryOptions{Match: mode, PathPrefix: "/Applications/Example.app/", SigningState: "DeveloperID"}
			var p *ddm.AppSettings
			var err error
			if allow {
				o.AlwaysAllowManagedApps = new(true)
				p, err = AllowBinaries(id, o, target(support.MacOS, support.ChannelDevice))
			} else {
				p, err = DenyBinaries(id, o, target(support.MacOS, support.ChannelDevice))
			}
			if allow && mode == MatchSigningID {
				if !errors.Is(err, ErrInput) {
					t.Fatal(err)
				}
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			var roundtrip ddm.AppSettings
			if err := json.Unmarshal(data, &roundtrip); err != nil {
				t.Fatal(err)
			}
			if err := roundtrip.Validate(target(support.MacOS, support.ChannelDevice)); err != nil {
				t.Fatal(err)
			}
			count := len(p.Allowed.AllowedBinaries) + len(p.Allowed.DeniedBinaries)
			if mode == MatchCDHash {
				if count != 2 || !strings.Contains(string(data), strings.Repeat("a", 40)) || strings.Contains(string(data), "SigningID") {
					t.Fatal(string(data))
				}
			} else if count != 1 {
				t.Fatalf("rules not deduplicated: %s", data)
			}
			if mode == MatchApp && !strings.Contains(string(data), `"SigningID":"com.example.signed"`) {
				t.Fatal(string(data))
			}
			if mode == MatchTeam && strings.Contains(string(data), "SigningID") {
				t.Fatal(string(data))
			}
			if allow {
				*o.AlwaysAllowManagedApps = false
				if !*p.Allowed.AlwaysAllowManagedApps {
					t.Fatal("flag aliases input")
				}
			}
		}
	}
	if _, err := DenyBinaries(identity(), BinaryOptions{Match: MatchCDHash}, target(support.MacOS, support.ChannelDevice)); err != nil {
		t.Fatal(err)
	}
}

func TestBinaryFailures(t *testing.T) {
	for _, o := range []BinaryOptions{{}, {Match: "unknown"}, {Match: MatchCDHash, PathPrefix: "relative"}, {Match: MatchCDHash, PathPrefix: "/a\nb"}, {Match: MatchCDHash, AlwaysAllowManagedApps: new(false)}, {Match: MatchCDHash, SigningState: "unknown"}} {
		if p, err := DenyBinaries(identity(), o, target(support.MacOS, support.ChannelDevice)); err == nil || p != nil {
			t.Fatalf("%+v accepted", o)
		}
	}
	for _, tc := range []struct {
		mode   MatchMode
		change func(*appidentity.Identity)
	}{
		{MatchCDHash, func(id *appidentity.Identity) { id.Architectures = nil }},
		{MatchCDHash, func(id *appidentity.Identity) { id.Architectures[0].Name = "" }},
		{MatchCDHash, func(id *appidentity.Identity) { id.Architectures[1].Name = id.Architectures[0].Name }},
		{MatchCDHash, func(id *appidentity.Identity) { id.Architectures[0].Signature.Status = appidentity.Invalid }},
		{MatchCDHash, func(id *appidentity.Identity) { id.Architectures[0].Signature.Status = appidentity.Unsigned }},
		{MatchCDHash, func(id *appidentity.Identity) { id.Architectures[0].CDHash = "short" }},
		{MatchCDHash, func(id *appidentity.Identity) { id.Architectures[0].CDHash = strings.Repeat("g", 40) }},
		{MatchApp, func(id *appidentity.Identity) { id.Architectures[0].SigningID = "" }},
		{MatchTeam, func(id *appidentity.Identity) { id.Architectures[0].TeamID = "" }},
		{MatchTeam, func(id *appidentity.Identity) { id.Architectures[0].TeamID = "*APPLE*" }},
	} {
		id := identity()
		tc.change(&id)
		if p, err := AllowBinaries(id, BinaryOptions{Match: tc.mode}, target(support.MacOS, support.ChannelDevice)); !errors.Is(err, ErrIdentity) || p != nil {
			t.Fatalf("missing fact selected fallback: %v %v", p, err)
		}
	}
	id := identity()
	for i := range id.Architectures {
		id.Architectures[i].TeamID = ""
		id.Architectures[i].Signature.Category = appidentity.Apple
	}
	p, err := AllowBinaries(id, BinaryOptions{Match: MatchTeam}, target(support.MacOS, support.ChannelDevice))
	if err != nil || *p.Allowed.AllowedBinaries[0].TeamID != "*APPLE*" {
		t.Fatalf("%+v %v", p, err)
	}
	id.Architectures[0].Signature.Status = appidentity.Invalid
	if _, err := AllowBinaries(id, BinaryOptions{Match: MatchTeam}, target(support.MacOS, support.ChannelDevice)); !errors.Is(err, ErrIdentity) {
		t.Fatal(err)
	}
}

func TestTargetValidation(t *testing.T) {
	good := target(support.MacOS, support.ChannelDevice)
	for _, bad := range []support.Target{
		{},
		{OS: support.MacOS, Channel: support.ChannelDevice},
		{OS: support.MacOS, Version: good.Version},
		{OS: "futureOS", Version: good.Version, Channel: support.ChannelDevice},
		target(support.IOS, support.ChannelDevice), target(support.MacOS, support.ChannelUser),
		{OS: support.MacOS, Version: osversion.New(26, 0, 0), Channel: support.ChannelDevice, Supervised: true},
		{OS: support.MacOS, Version: good.Version, Channel: support.ChannelDevice},
	} {
		if _, err := AllowBinaries(identity(), BinaryOptions{Match: MatchCDHash}, bad); err == nil {
			t.Fatalf("invalid target accepted: %+v", bad)
		}
	}
	good.Version = osversion.New(28, 0, 0)
	if _, err := AllowBinaries(identity(), BinaryOptions{Match: MatchCDHash}, good); err != nil {
		t.Fatalf("version hard-coded: %v", err)
	}
}

func TestPrivacyDefaults(t *testing.T) {
	entries := []PrivacyEntry{{BundleID: "com.example.app", Permissions: ddm.AppSettingsAppDictionary{OrganizationJustification: "Video calls", Camera: new("Allow")}}}
	for _, os := range []support.OS{support.IOS, support.MacOS} {
		channel := support.ChannelDevice
		want := "com.example.app"
		if os == support.MacOS {
			channel = support.ChannelUser
			entries[0].DesignatedRequirement = `identifier "com.example.app" and anchor apple generic`
			want += ` {identifier "com.example.app" and anchor apple generic}`
		}
		p, err := PrivacyDefaults(entries, target(os, channel))
		if err != nil {
			t.Fatal(err)
		}
		if p.Privacy.PermissionDefaults[want].Camera == nil {
			t.Fatalf("missing %s", want)
		}
	}
	key, err := ComposeIdentifier("com.example", `anchor apple`)
	if err != nil || key != "com.example {anchor apple}" {
		t.Fatalf("%q %v", key, err)
	}
	for _, args := range [][2]string{{"", "anchor apple"}, {"com.example", ""}, {"com.example", " anchor apple"}, {"com.example", "anchor apple\n"}, {"com.example", "{anchor apple}"}, {"com.example", "anchor\napple"}} {
		if _, err := ComposeIdentifier(args[0], args[1]); !errors.Is(err, ErrInput) {
			t.Fatal(err)
		}
	}
}

func TestPrivacyFailures(t *testing.T) {
	for _, tc := range []struct {
		os      support.OS
		channel support.Channel
		entry   PrivacyEntry
	}{
		{support.IOS, support.ChannelDevice, PrivacyEntry{}},
		{support.IOS, support.ChannelDevice, PrivacyEntry{BundleID: "com.example", Permissions: ddm.AppSettingsAppDictionary{OrganizationJustification: " "}}},
		{support.IOS, support.ChannelDevice, PrivacyEntry{BundleID: "com.example", DesignatedRequirement: "anchor apple", Permissions: ddm.AppSettingsAppDictionary{OrganizationJustification: "Reason"}}},
		{support.IOS, support.ChannelDevice, PrivacyEntry{BundleID: "com.example", Permissions: ddm.AppSettingsAppDictionary{OrganizationJustification: "Reason", Accessibility: new("Allow")}}},
		{support.IOS, support.ChannelDevice, PrivacyEntry{BundleID: "com.example", Permissions: ddm.AppSettingsAppDictionary{OrganizationJustification: "Reason", Camera: new("Deny")}}},
		{support.MacOS, support.ChannelUser, PrivacyEntry{BundleID: "com.example", Permissions: ddm.AppSettingsAppDictionary{OrganizationJustification: "Reason"}}},
		{support.MacOS, support.ChannelDevice, PrivacyEntry{BundleID: "com.example", DesignatedRequirement: "anchor apple", Permissions: ddm.AppSettingsAppDictionary{OrganizationJustification: "Reason"}}},
		{support.TvOS, support.ChannelDevice, PrivacyEntry{BundleID: "com.example", Permissions: ddm.AppSettingsAppDictionary{OrganizationJustification: "Reason"}}},
	} {
		if p, err := PrivacyDefaults([]PrivacyEntry{tc.entry}, target(tc.os, tc.channel)); err == nil || p != nil {
			t.Fatalf("%+v accepted", tc)
		}
	}
	entry := PrivacyEntry{BundleID: "com.example", Permissions: ddm.AppSettingsAppDictionary{OrganizationJustification: "Reason"}}
	for _, entries := range [][]PrivacyEntry{nil, {entry, entry}} {
		if _, err := PrivacyDefaults(entries, target(support.IOS, support.ChannelDevice)); !errors.Is(err, ErrInput) {
			t.Fatal(err)
		}
	}
}
