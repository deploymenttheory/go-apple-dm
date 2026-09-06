# 0014: Certificate association history and reuse policy

Status: accepted
Date: 2026-09-02
Phase: 4

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> (`Authenticate` and `TokenUpdate` carry the identity the device will sign with)
- Doc: <https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices>
- YAML: `third_party/device-management/mdm/checkin/authenticate.yaml`
- YAML: `third_party/device-management/mdm/checkin/tokenupdate.yaml`

## References read

- `micromdm/nanomdm@4948319` `service/certauth/certauth.go`, `storage/mysql/certauth.go`, `storage/kv/certauth.go`, `storage/file/certauth.go`, `cmd/nanomdm/main.go` (`/migration` mounted without certauth)
- `micromdm/micromdm@904493b` `mdm/checkin.go`, `platform/device/udidauth.go`, `platform/device/builtin/db.go`
- `fleetdm/fleet@b44343c` `server/mdm/nanomdm/service/certauth/certauth.go`, `server/mdm/nanomdm/storage/mysql/certauth.go` (`EnrollmentFromHash`), `server/datastore/mysql/migrations/tables/` (renewal repair migration)
- `zentralopensource/zentral@b10dd22` `zentral/contrib/mdm/public_views/mdm.py`, `zentral/contrib/mdm/models.py` (`EnrolledDevice` fingerprint and attestation fields)

## Known pitfalls found

- NanoMDM: the SQL `IsCertHashAssociated` accepts any hash ever associated, so old identities stay valid forever, while the file and KV backends overwrite; behaviour diverges by backend.
- NanoMDM: the `/migration` endpoint bypasses certauth, so a replayed check-in is accepted with no identity check.
- MicroMDM: trust on first use with an unconditional overwrite of the stored identity on every `Authenticate`; user enrollments skip binding; the SCEP challenge is static.
- Fleet: removed warn-only mode and never enables retroactive association, then needed a data-repair migration for renewals stuck on the old hash; `EnrollmentFromHash` queries a table that does not exist.
- Zentral: stores certificate fingerprints and attestation OIDs on `EnrolledDevice` but never compares them at check-in.

## What they do

- **NanoMDM**: `cert_auth_associations(id, sha256)` with a composite key; `AssociateCertHash` inserts and ignores duplicates; `HasCertHash`, `EnrollmentHasCertHash`, `IsCertHashAssociated` booleans; `allowRetroactive` and `warnOnly` flags on the service.
- **MicroMDM**: `udidauth.go` compares the UDID in the body with the certificate; `platform/device` stores the identity on the device record and overwrites it on each check-in.
- **Fleet**: forked NanoMDM certauth with warn-only removed; `EnrollmentFromHash` for renewal lookups; a migration to repair stuck rows.
- **Zentral**: `EnrolledDevice.cert_fingerprint`, `cert_not_valid_after`, attestation extension values written on enrollment; the check-in view trusts the mTLS or signature header alone.

## What we do better

1. History is append-only and persistent: `cert_associations(enrollment_id, cert_hash, associated_at)` is written in the same transaction as `AssociateCert`, survives re-enrollment, and is read through `CertHistory` (per enrollment, oldest first) and `CertHashHistory` (per hash). All four backends return the same rows.
2. Reuse is a service policy, not a boolean: `CertReusePolicy` (set through `Config.CertReuse`) with `DenyCertReuse` as the default rejects an `Authenticate` whose hash appears in another enrollment's history with `CodeForbidden` and `ErrCertReused` (a custom policy's error is wrapped so `errors.Is(err, ErrCertReused)` still holds), and publishes `CertReuseDenied` with the history rows; `AllowCertReuse` is an explicit opt-in. The policy is not consulted under `PinOff`.
3. Retroactive pinning in `authorize()` only pins a hash that has never been seen on another enrollment; otherwise `PinEnforce` refuses with `ErrCertReused` and `PinWarn` logs, and in both cases nothing is written.
4. The live pin stays unique: a race for the same hash yields exactly one success and `ErrConflict` for the rest (`codeForStorage` maps it to `CodeForbidden`), and `AllowCertReuse` cannot override a live pin. A failed history insert rolls back the pin.
5. User channels inherit the device's history, so a user enrollment can be checked against the identity its device presented.

## Verified by

1. `storagetest.RunCertAuthSuite/HistoryAppendOnly`, `RunCertAuthSuite/HashHistoryAcrossEnrollments`, `RunCertAuthSuite/HistoryUnknownAndEmpty` (prove claim 1; would fail on NanoMDM because file and KV overwrite while SQL retains, and on MicroMDM because the identity is overwritten).
2. `service.TestCertReuseDeniedAcrossEnrollments`, `service.TestCertReuseAllowedByPolicy` (prove claim 2; would fail on every reference because none rejects a second enrollment that presents a known identity).
3. `service.TestRetroactivePinOnlyIfUnseen` (proves claim 3; would fail on NanoMDM because `allowRetroactive` pins regardless of other enrollments).
4. `storagetest.RunConcurrencySuite/CertPinRace`, `sqlite.TestWriteFailuresSurface` (history insert fails, pin rolled back) (prove claim 4; would fail on NanoMDM because `AssociateCertHash` ignores duplicates silently).
5. `storagetest.RunCertAuthSuite/HistoryViaUserChannel`, `service.TestStorageFailuresAreInternal` (`CertHashHistory` failure through `storagetest.Failing` is `CodeInternal`) (prove claim 5; would fail on MicroMDM because user enrollments are never bound).

## Rejected alternatives

- `allowRetroactive` and `warnOnly` booleans (NanoMDM): two flags cannot express "pin if unseen elsewhere"; the policy function and `PinMode` do.
- Pruning history on re-enrollment: the value of history is exactly the record of what was presented before.
- A global "allow any identity" mode that also overrides live pins: a pin exists to stop a second device using an identity; the policy only governs history, never the pin.
- Storing history as JSON on the enrollment row: not queryable by hash, and `CertHashHistory` needs the reverse index.
