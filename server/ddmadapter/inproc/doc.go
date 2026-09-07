// Package inproc adapts a local ddm.Engine to service.DMHandler.
//
// # Design
//
// The adapter maps the check-in's Endpoint and Data to the engine and returns
// the protocol response: JSON for reads, empty 200 for accepted status, 404 for
// an unknown declaration and 400 for malformed input. Engine failures map to
// internal service errors. Parity tests compare the same operations with the
// split-process adapters.
//
// # References
//
//   - Decision record 0023: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0023-ddm-adapters-and-wire-contract.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Schema: third_party/device-management/mdm/checkin/declarativemanagement.yaml
package inproc
