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

## Explicit licensing and installation composition

[`managedcontent.Workflow`](../../devicemanagement/applications/managedcontent/doc.go)
uses caller-owned authenticated target and delivery adapters with the existing
`state.Store`. The [compiled example](../../devicemanagement/applications/managedcontent/example_test.go)
shows queued start and worker advancement. `Assign`, `Install` and
`AssignAndInstall` make no network or delivery calls. Commit their intent alongside
required local audit capture before scheduling `Step`.

Select the content, location, exact enrollment and delivery method explicitly.
Device licensing requires the selected authenticated device serial. User licensing
uses the associated Apple clientUserId; User Enrollment does not require disclosure
of a physical serial. If a serial is supplied, it must match. Multiple native
candidates never imply permission to pick the first one. The resolver supplies
current OS/version/channel and app-installation authorization, and binds Apple
user aliases to the authenticated native principal. A native user installation
scope ID is required when correlating a physical user-channel scope across
enrollments; it is not an Apple location's licensing alias.

`Step` checks current native asset capabilities and exact assignment readback.
`ConfirmAssignment` requires fresh remote ownership, complete coherent page sets,
one matching target and Associated user state. A successful licensing event,
available stock or a partial callback alone cannot permit installation. `Install`
can use an already assigned license without naming a historical job; a supplied
job adds complete-success and applicability checks. Token changes fence in-flight
source proof without changing the stable Apple UID or MDM identity.

MDM delivery constructs a typed app/book command. DDM app delivery constructs a
stable AppManaged configuration and activation. Generated support metadata enforces
versions, scopes and enrollment types. [Install Application](https://developer.apple.com/documentation/devicemanagement/install-application-command)
supports tvOS; [AppManaged](https://developer.apple.com/documentation/devicemanagement/appmanaged)
does not. macOS user licenses use the user channel and device licenses use the
device channel. [Install Media](https://developer.apple.com/documentation/devicemanagement/install-media-command)
has narrower book rules; its generated support record removes macOS support at
11.0. Unsupported delivery never silently changes method. Books remain user
assigned and nonreclaimable.

Delivery adapters must durably and atomically bind the stable dispatch identity,
payload digest and authenticated target, and reject same-ID mismatches or competing
management paths. Native scope ownership does not depend on optional serial or
licensing aliases; authenticated physical scope binding adds cross-enrollment
protection when serial data is available. Queue acceptance, acknowledgment,
declaration publication and device installation/version reports remain separate.
Offline delivery may be queued while actual installation remains unobserved.

After an uncertain dispatch, stop the original worker and wait for its lease to
expire before `Reconcile`, supplying an operator note. Readback never submits work.
A not-found result alone cannot enable replay. Explicit retry additionally requires
an authoritative owned read and the adapter's atomic same-identity idempotency
guarantee, including a late original call. A subsequent `Step` revalidates licensing
and the target before using the same persisted bytes and identity. A previously
observed dispatch cannot be repeated because its queue row was pruned. `Cancel`
releases only work that never entered delivery; it does not revoke licenses or
reclaim books.

For an intentional reinstall/update, set `Plan.PredecessorID` to the completed
operation in the same native installation scope and select the same delivery
method. Queuing makes no network calls. Promotion requires fresh owned terminal
device-processing evidence, then atomically hands off the lanes and fences the
old worker. Competing successor intents have one winner. MDM creates a new command
ID; DDM keeps the owned configuration/activation IDs and uses the adapter's atomic
same-method ownership handoff. Canceling a never-submitted successor restores
ownership with increasing epochs/fences, not an old worker lease. A method switch
requires a separate explicit migration procedure. Historical terminal processing
is not a claim that the app is still installed; current installation and version
remain independently reported.

On macOS, supported non-User-Enrollment app commands explicitly request
`InstallAsManaged=true` so a reinstall does not silently unmanage the app. The key
is absent where generated OS/version/enrollment metadata forbids it. This request
flag is intent, not evidence of native installation or management.

`Load` and `Operations` return ordinary intent and safe status. `LoadEvidence` is a
separate privileged read for exact source/configuration pages, wire payloads and
raw device/adapter observations. Adapter/SDK error strings are never copied into
ordinary operation state. Protect intent and identifiers with appropriate grants,
seal private state and retain unresolved operations for reconciliation.

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

## Reference-server operations

Set `DM_APPSBOOKS_MDM_ID` to the stable identity for this server. This enables the
Apps and Books HTTP/CLI composition only with sealed SQLite, PostgreSQL or MySQL
storage. The process uses the published SDK workflows over its existing protocol
state and event transactions. Tokens and receiver credentials have no ordinary
read endpoint. Trusted embedding can supply `AppsBooksConfig.BaseURL` and
`HTTPClient`; HTTP callers cannot select a credential destination.

The permission catalogue contains six dedicated actions: `readAppsBooks`,
`manageAppsBooks`, `readAppsBooksEvidence`, `readManagedContent`,
`manageManagedContent` and `readManagedContentEvidence`. None belongs to a routine
role action group. The evidence actions allow private raw source/configuration,
notification, command/declaration and native device evidence. Ordinary reads return
safe location metadata, licensing job status and managed-content operation status.

The authenticated API is rooted at `/admin/v1`:

| Resource | Explicit operations |
| --- | --- |
| `/appsbooks/locations/{location}` | `GET` safe metadata; `PUT` register or replace `{revision,sToken}` |
| Location `/receiver` | `PUT {token}` separately records/rotates the dedicated callback credential |
| Location `/configure` | `POST {revision,configuration}` explicitly claims/configures the location; notification credentials must match the saved receiver |
| Location `/refresh`, `/probe` | `POST {revision,maxPages}` for complete synchronization; `POST {revision}` for stopped configuration reconciliation |
| Location `/recover-configuration`, `/recover-licensing` | `POST {revision,sToken}`; licensing recovery additionally selects `jobId` |
| Location `/jobs`, `/jobs/{job}` | `GET` safe paginated status; `PUT {revision,request}` on a selected job queues local SDK licensing intent |
| Job `/submit`, `/poll` | `POST {worker,leaseSeconds}` explicitly submits or polls that job |
| Job `/reconcile` | `POST {eventId,note,abandon}` resolves stopped uncertain work without implicit POST replay |
| `/managed-content/users` | `PUT {enrollment,clientUserId}` explicitly binds an Apple user to a current authenticated native principal |
| `/managed-content/operations`, `/operations/{operation}` | `GET` safe status; `PUT {mode,plan}` queues `assign`, `install` or `assign-and-install` for one explicitly selected authenticated enrollment |
| Operation `/step`, `/reconcile` | `POST {worker,leaseSeconds,note,retryAbsent}` advances or explicitly reconciles retained work |
| Operation `/cancel` | `POST` cancels pre-dispatch intent without revoking a license |
| Location, job or operation `/evidence` | Separately privileged retained source/native evidence |
| Location `/notifications/{notification}/evidence` | Separately privileged durable notification receipt |

Lists use the exclusive `cursor` query parameter. SDK request types retain their
camelCase fields; native enrollment addressing is `{Channel,ID,ParentID}`, with
`Channel` the SDK's numeric channel. Native opaque identifiers address enrollments;
physical identity correlation uses only an explicitly supplied matching serial.
An authenticated User Enrollment target does not need a serial or ABM record.
Apple `clientUserId` is a separately chosen licensing identity, bound to the current
native principal/certificate. A queued operation pins that generation, so later
binding, principal or certificate changes cannot redirect it.

`POST /appsbooks/notifications/{location}` is the public callback route. It accepts
only the dedicated saved receiver bearer, validates the immutable location UID,
and commits the authenticated SDK inbox/deduplication and required event capture
before returning 204. Rotation immediately rejects the previous bearer; retained
identical notification replays are idempotent. Configure the receiver before the
explicit configuration request so Apple's synchronous test notification can arrive.

All local SDK writes participate in required event capture; a capture failure
rolls them back. Remote service calls execute outside local transactions. Durable
pending reservations remain the source of truth after a remote outcome becomes
uncertain; local rollback cannot undo a remote Apple request. No ownership or
licensing POST occurs during ordinary reads, registration or refresh. The
`managed-content-state` event intentionally contains metadata only; administrator
access/completion occurrences retain the separately projected action and actor.

The supervised `managed-content` worker resumes queued/prepared operations and
polls their accepted licensing jobs using the SDK's timing/fencing rules. Uncertain
submission or dispatch requires explicit stopped-worker reconciliation. Independent
licensing jobs expose explicit submit/poll/reconcile operations. Immutable native
receipts bind one dispatch ID, target and wire digest to the MDM queue or DDM
publication in the shared SQL transaction. Repeated dispatch IDs cannot enqueue
new work after queue pruning. Conflicting native management paths fail closed.

MDM typed install processing is historical evidence, separate from a bare ACK.
Current app installation/version requires an applicable authenticated native
`InstalledApplicationList` joined to the identifier returned by that exact install
command. A later empty report clears current installation/version without erasing
historical completion. DDM uses its own exact declaration-token and
`app.managed.list` evidence; MDM-only managed-app queries do not establish DDM
installation. Publication alone never establishes app installation. Missing or
stale native evidence remains unconfirmed. Reinstall/update uses an explicit
same-method SDK predecessor, never an implicit method switch.

`dmctl appsbooks` exposes `get`, `evidence`, `register`, `receiver`, `configure`,
`refresh`, `probe`, token recovery, `job-create`, `job-get`, `job-evidence`,
`submit`, `poll` and `reconcile`. `dmctl managed-content` exposes `create`, `get`,
`evidence`, `step`, `reconcile` and `cancel`. Mutating JSON comes from `-file` or
stdin, keeping credentials out of command arguments and URLs. See the maintained
[explicit operation examples](../../server/examples/managed-content/README.md).
