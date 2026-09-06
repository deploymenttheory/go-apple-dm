// Package ca is the certificate authority abstraction that issues device
// enrollment identities: a Signer interface, a Local signer over an
// in-memory key constrained by a Policy, a Depot that records what was
// issued, and self-signed CA generation.
//
// # Why
//
// Every MDM enrollment rests on a device identity certificate, and the
// server has to issue one through SCEP (phase 3) and, later, ACME (phase
// 7). Both endpoints need the same small thing from a CA: sign this CSR
// under this policy and remember the result. This package holds that
// contract so the protocol packages never touch a private key directly.
// Local signs with an in-memory RSA or ECDSA key; a deployment with an
// external CA implements Signer and keeps its keys where it wants them.
//
// The package deliberately leaves out the SCEP wire protocol (scep), the
// ACME protocol and attestation verification (phase 7), certificate
// storage beyond MemoryDepot, and revocation. Policy covers only what an
// enrollment identity needs: validity, key size and type, key usage, and
// subject alternative names.
//
// # References
//
//   - Decision record 0008: docs/research/decisions/0008-scep-and-ca.md
//   - Plan of record: docs/research/implementation_plan.md (phase 3)
//   - Threat model: docs/security/threat-model.md (SCEP rows)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-006)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/scep
//   - Schema: third_party/device-management/mdm/profiles/com.apple.security.scep.yaml
package ca
