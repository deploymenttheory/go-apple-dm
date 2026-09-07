// Package crypt seals byte values with AES-256-GCM using named keys from
// secrets.Provider.
//
// # Design
//
// Keyring derives keys with HKDF-SHA256, seals with an active key and accepts
// configured retired keys for reads. Ciphertext identifies its format and key
// name; additional authenticated data binds a value to its purpose and row.
// Strict mode rejects legacy plaintext. Callers rotate by retaining old keys
// until values have been rewrapped under the new active key.
//
// The package does not select database columns or encrypt entire records
// automatically. SQL backends define which values are sealed and how Rewrap
// walks them; raw check-in records and exports can still contain sensitive data.
//
// # References
//
//   - Decision record 0011: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0011-secrets-provider.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (Storage disclosure and ciphertext row swap rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//   - Schema: third_party/device-management/mdm/checkin/tokenupdate.yaml (UnlockToken)
//   - Schema: third_party/device-management/mdm/checkin/setbootstraptoken.yaml (BootstrapToken)
package crypt
