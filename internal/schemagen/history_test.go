package schemagen

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

func TestHistoryRetainsLegacySupportAndNewFields(t *testing.T) {
	t.Parallel()
	old := &Tree{Schemas: []*Schema{
		{
			Path:   "mdm/profiles/firewall.yaml",
			Title:  "Firewall",
			Family: FamilyProfiles,
			Payload: Payload{
				PayloadType: "firewall",
				SupportedOS: SupportedOS{MacOS: &OSSupport{Introduced: "12.0"}},
			},
			PayloadKeys: []Key{
				{
					Key:         "Logging",
					Type:        "<boolean>",
					SupportedOS: &SupportedOS{MacOS: &OSSupport{Removed: "15.0"}},
				},
				{Key: "Shared", Type: "<string>"},
			},
		},
	}}
	current := &Tree{Schemas: []*Schema{{
		Path: "mdm/profiles/firewall.yaml", Title: "Firewall", Family: FamilyProfiles,
		Payload: old.Schemas[0].Payload,
		PayloadKeys: []Key{
			{
				Key:         "New",
				Type:        "<string>",
				SupportedOS: &SupportedOS{MacOS: &OSSupport{Introduced: "27.0"}},
			},
			{Key: "Shared", Type: "<string>"},
		},
	}}}
	merged, err := MergeHistory(current, old)
	if err != nil {
		t.Fatal(err)
	}
	if len(current.Schemas[0].PayloadKeys) != 2 || len(old.Schemas[0].PayloadKeys) != 2 {
		t.Fatal("input mutated")
	}
	pkgs, err := Build(merged)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := effective(pkgs[0].Schemas[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key     string
		major   int
		allowed bool
	}{
		{"Logging", 12, true},
		{"Logging", 14, true},
		{"Logging", 15, false},
		{"Logging", 26, false},
		{"New", 15, false},
		{"New", 26, false},
		{"New", 27, true},
		{"Shared", 15, true},
		{"Shared", 26, true},
		{"Shared", 27, true},
	} {
		if got := entries["Firewall."+tc.key].Check(
			support.Target{OS: support.MacOS, Version: support.V(tc.major, 0, 0)},
		); got.Supported != tc.allowed {
			t.Fatalf("%+v: %+v", tc, got)
		}
	}
	keys := merged.Schemas[0].PayloadKeys
	if keys[0].Key != "Logging" || keys[1].Key != "Shared" || keys[2].Key != "New" {
		t.Fatal("historical emission order changed")
	}
	current.Schemas[0].PayloadKeys[1].Type = "<integer>"
	if _, err := MergeHistory(current, old); !errors.Is(err, ErrNaming) {
		t.Fatalf("incompatible wire type: %v", err)
	}
}

func TestHistoryMovesAndRemovedSchemas(t *testing.T) {
	t.Parallel()
	old := &Tree{
		Schemas: []*Schema{
			{
				Path:    "mdm/profiles/old.yaml",
				Title:   "Moved",
				Family:  FamilyProfiles,
				Payload: Payload{PayloadType: "id"},
			},
		},
	}
	current := &Tree{
		Schemas: []*Schema{
			{
				Path:    "mdm/profiles/new.yaml",
				Title:   "Moved",
				Family:  FamilyProfiles,
				Payload: Payload{PayloadType: "id"},
			},
		},
	}
	merged, err := MergeHistory(current, old)
	if err != nil || len(merged.Schemas) != 1 || merged.Schemas[0].Path != current.Schemas[0].Path {
		t.Fatalf("%+v: %v", merged, err)
	}
	current.Schemas = nil
	merged, err = MergeHistory(current, old)
	if err != nil || !reflect.DeepEqual(merged.Schemas, old.Schemas) {
		t.Fatalf("removed schema not retained: %+v %v", merged, err)
	}
	// Ambiguous identities must not silently collapse distinct payloads.
	current.Schemas = []*Schema{
		{Path: "mdm/profiles/a.yaml", Payload: Payload{PayloadType: "id"}, Family: FamilyProfiles},
		{Path: "mdm/profiles/b.yaml", Payload: Payload{PayloadType: "id"}, Family: FamilyProfiles},
	}
	if moves := historyMoves(current, old); len(moves) != 0 {
		t.Fatal(moves)
	}
}

func TestHistoryPreservesLegacyProfileRepresentation(t *testing.T) {
	t.Parallel()
	path := "declarative/declarations/configurations/legacy.yaml"
	old := &Tree{
		Schemas: []*Schema{
			{
				Path:        path,
				Title:       "Legacy Profile",
				Family:      FamilyDDM,
				PayloadKeys: []Key{{Key: "ProfileURL", Type: "<string>", Presence: "required"}},
			},
		},
	}
	current := &Tree{
		Schemas: []*Schema{
			{
				Path:   path,
				Title:  "Legacy Profile",
				Family: FamilyDDM,
				PayloadKeys: []Key{
					{Key: "ProfileURL", Type: "<string>"},
					{Key: "ProfileAssetReference", Type: "<string>"},
				},
			},
		},
	}
	merged, err := MergeHistory(current, old)
	if err != nil {
		t.Fatal(err)
	}
	pkgs, err := Build(merged)
	if err != nil {
		t.Fatal(err)
	}
	files, err := Generate(pkgs, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ProfileURL            string", "MarshalPlist()", "MarshalJSON()", "wireValue()"} {
		if !strings.Contains(string(files["ddm/types.gen.go"]), want) {
			t.Fatalf("missing %s", want)
		}
	}
	if !bytes.Contains(
		files["ddm/validate.gen.go"],
		[]byte("ProfileURL or ProfileAssetReference"),
	) {
		t.Fatal("missing alternative validation")
	}
	current.Schemas[0].Path = "unreviewed"
	old.Schemas[0].Path = "unreviewed"
	if _, err := MergeHistory(current, old); !errors.Is(err, ErrNaming) {
		t.Fatalf("unreviewed presence relaxation: %v", err)
	}
}

func TestHistoryProvenanceAndFailure(t *testing.T) {
	t.Parallel()
	root := auditFixture(t, map[string]string{"mdm/commands/a.yaml": auditSchema})
	files, err := Run(root, Options{History: root})
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := Write(out, files); err != nil {
		t.Fatal(err)
	}
	provenance, err := ReadGeneratedFrom(filepath.Join(out, generatedFromFile))
	if err != nil || provenance.History == nil ||
		provenance.History.YAMLSHA256 != provenance.YAMLSHA256 {
		t.Fatalf("%+v %v", provenance, err)
	}
	if err := Verify(root, out, Options{History: root}); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(root, Options{History: filepath.Join(t.TempDir(), "missing")}); err == nil {
		t.Fatal("missing history accepted")
	}
	if err := os.WriteFile(
		filepath.Join(root, "mdm", "commands", "a.yaml"),
		[]byte("title: [invalid]"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(schemaRoot(t), Options{History: root}); err == nil {
		t.Fatal("invalid history accepted")
	}
}

func TestWithdrawnZeroRemovalIsUnavailable(t *testing.T) {
	t.Parallel()
	entry, err := convertOS(nil, &SupportedOS{MacOS: &OSSupport{Introduced: "10.0", Removed: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{10, 14, 15, 26, 27} {
		if got := (&support.Entry{OS: entry}).Check(
			support.Target{OS: support.MacOS, Version: support.V(version, 0, 0)},
		); got.Supported {
			t.Fatalf("withdrawn property enabled on %d", version)
		}
	}
}
