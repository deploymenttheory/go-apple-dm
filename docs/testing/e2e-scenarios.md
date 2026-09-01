# End-to-end scenarios

Each scenario is a named test under `e2e/` that wires the reference server, the device
simulator, the fake APNs server, and a storage backend. Every scenario maps to at least one
Apple documentation page. Scenarios are added by the phase that delivers the capability.

Run with `make test-e2e` (build tag `e2e`). Status: E2E-001 to E2E-005 implemented in `e2e/mdm_test.go`.

| ID | Scenario | Apple source | Phase | Test |
|---|---|---|---|---|
| E2E-001 | Pre-issued identity enrols: Authenticate, TokenUpdate, Idle, no commands | Check-in | 2 | `TestE2E_EnrollIdle` |
| E2E-002 | Three commands queued, delivered in order, acknowledged with typed responses | Commands and queries | 2 | `TestE2E_CommandsInOrder` |
| E2E-003 | Device answers NotNow, command is retried after backoff | Handling NotNow status responses | 2 | `TestE2E_NotNowBackoff` |
| E2E-004 | Device answers Error with ErrorChain, result stored, event emitted | Commands and queries | 2 | `TestE2E_CommandError` |
| E2E-005 | Re-enrollment with a new identity clears tokens and pending queue | Check-in | 2 | `TestE2E_Reenroll` |
| E2E-006 | SCEP enrollment from an unsigned profile, push, command | Device management essentials | 3 | `TestE2E_SCEPEnrollPush` |
| E2E-007 | APNs 410 marks token invalid and emits PushTokenInvalid | Dealing with inactive managed devices and invalid push tokens | 3 | `TestE2E_PushInvalidToken` |
| E2E-008 | Declaration change: push, tokens, declaration-items, fetch, status verified | Integrating declarative management | 5 | `TestE2E_DDMRoundTrip` |
| E2E-009 | Activation predicate excludes a device; declaration-items omits it | Leveraging the declarative management data model | 5 | `TestE2E_DDMPredicate` |
| E2E-010 | NanoMDM container pointed at proxyserver completes a DDM session | Integrating declarative management | 5 | `TestE2E_ProxyServerNanoMDM` |
| E2E-011 | DEP profile assignment through a fake DEP service, MachineInfo, ADE enrollment | Device assignment | 6 | `TestE2E_DEPAssign` |
| E2E-012 | Account-driven service discovery routes Mac and iPhone differently | Onboarding users with account-driven enrollment | 6 | `TestE2E_ServiceDiscovery` |
| E2E-013 | User channel command on a macOS user enrollment | Check-in (UserAuthenticate) | 6 | `TestE2E_UserChannel` |
| E2E-014 | ACME enrollment with simulated attestation; bad chain rejected | Validating a Managed Device Attestation | 7 | `TestE2E_ACMEAttest` |
| E2E-015 | Reference server readiness fails when storage is down, recovers | Deployment guide | 8 | `TestE2E_Readiness` |
