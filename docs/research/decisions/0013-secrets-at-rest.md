# 0013: Secrets at rest

## Context

Database records contain escrowed tokens and private keys. Those values need protection independent of database access controls.

## Decision

`crypt.Keyring` loads named keys through `secrets.Provider` and derives AES-256 keys with HKDF-SHA256. Provider material shorter than 16 bytes is rejected. Ciphertext includes a format marker, key name, nonce and authenticated body. Additional authenticated data binds the value to its purpose and row identifier.

The MDM store seals unlock tokens, bootstrap tokens, push private keys and user-authentication tokens. Satellite stores also use the keyring for their credential records. `Rewrap` pages through rows and replaces old ciphertext with an old-bytes guard, allowing concurrent writes. The active key seals new data; accepted keys open existing data.

## Rationale

Named keys support rotation, and row-bound authentication prevents copying ciphertext between records or columns. Loading material during construction exposes provider errors before serving requests.

## Constraints

Without strict mode, a keyring can read legacy plaintext during migration. Enable strict mode only after sealing existing rows. Raw check-in plists remain plaintext and can contain sensitive fields; sealing selected columns does not encrypt the full database, exports or backups. In-memory backends do not seal process memory.

## Verification

Cryptography tests cover round trips, wrong AAD, tampering, unknown keys and weak material. SQL tests inspect sealed columns, exercise plaintext migration and strict mode, and verify rotation and concurrent-write failure handling.

## References

- [storage/crypt](../../../storage/crypt)
- [server/sqlstore/sqlcommon](../../../server/sqlstore/sqlcommon)
- [server/sqlstore/sqlite](../../../server/sqlstore/sqlite)
- <https://developer.apple.com/documentation/devicemanagement/check-in>
- <https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices>

Reference source identifiers and paths (relative to the named project):

- `fleetdm/fleet@b44343c`, `server/datastore/mysql/apple_mdm.go`, `encrypt`, `decrypt`
- `zentralopensource/zentral@b10dd22`, `zentral/core/secret_engines/backends/fernet.py`, `zentral/core/secret_engines/__init__.py`, `zentral/contrib/mdm/models.py`, `PushCertificate.set_private_key`, `EnrolledDevice`
- `micromdm/nanomdm@4948319`, `storage/mysql/pushcert.go`, `storage/mysql/bstoken.go`, `storage/mysql/schema.sql`
- `micromdm/micromdm@904493b`, `platform/config/builtin/db.go`, `platform/device/builtin/db.go`
