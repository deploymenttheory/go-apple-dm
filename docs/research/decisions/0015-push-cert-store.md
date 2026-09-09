# 0015: Push certificate store

## Context

APNs credentials are selected by topic and can be renewed while the server is running.

## Decision

This store remains MDM-only. Ordinary app provider identities use a separate encrypted state namespace and separate API actions, as described in [0049](0049-server-managed-app-push.md).

`pki/pushcert` parses standard-library-supported PEM key formats, verifies key/certificate pairing and derives the topic from the certificate UID. Store writes validate the topic and validity period and increment a version under a lock. Lists expose certificate metadata without private keys; SQL private-key columns are sealed.

`server/pushnotify` caches certificates per topic. A stale cache entry checks the stored version and reloads when it changes. A failed reload returns an error. The default cache TTL is 30 seconds; a zero TTL checks each time. `ExpiringCerts` supports scheduled expiry checks.

## Rationale

Versioned credentials support renewal without restarting transports or reading full key material for every push. A leaf parsing package lets storage validate uploads without importing the notifier.

## Constraints

Rotation becomes visible when the cache revalidates. Operators must renew before expiry and retain the topic expected by enrolled devices. Listing certificates is separate from privileged key retrieval.

## Verification

Parser and storage tests cover key formats, chain retention, mismatches, validity, topic derivation and version increments. Notifier tests cover caching, reloads, failed reads and expiry queries.

## References

- [pki/pushcert](../../../pki/pushcert)
- [storage/pushcert.go](../../../storage/pushcert.go)
- [server/pushnotify](../../../server/pushnotify)
- <https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers>
- <https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices>
- <https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens>
- <https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns>
- <https://developer.apple.com/documentation/devicemanagement/check-in>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@4948319`, `storage/pushcert.go`, `storage/mysql/pushcert.go`, `storage/kv/pushcert.go`, `http/api/pushcert.go`, `cryptoutil/cryptoutil.go`, `TopicFromPEMCert`
- `micromdm/micromdm@904493b`, `platform/config/builtin/db.go`, `platform/apns/push.go`, `platform/apns/service.go`, `platform/apns/builtin/db.go`
- `fleetdm/fleet@b44343c`, `server/mdm/nanomdm/storage/mysql/pushcert.go`, `server/datastore/mysql/apple_mdm.go`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/models.py`, `PushCertificate`, `zentral/contrib/mdm/forms.py`, `zentral/contrib/mdm/apns.py`
