// Package scep is a minimal SCEP endpoint for issuing the device
// enrollment identity, plus a client the simulator enrols with.
//
// # Why
//
// The SCEP payload in an enrollment profile tells the device where to get
// its identity certificate and what challenge to present. Phase 3 of the
// plan of record needs a server that answers GetCACert, GetCACaps, and
// PKIOperation, signs through the ca.Signer abstraction, and gates
// issuance with pluggable challenges (static, one-time with expiry, and
// HMAC bound to the CSR subject) and a CSR verifier hook (decision record
// 0008). Renewals are accepted only from certificates that chain to our
// CA. Policy rejections come back as signed failure CertReps, unparseable
// envelopes as 400, and bodies are size-limited.
//
// The package speaks SCEP and nothing else: key policy is ca, the profile
// payload that points at the endpoint is enroll, and ACME with device
// attestation is phase 7.
//
// # References
//
//   - Decision record 0008: docs/research/decisions/0008-scep-and-ca.md
//   - Decision record 0009: docs/research/decisions/0009-enrollment-profiles.md
//   - Plan of record: docs/research/implementation_plan.md (phase 3)
//   - Threat model: docs/security/threat-model.md (SCEP rows)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-006, E2E-016)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/scep
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//   - Schema: third_party/device-management/mdm/profiles/com.apple.security.scep.yaml
//   - RFC 8894 (Simple Certificate Enrolment Protocol): https://www.rfc-editor.org/rfc/rfc8894
package scep
