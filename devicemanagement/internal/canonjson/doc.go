// Package canonjson implements the JSON Canonicalization Scheme defined by RFC
// 8785.
//
// # Design
//
// Canonical bytes let declaration tokens remain stable across changes in
// whitespace, key order and equivalent number/string representations. The parser
// uses encoding/json/jsontext, rejects duplicate names and invalid UTF-8, bounds
// nesting at MaxDepth and rejects non-finite IEEE 754 numbers. Number
// serialization follows JCS double precision, so integers beyond 2^53 can lose
// precision.
//
// The package returns canonical content; mdmprotocol/ddm computes tokens from
// it.
//
// # References
//
//   - Decision record 0018: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0018-go-1.27-baseline.md
//   - Decision record 0019: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/research/decisions/0019-canonical-json-and-ddm-tokens.md
//   - Threat model: https://github.com/deploymenttheory/go-apple-dm/blob/main/docs/security/threat-model.md (/status row)
//   - Apple: https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest
//   - Schema: third_party/device-management/declarative/protocol/declarationitemsresponse.yaml
//   - Schema: third_party/device-management/declarative/protocol/tokensresponse.yaml
//   - RFC 8785 (JSON Canonicalization Scheme): https://www.rfc-editor.org/rfc/rfc8785
package canonjson
