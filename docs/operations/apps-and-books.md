# Apps and Books licensing

[`appsbooks`](../../devicemanagement/appleplatformservices/appsbooks/doc.go) implements
Apple's version 2 licensing API for device/user apps and user books. The caller
owns persistence, notification hosting and installation. Catalog metadata and
subscriptions are outside this client.

## Location setup

Create one shared `Client` per location with the downloaded base64 `sToken`, your
persisted MDM ID and, when known, the expected library UID. The original sToken is
the bearer credential. The decoded `token` member is not used as the HTTP token.
Replace the client when replacing credentials. Do not log configuration values.

Read `ClientConfig`, then explicitly call `SetClientConfig` to claim an unclaimed
location or configure this MDM's notifications. Another MDM ID returns
`ErrOwnership`. Licensing mutations require an already claimed location and check
configuration before writing. Every authenticated response checks `mdmInfo` when
present and the expected `uId`. Apple has no compare-and-swap ownership operation;
coordinate any administrative transfer between MDM services.

`ServiceConfig` caches dynamic URLs and limits for at most five minutes, refreshing
on the next call. The client bounds requests by current asset, target and user
limits and spaces HTTP requests by the location's rate limit. Separate processes
must coordinate their aggregate rate. Discovered credentialed endpoints must stay
on the configured HTTPS origin; redirects are not followed.

## Licensing and user lifecycle

Use `Assets`, `Assignments` and `Users` for single-page reads, or their `Walk…`
methods for bounded sequential traversal. Walkers return the first page's version
only after every page and callback succeeds. Preserve that version for the next
incremental assignments/users query. Assets do not accept `sinceVersionId`.
An empty page may still have a next page; changes to `totalPages` are not an end signal.

For a device app, check the purchased `AssetRecord` with
`CheckAssignment(true, false)` and associate its `adamId`/`pricingParam` with serial
numbers. For user apps or books, create or look up a user, then associate with
`clientUserId`. Registered users still need an associated Apple Account. Use
`InvitationURL` and your own communication flow for invitations; observe
`USER_ASSOCIATED` or query `Users` to establish association. `RequestUser` also
models Apple's `managedAppleId` field.

Books require user assignment and cannot be reclaimed or reassigned. Consult
`ProductType`, `DeviceAssignable` and `Revocable`; the service remains authoritative.
`Disassociate` removes selected revocable assignments. `Revoke` removes all
revocable assets from specified targets. `CreateUsers`, `UpdateUsers` and
`RetireUsers` implement the documented user lifecycle.

## Completion, notifications and retries

Persist each mutation's returned `EventID` with the location and requested tasks.
An accepted event is not completed licensing. `Status.Successful` requires COMPLETE,
matching positive requested/completed counts and no failures. Inspect partial
failure numbers and `errorInfo`; unknown fields inside errorInfo remain available.
The [compiled example](../../devicemanagement/appleplatformservices/appsbooks/example_test.go)
constructs an installation command only after licensing success.

Host an HTTPS notification endpoint and use `DecodeNotification` with a dedicated
shared token and expected UID. Handle `TEST_NOTIFICATION` before submitting its
configuration: Apple rejects configuration when the test cannot be delivered.
Select the typed payload using `Notification.Type`; unknown types remain raw.
Persist changes and deduplication records for `(UID, Notification.ID)` before
returning 2xx.
One SUCCESS batch does not prove completion of a larger event.

If notifications do not establish completion within five minutes, query EventStatus.
Poll PENDING no more frequently than every 30 seconds. If it remains pending after
ten minutes, reconcile and explicitly decide whether to resubmit. No background
scheduler or automatic mutation replay is hidden inside the client.

`ReadRetries` optionally enables bounded GET retries for HTTP 429/5xx, honoring
Retry-After as a minimum wait. POST is attempted once. Transport failures can mean
an unknown mutation outcome. Errors expose Apple numbers and retry timing while
ordinary error strings omit private response text; treat detailed error fields as
sensitive. Pass context deadlines to bound waits and network calls.

## Sources

Wire models were checked against Apple DocC field tables and examples on 2026-09-15:
[authentication and ownership](https://developer.apple.com/documentation/devicemanagement/getting-started-with-the-management-api),
[assets](https://developer.apple.com/documentation/devicemanagement/managing-assets),
[users](https://developer.apple.com/documentation/devicemanagement/managing-users),
[pagination](https://developer.apple.com/documentation/devicemanagement/using-paginated-endpoints),
[dynamic limits](https://developer.apple.com/documentation/devicemanagement/service-config),
[client configuration](https://developer.apple.com/documentation/devicemanagement/clientconfigrequest),
[event status](https://developer.apple.com/documentation/devicemanagement/statusresponse),
and [notifications](https://developer.apple.com/documentation/devicemanagement/subscribing-to-notifications).
Tests use local HTTPS responses based on those contracts; no live Apple credentials
or Apple-service acceptance is claimed.
