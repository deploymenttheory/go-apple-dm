package schemagen

import (
	"bytes"
	"strings"
	"testing"
)

func TestDocumentationMetadataDoesNotChangeGeneratedAPI(t *testing.T) {
	t.Parallel()
	const doc = "title: Test\npayload:\n  statusitemtype: test.status\npayloadkeys:\n- key: test.status\n  type: <string>\nreasons:\n- value: Error.Failed\n  details:\n  - key: Timestamp\n    type: <string>\n"
	baseline, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := Parse([]byte(doc + "    valuetype: timestamp\nexamples: []\n"))
	if err != nil {
		t.Fatal(err)
	}
	var outputs []Files
	for _, s := range []*Schema{baseline, candidate} {
		s.Path, s.Family = "declarative/status/test.yaml", FamilyStatus
		pkgs, err := Build(&Tree{Schemas: []*Schema{s}})
		if err != nil {
			t.Fatal(err)
		}
		files, err := Generate(pkgs, Options{Commit: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		outputs = append(outputs, files)
	}
	if len(outputs[0]) != len(outputs[1]) {
		t.Fatal("metadata changed generated file set")
	}
	for name, before := range outputs[0] {
		if !bytes.Equal(before, outputs[1][name]) {
			t.Fatalf("metadata changed %s", name)
		}
	}
}

func TestProfileTitleChangePreservesPublicName(t *testing.T) {
	t.Parallel()
	for _, title := range []string{"Content Caching", "Content Caching Service"} {
		s := &Schema{Title: title, Path: "mdm/profiles/com.apple.AssetCache.managed.yaml", Family: FamilyProfiles}
		pkgs, err := Build(&Tree{Schemas: []*Schema{s}})
		if err != nil {
			t.Fatal(err)
		}
		if pkgs[0].Schemas[0].Name != "ContentCaching" {
			t.Fatal("editorial title renamed the API")
		}
	}
}

func TestSeedMetadataStrictParsing(t *testing.T) {
	t.Parallel()
	const metadata = `examples:
- title: Request and response
  files:
  - tab: Exchange
    description: A protocol exchange.
    request-file: examples/request.plist
    response-file: examples/response.plist
  - tab: Status
    description: A status report.
    file: examples/status.json
reasons:
- value: Error.DownloadFailed
  details:
  - key: Timestamp
    type: <string>
    valuetype: timestamp
    description: RFC 3339 timestamp.
`
	s, err := Parse([]byte(auditSchema + metadata))
	if err != nil {
		t.Fatal(err)
	}
	f := s.Examples[0].Files
	if s.Examples[0].Title != "Request and response" || len(f) != 2 || f[0].RequestFile != "examples/request.plist" || f[0].ResponseFile != "examples/response.plist" || f[1].File != "examples/status.json" || f[0].Tab != "Exchange" || f[0].Description == "" {
		t.Fatalf("examples: %+v", s.Examples)
	}
	if d := s.Reasons[0].Details[0]; d.Type != "<string>" || d.ValueType != "timestamp" {
		t.Fatalf("reason annotation changed the type: %+v", d)
	}
	for _, suffix := range []string{"", "examples: []\n", "examples: null\n"} {
		if _, err := Parse([]byte(auditSchema + suffix)); err != nil {
			t.Fatal(err)
		}
	}
	for name, bad := range map[string]string{
		"example key":      strings.Replace(metadata, "- title:", "- unsupported:", 1),
		"file key":         strings.Replace(metadata, "    request-file:", "    unsupported:", 1),
		"reason key":       strings.Replace(metadata, "    valuetype:", "    unsupported:", 1),
		"examples shape":   "examples: text\n",
		"annotation shape": strings.Replace(metadata, "valuetype: timestamp", "valuetype: [timestamp]", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(auditSchema + bad)); err == nil {
				t.Fatal("invalid metadata accepted")
			}
		})
	}
}

func TestExampleReferencesStillAuditedAfterParsing(t *testing.T) {
	t.Parallel()
	before := auditFixture(t, map[string]string{"mdm/commands/a.yaml": auditSchema})
	for _, key := range []string{"file", "request-file", "response-file"} {
		for _, body := range []string{"{}", "invalid-json", "missing"} {
			t.Run(key+"/"+body, func(t *testing.T) {
				files := map[string]string{"mdm/commands/a.yaml": auditSchema + "examples:\n- title: Example\n  files:\n  - " + key + ": examples/data.json\n"}
				if body != "missing" {
					files["examples/data.json"] = body
				}
				r, err := Audit(before, auditFixture(t, files), "seed")
				if err != nil || !r.ParsePassed {
					t.Fatalf("metadata did not parse: %+v %v", r, err)
				}
				found := false
				for _, f := range r.Findings {
					found = found || f.Key == "upstream-input:examples"
				}
				if found != (body != "{}") {
					t.Fatalf("reference findings: %+v", r.Findings)
				}
			})
		}
	}
}
