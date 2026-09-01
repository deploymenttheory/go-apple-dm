# 0005: Storage interfaces, in-memory backend, contract suite

Status: accepted
Date: 2026-09-01
Phase: 2

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> (what an enrollment record must retain: topic, push token, push magic, unlock token, bootstrap token)
- Doc: <https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device>

## References read

- `micromdm/nanomdm@main` `storage/storage.go`, `storage/mdm.go`, `storage/queue.go`, `storage/push.go`, `storage/pushcert.go`, `storage/certauth.go`, `storage/bstoken.go`
- `jessepeterson/kmfddm@main` `storage/*.go` (pagination absent, last-seen absent)
- NanoMDM issues #260, #86, #71; KMFDDM issues #5, #6, #41

## Known pitfalls found

- NanoMDM #260: `ClearQueue` on Postgres is slow (row-by-row, no status index).
- NanoMDM #86: bootstrap tokens are not migrated between backends.
- NanoMDM #71: re-enrollment leaves bootstrap and unlock tokens from the previous identity.
- KMFDDM #5/#6: no last-seen timestamp, no pagination on list endpoints.
- NanoMDM keeps `context.Context` inside `mdm.Request` for storage calls.

## What they do

- **NanoMDM**: one `AllStorage` union of small interfaces; `StoreAuthenticate` disables the enrollment until `TokenUpdate`; `RetrieveNextCommand(skipNotNow)`; results stored per command; `TokenUpdateTally` for migration; cert hash association tables.
- **KMFDDM**: file/diskv/inmem/MySQL with declaration, set, enrollment, and status interfaces.

## What we do better

1. Interfaces split by concern with explicit `context.Context` and typed inputs: `EnrollmentStore`, `CommandQueue`, `PushStore`, `CertAuthStore`, `BootstrapTokenStore`.
2. `UpsertAuthenticate` is transactional: it resets push info, clears the pending queue, bootstrap token, unlock token, and cert association in one operation, so re-enrollment never leaks state.
3. `TouchLastSeen` and cursor pagination on `List` from the start; `Clear` takes a filter and returns the count so backends can index and batch it.
4. `NotNow` handling is persisted per command with backoff metadata instead of per connection.
5. A `storagetest` contract suite that every backend runs, including concurrency and idempotency cases, so the in-memory, SQLite, PostgreSQL, and MySQL backends behave identically.

## Verified by

1. `storagetest.RunEnrollmentSuite`, `RunCommandQueueSuite`, `RunPushSuite`, `RunCertAuthSuite`, `RunBootstrapTokenSuite` on `inmem`.
2. `RunEnrollmentSuite/ReenrollClearsState`.
3. `RunEnrollmentSuite/ListPagination`, `RunCommandQueueSuite/ClearFilter`.
4. `RunCommandQueueSuite/NotNowBackoff`.
5. Same suites run by the SQL backends in the storage backends PR.

## Rejected alternatives

- One monolithic storage interface: harder to fake and to implement partially.
- Storing raw plists only: keeps the door open for later, but typed enrollment records are what every consumer needs.
