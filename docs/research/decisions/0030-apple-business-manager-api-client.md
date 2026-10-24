# 0030: Apple Business Manager and Apple School Manager API client (`axm`)

## Context

The Apple School Manager and Apple Business Manager APIs use ES256 client assertions, bearer tokens, JSON:API resources and asynchronous device-assignment activities.

## Decision

`axm.Client` provides typed resource and relationship methods, explicit single-page results and opt-in iteration. Authentication accepts P-256 keys, creates time-bounded assertions and coordinates cached token refresh. An API 401 triggers one replay; retries for rate limits and transient failures are bounded, with POST retry requiring an explicit safe option.

Activity helpers validate required fields and migration deadlines, poll terminal activity state, download activity logs and wait for assigned-server convergence. `ResolveOrgDevicesBySerial` performs an exact, complete paginated lookup before serial-based assignment helpers submit returned opaque organization-device IDs. Missing, ambiguous or unaddressable resources and incomplete enumeration stop the operation before mutation. `NewActivityRequest` and resource endpoints accept opaque IDs, independently of any inventory model. Errors retain Apple's structured entries and expose typed classification helpers.

Activity downloads allow the configured API origin and Apple's documented HTTPS `store.blobstore.apple.com` origin on port 443. API-origin downloads authenticate and replay once with a refreshed token after a 401; a second 401 is a sanitized authentication error. Presigned storage requests carry neither API bearer nor cookies. Redirects are rejected, including redirects to another allowed origin: callers obtain a fresh URL from the activity resource. Downloads apply the configured bounded GET retry policy to transport, rate-limit and server failures, honoring Retry-After and cancellation while logging only sanitized causes. Downloads buffer bounded CSV and reject truncated transport, malformed records and missing outcomes. Download errors omit URLs and response bodies to protect signed query credentials.

`FetchActivityLog` preserves raw CSV and does not assert device success. `VerifyActivityLog` accepts explicit observed serial/outcome header names and a success value because the activity endpoint does not specify a stable CSV column schema. It checks the complete expected serial set, rejects duplicate/unexpected/missing device outcomes and retains every column and failure detail. Activity terminal state, log completeness and per-device success remain separate facts.

The reference server returns HTTP 202 for a waited assignment that reaches terminal activity state. Its wait deadline returns HTTP 504 whether it expires during an activity HTTP request or between polls. A deadline ends local observation without cancelling or repeating an accepted Apple activity; the activity remains independently queryable. Other transport failures return HTTP 502.

## Rationale

Explicit pagination and retry policy give callers control over work and side effects. Separate activity completion and assignment convergence reflect asynchronous service behavior.

## Constraints

Types are maintained from API documentation rather than the pinned device-management YAML. Unknown values are preserved where modeled. Endpoint coverage reflects the implemented client, not a guarantee of future Apple API coverage. Persistent credentials use `server/axmcreds`; the library client has no SQL dependency.

## Verification

The fake service verifies assertions and token scopes and exercises typed endpoints, paging, consistency delays and injected faults. Client tests cover key formats, claims, refresh races, replay, retry, errors and activity rules. Independent API and storage-host tests verify opaque IDs distinct from serials, no storage credentials, signed-query preservation, origin/redirect rejection, expiration, size limits and truncated/malformed/partial CSV. End-to-end tests exercise assignment with distinct opaque IDs. Reference-server tests expire wait deadlines inside activity HTTP requests and between polls, then verify the accepted activity remains queryable without a second mutation.

## References

- [appleplatformservices/axm](../../../devicemanagement/appleplatformservices/axm)
- [server/axmcreds](../../../server/axmcreds)
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
