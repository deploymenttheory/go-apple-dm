package dmctl_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// words collapses whitespace so assertions do not depend on column widths.
func words(s string) string { return strings.Join(strings.Fields(s), " ") }

// TestScopeVerbsPrintVerdicts checks that set and Blueprint assignment and
// command sending print availability verdicts and support tiers in human
// output, keep the server's bytes in JSON output, and fall back to the raw
// body when it is not a verdict.
func TestScopeVerbsPrintVerdicts(t *testing.T) {
	assignment := `{"Changed":true,"Verdict":{"Enforced":true,"Tier":"best-effort","Eligible":[{"Identifier":"apps"}],"Issues":[` +
		`{"Identifier":"extensions","Reason":"unsupported-target","Withheld":true,"Availability":[{"Path":"AppManaged.ExtensionConfigs","OS":"macOS","Rule":"introduced","Severity":"error","Introduced":"27.0","Deprecated":"latest","Removed":"latest"}]},` +
		`{"Identifier":"intelligence","Reason":"deprecated","Withheld":false,"Availability":[{"Path":"IntelligenceSettings.AllowVisualIntelligenceSummary","OS":"iOS","Rule":"deprecated","Severity":"warning","Introduced":"26.4","Deprecated":"27.0","Removed":"latest"}]},` +
		`{"Identifier":"future","Reason":"unvalidated","Withheld":false,"Availability":[{"Path":"AppManaged.FutureKey","OS":"macOS","Rule":"unvalidated","Severity":"warning"}]}]}}`
	enqueue := `{"CommandUUID":"CMD-1","Queued":0,"Skipped":"request type not supported","Tier":"guaranteed","Availability":[{"Path":"DeviceInformation.Queries.QueriesItem.OSUpdateSettings","OS":"macOS","Rule":"removed","Severity":"error","Introduced":"10.11","Deprecated":"26.0","Removed":"27.0"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/commands"):
			_, _ = w.Write([]byte(enqueue))
		case strings.HasSuffix(r.URL.Path, "/sets/raw"):
			_, _ = w.Write([]byte(`not json`))
		default:
			_, _ = w.Write([]byte(assignment))
		}
	}))
	t.Cleanup(srv.Close)
	env := noConfig(t)
	env["DMCTL_SERVER"], env["DMCTL_TOKEN"] = srv.URL, "tok"

	for _, args := range [][]string{
		{"sets", "assign", "device", "MAC-1", "apps"},
		{"blueprints", "assign", "engineering", "MAC-1"},
	} {
		out, _, err := run(t, env, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		for _, want := range []string{
			"changed true", "tier best-effort", "eligible apps", "withheld extensions unsupported-target",
			"AppManaged.ExtensionConfigs macOS error introduced introduced 27.0, deprecated latest, removed latest",
			"delivered intelligence deprecated", "iOS warning deprecated introduced 26.4, deprecated 27.0",
			"delivered future unvalidated", "AppManaged.FutureKey macOS warning unvalidated",
		} {
			if !strings.Contains(words(out), want) {
				t.Fatalf("%v output missing %q:\n%s", args, want, out)
			}
		}
	}
	out, _, err := runWithStdin(t, env, "<plist/>", "commands", "send", "-file", "-", "device", "MAC-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"command CMD-1", "queued 0", "skipped request type not supported", "tier guaranteed", "macOS error removed introduced 10.11, deprecated 26.0, removed 27.0"} {
		if !strings.Contains(words(out), want) {
			t.Fatalf("send output missing %q:\n%s", want, out)
		}
	}
	out, _, err = run(t, env, "-output", "json", "sets", "assign", "device", "MAC-1", "apps")
	if err != nil || strings.TrimSpace(out) != assignment {
		t.Fatalf("json output = %q, %v", out, err)
	}
	out, _, err = run(t, env, "sets", "assign", "device", "MAC-1", "raw")
	if err != nil || out != "not json" {
		t.Fatalf("raw output = %q, %v", out, err)
	}
}
