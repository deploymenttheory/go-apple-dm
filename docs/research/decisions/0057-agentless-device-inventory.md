# 0057: Independent device source evidence and explicit serial relationships

## Context

Organization devices can be discovered before enrollment. Native MDM and DDM can
subsequently supply authenticated device evidence without an installed agent. Apple
accounts, opaque resource IDs and enrollment/channel identities have independent
addressing and provenance roles. A mandatory normalized fleet record loses these
boundaries and can imply relationships or field equivalence that the sources do not
establish.

## Decision

Collection, correlation and optional resolution are independently usable library
operations. Apple clients, collectors and `devicemanagement/correlation` require no
inventory repository. `devicemanagement/source.Reference` supplies opaque account-scoped
provenance. Library packages never import the reference server.

`devicemanagement/inventory` persists one `ResourceRecord` per source reference.
Original successful observation history, complete list documents, request outcomes,
account credentials, jobs and schedules have separate document namespaces. Raw JSON
retains unknown members, relationships, null, false, zero, empty arrays, numeric
lexemes and its original byte representation. Failed attempts never replace successful
evidence. Explicit `data.<original path>` and `meta.<column>` selectors keep source
members distinct from metadata, including original `data.id` versus repository
`meta.id`; unqualified selectors are invalid. No aliases, unit conversions,
source-ranking projection or automatic record merges are persisted.

`Repository.Link` explicitly invokes the standalone correlator. The Apple input must
be an actual collected organization-device resource. Serial is the sole physical
cross-source key. Native reports require matching authenticated serials and recorded
ADE, account-driven Device Enrollment or profile-based Device Enrollment provenance.
User Enrollment, unknown methods, unauthenticated evidence and missing serials cannot
establish a hardware link. Opaque Apple IDs address APIs; enrollment, channel and user
IDs retain their protocol roles and establish neither serial identity nor authentication.

Checked links retain both original source references without combining their records.
Each newly retained source original, including a delayed report that does not replace
the latest snapshot, invalidates affected links and requires explicit fresh correlation.
Exact acknowledged replays add no evidence and retain checked links. Absence or
disconnection also invalidates links. Historical reports remain available, including unchecked
legacy reports. A valid authenticated refresh can establish a link without discarding
those originals; conflicting authenticated serials for the same source remain rejected.

Optional `correlation.Resolve` accepts only caller-declared equivalent fields and units.
Latest successful native evidence wins before Apple regardless of Apple's refresh time.
Omission retains earlier native evidence; false, zero, empty arrays and null remain
values. Originals, candidates and disagreements retain provenance. Purchasing and
coverage facts can remain Apple-only. Inventory introduces no fleet eligibility,
assignment policy or automatic field-resolution operation.

The synchronizer composes `axm/collection`. Each enumeration page commits resources,
complete source documents, safe outcomes and its checkpoint atomically after checking
the existing owner lease and account revision. Apple requests occur outside transactions.
Coverage pages retain source documents independently and publish successful coverage
resources only after full traversal. Failure cannot establish absence or replace an
older successful coverage result. Completed pagination does not imply an Apple-side
consistent snapshot. Native observers retain acknowledged command and validated status
history within the existing SQL protocol/event transaction.

## Persistence and migration

`Repository.Migrate` runs before workers and observers. One backend transaction extracts
original source envelopes, including unknown observation metadata and per-field times,
into opaque retained envelope history. Reconstructed source facts come solely from
actual retained raw JSON. Aggregate normalized fields, serial indexes and aliases are
never used to fabricate source data, authenticated serials or enrollment methods.
Missing originals and missing native authentication/method provenance stay incomplete
and refreshable. Serialless enrollment remains a usable independent protocol identity.

Migration removes obsolete aggregate/index namespaces, changes the existing lease owner
that old workers already check, restarts incompatible nonterminal cursors/checkpoints,
and activates a persistent old-write fence in the same transaction. The old outbound
lease deadline remains in place so replacement workers wait for possible in-flight
Apple requests. Accounts, credentials, schedules, completed job history and unrelated
enrollment, queue and audit state survive. Interruption rolls back completely; rerunning
a completed migration is harmless.

SQLite and PostgreSQL install initially inactive trigger fences transactionally.
MySQL installs only missing column/trigger DDL under an inventory-specific advisory
lock; partial DDL installation can be retried without resetting an active fence.
Activation remains transactional. MySQL triggers use a current locking read of the
existing inventory lock row, preventing transactions begun before migration from
recreating aggregate keys after it. Trigger creation requires a migration role permitted
by the deployment's binary logging policy. Disposable test services explicitly configure
that policy; the application role does not receive `SUPER`.

The SQL backend reuses the existing pool, local transaction coordination and AES-GCM
keyring with document-key/column associated data. Raw payloads, envelope history and
checkpoints remain sealed. In-memory embeddings retain their own transaction boundary
and provide neither cross-store rollback nor restart durability.

## Consequences

Independent source identities remain inspectable before and after enrollment. Explicit
serial relationships cannot silently move evidence between Apple accounts or enrollments.
Original history lets callers apply native-first resolution without persistence guessing
field equivalence. Missing historical originals remain visible as recovery needs.

The reference server supervises collection and exposes resource metadata under
`readInventory`; original source data requires `readRawInventory`. Credentials never
appear in account read responses. Named Apple accounts are sources in one fleet, not
authorization tenants. The server records the actual ADE, account-driven or profile-based issuance flow,
including User Enrollment, and carries it into issued certificate evidence. Native
adapters require the enrollment's pinned issued certificate before attaching that
method and authenticated serial. Historical missing provenance stays incomplete.
Serial inspection and evidence routes preserve source boundaries. Correlation is an
explicit managed mutation; resolution is an explicit raw-authorized read over checked
links and caller-declared equivalent paths. Arbitrary mappings, complete history and
original bytes require `readRawInventory`. Metadata queries reject retired filters and
predicate-mismatched cursors; raw saved predicates never leak through ordinary presets. Native collection requires enrollment
and connectivity. Automated tests do not establish populated live Apple workflows or
physical-device outcomes.

## Verification

Memory contracts exercise independent identities, explicit eligible links, User
Enrollment exclusion, serial conflicts, raw byte/number fidelity, unknown metadata,
partial report history, lifecycle invalidation, failed outcomes and transactional
fault injection at every persistence boundary. Migration tests cover false normalized
aliases, missing originals, preserved operational state, old worker responses,
serialless enrollment, interruption/retry and legacy-to-authenticated-refresh linking.

SQLite, PostgreSQL and MySQL contracts exercise encrypted migration, protocol/event
rollback, old transactions, retired native writes, repeated migration, key rotation
and original history. MySQL contracts cover partial DDL installation, active-fence
retry and concurrent opens. Independent external-package collection and correlation
contracts retain their caller-owned sink and authentication boundaries.

## References

- [Apple Business API](https://developer.apple.com/documentation/applebusinessapi)
- [Collection contract](../../../devicemanagement/appleplatformservices/axm/collection/doc.go)
- [Correlation contract](../../../devicemanagement/correlation/doc.go)
- [Inventory contract](../../../devicemanagement/inventory/doc.go)
- [Atomic storage](0005-storage-interfaces.md)
- [Secrets at rest](0013-secrets-at-rest.md)
- [MySQL trigger creation](https://dev.mysql.com/doc/refman/8.0/en/create-trigger.html)
- [Agentless inventory operations](../../operations/agentless-inventory.md)
