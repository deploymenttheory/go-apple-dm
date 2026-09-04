// Package ade serves Automated Device Enrollment: it reads and verifies
// the CMS-signed MachineInfo a device presents, persists it per serial,
// applies the software update gate, and hands the personalised enrollment
// profile back as application/x-apple-aspen-config, on both the
// token-based POST lane and the configuration_web_url lane.
//
// # Why
//
// A device assigned in Apple Business Manager fetches its enrollment
// profile from the MDM server during Setup Assistant. It identifies itself
// with MachineInfo, a plist signed by its built-in identity certificate
// whose chain runs through the Apple iPhone Device CA. Phase 6 of the plan
// of record needs that endpoint (decision record 0027): one parser for the
// three places the blob arrives (the x-apple-aspen-deviceinfo header, the
// deviceinfo query parameter, the request body), verification that
// honours CMS authenticated attributes and tolerates Apple's expired
// SHA-1 chain without a process-wide switch, a store keyed by serial that
// joins the DEP record, the software update and Platform SSO gate with
// its 403 bodies in JSON or plist, and a ProfileHook that lets the
// integrator choose and personalise the profile.
//
// The package does not build the profile (enroll does), sign CMS (cms
// does), talk to Apple's lookup service (gdmf does), or run the web view
// authentication itself: WebAuth is a small interface the mdmprotocol/enroll/webauth
// relying party satisfies, and Finish is what it calls when the user is
// authenticated. MachineInfo is not treated as attestation; it identifies
// the device to Apple's chain and nothing more.
//
// # References
//
//   - Decision record 0027: docs/research/decisions/0027-ade-enrollment-machineinfo-and-web-view-auth.md
//   - Decision record 0010: docs/research/decisions/0010-ota-profile-service.md (the Apple iPhone Device CA)
//   - Decision record 0009: docs/research/decisions/0009-enrollment-profiles.md
//   - Plan of record: docs/research/implementation_plan.md (phase 6)
//   - End-to-end scenarios: docs/testing/e2e-scenarios.md (E2E-011, E2E-018)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/machineinfo
//   - Apple: https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views
//   - Apple: https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired
//   - Apple: https://developer.apple.com/documentation/devicemanagement/profile
//   - Apple: https://developer.apple.com/library/archive/documentation/NetworkingInternet/Conceptual/iPhoneOTAConfiguration/ (the Apple iPhone Device CA)
//   - Schema: third_party/device-management/other/machineinfo.yaml
//   - Schema: third_party/device-management/mdm/errors/softwareupdate.required.yaml, psso.required.yaml, unrecognized.device.yaml
//   - RFC 5652 (Cryptographic Message Syntax): https://www.rfc-editor.org/rfc/rfc5652
package ade
