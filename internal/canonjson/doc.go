// Package canonjson produces the JSON Canonicalization Scheme (JCS) form of
// a JSON value as specified by RFC 8785.
//
// # Why
//
// Declaration and status tokens are derived from canonical bytes (decision
// record 0019), so two documents with the same meaning must canonicalize to
// the same bytes regardless of member order, whitespace, string escaping,
// or number spelling; otherwise a re-uploaded declaration would change its
// ServerToken and every device would re-fetch it. Phase 5 of the plan of
// record depends on this for the DDM engine.
//
// The parser is encoding/json/jsontext, which rejects duplicate object
// names and invalid UTF-8 (including unpaired surrogate escapes) by default,
// in line with the JSON policy of decision record 0018. On top of that this
// package bounds nesting depth at MaxDepth and rejects numbers that are not
// finite IEEE 754 doubles, because JCS defines number serialization in
// terms of doubles. Integers beyond 2^53 therefore lose precision, exactly
// as RFC 8785 section 3.2.2.3 describes. Hashing the canonical bytes into a
// token is ddm's job, not this package's.
//
// # References
//
//   - Decision record 0018: docs/research/decisions/0018-go-1.27-baseline.md
//   - Decision record 0019: docs/research/decisions/0019-canonical-json-and-ddm-tokens.md
//   - Plan of record: docs/research/implementation_plan.md (phase 5)
//   - Threat model: docs/security/threat-model.md (/status row)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Schema: third_party/device-management/declarative/protocol/declarationitemsresponse.yaml
//   - Schema: third_party/device-management/declarative/protocol/tokensresponse.yaml
//   - RFC 8785 (JSON Canonicalization Scheme): https://www.rfc-editor.org/rfc/rfc8785
package canonjson
