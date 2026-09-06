# 0042: A push failure is not a dead device

Status: accepted
Date: 2026-09-03
Phase: 9

## Apple sources

- Doc: <https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns>
  — the 32 reason strings and the status each is paired with. The only instruction not to repeat a
  push is on **410**: `Unregistered` reads "There is no need to send further pushes to the same
  device token". `BadDeviceToken` (400) instead reads "Verify that the request contains a valid
  token **and that the token matches the environment**", and `DeviceTokenNotForTopic` (400) reads
  "The device token doesn't match the specified topic" — both are instructions to the sender, and
  neither states anything about the device.
- Doc: <https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens>
  — cited by record 0007 as the authority for treating two 400 reasons as invalid tokens. It says
  nothing of the kind. Its entire guidance is that a device is inactive when it "fails to respond to
  push notifications after a specified time", that the period "can vary according to your IT policy",
  and that "a good time period to use ranges from several days to one week". Apple also notes that
  its "push notification servers cache your last push notification and deliver it to the device when
  it reconnects", which is the opposite of a reason to retire an enrollment on one response.

## References read

- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a` `server/mdm/apple/apns_errors.go`,
  `server/mdm/apple/apple_mdm.go:1843-1879`, `server/mdm/apple/apns_errors_test.go:76-77`,
  `server/mdm/apple/commander.go:952-989`
- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10` `push/nanopush/provider.go:41-66`,
  `push/push.go:12-15`, `push/service/service.go`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107` `platform/apns/service.go:97-101`,
  `platform/apns/push.go:80-83`
- `zentralopensource/zentral@6b93d01d1bc8471ed98807b02a26b83452e8c8b7` `zentral/contrib/mdm/apns.py:59-77`
- `micromdm/nanohub@3d73c1a83d5a042bfa5d31ba98d32de996007667` `cmd/nanohub/nanohub.go:97`
- Ours: `docs/research/decisions/0007-apns-push.md`, `push/doc.go`, `push/apns/apns.go`,
  `ddm/notifier.go`, `docs/research/decisions/0041-closed-apple-vocabularies-as-constants.md`

## Known pitfalls found

- **Our own record 0007 is not supported by its citation.** Its pitfall list asserts "Invalid tokens
  (410 `Unregistered`, 400 `BadDeviceToken`, `DeviceTokenNotForTopic`) must be recorded so the server
  stops pushing", citing the "inactive managed devices and invalid push tokens" page. That page never
  mentions an APNs reason string, a status code, or `BadDeviceToken`; it describes an inactivity
  policy measured in days. The two 400 reasons entered the classification as an inference.
- **`push/doc.go` and `push/apns/apns.go` already disagreed.** The package doc says "a 410 from APNs
  marks the token invalid"; the code also marked two 400 reasons invalid. The documented behaviour
  was the correct one.
- **The failure is fleet-wide, not per device.** `BadDeviceToken`'s own text names an environment
  mismatch, and `DeviceTokenNotForTopic` names a topic mismatch. A sandbox push certificate against
  production tokens, or a certificate whose topic does not match the enrollment's, answers this way
  for **every device at once**. The old classification turned one misconfiguration into a fleet-wide
  `PushTokenInvalid` storm.
- **`ddm/notifier.go:310` treated an invalid token as delivered.** `case r.Err != nil && !r.Invalid`
  means a result marked invalid completes its change group: the change is marked pushed, not retried,
  and no `LastError` is recorded. Combined with the point above, a topic mismatch marked a fleet's
  worth of declaration changes delivered that no device was ever woken for, silently.
- **Permanent configuration errors were retried for ever.** `BadTopic`, `TopicDisallowed`,
  `BadCertificate` and `BadCertificateEnvironment` fell into the `default` branch as `ErrUpstream`,
  the same class as a 500, so the notifier retried an identical request that can only be refused
  identically until an operator noticed by other means.
- **`retryAfter` invented Apple's answer.** It returned 30s when the header was absent or unparsable,
  so a missing `Retry-After` was indistinguishable from `Retry-After: 30`. No caller in the tree read
  the field, so the default was never a service to anyone — only a loss of signal.
- **`IdleTimeout` is a 400 that is not permanent.** It is the HTTP/2 connection going idle, not a
  fault in the request, so a rule of "every 400 is permanent" would stop retrying a transient.
- **Two booleans could disagree.** `Result{Sent, Invalid}` admits `{true, true}` and `{false, false}`,
  neither of which means anything. A closed `Outcome` cannot express a contradiction.

## What they do

- **fleet**: defines 5 of Apple's 32 reasons and branches on exactly one — `Unregistered` calls
  `ds.MDMTurnOff` for the host (`apple_mdm.go:1860`). It **deliberately declines** to treat
  `BadDeviceToken` the same way; `apns_errors_test.go:76` is named "BadDeviceToken does not turn off
  MDM". It also ignores the HTTP status entirely: `APNSReason` unwraps to `*nanopush.JSONPushError`
  and returns `.Reason`, so the 410 plays no part. Every failure then collapses into an
  `*APNSDeliveryError` whose `StatusCode()` is a flat 502 regardless of cause, so nothing downstream
  can distinguish a rejection from an outage.
- **nanomdm**: parses `reason` into `JSONPushError` and only concatenates it into a message string.
  It branches on neither the reason nor 410, and `push.Response` is `{Id, Err}` — there is no field
  in which to say "invalid", so the caller is left to string-match.
- **nanohub**: wires nanomdm's push service unchanged, inheriting all of the above.
- **micromdm**: reduces the push result to a bare error and `fmt.Println`s it (`service.go:97-101`).
- **zentral**: never calls `r.json()`. A 410 and a 400 are both "stop retrying" and a 5xx is retried
  with random exponential back-off; **no token is ever marked invalid on a push failure**.
- **Nobody** distinguishes "this device is gone" from "this request was refused", and nobody
  exposes either as a metric label.

## What we do better

1. **Only 410 retires an enrollment, because only 410 is what Apple says.** `OutcomeInvalidToken` is
   produced by status 410 alone. `BadDeviceToken` and `DeviceTokenNotForTopic` become
   `OutcomeRejected`, which no longer publishes `PushTokenInvalid` and no longer completes a DDM
   change. Fleet reaches the same verdict for `BadDeviceToken` and states it in a test; we reach it
   for both reasons, from the status rather than from a per-reason allowlist, and we record why.
2. **A refused request is its own outcome and its own event.** `OutcomeRejected` and
   `event.PushRejected` name the case that needs an operator rather than a retry — a wrong topic, a
   mismatched or expired push certificate, the wrong environment, a malformed push. No reference has
   this category at all: fleet flattens every failure to a 502, nanomdm to an error string, zentral
   to a boolean. It is the event to alert on, because its cause is normally shared by the whole topic.
3. **Permanent refusals are no longer retried as transients, and transients are still retried.**
   `Classify` keys on the status Apple pairs each reason with, so the 400, 403, 404, 405 and 413
   families are permanent while 429, 503, 500 and transport failures are not — with `IdleTimeout`
   carved out of the 400 family as the one that is a connection event rather than a bad request.
4. **The DDM notifier no longer marks undelivered work delivered.** Only a token APNs says is dead
   completes a change group; a rejection now fails it, applies the existing back-off, and leaves the
   APNs reason in `LastError`, so "why did this device never get the declaration" has an answer.
5. **`RetryAfter` reports Apple, not us.** It is what APNs asked for, and zero when APNs asked for
   nothing; `apns.DefaultRetryAfter` is exported for a caller that wants a floor. This follows the
   same rule as the notifier's dedupe key: the library's opinion is available, named, and the
   consumer's to take.
6. **The outcome cannot contradict itself.** `Sent()` and `TokenInvalid()` are derived from
   `Outcome`, so the pair `{Sent: true, Invalid: true}` that the old struct admitted is now
   unrepresentable. `Outcomes` is the closed list, which is also the bounded metric label phase 9
   needs for `apple.apns.push.duration{result}`.
7. **`Classify` is exported.** `Result` records `Status` and `Reason`, so a caller holding a stored
   result, or writing its own `Pusher`, reaches the same verdict without restating the table. No
   reference exposes its classification at all.

## Verified by

1. `apns.TestStatusMapping` (410 is `OutcomeInvalidToken`; **400 `BadDeviceToken` is
   `OutcomeRejected` and `TokenInvalid()` is false**) and `apns.TestClassifyEveryDocumentedReason`,
   which walks all 32 entries of `apns.Reasons` and asserts the outcome of each (proves claims 1 and
   3; the old code failed both for the two 400 reasons).
2. `push.TestNotifierPublishesRejectionSeparately` (a rejected enrollment raises `PushRejected` and
   a dead one raises `PushTokenInvalid`, in the same batch) (proves claim 2; the old code raised
   `PushTokenInvalid` for both).
3. `ddm.TestNotifier/RejectedPushIsNotTreatedAsDelivered` (the change stays pending, attempts
   increments, and `LastError` keeps the APNs reason) (proves claim 4; the old code completed the
   group and recorded nothing).
4. `apns.TestStatusMapping`'s 503-without-`Retry-After` case asserts `RetryAfter == 0` (proves
   claim 5; the old code returned 30s).
5. `push.TestOutcomesAreClosed` (every outcome is distinct and non-empty, and `Sent`/`TokenInvalid`
   agree with `Outcome` for all of them) (proves claim 6).
6. `apns.TestClassifyEveryDocumentedReason` calls the exported `Classify` directly, including an
   unknown status and a rejection with no reason body (proves claim 7).
7. `app.TestPushRouteSeparatesRejectionFromADeadToken` and
   `app.TestPushRouteReportsAnInvalidToken` (the admin API reports the two differently).

Failing paths: `apns.Classify(599, "SomethingNew")` and `apns.Classify(403, "")` cover the unknown
status and the empty-reason branches.

## Rejected alternatives

- **Leaving the classification as record 0007 set it.** The citation does not support it, the package
  doc contradicted it, and the failure mode is a fleet-wide false positive from a single
  misconfiguration. Fleet independently declined the same inference.
- **Keeping `Sent bool` and `Invalid bool` and adding `Rejected bool`.** Three booleans admit six
  meaningless combinations, and the bug being fixed here is exactly a caller reading one boolean and
  inferring the rest.
- **Marking a rejection invalid but not publishing the event.** The DDM notifier keys off the same
  flag, so the change would still have been marked delivered. The classification, not the
  notification, was wrong.
- **Not retrying `OutcomeRejected`.** A rejection is permanent for an *identical* request, but its
  cause — a certificate, a topic, an environment — is exactly what an operator fixes without
  restarting the server. Retrying with the existing back-off means the fleet recovers on its own
  once the cause is fixed, and `LastError` says what to fix meanwhile.
- **Inferring a fleet-wide misconfiguration from the aggregate** (for example, marking tokens invalid
  only when a minority of a batch reports `BadDeviceToken`). That is a policy a deployment may well
  want, but it is a judgement about a fleet, not a fact about a response; the library reports the
  fact and leaves the judgement to a consumer, as with the notifier's dedupe key.
- **Keeping a 30s default inside `RetryAfter`.** It presented our opinion as Apple's answer and made
  the two indistinguishable. `DefaultRetryAfter` keeps it available to anyone who wants it.
- **Classifying from the reason string rather than the status.** The reason is optional — a rejection
  may arrive with no JSON body — while the status is always present, and Apple pairs each reason with
  exactly one status. The reason refines exactly one case, `IdleTimeout`.
