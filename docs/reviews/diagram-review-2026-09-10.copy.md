# Exact replacement copy for the existing diagrams

Baseline: `ff85958196cb013e8e14c1055f067319b07e3299`. These are proposed field edits, not applied source changes. Use [the assessment](diagram-review-2026-09-10.md) for topology decisions. Element removal overrides its old copy entry. Tables include all changed leaf values, including supporting cards, guided-view notes, reference cards, and revision pins. Geometry is not approved by this copy specification.

### system-architecture

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | One /mdm URL serves check-in and connect, forked on Content-Type | An enrollment profile points the device to /mdm for check-in and command requests. SCEP or ACME issues the device identity certificate. |
| `cards[0].items[1]` | DDM has no device URL of its own: it arrives as a check-in message | This server carries declarative management requests inside MDM check-in messages. APNs prompts a connection; it does not deliver MDM commands. |
| `cards[0].items[2]` | Identity comes from SCEP or ACME with Managed Device Attestation | [remove field] |
| `cards[0].title` | Device protocol | How devices communicate |
| `cards[1].items[0]` | Roles mdm, ddm, or all from one binary | The shared runtime serves HTTP or native TLS and supervises background workers. Roles select the mounted services. |
| `cards[1].items[1]` | service.Core never imports ddm; the engine plugs in as a DMHandler | The admin API manages commands, declarations, enrollment profiles, controlled replacement, and separate app push credentials. |
| `cards[1].items[2]` | ddmsync.Notifier enqueues commands and pushes; the engine writes change rows | [remove field] |
| `cards[1].title` | Server | What the server manages |
| `cards[2].dot` | rose | amber |
| `cards[2].items[0]` | Stored principals use Cedar; DM_ADMIN_TOKEN bypasses policy | Follow the enrollment diagrams for discovery and trust, the command flow for device requests, and the companion diagrams for replacement, app notifications, and the test bench. |
| `cards[2].items[1]` | /admin/v1/dep and /admin/v1/axm drive the service clients | [remove field] |
| `cards[2].items[2]` | dep authenticates with OAuth 1.0a, axm with an ES256 client assertion, gdmf is unauthenticated | [remove field] |
| `cards[2].title` | Admin plane | Read next |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Device Management — https://developer.apple.com/documentation/devicemanagement |
| `cards[3].items[1]` | [new field] | Sending MDM commands to a device — https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device |
| `cards[3].items[2]` | [new field] | Integrating declarative management — https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management |
| `cards[3].items[3]` | [new field] | MDM payload — https://developer.apple.com/documentation/devicemanagement/mdm |
| `cards[3].title` | [new field] | Public documentation |
| `components[id=appleapis].label` | Apple services | Apple management services |
| `components[id=appleapis].sublabel` | DEP · Business Manager | Device assignment and business APIs |
| `components[id=store].label` | Storage | Persistent state |
| `components[id=store].sublabel` | storage.Store · feature stores | Enrollments, commands, and feature stores |
| `components[id=apns].label` | APNs | Apple Push Notification service |
| `components[id=apns].sublabel` | HTTP/2 | APNs provider API |
| `components[id=httpsurface].label` | Device HTTP surface | Device-facing endpoints |
| `components[id=httpsurface].sublabel` | /mdm · /scep · /acme | Enrollment, check-in, and commands |
| `components[id=push].label` | pushnotify.Notifier | MDM push notifier |
| `components[id=push].sublabel` | coalesced | Prompts devices to contact /mdm |
| `components[id=engine].label` | DDM engine | Declarative management |
| `components[id=engine].sublabel` | ddm.Engine · ddmsync | Desired state and device status |
| `connections[id=device-checkin].label` | check-in + connect | Check in and request commands |
| `connections[id=device-enroll].label` | enroll: SCEP / ACME | Obtain an enrollment identity |
| `connections[id=apns-device].label` | wake | Prompt a management connection |
| `connections[id=engine-push].label` | Notify | Request an MDM wake notification |
| `connections[id=core-engine].label` | DMHandler | Forward declarative check-ins |
| `meta.repository.revision` | a817270f13b4ef7a9b1d2895118981120546b3a2 | ff85958196cb013e8e14c1055f067319b07e3299 |
| `meta.views[id=admin-plane].label` | Admin plane | Administrative access |
| `meta.views[id=admin-plane].note` | Stored principals use Cedar policy; the bootstrap token bypasses policy. | Stored principals require Cedar authorization. The static root admin token bypasses it; it is not a macOS bootstrap token. |

Retain: `boundaries[0].label`, `boundaries[1].label`, `boundaries[2].label`, `components[id=devices].label`, `components[id=devices].sublabel`, `components[id=store].sources[0].label`, `components[id=dmctl].label`, `components[id=dmctl].sublabel`, `components[id=depaxm].label`, `components[id=depaxm].sublabel`, `components[id=httpsurface].sources[0].label`, `components[id=adminapi].label`, `components[id=adminapi].sources[0].label`, `components[id=adminapi].sublabel`, `components[id=adminapi].tag`, `components[id=core].label`, `components[id=core].sources[0].label`, `components[id=core].sublabel`, `components[id=engine].sources[0].label`, `components[id=engine].sources[1].label`, `connections[id=clients-apple].label`, `connections[id=cli-admin].label`, `connections[id=admin-engine].label`, `connections[id=admin-clients].label`, `connections[id=http-core].label`, `connections[id=core-store].label`, `connections[id=push-apns].label`, `connections[id=engine-store].label`, `meta.title`, `meta.views[id=device-protocol].label`, `meta.views[id=device-protocol].note`, `meta.views[id=wake-path].label`, `meta.views[id=wake-path].note`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Device Management](https://developer.apple.com/documentation/devicemanagement), [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device), [Integrating declarative management](https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management), [MDM payload](https://developer.apple.com/documentation/devicemanagement/mdm).

### package-layering

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | Every arrow means "imports": the tail depends on the head | Arrows show selected package dependencies, not network traffic. server/ is a separate Go module that imports the library. |
| `cards[0].items[1]` | A tier may import its own and every tier to its right, never to its left | The map groups composition code by purpose. The current tier test classifies server-prefixed packages in the server tier. |
| `cards[0].items[2]` | internal/layout reads the tier from the import path and fails the build on an upward edge | [remove field] |
| `cards[0].title` | Reading the arrows | How to read this map |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | server/ is a second Go module, so it can depend on the library and never the reverse | Tests reject unapproved upward imports and package-unit cycles. The explicit upward exception is mdmprotocol/enroll/ade importing appleplatformservices/gdmf. |
| `cards[1].items[1]` | The library module has no SQL driver dependency | Test scaffolding is excluded from the tier rule. pki/pushcert remains independent of the push client and imports only the standard library. |
| `cards[1].items[2]` | pki/pushcert uses only the standard library, keeping certificate parsing independent of push | [remove field] |
| `cards[1].title` | Module boundary | Enforced rules and exceptions |
| `cards[2].dot` | [new field] | slate |
| `cards[2].items[0]` | [new field] | Go Modules Reference — https://go.dev/ref/mod |
| `cards[2].items[1]` | [new field] | Apple device-management schema repository — https://github.com/apple/device-management |
| `cards[2].title` | [new field] | Public documentation |
| `components[id=apptier].label` | app | Composition and tooling |
| `components[id=apptier].sublabel` | cmd · internal/app · e2e | Server entrypoints, bench, and CLI |
| `components[id=servertier].label` | server/ | Server implementations |
| `components[id=servertier].sublabel` | service · httpapi · ddmsync | Services, transports, and SQL stores |
| `components[id=simtier].label` | simulator/ | Device simulator |
| `components[id=simtier].sublabel` | a device, in software | Exercises supported device exchanges |
| `components[id=storagetier].label` | storage/ | Storage contracts |
| `components[id=storagetier].sublabel` | contracts · in-memory | Interfaces and memory backends |
| `components[id=clienttier].label` | appleplatformservices/ | Apple service clients |
| `components[id=clienttier].sublabel` | dep · axm · gdmf · push | Enrollment assignment and push |
| `components[id=pkitier].label` | pki/ | Certificate services |
| `components[id=pkitier].sublabel` | ca · scep · acme · pushcert | Issue and validate identities |
| `components[id=protocoltier].label` | mdmprotocol/ | Device management protocols |
| `components[id=protocoltier].sublabel` | mdm · ddm · enroll · plist | Wire messages and protocol engines |
| `components[id=schematier].label` | schema/ | Generated Apple schemas |
| `components[id=schematier].sublabel` | generated from Apple | Typed payloads and validation |
| `components[id=foundationtier].label` | foundation | Shared utilities |
| `components[id=foundationtier].sublabel` | paging · clock · secrets | Clock, paging, secrets, and telemetry |
| `meta.repository.revision` | a817270f13b4ef7a9b1d2895118981120546b3a2 | ff85958196cb013e8e14c1055f067319b07e3299 |
| `meta.title` | Package Tiers and Import Direction | Package Dependencies and Module Boundaries |

Retain: `boundaries[0].label`, `boundaries[1].label`, `components[id=apptier].sources[0].label`, `components[id=apptier].tag`, `components[id=servertier].sources[0].label`, `components[id=servertier].sources[1].label`, `components[id=servertier].tag`, `components[id=simtier].sources[0].label`, `components[id=simtier].tag`, `components[id=storagetier].sources[0].label`, `components[id=storagetier].tag`, `components[id=clienttier].sources[0].label`, `components[id=clienttier].tag`, `components[id=pkitier].sources[0].label`, `components[id=pkitier].sources[1].label`, `components[id=pkitier].tag`, `components[id=protocoltier].sources[0].label`, `components[id=protocoltier].sources[1].label`, `components[id=protocoltier].tag`, `components[id=schematier].sources[0].label`, `components[id=schematier].tag`, `components[id=foundationtier].sources[0].label`, `components[id=foundationtier].tag`, `connections[0].label`, `connections[2].label`, `connections[4].label`, `connections[7].label`, `connections[8].label`, `meta.views[id=spine].label`, `meta.views[id=spine].note`, `meta.views[id=modules].label`, `meta.views[id=modules].note`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Go Modules Reference](https://go.dev/ref/mod), [Apple device-management schema repository](https://github.com/apple/device-management).

### storage-contract

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | One backend satisfies all eight; a caller depends only on the part it needs | storage.Store combines these eight contracts. Callers can depend on a smaller interface when they need only part of the behaviour. |
| `cards[0].items[1]` | The shared contract suite runs against each backend | Ordinary Authenticate upserts can reset tokens and non-terminal commands. Controlled profile replacement follows a different contract. |
| `cards[0].title` | Composed store interfaces | Eight composed interfaces |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | Stores expose sentinel contract errors and wrap database failures | ReplacementStore atomically tracks the candidate identity and promotes it only after successful check-in and command acknowledgement. |
| `cards[1].items[1]` | Re-enrollment clears push info, tokens, the pin and the pending queue in one transaction | The transition preserves the enrollment, user channels, escrow, and ordinary command queue. Shared tests exercise the memory and SQL implementations. |
| `cards[1].title` | Sentinels | Optional replacement contract |
| `cards[2].dot` | [new field] | amber |
| `cards[2].items[0]` | [new field] | Encryption depends on the backend and configured keyring; it is not promised by the interfaces alone. App push credentials live in a separate state store. |
| `cards[2].title` | [new field] | Storage guarantees |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Check-in — https://developer.apple.com/documentation/devicemanagement/check-in |
| `cards[3].items[1]` | [new field] | Set Bootstrap Token — https://developer.apple.com/documentation/devicemanagement/set-bootstrap-token |
| `cards[3].items[2]` | [new field] | Go cipher.NewGCM — https://pkg.go.dev/crypto/cipher#NewGCM |
| `cards[3].title` | [new field] | Public documentation |
| `components[id=authn].label` | Authenticate | Authenticate handler |
| `components[id=authn].sublabel` | first check-in | Creates or resets enrollment state |
| `components[id=pushcertstore].sublabel` | APNs cert and key | MDM push certificate and private key |
| `components[id=authorize].label` | Core.authorize | Identity authorization |
| `components[id=authorize].sublabel` | pin on every message | Checks the enrollment certificate |
| `components[id=pushcertmgr].label` | admin /pushcerts | MDM certificate administration |
| `components[id=pushcertmgr].sublabel` | upload or renew | Import or replace a topic credential |
| `components[id=setbst].label` | SetBootstrapToken | SetBootstrapToken handler |
| `components[id=setbst].sublabel` | escrow after FileVault | Stores the device’s bootstrap token |
| `components[id=bootstraptokenstore].sublabel` | sealed at rest | Store and retrieve escrowed tokens |
| `components[id=certauthstore].sublabel` | pin and history | Identity fingerprint and history |
| `components[id=pushstore].sublabel` | token · push magic | APNs token, topic, and PushMagic |
| `connections[id=s1].label` | upserts it | Create or reset enrollment |
| `connections[id=s3].label` | escrows | Store bootstrap token |
| `connections[id=s8].label` | seals the key | Validate and store certificate |
| `meta.repository.revision` | a817270f13b4ef7a9b1d2895118981120546b3a2 | ff85958196cb013e8e14c1055f067319b07e3299 |
| `meta.views[id=enrollment].label` | Enrollment | Enrollment identity |
| `meta.views[id=enrollment].note` | Authenticate upserts the record; every later message is checked against the pinned certificate. | Ordinary enrollment associates an identity. Later requests enforce configured pinning or the authorized replacement handshake. |
| `meta.views[id=secrets].label` | Escrow | Stored credentials |
| `meta.views[id=secrets].note` | Bootstrap tokens and digest state are sealed at rest. | Bootstrap and user-auth tokens are encrypted by configured SQL backends; encryption is not an interface guarantee. |
| `meta.views[id=migration].label` | Migration | Move enrollment records |
| `meta.views[id=migration].note` | Export and import move an enrollment between backends with its tokens intact. | Export and import transfer enrollment records and tokens. The ordinary command queue is not included. |

Retain: `boundaries[0].label`, `components[id=enqueue].label`, `components[id=enqueue].sublabel`, `components[id=userauthstore].label`, `components[id=userauthstore].sources[0].label`, `components[id=userauthstore].sublabel`, `components[id=pushcertstore].label`, `components[id=pushcertstore].sources[0].label`, `components[id=pushsvc].label`, `components[id=pushsvc].sublabel`, `components[id=userauth].label`, `components[id=userauth].sublabel`, `components[id=export].label`, `components[id=export].sublabel`, `components[id=enrollmentstore].label`, `components[id=enrollmentstore].sources[0].label`, `components[id=enrollmentstore].sublabel`, `components[id=commandqueue].label`, `components[id=commandqueue].sources[0].label`, `components[id=commandqueue].sublabel`, `components[id=bootstraptokenstore].label`, `components[id=bootstraptokenstore].sources[0].label`, `components[id=migrationstore].label`, `components[id=migrationstore].sources[0].label`, `components[id=migrationstore].sublabel`, `components[id=certauthstore].label`, `components[id=certauthstore].sources[0].label`, `components[id=pushstore].label`, `components[id=pushstore].sources[0].label`, `connections[id=s2].label`, `connections[id=s4].label`, `connections[id=s5].label`, `connections[id=s6].label`, `connections[id=s7].label`, `meta.title`, `meta.views[id=delivery].label`, `meta.views[id=delivery].note`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Check-in](https://developer.apple.com/documentation/devicemanagement/check-in), [Set Bootstrap Token](https://developer.apple.com/documentation/devicemanagement/set-bootstrap-token), [Go cipher.NewGCM](https://pkg.go.dev/crypto/cipher#NewGCM).

### storage-backends

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | In-memory, SQLite (pure Go), PostgreSQL and MySQL all run the same suite | SQLite, PostgreSQL, and MySQL reuse sqlcommon with backend-specific dialects. Memory storage implements the contracts without a SQL database. |
| `cards[0].items[1]` | A Failing wrapper injects an error by method name for failing-path tests | Replacement tests verify successful promotion and failed, cancelled, or expired attempts without resetting working enrollment state. |
| `cards[0].title` | Shared backend contracts | Shared implementation |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | Selected secret columns use row-bound AES-256-GCM | With a keyring, SQL stores seal selected secret columns and the replacement state blob. Raw check-in data can still contain secrets. |
| `cards[1].items[1]` | Raw check-in records can contain unsealed secrets; protect database and backups | Persistent app push credentials require encryption and occupy apppush/v1/ in state.Store. They do not use the MDM push-certificate table. |
| `cards[1].title` | Sealing boundary | Encryption boundaries |
| `cards[2].dot` | [new field] | amber |
| `cards[2].items[0]` | [new field] | The SQL rewrap list includes replacement state. It does not rewrap apppush/v1/; retain accepted keys until those credentials are re-imported. |
| `cards[2].title` | [new field] | Key rotation |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Go cipher.NewGCM — https://pkg.go.dev/crypto/cipher#NewGCM |
| `cards[3].items[1]` | [new field] | Go Modules Reference — https://go.dev/ref/mod |
| `cards[3].title` | [new field] | Public documentation |
| `components[id=inmem].label` | storage/inmem | In-memory backend |
| `components[id=inmem].sublabel` | reference backend | Process-local state for development |
| `components[id=crypt].label` | storage/crypt | Encryption keyring |
| `components[id=crypt].sublabel` | AES-256-GCM keyring | Authenticates and encrypts selected data |
| `components[id=sealedcols].label` | Sealed columns | Protected SQL values |
| `components[id=sealedcols].sublabel` | row-bound AAD | Tokens, keys, and replacement state |
| `components[id=suite].label` | storage/storagetest | Shared backend tests |
| `components[id=suite].sublabel` | one suite, every backend | Verify the same storage contract |
| `components[id=featurestores].label` | Feature stores | Feature-specific stores |
| `components[id=featurestores].sublabel` | own migration set each | Separate contracts and migrations |
| `components[id=pool].label` | one *sql.DB | Shared SQL connection pool |
| `components[id=pool].sublabel` | shared by every store | Reused by enabled persistent stores |
| `meta.repository.revision` | a817270f13b4ef7a9b1d2895118981120546b3a2 | ff85958196cb013e8e14c1055f067319b07e3299 |
| `meta.views[id=sealed].label` | Secrets at rest | Configured encryption |
| `meta.views[id=sealed].note` | Secret columns are sealed before they reach the driver. | A configured keyring seals selected SQL values, including pending replacement credentials and tokens. |
| `meta.views[id=features].label` | Feature stores | Feature persistence |
| `meta.views[id=features].note` | Domain stores and protocol security state share one SQL pool with separate migrations. | Persistent feature stores share a SQL pool. App push uses a separate namespace in transactional state. |

Retain: `components[id=contract].label`, `components[id=contract].sources[0].label`, `components[id=contract].sublabel`, `components[id=secretspkg].label`, `components[id=secretspkg].sources[0].label`, `components[id=secretspkg].sublabel`, `components[id=secretspkg].tag`, `components[id=sqlbackends].label`, `components[id=sqlbackends].sources[0].label`, `components[id=sqlbackends].sources[1].label`, `components[id=sqlbackends].sources[2].label`, `components[id=sqlbackends].sublabel`, `components[id=featuremig].label`, `components[id=featuremig].sublabel`, `components[id=inmem].sources[0].label`, `components[id=sqlcommon].label`, `components[id=sqlcommon].sources[0].label`, `components[id=sqlcommon].sublabel`, `components[id=crypt].sources[0].label`, `components[id=sealedcols].tag`, `components[id=suite].sources[0].label`, `components[id=suite].sources[1].label`, `components[id=dialect].label`, `components[id=dialect].sources[0].label`, `components[id=dialect].sublabel`, `components[id=featurestores].tag`, `components[id=pool].sources[0].label`, `connections[id=e1].label`, `connections[id=e2].label`, `connections[id=e11].label`, `connections[id=e12].label`, `connections[id=e3].label`, `connections[id=e4].label`, `connections[id=e5].label`, `connections[id=e6].label`, `connections[id=e7].label`, `connections[id=e8].label`, `connections[id=e9].label`, `connections[id=e10].label`, `meta.title`, `meta.views[id=sqlpath].label`, `meta.views[id=sqlpath].note`, `meta.views[id=proof].label`, `meta.views[id=proof].note`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Go cipher.NewGCM](https://pkg.go.dev/crypto/cipher#NewGCM), [Go Modules Reference](https://go.dev/ref/mod).

### service-layer

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | aspen-mdm-checkin routes to check-in, aspen-mdm to the command handler | The HTTP handler distinguishes check-in from command traffic by Content-Type. The service checks configured certificate status before running hooks. |
| `cards[0].items[1]` | Unsupported content type is 415; wrong method is 405; account reauthentication can return 401 | A pending profile replacement handles its permitted candidate exchanges before ordinary dispatch; unrelated requests still require the appropriate identity checks. |
| `cards[0].title` | One URL, two protocols | Request processing |
| `cards[1].items[0]` | Enabled certificate status checks precede hooks and service side effects | DMHandler connects the service to declarative management without importing the engine. UserVerifier supplies user-channel authentication policy. |
| `cards[1].items[1]` | Account hooks reserve identity before storage and confirm after success | Events report completed operations. Asynchronous subscribers preserve accepted work after request cancellation, within their configured drain lifecycle. |
| `cards[1].title` | Authorization order | Extension points |
| `cards[2].dot` | [new field] | slate |
| `cards[2].items[0]` | [new field] | Check-in — https://developer.apple.com/documentation/devicemanagement/check-in |
| `cards[2].items[1]` | [new field] | Sending MDM commands to a device — https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device |
| `cards[2].items[2]` | [new field] | Declarative Management check-in — https://developer.apple.com/documentation/devicemanagement/declarative-management |
| `cards[2].title` | [new field] | Public documentation |
| `components[id=certmw].label` | Cert middlewares | Device identity extraction |
| `components[id=certmw].sublabel` | TLS · header · signature | TLS, trusted proxy, or signed body |
| `components[id=errors].label` | service.Code | HTTP error mapping |
| `components[id=errors].sublabel` | 403 · 400 · 410 · 501 | Translate service failures to responses |
| `components[id=httpapi].label` | httpapi | MDM HTTP handler |
| `components[id=httpapi].sublabel` | /mdm Content-Type fork | Routes check-in and command requests |
| `components[id=bus].label` | event.Bus | Event bus |
| `components[id=bus].sublabel` | synchronous by default | Publishes service outcomes |
| `components[id=sinks].label` | eventsink | Event consumers |
| `components[id=sinks].sublabel` | webhook · slog | Audit, logging, and webhooks |
| `components[id=hooks].label` | Hook chain | Service hooks |
| `components[id=hooks].sublabel` | Before · After · veto | Run policy and completion callbacks |
| `components[id=core].label` | service.Core | MDM service core |
| `components[id=core].sublabel` | Checkin · Connect · Enqueue | Check-in, command results, and queues |
| `components[id=dmhandler].label` | DMHandler | Declarative request adapter |
| `components[id=dmhandler].sublabel` | inproc or proxyclient | In-process or signed HTTP forwarding |
| `components[id=verifier].label` | UserVerifier | User authentication verifier |
| `components[id=verifier].sublabel` | digest or custom verifier | Checks the user-channel digest |
| `connections[id=e4].label` | refusal body | Map errors to HTTP responses |
| `connections[id=e5].label` | every operation | Run configured hooks |
| `connections[id=e7].label` | DeclarativeManagement | Dispatch declarative check-in |
| `meta.repository.revision` | a817270f13b4ef7a9b1d2895118981120546b3a2 | ff85958196cb013e8e14c1055f067319b07e3299 |
| `meta.title` | Service Core: Seams and Refusal | MDM Service: Request Handling and Extension Points |
| `meta.views[id=seams].label` | Seams | Extension interfaces |
| `meta.views[id=seams].note` | Every feature attaches through an interface, so service imports none of them. | Declarative request adapters and user-authentication verifiers supply behaviour through service interfaces. |

Retain: `components[id=certmw].sources[0].label`, `components[id=device].label`, `components[id=device].sublabel`, `components[id=storepkg].label`, `components[id=storepkg].sublabel`, `components[id=ddmengine].label`, `components[id=ddmengine].sublabel`, `components[id=errors].sources[0].label`, `components[id=httpapi].sources[0].label`, `components[id=bus].sources[0].label`, `components[id=sinks].sources[0].label`, `components[id=sinks].sources[1].label`, `components[id=sinks].tag`, `components[id=hooks].sources[0].label`, `components[id=core].sources[0].label`, `components[id=core].sources[1].label`, `connections[id=e1].label`, `connections[id=e2].label`, `connections[id=e11].label`, `connections[id=e3].label`, `connections[id=e6].label`, `connections[id=e8].label`, `connections[id=e9].label`, `connections[id=e10].label`, `meta.views[id=request].label`, `meta.views[id=request].note`, `meta.views[id=refusal].label`, `meta.views[id=refusal].note`, `meta.views[id=events].label`, `meta.views[id=events].note`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Check-in](https://developer.apple.com/documentation/devicemanagement/check-in), [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device), [Declarative Management check-in](https://developer.apple.com/documentation/devicemanagement/declarative-management).

### checkin-dispatch

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | Four state changes return an empty body | Authenticate, TokenUpdate, SetBootstrapToken, and CheckOut normally return an empty successful response. Their order is not a universal workflow. |
| `cards[0].items[1]` | Four answers return a plist; DDM returns JSON | GetBootstrapToken, GetToken, UserAuthenticate, and ReturnToService return property lists. DeclarativeManagement returns JSON or an empty status acknowledgement. |
| `cards[0].title` | Two shapes | Independent message handlers |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | Unknown enrollments get 403; recognized account sessions can receive a 401 challenge | Ordinary messages enforce the configured identity policy. Account authentication may request reauthentication with HTTP 401. |
| `cards[1].items[1]` | A declined user is a 410 | A missing ReturnToService policy returns Enabled=false. Unsupported optional handlers and invalid requests follow their specific service error mappings. |
| `cards[1].items[2]` | ReturnToService with no handler answers Enabled false | [remove field] |
| `cards[1].title` | Refusal | Identity and refusal |
| `cards[2].dot` | [new field] | amber |
| `cards[2].items[0]` | [new field] | A pending replacement accepts only the permitted candidate exchanges and preserves working enrollment state. See the replacement sequence for promotion conditions. |
| `cards[2].title` | [new field] | Controlled replacement |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Check-in — https://developer.apple.com/documentation/devicemanagement/check-in |
| `cards[3].items[1]` | [new field] | Set Bootstrap Token — https://developer.apple.com/documentation/devicemanagement/set-bootstrap-token |
| `cards[3].items[2]` | [new field] | Declarative Management check-in — https://developer.apple.com/documentation/devicemanagement/declarative-management |
| `cards[3].title` | [new field] | Public documentation |
| `meta.title` | Check-in Dispatch and Refusal | MDM Check-in Messages and Their Effects |
| `meta.views[id=writes].label` | State changes | State-changing messages |
| `meta.views[id=writes].note` | State-changing check-ins return empty bodies; their ordering depends on the device. | Each message is dispatched independently. Bootstrap-token escrow and checkout are not mandatory enrollment steps. |
| `meta.views[id=answers].label` | Answers | Messages with responses |
| `meta.views[id=answers].note` | Four response types marshal plist; DeclarativeManagement returns JSON. | Bootstrap tokens, service tokens, user authentication, and ReturnToService return plists; declarative requests use JSON or no body. |
| `meta.views[id=gate].label` | Refusal | Identity checks |
| `meta.views[id=gate].note` | Certificate status precedes dispatch; Authenticate has rotation and reuse checks. | Certificate status precedes hooks. Controlled replacement handles permitted candidate messages before normal dispatch. |
| `nodes[id=dispatch].label` | dispatchCheckin | Check-in dispatch |
| `nodes[id=dispatch].sublabel` | switch on type | Select the handler for MessageType |
| `nodes[id=authenticate].sublabel` | creates it | Create enrollment and associate identity |
| `nodes[id=refusal].label` | 403 or account 401 | Request rejected |
| `nodes[id=refusal].sublabel` | 400 · 410 · 501 | Return the relevant HTTP error |
| `nodes[id=returntoservice].sublabel` | erase, re-enrol | Ask whether erasure and re-enrollment are allowed |
| `nodes[id=tokenupdate].sublabel` | enables it | Store push details and enable channel |
| `nodes[id=setbootstrap].sublabel` | escrow | Escrow the device’s bootstrap token |
| `nodes[id=checkout].sublabel` | disable, keep row | Disable enrollment; retain its record |
| `nodes[id=getbootstrap].sublabel` | reads escrow | Return the stored bootstrap token |
| `nodes[id=gettoken].sublabel` | GetTokenHandler | Ask the configured token provider |
| `nodes[id=userauthenticate].sublabel` | digest challenge | Authenticate a macOS user channel |
| `nodes[id=declarative].sublabel` | DMHandler seam | Synchronize declarations or report status |
| `nodes[id=authorize].label` | Identity and status | Identity and account policy |
| `nodes[id=authorize].sublabel` | pin · status · account | Checks vary by message and channel |

Retain: `edges[id=c1].label`, `edges[id=c5].label`, `edges[id=c4].label`, `edges[id=c12].label`, `edges[id=c6].label`, `edges[id=c7].label`, `edges[id=c8].label`, `edges[id=c9].label`, `edges[id=c10].label`, `edges[id=c11].label`, `edges[id=c2].label`, `edges[id=c3].label`, `groups[id=life].label`, `groups[id=pulls].label`, `lanes[id=entry].label`, `lanes[id=write].label`, `lanes[id=read].label`, `lanes[id=gate].label`, `meta.legend.entries.backend.label`, `meta.legend.entries.security.label`, `nodes[id=authenticate].label`, `nodes[id=returntoservice].label`, `nodes[id=tokenupdate].label`, `nodes[id=setbootstrap].label`, `nodes[id=checkout].label`, `nodes[id=getbootstrap].label`, `nodes[id=gettoken].label`, `nodes[id=userauthenticate].label`, `nodes[id=declarative].label`, `nodes[id=authorize].tag`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Check-in](https://developer.apple.com/documentation/devicemanagement/check-in), [Set Bootstrap Token](https://developer.apple.com/documentation/devicemanagement/set-bootstrap-token), [Declarative Management check-in](https://developer.apple.com/documentation/devicemanagement/declarative-management).

### ddm-engine

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | The engine writes change rows inside its transaction and never calls back into the notifier | The engine commits declaration changes and their change records together. The notifier reads those records after commit. |
| `cards[0].items[1]` | The notifier drains them on a 1s poll; the admin route wrapper Kicks it so the poll is not the only trigger | The notifier queues a DeclarativeManagement command through service.Core and requests an MDM push when a pusher is configured. |
| `cards[0].title` | Transactional change records | From desired state to synchronization |
| `cards[1].items[0]` | The notifier's Enqueuer is service.Core, so a DDM kick runs the hooks, the target screen and the audit trail | Defaults coalesce recent changes for two seconds and poll each second; admin mutations also wake the notifier. These timings are repository policy. |
| `cards[1].items[1]` | One DeclarativeManagement command per enrollment, under the dedupe key ddm | Ordinary Authenticate and CheckOut cleanup clears declarative state. Controlled profile replacement avoids that reset and retains the working enrollment’s state. |
| `cards[1].title` | Through the command path | Timing and cleanup |
| `cards[2].dot` | [new field] | slate |
| `cards[2].items[0]` | [new field] | Integrating declarative management — https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management |
| `cards[2].items[1]` | [new field] | Declarations — https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations |
| `cards[2].items[2]` | [new field] | RFC 8785 — JSON Canonicalization Scheme — https://www.rfc-editor.org/rfc/rfc8785 |
| `cards[2].title` | [new field] | Public documentation |
| `components[id=adapters].label` | ddmadapter | Declarative request adapters |
| `components[id=adapters].sublabel` | inproc · proxyclient | Bridge MDM check-in to the engine |
| `components[id=servicehook].label` | ddmsync.ServiceHook | Enrollment cleanup hook |
| `components[id=servicehook].sublabel` | clear enrollment state | Clears state after ordinary reset or checkout |
| `components[id=engine].label` | ddm.Engine | Declarative management engine |
| `components[id=engine].sublabel` | Handle · Tokens | Builds assigned declarations and stores status |
| `components[id=canonjson].label` | internal/canonjson | Canonical JSON |
| `components[id=canonjson].sublabel` | RFC 8785 | Stable bytes for declaration tokens |
| `components[id=notifier].label` | ddmsync.Notifier | Synchronization notifier |
| `components[id=notifier].sublabel` | 2s coalescing window | Coalesces committed declaration changes |
| `components[id=ddmstore].label` | ddm.Store | Declarative state store |
| `components[id=ddmstore].sublabel` | Update(fn func(Tx)) | Declarations, assignments, and snapshots |
| `components[id=predicate].label` | ddm/predicate | Activation predicate validation |
| `components[id=predicate].sublabel` | NSPredicate subset | Reject syntax outside the supported grammar |
| `components[id=core].label` | service.Core | MDM service core |
| `components[id=core].sublabel` | Enqueue runs the hooks | Validates and queues sync commands |
| `components[id=pushn].label` | pushnotify.Notifier | MDM push notifier |
| `components[id=pushn].sublabel` | wake the device | Prompts a device connection |
| `connections[id=e3].label` | clear on CheckOut or Authenticate | Clear after ordinary enrollment reset |
| `connections[id=e4].label` | change rows, in one Tx | Commit state and change records |
| `connections[id=e5].label` | ServerToken | Derive stable declaration tokens |
| `connections[id=e6].label` | activation predicate | Validate activation predicate syntax |
| `connections[id=e7].label` | PendingChanges | Read committed pending changes |
| `meta.repository.revision` | a817270f13b4ef7a9b1d2895118981120546b3a2 | ff85958196cb013e8e14c1055f067319b07e3299 |
| `meta.views[id=serveloop].label` | Serve loop | Serve device requests |
| `meta.views[id=serveloop].note` | The device reaches the engine only as service.Core's DMHandler. | The MDM service forwards declarative check-ins through an in-process adapter or the signed internal HTTP adapter. |
| `meta.views[id=notifyloop].label` | Notify loop | Notify after commit |
| `meta.views[id=notifyloop].note` | The notifier polls change rows; the engine never calls back into it. | The notifier reads committed changes, queues a synchronization command, and requests a wake through its configured pusher. |

Retain: `components[id=adapters].sources[0].label`, `components[id=adapters].sources[1].label`, `components[id=device].label`, `components[id=device].sublabel`, `components[id=sqlstore].label`, `components[id=sqlstore].sublabel`, `components[id=servicehook].sources[0].label`, `components[id=engine].sources[0].label`, `components[id=engine].sources[1].label`, `components[id=canonjson].sources[0].label`, `components[id=notifier].sources[0].label`, `components[id=ddmstore].sources[0].label`, `components[id=predicate].sources[0].label`, `components[id=core].sources[0].label`, `connections[id=e1].label`, `connections[id=e2].label`, `connections[id=e8].label`, `connections[id=e9].label`, `connections[id=e10].label`, `meta.title`, `meta.views[id=content].label`, `meta.views[id=content].note`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Integrating declarative management](https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management), [Declarations](https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations), [RFC 8785 — JSON Canonicalization Scheme](https://www.rfc-editor.org/rfc/rfc8785).

### ddm-serve

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | tokens, declaration-items, status, and declaration/{kind}/{identifier} | Endpoint selects tokens, declaration-items, declaration/{kind}/{identifier}, or status. These are message values, not four public server routes. |
| `cards[0].items[1]` | The kind is activation, asset, configuration or management; anything else is ErrBadEndpoint | The first three return JSON; accepted status reports receive an empty HTTP 200 response. |
| `cards[0].title` | Four operations, one string | Four operations inside check-in |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | Each enrollment gets a snapshot, so it fetches exactly what its manifest advertised | Manifest retrieval refreshes the enrollment snapshot. Declaration retrieval uses the version advertised by that snapshot. |
| `cards[1].items[1]` | A declaration absent from the enrollment's manifest returns 404, and the device removes it | Invalid endpoint syntax and invalid status data are rejected at different stages. HTTP 404 for a declaration tells the device to remove its local copy. |
| `cards[1].title` | 404 means remove | Snapshots and errors |
| `cards[2].dot` | [new field] | slate |
| `cards[2].items[0]` | [new field] | Declarative Management check-in — https://developer.apple.com/documentation/devicemanagement/declarative-management |
| `cards[2].items[1]` | [new field] | Declarations — https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations |
| `cards[2].items[2]` | [new field] | RFC 8785 — JSON Canonicalization Scheme — https://www.rfc-editor.org/rfc/rfc8785 |
| `cards[2].title` | [new field] | Public documentation |
| `edges[id=w1].label` | one string | Select the requested operation |
| `edges[id=w3].label` | hash it | Serialize SyncTokens |
| `edges[id=w4].label` | answer | Return JSON response |
| `meta.views[id=serve].label` | Tokens | Synchronization tokens |
| `meta.views[id=serve].note` | The main path: parse the endpoint, render the token, answer 200. | Refresh the enrollment snapshot and return its declaration token with the last token-change timestamp. |
| `meta.views[id=manifest].note` | declaration-items is served from the enrollment's snapshot; a declaration outside it is a 404. | Manifest retrieval refreshes the snapshot. Declaration retrieval serves its advertised version or returns HTTP 404. |
| `nodes[id=checkin].sublabel` | one Endpoint string | Read Endpoint from the check-in message |
| `nodes[id=parse].label` | ParseEndpoint | Parse requested operation |
| `nodes[id=parse].sublabel` | four operations | Validate the endpoint name and identifier |
| `nodes[id=statusbad].label` | 400 | Invalid status report |
| `nodes[id=statusbad].sublabel` | empty, too big, malformed | Missing data or failed report validation |
| `nodes[id=tokens].sublabel` | DeclarationsToken | Refresh snapshot and return sync token |
| `nodes[id=canonjson].label` | canonjson | Serialize canonical JSON |
| `nodes[id=canonjson].sublabel` | RFC 8785 | Encode a stable response body |
| `nodes[id=response].label` | 200 JSON | HTTP 200 |
| `nodes[id=response].sublabel` | empty for status | JSON response or empty status acknowledgement |
| `nodes[id=declitems].sublabel` | from the snapshot | Refresh and return the declaration manifest |
| `nodes[id=declaration].label` | declaration/{kind} | declaration/{kind}/{identifier} |
| `nodes[id=declaration].sublabel` | four standalone kinds | Fetch the version advertised in the snapshot |
| `nodes[id=status].sublabel` | StatusReport in | Validate and persist the device’s report |
| `nodes[id=badendpoint].label` | ErrBadEndpoint | Invalid endpoint |
| `nodes[id=badendpoint].sublabel` | bad kind, too long, no data | Unknown operation, kind, or identifier |
| `nodes[id=notfound].label` | 404 | HTTP 404 |
| `nodes[id=notfound].sublabel` | device removes it | Requested declaration is not available |

Retain: `edges[id=w2].label`, `edges[id=w5].label`, `edges[id=w7].label`, `edges[id=w8].label`, `edges[id=w9].label`, `edges[id=w11].label`, `edges[id=w12].label`, `lanes[id=endpoint].label`, `lanes[id=manifest].label`, `lanes[id=single].label`, `lanes[id=gate].label`, `meta.legend.entries.backend.label`, `meta.legend.entries.external.label`, `meta.legend.entries.security.label`, `meta.title`, `meta.views[id=manifest].label`, `meta.views[id=refused].label`, `meta.views[id=refused].note`, `nodes[id=checkin].label`, `nodes[id=tokens].label`, `nodes[id=declitems].label`, `nodes[id=status].label`, `phases[id=parse].label`, `phases[id=serve].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Declarative Management check-in](https://developer.apple.com/documentation/devicemanagement/declarative-management), [Declarations](https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations), [RFC 8785 — JSON Canonicalization Scheme](https://www.rfc-editor.org/rfc/rfc8785).

### acme-internals

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | The chain verifies to the Apple Enterprise Attestation Root | The server verifies Apple attestation evidence, challenge freshness, and the expected device identity before applying admission policy. |
| `cards[0].items[1]` | Freshness comes from the challenge token, the attested key must be the CSR key, and the attestation must name the bound device | At finalization, the certificate signing request must use the attested key. Accepting a challenge does not itself issue a certificate. |
| `cards[0].title` | Attestation verification | What attestation proves |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | Enabled revocation registers issuance before the certificate is returned | Signed requests use one-use replay nonces. Orders and identifiers are persisted so concurrent requests cannot reuse an authorization. |
| `cards[1].items[1]` | Optional revokeCert uses certificate-key or account authorization | ACME protocol failures use problem documents. Optional issuance registration and revocation add certificate-status tracking. |
| `cards[1].title` | Optional certificate status | Protocol and policy |
| `cards[2].dot` | [new field] | slate |
| `cards[2].items[0]` | [new field] | ACMECertificate — https://developer.apple.com/documentation/devicemanagement/acmecertificate |
| `cards[2].items[1]` | [new field] | Deploy Managed Device Attestation — https://support.apple.com/en-gb/guide/deployment/dep54e5ac1fd/web |
| `cards[2].items[2]` | [new field] | RFC 8555 — ACME — https://www.rfc-editor.org/rfc/rfc8555 |
| `cards[2].title` | [new field] | Public documentation |
| `components[id=directory].label` | GET /acme/directory | ACME discovery |
| `components[id=directory].sublabel` | then HEAD new-nonce | Find operation URLs and obtain a nonce |
| `components[id=device].label` | Device | Apple device |
| `components[id=device].sublabel` | Secure Enclave key | Requests a hardware-bound identity |
| `components[id=identifiers].label` | Identifiers | Enrollment identifier validation |
| `components[id=identifiers].sublabel` | HMAC · static · one-time | Bind one order to its intended device |
| `components[id=sqlstore].label` | acme/sqlstore · inmem | ACME storage implementations |
| `components[id=sqlstore].sublabel` | own migration set | Memory or feature-specific SQL storage |
| `components[id=capkg].label` | ca.Local | Certificate authority |
| `components[id=capkg].sublabel` | signs the CSR | Issue the approved identity certificate |
| `components[id=jose].label` | acme/jose | Signed-request verification |
| `components[id=jose].sublabel` | JWS · JWK · thumbprint | Parse JWS and verify account signatures |
| `components[id=server].label` | acme.Server | ACME request coordinator |
| `components[id=server].sublabel` | signed(fn, keyMode) | Validates requests and advances orders |
| `components[id=attest].label` | acme/attest | Attestation verification |
| `components[id=attest].sublabel` | Apple root · properties | Check chain, freshness, and device facts |
| `components[id=problem].label` | acme.Problem | ACME error response |
| `components[id=problem].sublabel` | RFC 7807 problem+json | Return an application/problem+json body |
| `components[id=nonces].label` | Nonce | Replay nonces |
| `components[id=nonces].sublabel` | one-shot | Reject reused or expired request nonces |
| `components[id=orders].label` | Order · Authorization | Orders and authorizations |
| `components[id=orders].sublabel` | one device-attest-01 | Track the device-attest-01 challenge |
| `components[id=store].label` | acme.Store | ACME persistence |
| `components[id=store].sublabel` | Update(fn func(Tx)) | Store protocol state transactionally |
| `components[id=policy].label` | acme.Policy | Admission policy |
| `components[id=policy].sublabel` | any · dep · sip | Decide whether attested facts are acceptable |
| `connections[id=e1].label` | JOSE POST | Signed ACME request |
| `connections[id=e2].label` | then a nonce | Discover URLs and obtain nonce |
| `connections[id=e11].from` | policy | server |
| `connections[id=e11].label` | then sign | Finalize: verify CSR, then issue |
| `connections[id=e4].from` | jose | server |
| `connections[id=e4].label` | take the nonce | Consume the request nonce |
| `connections[id=e6].label` | ClaimIdentifier | Claim the identifier once |
| `connections[id=e8].label` | Update(fn) | Persist order transitions |
| `meta.repository.revision` | a817270f13b4ef7a9b1d2895118981120546b3a2 | ff85958196cb013e8e14c1055f067319b07e3299 |
| `meta.views[id=signed].label` | Signed ACME POSTs | Signed ACME requests |
| `meta.views[id=signed].note` | A JWS with an embedded JWK for new-account, a key id after that; each nonce is one-shot. | New-account identifies its key with a JWK; later requests use the account key ID. The server consumes each replay nonce once. |
| `meta.views[id=order].label` | Order | Create an order |
| `meta.views[id=order].note` | One permanent-identifier, claimed once and bound to a specific device. | An enrollment identifier binds the order to its intended device and is claimed once. |
| `meta.views[id=attestation].label` | Attestation | Verify and authorize |
| `meta.views[id=attestation].note` | Chain, freshness, attested key and device binding, then a policy decision. | Validate Apple attestation and admission policy. Finalization separately proves that the CSR uses the attested key. |
| `meta.views[id=refused].label` | Refusal | Protocol errors |
| `meta.views[id=refused].note` | Every refusal leaves as an RFC 7807 problem document. | ACME handlers return problem documents for protocol failures; HTTP transport and proxy failures are outside that contract. |

Retain: `components[id=identifiers].sources[0].label`, `components[id=capkg].sources[0].label`, `components[id=capkg].sources[1].label`, `components[id=jose].sources[0].label`, `components[id=jose].sources[1].label`, `components[id=server].sources[0].label`, `components[id=server].sources[1].label`, `components[id=attest].sources[0].label`, `components[id=attest].sources[1].label`, `components[id=problem].sources[0].label`, `components[id=orders].sources[0].label`, `components[id=store].sources[0].label`, `components[id=policy].sources[0].label`, `connections[id=e12].label`, `connections[id=e13].label`, `connections[id=e3].label`, `connections[id=e5].label`, `connections[id=e7].label`, `connections[id=e9].label`, `connections[id=e10].label`, `meta.title`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [ACMECertificate](https://developer.apple.com/documentation/devicemanagement/acmecertificate), [Deploy Managed Device Attestation](https://support.apple.com/en-gb/guide/deployment/dep54e5ac1fd/web), [RFC 8555 — ACME](https://www.rfc-editor.org/rfc/rfc8555).

### push

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | Notifier reads push info from storage, then sends one push per Target | The notifier loads the device’s APNs token, MDM topic, and PushMagic. Repeated wake requests are coalesced. |
| `cards[0].items[1]` | A burst for one enrollment collapses into a single push | The notification prompts a device connection. Commands are delivered in the later HTTPS response from the MDM server. |
| `cards[0].title` | Send path | What the push contains |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | A 410 publishes PushTokenInvalid; the caller decides how to retire stored tokens | The client selects the MDM push certificate by topic. Certificate replacement retires cached connections; shutdown closes the clients. |
| `cards[1].items[1]` | Transient failures are distinct from invalid-token outcomes | APNs acceptance does not prove that a command ran. App notifications use a separate client and credential store; see the app delivery companion. |
| `cards[1].title` | Push outcomes | Credentials and outcomes |
| `cards[2].dot` | [new field] | slate |
| `cards[2].items[0]` | [new field] | Setting up push notifications for your device management customers — https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers |
| `cards[2].items[1]` | [new field] | Sending MDM commands to a device — https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device |
| `cards[2].items[2]` | [new field] | Establishing a certificate-based connection to APNs — https://developer.apple.com/documentation/usernotifications/establishing-a-certificate-based-connection-to-apns |
| `cards[2].title` | [new field] | Public documentation |
| `components[id=caller].label` | Callers | Command or declaration caller |
| `components[id=caller].sublabel` | ddmsync.Notifier · admin API | Requests a wake notification |
| `components[id=pushstore].label` | storage.PushStore | Stored MDM push details |
| `components[id=pushstore].sublabel` | token · topic · magic | APNs token, topic, and PushMagic |
| `components[id=notifier].label` | pushnotify.Notifier | MDM push notifier |
| `components[id=notifier].sublabel` | Notify(ids) | Resolve enabled enrollment targets |
| `components[id=bus].label` | event.Bus | Invalid-token event |
| `components[id=bus].sublabel` | PushTokenInvalid | Caller decides how to retire the token |
| `components[id=coalescer].label` | push.Coalescer | Duplicate wake suppression |
| `components[id=coalescer].sublabel` | 5s window | Default five-second coalescing window |
| `components[id=certstoredb].label` | storage.PushCertStore | Stored MDM push certificates |
| `components[id=certstoredb].sublabel` | versioned rows | Versioned credentials keyed by topic |
| `components[id=apnsclient].label` | push/apns.Client | MDM APNs client |
| `components[id=apnsclient].sublabel` | HTTP/2 | Send the MDM notification over HTTP/2 |
| `components[id=certstore].label` | push.CertStore | Certificate provider |
| `components[id=certstore].sublabel` | static or store-backed | Load the credential for the MDM topic |
| `components[id=pushcert].label` | pushcert | MDM certificate validation |
| `components[id=pushcert].sublabel` | topic from certificate | Verify key, validity, and topic |
| `connections[id=notifier-bus].label` | 410 emits invalid event | APNs 410: publish invalid-token event |
| `connections[id=client-apns].label` | apns-push-type mdm | Send MDM payload and push headers |
| `meta.repository.revision` | a817270f13b4ef7a9b1d2895118981120546b3a2 | ff85958196cb013e8e14c1055f067319b07e3299 |
| `meta.title` | Push: Waking a Managed Device | MDM Push Notifications: Prompting Device Connections |

Retain: `boundaries[0].label`, `components[id=notifier].sources[0].label`, `components[id=apnsclient].sources[0].label`, `components[id=certstore].sources[0].label`, `components[id=apns].label`, `components[id=apns].sublabel`, `components[id=pushcert].sources[0].label`, `connections[id=caller-notifier].label`, `connections[id=store-notifier].label`, `connections[id=notifier-coalescer].label`, `connections[id=coalescer-client].label`, `connections[id=certstore-client].label`, `connections[id=db-certstore].label`, `connections[id=certstore-pushcert].label`, `meta.views[id=send].label`, `meta.views[id=send].note`, `meta.views[id=certs].label`, `meta.views[id=certs].note`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Setting up push notifications for your device management customers](https://developer.apple.com/documentation/devicemanagement/setting-up-push-notifications-for-your-device-management-customers), [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device), [Establishing a certificate-based connection to APNs](https://developer.apple.com/documentation/usernotifications/establishing-a-certificate-based-connection-to-apns).

### apple-service-clients

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | DEP uses OAuth 1.0a with a server token from the portal exchange | The device-assignment client uses OAuth 1.0a credentials from the server-token exchange. The package retains the name dep. |
| `cards[0].items[1]` | Business Manager uses an ES256 client assertion | axm exchanges an ES256-signed client assertion for an OAuth access token, then sends that token on API calls. Software lookup is public. |
| `cards[0].items[2]` | The software lookup service needs no credentials at all | [remove field] |
| `cards[0].title` | Service authentication | Three authentication models |
| `cards[1].items[0]` | The syncer pages by cursor and handles cursor expiry by refetching | The synchronizer starts with a full fetch and follows incremental cursors; an expired cursor causes a new full fetch. |
| `cards[1].items[1]` | The assigner is state-driven and reads back what Apple recorded | The reconciler compares desired profile assignment with stored devices and calls Apple. Readback is configurable in the library and enabled by the reference server. |
| `cards[1].items[2]` | A 429 delays retries according to the service response | [remove field] |
| `cards[1].title` | Assignment | Inventory and assignments |
| `cards[2].dot` | rose | amber |
| `cards[2].items[0]` | An old OS is refused with ErrorCodeCodeSoftwareUpdateRequired (com.apple.softwareupdate.required) | The enrollment handler applies its configured update policy. Software lookup supplies available versions; it does not itself send the device’s HTTP rejection. |
| `cards[2].items[1]` | gdmf answers what the latest version is for that device | [remove field] |
| `cards[2].title` | Software update gate | Software updates |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Device assignment — https://developer.apple.com/documentation/devicemanagement/device-assignment |
| `cards[3].items[1]` | [new field] | Implementing OAuth for the Apple School Manager and Apple Business APIs — https://developer.apple.com/documentation/apple-school-and-business-manager-api/implementing-oauth-for-the-apple-school-manager-and-apple-business-api |
| `cards[3].items[2]` | [new field] | ErrorCodeSoftwareUpdateRequired — https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired |
| `cards[3].title` | [new field] | Public documentation |
| `components[id=depsvc].label` | DEP service | Device-assignment service |
| `components[id=depsvc].sublabel` | Device assignment API | Automated Device Enrollment API |
| `components[id=abm].label` | Business Manager API | Apple business and school APIs |
| `components[id=abm].sublabel` | JSON:API | Organization data and device operations |
| `components[id=depclient].sublabel` | sessions · cursors · token PKI | Authenticated device-assignment requests |
| `components[id=axmclient].sublabel` | paging · activities | OAuth access tokens and paged API calls |
| `components[id=gdmfclient].label` | gdmf.Client | Software lookup client |
| `components[id=gdmfclient].sublabel` | TTL cache, last-good | Cached catalogue with last-good fallback |
| `components[id=syncer].label` | dep.Syncer | Device inventory synchronizer |
| `components[id=syncer].sublabel` | cursor expiry · dedupe | Fetch all devices, then apply changes |
| `components[id=assigner].label` | dep.Assigner | Enrollment-profile reconciler |
| `components[id=assigner].sublabel` | state-driven | Assign profiles and verify Apple state |
| `components[id=adegate].label` | ADE update gate | Enrollment OS update policy |
| `components[id=adegate].sublabel` | enroll/ade | Choose a required software version |
| `connections[id=axm-auth].label` | ES256 assertion | OAuth access token |
| `connections[id=syncer-client].label` | FetchDevices | FetchDevices or SyncDevices |
| `connections[id=assigner-client].label` | AssignProfile | AssignProfile and device readback |
| `connections[id=syncer-store].label` | commit in a Tx | Commit devices and cursor together |
| `connections[id=assigner-store].label` | read back | Read and persist assignment state |
| `meta.repository.revision` | a817270f13b4ef7a9b1d2895118981120546b3a2 | ff85958196cb013e8e14c1055f067319b07e3299 |
| `meta.views[id=auth].note` | DEP uses OAuth 1.0a, axm uses ES256 assertions, and GDMF is public. | dep uses OAuth 1.0a; axm exchanges an ES256 assertion for a bearer access token; software lookup requires no credentials. |

Retain: `boundaries[0].label`, `components[id=gdmfsvc].label`, `components[id=gdmfsvc].sublabel`, `components[id=depclient].label`, `components[id=depclient].sources[0].label`, `components[id=depclient].sources[1].label`, `components[id=axmclient].label`, `components[id=axmclient].sources[0].label`, `components[id=axmclient].tag`, `components[id=gdmfclient].sources[0].label`, `components[id=syncer].sources[0].label`, `components[id=assigner].sources[0].label`, `components[id=depstore].label`, `components[id=depstore].sublabel`, `connections[id=dep-auth].label`, `connections[id=gdmf-auth].label`, `connections[id=gdmf-gate].label`, `meta.title`, `meta.views[id=dep].label`, `meta.views[id=dep].note`, `meta.views[id=auth].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Device assignment](https://developer.apple.com/documentation/devicemanagement/device-assignment), [Implementing OAuth for the Apple School Manager and Apple Business APIs](https://developer.apple.com/documentation/apple-school-and-business-manager-api/implementing-oauth-for-the-apple-school-manager-and-apple-business-api), [ErrorCodeSoftwareUpdateRequired](https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired).

### admin-plane

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | Bearer, then a principal, then a policy decision, then the handler | The static DM_ADMIN_TOKEN is checked first and grants root authority without Cedar. Stored-principal tokens are authenticated and authorized against policy. |
| `cards[0].items[1]` | Families include mdm, ddm, dep, axm, acme, pki, principals, audit and introspection | Each registered admin route declares an action. Policy editing requires root independently of Cedar. |
| `cards[0].items[2]` | A route with no action is a build error, not a runtime one | [remove field] |
| `cards[0].title` | Every admin request | Two authorization paths |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | The static token is compared first, in constant time, so it works when the principal store is empty or unreachable | Routes include MDM commands and results, declarative state, Apple service clients, certificates, audit, and principal administration. |
| `cards[1].items[1]` | It authenticates as root and bypasses Cedar entirely | PRs 12 and 14 add app push credentials and sends, enrollment-profile issuance, enrollment evidence, and controlled replacement. |
| `cards[1].items[2]` | Policy editing is gated on root in Go, outside Cedar, because a policy that can edit policies could grant itself anything | [remove field] |
| `cards[1].title` | Bootstrap authority | Current route coverage |
| `cards[2].dot` | emerald | amber |
| `cards[2].items[0]` | Use @file or env:NAME references; literal token arguments may appear in process listings | Reference tokens through a protected file or environment variable. The config rejects group and other permission bits. |
| `cards[2].items[1]` | A config file with any group or other permission bits is refused | Offline explanation and local bench supervision do not follow this HTTP request path. |
| `cards[2].items[2]` | Token creation prints the credential to stdout; protect captured output | [remove field] |
| `cards[2].title` | Credentials by reference | CLI credentials |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Cedar authorization overview — https://docs.cedarpolicy.com/ |
| `cards[3].items[1]` | [new field] | Sending MDM commands to a device — https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device |
| `cards[3].items[2]` | [new field] | Deploying device management enrollment profiles — https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles |
| `cards[3].title` | [new field] | Public documentation |
| `components[id=dmctl].label` | dmctl | Administration client |
| `components[id=dmctl].sublabel` | explain runs offline | dmctl or another authorized API client |
| `components[id=adminclient].label` | adminclient | Admin HTTP client |
| `components[id=adminclient].sublabel` | no redirects · 32 MiB cap | Bounded responses; redirects disabled |
| `components[id=authorized].label` | App.authorized | Route authorization wrapper |
| `components[id=authorized].sublabel` | every admin route | Checks the required action and resource |
| `components[id=config].label` | dmctl.json | CLI credential configuration |
| `components[id=config].sublabel` | 0600 or refused | Reject group or other permission bits |
| `components[id=manager].label` | adminauth.Manager | Stored-principal authorization |
| `components[id=manager].sublabel` | Authenticate · Authorize | Authenticate token and evaluate policy |
| `components[id=routes].label` | Admin route table | Authorized route handler |
| `components[id=routes].sublabel` | configured route families | Manage configured server capabilities |
| `components[id=adminstore].label` | adminauth.Store | Administration state |
| `components[id=adminstore].sublabel` | principals · tokens · policies | Principals, token records, and policies |
| `meta.repository.revision` | a817270f13b4ef7a9b1d2895118981120546b3a2 | ff85958196cb013e8e14c1055f067319b07e3299 |
| `meta.views[id=request].label` | One admin request | Authorize an admin request |
| `meta.views[id=request].note` | Bearer token, then a principal, then a Cedar decision, then the handler. | Static root tokens bypass Cedar. Stored-principal tokens require a policy decision before the route handler runs. |

Retain: `components[id=adminclient].sources[0].label`, `components[id=authorized].sources[0].label`, `components[id=config].sources[0].label`, `components[id=statictoken].label`, `components[id=statictoken].sublabel`, `components[id=manager].sources[0].label`, `components[id=routes].sources[0].label`, `components[id=routes].sources[1].label`, `components[id=routes].tag`, `components[id=cedar].label`, `components[id=cedar].sublabel`, `connections[id=cli-client].label`, `connections[id=config-cli].label`, `connections[id=client-authorized].label`, `connections[id=authorized-manager].label`, `connections[id=authorized-static].label`, `connections[id=authorized-routes].label`, `connections[id=manager-cedar].label`, `connections[id=manager-store].label`, `meta.title`, `meta.views[id=credentials].label`, `meta.views[id=credentials].note`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Cedar authorization overview](https://docs.cedarpolicy.com/), [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device), [Deploying device management enrollment profiles](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles).

### split-deployment

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | proxyclient imports proxywire and service, but never ddm | With DM_DDM_URL configured, the MDM role forwards declarative requests to the DDM role. Otherwise it calls the in-process adapter. |
| `cards[0].items[1]` | So when DM_DDM_URL is set the mdm role can only reach the engine over the hop; unset, it calls inproc in-process | The device continues to use /mdm; the internal forwarding endpoint is not a device enrollment URL. |
| `cards[0].title` | Adapter boundary | Choosing the adapter |
| `cards[1].items[0]` | The raw check-in body is POSTed unchanged, with its own Content-Type | The reference server requires request and response HMAC keys. A custom library HTTP client can add mutual TLS; that is not automatic server configuration. |
| `cards[1].items[1]` | HMAC covers request body and response status plus body | The shared runtime supports HTTP or native TLS and drains requests before workers. Process acceptance includes split-role scenarios. |
| `cards[1].items[2]` | The reference server requires both HMAC keys; mTLS and bearer options are library features | [remove field] |
| `cards[1].title` | The hop | Internal transport and runtime |
| `cards[2].dot` | rose | amber |
| `cards[2].items[0]` | 200 returns the body, 404 stays 404, 400 becomes a bad-request error | Successful bodies and declaration-not-found responses pass back through the adapter. Bad requests map to service errors; other upstream failures become internal errors. |
| `cards[2].items[1]` | Anything else becomes an internal error rather than leaking a status | [remove field] |
| `cards[2].items[2]` | Wrong keys and oversized bodies are rejected at the boundary | [remove field] |
| `cards[2].title` | Response mapping | Error mapping |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Declarative Management check-in — https://developer.apple.com/documentation/devicemanagement/declarative-management |
| `cards[3].items[1]` | [new field] | RFC 9440 — Client-Cert HTTP Header Field — https://www.rfc-editor.org/rfc/rfc9440 |
| `cards[3].title` | [new field] | Public documentation |
| `components[id=proxyclient].label` | proxyclient | Declarative forwarding client |
| `components[id=proxyclient].sublabel` | does not import ddm | Preserves the original check-in body |
| `components[id=mdmrole].label` | mdm role | Device-facing MDM role |
| `components[id=mdmrole].sublabel` | service.Core | Receives and authorizes check-in |
| `components[id=hop].label` | X-MDM-Signature | Signed internal HTTP exchange |
| `components[id=hop].sublabel` | HMAC-SHA256, optional mTLS | HMAC authenticates requests and responses |
| `components[id=inproc].label` | inproc adapter | In-process declarative adapter |
| `components[id=inproc].sublabel` | role all | Used when no remote URL is configured |
| `components[id=proxyserver].label` | proxyserver | Declarative forwarding endpoint |
| `components[id=proxyserver].sublabel` | /v1/declarative-management | Verify the signature, then decode |
| `components[id=engine].label` | ddm.Engine | Declarative management engine |
| `components[id=engine].sublabel` | ddm role | Handles the forwarded operation |
| `components[id=ddmstore].label` | ddm.Store | Declarative state storage |
| `components[id=ddmstore].sublabel` | own migration set | Snapshots, declarations, and device status |
| `meta.repository.revision` | a817270f13b4ef7a9b1d2895118981120546b3a2 | ff85958196cb013e8e14c1055f067319b07e3299 |
| `meta.views[id=single].label` | Single process | In-process adapter |
| `meta.views[id=single].note` | Role all keeps the same seam but calls the engine directly. | Without a remote declarative URL, the service invokes the engine through its in-process adapter. |

Retain: `components[id=device].label`, `components[id=device].sublabel`, `components[id=proxyclient].sources[0].label`, `components[id=hop].sources[0].label`, `components[id=inproc].sources[0].label`, `components[id=proxyserver].sources[0].label`, `connections[id=device-mdm].label`, `connections[id=mdm-proxyclient].label`, `connections[id=mdm-inproc].label`, `connections[id=client-hop].label`, `connections[id=hop-server].label`, `connections[id=server-engine].label`, `connections[id=inproc-engine].label`, `meta.title`, `meta.views[id=split].label`, `meta.views[id=split].note`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Declarative Management check-in](https://developer.apple.com/documentation/devicemanagement/declarative-management), [RFC 9440 — Client-Cert HTTP Header Field](https://www.rfc-editor.org/rfc/rfc9440).

### schema-generation

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | Generated schema files come from make generate; support code and removal policy are handwritten | make generate emits Go types, registries, support tables, tests, and source provenance from the pinned Apple schemas. |
| `cards[0].items[1]` | GENERATED_FROM.json is emitted too, so the record of which Apple commit the tree came from cannot drift from the tree | Handwritten code applies runtime policy and wraps generated data. Generation does not establish device compatibility by itself. |
| `cards[0].items[2]` | make verify regenerates and fails if the checked-in output differs | [remove field] |
| `cards[0].title` | Generated output | Generated and handwritten responsibilities |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | EXPORTED_IDENTIFIERS.lock records every exported name | make verify regenerates the outputs and detects changes against the checked-in files. EXPORTED_IDENTIFIERS.lock guards public naming stability. |
| `cards[1].items[1]` | A regeneration that would rename a type fails the build instead of breaking callers silently | GENERATED_FROM.json records the Apple revision and schema digest used for generation. |
| `cards[1].title` | The rename guard | Verification |
| `cards[2].dot` | [new field] | slate |
| `cards[2].items[0]` | [new field] | Apple device-management schema repository — https://github.com/apple/device-management |
| `cards[2].items[1]` | [new field] | Go Modules Reference — https://go.dev/ref/mod |
| `cards[2].title` | [new field] | Public documentation |
| `nodes[id=yaml].label` | device-management | Apple device-management schemas |
| `nodes[id=yaml].sublabel` | pinned submodule | Read the pinned upstream revision |
| `nodes[id=loader].label` | internal/schemagen | Schema generator |
| `nodes[id=loader].sublabel` | strict loader and model | Load and validate the YAML model |
| `nodes[id=types].label` | types.gen.go | Generated Go types |
| `nodes[id=types].sublabel` | typed messages | Represent Apple messages and payloads |
| `nodes[id=registry].label` | registry.gen.go | Generated registries and support data |
| `nodes[id=registry].sublabel` | identifier to type | Map identifiers and platform availability |
| `nodes[id=nameslock].label` | EXPORTED_IDENTIFIERS.lock | Exported-name compatibility lock |
| `nodes[id=nameslock].sublabel` | rename guard | Detect unintended public identifier changes |
| `nodes[id=conformance].label` | conformance tests | Generated conformance tests |
| `nodes[id=conformance].sublabel` | round-trip every type | Exercise encoded values and validation |
| `nodes[id=generatedfrom].label` | GENERATED_FROM.json | Generation provenance |
| `nodes[id=generatedfrom].sublabel` | commit · versions · digest | Record source commit and schema digest |

Retain: `flows[0].label`, `flows[1].label`, `flows[2].label`, `flows[3].label`, `flows[4].label`, `flows[5].label`, `meta.title`, `meta.views[id=emit].label`, `meta.views[id=emit].note`, `meta.views[id=guard].label`, `meta.views[id=guard].note`, `nodes[id=yaml].tag`, `stages[0].label`, `stages[1].label`, `stages[2].label`, `stages[3].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Apple device-management schema repository](https://github.com/apple/device-management), [Go Modules Reference](https://go.dev/ref/mod).

### request-decode

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | plist is the library's only point of contact with property lists | The transport decodes a check-in message or command response and extracts the device certificate from an enabled identity source. |
| `cards[0].items[1]` | Input has a byte bound; XML decoding also limits nesting depth | Decoding is not enrollment authorization. The service then applies certificate status, identity, account, and replacement policy. |
| `cards[0].title` | One codec boundary | Two outputs from the transport |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | The certificate comes from TLS, a proxy header, or the detached Mdm-Signature | Native TLS, a trusted proxy header, and Mdm-Signature are separate certificate sources. Header trust depends on the deployment boundary. |
| `cards[1].items[1]` | Whichever the source, it is pinned to the enrollment and checked on every later message | The signature verifier needs the original body bytes. Size limits apply before untrusted input reaches deeper processing. |
| `cards[1].title` | Identity | Identity sources |
| `cards[2].items[0]` | schema/support screens outbound commands, not decoded check-ins | Command platform and channel checks run when service.Core enqueues a command. They do not validate incoming check-in messages. |
| `cards[2].items[1]` | That screen is in flow-command-delivery, on service.Core.Enqueue | [remove field] |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Check-in — https://developer.apple.com/documentation/devicemanagement/check-in |
| `cards[3].items[1]` | [new field] | RFC 9440 — Client-Cert HTTP Header Field — https://www.rfc-editor.org/rfc/rfc9440 |
| `cards[3].items[2]` | [new field] | MDM payload — https://developer.apple.com/documentation/devicemanagement/mdm |
| `cards[3].title` | [new field] | Public documentation |
| `meta.views[id=body].label` | Body | Decode the device message |
| `meta.views[id=body].note` | A bounded plist decode, then a typed check-in message. | A bounded property-list decoder produces a typed check-in message or command response. |
| `meta.views[id=identity].label` | Identity | Extract and authorize identity |
| `meta.views[id=identity].note` | The identity certificate is recovered from the detached CMS signature. | TLS, a trusted proxy, or a verified Mdm-Signature supplies the certificate. Service policy then authorizes it. |
| `nodes[id=body].label` | PUT body | Device request body |
| `nodes[id=body].sublabel` | XML or binary plist | XML or binary property list |
| `nodes[id=signature].label` | Mdm-Signature | Mdm-Signature header |
| `nodes[id=signature].sublabel` | detached CMS | Detached CMS signature over the body |
| `nodes[id=plist].label` | plist.Decoder | Property-list decoder |
| `nodes[id=plist].sublabel` | bytes; XML depth bound | Bound input size and XML nesting |
| `nodes[id=cms].label` | cms.VerifyHeader | Verify signed request |
| `nodes[id=cms].sublabel` | skew tolerance | Validate signature and signing-time policy |
| `nodes[id=checkin].label` | mdm.Checkin | Typed check-in message |
| `nodes[id=checkin].sublabel` | one of nine messages | MessageType selects a check-in structure |
| `nodes[id=response].label` | mdm.Response | Typed command response |
| `nodes[id=response].sublabel` | command result | Idle or a result for CommandUUID |
| `nodes[id=cert].label` | identity certificate | Extracted device certificate |
| `nodes[id=cert].sublabel` | pinned per enrollment | Passed to service authorization |

Retain: `cards[2].title`, `flows[0].label`, `flows[1].label`, `flows[2].label`, `flows[3].label`, `flows[4].label`, `meta.title`, `nodes[id=body].tag`, `nodes[id=signature].tag`, `stages[0].label`, `stages[1].label`, `stages[2].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Check-in](https://developer.apple.com/documentation/devicemanagement/check-in), [RFC 9440 — Client-Cert HTTP Header Field](https://www.rfc-editor.org/rfc/rfc9440), [MDM payload](https://developer.apple.com/documentation/devicemanagement/mdm).

### reference-server

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | mdm serves devices and forwards DDM check-ins when DM_DDM_URL is set | dmserver and embedded bench scenarios use server/internal/runtime. The runtime builds the application, opens the listener, and starts configured workers. |
| `cards[0].items[1]` | ddm mounts no /mdm; all runs everything in one process | TLS is optional for the listener; reverse-proxy deployments remain supported. Readiness is exposed for supervisors and acceptance checks. |
| `cards[0].items[2]` | Every role builds the engine and runs the notifier | [remove field] |
| `cards[0].title` | Roles decide what mounts | Shared runtime |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | Revocation requires persistent CA keys and explicit CRL/OCSP lifetimes | mdm and all expose device enrollment and /mdm. A remote declarative URL selects forwarding; ddm exposes the internal declarative adapter. |
| `cards[1].items[1]` | DM_RATE_LIMITS enables selected route quotas; both controls default off | Enrollment discovery and trust routes are mounted only when enrollment is configured. App push uses its own clients and credential store. |
| `cards[1].title` | Optional security services | Role-specific services |
| `cards[2].dot` | [new field] | amber |
| `cards[2].items[0]` | [new field] | Stop accepting requests and drain in-flight HTTP work, then stop and await workers. Close application resources and APNs clients afterward. |
| `cards[2].items[1]` | [new field] | The runtime preserves the first failure and bounds shutdown time. |
| `cards[2].title` | [new field] | Ordered shutdown |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Go command: Test packages — https://pkg.go.dev/cmd/go#hdr-Test_packages |
| `cards[3].items[1]` | [new field] | Go Modules Reference — https://go.dev/ref/mod |
| `cards[3].title` | [new field] | Public documentation |
| `edges[id=r3].label` | shared pool | Provide configured stores |
| `edges[id=r5].label` | start | Register worker dependencies |
| `meta.title` | Reference Server: Build and Serve | Reference Server: Configure, Serve, and Shut Down |
| `meta.views[id=build].label` | Build | Build the application |
| `meta.views[id=build].note` | Validate the configuration, open one database, then wire the graph. | Validate configuration, open the chosen stores, and assemble services without starting worker loops. |
| `meta.views[id=serve].label` | Serve | Run and drain |
| `meta.views[id=serve].note` | The mux and workers depend on the configured role and enabled services. | The shared runtime serves HTTP or TLS, runs workers, and drains requests before stopping workers and closing resources. |
| `nodes[id=env].label` | ParseEnv | Load configuration |
| `nodes[id=env].sublabel` | then flags override | Environment values, then CLI overrides |
| `nodes[id=role].label` | Role | Select server role |
| `nodes[id=role].sublabel` | mdm · ddm · all | mdm, ddm, or all |
| `nodes[id=openstorage].label` | openStorage | Open configured storage |
| `nodes[id=openstorage].sublabel` | one *sql.DB | Memory or shared SQL connection pool |
| `nodes[id=wire].label` | App.wire | Build the application |
| `nodes[id=wire].sublabel` | engine, push, service | Assemble services and route handlers |
| `nodes[id=mux].label` | http.ServeMux | Registered HTTP routes |
| `nodes[id=mux].sublabel` | routes for this role | Determined by role and configuration |
| `nodes[id=workers].label` | App.Run | Background worker loops |
| `nodes[id=workers].sublabel` | configured workers | Started by the shared runtime |
| `nodes[id=configerr].label` | ErrConfig | Invalid configuration |
| `nodes[id=configerr].sublabel` | fails at build | Stop startup with a configuration error |

Retain: `edges[id=r1].label`, `edges[id=r2].label`, `edges[id=r4].label`, `edges[id=r6].label`, `lanes[id=config].label`, `lanes[id=build].label`, `lanes[id=run].label`, `lanes[id=gate].label`, `meta.legend.entries.backend.label`, `meta.legend.entries.database.label`, `meta.legend.entries.security.label`, `phases[id=p1].label`, `phases[id=p2].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Go command: Test packages](https://pkg.go.dev/cmd/go#hdr-Test_packages), [Go Modules Reference](https://go.dev/ref/mod).

### enrollment-paths

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | Both end at the same enroll.Profile, signed by the enrollment CA | Automated Device Enrollment, account-driven enrollment, and profile-based enrollment describe how enrollment begins. SCEP and ACME describe how the device obtains its identity. |
| `cards[0].items[1]` | An old OS and an unrouted model family are refused before any profile is built | Account-driven discovery selects User Enrollment (mdm-byod) or Device Enrollment (mdm-adde). The reference server’s OTA delivery supports SCEP only and separates identity bootstrap from MDM profile delivery. |
| `cards[0].title` | Enrollment profile issuance | Enrollment type and identity method |
| `cards[1].dot` | [new field] | emerald |
| `cards[1].items[0]` | [new field] | /MDMServiceConfig advertises the ADE URL and trust-anchor URL. A trust-profile URL is included only when HTTPS anchors are configured. |
| `cards[1].items[1]` | [new field] | Account-driven discovery uses /.well-known/com.apple.remotemanagement. HTTPS trust anchors are configured independently of identity issuers. |
| `cards[1].title` | [new field] | Discovery and trust |
| `cards[2].dot` | [new field] | amber |
| `cards[2].items[0]` | [new field] | Profiles use stable stored identifiers and advertise per-user connections. The device obtains its certificate, then checks in and supplies push details. |
| `cards[2].items[1]` | [new field] | Controlled replacement updates an existing enrollment through a separate authorized handshake; see the companion sequence. |
| `cards[2].title` | [new field] | Common outcome |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Deploying device management enrollment profiles — https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles |
| `cards[3].items[1]` | [new field] | MDM payload — https://developer.apple.com/documentation/devicemanagement/mdm |
| `cards[3].items[2]` | [new field] | Onboarding users with account-driven enrollment — https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment |
| `cards[3].items[3]` | [new field] | MachineInfo — https://developer.apple.com/documentation/devicemanagement/machineinfo |
| `cards[3].title` | [new field] | Public documentation |
| `edges[id=p1].label` | POST | Signed MachineInfo request |
| `edges[id=p4].label` | personalised | Build the authorized profile |
| `edges[id=p8].label` | finalized | Build the authorized profile |
| `meta.title` | Enrollment Paths and Their Gates | Enrollment Methods and the Shared Identity Flow |
| `nodes[id=adedevice].label` | ADE device | Automated Device Enrollment |
| `nodes[id=adedevice].sublabel` | assigned by DEP | Device assigned to the management service |
| `nodes[id=machineinfo].label` | MachineInfo | Verify MachineInfo |
| `nodes[id=machineinfo].sublabel` | CMS verified | Validate the signed device information |
| `nodes[id=updategate].label` | Update gate | Apply software-update policy |
| `nodes[id=updategate].sublabel` | configured minimum OS | Require a configured compatible OS |
| `nodes[id=adeprofile].label` | web view or token | Complete ADE authentication |
| `nodes[id=adeprofile].sublabel` | OIDC, then finish | Direct profile or configured web sign-in |
| `nodes[id=byoddevice].label` | Account enrollment | Account-driven enrollment |
| `nodes[id=byoddevice].sublabel` | Managed Apple Account | User signs in with a Managed Apple Account |
| `nodes[id=discovery].label` | discovery | Account-driven discovery |
| `nodes[id=discovery].sublabel` | model-family route | Select User or Device Enrollment |
| `nodes[id=challenge].label` | 401 challenge | Authentication required |
| `nodes[id=challenge].sublabel` | as-web or oauth2 | Return the configured sign-in method |
| `nodes[id=adprofile].label` | bearer accepted | Validate access token |
| `nodes[id=adprofile].sublabel` | reusable access token | Authorize this account enrollment |
| `nodes[id=profile].label` | enroll.Profile | Build enrollment profile |
| `nodes[id=profile].sublabel` | signed by the CA | MDM payload plus SCEP or ACME identity |
| `nodes[id=refused].label` | Refused | Enrollment cannot proceed |
| `nodes[id=refused].sublabel` | 403, before any profile | Return the applicable admission error |

Retain: `edges[id=p2].label`, `edges[id=p3].label`, `edges[id=p5].label`, `edges[id=p6].label`, `edges[id=p7].label`, `edges[id=p9].label`, `edges[id=p10].label`, `lanes[id=ade].label`, `lanes[id=account].label`, `lanes[id=out].label`, `lanes[id=gate].label`, `meta.legend.entries.backend.label`, `meta.legend.entries.external.label`, `meta.legend.entries.security.label`, `meta.views[id=ade].label`, `meta.views[id=ade].note`, `meta.views[id=account].label`, `meta.views[id=account].note`, `meta.views[id=refused].label`, `meta.views[id=refused].note`, `phases[id=p1].label`, `phases[id=p2].label`, `phases[id=p3].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Deploying device management enrollment profiles](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles), [MDM payload](https://developer.apple.com/documentation/devicemanagement/mdm), [Onboarding users with account-driven enrollment](https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment), [MachineInfo](https://developer.apple.com/documentation/devicemanagement/machineinfo).

### test-harness

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | The simulator speaks MDM, DDM, ADE, account-driven, user channel and ACME | Unit and backend-contract tests check code behaviour. Embedded and process scenarios exercise the maintained reference server and shared catalogue. |
| `cards[0].items[1]` | Simulator assertions verify modeled exchanges; hardware compatibility needs device testing | Simulated scenarios use local fixtures. Live app receipt and device inventory checks require real registration, credentials, and hardware. |
| `cards[0].title` | Simulator coverage | Different kinds of evidence |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | 95% overall and per non-exempt package; every exported function has a failing-path test | The gate merges emitted profiles and requires 95% overall and per non-exempt package. Fuzz smoke is a separate check. |
| `cards[1].items[1]` | A Failing wrapper injects an error by method name so refusal paths are covered too | A diagram cannot establish a passing coverage result. PR 14’s validation record reports remaining per-package failures; use a fresh report for current status. |
| `cards[1].title` | The gate | Coverage is a threshold |
| `cards[2].dot` | [new field] | amber |
| `cards[2].items[0]` | [new field] | The executable scenario catalogue defines supported modes. bench-docs-check verifies that the documentation matches it. |
| `cards[2].title` | [new field] | Catalogue consistency |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Go command: Test packages — https://pkg.go.dev/cmd/go#hdr-Test_packages |
| `cards[3].title` | [new field] | Public documentation |
| `meta.views[id=scenarios].label` | Scenarios | Scenario evidence |
| `meta.views[id=scenarios].note` | A real server, the device simulator as the client, and fakes for every Apple service. | Embedded and process scenarios share the maintained catalogue. Live scenarios require real credentials and device evidence. |
| `nodes[id=unit].label` | make test | Unit tests |
| `nodes[id=unit].sublabel` | race detector | Race detector and shuffled execution |
| `nodes[id=fuzz].label` | fuzz smoke | Fuzz smoke tests |
| `nodes[id=fuzz].sublabel` | cbor · plist · jose | Exercise parser and protocol inputs |
| `nodes[id=contract].label` | contract suites | Storage contract suites |
| `nodes[id=contract].sublabel` | storagetest · ddmtest | Verify shared backend behaviour |
| `nodes[id=backends].label` | every backend | Memory and SQL backends |
| `nodes[id=backends].sublabel` | inmem · sqlite · pg · mysql | SQLite, PostgreSQL, and MySQL |
| `nodes[id=simulator].label` | simulator.Device | Simulated Apple device |
| `nodes[id=simulator].sublabel` | no hardware | Exercises modeled protocol exchanges |
| `nodes[id=e2e].label` | e2e scenarios | Embedded runtime scenarios |
| `nodes[id=e2e].sublabel` | sqlite, postgres or inmem | Shared scenarios plus detailed regressions |
| `nodes[id=coverage].label` | 95% gate | Coverage policy |
| `nodes[id=coverage].sublabel` | overall and per package | 95% overall and per non-exempt package |

Retain: `edges[id=t1].label`, `edges[id=t2].label`, `edges[id=t3].label`, `edges[id=t4].label`, `edges[id=t5].label`, `edges[id=t6].label`, `lanes[id=unit].label`, `lanes[id=storage].label`, `lanes[id=scenario].label`, `meta.legend.entries.backend.label`, `meta.legend.entries.database.label`, `meta.legend.entries.security.label`, `meta.title`, `meta.views[id=contract].label`, `meta.views[id=contract].note`, `phases[id=p1].label`, `phases[id=p2].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Go command: Test packages](https://pkg.go.dev/cmd/go#hdr-Test_packages).

### flow-dep-sync-assign

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | The server generates a keypair and hands out a certificate | The operator uploads the server’s public certificate to the Apple management portal and imports the encrypted .p7m server token. |
| `cards[0].items[1]` | The operator uploads it at the portal and downloads an encrypted .p7m | The dep client uses the resulting OAuth 1.0a credentials; this token is unrelated to an MDM push certificate. |
| `cards[0].title` | Token exchange | Server-token setup |
| `cards[1].items[0]` | The first pass fetches; later passes sync by cursor | Inventory starts with a full fetch, then follows incremental cursors. An expired cursor starts a new full fetch. |
| `cards[1].items[1]` | An EXPIRED_CURSOR is handled by refetching rather than failing | Profile assignment configures what enrollment profile Apple directs the device to use; the device must still complete enrollment. |
| `cards[1].title` | Syncing | Recurring reconciliation |
| `cards[2].dot` | [new field] | slate |
| `cards[2].items[0]` | [new field] | Device assignment — https://developer.apple.com/documentation/devicemanagement/device-assignment |
| `cards[2].title` | [new field] | Public documentation |
| `meta.title` | DEP Token Exchange, Sync and Assignment | Automated Device Enrollment: Token Setup and Profile Assignment |
| `meta.views[id=onboard].label` | Onboarding | Set up API credentials |
| `meta.views[id=onboard].note` | The token PKI exchange is the only step that needs a human at the portal. | Upload the server’s public certificate to the Apple portal, then import the encrypted server token. |
| `meta.views[id=steady].label` | Steady state | Reconcile inventory and profiles |
| `meta.views[id=steady].note` | The DEP worker syncs device state and reconciles profile assignments. | The dep workers synchronize Apple inventory and reconcile enrollment-profile assignments on repeated runs. |
| `nodes[id=keypair].label` | Token PKI | Create server-token keypair |
| `nodes[id=keypair].sublabel` | keypair generated | Export the public certificate |
| `nodes[id=portal].label` | Portal upload | Apple management portal |
| `nodes[id=portal].sublabel` | returns a .p7m | Upload certificate; download server token |
| `nodes[id=importtoken].label` | Import token | Import server token |
| `nodes[id=importtoken].sublabel` | sealed at rest | Decrypt and store API credentials |
| `nodes[id=sync].label` | dep.Syncer | Synchronize device inventory |
| `nodes[id=sync].sublabel` | RunOnce | Fetch devices, then follow change cursors |
| `nodes[id=assign].label` | dep.Assigner | Reconcile profile assignments |
| `nodes[id=assign].sublabel` | RunOnce | Compare desired and recorded state |
| `nodes[id=depapi].label` | Device sync | Apple device inventory API |
| `nodes[id=depapi].sublabel` | cursor paging | Fetch and incremental sync operations |
| `nodes[id=assignapi].label` | Assign profile | Apple profile-assignment API |
| `nodes[id=assignapi].sublabel` | POST or PUT | Assign profile and read back device state |
| `nodes[id=backoff].label` | 429 ErrBackoff | API rate limit |
| `nodes[id=backoff].sublabel` | the account waits | Delay account retries after HTTP 429 |

Retain: `edges[id=e1].label`, `edges[id=e2].label`, `edges[id=e3].label`, `edges[id=e4].label`, `edges[id=e6].label`, `edges[id=e7].label`, `edges[id=e10].label`, `lanes[id=operator].label`, `lanes[id=server].label`, `lanes[id=apple].label`, `lanes[id=retry].label`, `meta.legend.entries.backend.label`, `meta.legend.entries.external.label`, `meta.legend.entries.security.label`, `nodes[id=depapi].tag`, `phases[id=onboarding].label`, `phases[id=background].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Device assignment](https://developer.apple.com/documentation/devicemanagement/device-assignment).

### flow-ade-enrollment

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | POST is the token lane and finishes immediately | The direct POST returns an enrollment profile after admission. GET enters web authentication only when WebAuth is configured. |
| `cards[0].items[1]` | GET is the configuration_web_url lane and resumes after OIDC | The identity provider redirects the device’s browser back; the server verifies the authentication result before issuing the profile. |
| `cards[0].title` | ADE entry points | Alternative entry paths |
| `cards[1].items[0]` | An old OS is refused with ErrorCodeCodeSoftwareUpdateRequired | The profile’s HTTPS anchors are independent of its SCEP or ACME identity issuer. Stable identifiers support later controlled replacement. |
| `cards[1].items[1]` | The catalogue is cached with a TTL and serves last-good on a refresh failure | The device obtains its identity certificate before Authenticate. TokenUpdate supplies push details and enables the enrollment. |
| `cards[1].title` | The update gate | Trust and activation |
| `cards[2].dot` | [new field] | amber |
| `cards[2].items[0]` | [new field] | The ADE handler applies its configured policy and may return Apple’s software-update-required error. The catalogue supplies version data only. |
| `cards[2].title` | [new field] | Update-required outcome |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | MachineInfo — https://developer.apple.com/documentation/devicemanagement/machineinfo |
| `cards[3].items[1]` | [new field] | Authenticating through web views — https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views |
| `cards[3].items[2]` | [new field] | ErrorCodeSoftwareUpdateRequired — https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired |
| `cards[3].items[3]` | [new field] | Deploying device management enrollment profiles — https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles |
| `cards[3].title` | [new field] | Public documentation |
| `messages[id=post].label` | signed MachineInfo | Submit signed MachineInfo |
| `messages[id=post].note` | CMS-verified against Apple's device CAs before anything else runs | Direct POST and web-view GET are alternative entry paths; the configured audit mode can relax signer rejection. |
| `messages[id=gate].label` | latest version for this model | If needed, resolve latest software |
| `messages[id=tokenupdate].label` | TokenUpdate enables the enrollment | Send APNs token, topic, and PushMagic |
| `messages[id=gate-back].label` | proceed, or 403 update required | Return available software version |
| `messages[id=gate-back].note` | refused with ErrorCodeCodeSoftwareUpdateRequired (com.apple.softwareupdate.required) | The ADE handler decides whether to send a software-update-required response to the device. |
| `messages[id=begin].label` | 302 to the web view | Web-view GET only: redirect to sign-in |
| `messages[id=auth].label` | authorization code, PKCE, nonce | Browser sign-in with OIDC |
| `messages[id=callback].from` | idp | device |
| `messages[id=callback].label` | /enroll/oidc/callback | Browser returns authorization code |
| `messages[id=callback].note` | id_token verified, then Resume(serial) picks the enrollment back up | The server exchanges the code and verifies the ID token before resuming the stored enrollment. |
| `messages[id=build].label` | email/SERIAL as the subject | Build the authorized enrollment profile |
| `messages[id=signed].label` | x-apple-aspen-config | Return signed enrollment profile |
| `messages[id=signed].note` | carries a SCEP or ACME payload; the device obtains its identity before check-in | Contains MDM settings, SCEP or ACME identity configuration, and explicitly configured HTTPS trust. |
| `messages[id=pinned].label` | certificate pinned | HTTP 200; enrollment identity recorded |
| `meta.views[id=verify].label` | Verify the device | Verify device information |
| `meta.views[id=verify].note` | MachineInfo is CMS-signed by Apple; the update gate refuses an old OS. | The handler verifies signed MachineInfo and applies the configured admission and software-update policy. |
| `meta.views[id=authenticate].label` | Authenticate the user | Optional web authentication |
| `meta.views[id=authenticate].note` | The web view resumes the enrollment and personalises the profile. | Only the configured web-view path signs the user in before resuming enrollment; direct POST returns the profile after admission. |
| `participants[id=device].label` | Device | Apple device |
| `participants[id=device].sublabel` | assigned by DEP | Starts Automated Device Enrollment |
| `participants[id=ade].label` | enroll/ade | ADE enrollment endpoint |
| `participants[id=ade].sublabel` | /enroll/ade | Verify device information and admission |
| `participants[id=gdmf].label` | gdmf | Software catalogue |
| `participants[id=gdmf].sublabel` | software lookup | Resolve an optional latest-version target |
| `participants[id=idp].label` | Identity provider | User identity provider |
| `participants[id=idp].sublabel` | OIDC | OpenID Connect authentication |
| `participants[id=profile].label` | enroll.Profile | Enrollment profile builder |
| `participants[id=profile].sublabel` | built and signed | Stable identifiers and configured trust |
| `participants[id=core].label` | service.Core | MDM check-in service |
| `participants[id=core].sublabel` | check-in | Associate identity and enable enrollment |

Retain: `messages[id=authenticate].label`, `meta.title`, `segments[0].label`, `segments[1].label`, `segments[2].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [MachineInfo](https://developer.apple.com/documentation/devicemanagement/machineinfo), [Authenticating through web views](https://developer.apple.com/documentation/devicemanagement/authenticating-through-web-views), [ErrorCodeSoftwareUpdateRequired](https://developer.apple.com/documentation/devicemanagement/errorcodesoftwareupdaterequired), [Deploying device management enrollment profiles](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles).

### flow-account-driven-enrollment

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | DM_DISCOVERY selects mdm-byod or mdm-adde by model family | The well-known response advertises an enrollment type and service URL. DM_DISCOVERY selects the route by model family. |
| `cards[0].items[1]` | Legacy query-string enrollment credentials are rejected | An unauthenticated request receives the configured authentication challenge. The resulting access token remains reusable until expiry or invalidation. |
| `cards[0].title` | Discovery | Discovery and authentication |
| `cards[1].items[0]` | Trusted issuance binds certificate, account and enrollment reference | Trusted SCEP or ACME issuance registers the certificate/account association. Service hooks reserve and confirm that association around enrollment persistence. |
| `cards[1].items[1]` | Reusable bearer; macOS device channel omits it; known sessions can reauthenticate | macOS device-channel requests omit the bearer token; applicable account channels require it and may receive a reauthentication challenge. |
| `cards[1].title` | Certificate and bearer checks | Identity and channel checks |
| `cards[2].dot` | [new field] | amber |
| `cards[2].items[0]` | [new field] | After obtaining its certificate, the device sends Authenticate and TokenUpdate. The latter stores push details and enables the channel. |
| `cards[2].title` | [new field] | Completed enrollment |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Onboarding users with account-driven enrollment — https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment |
| `cards[3].items[1]` | [new field] | Deploying device management enrollment profiles — https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles |
| `cards[3].items[2]` | [new field] | MDM payload — https://developer.apple.com/documentation/devicemanagement/mdm |
| `cards[3].title` | [new field] | Public documentation |
| `messages[id=wellknown].label` | GET .well-known | Discover the enrollment service |
| `messages[id=wellknown].note` | routed by model-family and user-identifier | GET /.well-known/com.apple.remotemanagement; this server’s configured router selects by model family. |
| `messages[id=servers].label` | Version + BaseURL | Return enrollment type and BaseURL |
| `messages[id=hook].label` | reserve, persist, confirm | HTTP 200 with an empty body |
| `messages[id=hook].note` | successful storage confirms the reserved enrollment identifier; invalid bearer can trigger 401 | Internal hooks reserve the association, persist enrollment, then confirm the binding. |
| `messages[id=build].label` | ManagedAppleID/Product | Build profile for the authenticated account |
| `messages[id=finalize].label` | Profile with issuance reference | Return enrollment profile |
| `messages[id=finalize].note` | trusted issuer registers certificate/account association before returning the certificate | The device must obtain its SCEP or ACME identity before starting MDM check-in. |
| `meta.views[id=discovery].label` | Discovery | Discover the enrollment method |
| `meta.views[id=discovery].note` | The organisation domain routes the device to a BYOD or ADDE enrollment URL. | The organization domain selects account-driven User Enrollment (mdm-byod) or Device Enrollment (mdm-adde). |
| `participants[id=device].label` | Device | Apple device and user |
| `participants[id=device].sublabel` | Managed Apple Account | Sign in with a Managed Apple Account |
| `participants[id=discovery].label` | enroll/discovery | Account-driven discovery |
| `participants[id=discovery].sublabel` | /.well-known | Organization’s well-known endpoint |
| `participants[id=account].label` | accountdriven | Account enrollment endpoint |
| `participants[id=account].sublabel` | /enroll/mdm-byod | mdm-byod or mdm-adde |
| `participants[id=idp].label` | Enrollment auth | Enrollment authentication |
| `participants[id=idp].sublabel` | OIDC → access token | Web or OAuth flow backed by OIDC |
| `participants[id=profile].label` | Profile / issuer | Profile builder and identity issuer |
| `participants[id=profile].sublabel` | SCEP or ACME | SCEP or ACME device identity |
| `participants[id=core].label` | service.Core | MDM check-in service |
| `participants[id=core].sublabel` | check-in | Validate the account and device identity |

Retain: `messages[id=post1].label`, `messages[id=post1].note`, `messages[id=challenge].label`, `messages[id=webauth].label`, `messages[id=token].label`, `messages[id=post2].label`, `messages[id=post2].note`, `messages[id=checkin].label`, `messages[id=checkin].note`, `meta.title`, `meta.views[id=challenge].label`, `meta.views[id=challenge].note`, `segments[0].label`, `segments[1].label`, `segments[2].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Onboarding users with account-driven enrollment](https://developer.apple.com/documentation/devicemanagement/onboarding-users-with-account-driven-enrollment), [Deploying device management enrollment profiles](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles), [MDM payload](https://developer.apple.com/documentation/devicemanagement/mdm).

### flow-command-delivery

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | service.Core asks schema/commands whether the command applies to that OS, version and channel | The service validates each target and queues commands for supported, enabled enrollments. The caller separately requests the MDM wake notification. |
| `cards[0].items[1]` | Unsupported targets are skipped with ErrUnsupportedTarget | The device initiates the command request. The HTTP response carries the command; APNs does not carry it. |
| `cards[0].title` | Target validation | Queue, wake, and collect |
| `cards[1].items[0]` | Stores the result of the previous command | An incoming non-Idle status is stored before selecting the next eligible command. An empty HTTP 200 ends the exchange when no command is available. |
| `cards[1].items[1]` | Takes the next pending command and marks it sent | Controlled enrollment-profile replacement intercepts the command connection and uses its staged InstallProfile record. See the replacement companion. |
| `cards[1].title` | Connect result and delivery | Results and special paths |
| `cards[2].dot` | [new field] | amber |
| `cards[2].items[0]` | [new field] | Use the stored command result or device evidence to establish completion. APNs acceptance and a queue state of sent are earlier steps. |
| `cards[2].title` | [new field] | Observing completion |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Sending MDM commands to a device — https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device |
| `cards[3].items[1]` | [new field] | Handling NotNow status responses — https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses |
| `cards[3].title` | [new field] | Public documentation |
| `messages[id=enqueue].label` | Enqueue(ids, cmd) | Queue command for selected enrollments |
| `messages[id=target-read].label` | Get(id) for each target | Read OS, version, and channel |
| `messages[id=ack].label` | Acknowledged + CommandUUID | Report status with CommandUUID |
| `messages[id=queue].label` | CommandQueue.Enqueue | Store command for supported targets |
| `messages[id=notify].label` | Notify(ids) | Request MDM wake for queued targets |
| `messages[id=wake].label` | wake | Prompt a device connection |
| `messages[id=connect].label` | PUT /mdm | PUT /mdm: Idle or previous result |
| `messages[id=store-result].label` | StoreResult(previous) | If non-Idle, store the previous result |
| `messages[id=next].label` | Next(id, skipNotNow) | Select eligible command and mark sent |
| `messages[id=deliver].label` | 200 application/xml | Return command, or empty HTTP 200 |
| `meta.views[id=wake].label` | Wake | Prompt a device connection |
| `meta.views[id=wake].note` | The server cannot reach the device; it asks APNs to wake it. | APNs prompts the device to contact the MDM server. Its acceptance response does not prove command execution. |
| `meta.views[id=collect].label` | Collect and report | Collect results and deliver |
| `meta.views[id=collect].note` | One connect stores the previous result and takes the next command. | A device poll records a non-Idle result, then receives the next eligible command or an empty successful response. |
| `participants[id=admin].label` | Admin API | Authorized administrator |
| `participants[id=admin].sublabel` | enqueue, then push | Queues a command and requests a wake |
| `participants[id=core].label` | service.Core | MDM command service |
| `participants[id=core].sublabel` | Enqueue · Connect | Validate targets and process device polls |
| `participants[id=store].label` | storage.Store | Enrollment and command storage |
| `participants[id=store].sublabel` | queue · pins · tokens | Track queued commands and results |
| `participants[id=push].label` | pushnotify.Notifier | MDM wake notifier |
| `participants[id=push].sublabel` | coalesced | Coalesce repeated wake requests |
| `participants[id=apns].label` | APNs | Apple Push Notification service |
| `participants[id=apns].sublabel` | HTTP/2 | Prompts the device to connect |
| `participants[id=device].label` | Device | Enrolled Apple device |
| `participants[id=device].sublabel` | enrolled | Requests and executes commands |

Retain: `messages[id=notify].note`, `messages[id=apns-post].label`, `meta.title`, `meta.views[id=enqueue].label`, `meta.views[id=enqueue].note`, `segments[0].label`, `segments[1].label`, `segments[2].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device), [Handling NotNow status responses](https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses).

### flow-ddm-sync

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | The device never gets a DDM URL: every call is a check-in message to /mdm | The engine commits changes to assigned declarations and persistent change records. The notifier queues a synchronization command through service.Core. |
| `cards[0].items[1]` | tokens, declaration-items, declaration/{kind}/{identifier}, status | A configured pusher prompts the device to poll. The device receives the command from the MDM server’s response. |
| `cards[0].title` | Four endpoints, one URL | Server-side change notification |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | The engine writes persistent change rows for the notifier | This server carries Endpoint values inside DeclarativeManagement check-ins to /mdm. The adapter passes each operation to the engine. |
| `cards[1].items[1]` | Its Enqueuer is service.Core, so a DDM kick runs the hooks and the target screen like any command | The device compares synchronization tokens, fetches the manifest and changed declarations as needed, and reports status separately. |
| `cards[1].title` | Change to command | Device synchronization |
| `cards[2].dot` | [new field] | amber |
| `cards[2].items[0]` | [new field] | Ordinary enrollment reset or checkout clears declarative state through the service hook. Controlled profile replacement avoids that reset. |
| `cards[2].title` | [new field] | State preservation |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Integrating declarative management — https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management |
| `cards[3].items[1]` | [new field] | Declarative Management check-in — https://developer.apple.com/documentation/devicemanagement/declarative-management |
| `cards[3].items[2]` | [new field] | Declarations — https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations |
| `cards[3].title` | [new field] | Public documentation |
| `messages[id=put].label` | PUT /declarations | Create or update an assigned declaration |
| `messages[id=changerows].label` | declaration + change rows | Commit declaration and change records |
| `messages[id=status].label` | status report | Check-in: status report |
| `messages[id=status].to` | engine | core |
| `messages[id=kick].label` | Kick | Wake the notifier |
| `messages[id=pending].label` | PendingChanges | Read pending enrollment changes |
| `messages[id=tokensread].label` | Tokens(id) | Build current synchronization tokens |
| `messages[id=enqueue].label` | Enqueue, dedupe key ddm | Queue DeclarativeManagement command |
| `messages[id=deliver].label` | delivered on next connect | Return command on a device poll |
| `messages[id=tokens].label` | tokens | Check-in: tokens |
| `messages[id=tokens].to` | engine | core |
| `messages[id=items].label` | declaration-items | Check-in: declaration-items |
| `messages[id=items].to` | engine | core |
| `messages[id=fetch].label` | declaration/{kind}/{id} | Check-in: declaration/{kind}/{identifier} |
| `messages[id=fetch].to` | engine | core |
| `meta.title` | Declarative Management Sync | Declarative Device Management: Change, Synchronize, Report |
| `meta.views[id=fetch].label` | Device fetch | Synchronize declarations |
| `meta.views[id=fetch].note` | Tokens, then the manifest, then only what the device does not have. | The device uses check-in requests to compare tokens, fetch the manifest and changed declarations, and report status. |
| `participants[id=engine].label` | ddm.Engine | Declarative management engine |
| `participants[id=engine].sublabel` | Handle · Tokens | Compute assigned state and sync tokens |
| `participants[id=ddmstore].label` | ddm.Store | Declarative state storage |
| `participants[id=ddmstore].sublabel` | snapshots · changes | Persist snapshots and pending changes |
| `participants[id=notifier].label` | ddmsync.Notifier | Change notifier |
| `participants[id=notifier].sublabel` | 2s window | Queue synchronization and request a wake |
| `participants[id=core].label` | service.Core | MDM service and adapters |
| `participants[id=core].sublabel` | Enqueue | Device-facing /mdm transport |
| `participants[id=device].label` | Device | Enrolled Apple device |
| `participants[id=device].sublabel` | check-in to /mdm | Fetch declarations and report status |

Retain: `messages[id=changerows].note`, `messages[id=kick].note`, `messages[id=enqueue].note`, `meta.views[id=change].label`, `meta.views[id=change].note`, `participants[id=admin].label`, `participants[id=admin].sublabel`, `segments[0].label`, `segments[1].label`, `segments[2].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Integrating declarative management](https://developer.apple.com/documentation/devicemanagement/integrating-declarative-management), [Declarative Management check-in](https://developer.apple.com/documentation/devicemanagement/declarative-management), [Declarations](https://developer.apple.com/documentation/devicemanagement/devicemanagement-declarations).

### flow-acme-attestation

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | The chain verifies to the Apple Enterprise Attestation Root | Challenge processing verifies the Apple attestation chain, freshness, required device identifiers, and configured admission policy. |
| `cards[0].items[1]` | The freshness code is derived from the challenge token | Finalization verifies the certificate signing request and checks that its public key matches the attested key. |
| `cards[0].title` | Attestation checks | Two verification stages |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | A chain from another authority, and a stale freshness code | Signed requests consume one-use replay nonces. A finalized order provides a certificate URL that the client retrieves using POST-as-GET. |
| `cards[1].items[1]` | A CSR for a key the attestation did not cover | SCEP and ACME identity issuance are independent of the HTTPS trust anchors distributed for enrollment endpoints. |
| `cards[1].title` | Rejected evidence | Certificate retrieval |
| `cards[2].dot` | [new field] | slate |
| `cards[2].items[0]` | [new field] | ACMECertificate — https://developer.apple.com/documentation/devicemanagement/acmecertificate |
| `cards[2].items[1]` | [new field] | Deploy Managed Device Attestation — https://support.apple.com/en-gb/guide/deployment/dep54e5ac1fd/web |
| `cards[2].items[2]` | [new field] | RFC 8555 — ACME — https://www.rfc-editor.org/rfc/rfc8555 |
| `cards[2].title` | [new field] | Public documentation |
| `messages[id=download].label` | PEM chain | Return the issued certificate chain |
| `messages[id=claim].label` | ClaimIdentifier, single use | Claim the enrollment identifier once |
| `messages[id=challenge].label` | POST the attestation object | Submit device-attest-01 evidence |
| `messages[id=chain].label` | verify to the Apple root | Verify Apple chain and freshness |
| `messages[id=binding].label` | serial and UDID match the binding | Compare required device identifiers |
| `messages[id=binding].to` | store | acme |
| `messages[id=authorize].label` | evaluate admission policy | Evaluate the configured admission policy |
| `messages[id=sign].label` | Sign with a PermanentIdentifier | Issue certificate with PermanentIdentifier |
| `meta.views[id=attest].label` | Attestation | Verify evidence and key |
| `meta.views[id=attest].note` | Verification checks the chain, freshness, requested key and bound device. | Challenge processing verifies evidence and policy; finalization verifies that the CSR uses the attested key. |
| `participants[id=device].label` | Device | Apple device |
| `participants[id=device].sublabel` | Secure Enclave key | Creates a hardware-bound identity key |
| `participants[id=acme].label` | acme.Server | ACME server |
| `participants[id=acme].sublabel` | /acme | Coordinates account, order, and issuance |
| `participants[id=store].label` | acme.Store | ACME state store |
| `participants[id=store].sublabel` | nonces · orders | Persists nonces, identifiers, and orders |
| `participants[id=attest].label` | acme/attest | Attestation verifier |
| `participants[id=attest].sublabel` | chain + properties | Checks Apple evidence and freshness |
| `participants[id=policy].label` | Policy | Certificate admission policy |
| `participants[id=policy].sublabel` | any · dep · sip | Accept or reject the attested device |
| `participants[id=ca].label` | ca.Local | Identity certificate authority |
| `participants[id=ca].sublabel` | enrollment CA | Signs an approved CSR |

Retain: `messages[id=directory].label`, `messages[id=account].label`, `messages[id=account].note`, `messages[id=order].label`, `messages[id=order].note`, `messages[id=finalize].label`, `messages[id=finalize].note`, `meta.title`, `meta.views[id=order].label`, `meta.views[id=order].note`, `segments[0].label`, `segments[1].label`, `segments[2].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [ACMECertificate](https://developer.apple.com/documentation/devicemanagement/acmecertificate), [Deploy Managed Device Attestation](https://support.apple.com/en-gb/guide/deployment/dep54e5ac1fd/web), [RFC 8555 — ACME](https://www.rfc-editor.org/rfc/rfc8555).

### flow-scep-issuance

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].items[0]` | Traditional enrollment supports static or HMAC challenges | The profile selects the SCEP endpoint and challenge. Static and HMAC challenges are traditional strategies; account and replacement credentials bind issuance to an authorized request. |
| `cards[0].items[1]` | Account enrollment uses CSR-bound credentials and trusted issuance registration | A successful challenge permits CSR validation and signing; it does not itself issue the certificate. |
| `cards[0].title` | Challenge strategies | Issuance authorization |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | SCEP protocol refusal uses signed CertRep with failInfo; malformed HTTP input can fail earlier | Successful issuance returns the device identity in a signed CertRep. Protocol rejection instead returns failInfo; malformed transport input may fail earlier. |
| `cards[1].items[1]` | The device reads failInfo to learn why | An accepted renewal proves possession with the existing certificate and can bypass the initial challenge. Configured certificate-status checks still apply. |
| `cards[1].title` | SCEP protocol errors | Success, failure, and renewal |
| `cards[2].dot` | [new field] | amber |
| `cards[2].items[0]` | [new field] | For account enrollment and controlled replacement, trusted issuance records the certificate association before the certificate is returned. HTTPS trust is configured separately. |
| `cards[2].title` | [new field] | Enrollment association |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | SCEP — https://developer.apple.com/documentation/devicemanagement/scep |
| `cards[3].items[1]` | [new field] | RFC 8894 — SCEP — https://www.rfc-editor.org/rfc/rfc8894 |
| `cards[3].items[2]` | [new field] | Deploying device management enrollment profiles — https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles |
| `cards[3].title` | [new field] | Public documentation |
| `messages[id=caps-back].label` | POSTPKIOperation, SHA-256, Renewal | Return supported SCEP capabilities |
| `messages[id=refused].label` | or a signed CertRep with failInfo | Failure: signed CertRep with failInfo |
| `messages[id=refused].note` | a refusal is still delivered with 200; the device reads failInfo | This is an alternative to success. Malformed input can fail before a signed protocol response is available. |
| `messages[id=cacert-back].label` | the CA certificate | Return CA/RA certificate material |
| `messages[id=pki].label` | PKIOperation, PKCSReq | Submit signed and encrypted PKCSReq |
| `messages[id=verify].label` | challengePassword from the CSR | Validate the CSR challenge password |
| `messages[id=verify-back].label` | derived from the common name | Authorization accepted or rejected |
| `messages[id=sign].label` | Sign(CSR, policy) | Validate CSR policy and issue certificate |
| `messages[id=certrep].label` | CertRep: success or failure | Success: return signed CertRep |
| `participants[id=device].label` | Device | Apple device |
| `participants[id=device].sublabel` | from the profile payload | Follows the profile’s SCEP payload |
| `participants[id=scep].label` | scep.Server | SCEP server |
| `participants[id=scep].sublabel` | /scep | Validates the certificate request |
| `participants[id=challenge].label` | Challenge | Issuance authorization |
| `participants[id=challenge].sublabel` | static · HMAC · one-time | Configured shared, HMAC, or one-use credential |
| `participants[id=ca].label` | ca.Local | Identity certificate authority |
| `participants[id=ca].sublabel` | enrollment CA | Signs an approved certificate request |

Retain: `messages[id=caps].label`, `messages[id=cacert].label`, `messages[id=cert].label`, `meta.title`, `meta.views[id=discover].label`, `meta.views[id=discover].note`, `meta.views[id=issue].label`, `meta.views[id=issue].note`, `segments[0].label`, `segments[1].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [SCEP](https://developer.apple.com/documentation/devicemanagement/scep), [RFC 8894 — SCEP](https://www.rfc-editor.org/rfc/rfc8894), [Deploying device management enrollment profiles](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles).

### lifecycle-command

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].dot` | amber | cyan |
| `cards[0].items[0]` | The command stays queued behind a backoff that starts at 30s and doubles to a one hour cap | A NotNow response schedules a repository-defined retry delay: 30 seconds, doubling to a one-hour cap. |
| `cards[0].items[1]` | A connect carrying NotNow skips not-now commands whose backoff has not elapsed | That same connection skips all commands already in NotNow, even if their delay has elapsed. A later eligible device poll can retry them. |
| `cards[0].title` | NotNow retry | Deferred commands |
| `cards[1].dot` | rose | emerald |
| `cards[1].items[0]` | Acknowledged is terminal, so an acknowledged command is never cleared | Sent means the server selected a command for delivery; it does not prove execution. A sent command can be selected again until a terminal result is recorded. |
| `cards[1].items[1]` | Clear and re-enrollment sweep the non-terminal states only | Acknowledged, Error, and CommandFormatError are terminal outcomes. Clear affects non-terminal commands, including pending and NotNow. |
| `cards[1].title` | Terminal | Delivery and terminal outcomes |
| `cards[2].dot` | [new field] | amber |
| `cards[2].items[0]` | [new field] | Controlled enrollment-profile replacement tracks its InstallProfile command in the replacement record. This diagram describes the ordinary command queue. |
| `cards[2].title` | [new field] | Separate replacement path |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Sending MDM commands to a device — https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device |
| `cards[3].items[1]` | [new field] | Handling NotNow status responses — https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses |
| `cards[3].title` | [new field] | Public documentation |
| `meta.views[id=happy].label` | Delivered | Deliver and acknowledge |
| `meta.views[id=happy].note` | Enqueued, handed to the device on its next connect, then acknowledged. | The server selects a command on a device poll. Only a stored terminal result establishes the command outcome. |
| `meta.views[id=notnow].label` | NotNow | Defer and retry |
| `meta.views[id=notnow].note` | A NotNow leaves the command queued behind a doubling backoff. | The current NotNow connection skips all deferred commands. Later eligible polls retry after the repository’s backoff delay. |
| `states[id=pending].sublabel` | enqueued | Stored and waiting for a device poll |
| `states[id=sent].sublabel` | handed to the device | Selected for an HTTP response |
| `states[id=acknowledged].sublabel` | typed response stored | Device result stored successfully |
| `states[id=notnow].sublabel` | temporarily deferred | Deferred until a later eligible poll |
| `states[id=cleared].sublabel` | Clear or re-enrollment | Removed from future delivery |
| `states[id=errored].label` | Error | Command failed |
| `states[id=errored].sublabel` | ErrorChain stored | Error or CommandFormatError result |
| `transitions[id=retry].label` | backoff elapsed | Later eligible poll after backoff |
| `transitions[id=fail].label` | Error | Error or CommandFormatError |
| `transitions[id=clear].label` | while non-terminal | Clear or ordinary enrollment reset |

Retain: `lanes[id=main].label`, `lanes[id=retry].label`, `lanes[id=terminal].label`, `meta.title`, `states[id=pending].label`, `states[id=sent].label`, `states[id=acknowledged].label`, `states[id=notnow].label`, `states[id=notnow].tag`, `states[id=cleared].label`, `states[id=cleared].tag`, `states[id=errored].tag`, `transitions[id=take].label`, `transitions[id=ack].label`, `transitions[id=decline].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Sending MDM commands to a device](https://developer.apple.com/documentation/devicemanagement/sending-mdm-commands-to-a-device), [Handling NotNow status responses](https://developer.apple.com/documentation/devicemanagement/handling-notnow-status-responses).

### lifecycle-enrollment

| Field (stable ID where available) | Current | Proposed |
|---|---|---|
| `cards[0].dot` | emerald | cyan |
| `cards[0].items[0]` | Authenticate upserts the enrollment and pins the certificate fingerprint | Authenticate creates or resets the device-channel record and associates its certificate according to pinning policy. TokenUpdate enables it. |
| `cards[0].items[1]` | TokenUpdate stores the push token and is what actually enables the enrollment | Changed certificates require re-enrollment policy approval: the library allows by default and the reference server denies ordinary rotation. |
| `cards[0].title` | Enrollment activation | Initial activation and ordinary reset |
| `cards[1].dot` | amber | emerald |
| `cards[1].items[0]` | Changed certificates require policy: library allows by default; reference server denies | An authorized replacement retains the active identity while collecting the candidate’s Authenticate, device TokenUpdate, and InstallProfile acknowledgement. |
| `cards[1].items[1]` | The upsert clears push info, unlock and bootstrap tokens, the pin, and the pending queue | Only successful completion promotes the candidate atomically. Failed, cancelled, and expired attempts keep the working enrollment, queue, escrow, and user channels. |
| `cards[1].title` | Re-enrollment | Controlled profile replacement |
| `cards[2].dot` | [new field] | amber |
| `cards[2].items[0]` | [new field] | CheckOut disables the enrollment while retaining its record; a later accepted enrollment can reactivate it. Declarative cleanup runs through the service hook. |
| `cards[2].items[1]` | [new field] | User channels are created by TokenUpdate under an existing device enrollment, with UserAuthenticate when required. They do not send device-channel Authenticate. |
| `cards[2].title` | [new field] | Checkout and user channels |
| `cards[3].dot` | [new field] | slate |
| `cards[3].items[0]` | [new field] | Check-in — https://developer.apple.com/documentation/devicemanagement/check-in |
| `cards[3].items[1]` | [new field] | Deploying device management enrollment profiles — https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles |
| `cards[3].items[2]` | [new field] | MDM payload — https://developer.apple.com/documentation/devicemanagement/mdm |
| `cards[3].title` | [new field] | Public documentation |
| `meta.title` | Enrollment Lifecycle | Device-channel Enrollment Lifecycle |
| `meta.views[id=enroll].label` | Enrollment activation | Activate a device enrollment |
| `meta.views[id=enroll].note` | Authenticate pins the identity; only TokenUpdate makes the enrollment usable. | Authenticate records the device identity; TokenUpdate supplies push details and enables the device channel. |
| `meta.views[id=recover].label` | Re-enrollment | Ordinary re-enrollment |
| `meta.views[id=recover].note` | A new identity clears tokens and the pending queue, then re-pins. | An accepted ordinary Authenticate resets enrollment state. Controlled replacement preserves it through a separate handshake. |
| `states[id=unknown].label` | Unknown | Not enrolled |
| `states[id=unknown].sublabel` | no enrollment row | No device enrollment record exists |
| `states[id=authenticated].label` | Authenticated | Awaiting push details |
| `states[id=authenticated].sublabel` | certificate pinned | Identity associated with enrollment |
| `states[id=enabled].sublabel` | push token stored | TokenUpdate supplies usable push details |
| `states[id=reenrolled].label` | Re-enrolled | Ordinary enrollment reset |
| `states[id=reenrolled].sublabel` | tokens and queue cleared | Tokens and non-terminal queue cleared |
| `states[id=denied].label` | Reuse denied | Identity request rejected |
| `states[id=denied].sublabel` | held by another enrollment | Existing enrollment state is unchanged |
| `states[id=checkedout].label` | Checked out | Disabled after CheckOut |
| `states[id=checkedout].sublabel` | DDM state cleared | Record retained; device no longer active |
| `transitions[id=rotate].label` | new identity | Accepted ordinary Authenticate |
| `transitions[id=repin].label` | re-pinned | Associate accepted identity |
| `transitions[id=reuse].label` | held elsewhere | Reject identity reuse request |

Retain: `lanes[id=main].label`, `lanes[id=rotation].label`, `lanes[id=terminal].label`, `states[id=enabled].label`, `states[id=reenrolled].tag`, `states[id=denied].tag`, `states[id=checkedout].tag`, `transitions[id=auth].label`, `transitions[id=token].label`, `transitions[id=checkout].label`. Exact retained values and original JSON pointers are in the machine-readable manifest.

Public links: [Check-in](https://developer.apple.com/documentation/devicemanagement/check-in), [Deploying device management enrollment profiles](https://developer.apple.com/documentation/devicemanagement/deploying-device-management-enrollment-profiles), [MDM payload](https://developer.apple.com/documentation/devicemanagement/mdm).
