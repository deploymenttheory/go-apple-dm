// Package dep is the client for Apple's Automated Device Enrollment web
// service (the DEP service behind Apple Business Manager and Apple School
// Manager): OAuth 1.0a sessions for many accounts, every endpoint of the
// Device assignment API, the server token lifecycle including the token
// PKI exchange, a device syncer, and a state-driven profile assigner.
//
// # Why
//
// An MDM server learns which devices an organisation bought, and tells
// Apple which enrollment profile they get, only through this service, so
// phase 6 of the plan of record (enrollment breadth) needs it before ADE
// enrollment can be exercised end to end. The reference implementations
// each leave a gap the record documents: sessions cached per process
// without a singleflight, cursors advanced before pages are delivered,
// assignment driven by op_type so a re-fetch or a server move never
// re-assigns, token expiry stored but never checked, and 7-day cursor
// expiry never enforced locally. This package holds one client for any
// number of accounts with tokens and sessions in a dep.Store, its own RFC
// 5849 signer, at-least-once page delivery with the cursor committed with
// the page, assignment computed from stored state, typed errors with
// Apple's codes parsed from bare and quoted bodies, and a fake service in
// dep/deptest every contract is proved against. Persistence lives in
// dep/inmem and dep/sqlstore. It deliberately leaves out the Apple
// Business Manager REST API (record 0030) and the enrollment side of ADE
// (record 0027), and takes no code from nanodep or MicroMDM.
//
// # References
//
//   - Decision record 0026: docs/research/decisions/0026-dep-client-sync-and-assignment.md
//   - Decision record 0013: docs/research/decisions/0013-secrets-at-rest.md (sealed token columns)
//   - Plan of record: docs/research/implementation_plan.md (section 5, DEP / ABM; phase 6)
//   - Threat model: docs/security/threat-model.md
//   - Apple: https://developer.apple.com/documentation/devicemanagement/device-assignment
//   - Apple: https://developer.apple.com/documentation/devicemanagement/authenticating-for-automated-device-enrollment
//   - Apple: https://developer.apple.com/documentation/devicemanagement/fetch-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/sync-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/device
//   - Apple: https://developer.apple.com/documentation/devicemanagement/fetchdeviceresponse
//   - Apple: https://developer.apple.com/documentation/devicemanagement/define-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/assign-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/clear-device-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/fetch-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/device-details
//   - Apple: https://developer.apple.com/documentation/devicemanagement/disown-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/activation-lock-devices
//   - Apple: https://developer.apple.com/documentation/devicemanagement/get-beta-enrollment-tokens
//   - Apple: https://developer.apple.com/documentation/devicemanagement/assign-account-driven-enrollment-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/fetch-account-driven-enrollment-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/remove-account-driven-enrollment-profile
//   - Apple: https://developer.apple.com/documentation/devicemanagement/limit
//   - Apple: https://developer.apple.com/documentation/devicemanagement/url
//   - Schema: third_party/device-management/other/skipkeys.yaml (schema/other.SkipKeys, the skip_setup_items vocabulary)
//   - RFC 5849 (OAuth 1.0): https://www.rfc-editor.org/rfc/rfc5849
//   - RFC 5652 (CMS enveloped data, the .p7m token file): https://www.rfc-editor.org/rfc/rfc5652
//   - RFC 8551 (S/MIME 4.0): https://www.rfc-editor.org/rfc/rfc8551
package dep
