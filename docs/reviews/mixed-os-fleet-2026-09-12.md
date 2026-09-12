# Mixed-OS fleet compatibility follow-up

Branch: `fix/apple-schema-incidents-40-46`.

This follow-up addresses the requirement to support older, current and next OS
releases in the same fleet. It supersedes the API blockers in the
[initial incident report](apple-schema-incidents-2026-09-12.md).

## Implemented behavior

The schema generator retains an explicit historical Apple checkout while adding
the candidate's definitions and availability metadata. The reviewed combination
uses release commit `67045e2fa06f528b196c01edee6a8bf88b844beb` and OS 27 seed
`b0180185a5e4077070710033341b71d0cbe1a18a`. Comparison with the published stable
API reports **zero removed declarations and zero changed public signatures**.
The 12 previously removed declarations and three changed fields are preserved.
No removal allowance or API guard was relaxed.

One core can target macOS 15.7, 26.4 and 27.0 in a single enqueue call:

| Operation | macOS 15.7 | macOS 26.4 | macOS 27.0 |
|---|---|---|---|
| Device inventory | Eligible | Eligible | Eligible |
| Legacy available-updates command | Eligible | Eligible, deprecated | Rejected |
| Enhanced logging, supervised user channel | Rejected | Rejected | Eligible |
| Legacy DDM profile with URL | Eligible | Eligible | Eligible |
| Legacy DDM profile with asset reference | Rejected | Rejected | Eligible |

The corresponding tests check command bytes, channel routing, acknowledgments,
declaration admission and JSON/plist encoding. Historical firewall logging fields
remain available for macOS 12–14 and unavailable at their actual macOS 15 removal
boundary. Withdrawn `removed: '0'` properties are always unavailable.

Unknown OS/version inventory cannot authorize advanced known commands. Inventory
queries remain available to establish the missing facts. Tracked, acknowledged
device-channel inventory refreshes OS, build and product information; invalid or
absent values do not erase prior observations.

Dispatch revalidates queued work against that refreshed inventory. A command made
unsupported by an upgrade is individually cleared, and the valid command behind
it can be sent. Audit history retains the cleared row and a `command-rejected`
event with fixed reason codes. No device acknowledgment is fabricated.

Built-in storage backends support the new optional `storage.CommandClearer`
extension. Custom backends without it return an error when obsolete queued work
needs clearing; the service does not risk clearing unrelated commands.

## Reproducibility and release selection

The normal library and seed previews include a pinned `third_party/device-management-history`
gitlink, both source records in `GENERATED_FROM.json`, and eight mandatory runtime
contracts. Normal generation and verification discover that
historical input from `.gitmodules`. Snapshot and publication checks verify the
historical pin as well as the candidate pin.

The combined OS 27 API is promoted into the normal library with the historical
release pin retained. Runtime inventory and dispatch fixes apply to all supported
fleet versions. `make test` requires all eight OS 27 contracts. See the
[completion report](os27-promotion-2026-09-12.md) for fresh promotion validation.

See [decision 0052](../research/decisions/0052-mixed-os-fleets.md) for the interface,
compatibility and custom-store contracts. Local validation artifacts are retained
under `cover/mixed-fleet/` and are gitignored.
