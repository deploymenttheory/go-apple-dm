# 0052: Preserve mixed-OS fleet support across schema updates

Status: Accepted

Date: 2026-09-12

## Context

A fleet can simultaneously run older releases, the current release, and the next
release or its seed. For this transition, macOS 15, 26 and 27 are distinct fleet
members. Their release labels are not an arithmetic sequence. Updating Apple's
schema must preserve management of older devices, including properties Apple has
deleted from newer documentation.

## Decision

Generate a combined API from the candidate schema and a pinned historical Apple
checkout. `schemagen -history` merges previous keys into the current source,
preserves emission order and Go representations, and retains historical removal
boundaries. Current definitions supply new fields and updated availability.
Ambiguous moves, incompatible wire types, and unreviewed required-to-optional
changes remain subject to the existing generation/API guards.

The normal library and seed previews carry the historical checkout as
`third_party/device-management-history`; `GENERATED_FROM.json` records both
commits and content hashes. Normal generation and verification discover that
history input from `.gitmodules`. The library adopts OS 27 seed commit
`b0180185a5e4077070710033341b71d0cbe1a18a` with historical release commit
`67045e2fa06f528b196c01edee6a8bf88b844beb`. The eight OS 27 contracts run in
ordinary validation. The monitor preserves the historical pin and compares older
Apple stable snapshots against that release baseline without proposing a downgrade.
Later transitions must retain all
previously published contracts; the public API guard detects incomplete history.

The OS 27 legacy-profile alternative keeps `ProfileURL` as a Go string. Generated
marshal methods omit its empty value when using `ProfileAssetReference`.
Validation requires a URL or asset reference, and rejects the asset form before
27. Existing URLs serialize unchanged. Parental-control application item types
and removed historical fields retain their existing public names and signatures.
Apple's `removed: '0'` marker means unavailable, not an unspecified boundary.

The service validates each known command against the device's actual OS, version,
channel and observed capabilities. Unknown OS/version inventory does not authorize
advanced commands. `DeviceInformation` and `SecurityInfo` remain available to
bootstrap that inventory, subject to their known platform and input constraints.
The explicit `ValidateTargets: false` option continues to bypass availability.

An acknowledged, tracked device-channel inventory result refreshes product, OS
and build versions. Missing and malformed observations preserve previous values;
user-channel responses cannot replace the parent device's inventory. Queued work
is revalidated before dispatch, including after that same request updates the
inventory. Ineligible commands are individually cleared, their audit rows remain,
and the next eligible command can be delivered. `command-rejected` records only
the UUID, request type and a fixed reason code; it does not invent a device result.

`storage.CommandClearer` is an optional extension for exact command clearing.
The built-in in-memory, SQLite, PostgreSQL and MySQL backends implement it.
Custom stores without it fail safely if queued work becomes ineligible; the
service never issues a broad clear by assuming a custom store understands a new
filter field. No database migration is needed.

## Validation

Mixed-fleet tests use one core and one enqueue call across macOS 15.7, 26.4 and
27.0. They exercise shared inventory, pre-27 software-update commands and 27-only
enhanced logging on the appropriate user channel. An upgrade regression proves
that refreshed inventory rejects an obsolete queued command while preserving
the valid work behind it. Storage contract tests run the same inventory refresh
and exact clearing against every built-in backend. Generator and schema tests
retain older firewall support, removed fields, URL serialization and OS 27 asset
validation without relaxing public API checks.
