package layout_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/v3/internal/layout"
)

// TestLoadReportsADirectoryThatIsNotAModule covers the failing path: go list
// cannot enumerate a directory outside a module, and the caller needs to know
// that the graph is absent rather than empty, because an empty graph would
// silently pass every boundary test in this package.
func TestLoadReportsADirectoryThatIsNotAModule(t *testing.T) {
	t.Parallel()
	g, err := layout.Load(t.TempDir())
	if err == nil {
		t.Fatalf("Load outside a module returned %v, want an error", g)
	}
	if !errors.Is(err, layout.ErrGoList) {
		t.Errorf("Load error = %v, want it to wrap ErrGoList", err)
	}
}

// TestLoadReportsAModuleWithNoPackages is the other way the graph can be
// absent: a module that parses but contains nothing to enumerate.
func TestLoadReportsAModuleWithNoPackages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test\n\ngo 1.27\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if _, err := layout.Load(dir); !errors.Is(err, layout.ErrGoList) {
		t.Errorf("Load of an empty module = %v, want it to wrap ErrGoList", err)
	}
}

// TestCyclesFindsAComponent proves the detector actually detects. The real
// module has no cycles, so without a synthetic graph this branch would never
// run and TestNoUnitCycles would be a test that cannot fail.
func TestCyclesFindsAComponent(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		graph map[string][]string
		want  [][]string
	}{
		"two units pointing at each other": {
			graph: map[string][]string{"a": {"b"}, "b": {"a"}},
			want:  [][]string{{"a", "b"}},
		},
		"a longer ring": {
			graph: map[string][]string{"a": {"b"}, "b": {"c"}, "c": {"a"}},
			want:  [][]string{{"a", "b", "c"}},
		},
		"two independent rings": {
			graph: map[string][]string{"a": {"b"}, "b": {"a"}, "y": {"z"}, "z": {"y"}},
			want:  [][]string{{"a", "b"}, {"y", "z"}},
		},
		"a chain is not a cycle": {
			graph: map[string][]string{"a": {"b"}, "b": {"c"}},
			want:  nil,
		},
		"a diamond is not a cycle": {
			graph: map[string][]string{"a": {"b", "c"}, "b": {"d"}, "c": {"d"}},
			want:  nil,
		},
		"empty": {graph: map[string][]string{}, want: nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := layout.Cycles(tc.graph)
			if len(got) != len(tc.want) {
				t.Fatalf("Cycles(%v) = %v, want %v", tc.graph, got, tc.want)
			}
			for i := range got {
				if !slices.Equal(got[i], tc.want[i]) {
					t.Errorf("component %d = %v, want %v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestUnitNamespacesInternalAndSchema pins the one rule that is not "first
// path element": internal and schema hold unrelated packages, so collapsing
// them into one unit would invent a cycle between internal/clock, which
// everything imports, and internal/app, which imports everything.
func TestUnitNamespacesInternalAndSchema(t *testing.T) {
	t.Parallel()
	for pkg, want := range map[string]string{
		"mdm":                    "mdm",
		"ddm/predicate":          "ddm",
		"internal/clock":         "internal/clock",
		"internal/app":           "internal/app",
		"schema/commands":        "schema/commands",
		"schema/support":         "schema/support",
		"acme/attest/attesttest": "acme",
	} {
		if got := layout.Unit(pkg); got != want {
			t.Errorf("Unit(%q) = %q, want %q", pkg, got, want)
		}
	}
}

// TestReachesIsTransitive covers the walk on a graph small enough to read.
func TestReachesIsTransitive(t *testing.T) {
	t.Parallel()
	g := &layout.Graph{Module: "example.test", Imports: map[string][]string{
		"a": {"b"}, "b": {"c"}, "c": nil, "d": {"d"},
	}}
	if got := g.Reaches("a"); !slices.Equal(got, []string{"b", "c"}) {
		t.Errorf("Reaches(a) = %v, want [b c]", got)
	}
	if got := g.Reaches("c"); len(got) != 0 {
		t.Errorf("Reaches(c) = %v, want none", got)
	}
	if got := g.Reaches("d"); !slices.Equal(got, []string{"d"}) {
		t.Errorf("Reaches(d) = %v, want [d]: a self import is still a reach", got)
	}
	if got := g.Packages(); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("Packages() = %v, want [a b c d]", got)
	}
}

// TestUnitGraphDropsSelfEdges keeps a package importing its own sibling from
// looking like a unit depending on itself.
func TestUnitGraphDropsSelfEdges(t *testing.T) {
	t.Parallel()
	g := &layout.Graph{Module: "example.test", Imports: map[string][]string{
		"ddm":           {"ddm/predicate", "mdm"},
		"ddm/predicate": nil,
	}}
	if got := g.UnitGraph()["ddm"]; !slices.Equal(got, []string{"mdm"}) {
		t.Errorf("UnitGraph()[ddm] = %v, want [mdm]", got)
	}
}
