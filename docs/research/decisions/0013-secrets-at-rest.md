# 0013: Secrets at rest

Status: accepted
Date: 2026-09-02
Phase: 4

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in> (`UnlockToken` arrives in `TokenUpdate`; `BootstrapToken` in `SetBootstrapToken`; both are escrowed secrets)
- Doc: <https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices> (push certificate private key handling)
- YAML: `third_party/device-management/mdm/checkin/tokenupdate.yaml` (`UnlockToken`)
- YAML: `third_party/device-management/mdm/checkin/setbootstraptoken.yaml` (`BootstrapToken`)

## References read

- `fleetdm/fleet@b44343c` `server/datastore/mysql/apple_mdm.go` (`encrypt`, `decrypt`: AES-GCM under the server private key, nil AAD)
- `zentralopensource/zentral@b10dd22` `zentral/core/secret_engines/backends/fernet.py`, `zentral/core/secret_engines/__init__.py`, `zentral/contrib/mdm/models.py` (`PushCertificate.set_private_key`, `EnrolledDevice` escrow fields)
- `micromdm/nanomdm@4948319` `storage/mysql/pushcert.go`, `storage/mysql/bstoken.go`, `storage/mysql/schema.sql`
- `micromdm/micromdm@904493b` `platform/config/builtin/db.go`, `platform/device/builtin/db.go`

## Known pitfalls found

- Fleet: one configuration key encrypts every secret with no AAD, no KDF, and no rotation path; `decrypt` slices below the nonce size and panics on a short blob.
- Zentral: ciphertext is tagged with the engine name, but fernet ignores context, so a row copied between columns still decrypts; the fernet key is derived from the Django `SECRET_KEY`.
- NanoMDM: push key, unlock token, and bootstrap token are plaintext columns; the unlock token is also duplicated inside the raw `TokenUpdate` column.
- MicroMDM: push key and device records are plaintext values in BoltDB.
- Detecting sealed rows by a magic prefix is probabilistic against arbitrary plaintext; a strict mode is required to close it.

## What they do

- **Fleet**: `encrypt(plain, key)` and `decrypt(blob, key)` in the datastore; nonce prepended; key is a config string used directly as the AES key; applied to MDM assets such as the APNs key and SCEP key.
- **Zentral**: pluggable secret engines (cleartext, fernet, AWS KMS, GCP KMS); `MultiFernet` accepts several keys derived by PBKDF2 from passwords with `SECRET_KEY` as salt; ciphertext prefixed with `engine_name$`; model fields call `set_private_key` and `get_private_key`.
- **NanoMDM**: `push_certs.key_pem`, `enrollments.unlock_token`, and `devices.bootstrap_token_b64` stored as given; no encryption layer in any backend.
- **MicroMDM**: push certificate and key in the config bucket; device records with unlock tokens in the device bucket; no encryption layer.

## What we do better

1. `crypt.Keyring` takes keys from `secrets.Provider` once at construction, derives 32-byte AES keys with HKDF-SHA256 (`hkdf.Key(sha256.New, providerBytes, salt=keyName, info="go-apple-mdm/storage/crypt/v1", 32)`: the key name is the salt, so two names backed by the same material yield distinct keys, and the info string pins the package and layout version), and refuses provider material under 16 bytes with `ErrWeakKey`. Provider failure is a startup error, not a per-row error.
2. Ciphertext is self-describing: magic, key name, 12-byte nonce, AES-256-GCM body. The AAD is `purpose + "\x00" + rowID`, so a blob bound to one table, column, and row fails with `ErrTampered` anywhere else.
3. Keys are named: `Active` seals, `Accepted` opens, unknown names fail with `ErrUnknownKey`. `Store.Rewrap` re-seals rows under the active key in pages of `RewrapBatchSize` (500) with an old-bytes guard on every `UPDATE`, skips rows already sealed under the active key, and returns the number of rows it rewrote (a concurrent writer makes it skip, so callers loop until it returns 0); rotation needs no downtime.
4. The sealed set is explicit and small: `enrollments.unlock_token` (AAD row id is the enrollment id), `enrollments.bootstrap_token` (device id), `push_certs.key_pem` (topic), `user_auth.auth_token` (user enrollment id); the AAD purpose is the `table.column` name. The push token (not a secret, hot path), `authenticate_raw` and `token_update_raw` (PII already in queryable plaintext columns; database encryption is the control), `cert_hash`, and `user_auth.challenge` stay plaintext, with the reason recorded here.
5. Adoption is gradual: a store opened with a keyring reads existing plaintext rows until `Strict` is set, and a sealed row read without a keyring fails with `ErrUnsealed` rather than returning ciphertext. The contract suite runs unchanged in both modes.
6. Seal and open helpers preserve empty as `NULL`, so `COALESCE(?, unlock_token)` still keeps an old token, and `len == 0` still maps to `ErrNotFound`; write failures inside `Rewrap` surface with the count applied so far.
7. `inmem` is unchanged: process memory is inside the trust boundary and the keyring would only add cost.

## Verified by

1. `crypt.TestNewKeyringErrors` (short key, missing active key, provider error), `crypt.TestNewKeyringAcceptsEdgeNames`, `crypt.TestDistinctNamesDeriveDistinctKeys`, `crypt.TestSealOpenRoundTrip` (prove claim 1; would fail on Fleet because the raw config string is the key and nothing rejects a short one).
2. `crypt.TestOpenRejectsWrongAAD`, `crypt.TestOpenRejectsTamper`, `crypt.TestAAD` (prove claim 2; would fail on Fleet and Zentral because neither binds ciphertext to its row, so a swapped blob decrypts).
3. `crypt.TestOpenAcceptsRetiredKey`, `crypt.TestOpenRejectsUnknownKey`, `sqlite.TestRewrapMovesToActiveKey` (prove claim 3; would fail on Fleet because there is one key and no rewrap).
4. `sqlite.TestRawColumnIsNotPlaintext` (reads the four columns with plain SQL and asserts the magic prefix and no plaintext), `sqlite.TestContract` (no keyring) and `sqlite.TestContractEncrypted` (the whole contract suite with a keyring); `postgres.TestContract/Encrypted` and `mysql.TestContract/Encrypted` run the enrollment, bootstrap token, push certificate, user auth, and migration suites with a keyring on real servers and then assert `Rewrap` finds nothing left to seal (prove claim 4; would fail on NanoMDM and MicroMDM because the columns are plaintext).
5. `sqlite.TestReadsPlaintextRowsWhenKeyringAdded` (then `Strict` fails), `sqlite.TestSealedRowWithoutKeyring`, `crypt.TestIsSealedAndKeyName` (prove claim 5; would fail on Fleet because a plaintext row fed to `decrypt` panics or returns garbage).
6. `sqlite.TestRewrapSurfacesWriteFailure`, `storagetest.RunBootstrapTokenSuite` inside `sqlite.TestContractEncrypted` (`COALESCE` path), `crypt.TestSealNonceFailure`, `crypt.TestNilKeyring` (prove claim 6).
7. `inmem.TestContract` runs `storagetest.RunAll` without a keyring option (claim 7).

## Rejected alternatives

- `golang.org/x/crypto` for HKDF: `crypto/hkdf` is in the standard library and the module has no other need for the dependency.
- Per-row KMS envelope keys: a network call per read on the check-in path; deployments that want KMS put the keyring material behind `secrets.Provider`.
- Relying on database encryption (TDE) only: it does not protect against a dumped table or a read-only replica, and it is invisible to the contract suite.
- Sealing `authenticate_raw` and `token_update_raw`: the same fields are queryable plaintext columns, so sealing the plist adds cost without removing exposure.
