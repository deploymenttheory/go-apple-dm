# 0042: Push failure classification

## Context

A failed push can mean that a device token is inactive, a request is invalid, or a transient failure occurred. These outcomes require different handling.

## Decision

`OutcomeInvalidToken` is produced by HTTP 410. `BadDeviceToken` and `DeviceTokenNotForTopic` are rejected requests, not evidence that the enrollment is inactive. Request/configuration refusals publish `PushRejected`; invalid tokens publish `PushTokenInvalid`. `IdleTimeout` is retryable despite its 400 status.

`Result.Sent` and `TokenInvalid` derive from one `Outcome`. `Classify` is public. `RetryAfter` reflects a valid APNs header and remains zero otherwise. The DDM notifier retains rejected work with an error and backoff; an invalid-token outcome completes the pending change group.

## Rationale

A topic or environment misconfiguration can affect many devices at once. Retaining it as a request rejection avoids interpreting configuration failure as mass device inactivity. Distinct outcomes preserve diagnostic information for callers.

## Constraints

The APNs classification and the notifier's retry scheduling are separate: the notifier still backs off rejected work so it can resume after correction. APNs acceptance does not establish command delivery. Unknown statuses remain classified conservatively by the implementation.

## Verification

APNs tests cover every documented reason and unknown status values. Notifier tests distinguish rejections from inactive tokens and verify rejected DDM changes remain pending. Admin tests expose the same distinction.

## References

- [appleplatformservices/push](../../../appleplatformservices/push)
- [appleplatformservices/push/apns](../../../appleplatformservices/push/apns)
- [server/pushnotify](../../../server/pushnotify)
- [server/ddmsync](../../../server/ddmsync)
- <https://developer.apple.com/documentation/usernotifications/handling-notification-responses-from-apns>
- <https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens>

Reference source identifiers and paths (relative to the named project):

- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a`, `server/mdm/apple/apns_errors.go`
- `server/mdm/apple/apple_mdm.go:1843-1879`, `server/mdm/apple/apns_errors_test.go:76-77`
- `server/mdm/apple/commander.go:952-989`
- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10`, `push/nanopush/provider.go:41-66`
- `push/push.go:12-15`, `push/service/service.go`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107`, `platform/apns/service.go:97-101`
- `platform/apns/push.go:80-83`
- `zentralopensource/zentral@6b93d01d1bc8471ed98807b02a26b83452e8c8b7`, `zentral/contrib/mdm/apns.py:59-77`
- `micromdm/nanohub@3d73c1a83d5a042bfa5d31ba98d32de996007667`, `cmd/nanohub/nanohub.go:97`
- `ddm/notifier.go`, `docs/research/decisions/0041-closed-apple-vocabularies-as-constants.md`
