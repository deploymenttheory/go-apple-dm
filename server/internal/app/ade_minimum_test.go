package app

import (
	"context"
	"errors"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/ade"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/enroll/adetest"
)

// TestADEMinimumVersions covers literal selection, disabled settings and unsupported enforcement branches.
func TestADEMinimumVersions(t *testing.T) {
	all := ADEMinimumOS{IOS: "27.1", IPadOS: "27.2", MacOS: "27.3", VisionOS: "27.4"}
	if err := all.validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		product, version, target string
		capable                  *bool
		want                     ade.Action
		fail                     bool
	}{
		{"iPhone15,2", "17.0", "27.1", new(true), ade.SoftwareUpdateRequired, false},
		{"iPad13,1", "17.0", "27.2", new(true), ade.SoftwareUpdateRequired, false},
		{"Mac14,7", "14.0", "27.3", new(true), ade.SoftwareUpdateRequired, false},
		{"RealityDevice14,1", "26.0", "27.4", new(true), ade.SoftwareUpdateRequired, false},
		{"iPhone15,2", "27.1", "", nil, ade.Proceed, false},
		{"iPad13,1", "27.2", "", new(false), ade.Proceed, false},
		{"Mac14,7", "28.0", "", new(false), ade.Proceed, false},
		{"RealityDevice14,1", "27.4", "", nil, ade.Proceed, false},
		{"AppleTV14,1", "26.0", "", new(true), ade.Proceed, false},
		{"Watch7,1", "26.0", "", new(true), ade.Proceed, false},
		{"Unknown1,1", "27.0", "", new(true), ade.Proceed, true},
		{"iPhone15,2", "16.0", "", new(true), ade.Proceed, true},
		{"iPad13,1", "16.0", "", new(true), ade.Proceed, true},
		{"Mac14,7", "13.0", "", new(true), ade.Proceed, true},
		{"RealityDevice14,1", "2.0", "", new(true), ade.Proceed, true},
		{"Mac14,7", "26.0", "", nil, ade.Proceed, true},
		{"iPhone15,2", "26.0", "", new(false), ade.Proceed, true},
		{"Mac14,7", "", "", new(true), ade.Proceed, true},
		{"Mac14,7", "invalid", "", new(true), ade.Proceed, true},
		{"Mac14,7", "0", "", new(true), ade.Proceed, true},
	} {
		t.Run(tc.product+"/"+tc.version, func(t *testing.T) {
			p := &ade.Parsed{MachineInfo: adetest.Info("minimum"), Platform: ade.PlatformFromProduct(tc.product)}
			p.PRODUCT, p.OSVERSION, p.MDMCANREQUESTSOFTWAREUPDATE = tc.product, tc.version, tc.capable
			d, err := ade.Gate(t.Context(), p, all, nil, nil)
			if tc.fail {
				if !errors.Is(err, ade.ErrGate) {
					t.Fatalf("%+v %v", d, err)
				}
				return
			}
			if err != nil || d.Action != tc.want {
				t.Fatalf("%+v %v", d, err)
			}
			if tc.target != "" && d.SoftwareUpdate.Details.OSVersion != tc.target {
				t.Fatal("literal minimum changed")
			}
		})
	}
	p := &ade.Parsed{MachineInfo: adetest.Info("disabled")}
	p.PRODUCT, p.Platform, p.OSVERSION = "Unknown1,1", ade.PlatformUnknown, ""
	if d, err := ade.Gate(t.Context(), p, ADEMinimumOS{}, nil, nil); err != nil || d.Action != ade.Proceed {
		t.Fatal(d, err)
	}
	if _, required, err := (ADEMinimumOS{MacOS: "27"}).MinimumOS(context.Background(), &ade.Parsed{Platform: ade.PlatformIPhone}); err != nil || required {
		t.Fatal(required, err)
	}
	for _, invalid := range []string{"bad", "0", "27.1.2.3", " 27.1", "-1", "27..1", "+27", "latest", "27 "} {
		if err := (ADEMinimumOS{IOS: invalid}).validate(); !errors.Is(err, ErrConfig) {
			t.Fatal(invalid, err)
		}
	}
	if _, _, err := (ADEMinimumOS{MacOS: "bad"}).MinimumOS(t.Context(), &ade.Parsed{Platform: ade.PlatformMac, MachineInfo: ade.MachineInfo{OSVERSION: "26"}}); err == nil {
		t.Fatal("invalid programmatic target accepted")
	}
	cfg, err := ParseEnv(func(key string) string {
		return map[string]string{EnvStorage: "inmem", EnvADEMinimumIOS: "27.1", EnvADEMinimumIPadOS: "27.2", EnvADEMinimumMacOS: "27.3", EnvADEMinimumVisionOS: "27.4"}[key]
	})
	if err != nil || cfg.Enroll.ADEMinimumOS != all {
		t.Fatal(cfg.Enroll.ADEMinimumOS, err)
	}
	for _, key := range []string{"DM_ADE_MINIMUM_TVOS", "DM_ADE_MINIMUM_WATCHOS"} {
		if _, err := ParseEnv(func(k string) string {
			if k == key {
				return "27"
			}
			return ""
		}); !errors.Is(err, ErrConfig) {
			t.Fatal("unsupported requirement accepted", key, err)
		}
	}
	if err := (EnrollConfig{PublicURL: "https://mdm.example", Topic: "topic", ADEMinimumOS: ADEMinimumOS{MacOS: "latest"}}).validate(); !errors.Is(err, ErrConfig) {
		t.Fatal("invalid enrollment configuration accepted", err)
	}
}
