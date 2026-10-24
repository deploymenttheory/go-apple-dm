// Package inventory retains independent Apple and native protocol source evidence.
//
// # Design
//
// Each ResourceRecord addresses one source.Reference: kind, account and opaque
// source resource identity. Successful original observations and list documents
// retain complete JSON, byte representation and provenance. History, safe failed
// outcomes and lifecycle metadata remain separate. No observation merges records,
// creates a provisional alias, resolves field equivalence or persists a ranked
// normalized device projection. Missing serials do not prevent source retention.
//
// Link explicitly invokes the independently reusable correlation package. Only an
// actual organization-device resource and matching authenticated native serials
// with eligible Device Enrollment provenance can establish a hardware relationship.
// User Enrollment and unchecked provenance cannot establish that relationship.
// Links retain both identities and require fresh explicit correlation after source
// evidence or lifecycle changes. Optional field resolution stays caller-owned.
//
// Migrate atomically extracts legacy original source envelopes, reconstructs facts
// solely from retained raw JSON and removes aggregate records and derived indexes.
// Missing originals or authenticated enrollment provenance remain incomplete and
// refreshable. It fences existing worker owners and restarts incompatible cursors
// while preserving account credentials, schedules and completed job history.
// Applications must call Migrate before starting workers or native observers.
//
// The syncer composes the standalone AxM collector. Successful resources, complete
// source documents, safe outcomes and enumeration checkpoints share fenced backend
// transactions. Coverage publishes only after complete traversal; a failed refresh
// never replaces successful source evidence. Account revisions and renewable leases
// serialize work. Native observers retain successful report history so omitted
// fields remain available to an explicit resolver with their original times.
//
// Backend supplies atomic document updates. Memory is suitable for embedding and
// tests. The reference SQL backend reuses the application transaction and row-bound
// storage keyring, including native protocol/event rollback. Default inspection
// exposes resource metadata; original source values require separate raw authority.
//
// # References
//
//   - Decision record 0057: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0057-agentless-device-inventory.md
//   - Decision record 0030: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0030-apple-business-manager-api-client.md
//   - Decision record 0021: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0021-status-reports-and-subscriptions.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Agentless inventory guide: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/agentless-inventory.md
//   - Apple: https://developer.apple.com/documentation/applebusinessapi
//   - Apple: https://developer.apple.com/documentation/appleschoolmanagerapi
//   - Apple: https://developer.apple.com/documentation/devicemanagement/status-items
package inventory
