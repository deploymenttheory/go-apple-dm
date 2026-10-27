# 0052: Preserve mixed-OS fleet support across schema updates

Status: Accepted

Date: 2026-09-12

## Context

A fleet can simultaneously run older releases, the current release, and the next
release or its seed. macOS release labels are not an arithmetic sequence: 26
follows 15. Updating Apple's schema must preserve management of older devices,
including properties Apple has deleted from newer documentation.

## Decision

Support is guaranteed from macOS 26 onwards: the schema contracts and acceptance
scenarios target macOS 26 and later. Earlier macOS releases are best effort; they
may function but are not actively tested. Per-item availability applies in both
tiers. Each feature's introduction, deprecation and removal boundaries decide its
eligibility at every OS version, so a best-effort device receives only what its
version supports and a guaranteed device is still refused a feature it predates.

Generate a combined API from the current schema and the final schema release of
each earlier supported major version. `schemagen -history` merges each retained
release into the current source, newest first. It preserves emission order and Go
representations and retains historical removal boundaries. Current definitions
supply new fields and updated availability; a key Apple no longer documents keeps
the availability recorded by the newest retained release that still documents it.
Ambiguous moves, incompatible wire types, and unreviewed required-to-optional
changes remain subject to the existing generation/API guards.

`third_party/apple-device-management/current` follows Apple's `release` branch within
the current major schema version (n). Each retained earlier major has a fixed
submodule beside it, in a directory named for the final schema release Apple
published for that major: `26.4` is n-1 while `current` carries 27.x. A schema
release version is the newest OS version the schema introduces; it need not match
the last OS release of that major. When an adopted Apple release starts a new major
schema version, its adoption pull request pins the previous major's final release in
a new versioned submodule, keeps the existing retained releases and moves `current`.
Adopting 28.0 therefore retains 27.x's final release as n-1 and keeps 26.4 as n-2.
Retained releases accumulate: removing one ends generated support for devices on
that major, so it is a reviewed change whose exported-name removals must be recorded.
A release retained for a guaranteed major, such as `26.4` for macOS 26, stays
retained while that major is guaranteed; removing it requires first moving the
guaranteed floor above that major.

Normal generation, verification and the OS 27 contracts discover retained releases
from `.gitmodules`. `GENERATED_FROM.json` records the current commit and each
retained release's commit, version and content hash, newest first. Seed assessments
use their own isolated immutable source workspaces; a candidate that starts a new
major is assessed with the published pin as its newest retained release, as its
adoption would generate it. The original OS 27 seed is retained as an immutable
canary snapshot. The thirteen OS 27 contracts run in ordinary validation. The Apple
Device Management Schema updates workflow checks upcoming schemas with the current
generator and uses the shipped release pin as its control. Mixed-fleet behaviour
contracts remain in ordinary CI. Every transition must retain all previously
published contracts; the public API guard detects incomplete history.

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

Availability results are machine-readable. Each support check names the rule it
applied: `not-available`, `introduced`, `removed`, `deprecated`, `channel`,
`scope`, `enrollment` or `context`. A removed key rejects the target; a
deprecated key is reported as a warning and is still delivered, so `Validate`
returns only blocking errors. Generated `ValidateInto` methods record into a
caller's `validation.Collector`, which returns key-level availability issues for
errors and warnings: the key's schema key path, target OS, rule, severity and the
key's introduced, deprecated and removed versions on that OS, where a zero
version is an absent bound. Issues never contain configuration values; schema key
paths omit wire map keys and array positions, and repeated values of one key
yield one issue. The DDM compatibility report attaches these issues to its
declaration issues. A withheld `unsupported-target` declaration lists its keys,
and an eligible declaration with deprecated keys has a non-withheld `deprecated`
issue listing them.

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
enhanced logging on the appropriate user channel. The macOS 15.7 member checks
per-item floor evaluation for a best-effort device; it does not make macOS 15 a
tested release. An upgrade regression proves
that refreshed inventory rejects an obsolete queued command while preserving
the valid work behind it. Storage contract tests run the same inventory refresh
and exact clearing against every built-in backend. Generator and schema tests
retain older firewall support, removed fields, URL serialization and OS 27 asset
validation without relaxing public API checks. Generator tests merge two retained
releases and record them newest first, and require each retained release named in
`.gitmodules` to be checked out. Workflow tests adopt a new major release both when the
pin is the previous major's final release and when Apple published a later one.
Compatibility tests report a macOS 27.0 key as withheld for macOS 26.4 and
eligible for 27.2, a key Apple removed in iOS 18.0 with its window, keys that are
unavailable on macOS, and a key deprecated in iOS 27.0 as a delivered warning.
They serialize each report and require it to contain no fixture values.
