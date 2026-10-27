package schemagen

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/osversion"
	"github.com/deploymenttheory/go-apple-dm/devicemanagement/schema/support"
)

// auditAvailability walks every key use of the built type model and reports
// each use whose generated validation consults a support entry that is missing
// or differs from the use's own source availability. Support paths are composed
// as the validation emitter composes them, so a struct shared by several uses
// is checked once per use against the entries that use's validation reads.
func auditAvailability(
	pkgs []*Package,
	tables func(*SchemaType) (map[string]*support.Entry, error),
) (problems []string, uses int, err error) {
	for _, pkg := range pkgs {
		for _, st := range pkg.Schemas {
			table, err := tables(st)
			if err != nil {
				return nil, 0, err
			}
			source, err := keyAvailability(st.Schema)
			if err != nil {
				return nil, 0, err
			}
			check := func(use, consulted string) {
				uses++
				want, got := source[use], table[consulted]
				switch {
				case want == nil:
					problems = append(problems, fmt.Sprintf("%s/%s: no source key for use %s", pkg.Name, st.Name, use))
				case got == nil:
					problems = append(problems, fmt.Sprintf("%s/%s: use %s consults missing entry %s", pkg.Name, st.Name, use, consulted))
				case !reflect.DeepEqual(want, got.OS):
					problems = append(problems, fmt.Sprintf("%s/%s: use %s consults %s with different availability", pkg.Name, st.Name, use, consulted))
				}
			}
			var walk func(td *TypeDef, prefix, supportPath string)
			walk = func(td *TypeDef, prefix, supportPath string) {
				if td == nil || td.Leaf {
					return
				}
				for _, f := range td.Fields {
					use := joinKeyPath(prefix, f.Key)
					check(use, supportPath+fieldSuffix(f))
					walk(f.Struct, use, supportPath+fieldSuffix(f))
					if f.Elem != nil {
						item := use + "." + f.Elem.Key
						walk(f.Elem.Struct, item, supportPath+elemSuffix(f))
						if f.Elem.Kind == KindScalar && f.Elem.Base == "string" {
							for _, value := range f.Elem.Src.Subkeys {
								check(item+"."+value.Key, supportPath+elemSuffix(f)+"."+value.Key)
							}
						}
					}
					for i, key := range variantKeys(f) {
						walk(f.Variants[i], use+"."+key, supportPath+fieldSuffix(f)+"."+key)
					}
				}
			}
			walk(st.Request, "", rootSupportPath(st.Request))
			if st.Response != nil {
				walk(st.Response, responsePrefix, rootSupportPath(st.Response))
			}
		}
	}
	sort.Strings(problems)
	return problems, uses, nil
}

// TestAvailabilityAuditCurrentTree requires every key use in the current
// schema, merged with each retained release as generation merges them, to be
// validated against its own source availability.
func TestAvailabilityAuditCurrentTree(t *testing.T) {
	t.Parallel()
	retained, err := RetainedReleases("../../.gitmodules")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := Load(schemaRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range retained {
		history, err := Load(root)
		if err != nil {
			t.Fatal(err)
		}
		if tree, err = MergeHistory(tree, history); err != nil {
			t.Fatal(err)
		}
	}
	pkgs, err := Build(tree)
	if err != nil {
		t.Fatal(err)
	}
	problems, uses, err := auditAvailability(pkgs, effective)
	if err != nil {
		t.Fatal(err)
	}
	recursive, err := auditRecursion(tree)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range append(problems, recursive...) {
		t.Error(p)
	}
	t.Logf("audited %d key uses, %d mismatched", uses, len(problems))
}

// auditRecursion reports recursive references whose values would be
// validated with different availability from the use they nest under.
// Generated validation checks a recursive value with its owner's struct, and
// therefore against the owner's entries, so the recursive key must make
// those values available exactly as the owner does.
func auditRecursion(tree *Tree) ([]string, error) {
	var problems []string
	for _, s := range tree.Schemas {
		source, err := keyAvailability(s)
		if err != nil {
			return nil, err
		}
		type ancestor struct {
			key  *Key
			path string
		}
		var walk func(keys []Key, prefix string, stack []ancestor) error
		walk = func(keys []Key, prefix string, stack []ancestor) error {
			for i := range keys {
				k := &keys[i]
				path := joinKeyPath(prefix, k.Key)
				if k.RecursiveTo != "" {
					var owner *ancestor
					for j := len(stack) - 1; j >= 0 && owner == nil; j-- {
						if stack[j].key.Key == k.RecursiveTo {
							owner = &stack[j]
						}
					}
					if owner == nil {
						return fmt.Errorf("%s: %s: recursive reference %q has no ancestor", s.Path, path, k.RecursiveTo)
					}
					// An array owner's values are its item dictionaries, whose
					// own block applies at both levels.
					want, got := source[owner.path], source[path]
					if owner.key.Type == "<array>" && len(owner.key.Subkeys) == 1 {
						item := &owner.key.Subkeys[0]
						want = source[owner.path+"."+item.Key]
						if got, err = convertOS(got, item.SupportedOS); err != nil {
							return err
						}
					}
					if !reflect.DeepEqual(want, got) {
						problems = append(problems, fmt.Sprintf("%s: recursive use %s differs from its owner %s", s.Path, path, owner.path))
					}
				}
				if err := walk(k.Subkeys, path, append(append([]ancestor(nil), stack...), ancestor{k, path})); err != nil {
					return err
				}
			}
			return nil
		}
		if err := walk(s.PayloadKeys, "", nil); err != nil {
			return nil, err
		}
		if err := walk(s.ResponseKeys, responsePrefix, nil); err != nil {
			return nil, err
		}
	}
	sort.Strings(problems)
	return problems, nil
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
