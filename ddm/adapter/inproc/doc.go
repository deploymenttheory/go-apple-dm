// Package inproc adapts a ddm.Engine to service.DMHandler for a server that
// runs the mdm and ddm roles in one process.
//
// # Why
//
// The service core answers a DeclarativeManagement check-in through one
// function type, service.DMHandler, whatever sits behind it. This package
// is the single-process binding: it maps the check-in's Endpoint and Data
// to Engine.Handle and turns the engine's errors into the service codes the
// HTTP layer already maps (400 for a malformed endpoint or status body, 404
// for an unknown declaration, 500 otherwise). It is the counterpart of
// proxyclient and proxyserver: the same check-in produces the same body and
// status through either, which proxyserver's parity test pins.
//
// # References
//
//   - Decision record 0023: docs/research/decisions/0023-ddm-adapters-and-wire-contract.md
//   - Plan of record: docs/research/implementation_plan.md (phase 5)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Schema: third_party/device-management/mdm/checkin/declarativemanagement.yaml
package inproc
