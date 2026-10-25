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
replaying a mutation. Apple has no ownership CAS; local fencing cannot revoke a
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

Accepted mutations return event IDs. Completion requires COMPLETE status, matching
positive requested/completed counts and no failures. One successful notification
batch does not settle a larger event. Optional bounded GET retries cover 429/5xx
and honor Retry-After; POST is attempted once. A transport failure can leave an
unknown mutation outcome, requiring reconciliation before explicit resubmission.

## Rationale

Location isolation prevents credential and ownership state from crossing accounts.
Explicit ownership writes and mutation retries avoid silently taking over a
location or duplicating work. Separate completion checks prevent installation
from racing unfinished licensing.

## Constraints

Callers persist event IDs, tasks and notification deduplication records. They can
use Workflow for location credentials, source snapshots and incremental versions. They host the authenticated HTTPS receiver, process TEST_NOTIFICATION,
persist notifications before 2xx, and schedule EventStatus fallback and reconciliation.
Registered users must associate an Apple Account. Books require user assignment
and cannot be reclaimed or reassigned. Installation, catalog metadata and
subscriptions are outside this client. Fixture tests do not establish live Apple
service acceptance.

## Verification

The shared memory/sealed-SQL workflow contract covers location isolation, immutable
identity, token renewal/expiry, restart, pending configuration, token ABA and
concurrent revision fencing, pagination failure and preservation of successful
evidence. Atomic-store fault injection verifies rollback at persistence boundaries.
The package's controlled HTTP tests cover token/location isolation, ownership,
dynamic limits, finite/missing/unlimited asset stock, sentinel and unknown-field
fidelity, bounded/cancelled pagination, user lifecycle, partial outcomes, notification
authentication, retries and cancellation. The compiled example waits for successful
licensing before constructing an installation command. See the
[operations guide](../../operations/apps-and-books.md) for polling intervals and
the [live checks](../../testing/lab.md#apple-management-feature-checks).

## References

- [Client and tests](../../../devicemanagement/appleplatformservices/appsbooks)
- [Authentication and ownership](https://developer.apple.com/documentation/devicemanagement/getting-started-with-the-management-api)
- [Assets and assignment restrictions](https://developer.apple.com/documentation/devicemanagement/managing-assets)
- [Unlimited asset schema](https://developer.apple.com/documentation/devicemanagement/unlimitedresponseasset)
- [Users and association](https://developer.apple.com/documentation/devicemanagement/managing-users)
- [Pagination](https://developer.apple.com/documentation/devicemanagement/using-paginated-endpoints)
- [Dynamic service configuration](https://developer.apple.com/documentation/devicemanagement/service-config)
- [Notifications](https://developer.apple.com/documentation/devicemanagement/subscribing-to-notifications)
