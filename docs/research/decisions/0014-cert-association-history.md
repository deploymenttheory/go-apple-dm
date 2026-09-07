# 0014: Certificate association history and reuse policy

## Context

The current identity pin and the record of previously used certificates serve different purposes. Re-enrollment must not erase evidence used to detect certificate reuse.

## Decision

`AssociateCert` writes the active pin and an append-only history row in one transaction. History survives re-enrollment and can be queried by enrollment or certificate hash. User channels resolve certificate history through their parent device.

`CertReusePolicy` governs certificates found in another enrollment's history. `DenyCertReuse` is the default; `AllowCertReuse` permits historical reuse but cannot override another enrollment's live pin. Retroactive pinning accepts only hashes unseen on other enrollments. Conflicting live pins return a typed storage conflict mapped to a forbidden service response.

## Rationale

Separate historical and active associations allow explicit rotation/reuse policy while retaining a unique current owner. Shared contract tests keep this behavior consistent across stores.

## Constraints

`PinOff` skips the service's reuse policy. Certificate history does not itself revoke certificates or establish a Managed Apple Account association. Account-driven issuance associations and optional status enforcement are separate controls (record 0047).

## Verification

Storage suites cover history ordering, reverse lookup, user-channel resolution and competing pin writes. Service tests cover denied/allowed reuse, retroactive pins, pin modes and storage errors.

## References

- [storage](../../../storage)
- [storage/storagetest](../../../storage/storagetest)
- [server/service](../../../server/service)
- [server/sqlstore/sqlcommon](../../../server/sqlstore/sqlcommon)
- <https://developer.apple.com/documentation/devicemanagement/check-in>
- <https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices>

Reference source identifiers and paths (relative to the named project):

- `micromdm/nanomdm@4948319`, `service/certauth/certauth.go`, `storage/mysql/certauth.go`, `storage/kv/certauth.go`, `storage/file/certauth.go`, `cmd/nanomdm/main.go`, `/migration`
- `micromdm/micromdm@904493b`, `mdm/checkin.go`, `platform/device/udidauth.go`, `platform/device/builtin/db.go`
- `fleetdm/fleet@b44343c`, `server/mdm/nanomdm/service/certauth/certauth.go`, `server/mdm/nanomdm/storage/mysql/certauth.go`, `EnrollmentFromHash`, `server/datastore/mysql/migrations/tables/`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/public_views/mdm.py`, `zentral/contrib/mdm/models.py`, `EnrolledDevice`
