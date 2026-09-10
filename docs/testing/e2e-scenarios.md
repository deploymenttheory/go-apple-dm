# End-to-end scenarios

These are the retained detailed regressions. The [shared bench catalogue](bench-catalogue.md)
links them to scenarios against the reference server's actual runtime. `make test-e2e`
runs both; `make test-acceptance` exercises built server processes. See the
[testing guide](bench.md) for the distinction between public workflow checks and
internal timing/storage assertions.


The named tests in [server/e2e](../../server/e2e/) compose protocol clients, simulator devices,
fake Apple services and storage backends. Their assertions verify modeled exchanges; they do
not establish physical-device compatibility. Apple sources are indexed in the
[reference catalogue](../research/reference_projects.md).

Run `make test-e2e` (build tag `e2e`). `E2E_STORE` selects `sqlite` (default, one database per test),
`postgres` (requires `TEST_POSTGRES_DSN`, one schema per test) or `inmem`. CI runs SQLite and
PostgreSQL scenarios. E2E-010 requires the separate DDM container: `scripts/testdb.sh ddm-up`
prints the `TEST_DDM_*` settings. The test skips when these are absent.

Scenario identifiers remain stable. E2E-015 and E2E-022 have no corresponding named tests in the
current tree and are not counted as implemented coverage. Readiness and declarative ACME
credential behavior have application-level tests; inspect their assertions before relying on
broader deployment behavior.

| ID | Scenario | Apple source | Test |
|---|---|---|---|
| E2E-001 | Pre-issued identity enrols: Authenticate, TokenUpdate, Idle, no commands | Check-in | `TestE2E_EnrollIdle` |
| E2E-002 | Three commands queued, delivered in order, acknowledged with typed responses | Commands and queries | `TestE2E_CommandsInOrder` |
| E2E-003 | Device answers NotNow, command is retried after backoff | Handling NotNow status responses | `TestE2E_NotNowBackoff` |
| E2E-004 | Device answers Error with ErrorChain, result stored, event emitted | Commands and queries | `TestE2E_CommandError` |
| E2E-005 | Re-enrollment enforces the configured identity replacement policy | Check-in | `TestE2E_Reenroll` |
| E2E-006 | SCEP enrollment from an unsigned profile, push, command | Device management essentials | `TestE2E_SCEPEnrollPush` |
| E2E-007 | APNs 410 marks token invalid and emits PushTokenInvalid | Dealing with inactive managed devices and invalid push tokens | `TestE2E_PushInvalidToken` |
| E2E-008 | Declaration change: push, tokens, declaration-items, fetch, status verified | Integrating declarative management | `TestE2E_DDMRoundTrip` |
| E2E-009 | Activation predicate excludes a device; declaration-items omits it | Leveraging the declarative management data model | `TestE2E_DDMPredicate` |
| E2E-010 | Split deployment: the `mdm` role in-process forwards Apple's DeclarativeManagement check-in through `proxyclient` to the project's `ddm`-role container (built from this repository, run by `scripts/testdb.sh ddm-up`), signed both ways; status codes and bodies relayed unchanged; wrong keys and oversized bodies rejected | Integrating declarative management | `TestE2E_DDMSplitDeployment` |
| E2E-011 | DEP: token PKI exchange with the fake DEP service, fetch then sync with cursor expiry, profile defined and assigned by the state-driven assigner, the device enrols through ADE with verified MachineInfo, the software update gate answers 403 for an old OS | Device assignment, MachineInfo | `TestE2E_DEPAssign` |
| E2E-012 | Account-driven service discovery routes a Mac to `mdm-adde` and an iPhone to `mdm-byod`; each enrols through the `apple-as-web` flow with the reusable access tokens and registered certificate associations and the right `EnrollmentMode` | Onboarding users with account-driven enrollment | `TestE2E_ServiceDiscovery` |
| E2E-013 | macOS user channel through `UserAuthenticate` (digest) and `TokenUpdate`; a user-channel command is delivered to that user, a device-only command addressed to the user is rejected at enqueue, two users on one device coexist | Check-in (UserAuthenticate) | `TestE2E_UserChannel` |
| E2E-014 | ACME enrollment with simulated attestation: the device orders with its client identifier, answers `device-attest-01`, and enrols with the issued identity; a chain from another authority, a stale freshness code, a certificate request for a key the attestation did not cover, a reused client identifier, and an attestation naming another device are each refused | Validating a Managed Device Attestation | `TestE2E_ACMEAttest` |
| E2E-016 | OTA profile service: phase 1 signed by the device certificate with the challenge, SCEP, phase 2 signed by the new identity, final profile enrolls | Over-the-Air Profile Delivery and Configuration | `TestE2E_OTAProfileService` |
| E2E-023 | `DeviceInformation` with `DeviceAttestationNonce`: the returned `DevicePropertiesAttestation` chain verifies and its properties are read; the device returns its cached attestation for a repeated nonce; a tampered chain is refused | Device Information Command | `TestE2E_DeviceAttestation` |
| E2E-017 | CheckOut then re-enrol: no DDM sets, snapshot, or status survive and no stale push is sent | Check-in (CheckOut) | `TestE2E_DDMCheckOutClears` |
| E2E-018 | ADE web view authentication: `configuration_web_url` receives the signed `x-apple-aspen-deviceinfo`, the OIDC flow completes against the fake provider with PKCE and nonce, the profile is personalised with the identity claim and served as `application/x-apple-aspen-config` | Authenticating through web views | `TestE2E_ADEWebViewAuth` |
| E2E-019 | Account-driven enrollment with the `apple-oauth2` flow: 401 parameters, authorization code with `login_hint`, token endpoint, refresh, second POST with the bearer | Implementing the OAuth 2 authentication account-driven enrollment flow | `TestE2E_AccountDrivenOAuth2` |
| E2E-020 | Shared iPad: DEP profile with `is_multi_user`, the logged-in user channel with `ManagedAppleID`, device-scoped and user-scoped commands routed by the schema metadata | Shared iPad | `TestE2E_SharedIPad` |
| E2E-021 | Apple Business Manager: list servers and devices with paging, assign devices to the configured MDM server, wait for the activity and the assignment to converge, audit events, unassign; token expiry replay and `Retry-After` honoured | Apple Business Manager API | `TestE2E_ABMAssignDevices` |
| E2E-025 | Return to service: a device escrows a bootstrap token, asks for its return-to-service configuration, and the response carries the escrowed token for the device to use during Return to Service | Check-in (ReturnToService) | `TestE2E_ReturnToService` |
| E2E-026 | A server with no return-to-service policy answers `Enabled` false rather than an error, so an unconfigured server never wipes a device | Check-in (ReturnToService) | `TestE2E_ReturnToServiceDisabledByDefault` |
| E2E-024 | `dmctl` drives every admin route the server advertises; a read-only principal is refused and the refusal is audited; a rotated token is rejected; the server wakes a device | Commands and queries | `TestE2E_AdminCLI` |
