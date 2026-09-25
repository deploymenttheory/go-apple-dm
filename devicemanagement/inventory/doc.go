// Package inventory maintains agentless device records from Apple Business
// Manager, Apple School Manager and native MDM and DDM observations.
//
// # Design
//
// The repository reconciles source observations into stable device records.
// Source identities are account-scoped; serial numbers join records only when
// the match is unambiguous. Conflicts remain inspectable, and aliases preserve
// references when provisional enrollment records join existing cloud records.
// Named Apple accounts supply one shared fleet rather than authorization tenants.
//
// Observations retain original payloads, source-qualified fields and provenance.
// Normalized fields provide a queryable projection without discarding unknown
// Apple attributes. Successful evidence and refresh failures have separate
// timestamps. Coverage retains every plan; a failed lookup does not imply absent
// coverage. Native observers accept caller-authenticated MDM results and DDM
// status, preserving per-item timestamps across partial status reports.
//
// The syncer collects organization devices, device details, MDM assignments and
// AppleCare coverage through the AxM client. Apple built-in MDM inventory is
// collected when available. Enumeration pages commit with their checkpoints;
// renewable leases serialize syncs and account revisions fence stale work.
// Jobs support pause, resume and cancellation, with cron and interval schedules.
// Queries, reports and streaming exports share the record and field model.
//
// Backend defines atomic document updates. The in-memory implementation supports
// embedding and tests; server/inventorystore supplies encrypted SQL persistence
// and joins native observations to the existing protocol transaction. HTTP
// routing, authorization, command dispatch and worker supervision belong to the
// reference server. Callers must authorize access to complete source payloads
// separately from the reviewed fields exposed by PublicRecord. Cloud records
// can exist before enrollment; native collection requires MDM enrollment and
// device connectivity, without an installed agent.
//
// # Errors
//
// ErrNotFound, ErrInvalid, ErrConflict and ErrStopped are catalogued client
// conditions under DM-INVENTORY-*. ErrLease is an operator condition of kind
// Conflict.
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
