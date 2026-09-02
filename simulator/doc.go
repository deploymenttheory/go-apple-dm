// Package simulator drives an MDM server the way an Apple device does.
//
// # Why
//
// Servers need to be tested end to end without hardware. The simulator
// performs check-in (Authenticate, TokenUpdate, CheckOut, bootstrap tokens,
// GetToken, DeclarativeManagement), polls the server URL with Idle,
// answers commands with typed responses, can inject NotNow and Error
// replies, enrolls through SCEP and the OTA profile service, and runs a
// declarative management client: it synchronises tokens, declaration items,
// and declarations, evaluates activation predicates with ddm/predicate, and
// builds status reports with Apple's reason codes so the server's grading
// is observable. Faults (dropped status, stale token, failed fetch) model
// the device behaviours the references tripped over. It never imports the
// server-side engine, so it stays an independent client.
//
// # References
//
//   - Decision record 0024: docs/research/decisions/0024-simulator-ddm-client-and-predicates.md
//   - Plan of record: docs/research/implementation_plan.md (phase 2, phase 5)
//   - E2E scenarios: docs/testing/e2e-scenarios.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/commands-and-queries
//   - Apple: https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management
//   - Schema: third_party/device-management/declarative/protocol/*.yaml, declarative/status/**
package simulator
