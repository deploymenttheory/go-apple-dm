# 0030: Apple Business Manager and Apple School Manager API client (`axm`)

## Context

The Apple School Manager and Apple Business Manager APIs use ES256 client assertions, bearer tokens, JSON:API resources and asynchronous device-assignment activities.

## Decision

`axm.Client` provides typed resource and relationship methods, explicit single-page results and opt-in iteration. Authentication accepts P-256 keys, creates time-bounded assertions and coordinates cached token refresh. An API 401 triggers one replay; retries for rate limits and transient failures are bounded, with POST retry requiring an explicit safe option.

Activity helpers validate required fields and migration deadlines, poll terminal activity state, download activity logs and wait for assigned-server convergence. Errors retain Apple's structured entries and expose typed classification helpers.

## Rationale

Explicit pagination and retry policy give callers control over work and side effects. Separate activity completion and assignment convergence reflect asynchronous service behavior.

## Constraints

Types are maintained from API documentation rather than the pinned device-management YAML. Unknown values are preserved where modeled. Endpoint coverage reflects the implemented client, not a guarantee of future Apple API coverage. Persistent credentials use `server/axmcreds`; the library client has no SQL dependency.

## Verification

The fake service verifies assertions and token scopes and exercises typed endpoints, paging, consistency delays and injected faults. Client tests cover key formats, claims, refresh races, replay, retry, errors and activity rules; end-to-end tests exercise assignment.

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
- <https://developer.apple.com/documentation/applebusinessapi/orgdeviceactivitytype>
- <https://developer.apple.com/documentation/applebusinessapi/errorresponse>
- <https://developer.apple.com/documentation/applebusinessapi/paginginformation>
- <https://developer.apple.com/documentation/applebusinessapi/pageddocumentlinks>
- <https://developer.apple.com/documentation/apple-school-and-business-manager-api/apple-school-manager-and-apple-business-api-changelog>

Reference source identifiers and paths (relative to the named project):

- `deploymenttheory/go-sdk-appleservices@da9fe80`, `axm/`, `client/auth.go`, `client/transport.go`, `client/pagination.go`, `client/errors.go`, `axm_api/*/crud.go`, `models.go`, `constants.go`, `acceptance/**`
- `micromdm/nanoaxm@6233fe7`, `client/oauth.go`, `client/atmgr.go`, `client/camgr.go`, `client/transport.go`, `goaxm/abm.go`, `cryptoutil/cryptoutil.go`, `storage/`
