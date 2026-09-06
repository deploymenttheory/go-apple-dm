// Package profile composes, signs, and parses Apple configuration profiles
// (.mobileconfig): the top-level envelope, the common payload keys, stable
// identifiers, and CMS signing.
//
// # Why
//
// Every enrollment starts with a profile, and phase 3 of the plan of record
// needs one built from typed payloads rather than templated XML. Payload
// bodies are the generated types in schema/profiles; this package supplies
// what Apple documents under profile-specific payload keys but does not
// generate: the Configuration envelope, PayloadIdentifier and PayloadUUID
// handling, scope, and a Resolver that maps PayloadType to a typed payload
// on parse (decision record 0009). Signing and parsing share the cms
// package so a signed profile round-trips and RequireSignature can reject
// an unsigned one.
//
// Which payloads go into an enrollment profile, and the OTA flow that
// delivers them, are enroll's concern; the payload schemas themselves are
// generated and never hand-edited.
//
// # References
//
//   - Decision record 0009: docs/research/decisions/0009-enrollment-profiles.md
//   - Decision record 0010: docs/research/decisions/0010-ota-profile-service.md
//   - Plan of record: docs/research/implementation_plan.md (phase 3)
//   - Threat model: docs/security/threat-model.md (Enrollment profile row)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/profile-specific-payload-keys
//   - Apple: https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles
//   - Schema: third_party/device-management/mdm/profiles/TopLevel.yaml, CommonPayloadKeys.yaml
//   - RFC 5652 (Cryptographic Message Syntax): https://www.rfc-editor.org/rfc/rfc5652
package profile
