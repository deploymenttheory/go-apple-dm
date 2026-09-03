package mdmctl_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// maxMainLines bounds cmd/mdmctl/main.go. The point is not the exact number
// but that logic cannot drift into it unnoticed.
const maxMainLines = 40

// cmd/ is exempt from the per-package coverage gate, but -coverpkg=./...
// still counts its statements toward the overall figure, so logic that moves
// into main is logic that leaves the gate. Record 0035 claim 1 is that main
// stays a single function that parses argv and calls mdmctl.Run; this is the
// guard that record cites, and it is enforced here rather than left to a
// reviewer noticing.
//
// micromdm and nanohubctl are the counter-examples: their CLI logic lives in
// cmd/ and behind a global singleton respectively, so neither is testable
// without running the binary.
func TestCmdMainStaysThin(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "mdmctl", "main.go")
	src, err := os.ReadFile(path) // #nosec G304 -- a fixed path inside the repository
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var funcs []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		funcs = append(funcs, fn.Name.Name)
	}
	if len(funcs) != 1 || funcs[0] != "main" {
		t.Fatalf("cmd/mdmctl/main.go declares %v; it must declare only main, so the logic stays in internal/mdmctl where the coverage gate reaches it", funcs)
	}

	if n := fset.File(file.Pos()).LineCount(); n > maxMainLines {
		t.Fatalf("cmd/mdmctl/main.go is %d lines, over the %d-line budget: move the logic into internal/mdmctl", n, maxMainLines)
	}

	// A type or var declared here is state that no test can reach either.
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		if gen.Tok == token.TYPE || gen.Tok == token.VAR {
			t.Errorf("cmd/mdmctl/main.go declares a %s; it belongs in internal/mdmctl", gen.Tok)
		}
	}
}

// The same argument applies to the server binary: cmd/mdmserver is wiring,
// and internal/app is where the gate reaches.
func TestCmdMdmserverStaysThin(t *testing.T) {
	path := filepath.Join("..", "..", "cmd", "mdmserver", "main.go")
	src, err := os.ReadFile(path) // #nosec G304 -- a fixed path inside the repository
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	// mdmserver legitimately owns the listener and the signal handling, so
	// its budget is larger than mdmctl's; what it must not grow is protocol
	// or admin logic.
	const maxServerLines = 200
	if n := fset.File(file.Pos()).LineCount(); n > maxServerLines {
		t.Fatalf("cmd/mdmserver/main.go is %d lines, over the %d-line budget: move the logic into internal/app", n, maxServerLines)
	}
}
