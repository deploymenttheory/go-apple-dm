// Package layout builds this module's package import graph so tests can
// assert the tier boundaries the repository layout claims.
//
// # Why
//
// Decision record 0044 arranges packages into tiers and says which tier may
// import which. A layout that is only a directory arrangement is a comment:
// nothing stops a future import from pointing the wrong way, and the tree
// then asserts a boundary the compiler does not enforce, which misleads more
// than a flat tree would. The boundary needs a gate.
//
// golangci-lint cannot be that gate here. The workflow runs it with
// --issues-exit-code=0 and only-new-issues, so a depguard rule would report
// a violation without failing the build. Tests do fail the build, so the
// rules live in this package's tests instead, and this file supplies them
// the graph: package edges from go list, the same edges collapsed to the
// directory units a tier is assigned to, strongly connected components, and
// transitive reachability.
//
// The package deliberately holds no rules. What may import what is a
// statement about this repository and belongs in the test beside the list of
// exceptions it is ratcheting down; this file only answers questions about
// the graph.
//
// # References
//
//   - Decision record 0044: docs/research/decisions/0044-repository-layout.md
//   - Decision record 0001: docs/research/decisions/0001-architecture.md
//   - Plan of record: docs/research/implementation_plan.md (section 1, package layout)
package layout
