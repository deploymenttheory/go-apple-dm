// Package cms signs and verifies the CMS (PKCS #7) signatures Apple MDM
// uses: the detached signature a device sends in the Mdm-Signature header
// when the MDM payload sets SignMessage, and the attached signature a
// server puts on configuration profiles.
//
// # Why
//
// The Mdm-Signature header is how a check-in or connect request proves it
// came from the enrolled device when TLS client certificates are not
// available to the application, and a signed enrollment profile is how a
// device knows the profile was not altered in transit. Phase 2 of the plan
// of record needs both. Verification wraps github.com/smallstep/pkcs7 and
// adds what that library lacks: a trust store, an injectable clock, and a
// signing-time tolerance (decision record 0006), because a device whose
// clock lags can sign with a certificate whose NotBefore is a few seconds
// in the future, which the library rejects unconditionally.
//
// The package knows nothing about enrollments or HTTP. httpapi turns a
// verified signer certificate into the request identity, and profile
// decides when a profile must be signed. Header encoding and decoding
// live here so both sides of the protocol agree on the format.
//
// # References
//
//   - Decision record 0006: docs/research/decisions/0006-mdm-signature-verification.md
//   - Decision record 0009: docs/research/decisions/0009-enrollment-profiles.md
//   - Plan of record: docs/research/implementation_plan.md (phase 2)
//   - Threat model: docs/security/threat-model.md (/checkin and /connect rows)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/managing-certificates-for-device-management-services-and-devices
//   - Schema: third_party/device-management/mdm/profiles/com.apple.mdm.yaml (SignMessage)
//   - RFC 5652 (Cryptographic Message Syntax): https://www.rfc-editor.org/rfc/rfc5652
package cms
