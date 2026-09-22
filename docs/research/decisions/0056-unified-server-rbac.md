# 0056: Unified reference server and managed RBAC

## Context

The reference server composes device management in one runtime. API principals
need managed role membership and explicit, inspectable grants for operational
capabilities and sensitive data.

## Decision

Run MDM and DDM together in the reference binary. Reusable protocol adapters remain
available to custom compositions. Certificate workflow roles (`customer`, `vendor`,
`combined`) are separate from administrative roles.

Persist roles and normalized principal membership with referential integrity.
Membership grants nothing without administrator-authored Cedar. Use the permission
catalogue to build the typed schema, route introspection and explicit routine
action groups. Authorize decoded commands by request type before side effects.
Separate raw content, destructive commands and credential access from routine reads.

Use one-time first-root bootstrap with a persisted consumed marker. Root manages
authority independently of Cedar, so policy repair remains possible, but needs
explicit operational grants. Lost-root recovery requires local database access and
an owned, drained maintenance fence; it records a persisted recovery event.

Invalid active policy sets and any evaluation diagnostic deny operational access.
Policies remain stored for explicit repair or removal; the server does not silently
discard a broken forbid or broaden its permissions.

## Constraints and verification

New actions never join routine groups implicitly. Authorization is fleet-wide,
without tenancy, device-group bindings or interactive user identities.

Storage contracts cover managed roles, inactive policy references and concurrent
bootstrap. HTTP tests cover first-root persistence, explicit grants, command and
sensitive-read denials, schema validation, policy activation and repair. The lab
and quickstart exercise stored credentials against the unified runtime.

## References

- [Access control](../../operations/access-control.md)
- [Permission catalogue](../../../server/internal/app/admincatalog.go)
- [Authority manager](../../../server/adminauth/manager.go)
- [Cedar schema](https://docs.cedarpolicy.com/schema/schema.html)
- [Zentral policy-based access control](https://docs.zentral.io/en/latest/configuration/pbac/)
