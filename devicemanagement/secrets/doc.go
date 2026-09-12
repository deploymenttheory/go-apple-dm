// Package secrets provides named credential sources and a value type that
// redacts formatted output.
//
// # Design
//
// Secret redacts fmt, text and JSON serialization; Bytes provides explicit
// access to the value. Static, Env, Dir and Chain providers support injected
// values, normalized environment names, bounded rooted files and fallback. Dir
// uses os.Root so names cannot escape its configured directory.
//
// Redaction reduces accidental disclosure through formatting; it does not
// protect values after explicit extraction or encrypt memory. storage/crypt
// provides sealing for selected persisted values.
//
// # References
//
//   - Decision record 0011: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0011-secrets-provider.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (Push credential exposure row)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
package secrets
