# Agentless device inventory

The `devicemanagement/inventory` package reconciles Apple Business Manager (ABM),
Apple School Manager (ASM), native MDM command responses and declarative status into
persistent device records. A record can exist before enrollment. No installed agent,
script execution, Jamf connection or Jamf write-back is involved.

The reference server keeps named Apple connections in **one shared fleet and one
database**. They are data sources, not tenants. Existing enrollment URLs and fleet
RBAC remain in force. Explicit `dep_accounts` links identify related Automated Device
Enrollment registrations; identical account names do not establish that relationship.
When supplied, `apple_org_id` is checked against linked ADE account metadata.

## Apple API collection

Every list follows `links.next` or `meta.paging.nextCursor`, retaining query parameters
and rejecting a different origin. Authentication is the existing AxM OAuth client:
`POST https://account.apple.com/auth/oauth2/token`, with `business.api` or `school.api`.
The JWT audience remains `https://account.apple.com/auth/oauth2/v2/token`.

| API | Purpose |
| --- | --- |
| `GET /v1/orgDevices` | Enumerate all organization devices and preserve original resources |
| `GET /v1/orgDevices/{id}` | Complete details, including release timestamps unavailable in list responses |
| `GET /v1/orgDevices/{id}/assignedServer` | Assigned MDM service and its metadata |
| `GET /v1/orgDevices/{id}/appleCareCoverage` | Every coverage plan, following all pages |
| `GET /v1/mdmServers` | Discover whether Apple's built-in MDM is available |
| `GET /v1/mdmDevices` | Apple built-in MDM devices, only when an `APPLE_MDM` service exists |
| `GET /v1/mdmDevices/{id}/details` | Additional built-in MDM inventory |

The existing AxM library also exposes assigned-server linkage and per-server device
relationships. Resource IDs are opaque; they are never substituted with serials.
Apple built-in MDM endpoints do not return inventory for devices enrolled only in
this reference server, and are not requested for ASM connections.

Wi-Fi, Bluetooth and Ethernet MAC addresses, IMEI, MEID, EID, order information,
purchase source, organization dates, MDM migration capability/status/deadline and
all unknown Apple attributes are retained. Scalar or array cellular/Ethernet responses
become arrays in normalized fields; original representations remain in raw evidence.
Empty or whitespace-only identifiers are omitted from normalized fields; empty array
members are removed. A field with no usable identifier is absent from filters and
exports, while its original source value remains available with raw access.
Null, false, empty arrays and integers above 2^53 survive. `fields` discovery exposes
source-qualified paths for new fields without a schema migration.

Coverage retains all plans and their original attributes. An active subscription can
have no end date or agreement number. A successful empty collection means `none`;
permission failures, 404s, timeouts and unknown statuses never become “no coverage”.
Failed refreshes keep the last successful values and mark attempt metadata separately.
`coverage_expiry` is the earliest finite expiry among currently active plans, evaluated
at the query's `as_of` time.

## Records and native collection

Source identity is matched first, followed by an unambiguous normalized serial.
Conflicting serials remain inspectable. Duplicate resource identities reporting one
serial within an Apple account prevent automatic matching. If a provisional enrollment
learns its serial later, its observations join the existing cloud record; the previous
local device link remains an alias.

Each source records its successful snapshot, observation time, latest attempt,
error code, expiry, absence and disconnection. Native operational observations and
cloud purchasing facts remain separately available. Newer evidence supplies the
normalized view; equally fresh evidence prefers DDM, then MDM. Partial DDM reports
retain omitted items and their original timestamps; full reports replace the snapshot.
DDM `diskmanagement.filevault.enabled` supplies the normalized `filevault_enabled`
field, including a reported `false`. Serial normalization retains the selected
source's provenance, and unset enrollment dates are omitted rather than represented
as year-one timestamps.
Account removal disconnects its sources but does not remove devices or disable enrollment.
An absent Apple device is tombstoned only after a complete enumeration and a confirming
individual lookup. Organization release and MDM enrollment are separate facts.

The supervised native worker discovers new enrollments within its one-minute sweep,
backfills existing acknowledged results and effective DDM status, and requests:

- `DeviceInformation`: every query allowed by the generated platform/version/channel metadata;
  an unknown platform starts with a bootstrap query, followed by the expanded query once known.
- `SecurityInfo`, `ProfileList`, `InstalledApplicationList`, and `CertificateList`, when supported.
- DDM status through the existing capability-aware subscription mechanism, enabled by default
  by `DM_SUBSCRIPTIONS`. Setting it to false remains supported.

Only acknowledged responses for stored commands can update inventory. User-channel data
does not overwrite physical-device fields. Native requests use the ordinary command queue,
validation, deduplication and APNs notifier. Without APNs credentials, requests remain queued
until the device contacts the server. Authenticated enrollment certificates supply their
own expiry metadata; arbitrary installed certificates are not treated as enrollment identity.
RAM is reported as unavailable because the covered Apple APIs do not provide a reliable
cross-platform RAM field.

## CLI: `devices collect`

The built-in CLI help includes this workflow, the command bundle, observed field
examples, permissions and result-inspection steps:

```sh
dmctl devices --help
dmctl devices collect --help
```

`dmctl devices collect DEVICE_ID` requests an asynchronous native inventory refresh
for an existing device record. `DEVICE_ID` is the local inventory record ID returned
by `devices list`, not an Apple resource ID, serial number or enrollment UDID.

```sh
dmctl devices list
dmctl devices collect DEVICE_ID
```

An example successful response is:

```json
{"queued":5}
```

This is the number of commands queued, not a device response or a count of returned
fields. The count can vary with eligible enrollments, platform support and queue
deduplication. A cloud-only record with no local enrollment cannot supply native
inventory and may queue zero commands. Collection requires `manageInventory`.

### Relationship to a device check-in

1. The server queues supported inventory commands through its ordinary MDM queue.
2. When commands are queued and APNs is configured, the server asks APNs to wake the device.
3. The device contacts the MDM command endpoint, retrieves commands and sends responses.
4. Acknowledged responses update the persistent device record, query indexes and reports.

This triggers the normal MDM command-polling cycle. In Apple's protocol terminology,
it is not an enrollment check-in message such as `Authenticate` or `TokenUpdate`
sent to `CheckInURL`. It does not require an installed inventory agent.

The CLI returns after submission and the configured push attempt, without waiting for
device responses. APNs acceptance does not prove delivery or completion. An offline
device, or a deployment without APNs credentials, retains queued work until the device
contacts the server. If a push attempt fails, the request can return an error even
though commands were already queued; inspect the queue before retrying.

`devices collect` bypasses the normal native collection freshness interval, which
defaults to 24 hours, while retaining command deduplication. There is no collection
completion wait mode: the shared `--wait` flag applies to `axm accounts sync`, not
to `devices collect`. DDM status arrives through its separate subscription/report
mechanism; collecting native commands does not guarantee a new DDM report.

### Commands and available information

| Native command | Collected information |
| --- | --- |
| `DeviceInformation` | Every query allowed by generated platform/version/channel metadata, covering identity, hardware, OS, storage and management/security indicators |
| `SecurityInfo` | Supported detailed security state, including FileVault information |
| `ProfileList` | Installed configuration profiles and returned payload metadata |
| `InstalledApplicationList` | Installed applications and returned application metadata |
| `CertificateList` | Installed certificates, certificate data and identity flags |

Unknown platforms start with a bootstrap `DeviceInformation` query before expanded
collection. A supported command can still fail because the installed MDM enrollment
does not grant its required device-side access rights. Server administrative
authorization and enrollment access rights are separate requirements.

The enrolled macOS 26.6.2 VM used for live validation returned the following 33
top-level `DeviceInformation` query-response fields. This is an observed example,
not a fixed schema or a promise that every Mac returns every field.

| Group | Observed Apple response fields |
| --- | --- |
| Identity | `SerialNumber`, `UDID`, `ProvisioningUDID`, `DeviceName`, `HostName`, `LocalHostName` |
| Hardware | `Model`, `ModelName`, `ModelNumber`, `ProductName`, `IsAppleSilicon`, `HasBattery`, `BatteryLevel`, `SupportsLOMDevice`, `SupportsiOSAppInstalls` |
| Storage | `DeviceCapacity`, `AvailableDeviceCapacity` |
| OS | `OSVersion`, `BuildVersion`, `SupplementalBuildVersion`, `TimeZone`, `SoftwareUpdateDeviceID` |
| Management | `IsSupervised`, `AwaitingConfiguration`, `ActiveManagedUsers`, `MDMOptions` |
| Security/recovery | `SystemIntegrityProtectionEnabled`, `IsActivationLockEnabled`, `IsActivationLockSupported`, `PINRequiredForDeviceLock`, `PINRequiredForEraseDevice`, `EACSPreflight` |
| Updates | `OSUpdateSettings` |

Its `OSUpdateSettings` contained `AutoCheckEnabled`, `AutomaticAppInstallationEnabled`,
`AutomaticOSInstallationEnabled`, `AutomaticSecurityUpdatesEnabled`,
`BackgroundDownloadEnabled`, `CatalogURL`, `IsDefaultCatalog` and `PreviousScanDate`.
The VM also returned one profile and five certificates. `SecurityInfo` and
`InstalledApplicationList` returned error `12007` with an explicit insufficient-access-rights
message. Wi-Fi, Bluetooth and Ethernet MAC addresses were not returned in this run;
no real IMEI, MEID or EID was established for the virtual Mac.

### Read results and verify freshness

```sh
dmctl devices get DEVICE_ID
dmctl devices get DEVICE_ID --raw
dmctl devices fields --raw
dmctl commands list device ENROLLMENT_ID
```

Use the record's `enrollment_id` for `ENROLLMENT_ID` in the command-queue example;
it is distinct from the inventory record ID. Queue inspection requires the relevant
command-read permission. Check command status and completion time, then each inventory
source's observation/attempt timestamps and error metadata. A successful `devices get`
can still contain retained older evidence. Failed attempts do not erase prior successes.

Ordinary record reads require `readInventory` and return reviewed normalized fields.
`--raw` requires `readRawInventory` and exposes complete stored evidence and additional
source-qualified fields, for example
`mdm.DeviceInformation.QueryResponses.SystemIntegrityProtectionEnabled`.
The authenticated enrollment identity separately supplies
`identity_certificate_expiry`; it is not inferred from arbitrary installed certificates.

To export only this record's reviewed fields, replace `DEVICE_ID` in the predicate:

```sh
dmctl inventory export --format csv \
  --where '[{"field":"id","operator":"eq","value":"DEVICE_ID"}]' \
  --columns serial_number,model,os_version,build_version,apple_silicon,filevault_enabled,identity_certificate_expiry
```

### Native collection versus AxM sync

| CLI operation | Source and purpose |
| --- | --- |
| `devices collect DEVICE_ID` | Ask locally enrolled devices for supported native MDM inventory |
| `axm accounts sync ACCOUNT_ID --wait` | Run and wait for that ABM/ASM account's Apple organization, assignment and coverage collection |
| `inventory sync` | Queue AxM sync jobs for all enabled Apple accounts |

Purchasing, warranty and AppleCare information come from AxM sync. Native collection
does not query those Apple organization APIs or populate missing cloud purchasing data.

## Configure a source

Grant the necessary explicit actions through existing Cedar policies:

| Action | Capability |
| --- | --- |
| `readInventory` | Records, public field discovery, account metadata, jobs, reports, exports and diagnostics |
| `readRawInventory` | Complete source payloads and unreviewed source-qualified fields |
| `manageInventory` | Sync, native collection, job controls, schedules and presets |
| `manageAxMAccounts` | Connection creation, credentials, updates, verification and deletion |

These actions do not inherit routine role grants. Raw inventory can contain installed
profile data and other sensitive responses. Ordinary records omit raw snapshots and
unreviewed fields. Credential reads are never part of the account API.

Create a local JSON file with an account and PEM key. Keep the key out of command-line
arguments:

```json
{
  "account": {
    "name": "Production ABM",
    "client_id": "BUSINESSAPI.example",
    "key_id": "example-key-id",
    "enabled": true,
    "device_ttl": "24h",
    "coverage_ttl": "168h",
    "coverage_budget": 0,
    "never_refetch_coverage": false,
    "dep_accounts": []
  },
  "private_key_pem": "-----BEGIN PRIVATE KEY-----\n...\n-----END PRIVATE KEY-----\n"
}
```

```sh
dmctl axm accounts create --file account.json
dmctl axm accounts list
dmctl axm accounts verify ACCOUNT_ID
dmctl axm accounts sync ACCOUNT_ID --wait
dmctl inventory sync
```

An enabled new connection queues initial discovery and gets a daily 02:00 UTC schedule.
Device detail/assignment freshness defaults to 24 hours; coverage to seven days.
`coverage_budget` limits coverage-device attempts per run; zero is unlimited. A subsequent
run skips still-fresh successes and continues with remaining devices. `--force` bypasses
freshness and the successful-coverage opt-out, but retains the per-run budget.
Organization enumeration runs on each sync to reconcile membership.

`dmctl axm accounts update ACCOUNT_ID --file account.json` requires the current
`account.revision`. Changing the connection identity requires a replacement key and
fences work using an older revision. Each worker builds an independently authenticated
client; tokens are never shared between accounts. Legacy `DM_AXM_*` configuration imports
once as the `default` account. Existing legacy AxM routes remain available.

```sh
dmctl inventory jobs list
dmctl inventory jobs get JOB_ID
dmctl inventory jobs pause JOB_ID
dmctl inventory jobs resume JOB_ID
dmctl inventory jobs cancel JOB_ID
dmctl inventory jobs cancel-all
dmctl axm accounts delete ACCOUNT_ID --revision REVISION
```

The queue is serial across accounts and server processes. Page records and resume
checkpoints commit together. A process restart reclaims expired work; pause/resume keeps
the checkpoint. Cancellation and account changes fence subsequent commits. In-flight
HTTP operations have a 75-second bound, below the two-minute lease, and may finish before
observing a stop. Runs distinguish `success`, `partial`, `failed`, and `cancelled`, with
`queued`, `running`, and `paused` as nonterminal states. Finished job metadata is retained
for 30 days by default. Records and disconnected-source history remain.

## Schedules, filtering and exports

```sh
dmctl inventory schedules set ACCOUNT_ID --every 6 --unit hours --zone Europe/London
dmctl inventory schedules set ACCOUNT_ID --cron '0 2 * * *' --zone UTC
dmctl inventory schedules set ACCOUNT_ID --cron '0 2 * * *' --disabled
dmctl inventory schedules list

dmctl devices list --focus apple --account ACCOUNT_ID
dmctl devices list --where '[{"field":"imei","operator":"contains","value":"123456789"}]'
dmctl devices list --where '[{"field":"migration_capable","operator":"eq","value":false}]'
dmctl devices get DEVICE_ID
dmctl devices get DEVICE_ID --raw
dmctl devices fields --raw
dmctl devices collect DEVICE_ID

dmctl inventory reports --focus combined
dmctl inventory export --format csv --columns serial_number,model,imei,ethernet_mac_addresses
dmctl inventory export --format ndjson --raw
dmctl inventory diagnostics
```

The repeat builder supports minutes, hours, days, weeks and months. Calendar repeats
keep the anchor's local wall time; short months use their last day. Schedule JSON can
supply an explicit `anchor`. Raw cron uses five numeric fields, lists, ranges and steps.
Time zones are IANA names. Missed runs coalesce instead of replaying every missed tick.
Schedules run in the server; a CLI process need not remain open.

Lists, reports and exports share `account`, `source`, `focus`, `search`, `where`, `cursor`
and `as_of`. Conditions are ANDed and support `eq`, array `contains`, `exists`, `lt`,
`lte`, `gt`, `gte`. Account and Apple/managed focus select source-specific projections.
Search examines public fields. Exact equality and array membership use persisted indexes;
other predicates scan bounded pages. `as_of` fixes time-dependent coverage calculations,
not a historical database snapshot.

Reports include model/product families, purchasing source, coverage, assignment,
migration, source overlap, enrollment, FileVault, check-in freshness, Apple Silicon,
expiry windows and year added. OS major-version currency is relative to the newest
reported major in each OS family within the source scope, never a hardcoded release.

Bulk exports stream to the CLI without its normal buffered-response size limit.
Raw exports capture an authorized access audit event before the first byte and record
the delivery outcome afterward. An interrupted stream causes a nonzero CLI exit.
Individual raw record/list responses remain bounded at 32 MiB; use pagination for lists.

CSV columns retain requested order and encode nested values as JSON cells. Network and
cellular identifiers are excluded from the default column set. CSV formula-leading values
are escaped. JSON/NDJSON raw exports retain complete stored source evidence. Save reusable
ordered columns and filters with `dmctl inventory presets save NAME --file preset.json`;
list them with `dmctl inventory presets`. Presets contain `name`, `format`, `columns` and
`query`. The preexisting enrollment export/import format is unchanged.

Diagnostics emit deliberately limited NDJSON summaries with consistent `<device N>`
labels within the bundle. They do not copy serials, raw logs, hosts, payloads, keys or
credentials into the output.

## Persistence and operation

In-memory applications use `inventory.NewMemory`. The reference server's SQLite,
PostgreSQL and MySQL implementations use the existing database and storage keyring.
`inventory_entries.payload` is authenticated against its document key. Source snapshots,
indexes and progress share transactions; SQL native projections also join protocol/event
transactions. In-memory stores have independent transaction boundaries.

The inventory migration set and encrypted-column binding are registered with server
backup/restore. External SQL integration tests create isolated random schemas/databases.
The PostgreSQL test user needs schema creation permission; the MySQL test user needs
privileges on `inventory_test_%` databases. CI and `scripts/testdb.sh up` configure the
MySQL grant on their disposable test services. Production connections must not be used
as test DSNs.

| Setting | Default |
| --- | --- |
| `DM_INVENTORY_NATIVE_INTERVAL` | `24h` between native collection attempts |
| `DM_INVENTORY_JOB_RETENTION` | `720h` for completed inventory jobs |

Programmatic configuration exposes `Config.InventoryNativeInterval` and
`Config.InventoryJobRetention`. A library application composes `inventory.Repository`
with its chosen `Backend`, and runs `inventory.Syncer` with an optional `ClientFactory`.
All normalized values retain source provenance; complete original AxM resources are
also available through the AxM resource types' `Raw` field.

The implementation was informed by [AxMJamfSync at the reviewed commit](https://github.com/karthikeyan-mac/AxMJamfSync/tree/9a7af2b54f7d891cfbf1d5ed326cd429fe72cf32),
[Apple organization-device attributes](https://developer.apple.com/documentation/applebusinessapi/orgdevice/attributes-data.dictionary),
[AppleCare coverage](https://developer.apple.com/documentation/applebusinessapi/get-all-applecare-coverage-for-an-orgdevice)
and [Apple built-in MDM inventory](https://developer.apple.com/documentation/applebusinessapi/get-apple-mdm-enrolled-devices).
Tests use local simulated Apple services; they do not establish production-account
permissions or guarantee which optional fields a particular Apple organization will return.
