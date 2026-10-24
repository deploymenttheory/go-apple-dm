// Package inventorystore persists agentless inventory in the shared reference
// server database.
//
// # Design
//
// Store implements inventory.Backend over SQLite, PostgreSQL and MySQL. Open
// applies versioned migrations to a caller-owned connection pool. Document keys
// provide ordered range scans; the inventory repository owns record structure,
// independent source evidence, original history, explicit links and job state.
//
// Open installs an initially inactive retired-namespace write fence. SQLite and
// PostgreSQL install it transactionally through schema migrations. MySQL inspects
// metadata under a connection-scoped advisory lock and installs only missing DDL,
// preserving an existing active fence across interrupted or concurrent opens.
// Its migration role needs trigger creation authority under the server's binary
// logging policy; the runtime application role does not require SUPER.
//
// Repository.Migrate activates the fence inside the same transaction that extracts
// original evidence, removes obsolete aggregates and changes worker lease owners.
// Old native observers cannot recreate aggregate keys. MySQL triggers use a current
// locking read so transactions begun before activation cannot bypass the fence.
//
// Updates acquire the inventory lock within a SQL unit of work. An existing
// transaction in the context is reused, allowing native inventory observations
// to commit or roll back with protocol results and events. Page snapshots and
// sync checkpoints likewise share an atomic repository update.
//
// A supplied keyring seals document payloads with associated data binding them
// to their document key and column purpose. Strict keyrings reject plaintext
// reads; a nil keyring permits plaintext storage for callers that explicitly
// choose it. The reference server owns keyring configuration and lifetime.
// MigrationSet and the server recovery bindings include inventory in database
// migration, backup, restore and encrypted-payload verification.
//
// # References
//
//   - Decision record 0057: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0057-agentless-device-inventory.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Decision record 0044: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0044-repository-layout.md
//   - Agentless inventory guide: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/agentless-inventory.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md
package inventorystore
