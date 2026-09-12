// Package ca defines certificate signing, issuance storage and policy for
// enrollment identities.
//
// # Design
//
// Signer separates SCEP and ACME from the CA implementation. Local signs with an
// in-memory RSA or ECDSA key and applies validity, key type/size, usage and SAN
// policy. Depot records issuance; trusted callbacks can register additional
// provenance before the certificate is returned. External CAs can implement
// Signer without exposing their keys to protocol callers.
//
// MemoryDepot and self-signed CA generation support development. Persistent
// deployments supply stable CA material and an appropriate depot. Optional
// status enforcement and publication live in pki/revocation.
//
// # References
//
//   - Decision record 0008: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0008-scep-and-ca.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (SCEP rows)
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-006)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/scep
//   - Schema: third_party/device-management/mdm/profiles/com.apple.security.scep.yaml
package ca
