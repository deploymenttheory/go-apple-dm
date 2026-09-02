// Package storage defines the persistence interfaces the service layer
// uses, split by concern: enrollments, push tokens, the command queue,
// bootstrap tokens, certificate associations, push certificates,
// UserAuthenticate state, and export and import, with Page and cursor
// types and sentinel errors.
//
// # Why
//
// An enrollment is the sum of what Authenticate, TokenUpdate, and
// SetBootstrapToken delivered, plus the commands queued for it, and the
// service must be able to keep that on any database an operator runs.
// Phase 2 of the plan of record fixes the contract here (decision record
// 0005) so the service is written once against interfaces and every
// backend proves itself against the storagetest suites. Later records add
// interfaces to the same package rather than new ones: certificate
// association history (0014), the push certificate store (0015),
// UserAuthenticate state (0016), and export and import (0017). Store
// composes them all.
//
// Backends live in sub-packages (inmem, sqlite, postgres, mysql via
// sqlcommon) and encryption of stored secrets in storage/crypt; this
// package holds no implementation beyond the NotNow backoff schedule.
//
// # References
//
//   - Decision record 0005: docs/research/decisions/0005-storage-interfaces.md
//   - Decision record 0013: docs/research/decisions/0013-secrets-at-rest.md
//   - Decision record 0014: docs/research/decisions/0014-cert-association-history.md
//   - Decision record 0015: docs/research/decisions/0015-push-cert-store.md
//   - Decision record 0016: docs/research/decisions/0016-user-authenticate-state.md
//   - Decision record 0017: docs/research/decisions/0017-enrollment-export-import.md
//   - Plan of record: docs/research/implementation_plan.md (section 3, core domain model; phase 2)
//   - Threat model: docs/security/threat-model.md (Storage rows)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-003, E2E-005)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
//   - Schema: third_party/device-management/mdm/checkin/authenticate.yaml, tokenupdate.yaml, setbootstraptoken.yaml
package storage
