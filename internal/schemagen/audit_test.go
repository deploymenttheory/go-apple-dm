package schemagen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func auditFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, data := range files {
		target := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

const auditSchema = `title: Example
payload:
  requesttype: Example
  supportedOS:
    macOS:
      introduced: '26.0'
payloadkeys:
- key: Value
  type: <string>
  presence: optional
`

func TestAuditGroupsParserErrorsAndKeepsIndependentDiff(t *testing.T) {
	t.Parallel()
	before := auditFixture(t, map[string]string{"mdm/commands/a.yaml": auditSchema})
	after := auditFixture(t, map[string]string{
		"mdm/commands/a.yaml": auditSchema + "future-metadata: []\n",
		"mdm/commands/b.yaml": strings.ReplaceAll(
			auditSchema,
			"Example",
			"Another",
		) + "future-metadata: []\n",
		"declarative/status/reasons.yaml": "title: Reasons\nreasons:\n- value: failure\n  details:\n  - key: When\n    type: <string>\n    valuetype: timestamp\n",
		"openapi/service/definition.json": "{}",
		"docs/new.yaml":                   "ignored: true",
		".hidden/ignored.yaml":            "ignored: true",
	})
	report, err := Audit(before, after, "seed_OS_27_0")
	if err != nil {
		t.Fatal(err)
	}
	if report.ParsePassed || report.BaselineCount != 1 || report.CandidateCount != 3 {
		t.Fatalf("report %+v", report)
	}
	grouped := 0
	for _, f := range report.Findings {
		if strings.Contains(f.Key, "schemagen.Schema:future-metadata") {
			grouped++
			if len(f.Evidence) != 2 {
				t.Fatalf("unknown metadata evidence %+v", f)
			}
		}
		if len(f.Fingerprint) != 64 {
			t.Fatalf("missing fingerprint %+v", f)
		}
	}
	if grouped != 1 {
		t.Fatalf("findings %+v", report.Findings)
	}
	if len(report.Changes) != 3 {
		t.Fatalf("unknown metadata must remain review evidence: %+v", report.Changes)
	}
	again, err := Audit(before, after, "seed_OS_27_0")
	if err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(report)
	b, _ := json.Marshal(again)
	if string(a) != string(b) {
		t.Fatal("audit output is not deterministic")
	}
	if !strings.Contains(report.Markdown(), "Runtime checks are reported separately") {
		t.Fatal(report.Markdown())
	}
}

func TestAuditSeparatesDocumentationRenamesAndAvailability(t *testing.T) {
	t.Parallel()
	base := auditFixture(t, map[string]string{
		"mdm/commands/rename.yaml": auditSchema,
		"mdm/checkin/message.yaml": strings.ReplaceAll(auditSchema, "Example", "Message"),
		"mdm/commands/gone.yaml":   strings.ReplaceAll(auditSchema, "Example", "Gone"),
	})
	updated := auditFixture(t, map[string]string{
		"mdm/commands/renamed.yaml": strings.Replace(
			auditSchema,
			"introduced: '26.0'",
			"introduced: '26.0'\n      removed: '27.0'",
			1,
		),
		"mdm/checkin/message.yaml": strings.ReplaceAll(
			auditSchema,
			"Example",
			"Message",
		) + "description: A protocol requirement.\n",
	})
	report, err := Audit(base, updated, "seed")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	for _, c := range report.Changes {
		kinds[c.Kind] = true
	}
	for _, kind := range []string{"renamed", "documentation", "removed"} {
		if !kinds[kind] {
			t.Fatalf("missing %s: %+v", kind, report.Changes)
		}
	}
	if !report.ParsePassed {
		t.Fatalf("unexpected failure %+v", report.Findings)
	}
	for _, category := range []string{"availability", "protocol-wording", "removed-schemas"} {
		found := false
		for _, f := range report.Findings {
			if strings.Contains(f.Key, category) {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing %s in %+v", category, report.Findings)
		}
	}
}

func TestAuditReportsMalformedUnknownDuplicateAndExampleInputs(t *testing.T) {
	t.Parallel()
	base := auditFixture(t, nil)
	candidate := auditFixture(t, map[string]string{
		"mdm/commands/a.yaml":   auditSchema,
		"mdm/commands/b.yaml":   auditSchema,
		"mdm/commands/bad.yaml": "title: [broken",
		"alien/new.yaml":        "title: Alien\n",
		"mdm/commands/examples.yaml": strings.ReplaceAll(
			auditSchema,
			"Example",
			"Examples",
		) + `examples:
- title: Examples
  files:
  - file: examples/invalid.json
    request-file: missing.plist
    response-file: /outside.json
`,
		"examples/invalid.json": "not-json",
	})
	report, err := Audit(base, candidate, "seed")
	if err != nil {
		t.Fatal(err)
	}
	if report.ParsePassed {
		t.Fatal("invalid inputs passed")
	}
	seen := map[string]bool{}
	for _, f := range report.Findings {
		seen[f.Key] = true
	}
	for _, key := range []string{"schema-format:parse:mdm/commands/bad.yaml", "schema-format:parse:alien/new.yaml", "upstream-input:examples"} {
		if !seen[key] {
			t.Fatalf("missing %s: %+v", key, report.Findings)
		}
	}
	if _, err := Audit("/missing-baseline", candidate, "seed"); err == nil {
		t.Fatal("missing baseline accepted")
	}
	if _, err := Audit(base, "/missing-candidate", "seed"); err == nil {
		t.Fatal("missing candidate accepted")
	}
}

func TestAuditRepeatedWireIDsAndAmbiguousMoves(t *testing.T) {
	t.Parallel()
	before := auditFixture(t, map[string]string{
		"mdm/commands/a.yaml": auditSchema,
		"mdm/commands/b.yaml": auditSchema,
		"mdm/commands/c.yaml": auditSchema,
	})
	after := auditFixture(t, map[string]string{
		"mdm/commands/a.yaml": auditSchema,
		"mdm/commands/d.yaml": auditSchema,
		"mdm/commands/e.yaml": auditSchema,
	})
	report, err := Audit(before, after, "seed")
	if err != nil || !report.ParsePassed || len(report.Changes) != 4 {
		t.Fatalf("repeated IDs: %+v %v", report, err)
	}
	for _, change := range report.Changes {
		if change.Kind == "renamed" {
			t.Fatal("ambiguous rename asserted")
		}
	}
}

func TestAuditIncompleteSourceCannotPass(t *testing.T) {
	t.Parallel()
	valid := auditFixture(t, map[string]string{"mdm/commands/a.yaml": auditSchema})
	empty := auditFixture(t, nil)
	bad := auditFixture(t, map[string]string{"mdm/commands/a.yaml": "title: [invalid"})
	if _, err := Audit(bad, valid, "seed"); err == nil {
		t.Fatal("invalid baseline accepted")
	}
	report, err := Audit(valid, empty, "seed")
	if err != nil || report.ParsePassed {
		t.Fatalf("empty candidate: %+v %v", report, err)
	}
}

func TestAuditYAMLAliasesAndEmptyValues(t *testing.T) {
	t.Parallel()
	var node yaml.Node
	data := `title: Sample
payload:
  payloadtype: example
payloadkeys: &keys
- key: Node
  subkeys: *keys
reasons:
- value: A
  details: []
notes:
- content: Words with    whitespace.
`
	if err := yaml.Unmarshal([]byte(data), &node); err != nil {
		t.Fatal(err)
	}
	fields, prose := map[string]string{}, map[string]string{}
	flattenAudit(&node, "", fields, prose, map[*yaml.Node]bool{})
	if fields["payloadkeys[Node].subkeys"] != "<recursive>" {
		t.Fatal(fields)
	}
	if fields["reasons[A].details"] != "[]" {
		t.Fatal(fields)
	}
	if len(prose) == 0 || auditScalar(nil) != "" || auditChild(nil, "none") != nil {
		t.Fatal("node handling")
	}
	if auditChild(&yaml.Node{Kind: yaml.ScalarNode}, "x") != nil {
		t.Fatal("scalar child")
	}
	flattenAudit(nil, "", fields, prose, map[*yaml.Node]bool{})
	d := compareFields(map[string]string{"a": "1", "b": "2"}, map[string]string{"a": "2", "c": "3"})
	if len(d) != 3 || !strings.Contains(d[1].Detail, "<absent>") {
		t.Fatal(d)
	}
}

func TestAuditMetadataOnlyAndExistingInputAreaDoNotRaiseReview(t *testing.T) {
	t.Parallel()
	before := auditFixture(
		t,
		map[string]string{"mdm/commands/a.yaml": auditSchema, "openapi/a.json": "{}"},
	)
	after := auditFixture(
		t,
		map[string]string{
			"mdm/commands/a.yaml":  auditSchema,
			"openapi/a.json":       "{}",
			"examples/unused.json": "{}",
		},
	)
	report, err := Audit(before, after, "release")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Findings) != 0 || len(report.Changes) != 0 {
		t.Fatalf("unchanged schema %+v", report)
	}
}

func TestAuditAPIChangesDespiteCompilableCandidate(t *testing.T) {
	t.Parallel()
	before := auditFixture(t, map[string]string{"commands/types.gen.go": `package commands
 type Example struct { Value string ` + "`json:\"value\"`" + `; Keep string; private int; Embedded }
 type Embedded struct{}
 type Alias = string
 type Contract interface { Read(string) error }
 const Wire = "old"
 var Registry = map[string]Example{}
 func (e *Example) Method(value string) (result error) {return nil}
 func Helper(x,y int) string{return ""}
 func private() {}
 func (e Example) hidden(){}
 `, "ignore.go": "invalid ignored source"})
	after := auditFixture(t, map[string]string{"commands/types.gen.go": `package commands
 type Example struct { Value int ` + "`json:\"new_value\"`" + `; Added bool }
 type Alias = int
 type Contract interface { Read(int) error }
 const Wire = "new"
 var Registry = map[string]Example{}
 func (e *Example) Method(other string) error {return nil}
 func Helper(one int,two int) string{return ""}
 `})
	report, err := CompareAPI(before, after)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Added) != 1 || len(report.Removed) != 3 || len(report.Changed) != 4 {
		t.Fatalf("API report %+v", report)
	}
	for _, c := range report.Changed {
		if strings.Contains(c.Name, "Method") || strings.Contains(c.Name, "Helper") {
			t.Fatalf("parameter name falsely breaks API: %+v", c)
		}
	}
	if _, err := CompareAPI("/missing-api", after); err == nil {
		t.Fatal("missing baseline accepted")
	}
	if _, err := CompareAPI(before, "/missing-api"); err == nil {
		t.Fatal("missing candidate accepted")
	}
	invalid := auditFixture(t, map[string]string{"broken.gen.go": "not Go"})
	if _, err := CompareAPI(before, invalid); err == nil {
		t.Fatal("invalid source accepted")
	}
}

func TestProvenanceUsesSelectedBranch(t *testing.T) {
	t.Parallel()
	root := auditFixture(t, map[string]string{"mdm/commands/a.yaml": auditSchema})
	files, err := Run(root, Options{Commit: "immutable", Ref: "seed_OS_27_0"})
	if err != nil {
		t.Fatal(err)
	}
	var record GeneratedFrom
	if err := json.Unmarshal(files[generatedFromFile], &record); err != nil {
		t.Fatal(err)
	}
	if record.Ref != "seed_OS_27_0" || record.Commit != "immutable" {
		t.Fatalf("provenance %+v", record)
	}
}

func TestBoundaryCasesCompareSourceAcrossVersionsAndContexts(t *testing.T) {
	t.Parallel()
	baseline := auditFixture(t, map[string]string{"mdm/commands/a.yaml": auditSchema})
	candidate := auditFixture(
		t,
		map[string]string{
			"mdm/commands/a.yaml": strings.Replace(
				auditSchema,
				"introduced: '26.0'",
				"introduced: '26.0'\n      removed: '27.0'\n      supervised: true",
				1,
			),
		},
	)
	cases, err := BoundaryProbes(baseline, candidate)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no changed support probes")
	}
	found := false
	for _, c := range cases {
		if c.Path == "Example" && c.Context == "device" && c.Version == "27.0" {
			found = true
			if c.Supported {
				t.Fatal("removed command allowed")
			}
		}
		if c.Context == "unsupervised" && c.Supported {
			t.Fatal("unsupervised command allowed")
		}
	}
	if !found {
		t.Fatal("no removal-boundary case")
	}
	unchanged, err := BoundaryProbes(baseline, baseline)
	if err != nil || len(unchanged) != 0 {
		t.Fatalf("unchanged: %v %v", unchanged, err)
	}
	if _, err := BoundaryProbes("/missing-boundary", candidate); err == nil {
		t.Fatal("missing baseline accepted")
	}
	if _, err := BoundaryProbes(baseline, "/missing-boundary"); err == nil {
		t.Fatal("missing candidate accepted")
	}
	invalid := auditFixture(
		t,
		map[string]string{
			"mdm/commands/a.yaml": strings.Replace(auditSchema, "<string>", "<unsupported>", 1),
		},
	)
	if _, err := BoundaryProbes(baseline, invalid); err == nil {
		t.Fatal("unsupported type accepted")
	}
	invalid = auditFixture(
		t,
		map[string]string{
			"mdm/commands/a.yaml": strings.Replace(auditSchema, "'26.0'", "'invalid-version'", 1),
		},
	)
	if _, err := BoundaryProbes(baseline, invalid); err == nil {
		t.Fatal("invalid source support accepted")
	}
}
