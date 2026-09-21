//go:build acceptance || e2e

// Package acceptance runs the reference bench through its runtime and command line.
//
// # Design
//
// The acceptance and e2e build tags enable simulated catalogue scenarios against
// an embedded runtime or a built dmserver executable. Tests exercise the unified
// server using persistent bench workspaces.
//
// Live scenarios are excluded from this suite. Reports describe the simulated
// exchanges that ran; they do not establish Apple-service or physical-device
// interoperability.
//
// # References
//
//   - Reference bench: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/reference-bench.md
//   - CI coverage: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/ci.md
package acceptance
