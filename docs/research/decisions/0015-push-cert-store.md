# 0015: Push certificate store

Status: accepted
Date: 2026-09-02
Phase: 4

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers> (push certificate topic in the certificate UID)
- Doc: <https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices> (renewal before expiry)
- Doc: <https://developer.apple.com/documentation/devicemanagement/dealing-with-inactive-managed-devices-and-invalid-push-tokens>
- Doc: <https://developer.apple.com/documentation/usernotifications/sending-notification-requests-to-apns>
- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> (`Topic` in `Authenticate` and `TokenUpdate` must match the certificate)
- YAML: `third_party/device-management/mdm/checkin/authenticate.yaml` (`Topic`)
- YAML: `third_party/device-management/mdm/checkin/tokenupdate.yaml` (`Topic`, `PushMagic`, `Token`)

## References read

- `micromdm/nanomdm@4948319` `storage/pushcert.go`, `storage/mysql/pushcert.go`, `storage/kv/pushcert.go`, `http/api/pushcert.go`, `cryptoutil/cryptoutil.go` (`TopicFromPEMCert`)
- `micromdm/micromdm@904493b` `platform/config/builtin/db.go` (PKCS#1 only), `platform/apns/push.go`, `platform/apns/service.go`, `platform/apns/builtin/db.go`
- `fleetdm/fleet@b44343c` `server/mdm/nanomdm/storage/mysql/pushcert.go`, `server/datastore/mysql/apple_mdm.go` (asset rows, upload path)
- `zentralopensource/zentral@b10dd22` `zentral/contrib/mdm/models.py` (`PushCertificate`), `zentral/contrib/mdm/forms.py`, `zentral/contrib/mdm/apns.py`

## Known pitfalls found

- NanoMDM: the key is a plaintext column; every push runs a staleness query; only the last `CERTIFICATE` block of the PEM is kept; expiry is never checked at push time.
- MicroMDM: the key must be PKCS#1; a failed reload keeps the old client silently; expiry is detected by matching a TLS error string; the device's `Topic` is never compared with the certificate.
- Fleet: a 5-minute per-process staleness cache; upload is a soft delete followed by an insert with no transaction; expiry is only checked on demand.
- Zentral: proves key and certificate pairing by an encrypt round trip and rejects topic changes, but the cache only refreshes on a later `not_after`, and an expired certificate silently leaves devices unpokeable.

## What they do

- **NanoMDM**: `push_certs(topic, cert_pem, key_pem, stale_token)`; `RetrievePushCert` returns PEM plus a stale token; `IsPushCertStale` compares tokens on every push; `StorePushCert` parses the certificate to find the topic; `PUT /v1/pushcert` on the API.
- **MicroMDM**: certificate and key in the BoltDB config bucket; `platform/apns` builds a `Push` client at start and on a config change; push errors wrapped with "possibly expired or invalid APNs certificate".
- **Fleet**: NanoMDM's MySQL store with the cert in `mdm_config_assets`; a per-process cache with a fixed TTL; the upload handler validates the pair and topic before writing.
- **Zentral**: `PushCertificate` model with `topic`, `not_after`, and a sealed private key through the secret engine; the form checks pairing by a sign and verify round trip; `apns.py` caches clients keyed by topic and refreshes when `not_after` moves.

## What we do better

1. `push/pushcert.Parse` is a leaf package: `tls.X509KeyPair` proves pairing for every PEM key format the standard library accepts (PKCS#1, PKCS#8, EC), the topic comes from the certificate UID, and `NotAfter` is exposed. `storage` imports only this package, so there is no cycle with `push`.
2. The store validates on write: `StorePushCert` (through `storage.ValidatePushCert`, shared by `inmem` and `sqlcommon`) rejects unpaired or unparseable PEM with `ErrInvalid`, derives the topic when the caller gives none and rejects a mismatch otherwise, refuses a certificate that is expired or not yet valid at the given time, and bumps `Version` under a row lock (`SELECT ... FOR UPDATE`, the store mutex in `inmem`) so a concurrent overwrite is ordered.
3. `PushCerts` lists topic, `NotAfter`, and `version` without keys, and `key_pem` is sealed under 0013, so an operator listing never exposes key material.
4. `push.NewStoreCertStore` caches per topic and revalidates by `PushCertVersion` once per TTL (`DefaultCertTTL` is 30 seconds; `WithCertTTL(0)` reproduces NanoMDM's per-push check; `WithCertClock` for tests). A cold miss loads the record directly, since `PushCert` returns the `Version` with it; a stale entry asks `PushCertVersion` first and reloads only when the number moved, and a failed reload returns an error instead of serving the old certificate. `apns.Client` already rebuilds its transport when the leaf changes, so a rotation takes effect without a restart and without a query per push.
5. `push.ExpiringCerts(ctx, store, now, within)` gives operators a scheduled check without a new event type or a hidden timer.

## Verified by

1. `pushcert.TestParse` (subtests `rsa pkcs1`, `rsa pkcs8`, `ec sec1`, `chain keeps both certificates`, `key mismatch`, `no topic`, `invalid inputs`), `pushcert.TestTopicFromCert`, `storagetest.RunPushCertSuite/Invalid` (garbage PEM, empty input, key mismatch, no topic in the UID) (prove claim 1; would fail on MicroMDM because a PKCS#8 key is rejected, and on NanoMDM because a mismatched pair is stored).
2. `storagetest.RunPushCertSuite/Invalid` (topic mismatch, expired, not yet valid; nothing is stored), `RunPushCertSuite/StoreGetList` (topic derived from the certificate when the caller passes none), `RunPushCertSuite/OverwriteBumpsVersion`, `sqlite.TestWriteFailuresSurface` (prove claim 2; would fail on NanoMDM and Fleet because an expired certificate is accepted on write).
3. `storagetest.RunPushCertSuite/StoreGetList` (listing carries no key, returned copies do not alias), `RunPushCertSuite/Invalid` (unknown topic is `ErrNotFound`), `sqlite.TestRawColumnIsNotPlaintext` (prove claim 3; would fail on NanoMDM because the key column is plaintext and the listing API returns it).
4. `push.TestStoreCertStoreCachesAndReloads` (one store read per TTL, reload after a renewal bumps the version), `push.TestStoreCertStoreErrors` (unknown topic, version and load failures, unparseable stored pair), `apns.TestPushWithStoreCertStore` through `pushtest` (prove claim 4; would fail on MicroMDM because a failed reload keeps the old client silently, and on Zentral because a same-`not_after` rotation is never picked up).
5. `push.TestExpiringCerts` (proves claim 5).

## Rejected alternatives

- A string stale token compared on every push (NanoMDM): a query per push on the hot path; a monotonically increasing `version` checked per TTL gives the same freshness bound.
- A new `PushCertExpiring` event: emitting it requires a timer inside the library; the helper leaves scheduling to the deployment.
- Loading the certificate from files inside the store: `push.StaticCertStore` already does that and the e2e harness keeps using it.
- Storing one global certificate without a topic key: multi-tenant deployments have several topics and the device reports which one it enrolled under.
