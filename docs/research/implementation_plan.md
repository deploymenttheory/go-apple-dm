# go-apple-dm: phased implementation plan

Status: approved 2026-09-01. Companion to [reference_projects.md](reference_projects.md), which is the
source research this plan is founded on. Plan file of record for the build loop in section 8.

Source research: `docs/research/reference_projects.md` (2026-09-01). Every phase below names the
reference implementations to read first and the Apple pages and YAML files that are the source of
truth. The rule throughout: read the reference, record what it does and where it fails, state what
we do better, prove it with a test.

## Context

`go-apple-dm` is an empty template repository. The goal is a pure Go, enterprise-grade library
for the Apple MDM protocol (check-in, commands, push, enrollment) and Declarative Device
Management (DDM), with a thin reference server. No existing Go project offers an importable,
context-first, typed, multi-backend MDM plus DDM core: NanoMDM is minimal and untyped, KMFDDM is a
separate experimental service, Fleet's code is a product not a library, and no Go type library is
generated from Apple's schema for DDM status and protocol types. This plan builds that library
with a 95% coverage floor and a research-guided build loop.

## Decisions (confirmed with the user)

| Decision | Choice |
|---|---|
| Deliverable | Library first, plus thin reference server `cmd/mdmserver` and admin CLI `cmd/mdmctl` |
| Storage | Interface-first. Backends: in-memory, SQLite (modernc, pure Go), PostgreSQL (pgx), MySQL |
| Typed protocol types | **Generated in this repo** from the vendored `apple/device-management` YAML. No dependency on `deploymenttheory/go-sdk-appleservices` (user reversed the earlier choice: "move everything into this project directly") |
| Adjacent surfaces in scope | Own APNs push client; SCEP server on `smallstep/scep`; ACME `device-attest-01` CA; legacy DEP web service client and new ABM/ASM API client |
| Coverage | 95% overall and per non-exempt package, gated in CI |
| Module path | `github.com/deploymenttheory/go-apple-dm`, Go 1.26 |
| plist library | `github.com/micromdm/plist` for encode and decode (XML and binary, streaming, `Unmarshaler` hook for single-pass dispatch on `MessageType`/`RequestType`, used by NanoMDM, MicroMDM, NanoCMD, Fleet so fixtures interoperate), wrapped in our `plist/` package so a swap touches one place |
| PKCS7 | `github.com/smallstep/pkcs7` (the maintained fork every reference uses) |

Existing repo tooling to keep and extend: `.golangci.yml` (v2, gosec, wrapcheck, err113, gci
prefix `github.com/deploymenttheory`), `.github/workflows/go-lint.yml`, `release-please.yml`,
`dependabot.yml`, `pr-title-validation.yml` (super-linter was removed in phase 4; golangci-lint in `go-lint.yml` is the only linter), harden-runner with pinned SHAs.

---

## 1. Package layout

```
schema/                 GENERATED root. Only internal/schemagen writes here; no hand-written code inside.
schema/commands         65 command request structs + a response struct for every command, RequestType registry
schema/checkin          9 check-in message structs, MessageType registry
schema/errors           Enrollment error codes as typed constants + lookup
schema/profiles         127 configuration profile payload structs, PayloadType registry
schema/ddm              Declarations: configurations, activations, assets, management, credentials, DeclarationBase, registry
schema/ddmproto         DeclarationItems, TokensResponse / SynchronizationTokens, StatusReport envelope
schema/status           48 status item structs + dotted-path registry (".StatusItems.device.model.family" -> type)
schema/other            MachineInfo, ManifestURL, PasswordHash, SkipKeys, ESSO
schema/support          Runtime metadata: Lookup(family, path) and (*Entry).Check(Target) answering
                        availability, introduction, deprecation, removal, channel, supervision, DEP,
                        Shared iPad and User Enrollment; Families()/Paths() enumerate it (0036)
schema/PROVENANCE.json  Upstream repo, ref, commit, sha256 of YAML tree, fetch date, generator version
schema/NAMES.lock       Every exported identifier; regeneration may add, never silently remove (see section 2)
internal/schemagen/     The generator: YAML loader -> intermediate model -> Go, metadata, and conformance-test emitters
cmd/admgen/             Generator CLI: fetch --ref, generate, verify (deterministic re-gen diff + rename-guard)
plist/                  Thin wrapper over micromdm/plist: Marshal/Unmarshal, XML+binary DetectFormat, MaxDepth, MaxBytes,
                        Unmarshaler dispatch helpers, fuzz targets. The one place a library swap would touch.
mdm/                    Protocol core (hand-written): EnrollmentID, Channel, Request, CommandEnvelope, Response, Status,
                        ErrorChain, DecodeCheckin/DecodeResponse (dispatch via schema/checkin and schema/commands registries)
profile/                Hand-written: mobileconfig envelope, builder over schema/profiles, CMS sign/verify, parser
ddm/                    Engine: declarations, sets, membership resolvers, tokens, declaration-items, status persistence,
                        change notifier; Report walker over schema/status with json.RawMessage for unknown paths
ddm/adapter/inproc      service.DeclarativeManagement implemented over the engine
ddm/adapter/proxyserver ingress for check-ins forwarded by our mdm role (Apple check-in plist, our signature); no third-party wire scheme
ddm/adapter/proxyclient the inverse: our mdm role forwards check-ins to a remote ddm role
cms/                    PKCS7 sign/verify with clock-skew tolerance and chain policy (wraps smallstep/pkcs7)
service/                Service interfaces, Core implementation, typed errors, Hook chain
service/hooks/          Built-in hooks: audit, rate limit, metrics (phase 9). Certificate pinning is
                        deliberately not a hook: it ships as service.Config.Pinning on Core (0014)
event/                  Typed in-process event bus; sinks: webhook (MicroMDM-compatible), slog, OpenTelemetry (phase 9)
adminauth/              Admin authorization: Cedar policies over per-route actions, principals and
                        roles, and hashed, checksummed API tokens; adminauth/inmem,
                        adminauth/sqlstore (own migration set), adminauth/adminauthtest contract
                        suite (0034)
storage/                Interfaces split by concern, Page/Cursor, sentinel errors
storage/inmem           Always compiled; used by every unit test
storage/sqlite          modernc.org/sqlite, WAL
storage/postgres        pgx v5
storage/mysql           go-sql-driver/mysql
storage/sqlcommon       Shared statements + embedded per-dialect migrations (pressly/goose as a library)
storage/storagetest     Contract suites every backend must pass
httpapi/                net/http handlers: /checkin, /connect, enrollment profile, OTA profile-service,
                        /.well-known/com.apple.remotemanagement, DDM proxy; middlewares: cert extraction (header or mTLS),
                        Mdm-Signature verify, body limits, HMAC
push/                   Pusher interface, Target/Result, PushCertStore, Coalescer
push/apns               HTTP/2 APNs client, per-topic pools, 410/429 handling
push/pushtest           Fake Pusher and fake APNs server
enroll/                 Enrollment profile builder, OTA two-phase flow, MachineInfo, DEP profile JSON,
                        account-driven service discovery documents and auth flows, re-enrollment policy
ca/                     Signer, Depot, Policy interfaces; memory and storage-backed depots; Apple root bundle
scep/                   SCEP endpoint on smallstep/scep with challenge providers
acme/                   ACME server: directory, nonce, account, order, device-attest-01 challenge, finalize; one-time client identifiers; policy hooks
acme/jose/              JWS and JWK for RFC 8555, with the interop fix for Apple's short ECDSA signatures
acme/attest/            Managed Device Attestation: object and chain parsing, Apple's OIDs, freshness, key binding
acme/inmem/, acme/sqlstore/  ACME state, contract-tested; sqlstore has its own migration set
internal/cbor/          the strict CBOR subset an attestation object uses
dep/                    DEP web service client (own OAuth 1.0a, session singleflight, token PKI, typed errors),
                        syncer (fetch/sync cursor state machine), state-driven assigner, stores
                        (dep/inmem, dep/sqlstore on its own migration set), dep/deptest fake service (0026)
axm/                    ABM/ASM API client, batteries included: own ES256 assertion, every documented
                        endpoint, pagination, Retry-After, activities and convergence waits; axm/axmtest fake (0030)
gdmf/                   Apple software lookup service client (pmv) behind an interface, with a fake
enroll/ade              ADE enrollment: typed MachineInfo, CMS verification against the device CA,
                        software update gate, profile hook; enroll/adetest signs MachineInfo for tests (0027)
enroll/webauth          Own OIDC relying party for configuration_web_url (PKCE, nonce, state store);
                        enroll/webauth/webauthtest fake provider (0027)
enroll/discovery        /.well-known/com.apple.remotemanagement router (0028)
enroll/accountdriven    Account-driven enrollment: apple-as-web and apple-oauth2 flows, two-tier tokens (0028)
simulator/              Public device simulator: MDM v1 + DDM client, fault injection
secrets/                Provider interface (env, file, test); redacting String()
internal/clock, internal/canonjson (RFC 8785), internal/app (server wiring, testable); UUIDv7 from the standard `uuid` package (Go 1.27)
cmd/mdmserver           Reference server, wiring only (<100 lines)
cmd/mdmctl              Admin CLI, wiring only; the logic lives in internal/mdmctl (dispatch,
                        rendering, config), internal/mdmctl/adminclient, and internal/mdmctl/explain,
                        which are gated at 95% because cmd/ statements still count toward the overall
                        coverage figure even though the package is exempt per package (0035)
third_party/device-management   Git submodule pinned by commit; PROVENANCE.json records commit, sha256, date
third_party/refs/       Git-ignored read-only clones of reference repos (make refs)
docs/research/decisions/        ADR-lite decision records (one per feature)
docs/security/threat-model.md   STRIDE per endpoint
docs/testing/e2e-scenarios.md   Named scenarios mapped to Apple doc pages
```

## 2. The generator (internal/schemagen)

This is the foundation everything else builds on, so it is Phase 1.

- **Input**: `third_party/device-management` at a pinned commit (`release` branch; `seed_*` branches
  run in a non-blocking CI job). The YAML meta-schema is `docs/schema.yaml`; honour per-key
  `supportedOS` inheritance from the payload object, `presence`, `rangelist`, `range`, `format`,
  `repetition`, `subkeytype` (shared nested types emitted once), `combinetype`, shared iPad and
  user enrollment `mode`, `allowed-enrollments`, `allowed-scopes`, `accessrights`.
- **Intermediate model** (`internal/schemagen/model`): `Schema{Kind, Title, Payload{Keys}, Reasons, Supported}`,
  `Key{Name, Type, Subkeys, Required, Default, Range, Enum, Repetition, Pattern, Supported, Deprecated, Removed}`.
  Nothing from `supportedOS` is flattened into comments; it all lands in the model. Recursive
  `subkeys` are resolved in our loader over `gopkg.in/yaml.v3` node trees, so no go-yaml fork.
  A golden snapshot of the model lives in `internal/schemagen/testdata`.
- **Type mapping**: required keys are values, optional keys are pointers; `<data>` is `[]byte`,
  `<date>` is `time.Time`, `<integer>` is `int64`, `<real>` is `float64`, `<any>` only where Apple
  says so; nested dictionaries become named types, not anonymous structs; field order follows the
  YAML so diffs stay small. Tags: `plist:"Key,omitempty"` and `json:"Key,omitempty"`.
- **Outputs** (all `*.gen.go`, header `// Code generated by admgen; DO NOT EDIT.`, matching the
  existing golangci `generated: strict` exclusion; generated packages import only `plist/` and stdlib):
  - `schema/commands`: request struct per command with `RequestType()`, a response struct for every
    command (an empty struct plus the common envelope when Apple lists no response keys, so decoding
    is uniform), `Registry` mapping RequestType to request and response factories.
  - `schema/checkin`: one struct per check-in message with `MessageType()`, registry.
  - `schema/errors`, `schema/profiles` (`PayloadType()`), `schema/ddm` (`DeclarationType()`, four
    family registries, `AssetTypes` constraints), `schema/ddmproto`, `schema/status`
    (`StatusItemType()`, dotted-path registry), `schema/other`.
  - `Validate(v support.Version) error` on every type: required, enum, range, repetition, pattern,
    nested; errors collected into `[]*schema.ValidationError{Path, Key, Rule}`, not first-fail;
    passing a target OS version also flags keys unsupported on that version, channel, or
    supervision state. No reference does version-aware validation.
  - `schema/support`: `Register(family, table)` and `Lookup(family, path) *Entry` over a compact
    generated table, with `(*Entry).Check(Target) Result` answering availability, introduction,
    deprecation, removal, channel, supervision, DEP, user approval, Shared iPad and User Enrollment
    for a given OS and version, and `Families()`/`Paths(family)` enumerating it. Used by validation,
    the simulator (a fake iOS 17 device rejects keys it would not accept), and `mdmctl explain`.
    (This paragraph described `Supports(...)` and `Removed(...)` until phase 8; those signatures
    were never built and the shipped API is the one above. Corrected by record 0036.)
  - Generated conformance tests per package (see section 6).
- **Naming contract**: Go identifier = Apple key with dots and hyphens removed, segments
  title-cased, Apple's capitalisation preserved (`UDID`, `OSUpdate`), reserved words suffixed.
  Rules live in `internal/schemagen/naming.go` with a table test. `schema/NAMES.lock` lists every
  exported identifier; `admgen verify` fails when one disappears without an entry in
  `schema/RENAMES.md`. Regeneration can add, never silently rename. This is the lesson from the
  SDK survey, where weekly regeneration could rename fields on v0.x.
- **Determinism gate**: `admgen verify` regenerates into a temp dir and diffs; CI runs it.
- **Lessons applied** from `jessepeterson/admgen` (commands only, no responses), `korylprince/go-adm`
  (broadest coverage but needs a go-yaml fork for recursive YAML: we parse with `gopkg.in/yaml.v3`
  into a node tree and resolve `subkeytype` references ourselves), and go-sdk-appleservices
  (emit-only, no decoding, no status, pseudo-UUIDs): we emit both directions, all families, and
  real UUID v7 for CommandUUID.

## 3. Core domain model (mdm, service, storage)

```go
// mdm
type Channel uint8 // ChannelDevice, ChannelUser, ChannelSharedIPadUser, ChannelUserEnrollment
type EnrollmentID struct{ Channel Channel; ID string; ParentID string }
func ParseEnrollment(e Enrollment) (EnrollmentID, error) // normalises UDID/UserID/EnrollmentID/EnrollmentUserID; rejects invalid combos

type Request struct {
    Enrollment  EnrollmentID
    Certificate *x509.Certificate
    Params      map[string]string
    Peer        PeerInfo
    ReceivedAt  time.Time
}
type CheckinMessage interface{ Enrollment() Enrollment; Kind() CheckinKind; Raw() []byte }
// Authenticate, TokenUpdate, CheckOut, UserAuthenticate, SetBootstrapToken, GetBootstrapToken, GetToken, DeclarativeManagement
func DecodeCheckin(raw []byte, opts ...DecodeOption) (CheckinMessage, error) // single pass via plist.Unmarshaler, size+depth limits

type CommandEnvelope struct{ CommandUUID, RequestType string; Raw []byte }
type Status string // Acknowledged, Error, CommandFormatError, Idle, NotNow
type ErrorChain struct{ ErrorCode int; ErrorDomain, LocalizedDescription, USEnglishDescription string }
type Response struct{ Enrollment Enrollment; CommandUUID string; Status Status; ErrorChain []ErrorChain; Raw []byte; Payload any }
func DecodeResponse(raw []byte, reg *commands.Registry) (*Response, error) // typed Payload when RequestType known
func NewCommand(p commands.Payload, o ...CommandOption) (*CommandEnvelope, error) // RFC 9562 UUIDv7 by default
```

```go
// service (context-first; NanoMDM hides ctx inside Request)
type Checkin interface {
    Authenticate(ctx context.Context, r *mdm.Request, m *mdm.Authenticate) error
    TokenUpdate(ctx context.Context, r *mdm.Request, m *mdm.TokenUpdate) error
    CheckOut(ctx context.Context, r *mdm.Request, m *mdm.CheckOut) error
    UserAuthenticate(ctx context.Context, r *mdm.Request, m *mdm.UserAuthenticate) (*mdm.UserAuthenticateResponse, error)
    SetBootstrapToken(ctx context.Context, r *mdm.Request, m *mdm.SetBootstrapToken) error
    GetBootstrapToken(ctx context.Context, r *mdm.Request, m *mdm.GetBootstrapToken) (*mdm.BootstrapToken, error)
    GetToken(ctx context.Context, r *mdm.Request, m *mdm.GetToken) (*mdm.GetTokenResponse, error)
    DeclarativeManagement(ctx context.Context, r *mdm.Request, m *mdm.DeclarativeManagement) (ddm.Response, error) // typed, not []byte
}
type Connect interface{ Connect(ctx context.Context, r *mdm.Request, resp *mdm.Response) (*mdm.CommandEnvelope, error) }
type Enqueuer interface{ Enqueue(ctx context.Context, ids []mdm.EnrollmentID, cmd *mdm.CommandEnvelope, opts ...EnqueueOption) (EnqueueResult, error) }
type Hook interface{ Before(ctx context.Context, c *Call) (context.Context, error); After(ctx context.Context, c *Call, err error) }
type Error struct{ Code ErrorCode; Retryable bool; Cause error } // typed errors instead of string errors
```

Every state change publishes a typed event (`Enrolled`, `Reenrolled`, `TokenUpdated`, `CheckedOut`,
`CommandQueued`, `CommandResult`, `PushTokenInvalid`, `CertRotated`, `DDMChanged`,
`DDMStatusReceived`). The MicroMDM-style webhook, audit log, metrics, and DDM reconcilers are all
subscribers. This replaces NanoMDM's single webhook and hand-rolled `multi` service.

```go
// storage, split by concern
type EnrollmentStore interface {
    UpsertAuthenticate(ctx, r, m) error      // transactional: clears bootstrap/unlock tokens, cert pin, DDM status, pending queue (NanoMDM #71)
    StoreTokenUpdate(ctx, r, m) error
    Disable(ctx, r) error
    TouchLastSeen(ctx, id mdm.EnrollmentID, at time.Time) error          // KMFDDM #5
    List(ctx, q EnrollmentQuery, p Page) (Result[Enrollment], error)     // cursor pagination (KMFDDM #6)
}
type CommandQueue interface {
    Enqueue(ctx, ids []mdm.EnrollmentID, cmd *mdm.CommandEnvelope, o EnqueueOptions) (map[mdm.EnrollmentID]error, error)
    Next(ctx, r *mdm.Request, skipNotNow bool) (*mdm.CommandEnvelope, error)
    StoreResult(ctx, r *mdm.Request, resp *mdm.Response) error
    Clear(ctx, id mdm.EnrollmentID, f ClearFilter) (int64, error)      // one indexed statement, batched (NanoMDM #260)
    NotNowBackoff(ctx, id mdm.EnrollmentID, cmdUUID string) (time.Duration, error)
}
// PushStore, PushCertStore (expiry + stale token), CertAuthStore (pin, rotate, history),
// BootstrapTokenStore, MigrationStore (exports enrollments + bootstrap/unlock tokens, NanoMDM #86), DEPTokenStore
```

SQL: `commands(enrollment_id, command_uuid, status, not_now_until, created_at)` indexed on
`(enrollment_id, status, created_at)`, results in a separate table, `Clear` in batches of 1,000.

## 4. DDM engine

```go
type Declaration struct{ Type, Identifier string; Payload json.RawMessage; ServerToken string; UpdatedAt time.Time }
func Canonical(d *Declaration) ([]byte, error) // RFC 8785, ServerToken excluded
func TokenFor(d *Declaration) string           // hex(sha256(canonical)); key reordering never triggers a resync
type MembershipResolver interface{ Declarations(ctx, id mdm.EnrollmentID) ([]DeclarationRef, error) } // static sets + pluggable dynamic membership
type Engine struct{ store Store; resolvers []MembershipResolver; notifier Notifier; clock clock.Clock }
func (e *Engine) Tokens(ctx, id) (*ddmproto.TokensResponse, error)           // DeclarationsToken = sha256 over sorted (type, identifier, token)
func (e *Engine) DeclarationItems(ctx, id) (*ddmproto.DeclarationItems, error)
func (e *Engine) Declaration(ctx, id, typ, identifier string) (json.RawMessage, error) // per-enrollment variable expansion hook
func (e *Engine) Status(ctx, id, raw []byte) error                            // typed parse via schema/status registry; unknown paths kept as json.RawMessage
```

- Notifier writes `ddm_changes(enrollment_id, seq)`; a worker coalesces per enrollment, enqueues
  one `DeclarativeManagement` command only if none is pending (dedupe key on the queue), pushes in
  batches (KMFDDM #11). Status webhook is an event subscriber (KMFDDM #2).
- `ClearEnrollment` runs on CheckOut and re-enroll (KMFDDM #41). Status subscription values are
  retained with history rather than dropped (Fleet drops them).
- Activation predicates stored verbatim, validated syntactically; the simulator evaluates a
  documented NSPredicate subset so end-to-end tests exercise them (Fleet sends one unconditional
  activation).
- `adapter/proxyserver` is tested in CI against the real NanoMDM container so the `-dm` contract
  holds. Better than KMFDDM: SQLite and PostgreSQL (KMFDDM #96), transactional set edits, dynamic
  membership, pagination, last-seen, typed status. Better than Fleet: importable, event-driven
  instead of cron reconcile, user-channel declarations.

## 5. Enrollment, push, CA, DEP

- **Enrollment**: `enroll.Profile{Topic, ServerURL, CheckInURL, Identity: SCEP|ACME|PKCS12, AccessRights, ServerCapabilities, ManagedAppleID, Anchors}`
  built from generated `profiles.MDM`, `profiles.SCEP`, `profiles.ACMECertificate`,
  `profiles.CertificateRoot` in `schema/profiles`; signed with `cms.Sign`. OTA `profile-service` two-phase flow verifies
  phase 1 against the Apple iPhone Device CA. `Mdm-Signature` middleware: `cms.VerifyOptions{ClockSkew: 5 * time.Minute}`
  (NanoMDM #73), chain to enrollment CA, pin cert hash per enrollment, rotation allowed only on
  `Authenticate` for the same UDID with a valid chain, `CertRotated` event. Service discovery at
  `/.well-known/com.apple.remotemanagement` routes per user type; account-driven simple and OAuth 2
  flows. `MachineInfo` typed from `other/machineinfo.yaml`. Re-enrollment policy is a Hook.
- **Push**: `Pusher` interface, `push/apns` on `golang.org/x/net/http2` with a pool per topic and
  `tls.Config` from `PushCertStore`, reload on stale token or expiry. 410 marks token invalid and
  publishes `PushTokenInvalid`; 429/503 jittered exponential backoff; `Coalescer` collapses pushes
  per enrollment within a window. `pushtest.Fake` scripts 410/429. Push CSR vendor signing helper
  modelled on `micromdm/mdmutil`.
- **CA / SCEP / ACME**: `ca.Signer`, `ca.Depot`, `ca.Policy{Validity, KeyUsage, SubjectTemplate, AllowedSANs}`.
  `scep` wraps `smallstep/scep` with challenge providers (static, one-time bound to UDID,
  HMAC-derived). `acme` implements directory, nonce, account, order, authz, challenge, finalize with
  `device-attest-01`: parse the WebAuthn-format attestation, verify chain to the embedded Apple
  Enterprise Attestation Root, extract serial and UDID OIDs, freshness check, then
  `PolicyHook(ctx, Attestation) error` for serial, UDID, or ABM-ownership allowlists. Handler-only
  like nanoca, with `NonceStore` and persisted attestations.
- **DEP / ABM**: `dep.Client` with OAuth 1.0a session, refresh on 401 and `EXPIRED_TOKEN`, persisted
  cursor, `EXPIRED_CURSOR` triggers full re-fetch; `Fetch/Sync/Details/Disown`,
  `DefineProfile/AssignProfile/RemoveProfile`, account-driven enrollment profile assignment.
  `axm.Client` for the OAuth 2 ABM/ASM API (ES256 JWT, pagination) behind interfaces with a fake.
  Tokens encrypted via `secrets.Provider`.

## 6. Test strategy and the 95% gate

Layers, each with a Makefile target and CI job:

1. **Unit** (`make test`): table-driven, golden fixtures in `internal/testdata/{checkin,commands,ddm,enroll,cms}`
   captured from reference servers with identifiers scrubbed; `-update` regenerates. Every exported
   function has at least one failing-path test.
2. **Storage contract suite** (`make test-storage`): `storagetest.Run*Suite(t, newStore)` per
   interface; inmem and sqlite always run; postgres and mysql under `//go:build integration` with
   `TEST_POSTGRES_DSN` / `TEST_MYSQL_DSN` (testcontainers-go locally, service containers in CI).
   Covers concurrency, idempotent TokenUpdate, migrations up and down, pagination edges, channel isolation.
3. **HTTP handler tests**: `httptest` around the real mux with fakes; bad signature, unknown
   enrollment, wrong content type, oversized body, NotNow, user-channel headers, HMAC mismatch.
4. **Schema conformance** (`make test-conformance`): generated per command, declaration, status
   item, and check-in message: build with every documented key, marshal, unmarshal, diff; assert
   required keys and OS ranges; any YAML file without a generated type fails with its path.
5. **Fuzz** (`make fuzz-smoke` 20s per target on PR, `make fuzz` nightly 10 minutes):
   `FuzzCheckinDecode`, `FuzzResponseDecode`, `FuzzDeclarationDecode`, `FuzzStatusReportDecode`,
   `FuzzCMSVerify`, `FuzzHMACProxy`, `FuzzProfileParse`, `FuzzSCEPPKIMessage`, `FuzzACMEAttestation`.
6. **Simulator and end-to-end** (`make test-e2e`): `simulator` covers SCEP or ACME enrollment,
   Authenticate, TokenUpdate (device and user, token rotation), CheckOut, GetToken, bootstrap
   tokens, Idle loop, typed responses, NotNow with retry, Error with ErrorChain, re-enrollment,
   Shared iPad users; DDM tokens, declaration-items diff by ServerToken, per-declaration fetch,
   predicate evaluation, status reports and subscriptions, user-channel declarations; fault
   injection (dropped connections, slow responses, malformed plist, stale tokens, duplicate
   CommandUUID). Scenarios documented in `docs/testing/e2e-scenarios.md` mapped to Apple pages.
7. **Property tests** (`pgregory.net/rapid`): plist round-trip, ServerToken determinism, queue
   ordering, enrollment state machine never reaches an invalid state.

**Gate** (`scripts/coverage-gate.sh`, `COVERAGE_MIN=95`): unit with `-race -coverpkg=./...`,
storage integration, e2e profiles merged with `go tool covdata`; fail if overall or any non-exempt
package is under 95%; print the ten least-covered functions; upload HTML. Exemptions in
`scripts/coverage-exempt.txt` with reasons: `cmd/...` (wiring only; note the exemption suppresses
the per-package line but not the overall figure, since `-coverpkg=./...` still counts those
statements, so a fat `main` fails the gate anyway), `*.gen.go`,
`storage/storagetest`, `push/pushtest`, `simulator` (measured, not gated). Injectable fakes for
every I/O boundary: `clock.Clock`, `push.Transport`, `ca.Signer`, per-method error-injecting
storage wrapper, recorded round-trippers for DEP and ABM.

## 7. CI

| Workflow | Job | What |
|---|---|---|
| `go-lint.yml` (exists) | `lint` | golangci-lint v2, current config, `only-new-issues` off once code exists |
| `go-test.yml` | `unit` | `gotestsum -- -race -shuffle=on ./...`, ubuntu and macos |
| `go-test.yml` | `generate-check` | `go generate ./...` then `git diff --exit-code`; rename-guard test |
| `go-test.yml` | `storage-integration` | services `postgres:17`, `mysql:8.4`; `-tags integration ./storage/...` |
| `go-test.yml` | `e2e` | reference server plus simulator on SQLite and PostgreSQL; our own `ddm`-role container for the split-deployment hop (record 0025 superseded the NanoMDM interop container: both sides of the wire are ours) |
| `go-test.yml` | `coverage-gate` | needs the above; merges profiles; runs the gate |
| `go-test.yml` | `fuzz-smoke` | `make fuzz-smoke` |
| `security.yml` | `govulncheck`, `gosec-sarif` | vulnerability scan; SARIF to code scanning |
| `schema-drift.yml` (nightly) | `schema-drift` | compares submodule commit to upstream `release` and `seed_*`; regenerates; runs conformance; opens or updates an issue with the diff |
| `release-please.yml` (exists) | unchanged | add `release-please-config.json` with `release-type: go` and manifest |

`make ci` runs everything locally so green local predicts green CI.

## 8. Research-guided build loop (per feature)

No code until step 3 is committed.

1. **Locate the source of truth**: Apple doc URL and YAML path(s) in `third_party/device-management`.
2. **Read at least two references** from `reference_projects.md` (defaults: NanoMDM + Fleet for
   MDM v1; KMFDDM + Fleet + Zentral for DDM; NanoDEP + Zentral `dep.py` for DEP; nanoca + step-ca
   for ACME). Clone read-only into `third_party/refs/` via `make refs`. Mine pitfalls:
   ```bash
   gh issue list -R micromdm/nanomdm --search "<topic>" --state all --limit 50
   gh issue list -R fleetdm/fleet --label "#g-mdm" --search "<topic>" --state all --limit 100
   gh api "repos/jessepeterson/kmfddm/commits?path=<file>"
   git -C third_party/refs/nanomdm log --oneline -- <file>
   ```
3. **Write the decision record** `docs/research/decisions/NNNN-<feature>.md`:
   ```markdown
   # NNNN: <feature>
   Status: proposed | accepted | superseded
   Apple sources: <doc URL>, <yaml paths>
   References read: <repo@commit path> ...
   Known pitfalls found: <issue links, one line each>
   What they do: <bullets per reference>
   What we do better: <numbered claims>
   Verified by: <test name per claim>
   Rejected alternatives: <one line each>
   ```
   Each "better" claim names a test that would fail on the reference behaviour.
4. **Implement**, tests first for the pitfalls found.
5. **PR checklist** (`.github/PULL_REQUEST_TEMPLATE.md`): decision record linked; Apple doc and
   YAML cited in the package doc comment; conformance green; coverage gate green; failing-path
   test per exported function; fuzz target added if the change parses untrusted input.
6. **Keep research current**: `reference_projects.md` gains a dated changelog; nightly
   `scripts/refs-activity.sh` lists reference repos pushed in the last 30 days for monthly review.

## 9. Phases

Order gives a working MDM v1 early, DDM next, then enrollment breadth, ACME, the admin surface,
operations, and hardening. Phase 8 was one row covering the reference server and ops until
2026-09-02; it was split into phase 8 (admin API, authorization, `mdmctl`) and phase 9
(observability and operations), moving hardening and v1 to phase 10.
Sizes are relative (S/M/L).

| # | Goal | Delivers | Read first | Apple sources | Better-than targets | Exit criteria | Size |
|---|---|---|---|---|---|---|---|
| 0 | Scaffold | `go.mod`, layout, Makefile, CI jobs, coverage gate, submodule + PROVENANCE, ADR-0001 architecture, ADR-0002 plist, ADR-0003 generator, threat model, PR template, `release-please-config.json` | NanoMDM README and operations guide; Fleet `tools/mdm/apple/glossary-and-protocols.md` | `reference_projects.md` section 1 | n/a | `make ci` green on empty tree | S |
| 1 | Schema and generator | `internal/schemagen`, `cmd/admgen`, `plist/`, all `schema/*` packages (commands, checkin, errors, profiles, ddm, ddmproto, status, other, support), `NAMES.lock`, generated conformance tests | `jessepeterson/admgen`, `korylprince/go-adm` (`yamlschema`, `declgen`), `apple/device-management/docs/schema.md` | Commands and queries, Declarations, Status items pages; `docs/schema.yaml`; all YAML dirs | Both request and response for all 65 commands; all four DDM families plus status and protocol; runtime OS/channel metadata; naming stability guard | Every YAML file has a generated type; determinism gate; 95% on generator | L |
| 2 | Protocol core | `mdm`, `cms`, `storage` + `inmem` + `storagetest`, `service`, `event`, `httpapi` check-in and connect, `simulator` MDM v1 | nanomdm `mdm/*.go`, `service/nanomdm/service.go`, `storage/*.go`, `http/mdm/*.go`; Fleet `pkg/mdm/mdmtest/apple.go` | Check-in page; `mdm/checkin/*.yaml` | ctx-first typed service; typed responses; transactional re-enroll (#71); signing-time tolerance (#73); indexed queue and batched Clear (#260); event bus | e2e: pre-issued identity enrols, TokenUpdate, Idle, three commands, NotNow, Error; 95% | M |
| 3 | Push, SCEP, enrollment | `push`, `push/apns`, `pushtest`, `ca`, `scep`, `enroll` (profile builder, OTA), `profile` | nanomdm `push/nanopush`, `mdm/push.go`, `storage/pushcert.go`; micromdm/scep `server`, `depot`; micromdm `platform/profile`; mdmutil `mdmcsr` | Essentials: push notifications, certificates; `mdm/profiles/mdm.yaml`, `scep.yaml` | 410/429 handling with events; coalescing; cert expiry reload; one-time SCEP challenges | e2e: SCEP enrol, push, command; fake APNs 410 path | M |
| 4 | SQL backends | `sqlite`, `postgres`, `mysql`, `sqlcommon` migrations, contract suite in CI. Delivered in this phase rather than later ones (records 0012 to 0017): secrets at rest (`storage/crypt`), certificate association history and reuse policy, push certificate store, UserAuthenticate state, enrollment export and import | nanomdm `storage/mysql`, `storage/pgsql`; kmfddm `storage/mysql` | n/a | Same contract on four backends; `Clear` on 100k rows under 1s on Postgres; pagination | Suites green on all backends; coverage gate | M |
| 5 | DDM engine | `ddm`, `ddm/adapter/*`, notifier, simulator DDM client | kmfddm `ddm/*`, `storage/*`, `http/api`; Fleet `server/service/apple_mdm.go` DDM handlers and `docs/Contributing/architecture/mdm/apple-declarative-device-management.md`; Zentral `zentral/contrib/mdm/declarations/protocol.py` | Declarations, Status items, DeclarativeManagementRequest, Integrating DDM; `declarative/**` | Canonical tokens (RFC 8785); dynamic membership; per-enrollment snapshots; status values retained; synthesised status subscriptions; unenroll cleanup (#41); coalesced notifier (#11); Apple's check-in forwarded verbatim between our own roles (no NanoMDM code); predicates validated at upload; delivered with records 0019 to 0025, `internal/app`, `cmd/mdmserver`, and the Dockerfile in minimal form | e2e: change, push, tokens, items, fetch, status verified; predicate scenario; split-deployment interop: our own image in the ddm role driven by our proxyclient (E2E-010) | L |
| 6 | Enrollment breadth | user and Shared iPad channels (0029), account-driven discovery and both auth flows (0028), `dep` with syncer, assigner, stores, and fake (0026), ADE MachineInfo, web view OIDC auth, software update gate (0027), `axm` batteries included with fake (0030), `gdmf` | nanodep `godep`, `client`; Zentral `dep.py`, `public_views/user.py`; Fleet `server/mdm/apple/apple_bm.go`; `vbnin/Apple-JSON-discovery-server`; `korylprince/dep-webview-oidc` | Device enrollment, Device assignment, account-driven pages; `other/machineinfo.yaml` | Cursor expiry handling; per-user-type discovery; typed MachineInfo | e2e: DEP profile assign and ADE enrol (fake DEP), ADE web view auth (fake IdP), account-driven discovery and both flows, user channel commands, Shared iPad, ABM assignment through the fake AxM (E2E-011 to E2E-013, E2E-018 to E2E-021) | L |
| 7 | ACME and attestation | `acme` server with `device-attest-01`, `acme/jose`, `acme/attest` and `attesttest`, `internal/cbor`, `acme/inmem` and `acme/sqlstore`, `ca` otherName SANs, ACME payload in `enroll`, simulator client, reference server wiring (0031-0033) | `brandonweeks/nanoca` handlers and verifier; step-ca `acme/challenge.go`, `acme/order.go`; `hslatman/ios-acme-simulator`; Fleet `server/mdm/acme` | Identity management: Validating a Managed Device Attestation; `mdm/profiles/com.apple.security.acme.yaml`, `declarative/declarations/assets/credential.acme.yaml` and `assets/credentials/acme.yaml`, `mdm/commands/information.device.yaml` | One-time client identifiers bound to a device; attested key bound to the CSR; freshness required, not optional; all ten OIDs parsed by their documented type; policy hooks; nonce expiry and body limits | e2e: ACME enrol with simulated attestation, rejected chain, wrong key, replayed identifier (E2E-014); declarative ACME identity (E2E-022); DeviceInformation attestation (E2E-023) | M |
| 8 | Admin API, authorization, and `mdmctl` | Cedar policies over per-route actions with an `AdminAuthorizer` seam; persisted admin principals with hashed, checksummed tokens on their own migration set (`adminauth`); the extended admin API (enrollment inventory, command enqueue and queue reads, push certificates, export and import, DDM and DEP parity, principals) mounted per role; `cmd/mdmctl` over it with an offline `explain <RequestType\|declaration>` backed by `schema/support`; APNs push wired into `internal/app`; `http.Server` hardening and a shutdown that waits for workers (records 0034 to 0036) | Fleet `server/authz` (Rego policy, `authzcheck`, `AuthorizeOrNotFound`); Zentral `server/pbac`, `utils/token.py`, `accounts/models.py`; step-ca `authority/authorize.go`, `authority/admin/api`; `macadmins/nanohubctl`; micromdm `cmd/mdmctl`; nanomdm and kmfddm `http/api` | Commands and queries; Check-in; Device assignment | Per-request least privilege (a policy can let a CI credential enqueue an inventory command but not an erase) where every reference has one shared secret; tokens hashed and revocable where Fleet stores them in plaintext and never expires them; actions declared as route data and proven by a test, where step-ca matches a URL prefix; a policy naming an action nobody serves refused at write time, where Cedar alone would accept it and never grant; authorization *denials* audited, which neither Fleet nor Zentral does | `mdmctl` drives every admin route; a read-only principal is refused and the refusal is audited; a rotated token is rejected; the reference server wakes a device; 95% | L |
| 9 | Observability and operations | Apple's closed vocabularies emitted as Go constants so a label set is bounded by the schema rather than by hand (0041), OpenTelemetry metrics with bounded cardinality, the event sinks `event/doc.go` promises (slog audit, MicroMDM-compatible webhook), `service/hooks` (metrics, audit, rate limit), liveness and readiness split from `/healthz`, the compose lab, the deployment guide | Fleet health, metrics and activity feed; micromdm `workflow/webhook`; nanomdm `service/webhook`; nanomdm and kmfddm operations guides | Deployment guide | Readiness semantics rather than a bound port; no device-supplied string ever becomes a metric label | E2E-015 readiness fails and recovers; the lab enrols a real device via SCEP and receives a DDM declaration | M |
| 10 | Hardening and v1 | Fuzz corpus, 10k-device load test, OS 27 seed schema conformance, security review, API freeze, `v1.0.0` | Reference issue tracker sweep | `seed_OS_27_0` branch; WWDC26 updates page | No regressions across schema versions | All DoD items; two minor releases with a frozen API | M |

**Definition of done per phase**: all phase packages at 95% or exempt; conformance generated for
every YAML the phase claims; e2e scenarios pass on SQLite and PostgreSQL; a decision record per
feature; package docs cite Apple sources; `make ci` green; conventional-commit CHANGELOG entry.

**Versioning**: `v0.x` until phase 10. Cut `v1.0.0` when MDM v1, DDM, SCEP, APNs, and four
backends are done, an external consumer has run the simulator suite, the threat model review is
closed, and the public API has been frozen for two minor releases.

## 10. Security practices

- CMS verification of `Mdm-Signature` with chain to the enrollment CA, detached-signature
  mismatch rejection, configurable expiry policy, identity pinning with audited rotation.
- Enrollment auth by mTLS or signature, never UDID alone.
- DDM proxy HMAC: SHA-256 over body plus timestamp, `subtle.ConstantTimeCompare`, replay window
  with nonce store.
- Secrets behind `secrets.Provider`; never logged; redacting `String()`; gosec G101.
- Untrusted input: body size limits per endpoint, plist depth limit, every decoder fuzzed, no
  reflection-based decoding into arbitrary types.
- `docs/security/threat-model.md` written in phase 0, updated per phase; `SECURITY.md` filled in.
- Submodule and reference clones pinned by commit; `govulncheck` in CI; dependabot already set.

## 11. Risks and mitigations

- **Apple schema drift**: submodule pinned; nightly drift job; rename-guard; seed branch
  conformance before each Apple release.
- **OS 27 removes legacy software update commands**: DDM software update declarations ship in
  phase 5; legacy commands carry runtime deprecation warnings from the metadata API.
- **Binary plist edge cases**: fuzz decoders; fixtures from real devices; size and depth limits.
- **User channel and Shared iPad**: channels modelled in `EnrollmentID` from phase 2; simulator
  covers them in phase 6; contract suites assert channel isolation.
- **Coverage cost of network code**: every I/O boundary behind an interface with a fake;
  exemptions limited to `cmd/` and generated code.
- **Generator scope creep**: phase 1 is the largest early phase; keep the meta-schema parser
  strict and fail on unknown YAML keys so Apple additions surface as explicit work.
- **NanoMDM users' interop expectations**: proxyserver adapter tested against the real NanoMDM
  container in CI.

## 12. Verification of this plan's execution

- Phase 0 ends with `make ci` green and the coverage gate script proven against a trivial package.
- Phase 1 ends with `go generate ./... && git diff --exit-code` clean and a conformance test per YAML file.
- From phase 2 on, every phase ends with named e2e scenarios in `docs/testing/e2e-scenarios.md`
  passing via `make test-e2e`, storage suites via `make test-storage`, and the gate via
  `scripts/coverage-gate.sh` reporting at least 95%.
- Phase 8 ends with `mdmctl` driving every admin route, a read-only principal refused and the
  refusal audited, a rotated token rejected, and the reference server waking a device.
- Phase 9 ends with a real device enrolling in the compose lab and receiving a DDM declaration.
