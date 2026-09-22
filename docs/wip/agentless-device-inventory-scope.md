# Agentless device inventory — feature scope

Scope recap dated 22 September 2026, reflecting implementation through `5467df9`
in [PR #255](https://github.com/deploymenttheory/go-apple-dm/pull/255).
This describes the implemented feature, not a proposed backlog.

## Purpose

Provide a persistent device record in the Go library, reference server and `dmctl`,
populated by Apple services without installing an agent on the device.
ABM/ASM organization inventory can establish a record before MDM enrollment.
Native Apple MDM responses and declarative device management (DDM) status can enrich
the same record after enrollment.

AxMJamfSync informed the Apple API collection and reporting scope. This implementation
stores and reconciles the information in this project's own inventory. It does not
connect to Jamf Pro or write purchasing and warranty data into Jamf records.

## Delivery across the project

| Layer | Included responsibility |
| --- | --- |
| AxM API library | Organization devices, details, MDM assignments, AppleCare plans, optional Apple built-in MDM inventory, pagination and original resource payloads |
| `devicemanagement/inventory` | Device records, source reconciliation, queryable fields, accounts, sync orchestration, jobs, schedules, reports and exports; transactional backend interface and in-memory implementation |
| Reference server | Encrypted SQLite/PostgreSQL/MySQL persistence, migrations, backup/restore integration, authenticated administrative routes, authorization and supervised collection workers |
| Native MDM/DDM integration | Enrichment from tracked, acknowledged MDM results, effective DDM status and enrollment metadata |
| `dmctl` | Account administration, device queries and collection, sync controls, schedules, reports, exports, presets and diagnostics |

## Apple collection scope

The AxM sync discovers organization devices, enriches their cloud data, and commits
the resulting observations into device records. It uses the existing AxM OAuth client
with the appropriate Business Manager or School Manager scope.

| Apple API | Information collected |
| --- | --- |
| `GET /v1/orgDevices` | Organization device inventory across all pages |
| `GET /v1/orgDevices/{id}` | Individual device details, including fields omitted from enumeration |
| `GET /v1/orgDevices/{id}/assignedServer` | Assigned MDM service and service metadata |
| `GET /v1/orgDevices/{id}/appleCareCoverage` | Every returned warranty/AppleCare plan across all pages |
| `GET /v1/mdmServers` | Available MDM services, including discovery of Apple's built-in MDM |
| `GET /v1/mdmDevices` | Devices managed by Apple's built-in MDM, when available |
| `GET /v1/mdmDevices/{id}/details` | Additional inventory for those Apple-managed devices |

Apple built-in MDM collection is conditional on an `APPLE_MDM` service and is not
requested for ASM. These endpoints do not supply native inventory for devices enrolled
only in this reference server. Organization resource IDs remain opaque and account-scoped.

## Device record and retained information

A device record has a stable local ID, optional serial number, creation/update times,
normalized fields, source observations, all coverage plans and any identity conflicts.
It exists independently of a particular enrollment or Apple account membership.

| Information group | Examples retained when returned by the source |
| --- | --- |
| Identity and hardware | Serial number, Apple resource IDs, UDID, model, product family/type and device name |
| Network and cellular | Wi-Fi, Bluetooth and Ethernet MAC addresses, IMEI, MEID and EID |
| Purchasing and organization | Order number/date, purchasing source identifiers/type, organization dates and release information |
| MDM assignment and migration | Assigned service, migration capability, migration status and deadline |
| Warranty and AppleCare | Every plan and its original attributes, status, agreement number, dates, payment type and renewal/cancellation information |
| Native operational data | Supported OS/hardware/security fields, profiles, installed applications, certificates, DDM status, enrollment state and check-in metadata |

The field list is extensible. Complete original AxM resource payloads and unknown
attributes are preserved alongside normalized, queryable values. Field discovery exposes
populated source-qualified paths without requiring a database migration for each new
Apple attribute. Nulls, false values, arrays and large integers are retained.

"All fields" means preserving the data returned by the implemented Apple collection
paths. It does not mean Apple supplies every field for every organization or device,
or that the feature calls every endpoint in every Apple service. Default read responses
contain reviewed fields; complete source evidence requires explicit raw-inventory access.

## Identity, freshness and reconciliation

- Match account-scoped source identity first, then an unambiguous normalized serial.
  Conflicting serials remain visible instead of forcing an uncertain merge.
- Merge a provisional enrollment record into its matching cloud record when its serial
  becomes known; preserve the earlier local record reference through an alias.
- Retain source provenance and observation times for selected fields. Newer evidence
  supplies the normalized view; equally fresh native evidence prefers DDM over MDM.
- Track successful observations separately from refresh attempts, errors and expiry.
  A failed refresh retains the last successful evidence.
- Preserve omitted items and their timestamps in partial DDM reports; full reports
  replace the source snapshot.
- Keep organization membership, MDM assignment and enrollment as separate facts.
  Confirm a missing organization device after complete enumeration and individual lookup
  before marking it absent.
- Deleting an account disconnects its observations and cancels its work. Device records
  remain, and deletion does not unenroll devices.

Coverage keeps every plan, including subscriptions without an end date or agreement
number. A successful empty result means no coverage; failed requests do not.
Coverage summaries are evaluated at read time, with the next expiry taken from the
earliest finite expiry among currently active plans.

## Native collection without an agent

The reference server backfills existing acknowledged results and effective DDM status,
then requests supported native inventory through the ordinary command queue:

- `DeviceInformation`, using generated platform/version/channel query metadata.
- `SecurityInfo`, `ProfileList`, `InstalledApplicationList` and `CertificateList`.
- DDM status through the existing capability-aware subscription mechanism.

Unknown platforms start with a bootstrap query before expanded collection. Only
acknowledged results for tracked commands update inventory. User-channel results do
not overwrite physical-device fields, and arbitrary installed certificates do not
become the device's MDM enrollment certificate.

Native collection requires enrollment and device connectivity. APNs wakes devices
through the existing notifier; without APNs credentials, commands wait for the device
to contact the server. Cloud organization discovery does not require enrollment.

## Accounts, jobs and scheduling

Multiple named ABM/ASM accounts feed **one shared fleet and database**. Credentials
and source identities remain separate, but accounts are not authorization tenants
or fully isolated AxMJamfSync environments. ADE account associations are explicit.

- Account create, read, update, verify and delete operations; credentials are never
  returned by account reads. Legacy AxM configuration imports once as a default account.
- Initial discovery and a daily 02:00 UTC schedule for a newly enabled account.
- Serial AxM sync across accounts and server processes, using renewable database leases.
- Atomic enumeration-page/checkpoint commits, restart recovery, pause/resume, cancellation
  and account-revision checks that prevent stale workers from committing changes.
- Honest terminal results: `success`, `partial`, `failed` and `cancelled`.
- Repeat schedules in minutes, hours, days, weeks or months, or five-field numeric cron;
  IANA time zones and coalescing of missed runs. Scheduling runs in the server.
- Device-detail/assignment freshness defaults to 24 hours; coverage freshness to seven
  days. Coverage budgets and opt-out settings control repeated requests. Forced sync
  bypasses freshness while retaining the per-run coverage budget.
- Native collection defaults to 24 hours; completed job retention defaults to 30 days.
  Record retention is separate from completed-job cleanup.

## Querying, reporting and export

Lists, reports and exports share source/account filters and the `combined`, `apple`
and `managed` focus modes. Queries support public-field search and typed conditions
for equality, array membership, existence and ordered comparisons.

Reports provide the data equivalents of dashboard counts: product families, models,
purchasing sources, coverage, MDM assignment, migration capability/status/deadlines,
source overlap, enrollment, FileVault, check-in freshness, Apple Silicon, certificate
expiry and year added. OS currency uses the newest reported major version within each
OS family and source scope for current/N-1/N-2 counts.

Exports support streaming JSON, NDJSON and CSV, with reusable presets for ordered
columns and filters. Network/cellular identifiers are opt-in CSV columns. Nested values
are encoded as JSON cells, and formula-leading CSV values are escaped. Raw exports
retain complete stored source evidence and require separate authorization. Interrupted
streams cause a nonzero CLI exit.

Diagnostics provide limited summaries with consistent `<device N>` labels. They do
not bundle raw logs, serial numbers, hosts, payloads or credentials.

The main CLI families are:

```text
dmctl axm accounts list|create|get|update|delete|verify|sync
dmctl devices list|get|fields|collect
dmctl inventory sync|jobs|schedules|reports|export|presets|diagnostics
```

## Persistence and access control

The reference server integrates inventory with its existing SQL database, storage
keyring, versioned migrations and backup/restore catalogue. Persistent document payloads
are encrypted and authenticated against their document identity. Native SQL observations
join the protocol/event transaction so failures can roll back together.

Four explicit administrative actions separate ordinary reads, complete source access,
inventory operations and account administration:

- `readInventory`
- `readRawInventory`
- `manageInventory`
- `manageAxMAccounts`

These permissions are not automatically granted to routine roles. Raw export access is
audited before delivery and its delivery outcome is recorded afterward. Library callers
embedding the repository must supply their own authorization boundary.

## Boundaries and validation

- No Jamf integration, Jamf reconciliation or Jamf write-back.
- No desktop/web dashboard, menu-bar application, OS notifications or launch-at-login
  behavior. Reporting and controls are exposed through the library, server API and CLI.
- No per-organization tenant isolation; named accounts share existing fleet-wide RBAC.
- No agent, scripts or agent-derived inventory. ABM-only records cannot imply current
  OS/security/application state that requires a native management source.
- RAM reporting is explicitly unavailable because the covered Apple APIs do not provide
  a reliable cross-platform field.
- Records hold current source snapshots and freshness metadata, not a complete historical
  inventory ledger. Query `as_of` controls time-based calculations, not historical replay.
- In-memory embedding does not provide restart durability or shared SQL transaction rollback.
- Tests use simulated Apple services. Live Apple account permissions, optional field
  availability and physical-device interoperability have not been validated by this PR.

CI passed for `5467df9`, including Linux/macOS/Windows unit tests, PostgreSQL/MySQL storage
integration, SQLite/PostgreSQL end-to-end tests, server acceptance, standalone installation,
security, lint, documentation and packaging. The unchanged 95% coverage gate passed at
**95.04% overall**, with **96.45%** for the inventory library and **96.83%** for inventory
SQL storage. These are coverage measurements for that revision, not a live-Apple certification.

## Implementation references

- [Operational guide and command examples](../operations/agentless-inventory.md)
- [ADR 0057: Agentless device records in a shared fleet](../research/decisions/0057-agentless-device-inventory.md)
- [Inventory record types](../../devicemanagement/inventory/types.go)
- [AxM sync implementation](../../devicemanagement/inventory/sync.go)
- [Reports and exports](../../devicemanagement/inventory/views.go)
- [Server inventory integration](../../server/internal/app/inventory.go)
- [Administrative routes and permissions](../../server/internal/app/admininventory.go)
- [CLI commands](../../server/internal/dmctl/inventory.go)
- [Passing CI coverage gate](https://github.com/deploymenttheory/go-apple-dm/actions/runs/35654007547/job/106518994216)
