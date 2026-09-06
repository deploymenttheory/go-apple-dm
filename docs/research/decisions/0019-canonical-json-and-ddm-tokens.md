# 0019: Canonical JSON and DDM tokens

Status: accepted
Date: 2026-09-02
Phase: 5

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/declarativemanagementrequest>
- Doc: <https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management>
- YAML: `third_party/device-management/declarative/protocol/declarationitemsresponse.yaml` ("servers must compute the token over the entire declaration content")
- YAML: `third_party/device-management/declarative/protocol/tokensresponse.yaml` (`SyncTokens`; the de facto shape is `{DeclarationsToken, Timestamp}`)
- YAML: `third_party/device-management/declarative/declarations/declarationbase.yaml` (`Identifier` and `ServerToken` should not exceed 64 octets)
- RFC 8785 (JSON Canonicalization Scheme)

## References read

- `jessepeterson/kmfddm@4b75a76` `ddm/token.go`, `ddm/items.go`, `storage/kv/declarations.go`, `storage/mysql/declarations.go`, `storage/mysql/schema.sql`, `storage/multi.go`
- `fleetdm/fleet@b44343c` `server/datastore/mysql/apple_mdm.go` (`MDMAppleDDMDeclarationsToken`, declaration upsert)
- `zentralopensource/zentral@b10dd22` `zentral/contrib/mdm/declarations/protocol.py`, `zentral/contrib/mdm/models.py`

## Known pitfalls found

- KMFDDM: `ServerToken` is a storage concern that differs per backend (KV: raw payload bytes plus creation micros plus a touch counter; MySQL: `SHA1(CONCAT(identifier, type, payload, created_at, touched_ct))` over a JSON column), so the same declaration has a different token on each backend and delete-then-recreate changes it.
- KMFDDM: the `DeclarationsToken` concatenates `ServerToken` strings with no separator, so two different lists can hash the same.
- KMFDDM: the MySQL items query has no `ORDER BY`, so the token depends on row order.
- KMFDDM: creation time is in the hash, so re-uploading identical content changes the token and re-syncs every device.
- KMFDDM: declarations whose type is not `com.apple.` vanish from the items response but still perturb the token.
- KMFDDM: `storage.Multi` shadows `err` and silently truncates the list.
- Fleet: `DeclarationsToken` is `md5(count || concat(...))` ordered by `uploaded_at DESC`, computed both in SQL and in Go and required to match byte for byte; the `ServerToken` had to move off a JSON column because MySQL normalises JSON and the hash no longer saw the uploaded bytes.
- Zentral: `sha1` over section-sorted, item-sorted `(key, Identifier, ServerToken)`; the composite is joined with a separator that can appear in identifiers, so the token is not guaranteed injective.

## What they do

- **KMFDDM**: token generation delegated to the backend; `ddm/token.go` hashes the concatenated server tokens; items and tokens built from whatever the backend returns.
- **Fleet**: MySQL generated column for `ServerToken` over `MEDIUMTEXT`; `DeclarationsToken` in SQL with a Go mirror; per-host timestamps for variables and assets folded in.
- **Zentral**: Python `hashlib.sha1` over sorted tuples; `declarations_token` stored per enrolled device and user.

## What we do better

1. `internal/canonjson` implements RFC 8785 over `jsontext`: keys sorted by UTF-16 code units, no whitespace, ES6 number formatting, JCS string escaping; invalid JSON, duplicate names, invalid UTF-8, and depth overflow are errors; output is idempotent and decodes to the same value.
2. `ServerToken = hex(sha256(canonical({Identifier, Payload, Type})))`, 64 octets (the Apple limit), computed once in Go from content only; the same declaration has the same token on every backend, key order and whitespace do not matter, and no wall clock enters the hash.
3. The uploaded `ServerToken` is ignored; re-uploading equivalent JSON yields the same token, no change row, and no notify.
4. `DeclarationsToken` is `sha256` over the refs sorted by `(kind, identifier, servertoken)`, each string written with a 4-byte length prefix, rendered as 64 hex characters; order independent, injective over the tuple list (`("ab","c")` differs from `("a","bc")`), and byte-identical across `inmem`, SQLite, PostgreSQL, and MySQL (golden value).
5. The tokens response is byte-stable while nothing changed; `Timestamp` is the snapshot's `TokenChangedAt` rendered as `2006-01-02T15:04:05Z` without fractional seconds and advances only when the token changes.

## Verified by

1. `canonjson.TestCanonicalize/RFCVectors`, `/KeysSortedByUTF16`, `/Numbers`, `/Idempotent`, `canonjson.FuzzCanonicalize` (prove claim 1; would fail on every reference because none has a canonical form).
2. `ddm.TestTokenFor/KeyOrderAndWhitespaceIgnored`, `/Length64`, `/IndependentOfTime` (prove claim 2; would fail on KMFDDM because creation time and the touch counter are hashed, and on Fleet because the hash is over the stored bytes as uploaded).
3. `ddm.TestPutDeclaration/UploadedServerTokenIgnored`, `/EquivalentJSONIsNoop` (prove claim 3; would fail on KMFDDM because every write bumps `touched_ct`).
4. `ddm.TestDeclarationsToken/OrderIndependent`, `/LengthPrefixed`, `/Length64`, `/GoldenAcrossBackends` (prove claim 4; would fail on KMFDDM because of the missing separator and unordered rows, and on Fleet because the SQL and Go paths must agree by construction rather than by a shared golden value).
5. `ddm.TestTokens/ByteStableWhileUnchanged`, `/TimestampAdvancesOnlyWhenTokenChanges` (prove claim 5; would fail on KMFDDM because `Timestamp` is the current time).

## Rejected alternatives

- Hashing the raw uploaded bytes (Fleet): whitespace and key order changes would re-sync every device.
- Letting each backend compute the token in SQL (KMFDDM, Fleet): two implementations that must agree, and JSON column normalisation on MySQL breaks the agreement.
- `sha1` or `md5` (Zentral, Fleet): no security need, but `sha256` is the standard library default and the hex output fits the 64-octet limit exactly.
- A reversible composite token (kind and identifier readable from the token): the token is opaque to the device and a longer token exceeds Apple's guidance.
- Fractional seconds in `Timestamp`: Apple's examples use whole seconds and a fractional clock would make the response differ between backends with different timestamp precision.
