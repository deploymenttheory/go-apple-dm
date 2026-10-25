# 0030: Apple Business Manager and Apple School Manager API client (`axm`)

## Context

The Apple School Manager and Apple Business Manager APIs use ES256 client assertions, bearer tokens, JSON:API resources and asynchronous device-assignment activities.

## Decision

`axm.Client` provides typed resource and relationship methods, explicit single-page results and opt-in iteration. Authentication accepts P-256 keys, creates time-bounded assertions and coordinates cached token refresh. An API 401 triggers one replay; retries for rate limits and transient failures are bounded, with POST retry requiring an explicit safe option.

Activity helpers validate required fields and migration deadlines, poll terminal activity state, download activity logs and wait for assigned-server convergence. `ResolveOrgDevicesBySerial` performs an exact, complete paginated lookup before serial-based assignment helpers submit returned opaque organization-device IDs. Missing, ambiguous or unaddressable resources and incomplete enumeration stop the operation before mutation. `NewActivityRequest` and resource endpoints accept opaque IDs, independently of any inventory model. Errors retain Apple's structured entries and expose typed classification helpers.

Activity downloads allow the configured API origin and Apple's documented HTTPS `store.blobstore.apple.com` origin on port 443. API-origin downloads authenticate and replay once with a refreshed token after a 401; a second 401 is a sanitized authentication error. Presigned storage requests carry neither API bearer nor cookies. Redirects are rejected, including redirects to another allowed origin: callers obtain a fresh URL from the activity resource. Downloads apply the configured bounded GET retry policy to transport, rate-limit and server failures, honoring Retry-After and cancellation while logging only sanitized causes. Downloads buffer bounded CSV and reject truncated transport, malformed records and missing outcomes. Download errors omit URLs and response bodies to protect signed query credentials.

`FetchActivityLog` preserves raw CSV and does not assert device success. `VerifyActivityLog` accepts explicit observed serial/outcome header names and a success value because the activity endpoint does not specify a stable CSV column schema. It checks the complete expected serial set, rejects duplicate/unexpected/missing device outcomes and retains every column and failure detail. Activity terminal state, log completeness and per-device success remain separate facts.

The reference server persists explicit account-scoped assignments and unassignments in
`server/axmassignment`, composed over the existing inventory backend and supervised
workers. A configured binding names this MDM service and the current account incarnation,
revision and credentials. Requests accept serials and a local idempotency request ID;
complete discovery resolves and persists opaque resource IDs. Unassignment requires a
current linkage to the configured service. Synchronization remains read-only.

HTTP 202 acknowledges retained local intent. Persisted Apple acceptance, terminal activity,
exact CSV per-device outcomes and assigned-service observation are separate evidence.
Partial failures and unknown outcomes survive completion. Raw CSV retains every byte and
column under separate raw-data authority; ordinary summaries omit signed download URLs
and remote error bodies. Successful device reads retain original source JSON and provenance.

A durable submitting marker precedes POST. Its overlap protection survives fencing
and credential/account replacement until the remote operation is resolved; fencing
queued work without a submission marker permits a new explicit request. Each binding
configuration has a fresh generation, so restoring previous service values cannot
revive stale authority. An expired worker, lost response or failed
acceptance commit moves work to read-only uncertain reconciliation without resubmission.
[Apple's create contract](https://developer.apple.com/documentation/applebusinessapi/create-an-orgdeviceactivity)
and [known-ID retrieval contract](https://developer.apple.com/documentation/applebusinessapi/get-orgdeviceactivity-information)
document no client idempotency key or activity discovery endpoint. We therefore cannot
reliably rediscover a lost ID from those contracts; observed convergence never invents
acceptance or terminal success, and non-convergence never proves safe retry.

Shared SQL transactions commit local workflow evidence and source observations together;
remote calls stay outside transactions. Leases and binding fingerprints fence stale
workers, including deletion/recreation, key replacement and binding ABA changes.
The remote attempt context expires independently of the worker lifetime; local
bookkeeping uses the still-live worker context with the same lease/binding checks.
An attempt timeout therefore cannot stop sibling application workers. A remote request already
started can still finish after fencing. Apple's unconditional mutation cannot atomically
protect the read-before-unassign check against concurrent external administration.
The [operations guide](../../operations/abm-assignments.md) defines states, permissions,
configuration and recovery limits.

Named-account administration uses current sealed inventory credentials for each
live device, coverage and service read. A short local transaction snapshots
metadata and its key before client construction; network I/O follows afterward.
Account incarnation, revision and key
checks fence responses after network I/O. Connection verification atomically
checks the same snapshot and stamps only that exact incarnation and key; a
delete/recreate cannot inherit a successful stale verification. The HTTP API has no global live-read
fallback. Raw Apple device and coverage resources require explicit raw-inventory
authority in addition to business-manager read authority. Named reads capture
access before credential opening and remote calls when capture is configured;
network I/O does not hold the local audit transaction. Connection diagnostics
return reviewed categories independently from inventory presence and the latest
current-revision synchronization outcome. CLI verbs preserve the account through
assignment intent, retained operation/log inspection and synchronization jobs.
See [named inspection](../../operations/abm-assignments.md#named-connection-inspection).

## Rationale

Explicit pagination and retry policy give callers control over work and side effects. Separate activity completion and assignment convergence reflect asynchronous service behavior.

## Constraints

Types are maintained from API documentation rather than the pinned device-management YAML. Unknown values are preserved where modeled. Endpoint coverage reflects the implemented client, not a guarantee of future Apple API coverage. Persistent credentials use `server/axmcreds`; the library client has no SQL dependency.

## Verification

The fake service verifies assertions and token scopes and exercises typed endpoints, paging, consistency delays and injected faults. Client tests cover key formats, claims, refresh races, replay, retry, errors and activity rules. Independent API and storage-host tests verify opaque IDs distinct from serials, no storage credentials, signed-query preservation, origin/redirect rejection, expiration, size limits and truncated/malformed/partial CSV. End-to-end tests exercise assignment with distinct opaque IDs. Reference-server tests expire contexts inside activity HTTP requests and between durable observations, then verify accepted IDs remain queryable without a second mutation. Assignment tests cover lost accepted responses, crashes before acceptance persistence, expired leases, account replacement, SQLite/PostgreSQL/MySQL replicas and restart, partial/unknown/missing CSV outcomes and delayed linkage visibility. Deterministic
virtual-time tests expire the actual 90-second attempt through the application
supervisor after acceptance, during a lost submission and after lease expiry; the
parent/sibling workers survive and no second POST occurs. SQL fixtures also retain
fenced-submission overlap locks and reject binding replacement/restoration.

## References

- [appleplatformservices/axm](../../../devicemanagement/appleplatformservices/axm)
- [server/axmcreds](../../../server/axmcreds)
- [server/axmassignment](../../../server/axmassignment)
- [server/internal/app/axm.go](../../../server/internal/app/axm.go)
- <https://developer.apple.com/documentation/apple-school-and-business-manager-api>
- <https://developer.apple.com/documentation/apple-school-and-business-manager-api/implementing-oauth-for-the-apple-school-manager-and-apple-business-api>
- <https://account.apple.com/auth/oauth2/v2/token>
- <https://developer.apple.com/documentation/applebusinessapi>
- <https://developer.apple.com/documentation/appleschoolmanagerapi>
- <https://developer.apple.com/documentation/applebusinessapi/create-an-orgdeviceactivity>
- <https://developer.apple.com/documentation/applebusinessapi/get-orgdeviceactivity-information>
- <https://developer.apple.com/documentation/applebusinessapi/get-orgdevice-information>
- <https://developer.apple.com/documentation/applebusinessapi/orgdeviceactivitytype>
- <https://developer.apple.com/documentation/applebusinessapi/errorresponse>
- <https://developer.apple.com/documentation/applebusinessapi/paginginformation>
- <https://developer.apple.com/documentation/applebusinessapi/pageddocumentlinks>
- <https://developer.apple.com/documentation/apple-school-and-business-manager-api/apple-school-manager-and-apple-business-api-changelog>

Reference source identifiers and paths (relative to the named project):

- `deploymenttheory/go-sdk-appleservices@da9fe80`, `axm/`, `client/auth.go`, `client/transport.go`, `client/pagination.go`, `client/errors.go`, `axm_api/*/crud.go`, `models.go`, `constants.go`, `acceptance/**`
- `micromdm/nanoaxm@6233fe7`, `client/oauth.go`, `client/atmgr.go`, `client/camgr.go`, `client/transport.go`, `goaxm/abm.go`, `cryptoutil/cryptoutil.go`, `storage/`
