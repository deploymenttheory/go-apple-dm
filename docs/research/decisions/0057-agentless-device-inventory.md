# 0057: Agentless device records in a shared fleet

## Context

Organization-owned devices must be discoverable before enrollment. Native MDM and DDM
can subsequently enrich them without an installed agent. Several ABM/ASM organizations
can supply one managed fleet, while their credentials and resource identities remain
independent. AxMJamfSync demonstrates the organization, assignment and coverage APIs;
its Jamf reconciliation layer is outside this project's scope.

## Decision

Add an explicit inventory orchestration tier below server adapters and above Apple
clients and protocol types; the layout tests enforce that dependency direction.
Add `devicemanagement/inventory` with a transactional backend contract, source snapshots,
stable device records, query indexes, sync jobs, leases, schedules, reports and exports.
Compose it in the reference server with the existing SQL pool, keyring, native protocol
handlers and supervised workers. Keep one fleet, one enrollment service and existing
fleet-wide RBAC. Named Apple accounts are data sources, not authorization tenants.

Provide source collection independently through
`devicemanagement/appleplatformservices/axm/collection`. Each caller-selected devices,
detail, assignment or coverage stream commits complete raw resources, request outcomes
and a versioned checkpoint through a caller-owned sink. The stream binds account,
generation, operation and opaque Apple resource ID. Library collection has no dependency
on inventory records; applications can keep their own evidence model and persistence.
Inventory synchronization composes the same collector with its existing leases and
transactional backend. Device pages commit with the inventory job checkpoint. Coverage
pages stage in caller-owned memory and replace successful inventory evidence only after
full traversal; interruption retries that resource rather than publishing partial coverage.

Failed collection attempts retain their classification separately from successful evidence.
Only the final successful list page emits an enumeration-completion marker. Incomplete
traversal cannot establish absence, and completion does not imply a consistent Apple-side
snapshot. Sinks fence stale checkpoints and atomically commit evidence, outcome and
continuation. After an uncertain commit result, callers reload authoritative state before
resuming; collector instances hold no durable state. New generations begin with an empty
checkpoint. Existing inventory page checkpoints resume through the collector without
changing their retained device evidence.

Preserve every AxM resource as raw JSON alongside structured fields, including unknown
members, nulls, false and arrays. Keep all coverage plans. Separate successful evidence
from refresh failures and coverage absence from lookup errors. Source identities are
opaque and account-scoped. Match serials only when unambiguous; retain conflicts and
resolve provisional enrollment records through stable aliases when their serial arrives.

Commit each enumeration page with its checkpoint. Serialize outbound syncs with a
renewable database lease and revision-fenced commits. Keep native enrollment, organization
membership and MDM assignment as separate facts. Account deletion disconnects observations
and cancels work without unenrolling or deleting devices.

Native observers run after tracked result persistence or validated status application,
inside the existing SQL event transaction. User-channel reports do not overwrite physical
inventory. Default views contain reviewed fields; complete payloads require the explicit
`readRawInventory` action. Credentials never appear in account read responses.

## Consequences

The reference server gains agentless device discovery before enrollment and native
operational inventory afterward. Source and field timestamps make stale or contradictory
data visible. Full raw evidence and forward-compatible fields avoid discarding newly
introduced Apple attributes. Dynamic fields use shared typed predicates and hash indexes
for equality/membership instead of a migration for every new Apple key.

Raw payloads and keys require the existing encrypted persistent-server configuration.
One shared fleet does not isolate administrators by organization. In-memory embeddings do
not offer cross-store rollback or restart durability. Native collection still requires
MDM enrollment and device connectivity, whereas AxM organization records do not.

See [agentless inventory operations](../../operations/agentless-inventory.md) for API calls,
CLI commands, freshness, scheduling, permissions and validation limits.

## Verification

The external-package collection tests use a caller-owned sink without inventory imports
and independent TLS/OAuth wire fixtures. They exercise original resource and document
JSON (unknown fields, relationships, nulls, false, zero, empty arrays and integers above
2^53), pagination and coverage pagination, checkpoint serialization and a new collector
instance, failed and ambiguous commits, cancellation, authentication/permission failures,
malformed documents, cyclic continuations and page caps. Inventory tests verify both
current and retained page-checkpoint resumption and incomplete-coverage preservation.

## References

- [Apple Business API](https://developer.apple.com/documentation/applebusinessapi)
- [Collection contract](../../../devicemanagement/appleplatformservices/axm/collection/doc.go)
