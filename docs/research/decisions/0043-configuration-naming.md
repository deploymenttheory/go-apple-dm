# 0043: `DM_` names the configuration, `MDM` names the protocol

## Context

Configuration names must remain distinguishable from Apple protocol keys and other projects' compatibility identifiers.

## Decision

The reference server uses `DM_*` environment variables and the CLI uses `DMCTL_*`. Binaries are `dmserver` and `dmctl`. Complete variable spellings are constants so operators can locate them directly in source.

`MDM` remains in protocol identifiers such as the `mdm` role, `MachineInfo` keys and Apple service error/activity names. References to other projects' tools retain their original names.

## Rationale

A consistent project configuration vocabulary gives users stable search terms while preserving exact wire values and external tool identity.

## Constraints

The parser does not provide legacy `MDM_*` or `MDMCTL_*` aliases. Existing configurations using those names must use the current names. Editorial terminology changes must never rename protocol fields, command names or environment variables.

## Verification

Configuration tests exercise server and CLI names and role values. Protocol conformance and service-client tests pin Apple's wire constants. Container integration uses the documented `DM_*` variables.

## References

- [server/internal/app/env.go](../../../server/internal/app/env.go)
- [server/internal/dmctl/config.go](../../../server/internal/dmctl/config.go)
- <https://developer.apple.com/documentation/devicemanagement>
- <https://developer.apple.com/documentation/devicemanagement/declarative-management>

Reference source identifiers and paths (relative to the named project):

- `micromdm/micromdm@904493b`, `cmd/micromdm/serve.go`
- `micromdm/nanomdm@d61174c`, `cmd/nanomdm/main.go`, `storage/mysql/mysql_test.go`
- `jessepeterson/kmfddm@7f06151`, `cmd/kmfddm/main.go`, `storage/mysql/mysql_test.go`
- `micromdm/nanodep@ae047ad`, `cmd/depserver/main.go`, `storage/pgsql/pgsql_test.go`
- `fleetdm/fleet@2330565`, `server/service/`
