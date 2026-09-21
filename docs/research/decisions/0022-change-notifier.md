# 0022: Change notifier

## Context

Declaration changes need to reach devices despite enqueue failures, push failures and bursts of administrative writes.

## Decision

The notifier drains transactional change rows grouped by enrollment. It waits for the configured coalescing window, builds one `DeclarativeManagement` command per enrollment with that enrollment's tokens, and enqueues through the service using a dedupe key containing the `ddm` prefix and a digest of the declarations token. Commands for the same token share pending work and the enrollment is still pushed. A new token queues a separate command even when an earlier generation is awaiting acknowledgment or a NotNow retry.

Failures record attempts, errors and the next retry time. `Run` polls until cancellation, and `Kick` requests an earlier drain. Deletes create notification work in the same transaction as the mutation.

## Rationale

Persistent changes allow retry independently of the originating HTTP request. Service enqueue retains command-target checks, hooks and events. Grouping and deduplication reduce repeated commands for the same enrollment.

## Constraints

Queueing and APNs acceptance do not establish device synchronization. Push outcomes follow record 0042. The event bus and webhook delivery are separate from this persistent change queue.

## Verification

Notifier tests cover coalescing, per-enrollment tokens, dedupe, disabled enrollments, enqueue/push failures, storage errors, wakeups and cancellation. A real queue regression checks delivery of a newer generation after an earlier command was sent. The simulator consumes tokens embedded in commands, fetching tokens separately only when absent or when checking convergence after a manifest conflict. Store contracts verify change recording and rollback.

## References

- [server/ddmsync](../../../server/ddmsync)
- [storage/ddm](../../../devicemanagement/storage/ddm)
- <https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management>
- <https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device>

Reference source identifiers and paths (relative to the named project):

- `jessepeterson/kmfddm@4b75a76`, `notifier/notifier.go`, `notifier/cmd_dm.go`, `http/api/notify.go`, `http/api/declarations.go`, `http/api/sets.go`
- `fleetdm/fleet@b44343c`, `server/service/apple_mdm.go`, `server/datastore/mysql/apple_mdm.go`
