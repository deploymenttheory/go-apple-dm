# 0056: Unified reference server and managed RBAC

Status: Accepted

Date: 2026-09-20

## Context

The `mdm`, `ddm` and `all` runtime modes separated two parts of device management.
They were unrelated to the existing free-form authorization roles. The reference
server already used Cedar but lacked managed role records, a typed schema and
command-specific permissions. Static root tokens bypassed policy indefinitely.

Zentral's [policy-based access control](https://docs.zentral.io/en/latest/configuration/pbac/)
and [source](https://github.com/zentralopensource/zentral/tree/35024afe662c19ee50d6450731fac152bf64d850)
provide the reference model: managed roles, explicit action groups, typed Cedar
policies and separate permissions for sensitive operations. This implementation
retains the project's named bearer principals and Go stores; it does not import
Zentral's Django user, group or identity-provider architecture.

## Decision

Run MDM and DDM together in the reference binary. Preserve the reusable protocol
adapters, but remove split-runtime configuration, private listener, bench mode
and CI fixtures. Keep PKI workflow roles separate.

Add persisted roles and normalized membership with referential integrity. Roles
grant nothing without administrator-authored Cedar. Use a versioned permission
catalogue to generate the schema, route introspection and explicit routine action
groups. Authorize decoded commands by request type before side effects. Separate
raw content, destructive operations and credential access from routine grants.

Use one-time first-root bootstrap with a persisted consumed marker. Root manages
authorization independently of Cedar so policy repair remains possible. Root has
no implicit operational grants. Lost-root recovery requires local access and an
owned, drained maintenance fence, and leaves a persisted event.

Fail closed on invalid active policy sets or evaluation diagnostics. Preserve
legacy policies for explicit repair, including forbids. Migrate role identities
and memberships without changing token digests or device data. No compatibility
mode or automatic permission broadening is introduced; this is an alpha project.

## Consequences and verification

Existing split deployments and static-token clients need configuration changes.
Policy authors use typed resources and separate list/content permissions. New
actions never join routine action groups implicitly. Authorization remains
fleet-wide, with no tenancy, device-group bindings or interactive identities.

Storage contracts cover managed roles, inactive policy references and concurrent
bootstrap. HTTP tests cover first-root persistence, explicit grants, command and
sensitive-read denials, schema validation, activation and repair of broken active
policies. The unified bench and quickstart exercise actual stored credentials.
See [operations](../../operations/access-control.md) for the complete contract.
