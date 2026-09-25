package layout_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode"

	"github.com/deploymenttheory/go-apple-dm/devicemanagement/fault"
)

const (
	catalogueDoc   = "docs/operations/error-responses.md"
	catalogueStart = "<!-- catalogue:start -->"
	catalogueEnd   = "<!-- catalogue:end -->"
)

// catalogueTable renders the documented tables: client conditions with their published
// type, device conditions with Apple's code, and the device conditions met as a bare
// status, which are declared wherever they are raised and gathered by scanning.
func catalogueTable(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("### Conditions published to API callers\n\n")
	b.WriteString("| Code | Kind | Status | Title |\n|---|---|---|---|\n")
	for _, e := range fault.Catalogue() {
		if e.Audience() != fault.Client {
			continue
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %d | %s |\n", e.Code(), e.Kind().String(), e.Kind().HTTPStatus(), e.Error())
	}
	b.WriteString("\n### Conditions a device meets with an Apple error document\n\n")
	b.WriteString("| Apple `code` | Kind | Status | Condition |\n|---|---|---|---|\n")
	for _, e := range fault.Catalogue() {
		if e.Audience() != fault.Device {
			continue
		}
		fmt.Fprintf(&b, "| `%s` | `%s` | %d | %s |\n", e.Code(), e.Kind().String(), e.Kind().HTTPStatus(), e.Error())
	}
	b.WriteString("\n### Conditions a device meets as a bare status\n\n")
	b.WriteString("| Kind | Status | Condition |\n|---|---|---|\n")
	for _, d := range bareDeviceConditions(t) {
		fmt.Fprintf(&b, "| `%s` | %d | %s |\n", d.kind.String(), d.kind.HTTPStatus(), d.message)
	}
	return b.String()
}

// bareDevice is a codeless device condition as its source declares it.
type bareDevice struct {
	kind    *fault.Kind
	message string
}

// kindsByName resolves the identifier a declaration names to its kind.
var kindsByName = func() map[string]*fault.Kind {
	out := map[string]*fault.Kind{}
	for _, k := range fault.Kinds() {
		out[exportedName(k.String())] = k
	}
	return out
}()

// exportedName turns a kind's snake_case name into its Go identifier.
func exportedName(snake string) string {
	var b strings.Builder
	for _, w := range strings.Split(snake, "_") {
		if w == "" {
			continue
		}
		if w == "http" {
			b.WriteString("HTTP")
			continue
		}
		b.WriteString(strings.ToUpper(w[:1]) + w[1:])
	}
	return b.String()
}

// bareDeviceConditions scans every non-test library source for a NewDevice declaration
// with an empty code and returns them sorted by message.
func bareDeviceConditions(t *testing.T) []bareDevice {
	t.Helper()
	root := repoRoot(t)
	var out []bareDevice
	err := filepath.WalkDir(filepath.Join(root, "devicemanagement"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 3 {
				return true
			}
			var fn string
			switch e := call.Fun.(type) {
			case *ast.SelectorExpr:
				fn = e.Sel.Name
			case *ast.Ident:
				fn = e.Name
			}
			if fn != "NewDevice" {
				return true
			}
			code, ok := call.Args[0].(*ast.BasicLit)
			if !ok || strings.Trim(code.Value, `"`) != "" {
				return true
			}
			var kindName string
			switch k := call.Args[1].(type) {
			case *ast.SelectorExpr:
				kindName = k.Sel.Name
			case *ast.Ident:
				kindName = k.Name
			}
			kind, ok := kindsByName[kindName]
			if !ok {
				t.Errorf("%s: device condition names an unknown kind %q", path, kindName)
				return true
			}
			if lit, ok := call.Args[2].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				out = append(out, bareDevice{kind: kind, message: strings.Trim(lit.Value, `"`)})
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].message < out[j].message })
	return out
}

// TestErrorResponsesTableMatchesCatalogue keeps the documented table equal to the
// catalogue, so a code is never promised in prose that the server does not raise, and
// never raised without being documented. UPDATE_DOCS=1 rewrites the table in place.
func TestErrorResponsesTableMatchesCatalogue(t *testing.T) {
	t.Parallel()
	path := filepath.Join(repoRoot(t), catalogueDoc)
	raw, err := os.ReadFile(path) // #nosec G304 -- a documentation file under the repository root, named by this test
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	start := strings.Index(doc, catalogueStart)
	end := strings.Index(doc, catalogueEnd)
	if start < 0 || end < 0 || end < start {
		t.Fatalf("%s lacks the %s / %s markers", catalogueDoc, catalogueStart, catalogueEnd)
	}
	want := catalogueStart + "\n" + catalogueTable(t) + catalogueEnd
	got := doc[start : end+len(catalogueEnd)]
	if got == want {
		return
	}
	if os.Getenv("UPDATE_DOCS") != "" {
		if err := os.WriteFile(path, []byte(doc[:start]+want+doc[end+len(catalogueEnd):]), 0o600); err != nil { // #nosec G703 -- the same repository-root path the test just read
			t.Fatal(err)
		}
		return
	}
	t.Fatalf("%s is out of date with the catalogue; run UPDATE_DOCS=1 go test ./internal/layout -run TestErrorResponsesTableMatchesCatalogue", catalogueDoc)
}

// declaration is one catalogue variable as its source declares it.
type declaration struct {
	name, file string
	call       string // NewClient, NewDevice
	code       string
}

// catalogueDeclarations parses the fault package's own source for every coded
// declaration, since an Entry does not know its Go name.
func catalogueDeclarations(t *testing.T) []declaration {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "devicemanagement", "fault")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []declaration
	fset := token.NewFileSet()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
				return true
			}
			call, ok := vs.Values[0].(*ast.CallExpr)
			if !ok || len(call.Args) < 1 {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || (fn.Name != "NewClient" && fn.Name != "NewDevice") {
				return true
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				out = append(out, declaration{name: vs.Names[0].Name, file: entry.Name(), call: fn.Name, code: strings.Trim(lit.Value, `"`)})
			}
			return true
		})
	}
	return out
}

// TestEveryCatalogueEntryIsRaised checks that each client condition the library
// catalogues is referenced by non-test library code outside the catalogue itself, so a
// published code always names something the library can raise. Device conditions are
// exempt: device.go declares the transport's vocabulary centrally so the set is
// enumerable, and the server that owns the route raises them.
func TestEveryCatalogueEntryIsRaised(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	referenced := map[string]bool{}
	err := filepath.WalkDir(filepath.Join(root, "devicemanagement"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || path == filepath.Join(root, "devicemanagement", "fault") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "fault" {
					referenced[sel.Sel.Name] = true
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var missing []string
	for _, d := range catalogueDeclarations(t) {
		if d.call == "NewDevice" {
			continue
		}
		if !referenced[d.name] {
			missing = append(missing, d.name+" ("+d.code+")")
		}
	}
	sort.Strings(missing)
	for _, m := range missing {
		t.Errorf("catalogued but never raised by the library: fault.%s", m)
	}
}

// domains maps the leading word of a catalogue identifier onto the domain segment of
// its code. A domain is Apple's current name for the thing, abbreviated where the
// repository already does: ADE for Automated Device Enrollment, AXM for the Apple School
// and Business Manager API.
var domains = map[string]string{
	"DDM": "DDM", "ADE": "ADE", "AXM": "AXM", "AppsBooks": "APPSBOOKS", "MDM": "MDM",
	"ActivationLock": "ACTIVATIONLOCK", "Manifest": "MANIFEST", "Profile": "PROFILE",
	"APNS": "APNS", "Enrollment": "ENROLLMENT", "PKI": "PKI", "ACME": "ACME",
	"PushCert": "PUSHCERT", "AppArtifact": "APPARTIFACT", "AppIdentity": "APPIDENTITY",
	"AppStore": "APPSTORE", "Plist": "PLIST", "Predicate": "PREDICATE", "Schema": "SCHEMA",
	"Record": "RECORD", "OSVersion": "OSVERSION", "Inventory": "INVENTORY",
}

// conditionSuffixes are the condition words a code may end with, one meaning each:
// INVALID is understood and rejected, MALFORMED could not be parsed, TOO-LARGE is a size
// bound, LIMIT-EXCEEDED a rate or quota bound. Anything else is a noun the domain owns.
var conditionSuffixes = regexp.MustCompile(`-(INVALID|MALFORMED|NOT-FOUND|CONFLICT|TOO-LARGE|LIMIT-EXCEEDED|EXPIRED|REVOKED|USED|UNKNOWN|UNSUPPORTED|DISABLED|REQUIRED|MISMATCH|STOPPED|VIOLATION|MISSING|UNKNOWN-TYPE|UNKNOWN-FORMAT|TOO-DEEP|OUT-OF-RANGE)$`)

// splitCamel splits a Go identifier on its word boundaries, keeping initialisms whole.
func splitCamel(name string) []string {
	var words []string
	runes := []rune(name)
	start := 0
	for i := 1; i < len(runes); i++ {
		prevUpper := unicode.IsUpper(runes[i-1])
		curUpper := unicode.IsUpper(runes[i])
		nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
		if (curUpper && !prevUpper) || (curUpper && prevUpper && nextLower) {
			words = append(words, string(runes[start:i]))
			start = i
		}
	}
	return append(words, string(runes[start:]))
}

// TestCatalogueNamingRule checks that a client condition's Go identifier and its
// published code say the same thing: the identifier is <Domain><Noun><Condition>, the
// code is DM-<DOMAIN>-<NOUN>-<CONDITION> with the same words, and the condition word is
// one from the small set with one meaning each.
func TestCatalogueNamingRule(t *testing.T) {
	t.Parallel()
	keys := make([]string, 0, len(domains))
	for k := range domains {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, d := range catalogueDeclarations(t) {
		if d.call != "NewClient" {
			continue
		}
		var domain, rest string
		for _, k := range keys {
			if strings.HasPrefix(d.name, k) {
				domain, rest = domains[k], strings.TrimPrefix(d.name, k)
				break
			}
		}
		if domain == "" {
			t.Errorf("%s: no known domain leads the identifier; add one to the domain table with Apple's current name", d.name)
			continue
		}
		want := "DM-" + domain
		for _, w := range splitCamel(rest) {
			want += "-" + strings.ToUpper(w)
		}
		if want != d.code {
			t.Errorf("%s: code %s does not spell the identifier; want %s", d.name, d.code, want)
		}
		if !conditionSuffixes.MatchString(d.code) {
			t.Errorf("%s: code %s does not end in a condition word with one meaning", d.name, d.code)
		}
	}
}
