package layout_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// logBoundaries are the library packages that answer a request or run a worker and
// so may log at Error level. Every other library package returns its failures and
// leaves the record to whoever sits at the boundary, which is what keeps one failure
// from being logged at every layer it passes through.
var logBoundaries = []string{
	"devicemanagement/mdmprotocol/enroll/",
	"devicemanagement/pki/acme/",
	"devicemanagement/pki/scep/",
	"devicemanagement/appleplatformservices/dep/",
}

// labelledMessage matches a log message that leads with a package label, which the
// logger's component attribute already carries.
var labelledMessage = regexp.MustCompile("^[\"`][a-z]+: ")

// labelledError matches an error text that leads with a package label. The binary that
// renders an error names itself; a label per layer stacks into a call path.
var labelledError = regexp.MustCompile("^[\"`][a-z][a-z0-9]*: ")

// helperPackages are the test scaffolding directories the discipline does not apply to.
var helperPackages = map[string]bool{
	"simulator": true, "testdata": true, "testpki": true, "acmetest": true, "attesttest": true,
	"axmtest": true, "ddmtest": true, "deptest": true, "gdmftest": true, "pushtest": true,
	"storagetest": true, "storetest": true, "telemetrytest": true, "webauthtest": true,
	"scheduletest": true, "inventorytest": true, "eventtest": true, "secretstest": true,
	"contentcachetest": true,
}

// TestLibraryLoggingDiscipline scans every non-test library source for three things:
// a log call that drops the context (Info instead of InfoContext, which loses the
// request and trace attributes), an Error-level record outside a boundary package,
// and a message that repeats its package as a prefix.
func TestLibraryLoggingDiscipline(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	var findings []string
	err := filepath.WalkDir(filepath.Join(root, "devicemanagement"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if helperPackages[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel := filepath.ToSlash(strings.TrimPrefix(path, root+string(filepath.Separator)))
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pos := fset.Position(call.Pos())
			where := rel + ":" + itoa(pos.Line)
			switch sel.Sel.Name {
			case "Info", "Warn", "Error", "Debug":
				if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					findings = append(findings, where+": "+sel.Sel.Name+" drops the context; use "+sel.Sel.Name+"Context")
				}
			case "InfoContext", "WarnContext", "ErrorContext", "DebugContext":
				if len(call.Args) > 1 {
					if lit, ok := call.Args[1].(*ast.BasicLit); ok && lit.Kind == token.STRING && labelledMessage.MatchString(lit.Value) {
						findings = append(findings, where+": message "+lit.Value+" repeats its package label")
					}
				}
				if sel.Sel.Name == "ErrorContext" && !atBoundary(rel) {
					findings = append(findings, where+": Error-level record outside a boundary package")
				}
			case "Log", "LogAttrs":
				// Log(ctx, level, msg, ...) and LogAttrs(ctx, level, msg, ...) carry the
				// context by construction; the message and the level are checked.
				if len(call.Args) > 2 {
					if lit, ok := call.Args[2].(*ast.BasicLit); ok && lit.Kind == token.STRING && labelledMessage.MatchString(lit.Value) {
						findings = append(findings, where+": message "+lit.Value+" repeats its package label")
					}
					if lvl, ok := call.Args[1].(*ast.SelectorExpr); ok && lvl.Sel.Name == "LevelError" && !atBoundary(rel) {
						findings = append(findings, where+": Error-level record outside a boundary package")
					}
				}
			case "Errorf", "New":
				// fmt.Errorf and errors.New: the text must not lead with a package label.
				if id, ok := sel.X.(*ast.Ident); ok && (id.Name == "fmt" || id.Name == "errors") {
					if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING && labelledError.MatchString(lit.Value) && !operationWord(lit.Value) {
						findings = append(findings, where+": error text "+lit.Value+" leads with a package label")
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		t.Error(f)
	}
}

// operationWords are the leading words an error text may carry with a colon: they name
// what was under way, not the package. A package name is never in this list.
var operationWords = map[string]bool{
	"sign": true, "parse": true, "marshal": true, "unmarshal": true, "encode": true, "decode": true,
	"read": true, "write": true, "open": true, "close": true, "dial": true, "listen": true,
	"nonce": true, "serial": true, "random": true, "depot": true, "transport": true, "visit": true,
	"waiting": true, "identifier": true, "token": true, "subscriptions": true, "verify": true,
	"load": true, "store": true, "fetch": true, "commit": true, "rollback": true, "begin": true,
	"query": true, "exec": true, "scan": true, "hash": true, "seal": true, "unseal": true,
	"import": true, "export": true, "render": true, "compile": true, "resolve": true,
}

// operationWord reports whether a labelled error text leads with an operation rather
// than a package name.
func operationWord(lit string) bool {
	word := strings.TrimLeft(lit, "\"`")
	if i := strings.Index(word, ":"); i > 0 {
		word = word[:i]
	}
	return operationWords[word]
}

// atBoundary reports whether a source file belongs to a package allowed to log at
// Error level.
func atBoundary(rel string) bool {
	for _, b := range logBoundaries {
		if strings.HasPrefix(rel, b) {
			return true
		}
	}
	return false
}

// itoa formats a line number without importing strconv into the test's namespace.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
