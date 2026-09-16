# Selected Apple management implementation plan

This plan contains only the eight approved additions. The checked items have code,
automated tests, examples and documentation in this checkout. The root library owns
protocol helpers and Apple clients; the server module owns admin routes and CLI
commands. Reuse generated types, storage contracts and dependencies.

Evidence was reviewed on 2026-09-14 and 2026-09-15 against Apple documentation and
the pinned device-management revision b0180185a5e4077070710033341b71d0cbe1a18a.
The [validation record](../testing/selected-features-2026-09-15.md) separates physical-Mac
results from fixture tests and identifies the remaining live-service checks.

## Phase 1: inspection

- [x] **DDM status pagination.** Extend the existing values route with cursor,
  limit and prefix; retain its 1,000-item default. Expose existing error and
  retained-report queries with the same authorization and channel identity.
  Add `dmctl enrollments status values|errors|reports`, using existing paging and
  output formats. Redact credential-bearing diagnostics without modifying storage.
  Verify more than 1,000 records, filters, cursors, channels and authorization.
  Apple defines [status reports](https://developer.apple.com/documentation/devicemanagement/statusreport)
  and [administrator-facing status errors](https://developer.apple.com/documentation/devicemanagement/processing-status-for-managed-apps).
  The pagination API is a project adapter improvement, not an Apple wire protocol.

- [x] **Offline profile lint.** Add `dmctl profile lint -file <path|-> -target <target>`.
  Reuse profile parsing, validation and support metadata. Report invalid,
  unsupported, removed, deprecated and unvalidated content without silently losing
  unknown input. Distinguish signature validity from certificate trust. Support
  human/JSON output and existing success/failure/usage/partial exit conventions.
  Test malformed, nested, unknown, signed, untrusted and encrypted inputs.
  Sources: [profile structure and signing](https://developer.apple.com/documentation/devicemanagement/configuring-multiple-devices-using-profiles)
  and [Apple schema metadata](https://github.com/apple/device-management/blob/release/docs/schema.md).
  Lint establishes schema conformance, not successful installation.

## Phase 2: protocol helpers

- [x] **Managed Apple Account JWT.** Accept registered certificate, matching RSA
  signer, server UUID and issuance time; generate a fresh UUID and RS256 JWT
  containing iss, iat, jti and service_type=com.apple.maid. Demonstrate the
  GetToken handler and com.apple.mdm.token capability. Independently verify
  signatures, claims, key mismatch, signing failures and uniqueness.
  Sources: [Get Token](https://developer.apple.com/documentation/devicemanagement/get-token)
  and [RS256](https://www.rfc-editor.org/rfc/rfc7518.html#section-3.3).

- [x] **ADE account password hashes.** Derive PBKDF2-HMAC-SHA512 with random
  32-byte salt and caller-selected positive iterations; return XML plist data
  using the generated type. Match Apple's example's 128-byte derived value,
  explicitly documenting that this length is example-derived. Demonstrate
  AccountConfiguration and SetAutoAdminPassword, including the ADE administrator
  GUID requirement. Verify independent vectors and command/plist round trips.
  Sources: [hash fields](https://developer.apple.com/documentation/devicemanagement/passwordhash/salted-sha512-pbkdf2-data.dictionary),
  [account example](https://developer.apple.com/documentation/devicemanagement/account-configuration-command),
  [password-change command](https://developer.apple.com/documentation/devicemanagement/setautoadminpasswordcommand/command-data.dictionary).
  Sample iteration counts are not universal security recommendations.

- [x] **FileVault recovery-key decryption.** Extend CMS using the existing pkcs7
  dependency; accept envelope, recipient certificate and matching crypto.Decrypter.
  Return plaintext bytes and typed errors. Demonstrate EncryptedNewRecoveryKey and
  retaining reply keys for delayed responses. Independently test BER/DER,
  recipient selection, wrong keys, malformed envelopes and supported algorithms.
  Automatically generate and retain encryption identities in Go before queueing
  rotation commands. Provide the escrow-profile workflow for macOS 26 Macs with
  an existing personal recovery key and bootstrap token; retain keys across
  restarts and expiry. See [workflow](../operations/protocol-helpers.md#automatic-filevault-encryption-certificates),
  [Apple escrow payload](https://developer.apple.com/documentation/devicemanagement/fderecoverykeyescrow)
  and [macOS 26 behavior](https://support.apple.com/en-us/124963).
  Sources: [reply certificate](https://developer.apple.com/documentation/devicemanagement/rotatefilevaultkeycommand/command-data.dictionary),
  [encrypted result](https://developer.apple.com/documentation/devicemanagement/rotatefilevaultkeyresponse/rotateresult-data.dictionary),
  [CMS](https://www.rfc-editor.org/rfc/rfc5652.html#section-6).

- [x] **Activation Lock bypass codes.** Generate a server code and its hash;
  derive hashes from valid server-format codes. Implement Apple's exact encoding
  and PBKDF2 parameters. Preserve device-returned codes as opaque. Show using the
  hash to enable Activation Lock and retaining the code for unlocking. Verify
  independent vectors, malformed codes and secret-safe diagnostics.
  Sources: [Apple algorithm](https://developer.apple.com/documentation/devicemanagement/creating-and-using-bypass-codes)
  and [MicroMDM implementation](https://micromdm.io/blog/organization-activation-lock/).

- [x] **Installation manifests.** Build generated ManifestURL objects from
  explicit metadata, HTTPS asset URLs and readers. Stream whole-file SHA-256 or
  explicit chunk hashes; return byte counts separately. Omit removed fields and
  new MD5 generation. Provide macOS InstallEnterpriseApplication and iOS/iPadOS
  InstallApplication examples. Test independent hashes, chunk boundaries, input
  failures and round trips.
  Sources: [manifest fields](https://developer.apple.com/documentation/devicemanagement/manifesturl/itemsitem/assetsitem),
  [packages](https://developer.apple.com/documentation/devicemanagement/installing-packages),
  [enterprise apps](https://developer.apple.com/documentation/devicemanagement/install-application-command).

## Phase 3: Apps and Books

- [x] **Licensing client.** Add a root-library client for device and user app/book
  licensing. Implement service/client configuration, assets, assignments,
  associate/disassociate/revoke, user list/create/update/retire and event status.
  Support page-index pagination and documented incremental queries; refresh
  dynamic limits on demand after five minutes. Keep configuration writes and
  uncertain mutation retries explicit. Surface MDM ownership conflicts.
  Decode bearer-authenticated notifications, including test notifications and
  deduplication IDs. Demonstrate user association and licensing completion before
  installation, with event-status fallback. Preserve book restrictions.
  Persistence, hosting, installation decisions, subscriptions and the separate
  catalog-metadata API are outside this client.
  Test token/location isolation, pagination, user lifecycle, limits, partial
  outcomes, ownership, notifications, cancellation and retries.
  Sources: [getting started](https://developer.apple.com/documentation/devicemanagement/getting-started-with-the-management-api),
  [assets](https://developer.apple.com/documentation/devicemanagement/managing-assets),
  [users](https://developer.apple.com/documentation/devicemanagement/managing-users),
  [pagination](https://developer.apple.com/documentation/devicemanagement/using-paginated-endpoints),
  [service config](https://developer.apple.com/documentation/devicemanagement/service-config),
  [notifications](https://developer.apple.com/documentation/devicemanagement/subscribing-to-notifications).

## Phase 4: documentation and verification

- [x] Update package docs, CLI help, admin examples and affected guides. Every
  owned Go package has `doc.go` with Design and References sections; the schema
  generator now emits `doc.go` and protects handwritten documentation.
- [x] Publish [inspection](../operations/status-and-profile-inspection.md),
  [protocol-helper](../operations/protocol-helpers.md) and
  [Apps and Books](../operations/apps-and-books.md) guides. Verify the six Apple
  documentation destinations in `appsbooks/doc.go` against their actual content.
- [x] Record independent fixture provenance and run race-enabled tests, both
  module linters, `make verify`, affected SQL contracts, end-to-end/acceptance
  tests and standalone server installation checks. Existing Go dependency
  resolution and CI responsibilities remain in place; no new download scripts.
- [x] Exercise DDM pagination, lint/install/remove a benign profile, and recover
  FileVault escrow output on the physical test Mac. Keep the new recovery key
  and matching certificate encrypted; remove temporary profiles after recovery.

Implementation entry points: `server/internal/app/adminstatus.go`,
`server/internal/dmctl/profilelint`, `devicemanagement/mdmprotocol/{enroll,cms,activationlock,manifest}`,
`server/replycerts`, and `devicemanagement/appleplatformservices/appsbooks`.

The remaining live checks require suitable Apple organisation credentials,
ADE enrollment, distributable installation assets or another device. They are
listed in the validation record and are not implied by the checked implementation
items. The FileVault test proves certificate acceptance and CMS recovery; it does
not prove the legacy password-based rotation command or disk unlock.
