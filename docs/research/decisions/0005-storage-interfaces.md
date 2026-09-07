# 0005: Storage interfaces, in-memory backend, contract suite

## Context

Enrollment lifecycle, command delivery, certificate ownership and escrowed tokens require consistent persistence behavior across backends.

## Decision

`storage.Store` composes concern-specific interfaces, including enrollment, queue, push, certificate, bootstrap-token, user-authentication and migration operations. Methods take contexts and return shared sentinel errors.

`UpsertAuthenticate` resets enrollment state transactionally, clearing pending work, push state, escrowed tokens and the active certificate pin while retaining certificate history. Device lifecycle changes also affect dependent user channels. Lists use cursors, and command delivery persists `NotNow` retry state. `Clear` accepts a filter and returns the number affected.

## Rationale

Narrow interfaces allow consumers to implement or fake only the operations they need. The in-memory backend and shared contract suites define behavior independently of SQL dialects.

## Constraints

Certificate history is distinct from the live pin and from account-driven certificate associations (records 0014 and 0047). Store contracts do not make operations across separate domain stores a distributed transaction.

## Verification

`storage/storagetest` covers lifecycle, pagination, idempotency, queue outcomes, certificate races, token storage and export/import. SQL backends run the same suites.

## References

- [storage](../../../storage)
- [storage/inmem](../../../storage/inmem)
- [storage/storagetest](../../../storage/storagetest)
- <https://developer.apple.com/documentation/devicemanagement/check-in>
- <https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@main`, `storage/storage.go`, `storage/mdm.go`, `storage/queue.go`, `storage/push.go`, `storage/pushcert.go`, `storage/certauth.go`, `storage/bstoken.go`
- `jessepeterson/kmfddm@main`, `storage/*.go`
