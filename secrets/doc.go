// Package secrets supplies credentials (push keys, DEP tokens, challenge
// keys) to the library without letting them leak into logs, errors, or
// JSON: a Secret that redacts itself wherever it is formatted and
// Providers that read from a static map, the environment, or a directory
// of files.
//
// # Why
//
// An MDM server holds the keys to every device it manages, and the
// commonest way they escape is a log line or an error message. Phase 3 of
// the plan of record introduces this package (decision record 0011) so
// every credential the library touches is a Secret, which prints as
// Redacted in fmt, slog, and JSON, and is fetched by name from a Provider
// the deployment chooses: Static for tests, Env for twelve-factor
// deployments, Dir for the file layout Docker and Kubernetes mount, and
// Chain to combine them. Dir is os.Root-scoped so a name cannot escape the
// directory.
//
// The package supplies secrets; it does not seal them. Encryption of
// stored per-device secrets under a key from a Provider is server/storage/crypt.
//
// # References
//
//   - Decision record 0011: docs/research/decisions/0011-secrets-provider.md
//   - Decision record 0013: docs/research/decisions/0013-secrets-at-rest.md
//   - Plan of record: docs/research/implementation_plan.md (phase 3)
//   - Threat model: docs/security/threat-model.md (Push credential exposure row)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
package secrets
