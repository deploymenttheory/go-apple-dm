# 0019: Canonical JSON and DDM tokens

## Context

A declaration's token must describe its served content, and an enrollment's aggregate token must be stable when membership and content do not change.

## Decision

`internal/canonjson` implements RFC 8785 canonical JSON with UTF-16 key ordering, canonical number/string formatting, depth limits and strict input validation. `ServerToken` is the hexadecimal SHA-256 digest of canonical `Identifier`, `Payload` and `Type` content. Uploaded tokens are ignored.

`DeclarationsToken` hashes sorted declaration references with length-prefixed strings. Snapshot timestamps change only when the token changes and are rendered with whole-second precision.

## Rationale

Canonical content avoids resynchronization caused only by whitespace or key order. Length prefixes distinguish tuple boundaries; sorting makes aggregate tokens independent of store row order. One Go implementation keeps every backend consistent.

## Constraints

Token algorithms are this project's implementation choices; Apple requires content changes to be reflected in the tokens. Tokens identify content and are not authorization credentials. SHA-256 hashing is not a mathematical guarantee of collision-free identifiers.

## Verification

Canonicalization tests cover RFC vectors, ordering, numeric forms, idempotency and invalid input. Token tests cover reordered content, stable timestamps, reference order, length-prefix boundaries and backend parity.

## References

- [internal/canonjson](../../../internal/canonjson)
- [mdmprotocol/ddm/token.go](../../../mdmprotocol/ddm/token.go)
- [mdmprotocol/ddm/token_test.go](../../../mdmprotocol/ddm/token_test.go)
- <https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest>
- <https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management>

Reference source identifiers and paths (relative to the named project):

- `jessepeterson/kmfddm@4b75a76`, `ddm/token.go`, `ddm/items.go`, `storage/kv/declarations.go`, `storage/mysql/declarations.go`, `storage/mysql/schema.sql`, `storage/multi.go`
- `fleetdm/fleet@b44343c`, `server/datastore/mysql/apple_mdm.go`, `MDMAppleDDMDeclarationsToken`
- `zentralopensource/zentral@b10dd22`, `zentral/contrib/mdm/declarations/protocol.py`, `zentral/contrib/mdm/models.py`
