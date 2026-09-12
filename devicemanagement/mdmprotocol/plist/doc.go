// Package plist wraps XML and binary property-list encoding and bounded
// decoding.
//
// # Design
//
// Marshal, Unmarshal, format detection and Decoder centralize use of
// github.com/micromdm/plist. Untrusted input can be bounded by bytes and XML
// nesting depth before codec dispatch. The depth control is specific to XML; the
// byte limit also applies to binary input.
//
// MessageType and RequestType dispatch belong to mdmprotocol/mdm. Using one
// codec wrapper keeps format and resource-limit choices consistent across
// protocol callers.
//
// # References
//
//   - Decision record 0002: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0002-plist-library.md
//   - Decision record 0004: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0004-checkin-and-command-core.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (tampering with message bodies row)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/commands-and-queries
package plist
