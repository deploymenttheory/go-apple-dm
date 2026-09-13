package layout_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/internal/layout"
)

func TestRepositoryGraphAcceptsSingleModuleAndRejectsBrokenModules(t *testing.T) {
	t.Parallel()
	for _, mode := range []string{"library only", "broken library", "broken server"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			write := func(name, value string) {
				t.Helper()
				file := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte(value), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			write("go.mod", "module example.test/library\n\ngo 1.24\n")
			write("unit/unit.go", "package unit\n")
			switch mode {
			case "broken library":
				write("unit/unit.go", "invalid Go source\n")
			case "broken server":
				write("server/go.mod", "invalid module declaration\n")
			}
			graph, err := layout.LoadRepo(dir)
			if mode != "library only" {
				if !errors.Is(err, layout.ErrGoList) {
					t.Fatalf("broken repository accepted: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(graph.Packages(), []string{"unit"}) {
				t.Fatal(graph.Packages())
			}
		})
	}
}
