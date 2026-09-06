# 0022: Change notifier

Status: accepted
Date: 2026-09-02
Phase: 5

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management> (the `DeclarativeManagement` command tells the device to synchronise)
- Doc: <https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device>
- YAML: `third_party/device-management/mdm/checkin/declarativemanagement.yaml` (command `Data` optional; the first send enables the engine)
- YAML: `third_party/device-management/declarative/protocol/tokensresponse.yaml` (the payload carried in `Data`)

## References read

- `jessepeterson/kmfddm@4b75a76` `notifier/notifier.go`, `notifier/cmd_dm.go`, `http/api/notify.go`, `http/api/declarations.go`, `http/api/sets.go`
- `fleetdm/fleet@b44343c` `server/service/apple_mdm.go` (DDM command enqueue after a declaration change), `server/datastore/mysql/apple_mdm.go` (cron reconcile)
- Record 0007 (push notifier and coalescing) for the `push.Notifier` and `push.Coalesce` contract.

## Known pitfalls found

- KMFDDM: notify runs synchronously after the API has already answered 204, so a failure is invisible to the caller.
- KMFDDM: enqueue and push failures are swallowed.
- KMFDDM: the request body buffer is drained after the first 30-id chunk, so enrollments 31 and later receive an empty command.
- KMFDDM: `DELETE` on a declaration or set never notifies, so devices keep a removed declaration until something else changes.
- KMFDDM: tokens are front-loaded into the command only when exactly one enrollment is targeted; every other device makes an extra `tokens` round trip.
- KMFDDM #11: no coalescing; a burst of uploads produces a command and a push per upload.
- Fleet: a failed enqueue is never retried; a cron reconcile eventually catches up, so the delay depends on the cron interval.

## What they do

- **KMFDDM**: `notifier.Notifier` called by the API handlers after the write; builds one `DeclarativeManagement` command per chunk; pushes through NanoMDM's API.
- **Fleet**: enqueues the command when a declaration changes; a cron job reconciles per-host rows and re-enqueues.

## What we do better

1. Change rows are written inside the mutating transaction (0020), so a committed change always has a row and a rolled-back write never notifies.
2. `ddm.Notifier{Store, Tokens, Enqueuer, Pusher, Bus, Clock, Window, Poll, Batch, Backoff}` drains pending rows grouped per enrollment and defers a group while its newest change is younger than `Window` (default 2s), so a burst becomes one command.
3. One `DeclarativeManagement` command per enrollment with `Data` always carrying that enrollment's `TokensResponse`, regardless of how many enrollments are in the batch.
4. Enqueue uses `DedupeKey: "ddm"`; when a pending DDM command already exists the change is completed without a new command and the enrollment is still pushed.
5. Failures are recorded on the change rows (`Attempts`, `LastError`, `NextAttemptAt` with backoff) and never swallowed; store failures surface from `DrainOnce`.
6. One push per batch through `push.Notifier`, and `DDMChanged` is published once per drained enrollment.
7. Deletes record changes before the row goes away, so a removed declaration or set notifies.
8. `Run(ctx)` polls every `Poll` and `Kick()` wakes it immediately; `Run` stops on context cancellation. Tests run under `testing/synctest` with a real `time.After` inside the bubble.

## Verified by

1. `ddmtest.RunAll/Changes/RecordInsideUpdate`, `/Changes/PendingByNextAttempt`, `/Changes/CompleteRemoves`, `/Changes/FailNeverDeletes` (prove claim 1).
2. `ddm.TestNotifier/CoalescesBurstWithinWindow` (proves claim 2; would fail on KMFDDM because each write notifies).
3. `ddm.TestNotifier/OneCommandPerEnrollment` (proves claim 3; would fail on KMFDDM because tokens are only attached for a single target and the 31st enrollment gets an empty command).
4. `ddm.TestNotifier/DedupeSkipCompletesAndPushes`, `/DisabledEnrollmentSkipped` (prove claim 4).
5. `ddm.TestNotifier/EnqueueFailureRecordedAndRetried`, `/PushFailureRecorded`, `/StoreFailuresSurface` (prove claim 5; would fail on KMFDDM and Fleet because a failed enqueue is dropped).
6. `ddm.TestNotifier/PublishesDDMChanged` and the push count asserted in `/CoalescesBurstWithinWindow` (prove claim 6).
7. `ddm.TestNotifier/DeleteNotifies` (proves claim 7; would fail on KMFDDM because DELETE never notifies).
8. `ddm.TestNotifier/KickWakesRunImmediately`, `/RunStopsOnContext`, `ddm.TestNewNotifier/RequiresStoreTokensEnqueuer` (prove claim 8).

## Rejected alternatives

- Notifying synchronously from the admin call (KMFDDM): the caller cannot see a failure that happens after the answer, and there is no retry.
- A cron reconcile over per-enrollment rows (Fleet): correct but slow and a full scan; the change table is the work queue.
- Sending the command without `Data`: costs every device a `tokens` round trip that the server already knows the answer to.
- A separate goroutine per change: no coalescing and unbounded fan-out under a burst.
