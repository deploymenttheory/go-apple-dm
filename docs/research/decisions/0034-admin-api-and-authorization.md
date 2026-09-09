# 0034: Admin API surface and authorization

## Context

Administrative routes can enqueue destructive commands, export secrets and change authorization policy. Credentials need distinct, revocable authority.

## Decision

The normal authorization model also applies to enrollment-profile issuance, enrollment-scoped command results and app push administration. See [0049](0049-server-managed-app-push.md) and the [API additions](../../operations/reference-bench.md). These are operational capabilities, not fixture-control routes.

Routes declare Cedar actions in the same table used to build the mux. Requests are evaluated with principal, action, resource and context under default-deny policies. Stored API credentials contain a checksum and are retained only as SHA-256 digests. Rotation and revocation invalidate stored tokens.

Root authority is a principal property checked outside Cedar for sensitive principal/policy administration. Credential issuance applies role-subset and directly-named-principal restrictions, and the last root cannot be removed, demoted or revoked. Policy writes validate referenced action names. Routine read responses project fields rather than expose complete stored records.

`DM_ADMIN_STORE` opens the principal/policy store on the process database, or memory when appropriate, and is disabled by default. `DM_ADMIN_TOKEN` remains accepted alongside it: a constant-time checked root credential that bypasses policy and is audited as `break-glass`.

## Rationale

Action context supports restrictions finer than route access, such as allowing inventory commands while denying erasure. Keeping root administration outside policy prevents a policy from granting its own authority. The configured bootstrap token provides access to an empty principal store.

## Constraints

The static token has no expiry and requires a restart to remove. Create principals, verify access with their credentials, unset the bootstrap token and restart. Its holder bypasses least-privilege policy. The reference server does not provide an administrative user/password/SSO system or OpenAPI document. Enqueue reports per-target skips in a successful HTTP response.

## Verification

Authorization tests cover default deny, context constraints, token lifecycle, issuance rules and lockout prevention. Application tests compare route metadata with mounted routes, verify projected responses, audit denials and break-glass use, and enforce component-specific route families.

## References

- [server/adminauth](../../../server/adminauth)
- [server/internal/app/admin.go](../../../server/internal/app/admin.go)
- [server/internal/app/adminintrospect.go](../../../server/internal/app/adminintrospect.go)
- <https://developer.apple.com/documentation/devicemanagement/commands-and-queries>
- <https://developer.apple.com/documentation/devicemanagement/check-in>
- <https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers>
- <https://developer.apple.com/documentation/devicemanagement/device-assignment>

Reference source identifiers and paths (relative to the named project):

- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a`, `server/authz/authz.go`, `server/authz/policy.rego`, `server/authz/errors.go`, `server/platform/middleware/authzcheck/authzcheck.go`, `server/contexts/authz/authz.go`, `server/contexts/token/token.go`, `server/service/middleware/auth/auth.go`, `server/service/middleware/auth/api_only.go`, `server/datastore/mysql/sessions.go`, `server/service/sessions.go`, `server/fleet/teams.go`, `server/activity/api/list_activities.go`, `server/service/handler.go`
- `zentralopensource/zentral@6b93d01d1bc8471ed98807b02a26b83452e8c8b7`, `server/pbac/{engine,cedar,entities,types}.py`, `server/accounts/{models,auth_backends,api_authentication}.py`, `server/accounts/views/api_tokens.py`, `zentral/utils/token.py`, `zentral/utils/{drf,views}.py`, `zentral/contrib/mdm/pbac.py`, `zentral/core/events/base.py`
- `smallstep/certificates@bb481fbf670c24721d5bdb1489ad0d1052c203b5`, `authority/authorize.go`, `authority/admin/api/{handler,middleware,admin}.go`, `authority/administrator/collection.go`, `authority/policy.go`, `logging/handler.go`
- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10`, `http/api/v1.go`, `http/api/pushcert.go`, `http/api/api.go`, `api/types.go`, `cmd/nanomdm/main.go`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151`, `http/api/v1.go`, `http/http.go`, `http/ddm/ddm.go`, `cmd/kmfddm/main.go`, `tools/api-*.sh`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107`, `cmd/micromdm/serve.go`, `pkg/httputil/httputil.go`, `platform/device/server.go`
- `micromdm/nanohub@3d73c1a83d5a042bfa5d31ba98d32de996007667`, `cmd/nanohub/nanohub.go`
- `micromdm/nanodep@2223746268b832f70be50f9ca27428a7785531be`, `cmd/depserver/main.go`, `http/http.go`
- `micromdm/nanocmd@f1302b5fc5684d3b0ad2ee5f2aa5f2c0ca9bd098`, `cmd/nanocmd/main.go`
