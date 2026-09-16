// Package bench runs reference-server test scenarios in simulated and live
// workspaces.
//
// # Design
//
// A workspace keeps local identities and configuration across runs. Start can
// embed the server runtime or launch dmserver processes, using the same server
// configuration in either case. Up supervises those processes in the foreground;
// other commands attach through an authenticated loopback control endpoint.
//
// Simulated workspaces provide local service fixtures. Live scenarios require
// the operator's credentials and device enrollment. Doctor reports missing
// prerequisites without enrolling a device or changing its trust settings.
//
// # References
//
//   - https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/reference-bench.md
package bench
