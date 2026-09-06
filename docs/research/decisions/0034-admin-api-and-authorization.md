# 0034: Admin API surface and authorization

Status: accepted
Date: 2026-09-02
Phase: 8

## Apple sources

- Doc: <https://developer.apple.com/documentation/devicemanagement/commands-and-queries>
  (the command surface an operator enqueues through this API)
- Doc: <https://developer.apple.com/documentation/devicemanagement/check-in>
  (the enrollment state this API lists: topic, push token, push magic, unlock token, bootstrap token)
- Doc: <https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers>
  (the push certificate this API accepts)
- Doc: <https://developer.apple.com/documentation/devicemanagement/device-assignment>
  (the device enrollment service accounts this API administers)
- YAML: `third_party/device-management/mdm/commands/*.yaml` (the request types `POST /commands` validates against)
- YAML: `third_party/device-management/mdm/checkin/tokenupdate.yaml` (`UnlockToken`, `PushMagic`, `Token`: the fields the enrollment projection must never return)
- YAML: `third_party/device-management/mdm/checkin/setbootstraptoken.yaml` (`BootstrapToken`, likewise)

Framing: Apple defines the device-facing protocol and says nothing about how a server is
administered. The admin API is therefore ours, and its job is to make the reference server
demonstrable and to give `dmctl` (0035) something to call — not to be a product's API. A product
embeds `service.Core`, `ddm.Engine` and the stores directly and brings its own API and its own
identity model, as Fleet does.

Dependency note: this record adds `github.com/cedar-policy/cedar-go` (Apache-2.0, v1.8.0), the
first non-driver dependency in the module. Records 0031 and 0032 rejected `go-jose` and
`fxamacker/cbor` on the ground that "the plan of record admits no new module dependencies", and
that reasoning is narrowed rather than abandoned: both of those replaced roughly two hundred lines
of well-specified parsing — one JWS serialisation, one CBOR subset — where writing and fuzzing our
own was cheaper than carrying a general library. An authorization policy language is not that.
Hand-rolling one is precisely how step-ca ended up with `strings.HasPrefix` as its entire role
model, and both projects in the survey that took authorization seriously concluded they needed an
engine. Cedar is purpose-built for authorization rather than general policy, is stable at v1, and
pulls exactly one transitive dependency (`golang.org/x/exp`), against the very large tree OPA
brings. The evaluator stays behind `adminauth`, so the blast radius of a future swap is one package.

## References read

- `fleetdm/fleet@111bc85f1d6cf1e7952efb6f9ea9d6277c36529a` `server/authz/authz.go`, `server/authz/policy.rego`, `server/authz/errors.go`, `server/platform/middleware/authzcheck/authzcheck.go`, `server/contexts/authz/authz.go`, `server/contexts/token/token.go`, `server/service/middleware/auth/auth.go`, `server/service/middleware/auth/api_only.go`, `server/datastore/mysql/sessions.go`, `server/service/sessions.go`, `server/fleet/teams.go`, `server/activity/api/list_activities.go`, `server/service/handler.go`
- `zentralopensource/zentral@6b93d01d1bc8471ed98807b02a26b83452e8c8b7` `server/pbac/{engine,cedar,entities,types}.py`, `server/accounts/{models,auth_backends,api_authentication}.py`, `server/accounts/views/api_tokens.py`, `zentral/utils/token.py`, `zentral/utils/{drf,views}.py`, `zentral/contrib/mdm/pbac.py`, `zentral/core/events/base.py`
- `smallstep/certificates@bb481fbf670c24721d5bdb1489ad0d1052c203b5` `authority/authorize.go`, `authority/admin/api/{handler,middleware,admin}.go`, `authority/administrator/collection.go`, `authority/policy.go`, `logging/handler.go`
- `micromdm/nanomdm@494831912abf895b41d533b5a9d81e2d6aa8ae10` `http/api/v1.go`, `http/api/pushcert.go`, `http/api/api.go`, `api/types.go`, `cmd/nanomdm/main.go`
- `jessepeterson/kmfddm@4b75a7652a71c9e74ccbcb78c8a7285211670151` `http/api/v1.go`, `http/http.go`, `http/ddm/ddm.go`, `cmd/kmfddm/main.go`, `tools/api-*.sh`
- `micromdm/micromdm@904493b9500ffc8a21846846781e362f5c612107` `cmd/micromdm/serve.go`, `pkg/httputil/httputil.go`, `platform/device/server.go`
- `micromdm/nanohub@3d73c1a83d5a042bfa5d31ba98d32de996007667` `cmd/nanohub/nanohub.go`
- `micromdm/nanodep@2223746268b832f70be50f9ca27428a7785531be` `cmd/depserver/main.go`, `http/http.go`
- `micromdm/nanocmd@f1302b5fc5684d3b0ad2ee5f2aa5f2c0ca9bd098` `cmd/nanocmd/main.go`

## Known pitfalls found

- `kmfddm/cmd/kmfddm/main.go:64-66`: an empty `-api` logs "empty API key; API disabled" and the
  server keeps running, while `docs/operations-guide.md:21` calls the flag "Required." The failure
  mode is a live server answering 404 on every admin route.
- `nanocmd/cmd/nanocmd/main.go:142`: the same, with no log line at all.
- `nanomdm/http/api/v1.go:41-42` and `kmfddm/http/api/v1.go:33-34`: both state that authentication
  is the caller's problem, layered by registering handlers on the right mux. A route registered on
  the wrong mux object is silently public.
- `smallstep/certificates` `authority/authorize.go:187`: the entire role check is
  `strings.HasPrefix(r.URL.Path, "/admin/admins") && r.Method != "GET" && adm.Type != SUPER_ADMIN`.
  Renaming a route silently changes authorization.
- `fleetdm/fleet` `server/datastore/mysql/sessions.go:105-122,177-206`: bearer session keys are
  stored and looked up in plaintext (`WHERE s.key = ?`) in the same schema where passwords are
  bcrypt'd. A dump or replica yields usable admin credentials.
- `fleetdm/fleet` `server/service/sessions.go:941-943`: `if session.APIOnly { sessionDuration = 0 }`
  — API-only tokens never expire by design; human sessions use an idle timeout from `accessed_at`,
  so a polling client never ages out.
- `fleetdm/fleet` `server/service/middleware/auth/api_only.go:91-93`: role-based 403s log at debug,
  and the comment says that is "how these denials went unnoticed during debugging". Denials never
  reach the activity feed.
- `zentralopensource/zentral` `server/server/settings.py:90-92`: `DEFAULT_PERMISSION_CLASSES` is
  `IsAuthenticated`, so a DRF view that forgets `permission_classes` is reachable by every API token.
- `zentralopensource/zentral` `server/accounts/models.py:90-98`: `APIToken` carries only `expiry` and
  `name`; a token is its user's full authority, with no scopes.
- `nanomdm/http/api/pushcert.go:89`: the API key uploads the APNs certificate *and its private key*,
  with PEM repair, the deprecated `x509.IsEncryptedPEMBlock`, and topic extraction all inline in the
  HTTP handler rather than in the store.
- `nanomdm/api/api.go` (`EnqueueWithPush`): returns 200/207/500 derived from per-target domain
  outcomes, conflating "the request was understood" with "every target accepted it".
- `micromdm/micromdm` `cmd/micromdm/serve.go:330`: the API key also streams the entire BoltDB over
  HTTP; `platform/device/server.go:16-21` requires the auth middleware to be chained onto every
  individual endpoint, so a new field on an `Endpoints` struct is silently public.
- `nanohub/cmd/nanohub/nanohub.go:223-272`: one `authMW` gates NanoMDM, NanoCMD and KMFDDM together,
  with no way to enable or credential them separately.
- Ours: `internal/app/app.go:341` mounts the admin mux only when `cfg.Role != RoleMDM`, but
  `service.Core` and the command queue exist only on the `mdm` and `all` roles — so in the split
  deployment E2E-010 exercises, no process can enqueue a command over HTTP.
- Ours: `internal/app/admin.go:203` accepts only `device` and `user`, so Shared iPad users and both
  User Enrollment channels are unaddressable, although record 0029 routes commands to them.
- Ours: `internal/app/admin.go:158` hardcodes `storage.Page{Limit: 1000}` for status values and
  ignores the engine's query type; `docs/security/threat-model.md:64` promises "the library exposes
  hooks for authz" and no such hook exists.

## What they do

- **NanoMDM, NanoHUB, KMFDDM, NanoCMD, NanoDEP**: one `-api` flag, HTTP Basic, a hardcoded username
  equal to the program name; one shared secret for the whole server. No principal, no scopes, no
  expiry, no revocation, no actor in any log line. Comparison is constant-time
  (`nanodep/http/http.go:29-41`). Admin and device routes share one listener. Only NanoDEP refuses
  to start without a credential (`cmd/depserver/main.go:58-62`), and only NanoDEP separates inbound
  caller auth from outbound Apple auth, deleting the inbound `Authorization` header before proxying.
- **MicroMDM**: the operator supplies the API key; nothing generates, hashes or stores it
  server-side. `mdmctl` persists it in cleartext at `~/.micromdm/<name>.json` under a directory
  created `0777`, and `dmctl config print` echoes it.
- **smallstep/certificates**: an admin is a persisted `(subject, provisioner)` pair authenticated by
  an x5c-signed JWT verified against the CA's own roots, with single-use replay protection, a
  one-minute clock leeway, and an audience bound to the request path
  (`authority/authorize.go:104-190`). Route wiring is default-deny by construction: all ~30
  registrations in one `Route()` compose the authn wrapper. Anti-lockout invariants protect the last
  super admin (`administrator/collection.go:129,190`) and reject policy changes that would lock out
  any admin (`policy.go:180-200`). Two roles, and the only role check is the URL prefix match above.
- **Fleet**: OPA/Rego compiled into the binary — `policy.rego` is 1588 lines over ~50 object types
  with `default allow = false`, evaluated per request on `{subject, object, action}`. Six roles at
  global or team scope. `authzcheck` wraps every endpoint and turns "handler returned without
  authorizing" into a 403, with a greppable `// skipauth:` convention for its 190 exceptions.
  `AuthorizeOrNotFound` returns 404 rather than 403 only when the caller also fails `read`.
  `gitops` gets `selective_read`/`selective_list` instead of `read`/`list`. Activities record the
  actor with `ActorAPIOnly` distinguishing a human from CI — but never record denials.
- **Zentral**: policy-based access control on AWS Cedar, with Django's permission backend
  deliberately gutted to return `set()`. API tokens are type-prefixed (`ztlu_`/`ztls_`), 178 bits of
  entropy, carry a base62 CRC32 checksum, and are stored only as a BLAKE2b-256 digest. Separate
  actions exist for secret *reveal* (`viewFileVaultPRK`, `viewAdminPassword`, `viewDeviceLockPIN`).
  Policy editing is excluded from the policy system that bounds it and needs a local, non-SSO
  superuser session. Audit events name the credential (token pk, name, expiry) alongside the user,
  carry before/after values, and are emitted from `transaction.on_commit`. A principal may only mint
  a token whose roles are a subset of its own.

## What we do better

1. Authorization is a policy language, not a scope string. Each admin route declares a Cedar action
   as data in one route table; a request becomes a Cedar `{principal, action, resource, context}`
   evaluated against operator-editable policies with `default deny`. Authorization is therefore
   never inferred from a URL prefix (step-ca) and never implied by which mux object a handler was
   registered on (the Nano family). A test walks the table and the mounted mux and fails when they
   disagree or when a route declares no action, which is a build-time proof where Fleet's
   `authzcheck` is a runtime tripwire its own package doc admits proves only that *a* check ran,
   not the right one.
2. Least privilege is expressible per request, not merely per credential. Because the action carries
   context, a policy can say that a CI principal may enqueue `DeviceInformation` and nothing else,
   or that exporting enrollments is forbidden to everyone outside a break-glass role. A scope list
   cannot express either: the best it can do is "may enqueue", which in this API means "may erase
   the fleet". Every reference except Fleet and Zentral has one credential that does everything, and
   Fleet's vocabulary is compiled in, so a deployment cannot add a rule without a Fleet release.
3. Credentials are revocable, checksummed, and never stored. A token is type-prefixed with a
   checksum, so a malformed value is rejected before a database round trip, and only its SHA-256
   digest is stored; `Rotate` invalidates the previous value immediately and `Revoke` needs no
   restart. Fleet stores bearer tokens in plaintext beside bcrypt'd passwords and never expires
   API-only tokens.
4. Authorization denials are audited. `AdminAction` and `AdminDenied` both carry the principal,
   method, path and scope at a visible level, and never the token or the body. Neither Fleet (debug
   level, never in the activity feed) nor Zentral (a log line, no event) can answer "who probed what
   and was refused", and our threat model already claims repudiation coverage.
5. Configuration fails closed. An admin API with neither a principal store nor an `AdminAuthorizer`
   is a `Build` error naming the missing setting; running without authorization takes an explicit
   opt-out. KMFDDM and NanoCMD both keep serving with the API silently absent.
6. The API is mounted per role by what that role can actually back: `commands` needs `Core`
   (mdm/all), `enrollments`, `commands-read` and `pushcerts` need `Store` (every role), and a
   `DDMURL`-forwarding `mdm` role registers no DDM family, because a declaration written there would
   land in a database no device reads. This closes the gap where a split deployment could not
   enqueue a command at all, and no reference has roles to get this wrong.
7. Command payloads are validated against Apple's schema before they are queued. `POST /commands`
   resolves the generated type through `commands.ByID`, decodes strictly so an operator's typo is a
   400 rather than a silently dropped key, validates shape against a zero `support.Target`, and
   leaves the per-target OS, channel and supervision check to `Core.Enqueue`. It never falls back to
   `Store.Enqueue`, which would skip the hook chain, that check, and the `CommandQueued` event.
   NanoMDM enqueues any plist it is handed.
8. HTTP status describes the request and the body describes each target. A well-formed enqueue is
   200 with `Queued` and `Skipped`, where each skip carries the wrapped `ErrUnsupportedTarget` text.
   NanoMDM returns 207 derived from domain outcomes.
9. Every read is projected, never a struct dump. The enrollment projection omits `UnlockToken`,
   `Push.Token`, `PushMagic`, `AuthenticateRaw` and `TokenUpdateRaw`, surfacing escrowed tokens as
   booleans and timestamps; `UserAuth` surfaces neither challenge nor token. MicroMDM's `GetDevices`
   returns the whole record.
10. Policy administration is excluded from the policy system that bounds it. Editing policies and
    principals is gated by a `Root` flag on the principal row, in Go, outside Cedar — because a
    policy that can edit policies can grant itself anything. Zentral draws exactly this line, and
    step-ca does not: any step-ca admin can rewrite provisioners and policy.
11. Three invariants prevent lockout and escalation: a principal may only issue a credential for a
    principal whose roles it already holds; a principal a policy names *directly* cannot have its
    credential issued by role-subset reasoning at all, because its authority no longer follows from
    its roles; and the last root principal cannot be deleted, demoted, or revoked. The first two are
    Zentral's `can_issue_credentials_for`, the third is step-ca's last-super-admin guard. No Go MDM
    reference has any of them.
12. A policy that names an action nobody serves is refused when it is written. Cedar parses such a
    policy happily and it then silently never grants — the exact failure Zentral's schema validation
    exists to prevent — so every action id in a submitted policy is checked against the route
    table's registry, and the error names the actions that do exist. This gets Zentral's write-time
    guarantee while staying on cedar-go's stable API, since its schema validator lives in
    `x/exp`, which is explicitly outside the module's compatibility promise.

## Verified by

1. `adminauth.TestAuthorize/DefaultDeny`, `/RoleGrant`, `/DecisionNamesThePolicy`,
   `app.TestAdminRoutes/EveryRouteHasAnAction`, `/MatchesMountedMux` (prove claim 1; would fail on
   step-ca because a route rename moves it out of the `/admin/admins` prefix, and on nanomdm and
   kmfddm because a handler on the wrong mux is public with no table to compare against).
2. `adminauth.TestAuthorize/ContextNarrowsAnAction` (a CI principal enqueues `DeviceInformation`
   and is refused `EraseDevice` under one action), `/ForbidOverridesPermit` (export forbidden
   except to a break-glass role), `app.TestMigration/DisabledByDefault` (prove claim 2; would fail
   on every reference with a single credential, and cannot be expressed at all by a scope list).
3. `adminauth.TestToken/Checksum`, `/HashedAtRest`, `/RotateInvalidatesOld`, `/RevokeIsImmediate`,
   `adminauthtest.RunSuite/TokenDigestOnly` (prove claim 3; would fail on Fleet because the plaintext
   key is the stored value and API-only tokens never expire).
4. `app.TestAdminAudit/PublishesAdminAction`, `/PublishesAdminDenied`, `/NeverLogsToken`,
   `/NeverLogsBody` (prove claim 4; would fail on Fleet because a denial is a debug line that never
   reaches the activity feed, and on Zentral because it emits no event).
5. `app.TestAdminConfig/EmptyCredentialIsABuildError`, `/ExplicitOptOutRuns` (prove claim 5; would
   fail on kmfddm and nanocmd, which run with the API silently disabled).
6. `app.TestAdminFamilies/EnqueueNotOnDDMRole`, `/ForwardingMdmRoleHasNoDDMFamily`,
   `/EnrollmentsOnEveryRole` (prove claim 6).
7. `app.TestEnqueue/RejectsUnknownPayloadKey`, `/UnknownRequestTypeSuggests`,
   `/ValidatesShapeNotTarget`, `/NeverBypassesCore` (prove claim 7; would fail on nanomdm because
   `RawCommandEnqueueHandler` accepts any plist and the device reports the error instead).
8. `app.TestEnqueue/PartialSkipIsStill200`, `/SkipCarriesTheReason` (prove claim 8; would fail on
   nanomdm because the transport status encodes domain outcomes).
9. `app.TestEnrollments/NeverReturnsUnlockToken`, `/NeverReturnsPushMagic`, `/NeverReturnsRawPlists`,
   `app.TestUserAuth/RedactsChallengeAndToken`, `app.TestPushCerts/NeverReturnsKey` (prove claim 9;
   would fail on micromdm because `GetDevices` returns the stored record).
10. `adminauth.TestAdministration/PolicyEditingNeedsRoot`, `/RootIsNotPolicyGrantable` (prove claim
    10; would fail on step-ca, where any admin may rewrite provisioners and policy).
11. `adminauth.TestAdministration/SubsetOnlyIssuance`, `/PrincipalNamedByPolicyIsRefused`,
    `/LastRootCannotBeRemoved`, `/LastRootCannotBeDemoted`, `/LastRootCannotBeRevoked` (prove
    claim 11).
12. `adminauth.TestPutPolicyRejectsUnknownAction` (the error names the known actions and nothing is
    stored), `adminauth.TestPutPolicyRejectsMalformedSource` (prove claim 12; would fail on a
    server that only parses, which is what cedar-go's stable API alone gives you).

Cross-cutting: `app.TestEnrollmentFromPath/SharedIPadUser`, `/UserEnrollmentUser`,
`/UserEnrollmentDevice` (all five channels addressable);
`app.TestStatusValues/PrefixFilters`, `/PaginatesWithCursor` (the hardcoded page limit is gone);
`storagetest.RunEnrollmentSuite/FilterBySerial` (the `idx_enrollments_serial` index, present in all
three dialects since phase 4, finally has a query); `e2e.TestE2E_AdminCLI` (E2E-024).

## Rejected alternatives

- Coarse scope strings on the credential (`read`, `write`, `enqueue`, `secrets`): the first design
  of this record, replaced. A scope cannot say "enqueue inventory commands but not `EraseDevice`",
  which is the distinction that matters most in an API that can wipe hardware, and every scope added
  later would be a new constant threaded through the route table by hand.
- OPA/Rego, as Fleet embeds: a far larger dependency tree, and Rego is a general policy language
  where Cedar is a purpose-built authorization one with `default deny` and forbid-overrides-permit
  as language semantics rather than conventions a policy author must remember.
- Cedar's own schema validation (`x/exp/schema`): it would give richer write-time checking, but its
  own README says the directory "is not subject to the semantic version constraints of the rest of
  the module and breaking changes may be made at any time". Validating action ids against our route
  table gets the property that matters on stable API; revisit when it graduates.
- Compiling the policy into the binary, as Fleet does: it makes every new rule a release of this
  library. Policies are stored and operator-editable, with a default policy shipped so a fresh
  deployment works.
- Users, sessions, passwords, or SSO in the library: an identity system is a product concern, and
  0011 and 0027 already refused Vault, KMS and SAML on the same grounds. Cedar supplies the
  authorization vocabulary; who a caller *is* stays a token lookup, and `AdminAuthorizer` remains
  the seam for a deployment that authenticates differently.
- Keeping one static `DM_ADMIN_TOKEN`: it cannot be revoked without a restart, cannot be
  attributed in an audit line, and with this phase's routes it both erases fleets and exports
  FileVault escrow.
- Tokens defined in configuration rather than storage: simpler, but revocation means editing
  configuration and restarting the process, which is the operational failure this phase exists to
  fix. The configured single token remains, as the development opt-out and as the break-glass
  bootstrap credential described in amendment 1.
- A runtime "did you authorize?" tripwire (Fleet's `authzcheck`): a table that builds the mux cannot
  disagree with itself at runtime, and the test proves it before the binary ships. Fleet needs the
  tripwire because its authorization is a call inside each handler rather than data.
- `GET /commands/{uuid}` as a global lookup: the only index is `(enrollment_id, state, seq)`, so it
  would be a full scan. Deferred until an index justifies it.
- OpenAPI: `GET /routes` is generated from the table that builds the mux, so it cannot drift, and it
  costs no dependency. A real document is a phase 10 question once the routes are frozen.

## Amendments

### 1. The static token is break-glass, not superseded (2026-09-03, phase 9)

**What changed.** This record originally said `AdminStore` "takes precedence over `AdminToken`",
and the code implemented that literally: configuring a principal store made `DM_ADMIN_TOKEN` stop
being accepted. It is now accepted *alongside* a store. A request presenting it authenticates as
root and bypasses policy evaluation; any other bearer token is resolved against the principal store
and authorized by policy as before. The static token is checked first, in constant time, so it
still works when the store is empty or unreachable.

Two supporting changes come with it. Requests authenticated this way are audited under the fixed
actor `break-glass` rather than a principal name, and log a warning naming the method and path
whenever a principal store is also configured. `GET /config` reports whether the credential is
accepted, so `dmctl status` can say it out loud.

**Why.** The original rule made the feature unreachable. An empty principal store authenticates
nobody, and `POST /principals` -- the route that creates the first one -- is itself an authorized
admin route, so a deployment that enabled the store locked itself out. Two alternatives were
rejected. Minting a root principal at first start and printing its token puts a live root
credential into container start-up logs, where it is captured by every log shipper in the path. An
offline `dmctl bootstrap` writing straight to the database needs DSN access and bypasses the very
authorization path it is bootstrapping, so the first credential would be the one credential never
checked by the system that issues it. Reusing the token that already exists, is already documented,
and is already understood as the development opt-out adds no new mechanism; making its use visible
in the audit trail is what makes keeping it defensible.

**So what.** It is a standing root credential with no expiry that cannot be revoked without
restarting the process. While it is set, every least-privilege property claimed above is void for
whoever holds it, which makes leaving it configured the most likely way a deployment of this server
gets owned. The operator sequence is: set it for first start, create principals with `dmctl
principals create`, confirm `dmctl status` reports break-glass active, unset it, restart, and
confirm status reports it gone. **An audit record carrying the actor `break-glass` after that point
is an incident**, and it is a fixed string so that it can be alerted on. The runbook is in
`docs/operations/deployment.md`.

Verified by: `app.TestBreakGlassAlongsideThePrincipalStore` (bootstraps an empty store, is audited
under its own actor, bypasses policy while a stored principal is refused, and is reported by
`GET /config`), `app.TestAdminStoreOnTheProcessDatabase` (a principal from the server's own store
authenticates, which is what made the store reachable at all), and
`dmctl.TestStatusReportsBreakGlass`.

### 2. The principal store is opened by the server (2026-09-03, phase 9)

**What changed.** `adminauth/sqlstore` was imported only by its own tests: `cmd/dmserver` never
set `Config.AdminStore`, so every claim in this record was true only of a store an in-process
caller injected. `internal/app` now opens it on the process's own database, behind
`DM_ADMIN_STORE`, following the same three-way selection as the other satellite stores -- an
injected store wins, then the process database, then memory when there is none.

**Why.** Off by default, because turning it on mounts the admin API, and a flag that silently
exposes an administrative surface whenever someone picks a SQL backend is a security change wearing
the clothes of a convenience.

**So what.** Principals, Cedar policies and revocable tokens are now reachable from the shipped
binary rather than only from tests. The store shares the process's pool and dialect and keeps its
own migration set (`adminauth_schema_migrations`), so its schema version moves independently of
`storage`.
