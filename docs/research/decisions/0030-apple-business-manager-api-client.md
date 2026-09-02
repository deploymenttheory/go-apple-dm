# 0030: Apple Business Manager and Apple School Manager API client (`axm`)

Status: accepted
Date: 2026-09-02
Phase: 6

## Apple sources

- Doc: <https://developer.apple.com/documentation/apple-school-and-business-manager-api> (overview)
- Doc: <https://developer.apple.com/documentation/apple-school-and-business-manager-api/implementing-oauth-for-the-apple-school-manager-and-apple-business-api> (ES256 client assertion: header `alg`, `kid`; claims `aud` = `https://account.apple.com/auth/oauth2/v2/token`, `exp` < `iat` + 180 days, `iat`, `sub` = client id, `jti`, `iss`; token request `grant_type=client_credentials`, `client_id`, `client_assertion_type=urn:ietf:params:oauth:client-assertion-type:jwt-bearer`, `client_assertion`, `scope` `business.api` or `school.api`; `expires_in` 3600)
- Doc: <https://developer.apple.com/documentation/applebusinessapi> (endpoint index: orgDevices, AppleCare coverage, MDM-enrolled devices, mdmServers CRUD and device linkages, assignedServer, orgDeviceActivities, users, userGroups, organizationalUnits, apps, packages, configurations, blueprints and their relationships, auditEvents) and <https://developer.apple.com/documentation/appleschoolmanagerapi>
- Doc: <https://developer.apple.com/documentation/applebusinessapi/create-an-orgdeviceactivity>, <https://developer.apple.com/documentation/applebusinessapi/get-orgdeviceactivity-information>, <https://developer.apple.com/documentation/applebusinessapi/orgdeviceactivitytype>, <https://developer.apple.com/documentation/applebusinessapi/errorresponse>, <https://developer.apple.com/documentation/applebusinessapi/paginginformation>, <https://developer.apple.com/documentation/applebusinessapi/pageddocumentlinks>
- Doc: <https://developer.apple.com/documentation/apple-school-and-business-manager-api/apple-school-manager-and-apple-business-api-changelog> (ABM 2.0 to 2.4 additions incl. `RELEASE_DEVICES`, MDM migration activities, blueprints, audit events)
- YAML: none (the API is documented only on the web; the local corpus of those pages was captured on 2026-09-02 for the type definitions)

## References read

- `deploymenttheory/go-sdk-appleservices@da9fe80` `axm/` (`client/auth.go`, `client/transport.go`, `client/pagination.go`, `client/errors.go`, `axm_api/*/crud.go`, `models.go`, `constants.go`, `acceptance/**`, fixtures) as the behavioural reference for how the live API responds; read only, never imported (repo rule)
- `micromdm/nanoaxm@6233fe7` `client/oauth.go`, `client/atmgr.go`, `client/camgr.go`, `client/transport.go`, `goaxm/abm.go`, `cryptoutil/cryptoutil.go`, `storage/`
- Record 0007 (push backoff shape), 0011 (secrets provider), 0013 (secrets at rest)

## Known pitfalls found

- go-sdk-appleservices `client/transport_options.go:16-25`: `WithBaseURL` never reaches the HTTP client, so a fake server cannot be targeted; `constants/auth.go:6` an App Store Connect audience left over and ignored; `client/auth.go:121-128` RS256 accepted although Apple requires ES256; `auth.go:115` every assertion minted for the full 180 days; `transport.go:102-107` a 401 invalidates the token but does not replay the request; `transport.go:60-62` retries only on transport errors for idempotent methods, so 429 and 5xx are never retried and `Retry-After` is ignored; `devices/constants.go:78-81` status constants in lower case while the fixture shows `ASSIGNED`/`UNASSIGNED`; `imei`, `meid`, `ethernetMacAddress` are arrays; `errors.go:22-103` `source` nesting differs from JSON:API's flat form.
- nanoaxm: no pagination iterator, no `Retry-After` handling, no activity polling, private keys stored in plaintext, `storage/storage.go:17-106` warns implementers to add distributed locking for the assertion cache.
- Observed API behaviour (acceptance tests): assignment is eventually consistent (linkage lags up to about 15 s, sometimes more); an unassigned device's `relationships/assignedServer` may be 200 with an empty id or 404; activities take minutes and expose a CSV at `downloadUrl`; `Accept: application/json` is mandatory (406 otherwise); MDM server ids are 32 upper-case hex characters, device ids are serial numbers, activity ids are UUIDs.

## What they do

- **go-sdk-appleservices**: ES256 or RS256 assertion with `iss == sub == client id`, 180-day `exp`, `jti` from the clock; token cached to `expires_in` minus 5 minutes under a mutex; every list endpoint merges all pages by following `links.next` and overlaying its query; typed models for every resource with fixtures; acceptance tests that poll activities every 5 s for 2 minutes and the assignment linkage every 2 s for 15 s.
- **nanoaxm**: two-level cache (assertion at 95% of 180 days, token at 80% of `expires_in`), one refresh and retry on 401, `Accept` header enforced, reverse proxy per AxM name.

## What we do better

1. `axm.Client` is complete: every endpoint on Apple's index is a typed method (org devices and AppleCare coverage, MDM-enrolled devices, MDM servers CRUD and device linkages, assigned server (linkage and full), org device activities create and read, users, user groups and their users, organizational units and their users, apps, packages, configurations CRUD, blueprints CRUD and the six relationship linkages add/list/remove, audit events with the required timestamp filters), on both hosts (`api-business.apple.com`, `api-school.apple.com`) with the scope derived from the client id prefix and overridable. Types come from the captured documentation pages and decode the observed shapes (arrays for `imei`, `meid`, `ethernetMacAddress`; upper-case status enums with unknown values preserved).
2. Authentication is our own ES256 client assertion (RFC 7519 header `alg`, `kid`; claims `iss`, `sub`, `aud`, `iat`, `exp`, `jti` as a UUID) built with `crypto/ecdsa`: only P-256 keys are accepted, `exp` defaults to 20 minutes and can never exceed `iat` + 180 days, `iat` is back-dated by a configurable skew, PEM keys in SEC1 or PKCS#8 form load from bytes, file, or `secrets.Provider`; the token request is form-encoded with all five parameters and `Content-Type: application/x-www-form-urlencoded`; token endpoint and API base URL are real options.
3. The access token is cached and refreshed at a configurable margin before `expires_in`, under a singleflight; a 401 on an API call invalidates the token and replays that request exactly once; a second 401 is a typed `*axm.AuthError`. Credentials and cached tokens go through `secrets.Provider` and `storage/crypt` when persisted.
4. Retries: 429 honours `Retry-After` (seconds or HTTP date) and 5xx and transport errors back off with jitter and a bounded count; `POST` is retried only when the caller marks it safe; 4xx other than 401 and 429 are never retried.
5. Errors decode Apple's `ErrorResponse` (`errors[]{id, status, code, title, detail, source, links, meta}`) accepting both `source{pointer|parameter}` forms, keep every error, and expose `errors.As` helpers (`IsNotFound`, `IsConflict`, `IsUnauthorized`, `IsRateLimited`); non-JSON bodies become a synthesised error carrying the status and body.
6. Pagination is explicit: single-page calls return `Page[T]{Items, Links, Meta}` with `meta.paging{total, limit, nextCursor}` and `links.next`; `All`/`Each` iterators follow `links.next` with a page cap, respect the context, and report progress; `limit` is validated (1 to 1000).
7. Device management workflows are first class: `AssignDevices`, `UnassignDevices`, `ReleaseDevices`, `AssignWithMigrationDeadline`, `UpdateMigrationDeadline`, `CancelMigration` build the JSON:API activity with the per-type rules enforced locally (server presence, deadline within 90 days); `WaitForActivity` polls to a terminal status with interval, backoff, and timeout; `FetchActivityLog` streams the CSV; `WaitForAssignedServer` tolerates the documented eventual consistency (empty id or 404 while pending).
8. `axm/axmtest.Server` is our own fake: token endpoint verifying the ES256 assertion against registered keys (`kid`, `aud`, `iat`/`exp` window, `jti` uniqueness, scope), bearer enforcement, every endpoint with JSON:API bodies and cursor pagination, an activity engine advancing through Apple's sub-statuses with a configurable consistency lag and CSV output, 406 on a missing `Accept`, and fault injection (expire tokens, 401 once, 429 with `Retry-After`, 5xx, per-serial outcomes). `internal/app` gains an `axm` configuration so the reference server can assign devices to its own MDM server from the admin API (0025), and the e2e suite drives assignment end to end.

## Verified by

1. `axm.TestEndpoints/*` (one subtest per endpoint against `axmtest`: method, path, query encoding incl. `fields[...]`, `limit`, `cursor`, `filter[...]`, `include`, `limit[apps]`, body shape), `axm.TestDecode/{ArraysForIMEIMEID,StatusEnumsPreserveUnknown,MDMServerID}`, `axm.TestScope/{FromClientID,Override,SchoolHost}`.
2. `axm.TestAssertion/{Claims,KidHeader,P256Only,ExpCapped180Days,DefaultTwentyMinutes,ClockSkew,JTIUnique}` (RSA accepted would fail on the SDK), `axm.TestKeyLoading/{SEC1,PKCS8,File,SecretsProvider,BadKeyNamesFormats}`, `axm.TestTokenExchange/{FormBody,ContentType,ScopeParameter,EndpointOverride,BaseURLOverride}` (the base URL case would fail on the SDK).
3. `axm.TestTokenCache/{ReuseWithinTTL,RefreshAtMargin,Singleflight,ForceRefresh}`, `axm.TestUnauthorized/{ReplayOnce,SecondIsAuthError}` (replay would fail on the SDK), `axm.TestCredentials/SealedAtRest`.
4. `axm.TestRetry/{RetryAfterSeconds,RetryAfterDate,BackoffJittered,BoundedCount,NoRetryOnPOSTByDefault,NoRetryOn4xx}` (429 would fail on the SDK and nanoaxm).
5. `axm.TestErrors/{Decode,BothSourceForms,Multiple,NonJSON,Helpers}`.
6. `axm.TestPagination/{FollowsLinksNext,MergesQueryFromNext,StopsWithoutNext,PageCap,RespectsContext,MetaPaging,LimitValidated}`.
7. `axm.TestActivities/{AssignBody,UnassignBody,ReleaseBody,MigrationDeadlineRules,ServerRequired,WaitForActivityTerminal,WaitTimeout,FetchActivityLog}`, `axm.TestAssignment/ConvergenceToleratesEmptyAnd404`.
8. `axmtest.TestServer/*`, `app.TestAxM/{ConfiguredFromEnv,AdminAssign}`, `e2e.TestE2E_ABMAssignDevices` (E2E-021: seed the fake, list with paging, assign, wait for completion and convergence, audit events, unassign; a second run with expired tokens proves the replay; a run with a rate limit proves `Retry-After`).

## Rejected alternatives

- Depending on `go-sdk-appleservices` or nanoaxm: the repo rules forbid both; their behaviours are the evidence, not the code.
- Generating types from Apple's OpenAPI: not in the pinned submodule; the captured documentation pages are cited and hand-typed with unknown fields preserved.
- Merging all pages by default (the SDK): callers must be able to cap; iterators are opt-in.
- A reverse proxy as the primary interface (nanoaxm): the typed client is what the reference server and integrators call; a proxy can be layered later.
