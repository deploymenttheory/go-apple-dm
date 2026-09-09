# Design decisions

These records describe the implemented design. Each decision identifies the relevant context,
contracts, rationale, constraints and verification evidence. Use the [template](TEMPLATE.md)
for new decisions; omit empty sections. Integrate amendments into the relevant record and keep
its number and filename stable so existing links continue to resolve.

The [architecture guide](../../architecture.md) summarizes how the decisions fit together.

| Decision | Design |
|---|---|
| 0001 | [Library-first architecture with a generated schema core](0001-architecture.md) |
| 0002 | [plist encoding and decoding](0002-plist-library.md) |
| 0003 | [In-repo schema generator over apple/device-management](0003-schema-generator.md) |
| 0004 | [Check-in and command protocol core](0004-checkin-and-command-core.md) |
| 0005 | [Storage interfaces, in-memory backend, contract suite](0005-storage-interfaces.md) |
| 0006 | [Mdm-Signature verification and identity pinning](0006-mdm-signature-verification.md) |
| 0007 | [APNs push for MDM](0007-apns-push.md) |
| 0008 | [SCEP endpoint and pluggable CA](0008-scep-and-ca.md) |
| 0009 | [Configuration profiles and the enrollment profile builder](0009-enrollment-profiles.md) |
| 0010 | [Over-the-air profile service (two-phase enrollment)](0010-ota-profile-service.md) |
| 0011 | [Secrets provider and redaction](0011-secrets-provider.md) |
| 0012 | [SQL storage backends (SQLite, PostgreSQL, MySQL)](0012-sql-storage-backends.md) |
| 0013 | [Secrets at rest](0013-secrets-at-rest.md) |
| 0014 | [Certificate association history and reuse policy](0014-cert-association-history.md) |
| 0015 | [Push certificate store](0015-push-cert-store.md) |
| 0016 | [UserAuthenticate challenge and token state](0016-user-authenticate-state.md) |
| 0017 | [Enrollment export and import](0017-enrollment-export-import.md) |
| 0018 | [Go 1.27 baseline and JSON policy](0018-go-1.27-baseline.md) |
| 0019 | [Canonical JSON and DDM tokens](0019-canonical-json-and-ddm-tokens.md) |
| 0020 | [DDM engine, membership, and storage](0020-ddm-engine-membership-and-storage.md) |
| 0021 | [Status reports and status subscriptions](0021-status-reports-and-subscriptions.md) |
| 0022 | [Change notifier](0022-change-notifier.md) |
| 0023 | [DDM adapters and the internal wire contract](0023-ddm-adapters-and-wire-contract.md) |
| 0024 | [Simulator DDM client and predicate subset](0024-simulator-ddm-client-and-predicates.md) |
| 0025 | [Reference server roles and container](0025-reference-server-roles-and-container.md) |
| 0026 | [DEP client, device sync, and profile assignment](0026-dep-client-sync-and-assignment.md) |
| 0027 | [Automated Device Enrollment: MachineInfo, the enrollment endpoint, and web view authentication](0027-ade-enrollment-machineinfo-and-web-view-auth.md) |
| 0028 | [Account-driven enrollment and service discovery](0028-account-driven-enrollment-and-service-discovery.md) |
| 0029 | [User channel, multiple users, and Shared iPad](0029-user-channel-and-shared-ipad.md) |
| 0030 | [Apple Business Manager and Apple School Manager API client (`axm`)](0030-apple-business-manager-api-client.md) |
| 0031 | [ACME server, client identifiers, and the ACME state store](0031-acme-server-and-state-store.md) |
| 0032 | [Managed Device Attestation: parsing, verification, and policy](0032-managed-device-attestation.md) |
| 0033 | [ACME identity in enrollment profiles, declarative credentials, and the reference server](0033-acme-identity-in-profiles-and-ddm.md) |
| 0034 | [Admin API surface and authorization](0034-admin-api-and-authorization.md) |
| 0035 | [`dmctl` structure, output, and credential handling](0035-dmctl-structure-and-credentials.md) |
| 0036 | [`dmctl explain` over `schema/support`](0036-dmctl-explain-over-schema-support.md) |
| 0037 | [Event sinks and default-deny redaction](0037-event-sinks-and-redaction.md) |
| 0038 | [The persisted audit trail](0038-persisted-audit-trail.md) |
| 0039 | [Declarative device management within the MDM enrollment](0039-ddm-is-an-extension-of-mdm.md) |
| 0040 | [An OpenTelemetry seam the consumer owns](0040-opentelemetry-seam.md) |
| 0041 | [Apple's closed vocabularies as Go constants](0041-closed-apple-vocabularies-as-constants.md) |
| 0042 | [Push failure classification](0042-push-failure-classification.md) |
| 0043 | [`DM_` names the configuration, `MDM` names the protocol](0043-configuration-naming.md) |
| 0044 | [Repository layout — layered tiers and a separate reference-server module](0044-repository-layout.md) |
| 0045 | [Return to Service](0045-return-to-service.md) |
| 0046 | [Generated schema provenance](0046-generated-from-is-generated.md) |
| 0047 | [Enrollment authentication and optional security services](0047-enrollment-authentication-and-optional-security-services.md) |
| 0048 | [Reference server as a maintained test bench](0048-reference-server-bench.md) |
| 0049 | [Server-managed app push credentials and sending](0049-server-managed-app-push.md) |
