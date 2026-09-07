// Package layout builds repository import graphs for dependency-boundary tests.
//
// # Design
//
// LoadRepo reads both Go modules, records package edges and maps them to
// repository-relative paths. The graph supports transitive reachability and
// directory-level strongly connected components. Tests define tier rules and
// their explicit exceptions.
//
// Keeping the graph reader separate from policy lets the tests verify package
// placement and module direction. The lint workflow reports issues without a
// failing issue exit code, so these boundaries are enforced through Go tests.
//
// # References
//
//   - Decision record 0044: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0044-repository-layout.md
//   - Decision record 0001: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0001-architecture.md
package layout
