# 0026: DEP client, device sync, and profile assignment

Status: accepted
Date: 2026-09-02
Phase: 6

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/device-assignment> (endpoint index)
- Doc: <https://developer.apple.com/documentation/devicemanagement/authenticating-for-automated-device-enrollment> (`/session`, OAuth 1.0a HMAC-SHA1, `X-ADM-Auth-Session` rotation, 401 on expiry)
- Doc: <https://developer.apple.com/documentation/devicemanagement/fetch-devices>, <https://developer.apple.com/documentation/devicemanagement/sync-devices> (`CURSOR_REQUIRED`, `INVALID_CURSOR`, `EXHAUSTED_CURSOR`, `EXPIRED_CURSOR` at 7 days, `USER_AGENT_*`; duplicates resolved by `op_date`)
- Doc: <https://developer.apple.com/documentation/devicemanagement/define-profile>, <https://developer.apple.com/documentation/devicemanagement/assign-profile>, <https://developer.apple.com/documentation/devicemanagement/clear-device-profile>, <https://developer.apple.com/documentation/devicemanagement/fetch-profile>, <https://developer.apple.com/documentation/devicemanagement/profile> (every profile key incl. `configuration_web_url`, `anchor_certs`, `is_multi_user`, `is_return_to_service`, `do_not_use_profile_from_backup`)
- Doc: <https://developer.apple.com/documentation/devicemanagement/device> (`op_type`, `op_date`, `profile_status`, `mdm_migration_deadline` v8+, MAC addresses v10+), <https://developer.apple.com/documentation/devicemanagement/fetchdeviceresponse>
- Doc: <https://developer.apple.com/documentation/devicemanagement/device-details>, <https://developer.apple.com/documentation/devicemanagement/disown-devices>, <https://developer.apple.com/documentation/devicemanagement/activation-lock-devices>, <https://developer.apple.com/documentation/devicemanagement/get-beta-enrollment-tokens>
- Doc: <https://developer.apple.com/documentation/devicemanagement/assign-account-driven-enrollment-profile>, <https://developer.apple.com/documentation/devicemanagement/fetch-account-driven-enrollment-profile>, <https://developer.apple.com/documentation/devicemanagement/remove-account-driven-enrollment-profile>
- YAML: none (the DEP web service is documented only on the web; the response schemas above are the source of the generated types)

## References read

- `micromdm/nanodep@2223746` `client/`, `godep/`, `tokenpki/`, `sync/`, `storage/`, `proxy/`, `cmd/depsyncer`, `docs/operations-guide.md`
- `fleetdm/fleet@b44343c` `server/mdm/apple/apple_mdm.go` (`RunAssigner`, `processDeviceResponse`, `buildJSONProfile`), `server/mdm/nanodep/` (vendored `4c207e8`), `server/datastore/mysql/apple_mdm.go` (cooldowns), `server/worker/macos_setup_assistant.go`
- `micromdm/micromdm@904493b` `dep/`, `platform/dep/sync/depsync.go`, `platform/config/apply_deptoken.go`
- `zentralopensource/zentral@b10dd22` `zentral/contrib/mdm/dep.py`, `dep_client.py` (see 0027 for the enrollment side)

## Known pitfalls found

- nanodep `sync/syncer.go:179-181` and `sync/assigner.go:135-143`: fetch responses carry no `op_type`, so a full re-fetch after `EXPIRED_CURSOR` never auto-assigns anything.
- nanodep `cmd/depsyncer/main.go:150-166`: the cursor advances even when the consumer fails; a webhook outage loses devices (at-most-once).
- nanodep `client/transport.go:181-185`: `make([]byte, 0, req.ContentLength)` panics on chunked bodies (`ContentLength == -1`).
- nanodep `client/transport.go:54-57`: session cache is per process with no singleflight; Apple may not support concurrent sessions per token.
- nanodep `client/auth.go:55`: `access_token_expiry` stored, never checked; #80 (token caching), #84 (session lock), #73 (fetch/sync repeats the cursor with `more_to_follow`), #42 (`USER_AGENT_INVALID`).
- nanodep: `THROTTLED`, `retry_after_seconds`, and `/account` per-URL limits are parsed and ignored; no 429 handling; protocol version pinned to 7 while the schema has v8 to v10 keys.
- MicroMDM `platform/dep/sync/service.go:31-35`: `Cursor.Valid()` is always true (7-day lifetime never enforced locally); `depsync.go:157-168` substring matching on error text; `dep/client.go:169` 3-minute session TTL with no 401 re-auth; #1016 same-cursor loop; #497 invalid cursor; #582/#632 loop death on errors.
- Fleet `apple_mdm.go:755,781` FIXMEs: `modified` devices are never re-assigned unless missing locally; `#23200` removed-then-re-added devices skipped; `#37460` simultaneous adds and modifies mis-reconciled; `#37462` assignment row left deleted after a server move; `#39871` Apple removes the assignment when a device is re-assigned to the same server and nothing re-assigns; `#44980` replica lag when screening cooldowns; `syncer.go:196-201` a callback failure re-fetches and re-assigns a whole page; cooldowns hard-coded (1h/24h).
- Fleet `#22955` ABM token renewal broken; `#21273` and `#33405` `T_C_NOT_SIGNED` handling; `#21500` one client per token.
- Zentral `dep_client.py:170-173`: unbounded recursive retry on 401/403; `dep.py:257` sync bypasses the expiry check; `models.py:1906` cursor column capped at 128 characters; `crypto.py:173-177` `.p7m` decrypt shells out to `/usr/bin/openssl`; no `DELETE /profile/devices`; `dep.py:529-537` `DefineProfile` writes `profile_status=assigned` locally instead of reading back.

## What they do

- **nanodep**: OAuth 1.0a `/session` through a `RoundTripper` keyed by DEP name in the context; body tee'd and replayed once on 401 or `403 FORBIDDEN`; adopts a rotated `X-ADM-Auth-Session`; token PKI staged then upstaged after the `.p7m` decrypts and the consumer key matches; fetch-then-sync state machine; cursor stored after the callback; assigner acts on `added` only; storage as tiny interfaces with a contract suite; generated schema from Apple's JSON.
- **Fleet**: dedupes by `op_date`, treats unknown `modified` as `added`, marks serials missing from the assign response as `FAILED`, per-serial cooldowns with a retry job id, `terms_expired` and `token_invalid` flags per token from an after-hook, cursor cleared when the Setup Assistant profile changes, `await_device_configured` forced true, `DisownDevices` on host delete.
- **MicroMDM**: hot token swap on `TokenAdded`, session under a mutex, cursor persisted with a timestamp and on shutdown, same-cursor guard, one auto-assigner (`*`), `deleted` never removes.
- **Zentral**: validates a new token with `GET /account` on upload; `X-Server-Protocol-Version: 10`; per-endpoint limits from the account detail; reads assignment results back from `/devices`; `THROTTLED` is a retryable outcome honouring `retry_after_seconds`; stale operations skipped by `op_date`; advisory lock per virtual server; `skip_setup_items` validated against `skipkeys.yaml`.

## What we do better

1. `dep.Client` is one client for many accounts: every call takes an account name, tokens and sessions come from `dep.Store`, and a `/session` re-authentication runs once per account under a singleflight; a rotated `X-ADM-Auth-Session` is persisted through the store so processes share one session. 401, `403 FORBIDDEN`, and `EXPIRED_TOKEN` re-authenticate exactly once; a second failure returns `*dep.Error{Status, Code, Body}` (code parsed from bare and quoted bodies, never substring-matched by callers).
2. The OAuth 1.0a signature (RFC 5849 HMAC-SHA1, `realm="ADM"`) is our own implementation with an injectable clock and nonce; request bodies are replayed from `GetBody` or a bounded buffer that tolerates `ContentLength == -1`; `X-Server-Protocol-Version` defaults to the newest version the generated device schema declares (10) and is per-account configurable; `User-Agent` is always set.
3. Token lifecycle is first class: `dep.Store` holds accounts by name with the OAuth1 tokens sealed through `storage/crypt`, the token PKI keypair (staged and current) with an atomic upstage, `AccessTokenExpiry` checked before every session request (`ErrTokenExpired` fails fast, `DEPTokenExpiring` published inside a configurable window), and `T_C_NOT_SIGNED` and 401 from `/session` mapped to distinct typed errors and account states (`TermsExpired`, `TokenInvalid`) that clear only on a definitive success.
4. `.p7m` unwrapping uses a MIME parser and validates the token JSON before anything is written; a corrupted token leaves the current keypair untouched. The same package produces `.p7m` files for tests (encrypt to a caller-supplied certificate), so the exchange is testable end to end.
5. `dep.Syncer` is a function over an injected clock and client: fetch until `EXHAUSTED_CURSOR`, then sync; a persisted cursor older than 7 days is discarded before the first call; `EXPIRED_CURSOR` and `INVALID_CURSOR` restart the fetch; the same cursor with `more_to_follow` is an error with backoff, never a loop; a transient error retries within seconds, not at the next tick; devices are deduplicated by serial keeping the latest `op_date` with `deleted` winning a tie.
6. Delivery is at-least-once with the cursor stored only after the page is committed: `dep.DeviceStore` records every device with `op_type`, `op_date`, `profile_uuid`, `profile_status`, `profile_push_time`, `os`, `device_family`, and the v8+ and v10+ keys; `deleted` tombstones the device; a page that fails to commit is re-requested with the same cursor and produces no duplicate events. Events `DEPDeviceAdded`, `DEPDeviceModified`, `DEPDeviceDeleted` are published per device.
7. `dep.Assigner` computes work from state, not from `op_type`: after every page and after a full re-fetch, every device whose `profile_uuid` differs from the account's current profile or whose `profile_status` is `empty` or `removed` is (re)assigned, including devices Apple reports as `modified` after a server move; per-serial outcomes are recorded; `NOT_ACCESSIBLE` and `FAILED` retry with jittered exponential backoff from configurable bases, `THROTTLED` honours `retry_after_seconds`, serials missing from the response are recorded as `FAILED`; HTTP 429 and `Retry-After` back off the whole account. Filters are a `func(Device) bool` hook, not a single `*`.
8. Profiles are typed from Apple's documented keys (every key on the `Profile` page, including `configuration_web_url`, `anchor_certs`, `is_multi_user`, `is_return_to_service`, `do_not_use_profile_from_backup`), unknown keys round-trip through `Extra`, and `DefineProfile` after `FetchProfile` is byte-stable; `is_mdm_removable=false` without `is_supervised=true` is rejected locally with the same code Apple uses (`FLAGS_INVALID`).
9. A new token is validated with `GET /account` before it is stored (organisation name, server UUID, and per-endpoint limits recorded; `T_C_NOT_SIGNED` surfaced to the caller); pagination limits come from the account detail with documented fallbacks; `skip_setup_items` are validated against `other/skipkeys.yaml`. The full endpoint set is implemented: account, fetch, sync, details, disown, activation lock, define/assign/remove/fetch profile (assign as `POST` by default, `PUT` behind an option for depsim), account-driven enrollment discovery URL (assign, fetch, remove), and beta enrollment tokens (`APPLE_SEED_FOR_IT_TURNED_OFF` typed).
10. `dep/deptest.Server` is our own fake DEP service: OAuth 1.0a verification with a clock window, session rotation and invalidation, scripted 401/403/`T_C_NOT_SIGNED`/429/`THROTTLED`, opaque cursors with age, fetch pages without `op_type` and sync pages with it, `.p7m` generation, and a request log. Every contract above is proved against it.

## Verified by

1. `dep.TestSession/RefreshOnce` (N concurrent 401s, one `/session` call; would fail on nanodep), `dep.TestSession/RotatedTokenPersisted`, `dep.TestSession/SecondFailureIsTypedError`, `dep.TestError/CodeFromQuotedAndBareBodies`.
2. `dep.TestOAuth1/SignatureVector` (RFC 5849 example and Apple's documented header), `dep.TestTransport/ChunkedBodyReplay` (would panic on nanodep), `dep.TestTransport/ProtocolVersionHeader`.
3. `dep.TestToken/ExpiredFailsFast` (no HTTP call), `dep.TestToken/ExpiringEvent`, `dep.TestToken/TermsNotSignedState`, `dep.TestToken/StateClearsOnlyOnSuccess`, `deptest.RunStoreSuite/AccountsSealedAtRest`, `RunStoreSuite/UpstageAtomic`.
4. `dep.TestTokenPKI/RoundTrip` (generate `.p7m` in test, unwrap, compare), `dep.TestTokenPKI/CorruptLeavesCurrentKeypair`, `dep.TestTokenPKI/ConsumerKeyMismatch`.
5. `dep.TestSyncer/FetchThenSync`, `/StaleCursorDiscarded` (8-day cursor triggers a fetch; would fail on MicroMDM), `/ExpiredCursorRefetches`, `/SameCursorIsError`, `/TransientErrorRetriesSoon`, `/DedupeByOpDate` (deleted wins a tie; would fail on nanodep).
6. `dep.TestSyncer/CursorStoredAfterCommit` (commit fails once, same cursor requested twice, no duplicate events; would fail on nanodep and Fleet), `deptest.RunStoreSuite/DevicesRoundTripAllKeys`, `/DeletedTombstones`.
7. `dep.TestAssigner/StaleProfileReassigned` (`modified` after a server move; would fail on Fleet and nanodep), `/AfterRefetchAssigns` (would fail on nanodep), `/RetryWithBackoff`, `/ThrottledHonoursRetryAfterSeconds`, `/MissingSerialIsFailed`, `/Http429BacksOffAccount`, `/FilterHook`.
8. `dep.TestProfile/RoundTripByteStable`, `/UnknownKeysPreserved`, `/FlagsInvalidLocally`.
9. `dep.TestToken/ValidatedWithAccountOnStore`, `dep.TestSyncer/LimitFromAccountDetail`, `dep.TestProfile/SkipKeysValidated`, `dep.TestEndpoints/*` (one subtest per endpoint against `deptest`), `dep.TestEndpoints/AssignIsPostByDefault`, `/AssignPutOption`, `/BetaTokensForbiddenTyped`.
10. `deptest.TestServer/*` (the fake's own contract: OAuth verification, cursor ageing, scripted errors), `e2e.TestE2E_DEPAssign` (E2E-011).

## Rejected alternatives

- Vendoring or importing nanodep (as Fleet does): the repo rule forbids MicroMDM-family dependencies, and its transport, syncer, and assigner carry the pitfalls above.
- Generating types from Apple's JSON schemas as nanodep does: the schemas are not in the pinned submodule; hand-typed structs from the documented keys with `Extra` for unknown keys keep the round trip stable and the source of truth cited.
- Cursor advance independent of delivery (nanodep): at-most-once loses devices; the cursor is part of the page commit.
- Assignment driven by `op_type == added` (nanodep, MicroMDM): misses re-fetches and server moves; state-driven assignment covers both.
- Cooldown tables keyed by fixed 1h/24h windows (Fleet): backoff with `retry_after_seconds` and jitter, configurable.
