// Package axmcreds seals Apple Business Manager and Apple School Manager
// credentials with a named keyring.
//
// # Design
//
// Store implements axm.CredentialStore while keeping encryption dependencies
// outside the API client. Records are held in memory as ciphertext with their
// name in associated data. Sealed exposes bytes for an application's persistence
// layer. The package does not write to a database or file; durable storage and
// keyring lifetime belong to the caller.
//
// # References
//
//   - Decision record 0030: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0030-apple-business-manager-api-client.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Decision record 0044: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0044-repository-layout.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (secrets at rest)
//   - Apple: https://developer.apple.com/documentation/applebusinessmanagerapi
package axmcreds
