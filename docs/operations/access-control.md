# Reference-server access control

The reference server runs MDM and DDM together. Roles are managed groups of
named API principals. Certificate setup roles (`customer`, `vendor`, `combined`) retain
their separate meaning.

A principal authenticates with a checksummed bearer token. The database stores
its digest; the plaintext is returned only on creation or rotation. Managed
role membership contributes `MDM::Role` parents to its Cedar entity. Membership
alone grants nothing. Administrators write explicit Cedar policies.

## Bootstrap and authority administration

Configure a protected `DM_BOOTSTRAP_TOKEN` secret and start the server. Exchange
it once with `dmctl -token @/protected/bootstrap -output human auth bootstrap
root`, redirecting stdout to a new private token file. This is the only endpoint
that accepts the bootstrap secret: `POST /admin/v1/auth/bootstrap`.

The first root and the consumed marker are committed together. The marker
survives restarts and removal of principals. Ordinary API requests never accept
the bootstrap secret. Remove its configuration when convenient; a restart is
unnecessary for consumption to take effect. In-memory deployments lose all
state on restart and are intended for development.

Root can read and mutate principals, roles and policies independently of Cedar,
including when an active policy fails validation. Root has no implicit fleet
permissions. Creating credentials, rotating/revoking them, changing memberships
or root status, and writing policies require root; Cedar cannot delegate those
operations. The last active root cannot be removed or demoted. Natural token
expiry can still require local recovery.

## Roles and policies

Using a stored root credential:

```sh
dmctl roles put operators -description 'Routine device diagnostics'
dmctl policies put operators -file - <<'CEDAR'
permit (principal in MDM::Role::"operators",
        action in MDM::Action::"OperatorActions", resource);
CEDAR
dmctl principals create inventory-worker -roles operators
```

Role names are immutable identifiers. `roles put` updates the description or
creates a record. `roles list`, `get` and `delete` provide the remaining lifecycle
operations. Deletion is refused while any principal or stored policy, including
an inactive policy, references the role. Create roles and named principals
before referencing them in policies.

| Action group | Explicit membership |
|---|---|
| `ViewerActions` | Enrollment/inventory/queue and selected credential metadata reads |
| `OperatorActions` | Viewer actions, device wakeup and five diagnostic commands |
| `DeviceAdminActions` | Operator actions plus declaration, set, blueprint publication/assignment and profile upload operations |
| `AuditorActions` | Audit, principal, role and policy reads |

The five diagnostic commands are `DeviceInformation`, `SecurityInfo`,
`ProfileList`, `InstalledApplicationList` and `CertificateList`. Group membership
is an explicit list in the permission catalogue. Adding a new action or command
does not automatically add it to a routine group.

Each known command has an `enqueueCommand.<RequestType>` permission. Erasure,
locking, recovery-key rotation, profile installation and other commands require
individual grants. Unrecognized raw commands require `enqueueUnknownCommand`.
The server decodes and authorizes a command before queueing or preparing its
encryption material.

Raw command responses require `readRawCommandResult`; profile content requires
`downloadConfigurationProfile`. Enrollment exports, declaration JSON, raw
blueprints and credential operations are also outside routine read grants.
Sensitive webhook destinations require `manageSensitiveWebhooks` in addition to
`manageWebhooks`; sensitive replay requires `replaySensitiveWebhooks` in addition
to `replayWebhooks`.

Use `dmctl actions`, `dmctl routes -output json`, `dmctl auth schema` and
`dmctl auth me` to inspect the current catalogue, mounted routes, exact Cedar
schema and authenticated principal. These require authentication but no policy
grant and contain no fleet data.

```sh
dmctl policies validate operators -file operators.cedar
dmctl policies put operators -file operators.cedar
dmctl policies deactivate operators
dmctl policies activate operators
```

Validation checks syntax, actions, resource types, context attributes and named
principal/role references. Inactive documents are preserved and excluded from
evaluation. Activating or updating a document requires validation. Invalid stored
documents can be replaced or deleted by root. An invalid active document blocks
the entire operational policy set; the server never silently drops a broken
forbid while retaining permits. Evaluation diagnostics also deny the request.

## Resource and request identities

Enrollment resources use `MDM::Enrollment::"<channel>/<escaped-id>/<escaped-parent>"`.
For a device, the empty parent leaves a trailing slash, for example
`MDM::Enrollment::"device/UDID-123/"`. User channels include the canonical stored
parent. Components use URL path escaping. The server resolves stored identities
before evaluation and rejects contradictory channel or parent input.

Other resources have their own types: `Declaration`, `DeclarationSet`,
`Blueprint`, `ConfigurationProfile` and `DEPAccount`. Collections use
`MDM::System::"any"` and have distinct list permissions. A policy on one item
does not authorize its collection. The catalogue declares available context:
`method`, `channel`, `set`, `blueprint` and `requestType`, as applicable.

Assignments in this release are fleet-wide. There are no tenant bindings,
device-group bindings, interactive users or SSO mappings. Administrators may
write explicit resource conditions in Cedar.

## Audit and recovery

SQL deployments durably capture administrative mutations, denials and sensitive
reads. Records include actor/credential identity, action, resolved resource,
deciding policies, policy version and outcome. Local mutations and their events
share a transaction. Sensitive response bytes are withheld until capture
succeeds. Remote mutations have authorization and completion records because
remote effects cannot participate in the local transaction. Ordinary reads do
not create audit traffic. Bodies, bearer tokens and returned secrets are omitted.

If all root credentials are lost or expired, use local database access and the
existing maintenance fence:

```sh
dmctl recovery pause -setup-file /protected/setup.json -ticket-file /protected/ticket
dmctl auth recover-root recovery-root -setup-file /protected/setup.json \
  -ticket-file /protected/ticket -token-file /protected/recovery-token
dmctl recovery resume -setup-file /protected/setup.json -ticket-file /protected/ticket
```

Recovery verifies the owned, drained fence inside the write transaction, creates
a new root without fleet grants and captures a persisted `recoverRoot` event. It
leaves maintenance paused and never reopens bootstrap. The output file must be
new and private. If writing it fails after commit, the error identifies the
created principal; repeat with a new name and then revoke the inaccessible one.

## Implementation and policy language

The [permission catalogue](../../server/internal/app/admincatalog.go) defines the
allowed actions, resource types and explicit action-group membership. The
[authorization manager](../../server/adminauth/manager.go) owns principal, role
and policy state. The [HTTP wrapper](../../server/internal/app/adminauthz.go)
resolves resource identities and applies route checks before handlers run.

Cedar describes [policy syntax](https://docs.cedarpolicy.com/policies/syntax-policy.html)
and its [authorization algorithm](https://docs.cedarpolicy.com/auth/authorization.html).
Cedar can skip a policy that produces an evaluation error. This server deliberately
denies an operation if any evaluation diagnostic occurs; invalid active documents
also block the operational policy set. This fail-closed policy is a project choice.
