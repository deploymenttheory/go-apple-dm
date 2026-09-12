package layout_test

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/deploymenttheory/go-apple-dm/internal/layout"
)

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
	g, err := layout.LoadRepo(repoRoot(t))
	if err != nil {
		t.Fatalf("load import graph: %v", err)
	}
	return g
}

// The tiers of decision record 0044, in ascending order. A package may
// import its own tier and every tier below it, and nothing above.
const (
	tierFoundation = iota // no domain knowledge: paging, clock, secrets, telemetry
	tierSchema            // generated types
	tierProtocol          // mdmprotocol: wire formats, no I/O
	tierPKI               // pki: identity issuance
	tierServices          // appleplatformservices: outbound clients to Apple
	tierStorage           // storage: contracts and the in-memory backend, no drivers
	tierClient            // simulator: a device, in software
	tierServer            // server: persistence, service layer, transport
	tierApp               // composition: cmd, internal/app, e2e
)

var tierNames = map[int]string{
	tierFoundation: "foundation", tierSchema: "schema", tierProtocol: "mdmprotocol",
	tierPKI: "pki", tierServices: "appleplatformservices", tierStorage: "storage",
	tierClient: "simulator", tierServer: "server", tierApp: "app",
}

// tierOf maps a package to its tier by path, which is the point of the
// layout: the import path states the tier, so this function is a reading of
// the tree rather than a list to maintain.
func tierOf(pkg string) int {
	pkg = strings.TrimPrefix(pkg, "devicemanagement/")
	switch {
	case strings.HasPrefix(pkg, "cmd/"), strings.HasPrefix(pkg, "e2e"),
		pkg == "internal/app", strings.HasPrefix(pkg, "internal/dmctl"),
		pkg == "internal/schemagen":
		return tierApp
	case strings.HasPrefix(pkg, "server/"):
		return tierServer
	case pkg == "simulator":
		return tierClient
	case pkg == "storage", strings.HasPrefix(pkg, "storage/"):
		return tierStorage
	case strings.HasPrefix(pkg, "appleplatformservices/"):
		return tierServices
	case strings.HasPrefix(pkg, "pki/"):
		return tierPKI
	case strings.HasPrefix(pkg, "mdmprotocol/"):
		return tierProtocol
	case strings.HasPrefix(pkg, "schema/"):
		return tierSchema
	default:
		return tierFoundation
	}
}

// isScaffolding reports whether pkg exists to serve tests rather than to run
// in a program. A fake or a contract suite follows the package it exercises
// and often needs a backend to seed, so tier rules say nothing about them;
// these are the packages scripts/coverage-exempt.txt exempts, for the same
// reason.
func isScaffolding(pkg string) bool {
	pkg = strings.TrimPrefix(pkg, "devicemanagement/")
	return strings.HasSuffix(path.Base(pkg), "test") ||
		pkg == "testpki" ||
		pkg == "schema/internal/conformance"
}

// knownTierExceptions records the exact permitted upward import edges.
//
// ADE uses gdmf.Lookup, gdmf.Asset and gdmf.CompareVersions for its
// software-update gate. gdmf has no in-module dependencies. Separating this
// vocabulary or adapting it at the composition boundary remains an open design
// choice; the test rejects additions to or unrecorded removals from this
// exception set.
var knownTierExceptions = map[string][]string{
	"devicemanagement/mdmprotocol/enroll/ade": {"devicemanagement/appleplatformservices/gdmf"},
}

// TestTiersOnlyImportDownwards is the boundary decision record 0044 claims,
// and the reason the tree is arranged this way at all. A caller who wants a
// declaration type must not acquire a database driver with it.
//
// This test enforces the repository-specific tier graph independently of the
// full-baseline lint gate, including imports in tests and explicit exceptions.
func TestTiersOnlyImportDownwards(t *testing.T) {
	t.Parallel()
	g := load(t)
	for _, pkg := range g.Packages() {
		if isScaffolding(pkg) {
			continue
		}
		from := tierOf(pkg)
		var up []string
		for _, dep := range g.Imports[pkg] {
			if to := tierOf(dep); to > from && !isScaffolding(dep) {
				up = append(up, dep)
			}
		}
		want := knownTierExceptions[pkg]
		if fmt.Sprint(up) == fmt.Sprint(want) {
			continue
		}
		for _, dep := range up {
			if !slices.Contains(want, dep) {
				t.Errorf("%s (%s) imports %s (%s): a package may not import a higher tier",
					pkg, tierNames[from], dep, tierNames[tierOf(dep)])
			}
		}
		for _, dep := range want {
			if !slices.Contains(up, dep) {
				t.Errorf("%s no longer imports %s: delete it from knownTierExceptions", pkg, dep)
			}
		}
	}
}

// TestEveryTierIsPopulated guards the reading above. tierOf falls through to
// foundation, so a renamed or mistyped tier directory would silently demote
// every package under it and make TestTiersOnlyImportDownwards vacuous.
func TestEveryTierIsPopulated(t *testing.T) {
	t.Parallel()
	g := load(t)
	count := map[int]int{}
	for _, pkg := range g.Packages() {
		count[tierOf(pkg)]++
	}
	for tier, name := range tierNames {
		if count[tier] == 0 {
			t.Errorf("tier %s has no packages: has a tier directory been renamed?", name)
		}
	}
}

// TestNoUnitCycles proves every directory can be assigned to one tier. A
// directory in a strongly connected component cannot: part of it would sit
// above another part, which is why event/sink and push/pushcert were lifted
// out of their parents, and why the per-domain storage backends live under
// server rather than beside the protocol packages they persist.
func TestNoUnitCycles(t *testing.T) {
	t.Parallel()
	g := load(t)
	for _, c := range layout.Cycles(g.UnitGraph()) {
		t.Errorf(
			"directory-level cycle, so these cannot be assigned to tiers: %s",
			strings.Join(c, " -> "),
		)
	}
}

// TestPushcertImportsOnlyTheStandardLibrary keeps the invariant that stops
// server/storage -> pki/pushcert -> appleplatformservices/push ->
// server/storage from becoming a real import cycle.
func TestPushcertImportsOnlyTheStandardLibrary(t *testing.T) {
	t.Parallel()
	g := load(t)
	if got := g.Imports["devicemanagement/pki/pushcert"]; len(got) > 0 {
		t.Errorf(
			"pki/pushcert must import nothing in this module so server/storage can validate a "+
				"certificate without depending on push; it imports %s",
			strings.Join(got, ", "),
		)
	}
}

// TestEventDependsOnlyOnTheProtocolCore keeps the bus in a low tier. The
// projections that know every domain live in server/eventsink for this
// reason.
func TestEventDependsOnlyOnTheProtocolCore(t *testing.T) {
	t.Parallel()
	g := load(t)
	want := []string{"devicemanagement/mdmprotocol/mdm"}
	if got := g.Imports["devicemanagement/mdmprotocol/event"]; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf(
			"event must depend only on mdm, so every domain can publish to it; it imports %v",
			got,
		)
	}
}

// Library packages belong under devicemanagement. The remaining root packages
// provide shared URL validation, generation and repository checks.
func TestLibraryPackageLocations(t *testing.T) {
	t.Parallel()
	for _, pkg := range load(t).Packages() {
		switch {
		case strings.HasPrefix(pkg, "devicemanagement/"), strings.HasPrefix(pkg, "server/"):
		case pkg == "internal/httpsurl", pkg == "internal/layout",
			pkg == "internal/schemagen", pkg == "cmd/schemagen":
		default:
			t.Errorf("library package %s must live under devicemanagement", pkg)
		}
	}
}

// A new container directory must not make protocol packages fall through to
// the foundation tier, which would hide upward dependencies.
func TestLibraryTierClassification(t *testing.T) {
	t.Parallel()
	for pkg, want := range map[string]int{
		"devicemanagement/clock":                     tierFoundation,
		"devicemanagement/internal/cbor":             tierFoundation,
		"devicemanagement/schema/commands":           tierSchema,
		"devicemanagement/mdmprotocol/ddm":           tierProtocol,
		"devicemanagement/pki/acme":                  tierPKI,
		"devicemanagement/appleplatformservices/axm": tierServices,
		"devicemanagement/storage/inmem":             tierStorage,
		"devicemanagement/simulator":                 tierClient,
		"server/service":                             tierServer,
		"internal/schemagen":                         tierApp,
	} {
		if got := tierOf(pkg); got != want {
			t.Errorf("tierOf(%q) = %d, want %d", pkg, got, want)
		}
	}
}
