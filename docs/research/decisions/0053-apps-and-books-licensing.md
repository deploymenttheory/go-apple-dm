# 0053: Apps and Books licensing

## Context

Apple licenses apps and books per content-token location. Mutations complete
asynchronously, and a location may already belong to another MDM service.

## Decision

The root-library `appsbooks.Client` owns HTTP encoding, location identity checks,
dynamic limits, pagination, user lifecycle and notification decoding. Use one
shared client per location and replace it when replacing credentials. The original
base64 sToken is the bearer credential; its decoded token field is not substituted.
Credentialed endpoints stay on the configured HTTPS origin and refuse redirects.

Configuration reads do not claim ownership. `SetClientConfig` is explicit, rejects
another MDM ID and requires a working notification receiver when configuring one.
Licensing mutations check claimed ownership. Responses check `mdmInfo` when present
and the expected library UID. Apple's API supplies no ownership compare-and-swap;
operators coordinate transfers between services.

Service configuration is cached for at most five minutes and refreshed on demand.
The client enforces current batch limits and spaces requests by location rate.
Separate processes must coordinate their aggregate traffic. Page walkers preserve
the first page's version only after all pages and callbacks succeed. Incremental
queries apply to assignments and users, not assets.

`AssetsQuery.Unlimited` retains three query states. Apple returns a separate typed
`unlimitedAssets` array when true, excludes unlimited assets when false, and uses
`totalCount: 2147483647` in `assets` when omitted. `AssetRecord.Stock` interprets
that exact sentinel as unlimited inventory. Optional numeric pointers retain
unreported counts without inventing zero availability. `UnlimitedAssetRecord`
omits available, total and retired counts. Both record types and `AssetsPage`
retain complete original JSON in `Raw`, including unknown fields and explicit
nulls. Typed edits do not modify that original evidence.

`WalkAssets` visits both arrays through `AssetInventory`, preserving their source
representation. It retains all filters, starts at page zero, follows empty pages,
and bounds traversal with a positive page cap and caller context. No checkpoint
is returned on request, pagination, cancellation or callback failure. Unlimited
inventory does not establish completed licensing, dispatch or installation.

Optional `Workflow` composes this HTTP client over the existing caller-owned
atomic `state.Store`, independently of inventory and server packages. One
persisted location binds a caller ID, immutable Apple `uId` and stable MDM ID.
Metadata, sToken credentials and sensitive source evidence are separate records.
`Load` exposes safe metadata; `LoadEvidence` is privileged because raw client
configuration can contain notification credentials. Token registration/replacement
uses read-only identity probes, rejects foreign ownership and duplicate local UID
aliases, and commits only at the expected monotonic revision.

`Configure` checks identity and durably reserves a pending revision before its
explicit POST. An uncertain or crashed operation blocks later configuration,
refresh and replacement. Once the original call/process has stopped, `Probe`
reconciles observed configuration through a GET without claiming ownership or
replaying a mutation. If the stored credential expires while pending,
`RecoverConfiguration` explicitly accepts a replacement sToken after the original
call/process has stopped. It validates the same Apple UID and stable MDM ownership
through a GET, then atomically replaces credentials, reconciles pending state and
advances the revision. Failed recovery preserves the prior pending reservation,
credentials, evidence and checkpoints. Ordinary registration cannot bypass pending
state. Both recovery paths retain the asset evidence and ordered assignment/user
journals, and neither replays a configuration POST. Apple has no ownership CAS; local fencing cannot revoke a
remote POST already in flight. Operators coordinate ownership transfers and stop
old processes before recovery.

`Refresh` fully reads unfiltered assets and incrementally reads assignments/users.
Only complete traversals publish evidence and the first-page checkpoints, in one
atomic commit. Assets retain the latest full page set; assignment/user source
batches form an ordered full-plus-incremental journal. Exact page/configuration
bytes preserve unknown fields, null, false and numeric tokens. No inferred
inventory or deletion projection is imposed. Failed refreshes retain successful
evidence/checkpoints. Every writer locks the location metadata key; UID binding
also locks its immutable claim key. Monotonic revisions fence all older local
work, including token ABA and cross-process races. No Apple call runs inside a
local transaction. Records have no TTL; applications own retention and schedules.

Workflow persists explicit licensing/user job intent, its location revision,
accepted event ID, worker fence, authoritative lease and per-target notification
observations. Per-location reservations serialize submissions and polling with
location writers. Expired submission leases never make a POST eligible again.
ReconcileJob requires a stopped original worker and an operator note; observed
event binding and abandonment are explicit decisions. Uncertain work continues
blocking submissions. RecoverLicensingToken validates a replacement through GET
and atomically renews credentials/fences workers while retaining uncertainty and
all evidence. Accepted jobs release reservations and permit ordinary token renewal.
Preflight uses fresh native asset capabilities and active users, preserving source
configuration/pages without an inventory dependency. Books reject device assignment
and explicit reclamation; revocation applies only to revocable assets.

ReceiveNotification authenticates receiver credentials and UID, then commits raw
receipts, deduplication and matching job changes before callers may acknowledge.
TEST_NOTIFICATION works while configuration is pending. Unknown-event receipts
apply when their event is explicitly bound; unknown notification types remain raw.
Callbacks verify event type and requested targets; distinct callback IDs never
duplicate target outcomes. Late failures downgrade success and cannot be erased
by older success. PollJob alone establishes aggregate completion after matching
operation/task-count checks; per-target evidence retains its separate availability.
Requests, raw event/status responses and notifications require privileged access.

Accepted mutations return event IDs. Completion requires COMPLETE status, matching
positive requested/completed counts and no failures. One successful notification
batch does not settle a larger event. Optional bounded GET retries cover 429/5xx
and honor Retry-After; POST is attempted once. A transport failure can leave an
unknown mutation outcome, requiring reconciliation before explicit resubmission.

The optional `applications/managedcontent.Workflow` connects explicit licensing
intent to caller-owned target and delivery adapters. `Assign`, `Install` and
`AssignAndInstall` atomically queue local intent without Apple or dispatch calls.
A request and its required audit capture can therefore share the existing state
transaction boundary. `Step` resolves the explicitly selected authenticated native
enrollment, validates platform/channel/version and native asset capabilities, and
requires fresh exact target assignment evidence. Installation of a previously
licensed asset does not require a synthetic historical job. When a licensing job
is supplied or created, complete aggregate success and applicability are additional
requirements; aggregate completion never replaces per-target assignment readback.

`ReadAsset` and `ConfirmAssignment` retain complete native page bytes and local
revision provenance. Confirmation checks fresh remote client ownership, requires
one assignment, requires Associated user state and rejects incomplete, duplicate,
foreign or version-inconsistent traversals. Token renewal or other location
revision changes fence in-flight proof. User licensing keeps Apple's clientUserId
separate from physical serial identity and remains usable with privacy-preserving
User Enrollment. A supplied serial must match the selected authenticated target.

Content operations reserve a native installation scope independently of optional
serial disclosure, location and licensing aliases. After resolution they also bind
an authenticated physical serial/native-principal scope when available. Delivery
adapters atomically bind stable IDs to immutable payloads and targets and reject
conflicting MDM/DDM management paths. Typed `InstallApplication`, `InstallMedia`
and `AppManaged` payloads use generated support metadata. tvOS app delivery uses
MDM; declarative managed-app delivery is unavailable there. Books retain their
user/platform rules, including removal of InstallMedia on macOS 11 and later.

Before dispatch, intent and exact wire bytes are durable. A crash cannot replay a
submission automatically. Readback reconciliation fences expired workers; explicit
same-identity retry requires an operator note and an authoritative adapter guarantee
of atomic idempotency and ownership, even if an older call arrives later. A known
completed dispatch cannot be repeated after its queue row disappears. Ordinary
operation reads contain safe status; source pages, configuration, wire payloads
and raw device/adapter data require explicit privileged evidence access. Licensing,
queue dispatch, command acknowledgment, declaration publication and actual device
installation/version reports remain separate observations.

Explicit same-method reinstall/update intent can name a completed predecessor in
`Plan.PredecessorID`. Queuing remains local. Fresh authoritative terminal device
readback gates an atomic lane handoff that supersedes the predecessor and fences
its worker. MDM successors use new command IDs. DDM successors reuse the owned
configuration/activation IDs and carry an exact predecessor identity/digest for
the adapter's atomic native handoff; no second app configuration is introduced.
Pre-dispatch cancellation restores ownership with increasing lane epochs and
worker fences, preventing ABA. Uncertain/pending predecessors and method switches
remain blocked. Terminal device processing, current installed state and reported
app version are distinct: removal does not erase an earlier terminal outcome or
permanently prevent an explicit reinstall.

The reference server composes both SDK workflows over its existing sealed SQL
state and transaction/event boundary. `DM_APPSBOOKS_MDM_ID` enables dedicated
HTTP/CLI location, explicit licensing, content, recovery and private evidence
operations. New grants have no routine-group membership. Actual SDK state writes
and required metadata-only event capture commit together, including callback
receipts before HTTP 204. No remote call runs inside that local transaction.
Native admission pins the selected authenticated principal/certificate and its
explicit Apple-user binding; later rebinding cannot redirect retained intent.
The supervised content worker resumes local intent and accepted licensing polls;
uncertain remote work stays fenced for explicit reconciliation. Native adapters
atomically bind immutable dispatch identity/wire to the MDM queue or DDM
publication, retaining receipts after queue removal. Historical install processing
and current native installation/version remain distinct observations.

## Rationale

Location isolation prevents credential and ownership state from crossing accounts.
Explicit ownership writes and mutation retries avoid silently taking over a
location or duplicating work. Separate completion checks prevent installation
from racing unfinished licensing.

## Constraints

Callers can use Workflow for durable location and job state or persist these
contracts themselves around Client. They host the authenticated HTTPS receiver,
acknowledge only after ReceiveNotification succeeds and schedule polling/reconciliation.
Workflow waits five minutes for fallback polling and spaces subsequent queries by
at least 30 seconds. No scheduler or automatic POST replay runs in the background.
Registered users must associate an Apple Account. Books require user assignment
and cannot be reclaimed or reassigned. Installation remains outside the HTTP licensing client; optional managed-content
composition uses caller-owned native adapters. Catalog metadata and subscriptions
are outside these workflows. Fixture tests do not establish live Apple
service acceptance.

## Verification

The shared memory/sealed-SQL workflow contract covers location isolation, immutable
identity, token renewal/expiry, restart, pending configuration, token ABA and
concurrent revision fencing, pagination failure and preservation of successful
evidence. Atomic-store fault injection verifies rollback at persistence boundaries, including
notification receipts and outcome deduplication. Shared job contracts cover partial
counts, restart, stale worker leases, uncertain responses, orphan/test callbacks,
replays, late failures, books, retired users and expired-token recovery.
The package's controlled HTTP tests cover token/location isolation, ownership,
dynamic limits, finite/missing/unlimited asset stock, sentinel and unknown-field
fidelity, bounded/cancelled pagination, user lifecycle, partial outcomes, notification
authentication, retries and cancellation. The compiled example waits for successful
licensing before constructing an installation command. See the
[operations guide](../../operations/apps-and-books.md) for polling intervals and
the [live checks](../../testing/lab.md#apple-management-feature-checks).

The maintained [acceptance procedures](../../operations/apps-and-books-acceptance.md)
separate read-only dedicated-token location evidence, explicitly selected free
revocable app licensing through SDK/reference jobs and authenticated callbacks,
and independent current native installation/version observations. Per-target
assignment readback and original-state cleanup are required independently of
aggregate completion. Uncertain runs retain a private immutable fence and their
absolute evidence locator. Preexisting assignments and externally prepared apps
are preserved. Fixture results remain simulated, with real-location and each
physical platform pending until designated resources are exercised.

## References

- [Managed content workflow](../../../devicemanagement/applications/managedcontent)
- [Install Application](https://developer.apple.com/documentation/devicemanagement/install-application-command)
- [Install Media](https://developer.apple.com/documentation/devicemanagement/install-media-command)
- [AppManaged](https://developer.apple.com/documentation/devicemanagement/appmanaged)

- [Client and tests](../../../devicemanagement/appleplatformservices/appsbooks)
- [Authentication and ownership](https://developer.apple.com/documentation/devicemanagement/getting-started-with-the-management-api)
- [Assets and assignment restrictions](https://developer.apple.com/documentation/devicemanagement/managing-assets)
- [Unlimited asset schema](https://developer.apple.com/documentation/devicemanagement/unlimitedresponseasset)
- [Users and association](https://developer.apple.com/documentation/devicemanagement/managing-users)
- [Pagination](https://developer.apple.com/documentation/devicemanagement/using-paginated-endpoints)
- [Dynamic service configuration](https://developer.apple.com/documentation/devicemanagement/service-config)
- [Notifications](https://developer.apple.com/documentation/devicemanagement/subscribing-to-notifications)
