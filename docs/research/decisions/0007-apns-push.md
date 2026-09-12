# 0007: APNs push for MDM

## Context

APNs wakes a managed device so that it can contact the command endpoint. A push response does not report command execution.

## Decision

`push.Pusher` returns a result per enrollment with a classified `Outcome`, reason and optional retry delay. The APNs client uses certificate authentication and HTTP/2, with clients cached per topic. Push certificates can be supplied from files or a store; expiry is checked before sending.

The notifier resolves channel-specific push information and publishes outcomes. Coalescing combines repeated notifications within a configured window. Only APNs HTTP 410 produces the invalid-token outcome; the caller decides whether to retire the stored token. Request/configuration failures and retryable transport failures remain distinct (record 0042).

## Rationale

Typed outcomes let callers distinguish an inactive token from a request that must be corrected. Coalescing reduces duplicate wake requests without changing command queue state.

## Constraints

APNs acceptance is not device delivery or command acknowledgement. Missing or invalid `Retry-After` is represented as zero; retry policy belongs to the caller. An APNs 400 such as `BadDeviceToken` does not establish that an enrollment is inactive.

## Verification

APNs tests cover status/reason mapping, per-topic clients and certificate expiry. Notifier and coalescing tests use scripted push fakes and an in-process APNs server.

## References

- [appleplatformservices/push](../../../devicemanagement/appleplatformservices/push)
- [appleplatformservices/push/apns](../../../devicemanagement/appleplatformservices/push/apns)
- [server/pushnotify](../../../server/pushnotify)
- <https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers>
- <https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens>
- <https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@main`, `push/nanopush/*.go`, `push/buford/*.go`, `storage/pushcert.go`, `mdm/push.go`
- `RobotsAndPencils/buford`
- `sideshow/apns2`
- `fleetdm/fleet@main`, `cmd/apple-apns-mock`, `server/mdm/apple/*push*`
