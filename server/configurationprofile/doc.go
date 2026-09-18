// Package configurationprofile stores and serves configuration profiles for
// Apple's LegacyProfile declarations.
//
// # Design
//
// Uploads validate configuration profiles and preserve their original bytes,
// PayloadIdentifier, PayloadUUID and CMS signatures. SHA-256 revisions identify
// immutable uploads; metadata and content share a protocol-state transaction.
// Parsing and inspection use mdmprotocol/profile and mdmprotocol/profile/inspect.
//
// The Expander binds hosted ProfileURL and AssetData DataURL values to the full
// enrollment identity. Downloads require a currently eligible declaration in
// the enrollment's advertised snapshot and validate profile scope against the
// configured target. Identity authentication, certificate pinning and revocation
// checks belong to server/service and the HTTP ingress in server/internal/app.
// Split deployments forward downloads through server/ddmadapter. Delivery works
// with ordinary DDM declarations and has no dependency on Blueprint authoring.
//
// # References
//
//   - Decision record 0055: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0055-blueprint-composition.md
//   - Decision record 0013: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0013-secrets-at-rest.md
//   - Decision record 0050: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0050-enrollment-security-boundaries.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md
//   - Operations: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/operations/blueprints.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/legacyprofile
//   - Schema: third_party/apple-device-management/current/declarative/declarations/configurations/legacy.yaml
//   - Schema: third_party/apple-device-management/current/declarative/declarations/assets/data.yaml
//   - Schema: third_party/apple-device-management/current/mdm/profiles/TopLevel.yaml, CommonPayloadKeys.yaml
package configurationprofile
