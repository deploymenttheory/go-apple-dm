package layout_test

import (
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/internal/layout"
)

// repoRoot is two directories above this test file.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func load(t *testing.T) *layout.Graph {
	t.Helper()
	g, err := layout.Load(repoRoot(t))
	if err != nil {
		t.Fatalf("load import graph: %v", err)
	}
	return g
}

// serverTier is the set of units that persist, serve, or assemble. Nothing in
// the protocol, PKI, or Apple-client tiers may reach them, because a caller
// who wants a declaration type should not acquire a database driver with it.
var serverTier = []string{
	"storage", "service", "httpapi", "eventsink", "internal/app", "internal/dmctl",
}

// pureTier is every package proven not to reach serverTier. The list is a
// ratchet: it may only grow. A package that appears here and later reaches the
// server tier fails TestPureTierCannotReachServerTier.
var pureTier = []string{
	"acme", "acme/attest", "acme/jose", "adminauth", "audit", "ca", "cms",
	"ddm/predicate", "dep", "enroll", "enroll/ade", "enroll/discovery",
	"enroll/webauth", "event", "gdmf", "mdm", "paging", "plist", "profile",
	"pushcert", "scep", "secrets", "simulator", "telemetry",
}

// knownServerTierReach records the packages that still reach the server tier
// and must not. Each is a hybrid that bundles a protocol or client half with a
// storage-backed half; decision record 0044 splits them, and every split
// deletes an entry here. The test asserts the set exactly, so a fix that does
// not shrink this list fails as loudly as a regression that grows it.
var knownServerTierReach = map[string][]string{
	"axm":                  {"storage/crypt"},
	"ddm":                  {"service", "storage"},
	"enroll/accountdriven": {"service", "storage"},
	"push":                 {"storage"},
	// apns implements push.Pusher and imports push for the interface, so it
	// inherits push's storage dependency. The split moves apns to the Apple
	// client tier and leaves the notifier server-side.
	"push/apns": {"storage"},
}

// inServerTier reports whether pkg persists, serves, or assembles. Besides
// the named units, a per-domain sqlstore or inmem package is a persistence
// backend wherever it sits, and the DDM adapters are server-side transport.
// Decision record 0044 moves all of them under server/.
func inServerTier(pkg string) bool {
	unit := layout.Unit(pkg)
	if slices.Contains(serverTier, unit) || slices.Contains(serverTier, pkg) {
		return true
	}
	switch path.Base(pkg) {
	case "sqlstore", "inmem":
		return true
	}
	return strings.HasPrefix(pkg, "ddm/adapter/")
}

// isScaffolding reports whether pkg exists to serve tests rather than to run
// in a program. Fakes and contract suites follow the package they exercise
// into whatever tier it occupies, so tier rules say nothing about them; these
// are the same packages scripts/coverage-exempt.txt exempts from the coverage
// gate, for the same reason.
func isScaffolding(pkg string) bool {
	base := path.Base(pkg)
	return strings.HasSuffix(base, "test") ||
		pkg == "internal/testpki" ||
		pkg == "simulator" ||
		strings.HasPrefix(pkg, "cmd/") ||
		strings.HasPrefix(pkg, "e2e") ||
		pkg == "schema/internal/conformance"
}

// TestPureTierCannotReachServerTier is the boundary decision record 0044
// claims. golangci-lint cannot enforce it: the workflow passes
// --issues-exit-code=0, so a depguard rule would report and not fail.
func TestPureTierCannotReachServerTier(t *testing.T) {
	t.Parallel()
	g := load(t)
	for _, pkg := range pureTier {
		if _, ok := g.Imports[pkg]; !ok {
			t.Errorf("package %q is listed in pureTier but no longer exists; update the list", pkg)
			continue
		}
		var reached []string
		for _, dep := range g.Reaches(pkg) {
			if inServerTier(dep) {
				reached = append(reached, dep)
			}
		}
		if len(reached) > 0 {
			t.Errorf("%s must not reach the server tier, but reaches %s", pkg, strings.Join(reached, ", "))
		}
	}
}

// TestKnownServerTierReachIsExact holds the remaining violations still. The
// map is a debt register, not a permission: adding a package to it needs a
// reason in decision record 0044, and removing one is the point of the split
// PRs.
func TestKnownServerTierReachIsExact(t *testing.T) {
	t.Parallel()
	g := load(t)
	for _, pkg := range g.Packages() {
		if slices.Contains(pureTier, pkg) {
			continue
		}
		if inServerTier(pkg) || isScaffolding(pkg) {
			continue
		}
		var reached []string
		for _, dep := range g.Reaches(pkg) {
			if inServerTier(dep) {
				reached = append(reached, dep)
			}
		}
		want, listed := knownServerTierReach[pkg]
		switch {
		case len(reached) == 0 && listed:
			t.Errorf("%s no longer reaches the server tier: delete it from knownServerTierReach", pkg)
		case len(reached) > 0 && !listed:
			t.Errorf("%s newly reaches the server tier (%s); split it or record why in decision 0044",
				pkg, strings.Join(reached, ", "))
		case listed && !slices.Equal(reached, want):
			t.Errorf("%s reaches %v, knownServerTierReach says %v", pkg, reached, want)
		}
	}
	for pkg := range knownServerTierReach {
		if _, ok := g.Imports[pkg]; !ok {
			t.Errorf("knownServerTierReach lists %q, which no longer exists", pkg)
		}
	}
}

// TestNoUnitCycles proves every top-level directory can be assigned to one
// tier. A directory in a strongly connected component cannot: part of it
// would sit above another part, which is why event/sink and push/pushcert
// were lifted out of their parents.
func TestNoUnitCycles(t *testing.T) {
	t.Parallel()
	g := load(t)
	if cycles := layout.Cycles(g.UnitGraph()); len(cycles) > 0 {
		for _, c := range cycles {
			t.Errorf("directory-level cycle, so these cannot be assigned to tiers: %s", strings.Join(c, " -> "))
		}
	}
}

// TestPushcertImportsOnlyTheStandardLibrary keeps the invariant that stops
// storage -> pushcert -> push -> storage from becoming a real import cycle.
// This one would be caught by the compiler, but only after someone had
// written the import; the failure here names the reason.
func TestPushcertImportsOnlyTheStandardLibrary(t *testing.T) {
	t.Parallel()
	g := load(t)
	if got := g.Imports["pushcert"]; len(got) > 0 {
		t.Errorf("pushcert must import nothing in this module so storage can validate a certificate "+
			"without depending on push; it imports %s", strings.Join(got, ", "))
	}
}

// TestEventDependsOnlyOnTheProtocolCore keeps the bus in a low tier. The
// projections that know every domain live in eventsink for this reason.
func TestEventDependsOnlyOnTheProtocolCore(t *testing.T) {
	t.Parallel()
	g := load(t)
	if got := g.Imports["event"]; !slices.Equal(got, []string{"mdm"}) {
		t.Errorf("event must depend only on mdm, so every domain can publish to it; it imports %v", got)
	}
}
