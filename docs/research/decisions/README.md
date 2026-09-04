# Decision records

One record per feature, written before the feature's code, using [TEMPLATE.md](TEMPLATE.md).
This is step 3 of the research-guided build loop in
[../implementation_plan.md](../implementation_plan.md) section 8.

## Per-feature checklist

Before code:

- [ ] Apple documentation page and the YAML file(s) under `third_party/device-management` located
- [ ] At least two reference implementations read (`make refs` clones them under `third_party/refs/`)
- [ ] Reference issue trackers and commit history mined for pitfalls (commands in the plan, section 8)
- [ ] Decision record written; every "what we do better" claim names a test

With the code:

- [ ] Package doc comment cites the Apple page and YAML file(s)
- [ ] Tests for every pitfall found, written first
- [ ] Every new exported function has at least one failing-path test
- [ ] Fuzz target added if the change parses untrusted input
- [ ] `make generate` and `make verify` clean if schema packages changed
- [ ] `make ci` green, coverage gate at or above 95%
- [ ] Threat model updated if an endpoint or trust boundary changed
- [ ] Record status moved to `accepted`

## Index

| ID | Title | Status | Phase |
|---|---|---|---|
| [0001](0001-architecture.md) | Library-first architecture with a generated schema core | accepted | 0 |
| [0002](0002-plist-library.md) | plist encoding and decoding | accepted | 0 |
| [0003](0003-schema-generator.md) | In-repo schema generator over apple/device-management | accepted | 1 |
| [0004](0004-checkin-and-command-core.md) | Check-in and command protocol core | accepted | 2 |
| [0005](0005-storage-interfaces.md) | Storage interfaces, in-memory backend, contract suite | accepted | 2 |
| [0006](0006-mdm-signature-verification.md) | Mdm-Signature verification and identity pinning | accepted | 2 |
| [0007](0007-apns-push.md) | APNs push client, notifier, and coalescing | accepted | 3 |
| [0008](0008-scep-and-ca.md) | Certificate authority abstraction and SCEP endpoint | accepted | 3 |
| [0009](0009-enrollment-profiles.md) | Configuration profiles and the enrollment profile builder | accepted | 3 |
| [0010](0010-ota-profile-service.md) | Over-the-air profile service (two-phase enrollment) | accepted | 3 |
| [0011](0011-secrets-provider.md) | Secrets provider and redaction | accepted | 3 |
| [0012](0012-sql-storage-backends.md) | SQL storage backends (SQLite, PostgreSQL, MySQL) | accepted | 4 |
| [0013](0013-secrets-at-rest.md) | Secrets at rest | accepted | 4 |
| [0014](0014-cert-association-history.md) | Certificate association history and reuse policy | accepted | 4 |
| [0015](0015-push-cert-store.md) | Push certificate store | accepted | 4 |
| [0016](0016-user-authenticate-state.md) | UserAuthenticate challenge and token state | accepted (HA1 verifier unverified against a real macOS client) | 4 |
| [0017](0017-enrollment-export-import.md) | Enrollment export and import | accepted | 4 |
| [0018](0018-go-1.27-baseline.md) | Go 1.27 baseline and JSON policy | accepted | 5 |
| [0019](0019-canonical-json-and-ddm-tokens.md) | Canonical JSON and DDM tokens | accepted | 5 |
| [0020](0020-ddm-engine-membership-and-storage.md) | DDM engine, membership, and storage | accepted | 5 |
| [0021](0021-status-reports-and-subscriptions.md) | Status reports and status subscriptions | accepted | 5 |
| [0022](0022-change-notifier.md) | Change notifier | accepted | 5 |
| [0023](0023-ddm-adapters-and-wire-contract.md) | DDM adapters and the internal wire contract | accepted | 5 |
| [0024](0024-simulator-ddm-client-and-predicates.md) | Simulator DDM client and predicate subset | accepted | 5 |
| [0025](0025-reference-server-roles-and-container.md) | Reference server roles and container | accepted | 5 |
| [0026](0026-dep-client-sync-and-assignment.md) | DEP client, device sync, and profile assignment | accepted | 6 |
| [0027](0027-ade-enrollment-machineinfo-and-web-view-auth.md) | ADE enrollment: MachineInfo, the enrollment endpoint, and web view authentication | accepted | 6 |
| [0028](0028-account-driven-enrollment-and-service-discovery.md) | Account-driven enrollment and service discovery | accepted | 6 |
| [0029](0029-user-channel-and-shared-ipad.md) | User channel, multiple users, and Shared iPad | accepted | 6 |
| [0030](0030-apple-business-manager-api-client.md) | Apple Business Manager and Apple School Manager API client | accepted | 6 |
| [0031](0031-acme-server-and-state-store.md) | ACME server, client identifiers, and the ACME state store | accepted | 7 |
| [0032](0032-managed-device-attestation.md) | Managed Device Attestation: parsing, verification, and policy | accepted | 7 |
| [0033](0033-acme-identity-in-profiles-and-ddm.md) | ACME identity in enrollment profiles, declarative credentials, and the reference server | accepted | 7 |
| [0034](0034-admin-api-and-authorization.md) | Admin API surface and authorization | accepted | 8 |
| [0035](0035-dmctl-structure-and-credentials.md) | `dmctl` structure, output, and credential handling | accepted | 8 |
| [0036](0036-dmctl-explain-over-schema-support.md) | `dmctl explain` over `schema/support` | accepted | 8 |
| [0037](0037-event-sinks-and-redaction.md) | Event sinks and default-deny redaction | accepted | 9 |
| [0038](0038-persisted-audit-trail.md) | The persisted audit trail | accepted | 9 |
| [0039](0039-ddm-is-an-extension-of-mdm.md) | Declarative management is an extension of MDM, not a peer | accepted | 9 |
| [0040](0040-opentelemetry-seam.md) | An OpenTelemetry seam the consumer owns | accepted | 9 |
| [0041](0041-closed-apple-vocabularies-as-constants.md) | Apple's closed vocabularies as Go constants | accepted | 9 |
| [0042](0042-push-failure-classification.md) | A push failure is not a dead device | accepted | 9 |
| [0043](0043-configuration-naming.md) | `DM_` names the configuration, `MDM` names the protocol | accepted | 9 |
