// Package storage defines MDM persistence contracts and shared sentinel errors.
//
// # Design
//
// Store composes enrollment, command queue, push, certificate, bootstrap-token,
// user-authentication and migration interfaces. Shared contracts define
// lifecycle cleanup, NotNow backoff, certificate history and cursor behavior
// independently of a database. Pagination values come from paging.
//
// storage/inmem supplies process-local storage; server/sqlstore supplies SQLite,
// PostgreSQL and MySQL implementations. storage/crypt handles selected secret
// values. Account-driven associations and revocation use separate state
// interfaces and are not included in enrollment export/import.
//
// # References
//
//   - Decision record 0005: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0005-storage-interfaces.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Decision record 0014: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0014-cert-association-history.md
//   - Decision record 0015: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0015-push-cert-store.md
//   - Decision record 0016: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0016-user-authenticate-state.md
//   - Decision record 0017: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0017-enrollment-export-import.md
//   - Decision record 0044: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0044-repository-layout.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (Storage rows)
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-003, E2E-005)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device
//   - Apple: https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses
//   - Schema: third_party/device-management/mdm/checkin/authenticate.yaml, tokenupdate.yaml, setbootstraptoken.yaml
package storage
