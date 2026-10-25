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
// browser flow to Finish. Bound.Parsed exposes the exact validated evidence for
// immutable storage with the caller's authentication session. The pointer and
// its reachable data are borrowed read-only values that alias the in-memory
// record. Callers snapshot them into session state with original identity,
// expiry and one-use consumption. Resume retrieves the latest per-serial record
// and cannot bind an original session.
//
// A required update fails explicitly when the device cannot execute the response
// or its OS version is unknown. Required policies reject unsupported platforms
// before comparing versions. Fulfilled requirements on supported platforms and
// unconfigured requirements proceed.
// Responses support iOS/iPadOS 17+, macOS 14+, and visionOS 26+; tvOS and watchOS
// have no supported enrollment update response. Policies remain caller-owned.
//
// The default device-chain verification ignores validity windows; audit mode
// permits unverified input. MachineInfo is not Managed Device Attestation or
// proof of organizational ownership. Consumers choose those policies separately.
// The supplied MachineInfo store is in memory.
//
// PasswordHash builds the XML plist embedded in ADE account commands using
// PBKDF2-HMAC-SHA512, random salt and caller-selected iterations. The derived
// length follows Apple's example; deployments select their iteration policy.
//
// # References
//
//   - Decision record 0027: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md
//   - Decision record 0010: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0010-ota-profile-service.md (the Apple iPhone Device CA)
//   - Decision record 0009: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0009-enrollment-profiles.md
//   - End-to-end scenarios: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/testing/e2e-scenarios.md (E2E-011, E2E-018)
//   - Password hash fields: https://developer.apple.com/documentation/devicemanagement/passwordhash/salted-sha512-pbkdf2-data.dictionary
//   - Apple: https://developer.apple.com/documentation/devicemanagement/machineinfo
//   - Apple: https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views
//   - Apple update capabilities: https://support.apple.com/guide/deployment/depd30715cbb/web
//   - Apple: https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired
//   - Apple: https://developer.apple.com/documentation/devicemanagement/profile
//   - Apple: https://developer.apple.com/library/archive/documentation/NetworkingInternet/Conceptual/iPhoneOTAConfiguration/Introduction/Introduction.html (the Apple iPhone Device CA)
//   - Schema: third_party/apple-device-management/current/other/machineinfo.yaml
//   - Schema: third_party/apple-device-management/current/mdm/errors/softwareupdate.required.yaml, psso.required.yaml, unrecognized.device.yaml
//   - RFC 5652 (Cryptographic Message Syntax): https://www.rfc-editor.org/rfc/rfc5652
package ade
