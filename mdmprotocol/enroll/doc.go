// Package enroll builds the MDM enrollment profile and serves the
// over-the-air profile service: the MDM payload, the identity it points at
// (SCEP or a pre-issued PKCS #12), optional trust anchors, and the
// two-phase OTA flow that issues the identity before handing over the
// final profile.
//
// # Why
//
// A device becomes managed by installing a configuration profile whose MDM
// payload names the check-in and server URLs, the push topic, the access
// rights, and the identity certificate to sign with. Phase 3 of the plan of
// record needs a builder that produces that profile from typed inputs and
// validates it against Apple's schema before it leaves the server
// (decision record 0009), plus the profile-service endpoint from the OTA
// archive documentation, where a device first proves itself with its
// built-in certificate, then enrols through SCEP, then fetches the profile
// signed by its new identity (decision record 0010).
//
// The profile envelope and signing belong to profile, the payload types to
// schema/profiles, SCEP issuance to scep, and account-driven and DEP
// enrollment to phase 6. This package only assembles and serves.
//
// # References
//
//   - Decision record 0009: docs/research/decisions/0009-enrollment-profiles.md
//   - Decision record 0010: docs/research/decisions/0010-ota-profile-service.md
//   - Plan of record: docs/research/implementation_plan.md (phase 3)
//   - Threat model: docs/security/threat-model.md (OTA profile-service and Enrollment profile rows)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-006, E2E-016)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/mdm
//   - Apple: https://developer.apple.com/documentation/devicemanagement/scep
//   - Apple: https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles
//   - Apple: https://developer.apple.com/library/archive/documentation/NetworkingInternet/Conceptual/iPhoneOTAConfiguration/
//   - Schema: third_party/device-management/mdm/profiles/TopLevel.yaml, CommonPayloadKeys.yaml
//   - Schema: third_party/device-management/mdm/profiles/com.apple.mdm.yaml, com.apple.security.scep.yaml, com.apple.security.root.yaml
package enroll
