package layout

import (
	"fmt"
	"os/exec"
	"slices"
	"sort"
	"strings"
)

// ErrGoList reports that the go command could not enumerate the module.
var ErrGoList = fmt.Errorf("layout: go list failed")

// Graph is this module's package import graph, keyed by package path
// relative to the module path ("mdm", "ddm/predicate"). Only in-module
// imports are recorded; the standard library and third-party dependencies
// are not part of a tier question.
type Graph struct {
	Module  string
	Imports map[string][]string
}

// Load runs go list in dir and returns the in-module import graph.
func Load(dir string) (*Graph, error) {
	mod, err := run(dir, "list", "-m")
	if err != nil {
		return nil, err
	}
	module := strings.TrimSpace(mod)
	if module == "" {
		return nil, fmt.Errorf("%w: empty module path", ErrGoList)
	}
	out, err := run(dir, "list", "-f", "{{.ImportPath}}|{{join .Imports \",\"}}", "./...")
	if err != nil {
		return nil, err
	}
	prefix := module + "/"
	g := &Graph{Module: module, Imports: map[string][]string{}}
	for _, line := range strings.Split(out, "\n") {
		path, imports, ok := strings.Cut(line, "|")
		if !ok || !strings.HasPrefix(path, prefix) {
			continue
		}
		from := strings.TrimPrefix(path, prefix)
		var edges []string
		for _, imp := range strings.Split(imports, ",") {
			if strings.HasPrefix(imp, prefix) {
				edges = append(edges, strings.TrimPrefix(imp, prefix))
			}
		}
		sort.Strings(edges)
		g.Imports[from] = edges
	}
	if len(g.Imports) == 0 {
		return nil, fmt.Errorf("%w: no packages found in %s", ErrGoList, dir)
	}
	return g, nil
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%w: go %s: %w", ErrGoList, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// Packages returns every in-module package path, sorted.
func (g *Graph) Packages() []string {
	out := make([]string, 0, len(g.Imports))
	for p := range g.Imports {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Reaches returns every package transitively imported by from, sorted. A
// package does not reach itself unless the graph says so.
func (g *Graph) Reaches(from string) []string {
	seen := map[string]bool{}
	var walk func(string)
	walk = func(p string) {
		for _, next := range g.Imports[p] {
			if !seen[next] {
				seen[next] = true
				walk(next)
			}
		}
	}
	walk(from)
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// namespaces hold unrelated packages rather than one cohesive unit, so a
// unit under them is two path elements deep: "internal/clock" is a
// foundation and "internal/app" is the composition root, and collapsing
// them would invent a cycle between what everything imports and what
// imports everything. The tier directories are namespaces for the same
// reason.
var namespaces = []string{
	"internal", "schema",
	"mdmprotocol", "pki", "appleplatformservices", "server",
}

// Unit is the directory a tier is assigned to: the first path element,
// except under a namespace, where it is the first two.
func Unit(pkg string) string {
	parts := strings.Split(pkg, "/")
	if len(parts) > 1 && slices.Contains(namespaces, parts[0]) {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

// UnitGraph collapses the package graph onto units, dropping self edges.
func (g *Graph) UnitGraph() map[string][]string {
	set := map[string]map[string]bool{}
	for from, edges := range g.Imports {
		a := Unit(from)
		if set[a] == nil {
			set[a] = map[string]bool{}
		}
		for _, to := range edges {
			if b := Unit(to); b != a {
				set[a][b] = true
			}
		}
	}
	out := map[string][]string{}
	for a, bs := range set {
		list := make([]string, 0, len(bs))
		for b := range bs {
			list = append(list, b)
		}
		sort.Strings(list)
		out[a] = list
	}
	return out
}

// Cycles returns the strongly connected components of size greater than one
// in the unit graph, each sorted, outermost sorted for a stable message. A
// unit in such a component cannot be assigned to a tier, because part of it
// sits above another part.
func Cycles(g map[string][]string) [][]string {
	var (
		index = map[string]int{}
		low   = map[string]int{}
		on    = map[string]bool{}
		stack []string
		out   [][]string
		next  int
	)
	var strong func(string)
	strong = func(v string) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		on[v] = true
		for _, w := range g[v] {
			switch {
			case func() bool { _, ok := index[w]; return !ok }():
				strong(w)
				low[v] = min(low[v], low[w])
			case on[w]:
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] != index[v] {
			return
		}
		var comp []string
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			on[w] = false
			comp = append(comp, w)
			if w == v {
				break
			}
		}
		if len(comp) > 1 {
			sort.Strings(comp)
			out = append(out, comp)
		}
	}
	keys := make([]string, 0, len(g))
	for v := range g {
		keys = append(keys, v)
	}
	sort.Strings(keys)
	for _, v := range keys {
		if _, ok := index[v]; !ok {
			strong(v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}
