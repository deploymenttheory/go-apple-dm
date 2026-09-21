# 0034: Admin API surface and authorization

## Context

Administrative routes can enqueue destructive commands, export device secrets and
change authority. Credentials need distinct, revocable permissions.

## Decision

The unified reference server always opens the principal, role and policy store.
`DM_BOOTSTRAP_TOKEN` is accepted only by `POST /admin/v1/auth/bootstrap`, which
atomically creates the first root principal and consumes bootstrap. Ordinary
requests authenticate stored checksummed bearer credentials; only their digests
are persisted. Creation and rotation return plaintext once.

Root administers principals, roles and policies independently of Cedar, including
policy repair. Root has no implicit fleet permissions. Managed role membership
grants nothing without explicit policy. The store prevents removing or immediately
expiring the last active root; natural expiry or loss of credentials requires
[fenced local recovery](../../operations/access-control.md).

Routes and the permission catalogue define typed actions, resources and contexts.
Known commands require `enqueueCommand.<RequestType>` before side effects; unknown
commands require `enqueueUnknownCommand`. FileVault escrow preparation requires
`enqueueCommand.InstallProfile`. Raw results require `readRawCommandResult`;
redacted enrollment status requires `readEnrollmentStatus`. Separate permissions
cover collection metadata, stored content, credentials and sensitive webhook export.

## Rationale and constraints

Explicit action groups prevent new capabilities inheriting routine grants.
Policy validation checks actions, resources, context and named role/principal
references. Invalid active documents or evaluation diagnostics deny operational
requests. This is stricter than Cedar's skip-on-error behavior. Root-only authority
operations remain available for repair and cannot be delegated through Cedar.

Local mutations and their outcome capture commit together. External calls have
separate authorization/completion records. Administrative credentials do not
provide device-channel authentication. The server has no interactive
administrator login, tenancy or device-group authorization layer.

## Verification

Storage contracts cover credential lifecycle, membership integrity and concurrent
bootstrap. HTTP tests cover explicit grants, granular command and sensitive-read
denials, policy activation/repair, resource identity and transactional auditing.

## References

- [Managed access control](../../operations/access-control.md)
- [Permission catalogue](../../../server/internal/app/admincatalog.go)
- [Route authorization](../../../server/internal/app/adminauthz.go)
- [Authority manager](../../../server/adminauth/manager.go)
- [Cedar authorization](https://docs.cedarpolicy.com/auth/authorization.html)
- [Apple MDM commands](https://developer.apple.com/documentation/devicemanagement/commands-and-queries)
