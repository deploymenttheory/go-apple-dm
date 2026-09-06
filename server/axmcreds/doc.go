// Package axmcreds seals Apple Business and School Manager API credentials
// under a keyring, so the client's private key never sits in plaintext at
// rest.
//
// # Why
//
// axm.CredentialStore is the contract the client reads credentials through,
// and axm defines it because axm is the consumer. Encryption is not the
// client's concern: sealing needs server/storage/crypt and a keyring supplied by
// the operator, which is server-side machinery an Apple API client has no
// business carrying. Keeping the implementation in axm made a program that
// only wanted to call the Business Manager API acquire a keyring, an AEAD,
// and a secrets provider to get there (decision record 0044).
//
// So the interface stays with its consumer and the implementation lives
// here. Store keeps records in memory sealed under a named key with the
// record name as associated data, and Sealed hands the ciphertext to
// whatever persistence layer the program already has.
//
// The package deliberately does not persist anything itself. Which database
// a sealed record is written to is the program's decision, and every SQL
// backend in this repository already knows how to write an opaque blob.
//
// # References
//
//   - Decision record 0030: docs/research/decisions/0030-apple-business-manager-api-client.md
//   - Decision record 0013: docs/research/decisions/0013-secrets-at-rest.md
//   - Decision record 0044: docs/research/decisions/0044-repository-layout.md
//   - Plan of record: docs/research/implementation_plan.md (phase 9)
//   - Threat model: docs/security/threat-model.md (secrets at rest)
//   - Apple: https://developer.apple.com/documentation/applebusinessmanagerapi
package axmcreds
