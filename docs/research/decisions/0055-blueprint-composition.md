# 0055: Blueprint composition and publication

Status: Accepted

Date: 2026-09-18

## Context

The DDM engine already stores declarations, resolves set assignments, maintains
advertised versions and serves device status. Authoring a workflow spanning
multiple configurations and conditional activations still requires callers to
build and update its graph manually. Apple also permits existing configuration
profiles through `com.apple.configuration.legacy`.

## Decision

Use the name Blueprint for local authoring. A pure library compiler accepts
stable source identifiers, generated typed or raw known payloads, pinned profile
references and activations. Source uses `Declarations`, `Identifier` and
`StandardConfigurations`; these convenience objects compile to Apple’s declaration
structures. It resolves the graph, validates payloads and
predicates, and produces deterministic identifiers and canonical declarations.
Declarations are copied into each Blueprint's namespace. Existing AXM client
Blueprint resources continue to describe Apple-hosted objects independently.

Add atomic replacement publication to the existing DDM engine. A publication
preserves set assignments, removes omitted memberships, retains old versions and
queues changes once per affected enrollment. Transactions explicitly implement
publication locking, including serialization before the first set row exists.
The reference database's fixed lock rows live in each dialect's initial migration.

The reference server owns authoring persistence, optimistic revision checks,
admin policy/audit and CLI commands in `server/blueprints`. Configuration profile
uploads, metadata and HTTPS delivery belong to `server/configurationprofile`.
The Blueprint manager resolves uploaded references through that package. It reuses the
encrypted protocol-state store for specs and immutable profile blobs. SQL uses
the shared unit of work; in memory the state transaction commits last inside the
DDM transaction's private write set. Compiler namespaces are reserved against
low-level admin mutation. No schema migration for an existing installation is
introduced because the product is new.

The `ConfigurationProfile` convenience object creates Apple’s `LegacyProfile`
declaration and defaults to `ProfileURL`. Its `UseProfileAssetReference` option
selects Apple’s `ProfileAssetReference` field. OS 27 asset delivery is explicit and
accepts only unsigned plist data with digest, size, MIME type and MDM
authentication. Original profile bytes and identities are preserved. Device
downloads use full enrollment identity, pinning, enabled state and certificate
status, together with current eligibility and the last advertised snapshot.
Retained obsolete revisions never authorize access on their own. Split roles
carry downloads over the same authenticated private hop as DDM requests.

## Consequences

Developers can reuse the compiler without taking on a server or database model.
Atomic publication prevents partial server state; Apple devices still apply
declarations independently. Profile/native setting conflicts and imported profile
identifier collisions retain normal Apple behavior. Hosted profile scope is
validated and is not automatically split across channels.

Source and uploaded files participate in encryption, rotation and backup. Initial
retention is conservative: no automatic profile garbage collection. A UI,
reusable catalog, smart groups, approval workflows and Apple Business synchronization
remain outside this feature. See the [operator/API guide](../../operations/blueprints.md).
