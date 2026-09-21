# 0052: Preserve mixed-OS fleet support across schema updates

Status: Accepted

Date: 2026-09-12

## Context

A fleet can simultaneously run older releases, the current release, and the next
release or its seed. macOS 15, 26 and 27 are distinct fleet members. Their release labels are not an arithmetic sequence. Updating Apple's
schema must preserve management of older devices, including properties Apple has
deleted from newer documentation.

## Decision

Generate a combined API from the candidate schema and a pinned historical Apple
checkout. `schemagen -history` merges previous keys into the current source,
preserves emission order and Go representations, and retains historical removal
boundaries. Current definitions supply new fields and updated availability.
Ambiguous moves, incompatible wire types, and unreviewed required-to-optional
changes remain subject to the existing generation/API guards.

The normal library carries the historical checkout at the versioned path selected by
the `apple-device-management-compatibility` `.gitmodules` entry; seed assessments use
their own isolated immutable source workspace. `GENERATED_FROM.json` records both
commits and content hashes. Normal generation and verification discover that history
input from `.gitmodules`. The library ships Apple release commit
`09f249a06e7e3289930bf6d05f38fb562f748ebf` with historical release commit
`67045e2fa06f528b196c01edee6a8bf88b844beb`. The original OS 27 seed is retained
as an immutable canary snapshot. The thirteen OS 27 contracts run in ordinary
validation. The monitor checks upcoming schemas with the current generator and uses the shipped
release pin as its control. Its scope is code generation; mixed-fleet behaviour
contracts remain in ordinary CI.
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

`devicemanagement/osversion` owns the shared major/minor/patch representation,
parsing, comparison and named macOS major release constants. Callers use this
package directly; `support.Target` and `support.OSSupport` declare their version
fields as `osversion.Version`. The support package owns feature availability and
management-context checks. See [version construction and validation](../../operations/os-versions.md).
Generated availability tables use these shared versions for introduced,
deprecated and removed boundaries; a major release constant does not discard a
feature's minor or patch floor.

Reviewed enum-value availability supplements are emitted by the generator and
queried through `profiles.ValueSupport(path, value)`. They inherit the containing
field's platform and enrollment constraints and can only raise an introduction
floor. Field relationships and format rules are emitted into typed `Validate`
methods, so profile inspection, declaration delivery and direct schema consumers
use the same checks. There is no separate reflection-based compatibility validator.

Declaration validation also enforces Apple's `allowed-scopes` and
`allowed-enrollments` metadata when the target channel is specified. Device and
user channels map to system and user scopes; user enrollment is distinct from
the user channel of a supervised device. Shared iPad scope overrides replace the
ordinary scope list, including an explicit empty list that permits no scope.
Omitted channels preserve OS/version-only validation. Local enrollment is not
inferred from an unsupervised MDM target.

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
filter field.

## Validation

Mixed-fleet tests use one core and one enqueue call across macOS 15.7, 26.4 and
27.0. They exercise shared inventory, pre-27 software-update commands and 27-only
enhanced logging on the appropriate user channel. An upgrade regression proves
that refreshed inventory rejects an obsolete queued command while preserving
the valid work behind it. Storage contract tests run the same inventory refresh
and exact clearing against every built-in backend. Generator and schema tests
retain older firewall support, removed fields, URL serialization and OS 27 asset
validation without relaxing public API checks.
