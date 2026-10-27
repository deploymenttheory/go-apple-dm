package schemagen

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// TestHistoryRetainsLegacySupportAndNewFields checks that history retains legacy support and new
// fields.
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
			support.Target{OS: support.MacOS, Version: osversion.New(tc.major, 0, 0)},
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

// TestHistoryMovesAndRemovedSchemas checks history moves and removed schemas.
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

// TestHistoryPreservesLegacyProfileRepresentation checks that history preserves legacy profile
// representation.
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

// TestHistoryProvenanceAndFailure checks that every retained release is merged and recorded
// newest first, and that missing or invalid releases fail.
func TestHistoryProvenanceAndFailure(t *testing.T) {
	t.Parallel()
	root := auditFixture(t, map[string]string{"mdm/commands/a.yaml": auditSchema})
	retained := func(key string) string {
		return auditFixture(t, map[string]string{"mdm/commands/a.yaml": auditSchema +
			"- key: " + key + "\n  type: <string>\n  presence: optional\n"})
	}
	newer, older := retained("NewerRetained"), retained("OlderRetained")
	opts := Options{Histories: []string{newer, older}}
	files, err := Run(root, opts)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := Write(out, files); err != nil {
		t.Fatal(err)
	}
	provenance, err := ReadGeneratedFrom(filepath.Join(out, generatedFromFile))
	if err != nil || len(provenance.History) != 2 {
		t.Fatalf("%+v %v", provenance, err)
	}
	for i, history := range []string{newer, older} {
		sum, err := yamlSHA256(history)
		if err != nil || provenance.History[i].YAMLSHA256 != sum {
			t.Fatalf("history %d = %+v, want %s (%v)", i, provenance.History[i], sum, err)
		}
	}
	var generated []byte
	for _, data := range files {
		generated = append(generated, data...)
	}
	for _, key := range []string{"NewerRetained", "OlderRetained"} {
		if !bytes.Contains(generated, []byte(key)) {
			t.Errorf("retained key %s was not generated", key)
		}
	}
	if err := Verify(root, out, opts); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(root, Options{Histories: []string{filepath.Join(t.TempDir(), "missing")}}); err == nil {
		t.Fatal("missing history accepted")
	}
	if err := os.WriteFile(
		filepath.Join(root, "mdm", "commands", "a.yaml"),
		[]byte("title: [invalid]"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(schemaRoot(t), Options{Histories: []string{root}}); err == nil {
		t.Fatal("invalid history accepted")
	}
}

// TestWithdrawnZeroRemovalIsUnavailable checks withdrawn zero removal is unavailable.
func TestWithdrawnZeroRemovalIsUnavailable(t *testing.T) {
	t.Parallel()
	entry, err := convertOS(nil, &SupportedOS{MacOS: &OSSupport{Introduced: "10.0", Removed: "0"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []int{10, 14, 15, 26, 27} {
		if got := (&support.Entry{OS: entry}).Check(
			support.Target{OS: support.MacOS, Version: osversion.New(version, 0, 0)},
		); got.Supported {
			t.Fatalf("withdrawn property enabled on %d", version)
		}
	}
}
