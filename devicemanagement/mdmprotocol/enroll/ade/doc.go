// Package ade serves Automated Device Enrollment profiles after parsing and
// verifying signed MachineInfo.
//
// # Design
//
// The parser accepts the device-info header, query parameter and CMS body with a
// size bound. Verification handles authenticated attributes and Apple's device
// chain through explicit per-handler options. Handlers retain parsed data by
// serial, optionally enrich it through DEP lookup, apply software-update policy
// and call ProfileHook for admission and profile selection. WebAuth connects the
// browser flow to Resume and Finish.
//
// The default device-chain verification ignores validity windows; audit mode
// permits unverified input. MachineInfo is not Managed Device Attestation or
// proof of organizational ownership. Consumers choose those policies separately.
// The supplied MachineInfo store is in memory.
//
// # References
//
//   - Decision record 0027: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md
//   - Decision record 0010: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0010-ota-profile-service.md (the Apple iPhone Device CA)
//   - Decision record 0009: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0009-enrollment-profiles.md
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-011, E2E-018)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/machineinfo
//   - Apple: https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views
//   - Apple: https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired
//   - Apple: https://developer.apple.com/documentation/devicemanagement/profile
//   - Apple: https://developer.apple.com/library/archive/documentation/NetworkingInternet/Conceptual/iPhoneOTAConfiguration/ (the Apple iPhone Device CA)
//   - Schema: third_party/device-management/other/machineinfo.yaml
//   - Schema: third_party/device-management/mdm/errors/softwareupdate.required.yaml, psso.required.yaml, unrecognized.device.yaml
//   - RFC 5652 (Cryptographic Message Syntax): https://www.rfc-editor.org/rfc/rfc5652
package ade
