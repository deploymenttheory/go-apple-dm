# 0007: APNs push for MDM

Status: accepted
Date: 2026-09-01
Phase: 3

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers>
- Doc: <https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens>
- Doc: <https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns> (headers, status codes, reason strings)
- YAML: `third_party/device-management/mdm/checkin/tokenupdate.yaml` (Topic, PushMagic, Token)

## References read

- `micromdm/nanomdm@main` `push/nanopush/*.go` (own HTTP/2 client), `push/buford/*.go` (adapter), `storage/pushcert.go` (stale token per topic), `mdm/push.go`
- `RobotsAndPencils/buford` (archived) as used by micromdm, nanomdm, Fleet
- `sideshow/apns2` (maintained general client, not MDM specific)
- `fleetdm/fleet@main` `cmd/apple-apns-mock` (Redis-coordinated mock) and `server/mdm/apple/*push*`

## Known pitfalls found

- `buford` is archived yet still a direct dependency of every reference; nanomdm added `nanopush` to stop depending on it.
- Invalid tokens must be recorded so the server stops pushing and can surface inactive devices; NanoMDM leaves this to the caller. **Corrected by record 0042**: this originally read "410 `Unregistered`, 400 `BadDeviceToken`, `DeviceTokenNotForTopic`", but the Apple page cited above says nothing about any reason string, and the two 400 reasons describe a request the sender must fix rather than a device that is gone. Only 410 retires an enrollment.
- A push storm on a busy device (many commands enqueued) sends one APNs request per command; Fleet coalesces in its cron.
- Push certificates expire yearly; NanoMDM's `IsPushCertStale` token forces a reload but nothing tracks expiry.

## What they do

- **nanopush**: one `http.Client` per topic with the push certificate as the TLS client cert, `POST /3/device/<token>` with `{"mdm":"<magic>"}`, `apns-topic`, `apns-push-type: mdm`, parses `apns-id` and the JSON `reason`.
- **Fleet**: APNs mock for load tests; pushes triggered by reconcile cron.

## What we do better

1. `push.Pusher` is an interface with a `Result` per enrollment that says whether the token is invalid and how long to back off (`Retry-After`), so the service can publish `PushTokenInvalid` and skip dead tokens instead of retrying forever. Record 0042 replaces the two booleans with a closed `Result.Outcome` that also distinguishes a refused request from a dead token.
2. `push/apns` uses only `net/http` (HTTP/2 negotiated by TLS) with one client per topic, certificates fetched from a `CertStore` that can rotate, and certificate expiry surfaced as an error before the request.
3. `push.Coalesce` collapses repeated pushes to the same enrollment inside a window; `push.Notifier` looks up push info from storage, sends, and publishes events, so callers push by enrollment id.
4. `pushtest` provides a scripted fake `Pusher` and an in-process APNs server so every path (410, 429 with Retry-After, 400 reasons, 5xx) is testable offline.

## Verified by

1. `push/apns.TestStatusMapping`, `push.TestNotifierPublishesInvalidToken`, and from record 0042 `push/apns.TestClassifyEveryDocumentedReason`.
2. `push/apns.TestPerTopicClientsAndExpiry`.
3. `push.TestCoalesce`.
4. `pushtest.TestServerScripting`.

## Rejected alternatives

- `sideshow/apns2`: fine library, but MDM needs only one request shape and certificate auth; a dependency for 60 lines of HTTP is not worth the surface.
- `buford`: archived.
