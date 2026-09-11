# 0035: `dmctl` structure, output, and credential handling

## Context

Operators need scriptable administrative access and offline command construction without embedding CLI logic in the binary entry point.

## Decision

`dmctl bench` adds workspace and scenario operations that do not use the ordinary global server context. `dmctl apppush` administers server credentials and sends; `-ca-file` / `DMCTL_CA_FILE` extends trust for normal administration. See [0048](0048-reference-server-bench.md).

The entry point delegates to `internal/dmctl`; HTTP access and schema explanation have separate internal packages. Configuration stores credential references (`token_env` or `token_file`) by default. Inline token storage requires an explicit option and warning. Config files use restricted permissions and reject group/other-readable credentials.

Remote administration requires verified HTTPS; HTTP is accepted only for literal
loopback IPs. `-insecure` is rejected with guidance to use `-ca-file`. Server URLs
cannot contain user information, query strings or fragments, and redirects are
refused before credentials can be forwarded.

JSON output preserves the response body; NDJSON streams items. `-all` follows cursors, while single-page output sends the next cursor to stderr. Exit codes distinguish success (0), request failure (1), usage (2), partial success (3), and authentication/authorization failure (4). `commands send -dry-run` builds a validated plist locally.

## Rationale

Separate logic permits direct testing and keeps stdout usable by scripts. Credential references avoid duplicating secrets in ordinary configuration. Local validation gives feedback before a request reaches the server.

## Constraints

The client packages are internal and are not a public Go API. CLI flag credentials can appear in process arguments; use environment/file references for routine operation. Shell completion, directory synchronization and a public admin SDK are not implemented.

## Verification

CLI tests cover output bytes, paging, exit codes, token sources, permissions, help, payload input forms and offline use. A structural test bounds the main function, and the end-to-end route-table scenario exercises all admin routes through typed verbs or `api`.

## References

- [server/cmd/dmctl](../../../server/cmd/dmctl)
- [server/internal/dmctl](../../../server/internal/dmctl)
- <https://developer.apple.com/documentation/devicemanagement/commands-and-queries>
- <https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations>

Reference source identifiers and paths (relative to the named project):

- `macadmins/nanohubctl@f88ef9a6e147eb23ed5b81068e2a3121fad58ba3`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107`, `cmd/mdmctl/`, `mdmdctl.go`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151`, `tools/*.sh`
- `tools/syncdir.py`, `tools/ideclr.py`
- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10`, `cmd/nano2nano/main.go`
- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a`, `server/service/middleware/auth/api_only.go`
- `cmd/dmserver/main.go`, `run`, `cmd/admgen/main.go`
- `scripts/coverage-gate.sh`, `scripts/coverage-exempt.txt`
