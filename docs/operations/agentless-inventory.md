# Agentless device inventory

The `devicemanagement/inventory` package retains Apple Business Manager (ABM),
Apple School Manager (ASM), native MDM and declarative status as independent source
records. Each record addresses one source kind, account and opaque source identity.
Successful original history and failed refresh outcomes remain separate. Collection
and correlation require no inventory repository or installed agent.

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

All source JSON is retained, including unknown attributes, relationships, null,
false, zero, empty arrays and integer lexemes above 2^53. Original JSON bytes survive
persistence without compaction or string escaping changing the source representation.
Source field discovery exposes `data.<original path>` selectors within each selected resource. Scalar
and array representations remain unchanged; no presentation aliases or inferred unit
conversions are stored.

Coverage retains every plan and its original attributes. Successful empty coverage
is the original `[]`; permission failures, 404s and timeouts are separate outcomes.
Failed refreshes never replace an earlier successful result. Coverage summaries and
field equivalence are caller-owned interpretations of the independent evidence.

## Standalone source collection

Use `devicemanagement/appleplatformservices/axm/collection` when the application needs
Apple evidence without adopting inventory records. Configure the existing `axm.Client`
with the account's credentials and HTTP trust, then construct a `collection.Collector`.
A `collection.Request` selects `Devices`, `Detail`, `Assignment` or `Coverage`, a
caller-owned account ID and generation, and the returned opaque resource ID when
applicable. Follow-up detail, assignment and coverage streams are explicit caller
choices; the collector introduces no enrichment or assignment policy.

```go
collector := collection.Collector{Client: appleClient, MaxPages: 100000}
request := collection.Request{Stream: collection.Stream{
    AccountID: accountID, Generation: generation, Operation: collection.Devices,
}, Limit: 1000}
checkpoint, err := collector.Collect(ctx, request, savedCheckpoint, sink)
```

The sink implements `Commit(context.Context, collection.Batch) error`. In one local
transaction it compares `Before` against persisted state, writes `Resources`,
`Document`, `Outcome` and `After`, and commits. Each resource retains exact source JSON;
the list document additionally retains included resources and document extensions.
Failed outcomes carry safe classifications and HTTP status separately from successful
evidence. Preserve old successful evidence when recording an unsuccessful attempt.
Do not persist or log the in-memory `Outcome.Err` without reviewing sensitive content.

A new generation starts with an empty `collection.Checkpoint`. Resume an existing
stream using its committed checkpoint, including after constructing a new collector.
A failed commit does not advance the returned checkpoint. If a commit may have succeeded
before returning an error, reload authoritative state before retrying. Sinks must fence
stale writers and make already committed replays harmless. The caller owns concurrency,
leases, account/client matching, credentials, transactions and scheduling. Apple calls
occur before the sink transaction. Checkpoints contain source URLs and resource context;
protect them as operational data and never reuse them across accounts or generations.

`EnumerationComplete` occurs only on the successful final page of a devices or coverage
stream. An empty successful enumeration differs from permission failures, 404s,
authentication failures and transport errors. A successful `null` assignment means
unassigned; the collector returns 404 as a failed outcome for the caller to interpret.
Partial enumeration does not prove absence. Completed Apple pagination does not promise
a consistent snapshot. `MaxPages` bounds traversal across restarts, and repeated page
URLs are rejected. Call `Step` instead of `Collect` when the host schedules one request
at a time. Completed checkpoints perform no further requests.

Inventory device enumeration uses this collector and commits observations, complete
list documents and safe outcomes with each job checkpoint. Compatible checkpoints
resume; migration restarts incompatible checkpoints with a fresh generation.
Detail and assignment streams remain singular requests. Inventory stages coverage
pages in memory and publishes coverage only after complete traversal, so a failed page
retains the last successful coverage evidence and retries the resource on the next job.

## Standalone serial correlation and resolution

Use [`correlation.Correlate`](../../devicemanagement/correlation/correlation.go) with
one actual `collection.Resource` of type `orgDevices`, its account and observation
time, and successful native report history. The Apple serial comes from the actual
resource's `attributes.serialNumber`; the host supplies the serial and enrollment
method bound to its authenticated native protocol exchange. Serial is the sole
physical cross-source key. Opaque resource IDs and enrollment/channel IDs scope
provenance and addressing; they establish neither hardware identity nor authentication.
No serial is inferred from those IDs.

ADE, account-driven Device Enrollment and profile-based Device Enrollment can match.
User Enrollment, unknown methods and unauthenticated reports are excluded. Missing
serials remain unmatched; a serial mismatch is a retained identity conflict. Different
nonempty authenticated serials for the same source reject all its reports. Multiple
independent enrollments can match one resource. Correlation returns copies of originals,
matched reports and rejected reports with reasons; it does not merge stored records.

Optionally call `Correlation.Resolve` with explicit source-qualified field mappings.
Each mapping declares equivalent meaning and units; keep, for example, gigabytes and
bytes separate unless the host explicitly converts them before mapping and retains
its conversion evidence. There are no built-in field aliases or unit conversions.
The latest successful native value wins over Apple even if Apple refreshed later.
Field omission retains earlier report evidence. False, zero, empty arrays and null
are values; an unreported mapped field has no selected value. Purchasing and coverage
facts can remain Apple-only inputs. Ties follow mapping order, then report order.
All candidates, source fields, report times and disagreements remain inspectable,
including historical native values. Numeric lexemes retain their precision.

See the [executable example](../../devicemanagement/correlation/example_test.go).
The host owns authentication, outcomes, retained report history and persistence.
Failed attempts must remain separate from successful reports. The correlation and
resolution APIs require no inventory repository and make no fleet-policy decision.
Inventory's explicit `Repository.Link` composes this correlator using retained
original history and authenticated enrollment provenance. It retains two source
identities without combining records. Optional resolution stays caller-owned.

## Records and native collection

Each `ResourceRecord` retains its original source reference and latest successful
snapshot. Successful observations, original migrated envelopes, list documents and
failed outcomes have independent history. A source update never joins records by
serial, creates a provisional alias or ranks source fields. Physical relationships
are explicit checked serial links. Every newly retained original, including a delayed
report that leaves the latest snapshot unchanged, invalidates affected links. Exact
acknowledged replays preserve checked links. Absence and disconnection invalidate links;
affected relationships require a new explicit correlation.

Native history retains partial reports and their reported per-item times. Omission
leaves prior successful observations available to an explicit resolver; storage does
not fabricate a replacement full report. The reference server records the profile
issuance flow and carries it into issued certificate evidence. Only fresh authenticated
check-ins, acknowledged command receipts and validated status receipts attach the
pinned certificate's recorded ADE, account-driven Device Enrollment, profile-based
Device Enrollment or User Enrollment provenance. The service supplies the original
successful Authenticate receipt separately from its connection-state observer.
`mdm.identity` retains that receipt's exact plist and only its reported fields,
including unknown members. An accepted replacement-candidate Authenticate is retained
by the controlled replacement handshake while the old pin remains active; it projects
no candidate identity or certificate inventory. TokenUpdate and command acknowledgement
commit the new pin before fresh receipts can qualify candidate hardware facts.
Neither an Idle poll nor a command connection authenticates
older stored device fields. A same-certificate Authenticate may leave operational
`DeviceInfo` unchanged; inventory still records only the current receipt, at its receipt
time. Omitted older unverified fields remain unchecked; previously qualified native
fields remain in history for explicit resolution. A native serial comes from an actual
Authenticate `SerialNumber` or DeviceInformation `QueryResponses.SerialNumber` receipt,
or a previously retained authenticated report of that serial for the same enrollment.
A serial-omitting command or DDM receipt cannot borrow an unchecked `DeviceInfo` serial
merely because a newer certificate's method is known. Such originals stay outside
hardware correlation; a later serial receipt never retroactively qualifies them.
Subsequent fresh receipts may use the genuine retained binding. Stored device state,
channels and account names supply neither a serial proof nor an enrollment method.

Background sweeps retain historical enrollment, acknowledged-command and effective
status evidence without consulting the current certificate for report authentication.
Exact originals and receipt times already retained are reused atomically on
backfill, preserving their captured fields, per-item times, provenance and checked links. Missing historical authentication or method evidence stays incomplete even after
new certificate issuance. A genuinely fresh authenticated receipt can establish its
own provenance with unchanged reported values, including an identical injected receipt
time; fresh receipts retain the library's normal observation/history semantics.
Native evidence is never automatically linked to hardware. User Enrollment cannot
establish a serial-based hardware relationship.

Account removal disconnects its sources without disabling enrollment or deleting
original evidence. An absent Apple resource is marked only after complete enumeration
and a confirming individual lookup. Organization membership, physical correlation
and protocol enrollment are separate facts. Serialless protocol enrollment remains
usable independently.

The supervised native worker discovers new enrollments within its one-minute sweep,
backfills existing acknowledged results and effective DDM status, and requests:

- `DeviceInformation`: every query allowed by the generated platform/version/channel metadata;
  an unknown platform starts with a bootstrap query, followed by the expanded query once known.
- `SecurityInfo`, `ProfileList`, `InstalledApplicationList`, and `CertificateList`, when supported.
- DDM status through the existing capability-aware subscription mechanism, enabled by default
  by `DM_SUBSCRIPTIONS`. Setting it to false remains supported.

Only acknowledged responses for stored commands can update inventory. User-channel identities
remain independent of physical hardware relationships. Native requests use the ordinary command queue,
validation, deduplication and APNs notifier. Without APNs credentials, requests remain queued
until the device contacts the server. Authenticated enrollment certificates supply their
own expiry metadata; arbitrary installed certificates are not treated as enrollment identity.
No cross-platform RAM field or implicit field equivalence is introduced.

## Serial inspection and explicit enrichment

All paths below are relative to `/admin/v1`. `GET /inventory/serials/{serial}` and
`dmctl devices inspect SERIAL` find independent organization-device resources before
enrollment. After enrollment they also find native sources carrying that authenticated
serial. A matching serial in this inspection is a discovery result; only `links` are
checked relationships. Reads perform no correlation or resolution. Multiple Apple
accounts and enrollments remain separately addressed.

`GET /devices/{id}/evidence` returns source metadata and checked links. Its raw counterpart
`GET /inventory/raw/devices/{id}/evidence`, exposed by `devices evidence ID --raw`,
additionally returns successful `history`, separate classified `attempts`, opaque
`legacy_envelopes`, and base64 `original_json` byte arrays aligned with `history`.
Decoding each base64 entry recovers the byte-exact original, including whitespace.
`GET /inventory/raw/serials/{serial}` provides the same raw evidence for serial inspection.
All complete-source operations require `readRawInventory`; ordinary inspection requires
`readInventory`. Record IDs come from `devices list`; old fleet UUIDs have no lookup alias.

Create a checked relationship explicitly with `POST /inventory/correlations`, requiring
`manageInventory`. Its JSON request contains independent record IDs:

```json
{"apple":"APPLE_RECORD_ID","native":"NATIVE_RECORD_ID"}
```

The Apple record must contain an actual `orgDevices` resource. The native record must
carry authenticated eligible method and serial provenance. User Enrollment is excluded,
even when serials match. A new original, absence or disconnection invalidates the link;
repeat explicit correlation after a refresh. Correlation changes no source originals.
Use a native Authenticate (`mdm.identity`), command or DDM receipt record for the native
side. The separate `enrollment` lifecycle metadata is not a hardware report.

Resolve only current checked links with `POST /inventory/raw/devices/{apple_id}/resolve`.
This is a read operation requiring `readRawInventory`. Supply `mappings`, whose named
`inputs` contain `resource_id` (the independent record ID) and `path` (the exact source
field). Apple paths are relative to `attributes`; native command paths keep protocol
containers such as `QueryResponses.SerialNumber`. Each mapping asserts semantic and unit
equivalence; the server introduces no automatic aliases, unit conversion or fleet policy.
For example, comparing the two documented serial fields requires this explicit request:

```json
{"mappings":[{"name":"reported_serial","inputs":[
  {"resource_id":"APPLE_RECORD_ID","path":"serialNumber"},
  {"resource_id":"NATIVE_RECORD_ID","path":"QueryResponses.SerialNumber"}
]}]}
```

Results return `selected`, every `candidates` entry, and `disagrees`. Each value retains original
JSON, source reference, path, observation time and native provenance. Latest successful
native evidence wins regardless of a later ABM sync. Omitted values retain prior native
history; false, zero, null and empty arrays remain meaningful. DDM field times belong to
the retained individual item. Unlinked or rejected sources cannot enter resolution.
Arbitrary original paths and candidate values are restricted to raw authority, including
when a mapping's chosen result name looks like an ordinary metadata column.

```sh
dmctl devices inspect SERIAL
dmctl devices inspect SERIAL --raw
dmctl devices evidence NATIVE_RECORD_ID --raw
dmctl inventory correlate --file correlation.json
dmctl devices resolve APPLE_RECORD_ID --raw --file mappings.json
```

The [source inventory diagram](../diagrams/inventory-inspection.html) shows the independent
collection, checked relationship and explicit resolution boundaries.

## CLI: `devices collect`

The built-in CLI help includes this workflow, the command bundle, observed field
examples, permissions and result-inspection steps:

```sh
dmctl devices --help
dmctl devices collect --help
```

`dmctl devices collect DEVICE_ID` requests an asynchronous native inventory refresh
for an existing `enrollment` source record. `DEVICE_ID` is that source record's local
ID from `devices list --source enrollment`, not an Apple resource ID, serial or UDID.

```sh
dmctl devices list --source enrollment
dmctl devices collect DEVICE_ID
```

An example successful response is:

```json
{"queued":5}
```

This is the number of commands queued, not a device response or a count of returned
fields. The count can vary with eligible enrollments, platform support and queue
deduplication. An Apple source record cannot supply a native
enrollment and queues zero commands. Collection requires `manageInventory`.

### Relationship to a device check-in

1. The server queues supported inventory commands through its ordinary MDM queue.
2. When commands are queued and APNs is configured, the server asks APNs to wake the device.
3. The device contacts the MDM command endpoint, retrieves commands and sends responses.
4. Acknowledged responses append independent successful native source evidence.

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

Use the `enrollment` source snapshot's `enrollment_id` for `ENROLLMENT_ID` in the command-queue example;
it is distinct from the inventory record ID. Queue inspection requires the relevant
command-read permission. Check command status, completion time and each source's
observation time. Programmatic `Repository.Attempts` and `Repository.Outcomes` expose
separate failed refresh history; job metadata reports failure counts. A successful
`devices get` can still contain retained older evidence. Failed attempts do not erase
prior successes.

Ordinary record reads require `readInventory` and return resource metadata.
`--raw` requires `readRawInventory` and exposes complete source evidence and original
field paths, for example `data.QueryResponses.SystemIntegrityProtectionEnabled` within an
`mdm.DeviceInformation` source record.
The authenticated enrollment identity separately supplies
`identity_certificate_expiry`; it is not inferred from arbitrary installed certificates.

To export only this record's metadata, replace `DEVICE_ID` in the predicate:

```sh
dmctl inventory export --format csv \
  --where '[{"field":"meta.id","operator":"eq","value":"DEVICE_ID"}]' \
  --columns meta.id,meta.source,meta.account_id,meta.resource_id,meta.observed_at,meta.incomplete
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
| `readRawInventory` | Complete source payloads and original source fields |
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
dmctl devices list --raw --source axm.device --where '[{"field":"data.attributes.imei","operator":"contains","value":"123456789"}]'
dmctl devices list --raw --source axm.device --where '[{"field":"data.attributes.isMdmMigrationCapable","operator":"eq","value":false}]'
dmctl devices get DEVICE_ID
dmctl devices get DEVICE_ID --raw
dmctl devices fields --raw
dmctl devices collect DEVICE_ID

dmctl inventory reports --focus combined
dmctl inventory export --format csv --columns meta.id,meta.source,meta.account_id,meta.resource_id
dmctl inventory export --format ndjson --raw
dmctl inventory diagnostics
```

The repeat builder supports minutes, hours, days, weeks and months. Calendar repeats
keep the anchor's local wall time; short months use their last day. Schedule JSON can
supply an explicit `anchor`. Raw cron uses five numeric fields, lists, ranges and steps.
Time zones are IANA names. Missed runs coalesce instead of replaying every missed tick.
Schedules run in the server; a CLI process need not remain open.

Lists, reports and exports share `account`, `source`, `focus`, `search`, `where`,
`cursor` and `as_of`. Conditions are ANDed and support `eq`, array `contains`,
`exists`, `lt`, `lte`, `gt`, `gte`. Account and Apple/managed focus select independent
source records. Search examines source identities and kind. Predicates scan bounded
pages over original field paths or metadata. Numeric comparisons retain exact integer
precision. `as_of` records the query time and is not a historical database snapshot.
HTTP `next_cursor` binds continuation to the original account, source, focus, search
and conditions. Preserve those predicates; changing only page size is permitted.
Malformed, old bare record/UUID cursors and continuations from another query return 400.
Malformed query pairs, including unescaped semicolons and invalid percent escapes,
also return 400 before any filters execute. CLI values are encoded as literal values;
a semicolon within an encoded account ID never removes the account scope.
Unknown parameters such as retired `serial`, `uuid` and normalized fleet filters also
return 400; use the serial inspection path and explicit source selectors.

Selectors are explicitly namespaced. `meta.id` is the repository's resource identity;
`meta.resource_id` is the opaque source address; `data.id` is the original source field
named `id`. Metadata and source members never shadow one another or imply equivalent
meaning. Original field paths escape literal dots as `\.` and backslashes as `\\`.
Use, for example, `data.meta.id` for an original nested `meta.id` member and
`data.meta\.id` for an original literal key named `meta.id`. Unqualified names are
invalid. `devices fields --raw` discovers available selectors; metadata-only discovery
excludes `data.*`. Original-data filters and exports require raw authority.

```sh
dmctl inventory export --raw --format csv --columns meta.id,data.id
```

Reports count independent resources by source kind and completeness. They do not
project physical fleet overlap, infer field equivalence or apply source ranking.

Bulk exports stream to the CLI without its normal buffered-response size limit.
Raw exports capture an authorized access audit event before the first byte and record
the delivery outcome afterward. An interrupted stream causes a nonzero CLI exit.
Individual raw record/list responses remain bounded at 32 MiB; use pagination for lists.

CSV columns retain requested order and encode nested values as JSON cells. Network and
cellular identifiers are excluded from the default column set. CSV formula-leading values
are escaped. JSON/NDJSON raw exports retain complete stored source evidence. Save reusable
ordered columns and filters with `dmctl inventory presets save NAME --file preset.json`;
list them with `dmctl inventory presets`. Ordinary preset routes accept only metadata
conditions and columns, and exclude raw presets from reads. Use `--raw` for raw preset
reads and saves (`/inventory/raw/presets`); saving requires both `manageInventory` and
`readRawInventory`. Original-source filter literals never appear in ordinary preset reads.
Presets contain `name`, `format`, `columns` and
`query`. The preexisting enrollment export/import format is unchanged.

Diagnostics emit deliberately limited NDJSON summaries with consistent `<resource N>`
labels within the bundle. They do not copy serials, raw logs, hosts, payloads, keys or
credentials into the output.

## Persistence and operation

In-memory applications use `inventory.NewMemory`. The reference server's SQLite,
PostgreSQL and MySQL implementations use the existing database and storage keyring.
`inventory_entries.payload` is authenticated against its document key. Source snapshots,
original history, outcomes and progress share transactions; SQL observers also join protocol/event
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
Call `Repository.Migrate` before starting workers or observers. Migration extracts
only retained original source evidence and preserves unknown source-envelope metadata.
It never reconstructs source facts or serials from normalized aliases. Missing originals,
invalid organization-device resources and missing authenticated enrollment provenance
stay incomplete and refreshable. Accounts, credentials, schedules, enrollment, queues
and audit state are preserved. Old worker owners are fenced, incompatible cursors restart
under a new generation, and the previous outbound lease deadline prevents overlapping
Apple requests. Migration and its model-fence activation commit together; interruption
rolls back and retry is safe.

SQL schema installation preinstalls an inactive retired-key write fence. MySQL installs
missing DDL under an inventory-specific advisory lock, retaining an active fence across
interruption or concurrent opens. Its migration role needs trigger creation authority
under the server's binary logging policy; the application role does not need `SUPER`.
The disposable `testdb` and CI services configure that test-only policy explicitly.
Complete original resources also remain available through AxM resource types' `Raw` field.

The implementation was informed by [AxMJamfSync at the reviewed commit](https://github.com/karthikeyan-mac/AxMJamfSync/tree/9a7af2b54f7d891cfbf1d5ed326cd429fe72cf32),
[Apple organization-device attributes](https://developer.apple.com/documentation/applebusinessapi/orgdevice/attributes-data.dictionary),
[AppleCare coverage](https://developer.apple.com/documentation/applebusinessapi/get-all-applecare-coverage-for-an-orgdevice)
and [Apple built-in MDM inventory](https://developer.apple.com/documentation/applebusinessapi/get-apple-mdm-enrolled-devices).
Tests use local simulated Apple services; they do not establish production-account
permissions or guarantee which optional fields a particular Apple organization will return.

Explicit assignment mutations use the separate [ABM assignment workflow](abm-assignments.md).
Organization synchronization remains read-only, including its assignment evidence stage.
