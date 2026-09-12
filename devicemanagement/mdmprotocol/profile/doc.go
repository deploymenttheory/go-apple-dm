// Package profile composes, signs and parses Apple configuration profiles.
//
// # Design
//
// The package supplies the top-level envelope and common payload keys around
// generated schema/profiles values. Callers choose stable PayloadIdentifier and
// PayloadUUID values; a Resolver selects typed payloads during parsing. Attached
// CMS signing and signature-required parsing use mdmprotocol/cms.
//
// Payload selection for MDM enrollment and OTA delivery belong to
// mdmprotocol/enroll. Preserving identifiers across updates is the caller's
// responsibility.
//
// # References
//
//   - Decision record 0009: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0009-enrollment-profiles.md
//   - Decision record 0010: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0010-ota-profile-service.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (Enrollment profile row)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/profile-specific-payload-keys
//   - Apple: https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles
//   - Schema: third_party/device-management/mdm/profiles/TopLevel.yaml, CommonPayloadKeys.yaml
//   - RFC 5652 (Cryptographic Message Syntax): https://www.rfc-editor.org/rfc/rfc5652
package profile
