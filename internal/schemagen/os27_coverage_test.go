//go:build schema_seed_os_27

package schemagen

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/commands"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/ddm"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/other"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/profiles"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/status"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

type os27Boundary struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
}

// The reviewed inventory is independent of generated Go names. Missing source
// cases, fields, fixtures or prose-only enum floors must not become silent passes.
func TestSeedOS27CoverageInventory(t *testing.T) {
	var inventory struct {
		SchemaCommit  string   `json:"schemaCommit"`
		HistoryCommit string   `json:"historyCommit"`
		Fixtures      []string `json:"fixtures"`
		Cases         []struct {
			Source     string         `json:"source"`
			Boundaries []os27Boundary `json:"boundaries"`
			Fixtures   []string       `json:"fixtures"`
			NativePlan string         `json:"nativePlan"`
		} `json:"cases"`
		ValueFloors map[string][]string `json:"valueFloors"`
		NonMacCases []struct {
			Source string `json:"source"`
			Reason string `json:"reason"`
		} `json:"nonMacCases"`
	}
	readOS27JSON(t, "test-lab/apple-features/macos27-coverage.json", &inventory)
	var provenance struct {
		Commit  string `json:"commit"`
		History struct {
			Commit string `json:"commit"`
		} `json:"history"`
	}
	readOS27JSON(t, "devicemanagement/schema/GENERATED_FROM.json", &provenance)
	if inventory.SchemaCommit != provenance.Commit || inventory.HistoryCommit != provenance.History.Commit {
		t.Fatal("schema inputs changed: review the macOS 27 coverage inventory")
	}
	var manifest struct {
		Features []struct {
			ID   string `json:"id"`
			File string `json:"file"`
		} `json:"features"`
	}
	readOS27JSON(t, "test-lab/apple-features/manifest.json", &manifest)
	var fixtureIDs []string
	fixtureKinds := map[string]string{}
	for _, f := range manifest.Features {
		fixtureIDs = append(fixtureIDs, f.ID)
		var declaration struct{ Type string }
		readOS27JSON(t, "test-lab/apple-features/"+f.File, &declaration)
		fixtureKinds[f.ID] = declaration.Type
	}
	slices.Sort(fixtureIDs)
	if !slices.Equal(fixtureIDs, inventory.Fixtures) {
		t.Fatal("fixture IDs differ from the reviewed coverage inventory", fixtureIDs)
	}
	tree, err := Load("../../third_party/apple-device-management/current")
	if err != nil {
		t.Fatal(err)
	}
	reviewed := map[string][]os27Boundary{}
	for _, c := range inventory.Cases {
		if _, exists := reviewed[c.Source]; exists || len(c.Boundaries) == 0 || c.NativePlan == "" {
			t.Fatal("duplicate or incomplete coverage case", c.Source)
		}
		for _, id := range c.Fixtures {
			if !slices.Contains(fixtureIDs, id) {
				t.Errorf("%s: unknown fixture %s", c.Source, id)
			}
		}
		reviewed[c.Source] = c.Boundaries
	}
	bySource := map[string]*Schema{}
	for _, s := range tree.Schemas {
		bySource[s.Path] = s
		actual := explicitMacOS27Boundaries(s)
		if len(actual) == 0 {
			continue
		}
		if !slices.Equal(actual, reviewed[s.Path]) {
			t.Errorf("unreviewed macOS 27 boundary in %s: got %+v, reviewed %+v", s.Path, actual, reviewed[s.Path])
		}
		delete(reviewed, s.Path)
	}
	for source := range reviewed {
		t.Errorf("reviewed boundary no longer exists: %s", source)
	}
	for _, c := range inventory.Cases {
		s := bySource[c.Source]
		if s == nil {
			continue
		}
		for _, id := range c.Fixtures {
			if fixtureKinds[id] != s.Payload.Identifier() {
				t.Errorf("%s: fixture %s tests a different declaration type", c.Source, id)
			}
		}
	}
	for _, c := range inventory.NonMacCases {
		s := bySource[c.Source]
		if s == nil || s.Payload.SupportedOS.MacOS == nil || s.Payload.SupportedOS.MacOS.Introduced != "n/a" || c.Reason == "" {
			t.Errorf("invalid non-Mac disposition: %s", c.Source)
		}
	}
	if !reflect.DeepEqual(inventory.ValueFloors, ssoValueFloors) {
		t.Fatal("reviewed prose-only SSO value floors changed")
	}
	checkOS27InheritedBoundaries(t)
	for path, values := range inventory.ValueFloors {
		for _, value := range values {
			entry := profiles.ValueSupport(path, value)
			if entry == nil {
				t.Fatalf("missing value boundary: %s=%s", path, value)
			}
			for _, version := range []string{"26.0", "26.4", "26.6.2", "27.0"} {
				target := boundaryTarget(support.MacOS, osversion.MustParse(version), "device")
				if got := entry.Check(target).Supported; got != (version == "27.0") {
					t.Errorf("%s=%s on %s: supported=%v", path, value, version, got)
				}
			}
		}
	}
}

func readOS27JSON(t *testing.T, path string, target any) {
	t.Helper()
	root, err := os.OpenRoot("../..")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	raw, err := root.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		t.Fatal(err)
	}
}

func explicitMacOS27Boundaries(s *Schema) []os27Boundary {
	var result []os27Boundary
	add := func(path string, m *SupportedOS) {
		if m == nil || m.MacOS == nil {
			return
		}
		for kind, version := range map[string]string{"introduced": m.MacOS.Introduced, "deprecated": m.MacOS.Deprecated, "removed": m.MacOS.Removed} {
			if strings.HasPrefix(version, "27.") {
				result = append(result, os27Boundary{Path: path, Kind: kind, Version: version})
			}
		}
	}
	add("$", &s.Payload.SupportedOS)
	var walk func([]Key, string)
	walk = func(keys []Key, prefix string) {
		for _, k := range keys {
			path := prefix + "." + k.Key
			add(path, k.SupportedOS)
			walk(k.Subkeys, path)
		}
	}
	walk(s.PayloadKeys, "payload")
	walk(s.ResponseKeys, "response")
	slices.SortFunc(result, func(a, b os27Boundary) int { return strings.Compare(a.Path+":"+a.Kind, b.Path+":"+b.Kind) })
	return result
}

func checkOS27InheritedBoundaries(t *testing.T) {
	t.Helper()
	// Resolve inheritance using the source model and retained historical schema,
	// then compare every affected descendant with the compiled public tables.
	tables, _, err := sourceSupport("../../third_party/apple-device-management/current", compatibilitySchemaRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	lookup := map[string]func(string) *support.Entry{
		"commands": commands.Support, "ddm": ddm.Support, "other": other.Support,
		"profiles": profiles.Support, "status": status.Support,
	}
	checked := 0
	for family, table := range tables {
		for path, expected := range table {
			m := expected.OS[support.MacOS]
			if m == nil || (m.Introduced.Major != osversion.MacOS27 && m.Deprecated.Major != osversion.MacOS27 && m.Removed.Major != osversion.MacOS27) {
				continue
			}
			find := lookup[family]
			if find == nil {
				t.Fatalf("unreviewed generated family %s", family)
			}
			actual := find(path)
			if actual == nil || !reflect.DeepEqual(actual.OS[support.MacOS], m) {
				t.Errorf("generated metadata differs from inherited source: %s/%s", family, path)
				continue
			}
			checked++
			for _, version := range []string{"26.0", "26.4", "26.6.2", "27.0"} {
				for _, channel := range []string{"device", "user", "unsupervised", "non-dep", "not-user-approved", "user-enrollment"} {
					target := boundaryTarget(support.MacOS, osversion.MustParse(version), channel)
					want, got := expected.Check(target), actual.Check(target)
					if want.Supported != got.Supported || want.Deprecated != got.Deprecated {
						t.Errorf("boundary mismatch %s/%s %s %s", family, path, version, channel)
					}
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no inherited macOS 27 boundaries checked")
	}
	t.Logf("checked %d inherited macOS 27 paths across four versions and six enrollment contexts", checked)
}

func compatibilitySchemaRoot(t *testing.T) string {
	t.Helper()
	command := exec.Command("git", "config", "--file", "../../.gitmodules", "--get", "submodule.apple-device-management-compatibility.path")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return "../../" + strings.TrimSpace(string(output))
}
