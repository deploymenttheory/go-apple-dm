// Package simulator exercises modeled Apple MDM and enrollment flows as a
// device-side test client.
//
// # Design
//
// The client supports check-in, command polling/results, SCEP, OTA, ADE,
// account-driven authentication, user channels, Shared iPad and ACME.
// Declarative synchronization tracks tokens and versions, evaluates the
// supported predicate subset and emits full/incremental status reports. Fault
// options cover NotNow, command errors, stale tokens, failed fetches and dropped
// reports.
//
// The simulator uses independent client paths where available, including
// golang.org/x/crypto/acme. It does not reproduce every device behavior; tests
// against it do not replace physical-device and live-service validation.
//
// # References
//
//   - Decision record 0024: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0024-simulator-ddm-client-and-predicates.md
//   - E2E scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/commands-and-queries
//   - Apple: https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management
//   - Schema: third_party/device-management/declarative/protocol/*.yaml, declarative/status/**
package simulator
