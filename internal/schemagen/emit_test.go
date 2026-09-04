package schemagen

import (
	"regexp"
	"strings"
	"testing"
)

// syntheticTree builds schemas exercising emitter branches the Apple tree
// does not reach in every combination: scalar arrays and wildcard maps with
// constraints, invalid formats, repetition, variants, errors without codes.
func syntheticTree(t *testing.T) *Tree {
	t.Helper()
	docs := map[string]string{
		"mdm/commands/shapes.yaml": `title: Shapes
payload:
  requesttype: Shapes
  supportedOS:
    iOS:
      introduced: '15.0'
      allowed-enrollments: [device]
      allowed-scopes: [system]
      sharedipad:
        mode: allowed
        devicechannel: true
        userchannel: false
        allowed-scopes: [system]
      userenrollment:
        mode: allowed
        behavior: none
      beta: true
      always-skippable: true
      multiple: false
      allowmanualinstall: true
      userapprovedmdm: false
      requiresdep: false
      accessrights: AllowInspection
payloadkeys:
- key: Strs
  type: <array>
  presence: required
  repetition:
    min: 1
    max: 3
  subkeys:
  - key: StrsItem
    type: <string>
    rangelist: [a, b]
    format: ^[ab]$
- key: Nums
  type: <array>
  subkeys:
  - key: NumsItem
    type: <integer>
    range:
      min: 0
      max: 9
- key: Reals
  type: <array>
  subkeys:
  - key: RealsItem
    type: <real>
    rangelist: [1.5, 2.5]
- key: BadFormat
  type: <string>
  format: (unclosed
- key: BadFormatArr
  type: <array>
  subkeys:
  - key: BadFormatArrItem
    type: <string>
    format: (unclosed
- key: MapStr
  type: <dictionary>
  subkeys:
  - key: ANY
    type: <string>
    rangelist: [x]
    format: ^x$
- key: MapNum
  type: <dictionary>
  subkeys:
  - key: ANY
    type: <integer>
    range:
      min: 1
- key: MapBad
  type: <dictionary>
  subkeys:
  - key: ANY
    type: <string>
    format: (unclosed
- key: MapDict
  type: <dictionary>
  subkeys:
  - key: ANY key
    type: <dictionary>
    subkeys:
    - key: Inner
      type: <string>
- key: MapMap
  type: <dictionary>
  subkeys:
  - key: ANY
    type: <dictionary>
    subkeys:
    - key: ANY
      type: <string>
- key: MapArr
  type: <dictionary>
  subkeys:
  - key: ANY
    type: <array>
    subkeys:
    - key: Item
      type: <string>
- key: ArrOfArr
  type: <array>
  subkeys:
  - key: Row
    type: <array>
    subkeys:
    - key: Cell
      type: <integer>
- key: ArrOfMap
  type: <array>
  subkeys:
  - key: M
    type: <dictionary>
- key: RealRange
  type: <real>
  range:
    min: 0.5
- key: IntEnum
  type: <integer>
  rangelist: [3, 4]
- key: FloatFromInt
  type: <real>
  rangelist: [7]
- key: IntFromFloat
  type: <integer>
  rangelist: [2.0]
- key: DateKey
  type: <date>
- key: Union
  type: <array>
  subkeys:
  - key: A
    type: <dictionary>
    subkeys:
    - key: Item
      type: <string>
      rangelist: [A]
  - key: B
    type: <dictionary>
    subkeys:
    - key: Item
      type: <string>
      rangelist: [B]
  - key: Scalar
    type: <string>
responsekeys:
- key: Out
  type: <string>
`,
		"mdm/errors/nocode.yaml":                             "title: Error No Code\npayloadkeys:\n- key: description\n  type: <string>\n",
		"mdm/errors/withcode.yaml":                           "title: Error With Code\npayloadkeys:\n- key: code\n  type: <string>\n  rangelist: [com.example.code]\n",
		"other/thing.yaml":                                   "title: Thing\npayload:\n  payloadtype: Thing\npayloadkeys:\n- key: A\n  type: <boolean>\n",
		"declarative/status/leaf.dict.yaml":                  "title: Status Leaf Dict\npayload:\n  statusitemtype: leaf.dict\npayloadkeys:\n- key: leaf.dict\n  type: <dictionary>\n  subkeys:\n  - key: x\n    type: <string>\n",
		"declarative/declarations/declarationbase.yaml":      "title: DeclarationBase\npayload:\n  declarationtype: any\npayloadkeys:\n- key: Type\n  type: <string>\n",
		"declarative/declarations/assets/credentials/c.yaml": "title: C Credential\npayload:\n  credentialtype: com.example.c\npayloadkeys:\n- key: X\n  type: <string>\n",
		"declarative/protocol/p.yaml":                        "title: P\npayloadkeys:\n- key: X\n  type: <string>\n",
		"mdm/checkin/withresp.yaml":                          "title: With Resp\npayload:\n  requesttype: WithResp\npayloadkeys:\n- key: MessageType\n  type: <string>\nresponsekeys:\n- key: TokenData\n  type: <data>\n",
	}
	var schemas []*Schema
	for path, doc := range docs {
		s, err := Parse([]byte(doc))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		s.Path = path
		s.Family, s.Kind, err = Classify(path)
		if err != nil {
			t.Fatal(err)
		}
		schemas = append(schemas, s)
	}
	tree := &Tree{Schemas: schemas}
	// Load sorts; do the same for determinism.
	for i := range tree.Schemas {
		for j := i + 1; j < len(tree.Schemas); j++ {
			if tree.Schemas[j].Path < tree.Schemas[i].Path {
				tree.Schemas[i], tree.Schemas[j] = tree.Schemas[j], tree.Schemas[i]
			}
		}
	}
	return tree
}

func TestGenerateSynthetic(t *testing.T) {
	t.Parallel()
	pkgs, err := Build(syntheticTree(t))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	files, err := Generate(pkgs, Options{Commit: "synthetic"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	// Collapse whitespace so gofmt alignment does not matter to the checks.
	ws := regexp.MustCompile(`\s+`)
	get := func(name string) string {
		data, ok := files[name]
		if !ok {
			t.Fatalf("missing %s", name)
		}
		return ws.ReplaceAllString(string(data), " ")
	}
	types := get("commands/types.gen.go")
	for _, want := range []string{
		"Strs []string", "Nums []int64", "MapStr map[string]string", "MapNum map[string]int64",
		"MapDict map[string]ShapesMapDict", "MapMap map[string]map[string]string", "MapArr map[string][]string",
		"ArrOfArr [][]int64", "ArrOfMap []map[string]any", "Union []any", "type ShapesUnionA struct", "type ShapesUnionB struct",
		"DateKey *time.Time",
	} {
		if !strings.Contains(types, want) {
			t.Errorf("types.gen.go missing %q", want)
		}
	}
	validate := get("commands/validate.gen.go")
	for _, want := range []string{
		"not RE2-compatible", "c.Repetition(path, present, len(x.Strs), 1, 3)", "c.Enum(ip, true, v, []any{\"a\", \"b\"})",
		"c.Range(ip, true, float64(v), new(0.0), new(9.0))", "c.Pattern(ip, true, v, format0)",
		"for k, v := range x.MapStr", "for k, v := range x.MapNum", "case *ShapesUnionA:", "case ShapesUnionB:",
		"c.Range(path, true, float64(*x.RealRange), new(0.5), nil)", "c.Enum(path, true, *x.IntEnum, []any{int64(3), int64(4)})",
	} {
		if !strings.Contains(validate, want) {
			t.Errorf("validate.gen.go missing %q", want)
		}
	}
	support := get("commands/support.gen.go")
	for _, want := range []string{
		`AllowedEnrollments: []string{"device"}`, "SharedIPadMode: \"allowed\"", "UserEnrollmentBehavior: \"none\"",
		"Beta: true", "AlwaysSkippable: support.Bool(true)", `AccessRights: "AllowInspection"`, "SharedIPadScopes",
	} {
		if !strings.Contains(support, want) {
			t.Errorf("support.gen.go missing %q", want)
		}
	}
	conf := get("commands/conformance_gen_test.go")
	for _, want := range []string{
		"conformance.Deref(sampleShapesUnionA(d + 1))", `map[string]string{"k": "x"}`, "[][]int64{[]int64{int64(1)}}",
		"1.5", "int64(3)", "7.0", "int64(2)", "sampleTime",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("conformance_gen_test.go missing %q", want)
		}
	}
	reg := get("errors/registry.gen.go")
	if !strings.Contains(reg, `ErrorCodeWithCode = "com.example.code"`) || strings.Contains(reg, "NoCode:") {
		t.Errorf("errors registry: %s", reg)
	}
	if !strings.Contains(get("checkin/registry.gen.go"), "NewResponse: func() any { return new(WithRespResponse) }") {
		t.Error("checkin response constructor missing")
	}
	if !strings.Contains(get("ddm/registry.gen.go"), "KindCredential") || strings.Contains(get("ddm/registry.gen.go"), `"DeclarationBase": {`) {
		t.Error("ddm registry kinds")
	}
	if !strings.Contains(get("status/types.gen.go"), "Value LeafDictLeafDict") {
		t.Error("status dict leaf")
	}
	if !strings.Contains(get("other/registry.gen.go"), `TypeThing = "Thing"`) {
		t.Error("other constants")
	}
	// doc.gen.go is the package's only package comment and follows the
	// doc.go layout every hand-written package uses (see ddm/doc.go).
	doc := string(files["commands/doc.gen.go"])
	if !strings.HasPrefix(doc, "// Code generated by admgen; DO NOT EDIT.\n// Source: apple/device-management@synthetic\n\n// Package commands holds the MDM commands and their responses generated from\n") {
		t.Errorf("doc.gen.go header:\n%s", doc)
	}
	for _, want := range []string{
		"// # Why\n", "// # References\n", "decision record 0003", "schema/NAMES.lock",
		"//   - Decision record 0001: docs/research/decisions/0001-architecture.md\n",
		"//   - Decision record 0003: docs/research/decisions/0003-schema-generator.md\n",
		"//   - Plan of record: docs/research/implementation_plan.md (phase 1)\n",
		"//   - Apple: https://developer.apple.com/documentation/devicemanagement/commands-and-queries\n",
		"//   - Apple: https://github.com/apple/device-management/blob/release/docs/schema.md\n",
		"//   - Schema: third_party/device-management/mdm/commands/**\n",
		"//   - Upstream: https://github.com/apple/device-management at commit synthetic (schema/GENERATED_FROM.json)\n",
		"\npackage commands\n",
	} {
		if !strings.Contains(doc, want) {
			t.Errorf("doc.gen.go missing %q", want)
		}
	}
	if strings.Count(doc, "// Package ") != 1 || strings.Contains(string(files["commands/types.gen.go"]), "\n// Package commands") {
		t.Error("package comment must appear exactly once, in doc.gen.go")
	}
}

func TestUniqueNameSuffixes(t *testing.T) {
	t.Parallel()
	b := &builder{pkg: &Package{used: map[string]bool{"A": true, "AB": true, "AB2": true}}}
	if got := b.uniqueName("A", "AB"); got != "AB3" {
		t.Errorf("uniqueName = %s, want AB3", got)
	}
	if got := b.uniqueName("", "Z"); got != "Z" {
		t.Errorf("uniqueName = %s, want Z", got)
	}
}

func TestHeaderExtra(t *testing.T) {
	t.Parallel()
	e := &emitter{pkg: &Package{Name: "x"}, opts: Options{Commit: "c"}}
	if h := e.header("note"); !strings.Contains(h, "// note\n") || !strings.Contains(h, "package x") {
		t.Errorf("header = %q", h)
	}
}

// A reason code is scoped to the schema that declares it: Apple's
// statusreason.yaml says "each status item defines its own set of code,
// description, and details values", and app.managed.list and package.list do
// declare Error.DownloadFailed with different prose. The emitted vocabulary
// must keep both meanings rather than let one schema's win, and the constant
// must say so instead of asserting a meaning it cannot have.
func TestGenerateReasonsScopedToSchema(t *testing.T) {
	t.Parallel()
	docs := map[string]string{
		"declarative/status/one.yaml": `title: Status One
payload:
  statusitemtype: one
payloadkeys:
- key: one
  type: <string>
reasons:
- value: Error.Shared
  description: One failed.
  details:
  - key: Timestamp
    type: <string>
    description: When it failed.
- value: Error.OnlyHere
  description: Only one declares this.
`,
		"declarative/status/two.yaml": `title: Status Two
payload:
  statusitemtype: two
payloadkeys:
- key: two
  type: <string>
reasons:
- value: Error.Shared
  description: Two failed.
`,
	}
	var schemas []*Schema
	for _, path := range []string{"declarative/status/one.yaml", "declarative/status/two.yaml"} {
		s, err := Parse([]byte(docs[path]))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		s.Path = path
		if s.Family, s.Kind, err = Classify(path); err != nil {
			t.Fatal(err)
		}
		schemas = append(schemas, s)
	}
	pkgs, err := Build(&Tree{Schemas: schemas})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	files, err := Generate(pkgs, Options{Commit: "synthetic"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	src, ok := files["status/reasons.gen.go"]
	if !ok {
		t.Fatal("missing status/reasons.gen.go")
	}
	// Strip comment markers before collapsing whitespace, so a doc line that
	// wraps still reads as one sentence.
	got := regexp.MustCompile(`\s+`).ReplaceAllString(
		strings.ReplaceAll(string(src), "//", ""), " ")
	for _, want := range []string{
		// Both declarations survive, each carrying its own schema.
		`{Code: ReasonErrorShared, Description: "One failed.", Schema: "declarative/status/one.yaml"`,
		`{Code: ReasonErrorShared, Description: "Two failed.", Schema: "declarative/status/two.yaml"}`,
		// A code only one schema declares takes that schema's prose.
		`ReasonErrorOnlyHere is Apple's "Error.OnlyHere": Only one declares this.`,
		// A code two schemas declare cannot claim one meaning.
		`ReasonErrorShared is Apple's "Error.Shared". Its meaning depends on the status item reporting it`,
		`Details: []ReasonDetail{ {Key: "Timestamp", Type: "<string>", Description: "When it failed."}, }`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("reasons.gen.go missing %q", want)
		}
	}
	// The constants and the map are one set, which is what makes the map a
	// bound on the vocabulary rather than a sample of it.
	conformance := string(files["status/conformance_gen_test.go"])
	if !strings.Contains(conformance, "if len(status.Reasons) != 2 {") {
		t.Error("the conformance test does not pin the vocabulary size")
	}
}
