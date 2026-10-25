# Apps and Books licensing

[`appsbooks`](../../devicemanagement/appleplatformservices/appsbooks/doc.go) implements
Apple's version 2 licensing API for device/user apps and user books. The caller
can use optional atomic location persistence; notification hosting and installation
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
schedules, access controls and retention for their deployment. No background
jobs, licensing mutations, installation or policy decisions are added by Workflow.

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

## Completion, notifications and retries

Persist each mutation's returned `EventID` with the location and requested tasks.
An accepted event is not completed licensing. `Status.Successful` requires COMPLETE,
matching positive requested/completed counts and no failures. Inspect partial
failure numbers and `errorInfo`; unknown fields inside errorInfo remain available.
The [compiled example](../../devicemanagement/appleplatformservices/appsbooks/example_test.go)
constructs an installation command only after licensing success.

Host an HTTPS notification endpoint and use `DecodeNotification` with a dedicated
shared token and expected UID. Handle `TEST_NOTIFICATION` before submitting its
configuration: Apple rejects configuration when the test cannot be delivered.
Select the typed payload using `Notification.Type`; unknown types remain raw.
Persist changes and deduplication records for `(UID, Notification.ID)` before
returning 2xx.
One SUCCESS batch does not prove completion of a larger event.

If notifications do not establish completion within five minutes, query EventStatus.
Poll PENDING no more frequently than every 30 seconds. If it remains pending after
ten minutes, reconcile and explicitly decide whether to resubmit. No background
scheduler or automatic mutation replay is hidden inside the client.

`ReadRetries` optionally enables bounded GET retries for HTTP 429/5xx, honoring
Retry-After as a minimum wait. POST is attempted once. Transport failures can mean
an unknown mutation outcome. Errors expose Apple numbers and retry timing while
ordinary error strings omit private response text; treat detailed error fields as
sensitive. Pass context deadlines to bound waits and network calls.

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
