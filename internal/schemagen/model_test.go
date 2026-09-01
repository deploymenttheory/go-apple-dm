package schemagen

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// schemaRoot returns the vendored Apple schema, skipping when the submodule
// is not initialised.
func schemaRoot(t *testing.T) string {
	t.Helper()
	root := filepath.Join("..", "..", "third_party", "device-management")
	if _, err := os.Stat(filepath.Join(root, "docs", "schema.yaml")); err != nil {
		t.Skip("apple/device-management submodule not initialised")
	}
	return root
}

func TestLoadAllYAMLNoUnknownKeys(t *testing.T) {
	t.Parallel()
	tree, err := Load(schemaRoot(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	counts := map[Family]int{}
	for _, s := range tree.Schemas {
		counts[s.Family]++
		if s.Path == "" || s.Title == "" {
			t.Errorf("schema without path or title: %+v", s)
		}
	}
	// Counts from the pinned commit in schema/PROVENANCE.json. A change here
	// means Apple added or removed files: update PROVENANCE and this table.
	want := map[Family]int{
		FamilyCommands: 65, FamilyCheckin: 9, FamilyErrors: 5, FamilyProfiles: 127,
		FamilyDDM: 52, FamilyDDMProto: 3, FamilyStatus: 48, FamilyOther: 5,
	}
	for f, n := range want {
		if counts[f] != n {
			t.Errorf("family %s: got %d schemas, want %d", f, counts[f], n)
		}
	}
	for i := 1; i < len(tree.Schemas); i++ {
		if tree.Schemas[i-1].Path >= tree.Schemas[i].Path {
			t.Fatalf("schemas not sorted: %s >= %s", tree.Schemas[i-1].Path, tree.Schemas[i].Path)
		}
	}
	if got := len(tree.ByFamily(FamilyCheckin)); got != 9 {
		t.Errorf("ByFamily(checkin) = %d", got)
	}
}

func TestClassify(t *testing.T) {
	t.Parallel()
	cases := []struct {
		rel    string
		family Family
		kind   Kind
		err    bool
	}{
		{"mdm/commands/device.lock.yaml", FamilyCommands, KindDefault, false},
		{"mdm/checkin/authenticate.yaml", FamilyCheckin, KindDefault, false},
		{"mdm/errors/unrecognized.device.yaml", FamilyErrors, KindDefault, false},
		{"mdm/profiles/com.apple.mdm.yaml", FamilyProfiles, KindDefault, false},
		{"declarative/declarations/declarationbase.yaml", FamilyDDM, KindBase, false},
		{"declarative/declarations/activations/simple.yaml", FamilyDDM, KindActivation, false},
		{"declarative/declarations/assets/data.yaml", FamilyDDM, KindAsset, false},
		{"declarative/declarations/assets/credentials/scep.yaml", FamilyDDM, KindCredential, false},
		{"declarative/declarations/configurations/legacy.yaml", FamilyDDM, KindConfiguration, false},
		{"declarative/declarations/management/properties.yaml", FamilyDDM, KindManagement, false},
		{"declarative/protocol/statusreport.yaml", FamilyDDMProto, KindDefault, false},
		{"declarative/status/device.model.family.yaml", FamilyStatus, KindDefault, false},
		{"other/machineinfo.yaml", FamilyOther, KindDefault, false},
		{"docs/schema.yaml", familyUnknown, KindDefault, true},
	}
	for _, c := range cases {
		f, k, err := Classify(c.rel)
		if (err != nil) != c.err {
			t.Errorf("Classify(%q) err = %v, want err=%v", c.rel, err, c.err)
		}
		if c.err && !errors.Is(err, ErrUnknownFamily) {
			t.Errorf("Classify(%q) err = %v, want ErrUnknownFamily", c.rel, err)
		}
		if f != c.family || k != c.kind {
			t.Errorf("Classify(%q) = %s/%s, want %s/%s", c.rel, f, k, c.family, c.kind)
		}
	}
}

func TestParseStrict(t *testing.T) {
	t.Parallel()
	good := "title: T\npayload:\n  requesttype: X\npayloadkeys:\n- key: A\n  type: <string>\n  presence: required\n"
	s, err := Parse([]byte(good))
	if err != nil {
		t.Fatalf("Parse good: %v", err)
	}
	if s.Payload.Identifier() != "X" || !s.PayloadKeys[0].Required() {
		t.Fatalf("unexpected parse: %+v", s)
	}
	for name, bad := range map[string]string{
		"unknown top key":  "title: T\nbogus: 1\n",
		"unknown key prop": "title: T\npayloadkeys:\n- key: A\n  type: <string>\n  wat: 1\n",
		"missing title":    "description: no title\n",
		"not yaml":         "title: [unterminated\n",
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseBreaksRecursiveAnchors(t *testing.T) {
	t.Parallel()
	// Mirrors Apple's safari.bookmarks.yaml: a folder item whose subkeys alias
	// the enclosing subkeys sequence.
	doc := `title: Bookmarks
payloadkeys:
- key: Groups
  type: <array>
  subkeys: &items
  - key: Item
    type: <dictionary>
    subkeytype: BookmarkItem
    subkeys:
    - key: Title
      type: <string>
    - key: Children
      type: <array>
      subkeys: *items
`
	s, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	item := s.PayloadKeys[0].Subkeys[0]
	children := item.Subkeys[1]
	if children.RecursiveTo != "Groups" {
		t.Fatalf("RecursiveTo = %q, want Groups (owner of the aliased subkeys)", children.RecursiveTo)
	}
	if len(children.Subkeys) != 0 {
		t.Fatalf("recursive subkeys should be empty, got %d", len(children.Subkeys))
	}
	// A non-cyclic alias is preserved as a normal copy.
	shared := `title: Shared
payloadkeys:
- key: A
  type: <dictionary>
  subkeys: &id001
  - key: X
    type: <string>
- key: B
  type: <dictionary>
  subkeys: *id001
`
	s, err = Parse([]byte(shared))
	if err != nil {
		t.Fatalf("Parse shared: %v", err)
	}
	if len(s.PayloadKeys[1].Subkeys) != 1 || s.PayloadKeys[1].Subkeys[0].Key != "X" || s.PayloadKeys[1].RecursiveTo != "" {
		t.Fatalf("shared alias not expanded: %+v", s.PayloadKeys[1])
	}
	// Recursive alias where the owner has no subkeytype falls back to key name.
	noType := `title: T
payloadkeys:
- key: Tree
  type: <array>
  subkeys: &n
  - key: Node
    type: <dictionary>
    subkeys:
    - key: Kids
      type: <array>
      subkeys: *n
`
	s, err = Parse([]byte(noType))
	if err != nil {
		t.Fatalf("Parse noType: %v", err)
	}
	if got := s.PayloadKeys[0].Subkeys[0].Subkeys[0].RecursiveTo; got != "Tree" {
		t.Fatalf("RecursiveTo = %q, want Tree", got)
	}
	if ownerName(nil) != "" || ownerName(&yaml.Node{Kind: yaml.ScalarNode}) != "" || ownerName(&yaml.Node{Kind: yaml.MappingNode}) != "" {
		t.Fatal("ownerName of non-mapping or keyless mapping should be empty")
	}
}

func TestPayloadIdentifierOrder(t *testing.T) {
	t.Parallel()
	p := Payload{PayloadType: "pt", DeclarationType: "dt"}
	if p.Identifier() != "dt" {
		t.Fatalf("Identifier = %q, want declaration type first", p.Identifier())
	}
	if (Payload{}).Identifier() != "" {
		t.Fatal("empty payload should have empty identifier")
	}
}

func TestSupportedOSHelpers(t *testing.T) {
	t.Parallel()
	var s SupportedOS
	if !s.IsZero() {
		t.Fatal("zero SupportedOS should be zero")
	}
	s.MacOS = &OSSupport{Introduced: "13.0"}
	if s.IsZero() || s.ByName("macOS") == nil || s.ByName("iOS") != nil || s.ByName("nope") != nil {
		t.Fatal("ByName/IsZero mismatch")
	}
	for _, n := range OSNames {
		_ = s.ByName(n)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Parallel()
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected error for missing root")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "mdm", "commands"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mdm", "commands", "bad.yaml"), []byte("title: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected decode error")
	}
	if err := os.WriteFile(filepath.Join(dir, "mdm", "commands", "bad.yaml"), []byte("title: ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.yaml"), []byte("title: stray\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); !errors.Is(err, ErrUnknownFamily) {
		t.Fatalf("expected ErrUnknownFamily, got %v", err)
	}
	if _, err := LoadFile(filepath.Join(dir, "nope.yaml")); err == nil {
		t.Fatal("expected read error")
	}
}
