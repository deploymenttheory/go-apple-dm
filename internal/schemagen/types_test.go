package schemagen

import (
	"errors"
	"strings"
	"testing"
)

func TestBuildWholeTree(t *testing.T) {
	t.Parallel()
	tree, err := Load(schemaRoot(t))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	pkgs, err := Build(tree)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(pkgs) != len(AllFamilies) {
		t.Fatalf("got %d packages, want %d", len(pkgs), len(AllFamilies))
	}
	byName := map[string]*Package{}
	for _, p := range pkgs {
		byName[p.Name] = p
		names := map[string]bool{}
		for _, td := range p.Types {
			if names[td.Name] {
				t.Errorf("%s: duplicate type %s", p.Name, td.Name)
			}
			names[td.Name] = true
			for _, f := range td.Fields {
				if f.GoType == "" || f.Name == "" || f.Path == "" {
					t.Errorf("%s.%s: incomplete field %+v", p.Name, td.Name, f)
				}
			}
		}
	}
	cmds := byName["commands"]
	if len(cmds.Schemas) != 65 {
		t.Fatalf("commands: %d schemas", len(cmds.Schemas))
	}
	for _, st := range cmds.Schemas {
		if st.Response == nil {
			t.Errorf("command %s has no response type", st.Name)
		}
	}
	// Spot checks against known shapes.
	var lock *SchemaType
	for _, st := range cmds.Schemas {
		if st.Name == "DeviceLock" {
			lock = st
		}
	}
	if lock == nil {
		t.Fatal("DeviceLock not built")
	}
	if got := fieldByKey(lock.Request, "Message"); got == nil || got.GoType != "*string" {
		t.Errorf("DeviceLock.Message = %+v, want *string", got)
	}
	if got := fieldByKey(lock.Response, "MessageResult"); got == nil || !strings.HasPrefix(got.Path, "response:") {
		t.Errorf("DeviceLockResponse.MessageResult = %+v", got)
	}
	status := byName["status"]
	for _, st := range status.Schemas {
		if st.Schema.Payload.StatusItemType == "" {
			if st.Request.Leaf {
				t.Errorf("status %s: leaf without statusitemtype", st.Name)
			}
			continue
		}
		if !st.Request.Leaf || len(st.Request.Fields) != 1 || st.Request.Fields[0].Name != "Value" {
			t.Errorf("status %s: not a leaf with single Value field", st.Name)
		}
	}
	// No recursive placeholder may survive the build.
	for _, p := range pkgs {
		for _, td := range p.Types {
			for _, f := range td.Fields {
				if strings.Contains(f.GoType, "recursive:") || (f.Elem != nil && strings.Contains(f.Elem.GoType, "recursive:")) {
					t.Errorf("%s.%s.%s: unresolved recursion %s", p.Name, td.Name, f.Name, f.GoType)
				}
			}
		}
	}
	// Recursive Safari bookmarks resolve to a self-referential slice.
	ddm := byName["ddm"]
	var bookmarks *SchemaType
	for _, st := range ddm.Schemas {
		if strings.HasSuffix(st.Schema.Path, "safari.bookmarks.yaml") {
			bookmarks = st
		}
	}
	if bookmarks == nil {
		t.Fatal("safari bookmarks not built")
	}
	found := false
	for _, td := range ddm.Types {
		if td.Schema != bookmarks.Schema {
			continue
		}
		for _, f := range td.Fields {
			rec := f.Src.RecursiveTo != "" || (f.Elem != nil && f.Elem.Src.RecursiveTo != "")
			if !rec {
				continue
			}
			found = true
			if !strings.HasPrefix(f.GoType, "[]") || !byName["ddm"].used[f.Base] {
				t.Errorf("recursive field %s.%s has type %s, want slice of a generated struct", td.Name, f.Name, f.GoType)
			}
		}
	}
	if !found {
		t.Error("no recursive field found in safari bookmarks")
	}
}

func fieldByKey(td *TypeDef, key string) *Field {
	if td == nil {
		return nil
	}
	for _, f := range td.Fields {
		if f.Key == key {
			return f
		}
	}
	return nil
}

func buildOne(t *testing.T, path, doc string) (*Package, error) {
	t.Helper()
	s, err := Parse([]byte(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	s.Path = path
	s.Family, s.Kind, err = Classify(path)
	if err != nil {
		t.Fatalf("Classify: %v", err)
	}
	pkgs, err := Build(&Tree{Schemas: []*Schema{s}})
	if err != nil {
		return nil, err
	}
	return pkgs[0], nil
}

func TestBuildShapes(t *testing.T) {
	t.Parallel()
	doc := `title: Shape Test
payload:
  requesttype: ShapeTest
payloadkeys:
- key: S
  type: <string>
  presence: required
- key: OptS
  type: <string>
  presence: optional
- key: I
  type: <integer>
  presence: optional
- key: R
  type: <real>
  presence: required
- key: B
  type: <boolean>
  presence: optional
- key: D
  type: <date>
  presence: optional
- key: Data
  type: <data>
  presence: optional
- key: Any
  type: <any>
  presence: optional
- key: Dict
  type: <dictionary>
  presence: optional
- key: Obj
  type: <dictionary>
  presence: optional
  subkeys:
  - key: X
    type: <string>
    presence: required
- key: Arr
  type: <array>
  presence: optional
- key: Strs
  type: <array>
  presence: required
  subkeys:
  - key: StrsItem
    type: <string>
- key: Objs
  type: <array>
  presence: optional
  subkeys:
  - key: ObjsItem
    type: <dictionary>
    subkeytype: Shared
    subkeys:
    - key: Y
      type: <integer>
      presence: optional
- key: Objs2
  type: <array>
  presence: optional
  subkeys:
  - key: Objs2Item
    type: <dictionary>
    subkeytype: Shared
    subkeys:
    - key: Y
      type: <integer>
      presence: optional
responsekeys:
- key: Out
  type: <string>
  presence: required
`
	p, err := buildOne(t, "mdm/commands/shape.yaml", doc)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	st := p.Schemas[0]
	want := map[string]string{
		"S": "string", "OptS": "*string", "I": "*int64", "R": "float64", "B": "*bool",
		"D": "*time.Time", "Data": "[]byte", "Any": "any", "Dict": "map[string]any",
		"Obj": "*ShapeTestObj", "Arr": "[]any", "Strs": "[]string",
		"Objs": "[]ShapeTestShared", "Objs2": "[]ShapeTestShared",
	}
	for key, typ := range want {
		f := fieldByKey(st.Request, key)
		if f == nil || f.GoType != typ {
			t.Errorf("field %s = %+v, want type %s", key, f, typ)
		}
	}
	if f := fieldByKey(st.Response, "Out"); f == nil || f.Path != "response:Out" {
		t.Errorf("response field path = %+v", f)
	}
	shared := 0
	for _, td := range p.Types {
		if td.Name == "ShapeTestShared" {
			shared++
		}
	}
	if shared != 1 {
		t.Errorf("subkeytype Shared emitted %d times, want 1", shared)
	}
}

func TestBuildErrors(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"field collision": "title: T\npayload:\n  requesttype: T\npayloadkeys:\n- key: Foo-Bar\n  type: <string>\n- key: FooBar\n  type: <string>\n",
		"unknown type":    "title: T\npayload:\n  requesttype: T\npayloadkeys:\n- key: A\n  type: <weird>\n",
	}
	for name, doc := range cases {
		if _, err := buildOne(t, "mdm/commands/t.yaml", doc); !errors.Is(err, ErrNaming) {
			t.Errorf("%s: err = %v, want ErrNaming", name, err)
		}
	}
	// Status items must have exactly one key.
	if _, err := buildOne(t, "declarative/status/x.yaml", "title: Status X\npayload:\n  statusitemtype: x\npayloadkeys:\n- key: a\n  type: <string>\n- key: b\n  type: <string>\n"); !errors.Is(err, ErrNaming) {
		t.Errorf("status multi-key: %v", err)
	}
	// Type collision across two schemas.
	a, _ := Parse([]byte("title: Same\npayload:\n  requesttype: Same\n"))
	a.Path, a.Family = "mdm/commands/a.yaml", FamilyCommands
	b, _ := Parse([]byte("title: Same\npayload:\n  requesttype: Same\n"))
	b.Path, b.Family = "mdm/commands/b.yaml", FamilyCommands
	if _, err := Build(&Tree{Schemas: []*Schema{a, b}}); !errors.Is(err, ErrNaming) {
		t.Errorf("type collision: %v", err)
	}
	// Recursive reference with no matching ancestor.
	bad := &Schema{Title: "R", Path: "mdm/commands/r.yaml", Family: FamilyCommands, Payload: Payload{RequestType: "R"},
		PayloadKeys: []Key{{Key: "A", Type: "<array>", RecursiveTo: "Nope"}}}
	if _, err := Build(&Tree{Schemas: []*Schema{bad}}); !errors.Is(err, ErrNaming) {
		t.Errorf("dangling recursive: %v", err)
	}
	// Same subkeytype name with a different shape gets a distinct name.
	doc := `title: T
payload:
  requesttype: T
payloadkeys:
- key: A
  type: <dictionary>
  subkeytype: S
  subkeys:
  - key: X
    type: <string>
- key: B
  type: <dictionary>
  subkeytype: S
  subkeys:
  - key: Y
    type: <string>
`
	p, err := buildOne(t, "mdm/commands/t.yaml", doc)
	if err != nil {
		t.Fatalf("different shapes: %v", err)
	}
	fa, fb := fieldByKey(p.Schemas[0].Request, "A"), fieldByKey(p.Schemas[0].Request, "B")
	if fa.Base == fb.Base {
		t.Errorf("different shapes share a type name: %s", fa.Base)
	}
}

func TestStatusLeafShapes(t *testing.T) {
	t.Parallel()
	scalar := "title: Status Device Model Family\npayload:\n  statusitemtype: device.model.family\npayloadkeys:\n- key: device.model.family\n  type: <string>\n  presence: required\n"
	p, err := buildOne(t, "declarative/status/device.model.family.yaml", scalar)
	if err != nil {
		t.Fatal(err)
	}
	td := p.Schemas[0].Request
	if td.Name != "DeviceModelFamily" || td.Fields[0].GoType != "string" || td.Fields[0].Key != "device.model.family" {
		t.Errorf("scalar leaf: %+v %+v", td, td.Fields[0])
	}
	dict := "title: Status Management Declarations\npayload:\n  statusitemtype: management.declarations\npayloadkeys:\n- key: management.declarations\n  type: <dictionary>\n  presence: optional\n  subkeys:\n  - key: activations\n    type: <array>\n    subkeys:\n    - key: item\n      type: <dictionary>\n      subkeys:\n      - key: identifier\n        type: <string>\n        presence: required\n"
	p, err = buildOne(t, "declarative/status/management.declarations.yaml", dict)
	if err != nil {
		t.Fatal(err)
	}
	td = p.Schemas[0].Request
	if td.Fields[0].GoType != "ManagementDeclarationsManagementDeclarations" || td.Fields[0].Pointer {
		t.Logf("types: %v", func() []string {
			var n []string
			for _, x := range p.Types {
				n = append(n, x.Name)
			}
			return n
		}())
		t.Errorf("dict leaf: %+v", td.Fields[0])
	}
}
