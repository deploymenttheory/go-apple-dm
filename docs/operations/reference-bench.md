# Reference server additions for the bench

These are ordinary reference-server capabilities. The bench supplies configuration
and exercises the same APIs as an operator. See the [runbook](../../test-lab/README.md)
for workspace commands and the [testing guide](../testing/bench.md) for test layers.

## Runtime and configuration

`dmserver` uses the shared HTTP/worker lifecycle. Native TLS is optional; existing
HTTP-behind-a-proxy deployments retain their behavior. On shutdown, requests drain
before workers stop and connections/storage close. `/healthz` checks storage;
`/readyz` additionally requires configured workers to be running.

| Setting | Behavior |
|---|---|
| `DM_TLS_CERT_FILE`, `DM_TLS_KEY_FILE` | Native TLS PEM identity, configured together; CLI `-tls-cert`, `-tls-key` |
| `DM_ENROLL_TLS_ANCHOR_FILE` | Public CA bundle needed to trust enrollment HTTPS; empty means publicly trusted HTTPS, independent of device-identity roots |
| `DM_CA_FILE` | Existing device identity roots, also used for optional client TLS certificates |
| `DM_PUSH_ROOT_CA_FILE` | Trust bundle for an explicitly configured MDM APNs endpoint |
| `DM_APP_PUSH_DEVELOPMENT_HOST`, `DM_APP_PUSH_PRODUCTION_HOST` | Optional app APNs endpoint overrides; defaults are Apple's respective endpoints |
| `DM_APP_PUSH_ROOT_CA_FILE` | Trust roots for app APNs endpoint overrides |
| `DM_OIDC_ROOT_CA_FILE` | Additional trust roots for the configured OIDC provider |
| `DM_USER_AUTH_HA1_FILE` | Private JSON map of username to RFC 2617 HA1 hex digest, realm `mdm`; unset leaves digest verification unconfigured |
| `DM_REQUIRE_USER_AUTH` | Existing switch requiring successful authentication before user TokenUpdate |
| `DM_OTA_ANCHOR_FILE`, `DM_OTA_CHALLENGE` | Explicit bootstrap-device roots and admission challenge for `/ota`; requires SCEP identity mode |
| `DMCTL_CA_FILE` / `dmctl -ca-file` | Additional server trust roots for administration without disabling TLS verification |

User HA1 values are password-equivalent credentials: store the file with private
permissions. OTA phase two additionally verifies the issued identity's subject
matches the device UUID. These options are inactive unless configured. The bench
sets them only for applicable simulated scenarios.

## Administration

Every route is under `/admin/v1`, uses the existing authentication/policy wrapper,
and appears in `/routes`. Mutations produce normal administrative audit events.

| Method and path | Input and result | Action |
|---|---|---|
| `POST /enrollment-profiles` | JSON `DeviceID`, optional `Serial`, `Identity` (`acme` or `scep`) and `AccessRights`; returns a mobileconfig using the configured issuer and topic. Default rights request device inventory. | `issueEnrollmentProfile` |
| `POST /enrollments/{channel}/{id}/replacement` | Optional `Identity`; creates a 30-minute attempt and its InstallProfile command. Device channel only; original profile must have installation rights and recorded metadata. | `replaceEnrollmentProfile` |
| `GET /enrollments/{channel}/{id}/replacement` | Redacted current attempt, delivery, acknowledgment, expiry and certificate fingerprints. | `readEnrollment` |
| `DELETE /enrollments/{channel}/{id}/replacement/{attempt}` | Cancels that pending attempt; preserves the active enrollment. | `replaceEnrollmentProfile` |
| `GET /enrollments/{channel}/{id}/enrollment-evidence` | Actual pinned identity's issuance method and certificate/check-in timestamps; unknown issuance remains unknown. | `readEnrollment` |
| `GET /enrollments/{channel}/{id}/commands/{uuid}/result` | 200: `CommandUUID`, `Status`, base64 plist `Response`, `ErrorChain`; 204 when the command has no result; 404 when absent. User channels require `?parent=...`. | `readCommands` |
| `GET /apppush/credentials` | Paged `Items` containing `Topic`, `NotBefore`, `NotAfter`, `Version`, and `NextCursor`. | `manageAppPushCredentials` |
| `PUT /apppush/credentials` | JSON PEM strings `CertPEM`, `KeyPEM`, optional expected `Topic`; returns metadata. | `manageAppPushCredentials` |
| `POST /apppush/send` | JSON `Environment`, `Topic`, hexadecimal `Token`, `PushType`, object `Payload`; optional `Priority`, `Expiration`. Returns `Accepted`, `Outcome`, `Status`, `Reason`, `APNSID`. | `sendAppPush` |

Environment is explicitly `development` or `production`; push type is `alert` or
`background`. Local request validation returns 400. Upstream failures return 502
with a bounded classification; successful APNs acceptance returns 200. The raw
transport error is omitted. A successful APNs response does not prove delivery.

Enrollment profile/result responses use `Cache-Control: no-store`. Result access
is scoped to the enrollment and its existing authorization action. All channel
names supported by enrollment storage are accepted by the administration API,
including Shared iPad and User Enrollment channels.

`dmctl apppush list|put|send` provides typed administration. `dmctl bench profile`
requests the same profile API and writes a new private file. Offline certificate
inspection, CSR generation and vendor CSR signing remain under `apns` and
`pushcerts`. `dmctl api` can access the result endpoint directly.

## Persistence and migration

App credentials use the existing transactional state table with the new
`apppush/v1/` namespace. No new SQL table migration or MDM-store interface change
is needed. Persistent composition requires the existing storage keyring; app
records authenticate their storage key as AAD. Memory composition is transient.

MDM and app credential stores remain separate. Updating an app credential cannot
replace an MDM topic. A successful import increments its version; the APNs clients
retire local connections and load committed credentials on the next send. Retain
old encryption key names until affected app identities are re-imported under the
active key. Existing MDM rewrap tooling does not cover the new namespace.

Existing local lab CA/key files, CSRs, credentials and receipts remain in place.
Live workspaces use `mdm/mdm.sqlite` and accept both `bench` and the earlier `lab`
key names with the preserved storage key material. The standalone lab executable
and Python runner are replaced by the maintained bench commands.

## Enrollment service discovery and live testing

`GET /MDMServiceConfig` is public HTTPS JSON. Its ADE URL follows the configured
DEP profile URL; `dep_anchor_certs_url` is always present and serves a JSON array
of base64 DER certificates. Private HTTPS also advertises `trust_profile_url`,
which serves only `com.apple.security.root` payloads. This is independent of
account-driven `/.well-known/com.apple.remotemanagement` discovery.

The replacement table is included in each SQL backend's initial schema. Pending
state is sealed with the configured storage keyring and committed atomically with
identity/token changes. No database upgrade migration is required for this
pre-release application. See the [Mac enrollment runbook](mac-enrollment-testing.md)
for ACME, SCEP, replacement and the exact live evidence required.
