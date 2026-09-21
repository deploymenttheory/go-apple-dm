//go:build schema_seed_os_27

package ddm_test

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	json "encoding/json/v2"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/mdmprotocol/mdm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// Fixtures are the same files operators review for the live handover. Explicit
// expected OS/version combinations make this independent of generated metadata.
func TestSeedOS27FeatureFixtures(t *testing.T) {
	root, err := os.OpenRoot("../../../test-lab/apple-features")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	data, err := root.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Features []struct {
			ID           string          `json:"id"`
			File         string          `json:"file"`
			Platforms    []support.OS    `json:"platforms"`
			Versions     []string        `json:"versions"`
			MacOSChannel support.Channel `json:"macOSChannel"`
		} `json:"features"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	coverage, err := root.ReadFile("macos27-coverage.json")
	if err != nil {
		t.Fatal(err)
	}
	var reviewed struct {
		Fixtures []string `json:"fixtures"`
	}
	if err := json.Unmarshal(coverage, &reviewed); err != nil {
		t.Fatal(err)
	}
	var fixtureIDs []string
	for _, f := range manifest.Features {
		fixtureIDs = append(fixtureIDs, f.ID)
	}
	slices.Sort(fixtureIDs)
	if !slices.Equal(fixtureIDs, reviewed.Fixtures) {
		t.Fatal("fixture IDs differ from the reviewed coverage inventory", fixtureIDs)
	}
	for _, feature := range manifest.Features {
		t.Run(feature.ID, func(t *testing.T) {
			raw, err := root.ReadFile(feature.File)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ddm.ParseDeclaration(raw, support.Target{}); err != nil {
				t.Fatal("invalid fixture", err)
			}
			for _, platform := range support.AllOS {
				for _, version := range []string{"26.0", "26.4", "26.6.2", "27.0"} {
					target := support.Target{OS: platform, Version: osversion.MustParse(version), Channel: support.ChannelDevice, Supervised: true, DEP: true, UserApproved: true}
					if platform == support.MacOS && feature.MacOSChannel != "" {
						target.Channel = feature.MacOSChannel
					}
					_, err := ddm.ParseDeclaration(raw, target)
					want := slices.Contains(feature.Platforms, platform) && slices.Contains(feature.Versions, version)
					if (err == nil) != want {
						t.Errorf("%s %s allowed=%v, want %v: %v", platform, version, err == nil, want, err)
					}
				}
			}
		})
	}
}

// Exercise server delivery as well as schema validation. Dependencies use the
// same fixture identifiers as the bundles prepared for physical-device tests.
func TestSeedOS27FeatureDelivery(t *testing.T) {
	t.Run("array-asset-dependencies", checkArrayAssetReferences)
	root, err := os.OpenRoot("../../../test-lab/apple-features")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	raw, err := root.ReadFile("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Features []struct {
			ID           string          `json:"id"`
			File         string          `json:"file"`
			Platforms    []support.OS    `json:"platforms"`
			Versions     []string        `json:"versions"`
			MacOSChannel support.Channel `json:"macOSChannel"`
		} `json:"features"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, platform := range support.AllOS {
		for _, version := range []string{"26.0", "26.4", "26.6.2", "27.0"} {
			t.Run(string(platform)+"/"+version, func(t *testing.T) {
				target := support.Target{OS: platform, Version: osversion.MustParse(version), Channel: support.ChannelDevice, Supervised: true, DEP: true, UserApproved: true}
				h := newHarness(t, func(c *ddm.Config) {
					c.EnrollmentTarget = func(_ context.Context, id mdm.EnrollmentID) (support.Target, error) {
						resolved := target
						if id.Channel == mdm.ChannelUser {
							resolved.Channel = support.ChannelUser
						}
						return resolved, nil
					}
				})
				deviceID := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "feature-device"}
				userID := mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "feature-user", ParentID: deviceID.ID}
				ids := []mdm.EnrollmentID{deviceID}
				if platform == support.MacOS {
					ids = append(ids, userID)
				}
				var declarations []*ddm.Declaration
				for _, f := range manifest.Features {
					body, err := root.ReadFile(f.File)
					if err != nil {
						t.Fatal(err)
					}
					d, _, err := h.engine.PutDeclaration(t.Context(), body)
					if err != nil {
						t.Fatal(err)
					}
					for _, id := range ids {
						if _, err := h.engine.AssignDeclaration(t.Context(), id, d.Identifier); err != nil {
							t.Fatal(err)
						}
					}
					declarations = append(declarations, d)
				}
				for i, f := range manifest.Features {
					d := declarations[i]
					id := deviceID
					if platform == support.MacOS && f.MacOSChannel == support.ChannelUser {
						id = userID
					}
					snapshot, err := h.engine.Manifest(t.Context(), id)
					if err != nil {
						t.Fatal(err)
					}
					body, err := h.engine.Declaration(t.Context(), id, d.Kind, d.Identifier)
					want := slices.Contains(f.Platforms, platform) && slices.Contains(f.Versions, version)
					if !want {
						if !errors.Is(err, ddm.ErrNotFound) {
							t.Errorf("%s: unsupported declaration served: %v", f.ID, err)
						}
						continue
					}
					if err != nil {
						t.Errorf("%s: compatible declaration withheld: %v", f.ID, err)
						continue
					}
					// Compare against the authored fixture, not the stored canonical
					// declaration: otherwise an upload-time loss would go unnoticed.
					authored, err := root.ReadFile(f.File)
					if err != nil {
						t.Fatal(err)
					}
					checkFixturePayload(t, f.ID, authored, body)
					var served struct{ ServerToken string }
					if err := json.Unmarshal(body, &served); err != nil {
						t.Fatal(err)
					}
					found := false
					for _, item := range snapshot.Items {
						if item.Identifier == d.Identifier {
							found = true
							if item.ServerToken != served.ServerToken {
								t.Errorf("%s: token mismatch", f.ID)
							}
						}
					}
					if !found {
						t.Errorf("%s: served without advertisement", f.ID)
					}
				}
			})
		}
	}
}

// checkFixturePayload checks that serving a declaration changes only its server-authored
// ServerToken.
func checkFixturePayload(t *testing.T, id string, authored, served []byte) {
	t.Helper()
	var wire map[string]jsontext.Value
	if err := json.Unmarshal(served, &wire); err != nil {
		t.Fatal(err)
	}
	// The fixture harness has no dynamic expansion. ServerToken is the only
	// server-authored member; identifiers, types and every payload value survive.
	delete(wire, "ServerToken")
	got, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	want := jsontext.Value(bytes.Clone(authored))
	actual := jsontext.Value(got)
	if err := want.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	if err := actual.Canonicalize(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(want, actual) {
		t.Errorf("%s: delivered declaration changed fixture payload\nwant: %s\n got: %s", id, want, actual)
	}
}

// checkArrayAssetReferences checks that relay eligibility follows assignment and removal of its
// referenced array asset.
func checkArrayAssetReferences(t *testing.T) {
	h := newHarness(t, func(c *ddm.Config) {
		c.EnrollmentTarget = func(context.Context, mdm.EnrollmentID) (support.Target, error) {
			return support.Target{OS: support.MacOS, Version: osversion.New(osversion.MacOS27, 0, 0), Supervised: true}, nil
		}
	})
	id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "relay"}
	raw := declJSON("com.apple.configuration.network.relay", "relay", map[string]any{"VisibleName": "Test Relay", "Relays": []any{map[string]any{"HTTP3RelayURL": "https://example.test", "PublicKeyData": []string{"public-key"}}}})
	d, _, err := h.engine.PutDeclaration(t.Context(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.AssignDeclaration(t.Context(), id, d.Identifier); err != nil {
		t.Fatal(err)
	}
	check := func(allowed bool) {
		t.Helper()
		r, err := h.engine.Compatibility(t.Context(), id)
		if err != nil {
			t.Fatal(err)
		}
		present := false
		for _, entry := range r.Eligible {
			if entry.Identifier == "relay" {
				present = true
			}
		}
		if present != allowed {
			t.Fatalf("allowed=%v: %+v", allowed, r)
		}
	}
	check(false)
	if _, _, err := h.engine.PutDeclaration(t.Context(), assetData("public-key", "https://example.test/key")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.AssignDeclaration(t.Context(), id, "public-key"); err != nil {
		t.Fatal(err)
	}
	check(true)
	if _, err := h.engine.UnassignDeclaration(t.Context(), id, "public-key"); err != nil {
		t.Fatal(err)
	}
	check(false)
}

// TestSeedOS27FeatureContextWithholding checks that unsupported OS 27 enrollment contexts
// reject declarations and withhold them from manifests and direct delivery.
func TestSeedOS27FeatureContextWithholding(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		target     support.Target
	}{
		{"app privacy requires Mac user scope", "app-privacy", support.Target{OS: support.MacOS, Channel: support.ChannelDevice, Supervised: true}},
		{"website privacy requires Mac user scope", "website-privacy", support.Target{OS: support.MacOS, Channel: support.ChannelDevice, Supervised: true}},
		{"binary controls require Mac system scope", "binary-controls", support.Target{OS: support.MacOS, Channel: support.ChannelUser, Supervised: true}},
		{"Siri requires supervision", "siri", support.Target{OS: support.MacOS, Channel: support.ChannelDevice}},
		{"accessibility requires supervision", "accessibility", support.Target{OS: support.MacOS, Channel: support.ChannelDevice}},
		{"web content filter requires Mac system scope", "webcontent-filter", support.Target{OS: support.MacOS, Channel: support.ChannelUser, Supervised: true}},
		{"web content filter requires supervision for MDM", "webcontent-filter", support.Target{OS: support.MacOS, Channel: support.ChannelDevice}},
		{"Siri AI unavailable on Shared iPad", "siri", support.Target{OS: support.IOS, Channel: support.ChannelUser, Supervised: true, SharedIPad: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := tc.target
			target.Version = osversion.New(27, 0, 0)
			h := newHarness(t, func(c *ddm.Config) {
				c.EnrollmentTarget = func(context.Context, mdm.EnrollmentID) (support.Target, error) { return target, nil }
			})
			id := mdm.EnrollmentID{Channel: mdm.ChannelDevice, ID: "context-device"}
			if target.Channel == support.ChannelUser {
				id = mdm.EnrollmentID{Channel: mdm.ChannelUser, ID: "context-user", ParentID: id.ID}
				if target.SharedIPad {
					id.Channel = mdm.ChannelSharedIPadUser
				}
			}
			raw, err := os.ReadFile("../../../test-lab/apple-features/declarations/" + tc.file + ".json")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := ddm.ParseDeclaration(raw, target); err == nil {
				t.Fatal("unsupported context accepted")
			}
			d, _, err := h.engine.PutDeclaration(t.Context(), raw)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.engine.AssignDeclaration(t.Context(), id, d.Identifier); err != nil {
				t.Fatal(err)
			}
			snapshot, err := h.engine.Manifest(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Items) != 0 {
				t.Fatal("unsupported declaration advertised")
			}
			if _, err := h.engine.Declaration(t.Context(), id, d.Kind, d.Identifier); !errors.Is(err, ddm.ErrNotFound) {
				t.Fatalf("unsupported declaration served: %v", err)
			}
		})
	}
}
