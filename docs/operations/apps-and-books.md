# Apps and Books licensing

[`appsbooks`](../../devicemanagement/appleplatformservices/appsbooks/doc.go) implements
Apple's version 2 licensing API for device/user apps and user books. The caller
can use optional atomic location and licensing-job persistence; notification hosting and installation
remain caller-owned. Catalog metadata and
subscriptions are outside this client.

## Location setup

Create one shared `Client` per location with the downloaded base64 `sToken`, your
persisted MDM ID and, when known, the expected library UID. The original sToken is
the bearer credential. The decoded `token` member is not used as the HTTP token.
Replace the client when replacing credentials. Do not log configuration values.

Read `ClientConfig`, then explicitly call `SetClientConfig` to claim an unclaimed
location or configure this MDM's notifications. Another MDM ID returns
`ErrOwnership`. Licensing mutations require an already claimed location and check
configuration before writing. Every authenticated response checks `mdmInfo` when
present and the expected `uId`. Apple has no compare-and-swap ownership operation;
coordinate any administrative transfer between MDM services.

`ServiceConfig` caches dynamic URLs and limits for at most five minutes, refreshing
on the next call. The client bounds requests by current asset, target and user
limits and spaces HTTP requests by the location's rate limit. Separate processes
must coordinate their aggregate rate. Discovered credentialed endpoints must stay
on the configured HTTPS origin; redirects are not followed.

## Atomic location workflows

`NewWorkflow` composes the existing client with caller-owned `state.Store`; it
introduces no inventory dependency. Supply one stable `Config.MDMID`, shared
transport/clock options and empty `SToken`/`UID`. Share the workflow within a
process; processes still coordinate Apple's aggregate per-location request rate.
The server's sealed `statestore` implements this existing atomic contract. Protect
its keyring and backups; memory state is suitable for tests and loses all state
on restart. See the [compiled workflow example](../../devicemanagement/appleplatformservices/appsbooks/example_test.go).

`Register(ctx, id, revision, sToken)` explicitly creates a caller-named location
with revision zero or replaces its token at the current revision. It probes
`ClientConfig` through a GET before committing anything. Apple's response `uId`
is the immutable library identity; the decoded sToken's `token` is credential
material, not that library UID. A replacement must match the persisted UID and
MDM identity. Another MDM's `mdmInfo.id`, a wrong UID, expiry or a revision race
rejects the replacement and retains the prior token and evidence. The store binds
one UID to one local location, preventing aliases from sharing credentials.
Ownership is never acquired by registration or reads. These checks follow
Apple's [authentication and ownership contract](https://developer.apple.com/documentation/devicemanagement/getting-started-with-the-management-api).

Safe metadata, separate sToken credentials and sensitive source evidence occupy
distinct records. `Load` returns safe `Location` metadata, including revision,
credential revision, token expiration and last observed `OwnerID` (empty means
unclaimed). `LoadEvidence` explicitly returns privileged source data: raw client
configuration may contain notification credentials, and user pages contain
personal data. Apply separate authorization to this operation and source exports;
ordinary location inspection must use `Load`. Byte-slice page/configuration fields
preserve exact original JSON through persistence, including unknown fields,
null, false and large integers.

`Configure` is the explicit ownership/configuration write. It checks remote
identity and validates the request, then atomically reserves a pending revision
before sending the POST. Other configuration, refresh and token replacement
operations reject pending state. A failed or crashed call leaves this reservation
durable. After the original call/process has stopped, `Probe` performs a GET and
publishes the observed configuration, clearing pending state without replaying
any write. It also rejects foreign ownership. If the stored token has expired,
`RecoverConfiguration(ctx, id, revision, replacementSToken)` is the explicit recovery
operation: after the original call/process has stopped, it probes with the valid
replacement token and requires the same persisted Apple UID and MDM identity.
One atomic commit replaces credentials, retains the observed raw configuration,
clears pending state and advances the revision. Existing asset evidence and ordered
assignment/user journals and checkpoints remain intact. Failed probes, persistence
or stale revisions preserve the pending reservation, credentials and evidence.
Recovery requires an existing pending reservation and never replays a POST;
ordinary `Register` remains blocked while pending. The
[compiled recovery example](../../devicemanagement/appleplatformservices/appsbooks/example_test.go)
uses `Load` to obtain the revision after the caller stops the original operation.
Operators must stop old processes before recovery and coordinate transfers:
Apple offers no ownership CAS, and a local revision cannot fence a remote POST already in flight.

`Refresh` reads all assets without filters and assignments/users incrementally
from the last successful checkpoints. It retains the full asset page set and an
ordered journal of complete assignment/user batches; callers derive their own
views without guessed deletion semantics. The positive page cap applies to each
traversal. Empty pages and changing versions are valid; only `nextPageIndex`
controls continuation. As required by Apple's [pagination contract](https://developer.apple.com/documentation/devicemanagement/using-paginated-endpoints),
only the first page's version becomes the next incremental checkpoint. All three
source results and checkpoints commit together after complete traversal. Failure,
cancellation or changed local revision preserves previous successful evidence.
Every committed operation advances the revision, including replacement with a
previously used token, so old work cannot publish after a token ABA sequence.

The source journal grows with successful refreshes; applications choose refresh
schedules, access controls and retention for their deployment. Applications schedule explicit job submission and polling. Workflow starts no background
garoutines and makes no fleet policy or installation decisions.

## Storage verification

The published shared workflow contract runs against caller-owned memory and the
server's existing sealed SQL state adapter. SQLite, PostgreSQL and MySQL tests use
two independent connection pools for replica/configuration races and recovery.
They inspect physical ciphertext, reject plaintext in strict mode and reject
ciphertext copied to another row. Test-only key scopes isolate scenarios without
resetting shared databases. The [SQL composition tests](../../server/statestore/appsbooks_test.go)
and [integration tests](../../server/statestore/appsbooks_integration_test.go)
consume the root library version declared in `server/go.mod`.

From `server/`, with the isolated test database DSNs configured, run:

```sh
GOWORK=off go test -tags=integration ./statestore -run AppsBooks -race -count=1
```

The [Apple service clients diagram](../diagrams/apple-service-clients.html) shows
this optional caller composition. It does not configure Apps and Books workflows
in the reference-server runtime.

## Licensing and user lifecycle

Use `Assets`, `Assignments` and `Users` for single-page reads, or their `Walk…`
methods for bounded sequential traversal. Walkers return the first page's version
only after every page and callback succeeds. Preserve that version for the next
incremental assignments/users query. Assets do not accept `sinceVersionId`.
An empty page may still have a next page; changes to `totalPages` are not an end signal.

`AssetsQuery.Unlimited` is a `*bool`: `new(true)` asks Apple to include its
separate `unlimitedAssets` array, `new(false)` excludes unlimited assets, and nil
omits the filter. With omission Apple represents unlimited free assets in
`assets` using `totalCount: 2147483647`. This is a sentinel, not finite stock.
See Apple's [asset retrieval semantics](https://developer.apple.com/documentation/devicemanagement/managing-assets)
and [unlimited record schema](https://developer.apple.com/documentation/devicemanagement/unlimitedresponseasset).

`AssetsPage.Assets` contains `AssetRecord` values and `UnlimitedAssets` contains
typed `UnlimitedAssetRecord` values. Numeric pointers distinguish missing counts
from reported zero; unlimited records have no available, total or retired count.
Use `AssetRecord.Stock()` or walker `AssetInventory.Stock()` to distinguish
`StockUnknown`, `StockFinite` (including zero), and `StockUnlimited`. Consult
`LicenseStock.Available` only for `StockFinite`. Never subtract assigned counts
from the sentinel to calculate finite availability.

`WalkAssets` visits `assets` then `unlimitedAssets` on each page. Exactly one of
`AssetInventory.Record` or `Unlimited` is populated. A sentinel-bearing `Record`
still reports `StockUnlimited`. All filters persist across pages, starting at
page zero; context cancellation and callback errors stop traversal without a
checkpoint. Choose a positive maximum page count and a context deadline.
The [compiled traversal example](../../devicemanagement/appleplatformservices/appsbooks/example_test.go)
shows the stock distinction. Keep each observation with its location identity.

Each page and record exposes complete original JSON in `Raw`; retain it for
source export, unknown fields, explicit nulls and exact large integers. Marshalling
the typed value is a projection and does not reproduce unknown fields. Typed
field edits leave `Raw` unchanged. Availability, including unlimited inventory,
establishes no licensing, dispatch or installation outcome.

For a device app, check the purchased `AssetRecord` with
`CheckAssignment(true, false)` and associate its `adamId`/`pricingParam` with serial
numbers. For user apps or books, create or look up a user, then associate with
`clientUserId`. Registered users still need an associated Apple Account. Use
`InvitationURL` and your own communication flow for invitations; observe
`USER_ASSOCIATED` or query `Users` to establish association. `RequestUser` also
models Apple's `managedAppleId` field.

Books require user assignment and cannot be reclaimed or reassigned. Consult
`ProductType`, `DeviceAssignable` and `Revocable`; the service remains authoritative.
`Disassociate` removes selected revocable assignments. `Revoke` removes all
revocable assets from specified targets. `CreateUsers`, `UpdateUsers` and
`RetireUsers` implement the documented user lifecycle.

## Durable licensing jobs

`Workflow.CreateJob` persists one explicit `JobRequest` and caller-chosen job ID at
an expected location revision. It performs no Apple request. Reusing the ID is
idempotent for an identical request and revision; changing the request is rejected.
Supported operations are association, disassociation, revocation of revocable
assets, user creation, user updates and user retirement. Supply a positive
`MaxPages` for bounded preflight reads. `LoadJob` and cursor-based `Jobs` return
private intent, outcomes, worker state and source evidence; authorize these
separately from safe location metadata. No record has an automatic TTL.

`SubmitJob` reserves a durable worker fence and location lease before HTTP. It
reads fresh location ownership, asset restrictions and active user records before
issuing the explicit POST once. Books require user targets and reject explicit
disassociation; `Revoke` only removes Apple's revocable assets. Retired historical
rows do not hide a recreated active user. Missing/retired active targets fail
preflight. Caller-provided `clientUserId` is never derived from a device serial.
The job retains raw preflight configuration and complete asset/user pages,
accepted-event JSON, aggregate status JSON and callback evidence, including
unknown fields and exact numbers. These can contain personal data and receiver
credentials; protect them like other privileged source evidence.

A queued job whose captured location revision changes cannot submit. While a
submission or polling lease is reserved, location writers and other jobs cannot
start conflicting work. A successfully accepted event releases the reservation,
allowing validated token renewal and other explicit jobs. HTTP runs outside all
state transactions. Leases use the store's authoritative clock; select a duration
long enough for bounded HTTP work (maximum one hour). Expiry fences publication
and requires explicit recovery; it never makes a submitted POST eligible again.

A crash after POST but before saving its response leaves `submitting` state; a
failed call leaves `uncertain` state. Inspect these through `LoadJob` after restart.
Stop the original call/process before invoking `ReconcileJob`. Supply an observed
event ID to bind it to the original request, or keep the job uncertain while
investigating assignments/users through read-only endpoints and retained callbacks.
Apple exposes event status by ID, not an API for discovering an unknown submission
by a local job ID. Event binding is therefore an explicit operator assertion and
subsequent responses must match the requested event type and counts.
An uncertain job continues blocking other submissions at the location.
Abandonment requires a nonempty operator note and acknowledges unresolved remote
state; it neither undoes Apple work nor resets the job to queued. Investigate
remote state before creating a new explicit request. Local fences cannot cancel
an already-started remote request, and Apple provides no ownership CAS.

If expiry blocks an uncertain/crashed submission, stop the original worker and
call `RecoverLicensingToken` with the location revision, job ID and replacement
sToken. A GET validates the same UID and MDM ownership. One transaction renews
credentials and fences the old worker while preserving request, event and target
evidence. Failed recovery retains everything. The job remains reconciliation-only;
no POST is replayed. Accepted jobs permit ordinary `Register` renewal; an expired
polling lease is released through explicit `ReconcileJob` after stopping its worker.
See the [compiled examples](../../devicemanagement/appleplatformservices/appsbooks/example_test.go).

## Completion, notifications and retries

An accepted event is separate from completed licensing, dispatch and installation.
`PollJob` starts its fallback at least five minutes after acceptance and spaces
subsequent polls by at least 30 seconds. Applications own scheduling and deadlines.
PENDING remains inspectable; after ten minutes investigate and explicitly decide
what to do. Workflow never automatically resubmits. This applies Apple's
[notification and polling guidance](https://developer.apple.com/documentation/devicemanagement/subscribing-to-notifications)
with a conservative project policy for uncertain remote mutations.

Whole-job success requires COMPLETE, the matching event type, matching positive
requested/completed counts, the job's expected task count and no observed failures.
Revoke-all has no known task count before Apple evaluates existing assignments;
it still requires positive matching aggregate counts. Partial/mismatched COMPLETE
is a failed job. `Status.Successful` alone does not correlate a status to job intent.
Notification batches record unique per-target observations but never establish
whole-event completion. Unreported targets remain unknown even when Apple's
aggregate completion succeeds. Source status and available target evidence remain
separate. Late failure evidence can downgrade success; older success cannot erase
failure. This prevents duplicate/out-of-order batches from duplicating outcomes.

Host an HTTPS receiver and call `ReceiveNotification` with a dedicated per-location
receiver token. It authenticates bearer credentials and the persisted UID before
processing. A nil return means the receipt, deduplication and matching job changes
committed atomically; **return HTTP 2xx only then**. On error return non-2xx so Apple
can retry. The method does not close the request body. `(UID, notificationId)` is
scoped to the location; reuse with different bytes is rejected. Matching event/type
and target checks prevent unrelated callbacks from changing a job.

Handle `TEST_NOTIFICATION` through this same method while `Configure` is in flight;
Apple rejects configuration if its test delivery fails. Supply the receiver token
through the host's protected configuration independently of the sToken. Unknown
notification types, asset-count deltas and user-association callbacks are retained
without inferred inventory projections. `LoadNotification` exposes privileged
original evidence. Unknown-event callbacks remain retained and apply atomically
when the event ID is later bound, including after restart. Applications choose
retention and can inspect unsolicited receipts through their caller-owned state.
Apple deliveries are best effort, may repeat and split requests into batches of
at most 100 tasks. Always pair them with EventStatus polling, as required by
Apple's [notification contract](https://developer.apple.com/documentation/devicemanagement/subscribing-to-notifications).

The low-level `Client` remains usable without Workflow. Persist returned event IDs
and deduplication yourself when composing it directly. `ReadRetries` optionally
retries bounded GET failures for HTTP 429/5xx and honors Retry-After. POST is always
attempted once. Transport failures can leave unknown remote outcomes. Error strings
omit credentials and detailed private Apple failure messages; treat raw evidence
and errorInfo as sensitive. Fixture checks do not establish live Apple acceptance;
real-location and hardware validation remain required separately.

## Sources

The client models the field tables and examples in these Apple contracts:
[authentication and ownership](https://developer.apple.com/documentation/devicemanagement/getting-started-with-the-management-api),
[assets](https://developer.apple.com/documentation/devicemanagement/managing-assets),
[users](https://developer.apple.com/documentation/devicemanagement/managing-users),
[pagination](https://developer.apple.com/documentation/devicemanagement/using-paginated-endpoints),
[dynamic limits](https://developer.apple.com/documentation/devicemanagement/service-config),
[client configuration](https://developer.apple.com/documentation/devicemanagement/clientconfigrequest),
[event status](https://developer.apple.com/documentation/devicemanagement/statusresponse),
and [notifications](https://developer.apple.com/documentation/devicemanagement/subscribing-to-notifications).
Tests use local HTTPS responses based on those contracts; no live Apple credentials
or Apple-service acceptance is claimed.
