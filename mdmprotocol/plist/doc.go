// Package plist is the library's single point of contact with property
// list encoding: Marshal, Unmarshal, format detection, and a bounded
// Decoder for untrusted input.
//
// # Why
//
// Apple sends MDM check-in messages and command responses as XML or binary
// plists, and the server answers in kind. Decision record 0002 chose
// github.com/micromdm/plist as the encoder, the one accepted dependency on
// the MicroMDM family, and this wrapper is the one place a library swap
// would touch. It adds what protocol code needs when the input comes from
// a device: XML and binary format detection, a byte limit, and an XML
// nesting-depth limit, so a malformed body is rejected before it reaches
// the decoders in mdm. Phase 1 of the plan of record delivers it with the
// schema packages, whose generated types carry the plist struct tags it
// honours.
//
// The package does not know about MDM message types; dispatch on
// MessageType and RequestType is mdm's job.
//
// # References
//
//   - Decision record 0002: docs/research/decisions/0002-plist-library.md
//   - Decision record 0004: docs/research/decisions/0004-checkin-and-command-core.md
//   - Plan of record: docs/research/implementation_plan.md (phase 1)
//   - Threat model: docs/security/threat-model.md (tampering with message bodies row)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/check-in
//   - Apple: https://developer.apple.com/documentation/devicemanagement/commands-and-queries
package plist
