package schemagen

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// TestAvailabilityAuditCurrentTree requires every key use in the current
// schema, merged with each retained release as generation merges them, to be
// validated against its own source availability.
func TestAvailabilityAuditCurrentTree(t *testing.T) {
	t.Parallel()
	retained, err := RetainedReleases("../../.gitmodules")
	if err != nil {
		t.Fatal(err)
	}
	audit, err := AuditAvailability(schemaRoot(t), retained...)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range audit.Problems {
		t.Error(p)
	}
	t.Logf("audited %d key uses, %d problems", audit.Uses, len(audit.Problems))
}

// availabilityFixture repeats subkeytype Rule under four arrays: First and
// Third are available identically, Second narrows its parent, and Fourth's
// Action carries its own deprecation. Settings is a heterogeneous array whose
// Alpha variant has its own block, and Out is a response key.
const availabilityFixture = `title: Availability Test
payload:
  requesttype: AvailTest
  supportedOS:
    iOS:
      introduced: '15.0'
    macOS:
      introduced: '12.0'
payloadkeys:
- key: First
  type: <array>
  presence: optional
  subkeys:
  - key: FirstItem
    type: <dictionary>
    subkeytype: Rule
    subkeys:
    - key: Action
      type: <string>
      presence: required
- key: Second
  type: <array>
  presence: optional
  supportedOS:
    iOS:
      introduced: n/a
    macOS:
      introduced: '14.0'
  subkeys:
  - key: SecondItem
    type: <dictionary>
    subkeytype: Rule
    subkeys:
    - key: Action
      type: <string>
      presence: required
- key: Third
  type: <array>
  presence: optional
  subkeys:
  - key: ThirdItem
    type: <dictionary>
    subkeytype: Rule
    subkeys:
    - key: Action
      type: <string>
      presence: required
- key: Fourth
  type: <array>
  presence: optional
  subkeys:
  - key: FourthItem
    type: <dictionary>
    subkeytype: Rule
    subkeys:
    - key: Action
      type: <string>
      presence: required
      supportedOS:
        iOS:
          deprecated: '18.0'
- key: Settings
  type: <array>
  presence: optional
  subkeys:
  - key: Alpha
    type: <dictionary>
    supportedOS:
      iOS:
        introduced: '16.0'
      macOS:
        introduced: n/a
    subkeys:
    - key: Item
      type: <string>
      presence: required
  - key: Beta
    type: <dictionary>
    subkeys:
    - key: Item
      type: <string>
      presence: required
responsekeys:
- key: Out
  type: <string>
  presence: required
  supportedOS:
    macOS:
      deprecated: '26.0'
`

// TestSharedSubkeyTypeFollowsEachUsesAvailability checks that every use of a
// shared subkeytype has its own table entry and that generated validation
// reads the entry of the use it is validating.
func TestSharedSubkeyTypeFollowsEachUsesAvailability(t *testing.T) {
	t.Parallel()
	p, err := buildOne(t, "mdm/commands/avail.yaml", availabilityFixture)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	st := p.Schemas[0]
	shared := fieldByKey(st.Request, "First").Elem.Struct
	for _, key := range []string{"Second", "Third", "Fourth"} {
		if got := fieldByKey(st.Request, key).Elem.Struct; got != shared {
			t.Errorf("%s emits %s, want the shared %s", key, got.Name, shared.Name)
		}
	}
	table, err := effective(st)
	if err != nil {
		t.Fatal(err)
	}
	type want struct {
		os                              support.OS
		notAvailable                    bool
		introduced, deprecated, removed string
	}
	version := func(v string) osversion.Version {
		if v == "" {
			return osversion.Version{}
		}
		return osversion.MustParse(v)
	}
	for path, w := range map[string]want{
		"AvailTest.First.FirstItem.Action":   {os: support.IOS, introduced: "15.0"},
		"AvailTest.Second.SecondItem.Action": {os: support.IOS, notAvailable: true},
		"AvailTest.Third.ThirdItem.Action":   {os: support.IOS, introduced: "15.0"},
		"AvailTest.Fourth.FourthItem.Action": {os: support.IOS, introduced: "15.0", deprecated: "18.0"},
		"AvailTest.Settings.Alpha.Item":      {os: support.MacOS, notAvailable: true},
		"AvailTest.Settings.Beta.Item":       {os: support.MacOS, introduced: "12.0"},
		"AvailTest.response.Out":             {os: support.MacOS, introduced: "12.0", deprecated: "26.0"},
	} {
		entry := table[path]
		if entry == nil || entry.OS[w.os] == nil {
			t.Errorf("%s: no %s entry", path, w.os)
			continue
		}
		got := entry.OS[w.os]
		if got.NotAvailable != w.notAvailable || got.Introduced != version(w.introduced) ||
			got.Deprecated != version(w.deprecated) || got.Removed != version(w.removed) {
			t.Errorf("%s %s = %+v, want %+v", path, w.os, got, w)
		}
	}
	files, err := Generate([]*Package{p}, Options{Commit: "synthetic"})
	if err != nil {
		t.Fatal(err)
	}
	validate := string(files["commands/validate.gen.go"])
	for _, want := range []string{
		"\tx.ValidateInto(c)\n\treturn c.Err()\n",
		`func (x *AvailTest) ValidateInto(c *validation.Collector) { x.validate(c, "", "AvailTest") }`,
		`x.validate(c, "", "AvailTest")`,
		`x.validate(c, "", "AvailTest.response")`,
		"func (x *" + shared.Name + ") validate(c *validation.Collector, path, supportPath string)",
		`supportTable[supportPath+".Action"]`,
		`x.First[i].validate(c, validation.Index(fieldPath, i), supportPath+".First.FirstItem")`,
		`x.Second[i].validate(c, validation.Index(fieldPath, i), supportPath+".Second.SecondItem")`,
		`t.validate(c, itemPath, supportPath+".Settings.Alpha")`,
	} {
		if !strings.Contains(validate, want) {
			t.Errorf("validate.gen.go missing %q", want)
		}
	}
	registry := string(files["commands/registry.gen.go"])
	if strings.Count(registry, "\tValidateInto(c *validation.Collector)\n") != 2 {
		t.Errorf("Command and Response interfaces must declare ValidateInto:\n%s", registry)
	}
	supportFile := string(files["commands/support.gen.go"])
	for _, want := range []string{
		"\taddAvailTestSupport(supportTable)\n",
		"func addAvailTestSupport(table map[string]*support.Entry) {",
		`table["AvailTest.Third.ThirdItem.Action"] = &support.Entry{`,
	} {
		if !strings.Contains(supportFile, want) {
			t.Errorf("support.gen.go missing %q", want)
		}
	}
	if problems, _, err := auditAvailability([]*Package{p}, effective); err != nil || len(problems) != 0 {
		t.Fatalf("audit: %v %q", err, problems)
	}
	// A table keyed by struct rather than by use gives Second First's entry
	// and has none for Third; the audit must report both.
	stale := func(st *SchemaType) (map[string]*support.Entry, error) {
		table, err := effective(st)
		table["AvailTest.Second.SecondItem.Action"] = table["AvailTest.First.FirstItem.Action"]
		delete(table, "AvailTest.Third.ThirdItem.Action")
		return table, err
	}
	problems, _, err := auditAvailability([]*Package{p}, stale)
	if err != nil || len(problems) != 2 {
		t.Fatalf("audit of a struct-keyed table: %v %q", err, problems)
	}
	failing := func(*SchemaType) (map[string]*support.Entry, error) { return nil, errors.New("boom") }
	if _, _, err := auditAvailability([]*Package{p}, failing); err == nil {
		t.Fatal("audit ignored a table error")
	}
}

// TestChildSupportPath checks the support path passed to nested and recursive
// struct values.
func TestChildSupportPath(t *testing.T) {
	t.Parallel()
	top := &TypeDef{Name: "Top", Schema: &Schema{Family: FamilyCommands, Payload: Payload{RequestType: "Top"}}}
	owner := &TypeDef{Name: "Owner", KeyPath: "A", Nested: true, Schema: top.Schema}
	sibling := &TypeDef{Name: "Sibling", KeyPath: "AB", Nested: true, Schema: top.Schema}
	inner := &TypeDef{Name: "Inner", KeyPath: "A.B.C", Nested: true, Schema: top.Schema}
	resp := &TypeDef{Name: "Resp", KeyPath: "R", Nested: true, IsResponse: true, Schema: top.Schema}
	e := &emitter{pkg: &Package{Types: []*TypeDef{top, owner, sibling, inner, resp}}}
	for _, c := range []struct {
		td    *TypeDef
		child *TypeDef
		base  string
		want  string
	}{
		{inner, inner, "", `supportPath+".X"`},
		{inner, nil, "Missing", "supportPath"},
		{inner, nil, "Top", `"Top"`},
		{owner, nil, "Owner", "supportPath"},
		{inner, nil, "Owner", `strings.TrimSuffix(supportPath, ".B.C")`},
		{sibling, nil, "Owner", `"Top.A"`},
		{inner, nil, "Resp", `"Top.response.R"`},
	} {
		if got := e.childSupportPath(c.td, c.child, c.base, ".X"); got != c.want {
			t.Errorf("childSupportPath(%s, %s) = %s, want %s", c.td.Name, c.base, got, c.want)
		}
	}
}

// TestAvailabilityFailures checks invalid versions and recursion audits.
func TestAvailabilityFailures(t *testing.T) {
	t.Parallel()
	bad := &SupportedOS{IOS: &OSSupport{Introduced: "x"}}
	if _, err := keyAvailability(&Schema{Path: "p", Payload: Payload{SupportedOS: *bad}}); err == nil {
		t.Error("payload block with an invalid version accepted")
	}
	if _, err := keyAvailability(&Schema{Path: "p", PayloadKeys: []Key{{Key: "A", Subkeys: []Key{{Key: "B", SupportedOS: bad}}}}}); err == nil {
		t.Error("nested key with an invalid version accepted")
	}
	if _, err := keyAvailability(&Schema{Path: "p", ResponseKeys: []Key{{Key: "A", SupportedOS: bad}}}); err == nil {
		t.Error("response key with an invalid version accepted")
	}
	if _, err := effective(&SchemaType{Schema: &Schema{Path: "p", PayloadKeys: []Key{{Key: "A", SupportedOS: bad}}}}); err == nil {
		t.Error("effective accepted an invalid version")
	}
	orphan := &SchemaType{Name: "T", Schema: &Schema{Path: "p"}, Request: &TypeDef{Fields: []*Field{{Key: "Missing", Src: &Key{}}}}}
	if _, err := effective(orphan); err == nil {
		t.Error("effective accepted a field without a source key")
	}
	narrowed := &Tree{Schemas: []*Schema{{Path: "p", PayloadKeys: []Key{{
		Key: "Tree", Type: "<dictionary>",
		Subkeys: []Key{{Key: "Child", Type: "<dictionary>", RecursiveTo: "Tree", SupportedOS: &SupportedOS{MacOS: &OSSupport{Introduced: "27.0"}}}},
	}}}}}
	if problems, err := auditRecursion(narrowed); err != nil || len(problems) != 1 {
		t.Errorf("recursive use narrowing its owner: %q, %v", problems, err)
	}
	orphaned := &Tree{Schemas: []*Schema{{Path: "p", PayloadKeys: []Key{{Key: "Child", Type: "<dictionary>", RecursiveTo: "Nope"}}}}}
	if _, err := auditRecursion(orphaned); err == nil {
		t.Error("recursive reference without an ancestor accepted")
	}
}

// recursionFixture nests Tree in itself through a YAML alias. The recursive
// Child key is introduced later than Tree, so generated validation would
// check Child's values against Tree's entries.
const recursionFixture = `title: Recursion Test
payload:
  requesttype: RecursionTest
  supportedOS:
    macOS:
      introduced: '12.0'
payloadkeys:
- key: Tree
  type: <dictionary>
  presence: optional
  subkeys: &tree
  - key: Name
    type: <string>
    presence: optional
  - key: Child
    type: <dictionary>
    presence: optional
    supportedOS:
      macOS:
        introduced: '27.0'
    subkeys: *tree
`

// TestAuditAvailability checks the audit of schema trees on disk: a clean
// tree, a recursive key narrowing its owner, and trees that cannot be loaded
// or built.
func TestAuditAvailability(t *testing.T) {
	t.Parallel()
	tree := func(files map[string]string) string {
		root := t.TempDir()
		for rel, body := range files {
			path := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return root
	}
	clean := tree(map[string]string{"mdm/commands/avail.yaml": availabilityFixture})
	audit, err := AuditAvailability(clean, "", clean)
	if err != nil || audit.Uses == 0 || len(audit.Problems) != 0 {
		t.Fatalf("clean tree: %+v, %v", audit, err)
	}
	recursive := tree(map[string]string{"mdm/commands/tree.yaml": recursionFixture})
	audit, err = AuditAvailability(recursive)
	want := []string{"mdm/commands/tree.yaml: recursive use Tree.Child differs from its owner Tree"}
	if err != nil || !slices.Equal(audit.Problems, want) {
		t.Fatalf("recursive tree: %+v, %v; want problems %q", audit, err, want)
	}
	misplaced := strings.Replace(availabilityFixture, "- key: Out\n", "- key: Out\n  repetition:\n    min: 1\n    max: 2\n", 1)
	for name, c := range map[string]struct {
		root      string
		histories []string
		want      error
	}{
		"missing schema":       {root: filepath.Join(clean, "missing"), want: os.ErrNotExist},
		"missing history":      {root: clean, histories: []string{filepath.Join(clean, "missing")}, want: os.ErrNotExist},
		"misplaced repetition": {root: tree(map[string]string{"mdm/commands/avail.yaml": misplaced}), want: ErrNaming},
		"unparseable history":  {root: clean, histories: []string{tree(map[string]string{"mdm/commands/avail.yaml": "payload: ["})}},
	} {
		audit, err := AuditAvailability(c.root, c.histories...)
		if err == nil || (c.want != nil && !errors.Is(err, c.want)) {
			t.Errorf("%s: audit = %+v, error %v, want %v", name, audit, err, c.want)
		}
	}
}
